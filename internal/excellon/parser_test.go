package excellon_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"drillapi/internal/excellon"
)

// validFile is a small well-formed program reused by several tests.
const validFile = "M48\nMETRIC\nT01C0.300\nT02C1.500\n%\nT01\nX1.000Y2.000\nX-0.500Y2.000\nT02\nX10Y-3.25\nM30\n"

func TestParse_Valid(t *testing.T) {
	r, err := excellon.Parse(validFile)
	require.NoError(t, err)
	require.NotNil(t, r)

	assert.Equal(t, 2, len(r.Tools))
	assert.Equal(t, excellon.ToolCount{Tool: "T01", Holes: 2}, r.Tools[0])
	assert.Equal(t, excellon.ToolCount{Tool: "T02", Holes: 1}, r.Tools[1])
	assert.Equal(t, 3, r.Total)

	// All numeric output is fixed at three decimals.
	assert.Equal(t, "-0.500", r.MinX)
	assert.Equal(t, "-3.250", r.MinY)
	assert.Equal(t, "10.000", r.MaxX)
	assert.Equal(t, "2.000", r.MaxY)
}

func TestParse_TrailingNewlineAccepted(t *testing.T) {
	r, err := excellon.Parse("M48\nMETRIC\nT01C0.250\n%\nT01\nX0Y0\nM30\n")
	require.NoError(t, err)
	assert.Equal(t, 1, r.Total)
	assert.Equal(t, "0.000", r.MinX)
}

func TestParse_SingleHoleSetsAllExtremes(t *testing.T) {
	in := "M48\nMETRIC\nT99C1\n%\nT99\nX-5.5Y-2.123\nM30\n"
	r, err := excellon.Parse(in)
	require.NoError(t, err)
	assert.Equal(t, "-5.500", r.MinX)
	assert.Equal(t, "-5.500", r.MaxX)
	assert.Equal(t, "-2.123", r.MinY)
	assert.Equal(t, "-2.123", r.MaxY)
	assert.Equal(t, "T99", r.Tools[0].Tool)
}

func TestParse_OnlyUsedToolsAppearInHeaderOrder(t *testing.T) {
	in := "M48\nMETRIC\nT01C0.1\nT02C0.2\nT03C0.3\n%\nT03\nX1Y1\nT01\nX2Y2\nM30\n"
	r, err := excellon.Parse(in)
	require.NoError(t, err)
	require.Equal(t, 2, len(r.Tools))
	// Definition order, not first-use order; unused T02 is skipped.
	assert.Equal(t, "T01", r.Tools[0].Tool)
	assert.Equal(t, "T03", r.Tools[1].Tool)
}

func TestParse_MinMaxIndependenceAcrossAxes(t *testing.T) {
	in := "M48\nMETRIC\nT01C0.1\n%\nT01\nX100Y0\nX0Y100\nX50Y50\nM30\n"
	r, err := excellon.Parse(in)
	require.NoError(t, err)
	assert.Equal(t, "0.000", r.MinX)
	assert.Equal(t, "100.000", r.MaxX)
	assert.Equal(t, "0.000", r.MinY)
	assert.Equal(t, "100.000", r.MaxY)
}

func TestParse_EarliestErrorWins(t *testing.T) {
	// Two bad lines; the first one (4) must be reported.
	in := "M48\nMETRIC\nT01C0.300\nBOGUS\n%\nT99\nX1Y1\nM30\n"
	_, err := excellon.Parse(in)
	require.Error(t, err)
	pe := err.(*excellon.ParseError)
	assert.Equal(t, 4, pe.Line)
	assert.Equal(t, excellon.CodeLineOrder, pe.Code)
}

