// Command verify is a one-shot smoke test against a running API
// instance. It checks representative success and failure cases and exits
// non-zero if any expectation breaks. Docker Compose runs it once after
// the API service starts.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	"github.com/stretchr/testify/assert"
)

type checkT struct {
	failed bool
}

func (t *checkT) Errorf(format string, args ...any) {
	log.Printf("FAIL: "+format, args...)
	t.failed = true
}

func main() {
	base := os.Getenv("API_URL")
	if base == "" {
		port := os.Getenv("API_PORT")
		if port == "" {
			port = "8080"
		}
		base = "http://localhost:" + port
	}

	client := &http.Client{Timeout: 5 * time.Second}
	waitForAPI(client, base+"/healthz")

	t := &checkT{}
	a := assert.New(t)

	// 1. A valid program: two tools, three holes, negative coordinates.
	valid := "M48\n" +
		"METRIC\n" +
		"T01C0.300\n" +
		"T02C1.500\n" +
		"%\n" +
		"T01\n" +
		"X1.000Y2.000\n" +
		"X-0.500Y2.000\n" +
		"T02\n" +
		"X10.000Y-3.250\n" +
		"M30\n"
	resp, body := post(client, base+"/drill-files/statistics", "text/plain", valid)
	a.Equal(http.StatusOK, resp.StatusCode, "valid file must return 200: %s", body)
	var report map[string]any
	_ = json.Unmarshal(body, &report)
	a.EqualValues(float64(3), report["total_holes"])
	a.EqualValues("T01", report["tools"].([]any)[0].(map[string]any)["tool"])
	a.EqualValues(float64(2), report["tools"].([]any)[0].(map[string]any)["holes"])
	a.Equal("-0.500", report["min_x"])
	a.Equal("-3.250", report["min_y"])
	a.Equal("10.000", report["max_x"])
	a.Equal("2.000", report["max_y"])

	// 2. Earliest error wins: undefined tool on line 7.
	undef := "M48\nMETRIC\nT01C0.300\n%\nT02\nX1Y1\nM30\n"
	resp, body = post(client, base+"/drill-files/statistics", "text/plain", undef)
	expect422(a, resp, body, "UNDEFINED_TOOL", 5)

	// 3. Malformed number lexicon beats a later undefined reference.
	badNum := "M48\nMETRIC\nT01C0.300\n%\nT01\nX01Y1\nM30\n"
	resp, body = post(client, base+"/drill-files/statistics", "text/plain", badNum)
	expect422(a, resp, body, "INVALID_NUMBER", 6)

	// 4. Duplicate tool definition.
	dup := "M48\nMETRIC\nT01C0.300\nT01C0.400\n%\nT01\nX1Y1\nM30\n"
	resp, body = post(client, base+"/drill-files/statistics", "text/plain", dup)
	expect422(a, resp, body, "DUPLICATE_TOOL", 4)

	// 5. Wrong first line.
	order := "X1Y1\n"
	resp, body = post(client, base+"/drill-files/statistics", "text/plain", order)
	expect422(a, resp, body, "LINE_ORDER", 1)

	// 6. No drill records -> NO_HOLES at the M30 line.
	noHoles := "M48\nMETRIC\nT01C0.300\n%\nT01\nM30\n"
	resp, body = post(client, base+"/drill-files/statistics", "text/plain", noHoles)
	expect422(a, resp, body, "NO_HOLES", 6)

	// 7. Wrong media type is rejected before parsing.
	resp, body = post(client, base+"/drill-files/statistics", "application/json", valid)
	a.Equal(http.StatusUnsupportedMediaType, resp.StatusCode, "JSON body must return 415: %s", body)

	// 8. min_clearance satisfied: the statistics are identical to the
	//    no-parameter response.
	resp, body = post(client, base+"/drill-files/statistics?min_clearance=0.5", "text/plain", valid)
	a.Equal(http.StatusOK, resp.StatusCode, "valid clearance must return 200: %s", body)
	var cleared map[string]any
	_ = json.Unmarshal(body, &cleared)
	a.EqualValues(float64(3), cleared["total_holes"])
	a.Equal("-0.500", cleared["min_x"])
	a.Equal("2.000", cleared["max_y"])

	// 9. Critical tangency passes: distance == r + r + clearance.
	tangent := "M48\nMETRIC\nT01C1.000\n%\nT01\nX0Y0\nX1.500Y0\nM30\n"
	resp, body = post(client, base+"/drill-files/statistics?min_clearance=0.5", "text/plain", tangent)
	a.Equal(http.StatusOK, resp.StatusCode, "tangent holes must pass: %s", body)

	// 10. Clearance conflict: 422 HOLE_CLEARANCE locating both lines.
	conflict := "M48\nMETRIC\nT01C1.000\n%\nT01\nX0Y0\nX1Y0\nM30\n"
	resp, body = post(client, base+"/drill-files/statistics?min_clearance=0.5", "text/plain", conflict)
	a.Equal(http.StatusUnprocessableEntity, resp.StatusCode, "conflict must return 422: %s", body)
	var cerr map[string]any
	_ = json.Unmarshal(body, &cerr)
	a.Equal("HOLE_CLEARANCE", cerr["code"], "body: %s", body)
	a.EqualValues(float64(7), cerr["line"], "body: %s", body)
	a.EqualValues(float64(6), cerr["conflict_line"], "body: %s", body)

	// 11. Invalid min_clearance is a 400 client error and is never
	//     masked as a file error, even when the body itself is invalid.
	resp, body = post(client, base+"/drill-files/statistics?min_clearance=-1", "text/plain", "GARBAGE\n")
	a.Equal(http.StatusBadRequest, resp.StatusCode, "invalid clearance must return 400: %s", body)
	var badParam map[string]any
	_ = json.Unmarshal(body, &badParam)
	a.Equal("INVALID_CLEARANCE", badParam["code"], "body: %s", body)

	// 12. Exact half-turn symmetry about (0,0): T01 holes at
	//     (1,2)/(-1,-2), T02 holes at (5,0)/(-5,0). The statistics are
	//     identical to the no-parameter response.
	symmetric := "M48\n" +
		"METRIC\n" +
		"T01C0.300\n" +
		"T02C1.500\n" +
		"%\n" +
		"T01\n" +
		"X1.000Y2.000\n" +
		"X-1.000Y-2.000\n" +
		"T02\n" +
		"X5.000Y0\n" +
		"X-5.000Y0\n" +
		"M30\n"
	resp, body = post(client, base+"/drill-files/statistics?symmetry_center=0,0", "text/plain", symmetric)
	a.Equal(http.StatusOK, resp.StatusCode, "symmetric pattern must return 200: %s", body)
	var symOK map[string]any
	_ = json.Unmarshal(body, &symOK)
	a.EqualValues(float64(4), symOK["total_holes"], "body: %s", body)
	a.Equal("-5.000", symOK["min_x"], "body: %s", body)
	a.Equal("5.000", symOK["max_x"], "body: %s", body)

	// 13. Count imbalance: the T02 class has one hole at (5,0) but no
	//     rotated partner at (-5,0), so the whole file is rejected with
	//     422 ASYMMETRIC_PATTERN and the uncovered source line (10) is
	//     listed in body line order.
	imbalanced := "M48\n" +
		"METRIC\n" +
		"T01C0.300\n" +
		"T02C1.500\n" +
		"%\n" +
		"T01\n" +
		"X1.000Y2.000\n" +
		"X-1.000Y-2.000\n" +
		"T02\n" +
		"X5.000Y0\n" +
		"M30\n"
	resp, body = post(client, base+"/drill-files/statistics?symmetry_center=0,0", "text/plain", imbalanced)
	a.Equal(http.StatusUnprocessableEntity, resp.StatusCode, "asymmetric pattern must return 422: %s", body)
	var asym map[string]any
	_ = json.Unmarshal(body, &asym)
	a.Equal("ASYMMETRIC_PATTERN", asym["code"], "body: %s", body)
	a.EqualValues(float64(10), asym["line"], "body: %s", body)
	a.Equal([]any{float64(10)}, asym["uncovered_lines"], "body: %s", body)

	// 14. A lone hole exactly at the center is a fixed point of the
	//     half-turn and therefore symmetric on its own; it must pass.
	selfMap := "M48\nMETRIC\nT01C0.300\n%\nT01\nX0Y0\nM30\n"
	resp, body = post(client, base+"/drill-files/statistics?symmetry_center=0,0", "text/plain", selfMap)
	a.Equal(http.StatusOK, resp.StatusCode, "center fixed point must return 200: %s", body)
	var fixed map[string]any
	_ = json.Unmarshal(body, &fixed)
	a.EqualValues(float64(1), fixed["total_holes"], "body: %s", body)

	// 14b. The fixed point never supplies a partner: adding a lone
	//      off-center hole still fails and reports only that source line.
	plusUnbalanced := "M48\nMETRIC\nT01C0.300\n%\nT01\nX0Y0\nX1Y0\nM30\n"
	resp, body = post(client, base+"/drill-files/statistics?symmetry_center=0,0", "text/plain", plusUnbalanced)
	a.Equal(http.StatusUnprocessableEntity, resp.StatusCode, "unbalanced off-center hole must return 422: %s", body)
	var plusAsym map[string]any
	_ = json.Unmarshal(body, &plusAsym)
	a.Equal("ASYMMETRIC_PATTERN", plusAsym["code"], "body: %s", body)
	a.EqualValues(float64(7), plusAsym["line"], "body: %s", body)
	a.Equal([]any{float64(7)}, plusAsym["uncovered_lines"], "body: %s", body)

	// 15. Audit priority: a file error is reported before the symmetry
	//     audit even runs.
	fileErr := "M48\nMETRIC\nT01C0.300\n%\nT02\nX1Y1\nM30\n"
	resp, body = post(client, base+"/drill-files/statistics?symmetry_center=0,0", "text/plain", fileErr)
	expect422(a, resp, body, "UNDEFINED_TOOL", 5)

	// 16. Audit priority: a clearance conflict aborts before the
	//     symmetry audit. Two coincident holes off-center at (1,0) fail
	//     the spacing audit (lines 6/7) and are also unbalanced under
	//     the half-turn about the origin; the clearance result wins.
	resp, body = post(client,
		base+"/drill-files/statistics?min_clearance=0.5&symmetry_center=0,0",
		"text/plain", "M48\nMETRIC\nT01C1.000\n%\nT01\nX1Y0\nX1Y0\nM30\n")
	a.Equal(http.StatusUnprocessableEntity, resp.StatusCode, "clearance must precede symmetry: %s", body)
	var clearFirst map[string]any
	_ = json.Unmarshal(body, &clearFirst)
	a.Equal("HOLE_CLEARANCE", clearFirst["code"], "body: %s", body)
	a.EqualValues(float64(7), clearFirst["line"], "body: %s", body)
	a.EqualValues(float64(6), clearFirst["conflict_line"], "body: %s", body)

	// 17. Invalid symmetry_center is a 400 client error, rejected before
	//     the body is read (the body here is garbage on purpose) — and
	//     the parameter may not be repeated.
	resp, body = post(client, base+"/drill-files/statistics?symmetry_center=1", "text/plain", "GARBAGE\n")
	a.Equal(http.StatusBadRequest, resp.StatusCode, "invalid center must return 400: %s", body)
	var badCenter map[string]any
	_ = json.Unmarshal(body, &badCenter)
	a.Equal("INVALID_SYMMETRY", badCenter["code"], "body: %s", body)

	resp, body = post(client,
		base+"/drill-files/statistics?symmetry_center=0,0&symmetry_center=1,1",
		"text/plain", "GARBAGE\n")
	a.Equal(http.StatusBadRequest, resp.StatusCode, "repeated center must return 400: %s", body)
	var dupCenter map[string]any
	_ = json.Unmarshal(body, &dupCenter)
	a.Equal("INVALID_SYMMETRY", dupCenter["code"], "body: %s", body)

	// 18. Panel audit: three instances of the asymmetric L template at
	//     angle 0, offsets (10,5), (20,3), (30,8); instances come back
	//     sorted by anchor coordinates.
	auditTemplate := "M48\nMETRIC\nT01C0.500\n%\nT01\nX0Y0\nX2Y0\nX0Y1\nM30\n"
	panel3 := "M48\nMETRIC\nT01C0.500\n%\nT01\n" +
		"X10Y5\nX12Y5\nX10Y6\n" +
		"X20Y3\nX22Y3\nX20Y4\n" +
		"X30Y8\nX32Y8\nX30Y9\n" +
		"M30\n"
	resp, body = postMultipart(client, base+"/drill-files/panel-audit",
		map[string]string{"template": auditTemplate, "panel": panel3})
	a.Equal(http.StatusOK, resp.StatusCode, "multi-instance panel must return 200: %s", body)
	var audit map[string]any
	_ = json.Unmarshal(body, &audit)
	layouts, _ := audit["layouts"].([]any)
	a.Equal(1, len(layouts), "body: %s", body)
	if len(layouts) == 1 {
		layout := layouts[0].(map[string]any)
		a.EqualValues(0, layout["angle"], "body: %s", body)
		instances, _ := layout["instances"].([]any)
		a.Equal(3, len(instances), "body: %s", body)
		wantOffsets := [][2]string{{"10.000", "5.000"}, {"20.000", "3.000"}, {"30.000", "8.000"}}
		for i, inst := range instances {
			m := inst.(map[string]any)
			a.Equal(wantOffsets[i][0], m["offset_x"], "instance %d, body: %s", i, body)
			a.Equal(wantOffsets[i][1], m["offset_y"], "instance %d, body: %s", i, body)
			a.Equal(wantOffsets[i][0], m["anchor_x"], "instance %d, body: %s", i, body)
			a.Equal(wantOffsets[i][1], m["anchor_y"], "instance %d, body: %s", i, body)
		}
	}

	// 19. Rotation recognition: the panel is the template rotated 90
	//     degrees counterclockwise and translated by (10,10).
	panel90 := "M48\nMETRIC\nT01C0.500\n%\nT01\nX10Y10\nX10Y12\nX9Y10\nM30\n"
	resp, body = postMultipart(client, base+"/drill-files/panel-audit",
		map[string]string{"template": auditTemplate, "panel": panel90})
	a.Equal(http.StatusOK, resp.StatusCode, "rotated panel must return 200: %s", body)
	var rotated map[string]any
	_ = json.Unmarshal(body, &rotated)
	rotLayouts, _ := rotated["layouts"].([]any)
	a.Equal(1, len(rotLayouts), "body: %s", body)
	if len(rotLayouts) == 1 {
		layout := rotLayouts[0].(map[string]any)
		a.EqualValues(90, layout["angle"], "body: %s", body)
		instances, _ := layout["instances"].([]any)
		a.Equal(1, len(instances), "body: %s", body)
		if len(instances) == 1 {
			inst := instances[0].(map[string]any)
			a.Equal("10.000", inst["offset_x"], "body: %s", body)
			a.Equal("10.000", inst["offset_y"], "body: %s", body)
			a.Equal("9.000", inst["anchor_x"], "body: %s", body)
			a.Equal("10.000", inst["anchor_y"], "body: %s", body)
		}
	}

	// 20. Overlapping duplicate holes conserve counts: the template
	//     drills (0,0) twice and (1,0) once; the panel holds two
	//     overlapping copies at offsets (0,0) and (1,0), so (1,0)
	//     appears 1+2 = 3 times.
	dupTemplate := "M48\nMETRIC\nT01C0.500\n%\nT01\nX0Y0\nX0Y0\nX1Y0\nM30\n"
	dupPanel := "M48\nMETRIC\nT01C0.500\n%\nT01\nX0Y0\nX0Y0\nX1Y0\nX1Y0\nX1Y0\nX2Y0\nM30\n"
	resp, body = postMultipart(client, base+"/drill-files/panel-audit",
		map[string]string{"template": dupTemplate, "panel": dupPanel})
	a.Equal(http.StatusOK, resp.StatusCode, "overlapping duplicates must return 200: %s", body)
	var overlap map[string]any
	_ = json.Unmarshal(body, &overlap)
	ovLayouts, _ := overlap["layouts"].([]any)
	a.Equal(1, len(ovLayouts), "body: %s", body)
	if len(ovLayouts) == 1 {
		instances, _ := ovLayouts[0].(map[string]any)["instances"].([]any)
		a.Equal(2, len(instances), "body: %s", body)
		if len(instances) == 2 {
			a.Equal("0.000", instances[0].(map[string]any)["offset_x"], "body: %s", body)
			a.Equal("1.000", instances[1].(map[string]any)["offset_x"], "body: %s", body)
		}
	}

	// 21. Extra hole: one clean instance at (10,5) plus an extra hole at
	//     (99,99) on panel line 8. All four rotations fail with 422
	//     PANEL_PATTERN_MISMATCH and per-angle evidence.
	twoHoleTemplate := "M48\nMETRIC\nT01C0.500\n%\nT01\nX0Y0\nX2Y0\nM30\n"
	extraPanel := "M48\nMETRIC\nT01C0.500\n%\nT01\nX10Y5\nX12Y5\nX99Y99\nM30\n"
	resp, body = postMultipart(client, base+"/drill-files/panel-audit",
		map[string]string{"template": twoHoleTemplate, "panel": extraPanel})
	a.Equal(http.StatusUnprocessableEntity, resp.StatusCode, "extra hole must return 422: %s", body)
	var mismatch map[string]any
	_ = json.Unmarshal(body, &mismatch)
	a.Equal("PANEL_PATTERN_MISMATCH", mismatch["code"], "body: %s", body)
	failures, _ := mismatch["failures"].([]any)
	a.Equal(4, len(failures), "body: %s", body)
	if len(failures) == 4 {
		f0 := failures[0].(map[string]any)
		a.EqualValues(0, f0["angle"], "body: %s", body)
		a.EqualValues(8, f0["anchor_line"], "body: %s", body)
		a.EqualValues(7, f0["missing_line"], "body: %s", body)
		a.Equal("99.000", f0["offset_x"], "body: %s", body)
		a.Equal("99.000", f0["offset_y"], "body: %s", body)
	}

	// 22. Missing hole: the panel holds only the first hole of the
	//     instance at (10,5); again all four rotations fail.
	missingPanel := "M48\nMETRIC\nT01C0.500\n%\nT01\nX10Y5\nM30\n"
	resp, body = postMultipart(client, base+"/drill-files/panel-audit",
		map[string]string{"template": twoHoleTemplate, "panel": missingPanel})
	a.Equal(http.StatusUnprocessableEntity, resp.StatusCode, "missing hole must return 422: %s", body)
	var missing map[string]any
	_ = json.Unmarshal(body, &missing)
	a.Equal("PANEL_PATTERN_MISMATCH", missing["code"], "body: %s", body)
	missFailures, _ := missing["failures"].([]any)
	a.Equal(4, len(missFailures), "body: %s", body)
	if len(missFailures) == 4 {
		f0 := missFailures[0].(map[string]any)
		a.EqualValues(0, f0["angle"], "body: %s", body)
		a.EqualValues(6, f0["anchor_line"], "body: %s", body)
		a.EqualValues(7, f0["missing_line"], "body: %s", body)
	}

	// 23. An invalid file keeps its original error code and line, tagged
	//     with its part; the template is judged first.
	resp, body = postMultipart(client, base+"/drill-files/panel-audit",
		map[string]string{"template": "GARBAGE\n", "panel": "GARBAGE\n"})
	a.Equal(http.StatusUnprocessableEntity, resp.StatusCode, "invalid template must return 422: %s", body)
	var badTemplate map[string]any
	_ = json.Unmarshal(body, &badTemplate)
	a.Equal("LINE_ORDER", badTemplate["code"], "body: %s", body)
	a.EqualValues(1, badTemplate["line"], "body: %s", body)
	a.Equal("template", badTemplate["part"], "body: %s", body)

	resp, body = postMultipart(client, base+"/drill-files/panel-audit",
		map[string]string{"template": twoHoleTemplate, "panel": "M48\nMETRIC\nT01C0.300\n%\nT02\nX1Y1\nM30\n"})
	a.Equal(http.StatusUnprocessableEntity, resp.StatusCode, "invalid panel must return 422: %s", body)
	var badPanel map[string]any
	_ = json.Unmarshal(body, &badPanel)
	a.Equal("UNDEFINED_TOOL", badPanel["code"], "body: %s", body)
	a.EqualValues(5, badPanel["line"], "body: %s", body)
	a.Equal("panel", badPanel["part"], "body: %s", body)

	// 24. The panel audit only accepts multipart/form-data.
	resp, body = post(client, base+"/drill-files/panel-audit", "text/plain", twoHoleTemplate)
	a.Equal(http.StatusUnsupportedMediaType, resp.StatusCode, "text body must return 415: %s", body)

	if t.failed {
		os.Exit(1)
	}
	log.Println("verify: all checks passed")
}

