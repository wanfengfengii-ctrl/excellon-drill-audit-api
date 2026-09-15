package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"drillapi/internal/api"
)

func post(t *testing.T, r http.Handler, contentType, body string) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	return postPath(t, r, "/drill-files/statistics", contentType, body)
}

func postPath(t *testing.T, r http.Handler, path, contentType, body string) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w, w.Body.Bytes()
}

func TestHealthz(t *testing.T) {
	r := api.Router()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "ok", w.Body.String())
}

func TestStatistics_Valid(t *testing.T) {
	body := "M48\nMETRIC\nT01C0.300\nT02C1.500\n%\nT01\nX1.000Y2.000\nX-0.500Y2.000\nT02\nX10.000Y-3.250\nM30\n"
	w, raw := post(t, api.Router(), "text/plain; charset=utf-8", body)

	require.Equal(t, http.StatusOK, w.Code, string(raw))
	assert.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"))

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.EqualValues(t, 3, got["total_holes"])
	assert.Equal(t, "-0.500", got["min_x"])
	assert.Equal(t, "10.000", got["max_x"])

	tools := got["tools"].([]any)
	assert.Equal(t, "T01", tools[0].(map[string]any)["tool"])
	assert.EqualValues(t, 2, tools[0].(map[string]any)["holes"])
}

func TestStatistics_InvalidFile422(t *testing.T) {
	// Undefined tool T02 on line 5; no partial statistics are returned.
	body := "M48\nMETRIC\nT01C0.300\n%\nT02\nX1Y1\nM30\n"
	w, raw := post(t, api.Router(), "text/plain", body)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, string(raw))

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "UNDEFINED_TOOL", got["code"])
	assert.EqualValues(t, 5, got["line"])
	assert.NotContains(t, string(raw), "total_holes")
}

func TestStatistics_AllErrorCodes(t *testing.T) {
	cases := []struct {
		code string
		body string
	}{
		{"LINE_ORDER", "GARBAGE\n"},
		{"INVALID_NUMBER", "M48\nMETRIC\nT01C0\n%\nT01\nX1Y1\nM30\n"},
		{"DUPLICATE_TOOL", "M48\nMETRIC\nT01C0.1\nT01C0.2\n%\nT01\nX1Y1\nM30\n"},
		{"UNDEFINED_TOOL", "M48\nMETRIC\nT01C0.1\n%\nT09\nX1Y1\nM30\n"},
		{"NO_HOLES", "M48\nMETRIC\nT01C0.1\n%\nM30\n"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			w, raw := post(t, api.Router(), "text/plain", tc.body)
			require.Equal(t, http.StatusUnprocessableEntity, w.Code, string(raw))
			var got map[string]any
			require.NoError(t, json.Unmarshal(raw, &got))
			assert.Equal(t, tc.code, got["code"])
		})
	}
}

func TestStatistics_UnsupportedMediaType(t *testing.T) {
	w, raw := post(t, api.Router(), "application/json", `{"x":1}`)
	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code, string(raw))
}

func TestStatistics_MissingContentType(t *testing.T) {
	w, _ := post(t, api.Router(), "", "M48\n")
	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
}

// clearanceFile drills T01 (r = 0.5) at (0,0) and (2,0): center distance 2.
const clearanceFile = "M48\nMETRIC\nT01C1.000\n%\nT01\nX0Y0\nX2Y0\nM30\n"

func TestStatistics_MinClearanceSatisfied(t *testing.T) {
	// need = 0.5 + 0.5 + 0.5 = 1.5 <= 2: the statistics are identical
	// to the no-parameter response.
	w, raw := postPath(t, api.Router(), "/drill-files/statistics?min_clearance=0.5", "text/plain", clearanceFile)
	require.Equal(t, http.StatusOK, w.Code, string(raw))

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.EqualValues(t, 2, got["total_holes"])
	assert.Equal(t, "0.000", got["min_x"])
	assert.Equal(t, "2.000", got["max_x"])
	tools := got["tools"].([]any)
	assert.Equal(t, "T01", tools[0].(map[string]any)["tool"])
	assert.EqualValues(t, 2, tools[0].(map[string]any)["holes"])
}

