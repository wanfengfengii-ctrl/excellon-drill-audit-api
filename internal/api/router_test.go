package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