func waitForAPI(client *http.Client, url string) {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	log.Fatalf("verify: API at %s did not become ready", url)
}

func post(client *http.Client, url, contentType, bodyText string) (*http.Response, []byte) {
	resp, err := client.Post(url, contentType, bytes.NewBufferString(bodyText))
	if err != nil {
		log.Fatalf("verify: request failed: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

// postMultipart uploads the given parts as multipart/form-data. Parts
// are written in template-then-panel order for reproducibility.
func postMultipart(client *http.Client, url string, parts map[string]string) (*http.Response, []byte) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, name := range []string{"template", "panel"} {
		content, ok := parts[name]
		if !ok {
			continue
		}
		fw, err := w.CreateFormFile(name, name+".drl")
		if err != nil {
			log.Fatalf("verify: multipart writer failed: %v", err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			log.Fatalf("verify: multipart writer failed: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		log.Fatalf("verify: multipart writer failed: %v", err)
	}
	resp, err := client.Post(url, w.FormDataContentType(), &buf)
	if err != nil {
		log.Fatalf("verify: request failed: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func expect422(a *assert.Assertions, resp *http.Response, body []byte, code string, line int) {
	a.Equal(http.StatusUnprocessableEntity, resp.StatusCode,
		"%s must return 422, got: %s", code, body)
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		a.NoError(err, fmt.Sprintf("error body: %s", body))
		return
	}
	a.Equal(code, got["code"], "body: %s", body)
	a.EqualValues(float64(line), got["line"], "body: %s", body)
}