func TestStatistics_MinClearanceTangencyPasses(t *testing.T) {
	// need = 0.5 + 0.5 + 1.0 = 2.0 == distance: exactly tangent holes
	// are not a conflict.
	w, raw := postPath(t, api.Router(), "/drill-files/statistics?min_clearance=1", "text/plain", clearanceFile)
	require.Equal(t, http.StatusOK, w.Code, string(raw))
}

func TestStatistics_MinClearanceConflict(t *testing.T) {
	// need = 0.5 + 0.5 + 1.5 = 2.5 > 2: conflict between the hole on
	// line 7 and the earlier hole on line 6.
	w, raw := postPath(t, api.Router(), "/drill-files/statistics?min_clearance=1.5", "text/plain", clearanceFile)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, string(raw))

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "HOLE_CLEARANCE", got["code"])
	assert.EqualValues(t, 7, got["line"])
	assert.EqualValues(t, 6, got["conflict_line"])
	assert.NotContains(t, string(raw), "total_holes")
}

func TestStatistics_InvalidClearance(t *testing.T) {
	bad := []string{"-1", "-0", "1.1234", "01", "1.", ".5", "abc", "", "%2B1", "1e3"}
	for _, v := range bad {
		t.Run("value/"+v, func(t *testing.T) {
			w, raw := postPath(t, api.Router(), "/drill-files/statistics?min_clearance="+v, "text/plain", clearanceFile)
			require.Equal(t, http.StatusBadRequest, w.Code, string(raw))

			var got map[string]any
			require.NoError(t, json.Unmarshal(raw, &got))
			assert.Equal(t, "INVALID_CLEARANCE", got["code"])
		})
	}
}

func TestStatistics_InvalidClearanceNotMaskedByFileError(t *testing.T) {
	// The body would fail parsing on line 1, but the invalid parameter
	// is rejected first as a client error, never as a file error.
	w, raw := postPath(t, api.Router(), "/drill-files/statistics?min_clearance=-1", "text/plain", "GARBAGE\n")
	require.Equal(t, http.StatusBadRequest, w.Code, string(raw))

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "INVALID_CLEARANCE", got["code"])
	assert.NotContains(t, string(raw), "LINE_ORDER")
}

func TestStatistics_NoClearanceParamKeepsBehavior(t *testing.T) {
	// Overlapping holes stay legal when the audit is not requested.
	body := "M48\nMETRIC\nT01C1.000\n%\nT01\nX1Y1\nX1Y1\nM30\n"
	w, raw := post(t, api.Router(), "text/plain", body)
	require.Equal(t, http.StatusOK, w.Code, string(raw))

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.EqualValues(t, 2, got["total_holes"])
}

// symmetryOK is a half-turn-symmetric program about (0,0): T01 holes at
// (1,2)/(-1,-2) and T02 holes at (5,0)/(-5,0), lines 6..9.
const symmetryOK = "M48\nMETRIC\nT01C0.300\nT02C1.500\n%\nT01\nX1.000Y2.000\nX-1.000Y-2.000\nT02\nX5.000Y0\nX-5.000Y0\nM30\n"

func TestStatistics_SymmetryCenterSuccess(t *testing.T) {
	w, raw := postPath(t, api.Router(), "/drill-files/statistics?symmetry_center=0,0", "text/plain", symmetryOK)
	require.Equal(t, http.StatusOK, w.Code, string(raw))

	// A passing audit leaves the statistics response unchanged.
	var withCenter map[string]any
	require.NoError(t, json.Unmarshal(raw, &withCenter))
	w2, raw2 := post(t, api.Router(), "text/plain", symmetryOK)
	require.Equal(t, http.StatusOK, w2.Code, string(raw2))
	var withoutCenter map[string]any
	require.NoError(t, json.Unmarshal(raw2, &withoutCenter))
	assert.Equal(t, withoutCenter, withCenter)
	assert.EqualValues(t, 4, withCenter["total_holes"])
}

