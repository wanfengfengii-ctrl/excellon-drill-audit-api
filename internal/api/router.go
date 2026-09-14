// Package api wires the Excellon parser to an HTTP server.
package api

import (
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"drillapi/internal/excellon"
)

// maxBodyBytes caps an uploaded drill program at 10 MiB.
const maxBodyBytes = 10 << 20

// ErrorBody is the JSON envelope returned for every 4xx response.
type ErrorBody struct {
	Code string `json:"code"`
	Line int    `json:"line,omitempty"`
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

	return r
}

func postStatistics(c *gin.Context) {
	// Require text/plain explicitly; parameters (charset etc.) are ignored.
	if !strings.HasPrefix(c.ContentType(), "text/plain") {
		c.JSON(http.StatusUnsupportedMediaType, ErrorBody{Code: "UNSUPPORTED_MEDIA_TYPE"})
		return
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

	report, err := excellon.Parse(string(body))
	if err != nil {
		pe := err.(*excellon.ParseError)
		c.JSON(http.StatusUnprocessableEntity, ErrorBody{Code: pe.Code, Line: pe.Line})
		return
	}
	c.JSON(http.StatusOK, report)
}
