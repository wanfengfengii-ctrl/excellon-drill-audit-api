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