func TestStatistics_SymmetryCenterNonOrigin(t *testing.T) {
	// (1,2) rotated about (1.5,1) lands at (2,0): 2*1.5-1 = 2, 2*1-2 = 0.
	body := "M48\nMETRIC\nT01C0.300\n%\nT01\nX1Y2\nX2Y0\nM30\n"
	w, raw := postPath(t, api.Router(), "/drill-files/statistics?symmetry_center=1.5,1", "text/plain", body)
	require.Equal(t, http.StatusOK, w.Code, string(raw))
}

func TestStatistics_SelfMappingCenterHoles(t *testing.T) {
	// A hole exactly at the center is a half-turn fixed point and
	// covers itself; one or many center holes pass without a partner.
	lone := "M48\nMETRIC\nT01C0.300\n%\nT01\nX0Y0\nM30\n"
	w, raw := postPath(t, api.Router(), "/drill-files/statistics?symmetry_center=0,0", "text/plain", lone)
	require.Equal(t, http.StatusOK, w.Code, string(raw))

	many := "M48\nMETRIC\nT01C0.300\n%\nT01\nX0Y0\nX0Y0\nX0Y0\nX1Y0\nX-1Y0\nM30\n"
	w, raw = postPath(t, api.Router(), "/drill-files/statistics?symmetry_center=0,0", "text/plain", many)
	require.Equal(t, http.StatusOK, w.Code, string(raw))

	// But a fixed point never supplies a partner: the lone off-center
	// hole on line 7 stays uncovered.
	plusOne := "M48\nMETRIC\nT01C0.300\n%\nT01\nX0Y0\nX1Y0\nM30\n"
	w, raw = postPath(t, api.Router(), "/drill-files/statistics?symmetry_center=0,0", "text/plain", plusOne)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, string(raw))
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "ASYMMETRIC_PATTERN", got["code"])
	assert.EqualValues(t, 7, got["line"])
	assert.Equal(t, []any{float64(7)}, got["uncovered_lines"])
	assert.NotContains(t, string(raw), "total_holes")
}

func TestStatistics_AsymmetricPatternListsUncoveredLines(t *testing.T) {
	// Lines 7/8 pair under T01; the T02 hole on line 10 (5,0) has no
	// T02 partner at (-5,0), so line 10 is uncovered.
	body := "M48\nMETRIC\nT01C0.300\nT02C1.500\n%\nT01\nX1.000Y2.000\nX-1.000Y-2.000\nT02\nX5.000Y0\nM30\n"
	w, raw := postPath(t, api.Router(), "/drill-files/statistics?symmetry_center=0,0", "text/plain", body)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, string(raw))

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "ASYMMETRIC_PATTERN", got["code"])
	assert.EqualValues(t, 10, got["line"])
	assert.Equal(t, []any{float64(10)}, got["uncovered_lines"])
}

func TestStatistics_DifferentToolDoesNotPair(t *testing.T) {
	// (1,0) with T01 and (-1,0) with T02: geometrically symmetric but
	// different tool classes, so both hole lines are uncovered in order.
	body := "M48\nMETRIC\nT01C0.1\nT02C0.2\n%\nT01\nX1Y0\nT02\nX-1Y0\nM30\n"
	w, raw := postPath(t, api.Router(), "/drill-files/statistics?symmetry_center=0,0", "text/plain", body)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, string(raw))

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "ASYMMETRIC_PATTERN", got["code"])
	assert.Equal(t, []any{float64(7), float64(9)}, got["uncovered_lines"])
}

func TestStatistics_InvalidSymmetryCenter(t *testing.T) {
	bad := []string{
		"", "0", "0,", ",0", ",", "0,0,0",
		"abc,0", "0,abc", "01,0", "0,01", "0.1234,0", "0,0.1234",
		"-0,0", "0,-0.000", "1.,0", ".5,0", "+1,0", "0,1e3",
		"0 0", "0, 0",
	}
	for _, v := range bad {
		t.Run("value/"+v, func(t *testing.T) {
			w, raw := postPath(t, api.Router(), "/drill-files/statistics?symmetry_center="+url.QueryEscape(v), "text/plain", symmetryOK)
			require.Equal(t, http.StatusBadRequest, w.Code, string(raw))

			var got map[string]any
			require.NoError(t, json.Unmarshal(raw, &got))
			assert.Equal(t, "INVALID_SYMMETRY", got["code"])
			assert.NotContains(t, string(raw), "line")
		})
	}
}