func TestParse_Errors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		line int
		code string
	}{
		// ---- structure / LINE_ORDER ----
		{"empty", "", 1, excellon.CodeLineOrder},
		{"wrong first line", "M30\n", 1, excellon.CodeLineOrder},
		{"missing M48", "METRIC\nT01C0.1\n%\nT01\nX1Y1\nM30\n", 1, excellon.CodeLineOrder},
		{"wrong second line", "M48\nINCH\nT01C0.1\n%\nT01\nX1Y1\nM30\n", 2, excellon.CodeLineOrder},
		{"header without tool defs", "M48\nMETRIC\n%\nT01\nX1Y1\nM30\n", 3, excellon.CodeLineOrder},
		{"missing percent", "M48\nMETRIC\nT01C0.1\nT01\nX1Y1\nM30\n", 4, excellon.CodeLineOrder},
		{"second percent", "M48\nMETRIC\nT01C0.1\n%\n%\nT01\nX1Y1\nM30\n", 5, excellon.CodeLineOrder},
		{"missing M30", "M48\nMETRIC\nT01C0.1\n%\nT01\nX1Y1\n", 7, excellon.CodeLineOrder},
		{"M30 not last", "M48\nMETRIC\nT01C0.1\n%\nT01\nM30\nX1Y1\n", 6, excellon.CodeLineOrder},
		{"empty last line (double newline)", "M48\nMETRIC\nT01C0.1\n%\nT01\nX1Y1\nM30\n\n", 7, excellon.CodeLineOrder},
		{"blank line in body", "M48\nMETRIC\nT01C0.1\n%\nT01\n\nX1Y1\nM30\n", 6, excellon.CodeLineOrder},
		{"blank line in header", "M48\nMETRIC\n\nT01C0.1\n%\nT01\nX1Y1\nM30\n", 3, excellon.CodeLineOrder},
		{"CRLF rejected", "M48\r\nMETRIC\r\nT01C0.1\r\n%\r\nT01\r\nX1Y1\r\nM30\r\n", 1, excellon.CodeLineOrder},
		{"stray space", " M48\nMETRIC\nT01C0.1\n%\nT01\nX1Y1\nM30\n", 1, excellon.CodeLineOrder},
		{"lowercase m48", "m48\nMETRIC\nT01C0.1\n%\nT01\nX1Y1\nM30\n", 1, excellon.CodeLineOrder},
		{"forbidden G00 command", "M48\nMETRIC\nT01C0.1\n%\nG00\nT01\nX1Y1\nM30\n", 5, excellon.CodeLineOrder},
		{"spindle command in body", "M48\nMETRIC\nT01C0.1\n%\nM03\nT01\nX1Y1\nM30\n", 5, excellon.CodeLineOrder},
		{"repeated select in header position", "M48\nMETRIC\nT01\n%\nT01\nX1Y1\nM30\n", 3, excellon.CodeLineOrder},
		{"tool def in body", "M48\nMETRIC\nT01C0.1\n%\nT02C0.2\nX1Y1\nM30\n", 5, excellon.CodeLineOrder},
		{"bare axis", "M48\nMETRIC\nT01C0.1\n%\nT01\nX1\nM30\n", 6, excellon.CodeLineOrder},
		{"missing X axis", "M48\nMETRIC\nT01C0.1\n%\nT01\nY1X1\nM30\n", 6, excellon.CodeLineOrder},
		{"missing Y axis", "M48\nMETRIC\nT01C0.1\n%\nT01\nX1Z1\nM30\n", 6, excellon.CodeLineOrder},
		{"duplicate X axis", "M48\nMETRIC\nT01C0.1\n%\nT01\nX1X2Y1\nM30\n", 6, excellon.CodeLineOrder},
		{"duplicate Y axis", "M48\nMETRIC\nT01C0.1\n%\nT01\nX1Y2Y1\nM30\n", 6, excellon.CodeLineOrder},
		{"empty coordinate", "M48\nMETRIC\nT01C0.1\n%\nT01\nXY1\nM30\n", 6, excellon.CodeLineOrder},
		{"junk after Y makes bad number", "M48\nMETRIC\nT01C0.1\n%\nT01\nX1Y1Z\nM30\n", 6, excellon.CodeInvalidNumber},
		{"hole before any tool", "M48\nMETRIC\nT01C0.1\n%\nX1Y1\nM30\n", 5, excellon.CodeUndefinedTool},

		// ---- number lexicon / INVALID_NUMBER (diameters reject signs) ----
		{"diameter leading zero", "M48\nMETRIC\nT01C01.0\n%\nT01\nX1Y1\nM30\n", 3, excellon.CodeInvalidNumber},
		{"diameter plus sign", "M48\nMETRIC\nT01C+1.0\n%\nT01\nX1Y1\nM30\n", 3, excellon.CodeInvalidNumber},
		{"diameter negative", "M48\nMETRIC\nT01C-0.1\n%\nT01\nX1Y1\nM30\n", 3, excellon.CodeInvalidNumber},
		{"diameter zero", "M48\nMETRIC\nT01C0\n%\nT01\nX1Y1\nM30\n", 3, excellon.CodeInvalidNumber},
		{"diameter zero decimal", "M48\nMETRIC\nT01C0.000\n%\nT01\nX1Y1\nM30\n", 3, excellon.CodeInvalidNumber},
		{"diameter four decimals", "M48\nMETRIC\nT01C0.1234\n%\nT01\nX1Y1\nM30\n", 3, excellon.CodeInvalidNumber},
		{"diameter dangling dot", "M48\nMETRIC\nT01C1.\n%\nT01\nX1Y1\nM30\n", 3, excellon.CodeInvalidNumber},
		{"diameter bare dot", "M48\nMETRIC\nT01C.\n%\nT01\nX1Y1\nM30\n", 3, excellon.CodeInvalidNumber},
		{"diameter exponent", "M48\nMETRIC\nT01C1e2\n%\nT01\nX1Y1\nM30\n", 3, excellon.CodeInvalidNumber},
		{"coordinate leading zero", "M48\nMETRIC\nT01C0.1\n%\nT01\nX00Y1\nM30\n", 6, excellon.CodeInvalidNumber},
		{"coordinate four decimals", "M48\nMETRIC\nT01C0.1\n%\nT01\nX1.1234Y1\nM30\n", 6, excellon.CodeInvalidNumber},
		{"coordinate plus sign", "M48\nMETRIC\nT01C0.1\n%\nT01\nX+1Y1\nM30\n", 6, excellon.CodeInvalidNumber},
		{"coordinate negative zero", "M48\nMETRIC\nT01C0.1\n%\nT01\nX-0Y1\nM30\n", 6, excellon.CodeInvalidNumber},
		{"coordinate negative zero decimals", "M48\nMETRIC\nT01C0.1\n%\nT01\nX-0.000Y1\nM30\n", 6, excellon.CodeInvalidNumber},
		{"coordinate dangling dot", "M48\nMETRIC\nT01C0.1\n%\nT01\nX1.Y1\nM30\n", 6, excellon.CodeInvalidNumber},
		{"coordinate hexish", "M48\nMETRIC\nT01C0.1\n%\nT01\nX0x1Y1\nM30\n", 6, excellon.CodeInvalidNumber},

		// ---- DUPLICATE_TOOL ----
		{"duplicate tool", "M48\nMETRIC\nT01C0.3\nT01C0.4\n%\nT01\nX1Y1\nM30\n", 4, excellon.CodeDuplicateTool},

		// ---- UNDEFINED_TOOL ----
		{"undefined select", "M48\nMETRIC\nT01C0.1\n%\nT02\nX1Y1\nM30\n", 5, excellon.CodeUndefinedTool},
		{"select T00", "M48\nMETRIC\nT01C0.1\n%\nT00\nX1Y1\nM30\n", 5, excellon.CodeLineOrder},

		// ---- NO_HOLES ----
		{"no holes, only select", "M48\nMETRIC\nT01C0.1\n%\nT01\nM30\n", 6, excellon.CodeNoHoles},
		{"no holes at all", "M48\nMETRIC\nT01C0.1\n%\nM30\n", 5, excellon.CodeNoHoles},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := excellon.Parse(tc.in)
			require.Error(t, err, "expected parse failure")
			pe, ok := err.(*excellon.ParseError)
			require.True(t, ok, "error must be *ParseError, got %T", err)
			assert.Equal(t, tc.code, pe.Code)
			assert.Equal(t, tc.line, pe.Line)
		})
	}
}

