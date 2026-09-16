// Package api wires the Excellon parser to an HTTP server.
package api

import (
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"drillapi/internal/excellon"
)

// maxBodyBytes caps an uploaded drill program at 10 MiB.
const maxBodyBytes = 10 << 20

// ErrorBody is the JSON envelope returned for every 4xx response.
type ErrorBody struct {
	Code string `json:"code"`
	Line int    `json:"line,omitempty"`
	// ConflictLine is set only for HOLE_CLEARANCE: the earlier hole the
	// hole at Line collides with.
	ConflictLine int `json:"conflict_line,omitempty"`
	// UncoveredLines is set only for ASYMMETRIC_PATTERN: every hole line
	// left without a rotation partner, in body line order; Line is the
	// first of them.
	UncoveredLines []int `json:"uncovered_lines,omitempty"`
	// Part is set only by the panel audit: the multipart field
	// ("template" or "panel") the error belongs to.
	Part string `json:"part,omitempty"`
	// Failures is set only for PANEL_PATTERN_MISMATCH: one record per
	// rotation angle, in angle order, each locating the anchor panel
	// line, the first missing template line and the offset in use.
	Failures []excellon.OrientationFailure `json:"failures,omitempty"`
}

// Router builds the application's HTTP handler.
func Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	r.POST("/drill-files/statistics", postStatistics)
	r.POST("/drill-files/panel-audit", postPanelAudit)

	return r
}

func postStatistics(c *gin.Context) {
	// Require text/plain explicitly; parameters (charset etc.) are ignored.
	if !strings.HasPrefix(c.ContentType(), "text/plain") {
		c.JSON(http.StatusUnsupportedMediaType, ErrorBody{Code: "UNSUPPORTED_MEDIA_TYPE"})
		return
	}

	// Optional min_clearance audit parameter: a canonical decimal >= 0.
	// An invalid value is a client error, never a file error, so it is
	// rejected before the request body is even read.
	var clearance *decimal.Decimal
	if raw, present := c.GetQuery("min_clearance"); present {
		d, ok := excellon.ParseClearance(raw)
		if !ok {
			c.JSON(http.StatusBadRequest, ErrorBody{Code: "INVALID_CLEARANCE"})
			return
		}
		clearance = &d
	}

	// Optional, non-repeatable symmetry_center audit parameter:
	// "x,y" in the file coordinate lexicon. A repeated or malformed
	// value is a client error, rejected before the body is read.
	var center *excellon.SymmetryCenter
	if vals, present := c.GetQueryArray("symmetry_center"); present {
		if len(vals) != 1 {
			c.JSON(http.StatusBadRequest, ErrorBody{Code: "INVALID_SYMMETRY"})
			return
		}
		sc, ok := excellon.ParseSymmetryCenter(vals[0])
		if !ok {
			c.JSON(http.StatusBadRequest, ErrorBody{Code: "INVALID_SYMMETRY"})
			return
		}
		center = &sc
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodyBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorBody{Code: "BAD_REQUEST"})
		return
	}
	if len(body) > maxBodyBytes {
		c.JSON(http.StatusRequestEntityTooLarge, ErrorBody{Code: "PAYLOAD_TOO_LARGE"})
		return
	}

	report, err := excellon.ParseWithAudits(string(body), clearance, center)
	if err != nil {
		pe := err.(*excellon.ParseError)
		c.JSON(http.StatusUnprocessableEntity, ErrorBody{
			Code:           pe.Code,
			Line:           pe.Line,
			ConflictLine:   pe.ConflictLine,
			UncoveredLines: pe.UncoveredLines,
		})
		return
	}
	c.JSON(http.StatusOK, report)
}

// panelParts names the two multipart file fields, in validation order:
// the template is always judged before the panel.
var panelParts = [2]string{"template", "panel"}

// panelAuditResponse is the 200 body of the panel audit: every feasible
// layout in rotation angle order.
type panelAuditResponse struct {
	Layouts []excellon.PanelLayout `json:"layouts"`
}

func postPanelAudit(c *gin.Context) {
	// Require multipart/form-data explicitly; parameters (boundary etc.)
	// are ignored.
	if !strings.HasPrefix(c.ContentType(), "multipart/form-data") {
		c.JSON(http.StatusUnsupportedMediaType, ErrorBody{Code: "UNSUPPORTED_MEDIA_TYPE"})
		return
	}

	// Both parts reuse the drill-file validation; the template is parsed
	// first, so its error wins when both files are invalid.
	texts := make(map[string]string, len(panelParts))
	for _, part := range panelParts {
		text, ok := readPart(c, part)
		if !ok {
			return
		}
		texts[part] = text
	}
	holes := make(map[string][]excellon.Hole, len(panelParts))
	for _, part := range panelParts {
		hs, err := excellon.ParseHoles(texts[part])
		if err != nil {
			pe := err.(*excellon.ParseError)
			c.JSON(http.StatusUnprocessableEntity, ErrorBody{
				Code:           pe.Code,
				Line:           pe.Line,
				ConflictLine:   pe.ConflictLine,
				UncoveredLines: pe.UncoveredLines,
				Part:           part,
			})
			return
		}
		holes[part] = hs
	}

	layouts, failures := excellon.AuditPanel(holes["template"], holes["panel"])
	if len(layouts) == 0 {
		// All four rotations failed: report the per-angle evidence.
		c.JSON(http.StatusUnprocessableEntity, ErrorBody{
			Code:     excellon.CodePanelPatternMismatch,
			Failures: failures,
		})
		return
	}
	c.JSON(http.StatusOK, panelAuditResponse{Layouts: layouts})
}

// readPart extracts one uploaded file field as text. A missing or
// unreadable part is a 400 client error; a part over the size cap is a
// 413. It reports whether the handler may continue.
func readPart(c *gin.Context, part string) (string, bool) {
	fh, err := c.FormFile(part)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorBody{Code: "MISSING_PART", Part: part})
		return "", false
	}
	f, err := fh.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorBody{Code: "BAD_REQUEST", Part: part})
		return "", false
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, maxBodyBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorBody{Code: "BAD_REQUEST", Part: part})
		return "", false
	}
	if len(body) > maxBodyBytes {
		c.JSON(http.StatusRequestEntityTooLarge, ErrorBody{Code: "PAYLOAD_TOO_LARGE", Part: part})
		return "", false
	}
	return string(body), true
}