func TestStatistics_RepeatedSymmetryCenterRejected(t *testing.T) {
	// The parameter is non-repeatable, even when both values are valid;
	// the garbage body proves the query is rejected before it is read.
	w, raw := postPath(t, api.Router(),
		"/drill-files/statistics?symmetry_center=0,0&symmetry_center=1,1",
		"text/plain", "GARBAGE\n")
	require.Equal(t, http.StatusBadRequest, w.Code, string(raw))

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "INVALID_SYMMETRY", got["code"])
	assert.NotContains(t, string(raw), "LINE_ORDER")
}

func TestStatistics_InvalidSymmetryNotMaskedByFileError(t *testing.T) {
	// The body would fail parsing on line 1, but the invalid parameter
	// is a client error rejected first, before the body is read.
	w, raw := postPath(t, api.Router(),
		"/drill-files/statistics?symmetry_center=-1", "text/plain", "GARBAGE\n")
	require.Equal(t, http.StatusBadRequest, w.Code, string(raw))

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "INVALID_SYMMETRY", got["code"])
	assert.NotContains(t, string(raw), "LINE_ORDER")
}

func TestStatistics_FileErrorBeatsSymmetryAudit(t *testing.T) {
	// Undefined T02 on line 5: the symmetry audit never runs.
	body := "M48\nMETRIC\nT01C0.300\n%\nT02\nX1Y1\nM30\n"
	w, raw := postPath(t, api.Router(), "/drill-files/statistics?symmetry_center=0,0", "text/plain", body)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, string(raw))

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "UNDEFINED_TOOL", got["code"])
	assert.EqualValues(t, 5, got["line"])
	assert.NotContains(t, string(raw), "ASYMMETRIC_PATTERN")
}

func TestStatistics_ClearanceBeatsSymmetryAudit(t *testing.T) {
	// Two coincident holes off-center at (1,0): the clearance audit
	// fails (line 7 vs line 6) and the holes are also unbalanced under
	// the half-turn about (0,0); the clearance result must win.
	body := "M48\nMETRIC\nT01C1.000\n%\nT01\nX1Y0\nX1Y0\nM30\n"
	path := "/drill-files/statistics?min_clearance=0.5&symmetry_center=0,0"
	w, raw := postPath(t, api.Router(), path, "text/plain", body)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, string(raw))

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "HOLE_CLEARANCE", got["code"])
	assert.EqualValues(t, 7, got["line"])
	assert.EqualValues(t, 6, got["conflict_line"])
	assert.NotContains(t, string(raw), "ASYMMETRIC_PATTERN")
}

func TestStatistics_BothAuditsPass(t *testing.T) {
	// Holes 3 apart (need 1.5) form a half-turn pair about (1.5,0).
	body := "M48\nMETRIC\nT01C1.000\n%\nT01\nX0Y0\nX3Y0\nM30\n"
	path := "/drill-files/statistics?min_clearance=0.5&symmetry_center=1.5,0"
	w, raw := postPath(t, api.Router(), path, "text/plain", body)
	require.Equal(t, http.StatusOK, w.Code, string(raw))
}

func TestStatistics_NoSymmetryParamKeepsBehavior(t *testing.T) {
	// A lone off-center hole would fail the symmetry audit; without the
	// parameter the response is unchanged from before the feature.
	body := "M48\nMETRIC\nT01C0.300\n%\nT01\nX1Y1\nM30\n"
	w, raw := post(t, api.Router(), "text/plain", body)
	require.Equal(t, http.StatusOK, w.Code, string(raw))
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.EqualValues(t, 1, got["total_holes"])
}