func TestParse_PrecedenceWithinLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		code string
	}{
		// Malformed tool definition => LINE_ORDER beats INVALID_NUMBER.
		{"bad def shape with bad number", "M48\nMETRIC\nT1Cxx\n%\nT01\nX1Y1\nM30\n", excellon.CodeLineOrder},
		// Duplicate with an invalid diameter: lexicon beats duplicate.
		{"invalid number beats duplicate", "M48\nMETRIC\nT01C0.1\nT01C0.0\n%\nT01\nX1Y1\nM30\n", excellon.CodeInvalidNumber},
		// Valid duplicate shape+number: duplicate reported.
		{"duplicate over fine", "M48\nMETRIC\nT01C0.1\nT01C0.2\n%\nT01\nX1Y1\nM30\n", excellon.CodeDuplicateTool},
		// Hole with bad shape AND no tool selected: structure wins.
		{"structure beats undefined tool", "M48\nMETRIC\nT01C0.1\n%\nZ1\nM30\n", excellon.CodeLineOrder},
		// Bad coordinate lexicon beats "no tool selected".
		{"invalid number beats no tool", "M48\nMETRIC\nT01C0.1\n%\nX01Y1\nM30\n", excellon.CodeInvalidNumber},
		// Well-formed hole before a select: undefined tool.
		{"well formed hole no tool", "M48\nMETRIC\nT01C0.1\n%\nX1Y1\nM30\n", excellon.CodeUndefinedTool},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := excellon.Parse(tc.in)
			require.Error(t, err)
			assert.Equal(t, tc.code, err.(*excellon.ParseError).Code)
		})
	}
}

func TestParse_CanonicalNumberBoundaries(t *testing.T) {
	good := []string{"0", "0.0", "0.000", "1", "123456789", "1.5", "12.345", "-1", "-0.001", "-999.999"}
	for _, n := range good {
		t.Run("good/"+n, func(t *testing.T) {
			in := "M48\nMETRIC\nT01C0.1\n%\nT01\nX" + n + "Y0\nM30\n"
			_, err := excellon.Parse(in)
			assert.NoError(t, err)
		})
	}

	bad := []string{"00", "01.5", "00.1", "1.", ".5", "1.0000", "1.2.3", "1e3", "--1", "-", "+", " 1", "1 ", "0x1", "-0", "-0.00"}
	for _, n := range bad {
		t.Run("bad/"+n, func(t *testing.T) {
			in := "M48\nMETRIC\nT01C0.1\n%\nT01\nX" + n + "Y0\nM30\n"
			_, err := excellon.Parse(in)
			if assert.Error(t, err) {
				assert.Equal(t, excellon.CodeInvalidNumber, err.(*excellon.ParseError).Code)
			}
		})
	}
}

func TestParseError_Message(t *testing.T) {
	e := &excellon.ParseError{Line: 7, Code: excellon.CodeUndefinedTool}
	assert.Equal(t, "line 7: UNDEFINED_TOOL", e.Error())
}
