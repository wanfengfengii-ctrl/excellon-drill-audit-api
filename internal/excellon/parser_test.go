package excellon_test

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
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

func TestParseError_ClearanceMessage(t *testing.T) {
	e := &excellon.ParseError{Line: 9, Code: excellon.CodeHoleClearance, ConflictLine: 7}
	assert.Equal(t, "line 9: HOLE_CLEARANCE (conflicts with line 7)", e.Error())
}

// dec is a test helper turning a canonical string into a *decimal.Decimal.
func dec(s string) *decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return &d
}

func TestParseWithClearance_NilDisablesAudit(t *testing.T) {
	// Two holes at the exact same spot: legal without the audit.
	in := "M48\nMETRIC\nT01C1.000\n%\nT01\nX1Y1\nX1Y1\nM30\n"
	r, err := excellon.Parse(in)
	require.NoError(t, err)
	assert.Equal(t, 2, r.Total)

	// The same file fails as soon as the audit is requested, even with
	// a zero clearance (distance 0 < r + r).
	_, err = excellon.ParseWithClearance(in, dec("0"))
	require.Error(t, err)
	pe := err.(*excellon.ParseError)
	assert.Equal(t, excellon.CodeHoleClearance, pe.Code)
	assert.Equal(t, 7, pe.Line)
	assert.Equal(t, 6, pe.ConflictLine)
}

func TestParseWithClearance_ValidSpacingKeepsStatistics(t *testing.T) {
	want, err := excellon.Parse(validFile)
	require.NoError(t, err)

	got, err := excellon.ParseWithClearance(validFile, dec("0.5"))
	require.NoError(t, err)
	// The audit must not alter the statistics of a passing file.
	assert.Equal(t, want, got)
}

func TestParseWithClearance_TangencyPasses(t *testing.T) {
	cases := []struct {
		name      string
		clearance string
		in        string
	}{
		// dist == r + r with zero clearance.
		{"zero clearance tangent", "0",
			"M48\nMETRIC\nT01C1.000\n%\nT01\nX0Y0\nX1Y0\nM30\n"},
		// dist == r + r + clearance.
		{"tangent with clearance", "0.5",
			"M48\nMETRIC\nT01C1.000\n%\nT01\nX0Y0\nX1.500Y0\nM30\n"},
		// 3-4-5 triangle: dist 5 == 1 + 2 + 2 (diameters 2 and 4).
		{"pythagorean tangency", "2",
			"M48\nMETRIC\nT01C2.000\nT02C4.000\n%\nT01\nX0Y0\nT02\nX3Y4\nM30\n"},
		// Fractional radii: d = 0.3 -> r = 0.15; dist 0.3 == 0.15 + 0.15.
		// Exact decimal halving keeps this a pass where floats would not.
		{"fractional radius tangency", "0",
			"M48\nMETRIC\nT01C0.300\n%\nT01\nX0Y0\nX0.300Y0\nM30\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := excellon.ParseWithClearance(tc.in, dec(tc.clearance))
			require.NoError(t, err)
			require.NotNil(t, r)
		})
	}
}

func TestParseWithClearance_ConflictLocatesBothLines(t *testing.T) {
	// Lines 6/7/8 drill at x = 0, 2, 1 with r = 0.5 and clearance 0.5
	// (need = 1.5). Line 8 is 1.0 away from both earlier holes; the
	// earliest conflicting line (6) must be reported.
	in := "M48\nMETRIC\nT01C1.000\n%\nT01\nX0Y0\nX2Y0\nX1Y0\nM30\n"
	_, err := excellon.ParseWithClearance(in, dec("0.5"))
	require.Error(t, err)
	pe := err.(*excellon.ParseError)
	assert.Equal(t, excellon.CodeHoleClearance, pe.Code)
	assert.Equal(t, 8, pe.Line)
	assert.Equal(t, 6, pe.ConflictLine)
}

func TestParseWithClearance_FirstConflictWins(t *testing.T) {
	// Two independent conflicting pairs: (6,7) and (8,9). The pair
	// completed first in textual order (line 7) is reported.
	in := "M48\nMETRIC\nT01C1.000\n%\nT01\nX0Y0\nX0.5Y0\nX10Y0\nX10.5Y0\nM30\n"
	_, err := excellon.ParseWithClearance(in, dec("0.5"))
	require.Error(t, err)
	pe := err.(*excellon.ParseError)
	assert.Equal(t, 7, pe.Line)
	assert.Equal(t, 6, pe.ConflictLine)
}

func TestParseWithClearance_UsesSelectedToolRadius(t *testing.T) {
	// T01 r = 0.25, T02 r = 1.0; centers 1.3 apart (lines 7 and 9).
	in := "M48\nMETRIC\nT01C0.500\nT02C2.000\n%\nT01\nX0Y0\nT02\nX1.300Y0\nM30\n"

	// need = 0.25 + 1.0 + 0.05 = 1.30 == dist: tangent, passes.
	_, err := excellon.ParseWithClearance(in, dec("0.05"))
	require.NoError(t, err)

	// need = 1.301 > 1.3: conflict.
	_, err = excellon.ParseWithClearance(in, dec("0.051"))
	require.Error(t, err)
	pe := err.(*excellon.ParseError)
	assert.Equal(t, excellon.CodeHoleClearance, pe.Code)
	assert.Equal(t, 9, pe.Line)
	assert.Equal(t, 7, pe.ConflictLine)
}

func TestParseWithClearance_ExistingErrorsKeepPriority(t *testing.T) {
	cases := []struct {
		name string
		in   string
		line int
		code string
	}{
		// INVALID_NUMBER on line 6: line 7 never gets audited.
		{"earlier invalid number",
			"M48\nMETRIC\nT01C1.000\n%\nT01\nX01Y0\nX0Y0\nM30\n",
			6, excellon.CodeInvalidNumber},
		// Same line: the hole's own bad number beats its clearance conflict.
		{"same line invalid number beats clearance",
			"M48\nMETRIC\nT01C1.000\n%\nT01\nX0Y0\nX0.0.1Y0\nM30\n",
			7, excellon.CodeInvalidNumber},
		// Structure error on line 5 precedes any hole pair.
		{"earlier structure error",
			"M48\nMETRIC\nT01C1.000\n%\nBOGUS\nT01\nX0Y0\nX0Y0\nM30\n",
			5, excellon.CodeLineOrder},
		// Undefined tool reference precedes any hole pair.
		{"earlier undefined tool",
			"M48\nMETRIC\nT01C1.000\n%\nT02\nX0Y0\nX0Y0\nM30\n",
			5, excellon.CodeUndefinedTool},
		// NO_HOLES is still reported when the audit is active.
		{"no holes still reported",
			"M48\nMETRIC\nT01C1.000\n%\nT01\nM30\n",
			6, excellon.CodeNoHoles},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := excellon.ParseWithClearance(tc.in, dec("0.5"))
			require.Error(t, err)
			pe := err.(*excellon.ParseError)
			assert.Equal(t, tc.code, pe.Code)
			assert.Equal(t, tc.line, pe.Line)
			assert.Equal(t, 0, pe.ConflictLine)
		})
	}
}

func TestParseClearance(t *testing.T) {
	valid := []string{"0", "0.0", "0.000", "1", "1.5", "12.345", "1000"}
	for _, s := range valid {
		t.Run("valid/"+s, func(t *testing.T) {
			d, ok := excellon.ParseClearance(s)
			require.True(t, ok)
			want, _ := decimal.NewFromString(s)
			assert.True(t, want.Equal(d), "value mismatch for %q", s)
		})
	}

	invalid := []string{"", "-0", "-0.0", "-1", "-1.5", "01", "00", "1.", ".5", "1.1234", "+1", "1e3", "abc", " 1", "1 ", "1,5"}
	for _, s := range invalid {
		t.Run("invalid/"+s, func(t *testing.T) {
			_, ok := excellon.ParseClearance(s)
			assert.False(t, ok)
		})
	}
}

// symmetryFile drills two holes at (1,2) and (-1,-2), a half-turn about
// the origin, plus a T02 hole at (5,0) with no partner.
const symmetryFile = "M48\nMETRIC\nT01C0.300\nT02C1.500\n%\nT01\nX1.000Y2.000\nX-1.000Y-2.000\nT02\nX5.000Y0\nM30\n"

func TestParseSymmetryCenter(t *testing.T) {
	valid := []struct {
		in   string
		x, y string
	}{
		{"0,0", "0", "0"},
		{"1.5,-2.25", "1.5", "-2.25"},
		{"-0.5,0.001", "-0.5", "0.001"},
		{"12,-3", "12", "-3"},
	}
	for _, tc := range valid {
		t.Run("valid/"+tc.in, func(t *testing.T) {
			got, ok := excellon.ParseSymmetryCenter(tc.in)
			require.True(t, ok)
			x, _ := decimal.NewFromString(tc.x)
			y, _ := decimal.NewFromString(tc.y)
			assert.True(t, got.X.Equal(x), "x mismatch for %q", tc.in)
			assert.True(t, got.Y.Equal(y), "y mismatch for %q", tc.in)
		})
	}

	invalid := []string{
		"", "1", "1,", ",1", "1,2,3", ",",
		"abc,1", "1,abc", "01,1", "1,01", "1.1234,1", "1,1.1234",
		"-0,1", "1,-0.0", "1.,1", ".5,1", "+1,1", "1e3,0",
		"1 2", "1, 2", " 1,2", "1,2 ",
	}
	for _, s := range invalid {
		t.Run("invalid/"+s, func(t *testing.T) {
			_, ok := excellon.ParseSymmetryCenter(s)
			assert.False(t, ok)
		})
	}
}

func center(x, y string) *excellon.SymmetryCenter {
	cx, _ := decimal.NewFromString(x)
	cy, _ := decimal.NewFromString(y)
	return &excellon.SymmetryCenter{X: cx, Y: cy}
}

func TestParseWithAudits_ExactHalfTurnSymmetry(t *testing.T) {
	cases := []struct {
		name string
		in   string
		c    *excellon.SymmetryCenter
	}{
		{
			"pair about origin",
			"M48\nMETRIC\nT01C0.1\n%\nT01\nX1.000Y2.000\nX-1.000Y-2.000\nM30\n",
			center("0", "0"),
		},
		{
			"pair about non-origin center",
			// 2*(1.5,1) - (1,2) = (2,0).
			"M48\nMETRIC\nT01C0.1\n%\nT01\nX1Y2\nX2Y0\nM30\n",
			center("1.5", "1"),
		},
		{
			"negative center with zero coordinate",
			// 2*(-0.5,0) - (-1,0) = (0,0); lexical "0" and "0.000"
			// normalize to the same key.
			"M48\nMETRIC\nT01C0.1\n%\nT01\nX-1.000Y0\nX0Y0.000\nM30\n",
			center("-0.5", "0"),
		},
		{
			"duplicate holes keep the counts equal",
			"M48\nMETRIC\nT01C0.1\n%\nT01\nX1Y0\nX1Y0\nX-1Y0\nX-1Y0\nM30\n",
			center("0", "0"),
		},
		{
			"a single hole at the center is a fixed point",
			// One hole exactly at the center rotates onto itself, so
			// count conservation holds with no partner required.
			"M48\nMETRIC\nT01C0.1\n%\nT01\nX0Y0\nM30\n",
			center("0", "0"),
		},
		{
			"center fixed points pass at any multiplicity",
			// Three coincident center holes are all fixed points; the
			// extra off-center pair is balanced as usual.
			"M48\nMETRIC\nT01C0.1\n%\nT01\nX0Y0\nX0Y0\nX0Y0\nX1Y0\nX-1Y0\nM30\n",
			center("0", "0"),
		},
		{
			"a fixed-point center hole never supplies a partner",
			// A center hole coexists with a balanced pair; it covers
			// itself and must not be consumed as anyone's partner.
			"M48\nMETRIC\nT01C0.1\n%\nT01\nX0Y0\nX0Y0\nX1Y0\nX-1Y0\nM30\n",
			center("0", "0"),
		},
		{
			"symmetry is per tool",
			// T01 pair and T02 pair independently.
			"M48\nMETRIC\nT01C0.1\nT02C0.2\n%\nT01\nX1Y0\nT02\nX1Y0\nT01\nX-1Y0\nT02\nX-1Y0\nM30\n",
			center("0", "0"),
		},
		{
			"symmetric subset of the two-tool fixture",
			// Remove the unpaired T02 hole from symmetryFile.
			"M48\nMETRIC\nT01C0.300\nT02C1.500\n%\nT01\nX1.000Y2.000\nX-1.000Y-2.000\nM30\n",
			center("0", "0"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := excellon.Parse(tc.in)
			require.NoError(t, err)
			got, err := excellon.ParseWithAudits(tc.in, nil, tc.c)

			require.NoError(t, err)
			// A passing audit must not alter the statistics.
			assert.Equal(t, want, got)
		})
	}
}

func TestParseWithAudits_AsymmetricPatterns(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		c         *excellon.SymmetryCenter
		uncovered []int
	}{
		{
			"center fixed point does not cover an unbalanced off-center hole",
			// Line 6 sits at the center and passes on its own; the lone
			// (1,0) on line 7 still has no partner at (-1,0).
			"M48\nMETRIC\nT01C0.1\n%\nT01\nX0Y0\nX1Y0\nM30\n",
			center("0", "0"),
			[]int{7},
		},
		{
			"three center fixed points plus one unbalanced hole",
			// Lines 6-8 are center fixed points; the lone (1,0) on
			// line 9 is the only uncovered source.
			"M48\nMETRIC\nT01C0.1\n%\nT01\nX0Y0\nX0Y0\nX0Y0\nX1Y0\nM30\n",
			center("0", "0"),
			[]int{9},
		},
		{
			"missing rotated partner",
			// symmetryFile: the lone T02 hole (5,0) is on line 10.
			symmetryFile,
			center("0", "0"),
			[]int{10},
		},
		{
			"duplicate source exhausts the partner count",
			// Two (1,0) holes, one (-1,0): the second (1,0) on line 8
			// is uncovered; line 9 was consumed earlier as its partner.
			"M48\nMETRIC\nT01C0.1\n%\nT01\nX1Y0\nX1Y0\nX-1Y0\nM30\n",
			center("0", "0"),
			[]int{7},
		},
		{
			"same imbalance with reversed textual order",
			// The extra (-1,0) is the second occurrence, again line 7;
			// pairing order cannot change the result.
			"M48\nMETRIC\nT01C0.1\n%\nT01\nX-1Y0\nX-1Y0\nX1Y0\nM30\n",
			center("0", "0"),
			[]int{7},
		},
		{
			"rotated point drilled with another tool never pairs",
			// T01@(1,0) would need T01@(-1,0); a T02 hole there is a
			// different key, so both source lines are uncovered.
			"M48\nMETRIC\nT01C0.1\nT02C0.2\n%\nT01\nX1Y0\nT02\nX-1Y0\nM30\n",
			center("0", "0"),
			[]int{7, 9},
		},
		{
			"uncovered lines stay in body line order",
			// Lines 6 and 9 unpaired; line 7's partner is line 8.
			"M48\nMETRIC\nT01C0.1\n%\nT01\nX1Y0\nX2Y0\nX-2Y0\nX5Y0\nM30\n",
			center("0", "0"),
			[]int{6, 9},
		},
		{
			"wrong center leaves the non-fixed hole uncovered",
			// Symmetric about the origin, audited about (1,0): the
			// (1,0) hole on line 6 is then a fixed point and passes;
			// the (-1,0) hole needs a (3,0) hole that does not exist.
			"M48\nMETRIC\nT01C0.1\n%\nT01\nX1Y0\nX-1Y0\nM30\n",
			center("1", "0"),
			[]int{7},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := excellon.ParseWithAudits(tc.in, nil, tc.c)
			require.Error(t, err)
			pe, ok := err.(*excellon.ParseError)
			require.True(t, ok, "error must be *ParseError, got %T", err)
			assert.Equal(t, excellon.CodeAsymmetricPattern, pe.Code)
			assert.Equal(t, tc.uncovered, pe.UncoveredLines)
			assert.Equal(t, tc.uncovered[0], pe.Line)
			assert.Zero(t, pe.ConflictLine)
		})
	}
}

func TestParseWithAudits_DuplicateDeterminismAcrossPermutations(t *testing.T) {
	// Three T01 holes at (1,0) and one at (-1,0) in every body
	// permutation: exactly two (1,0) holes stay uncovered, and they
	// must be the surplus class in every ordering — the single (-1,0)
	// hole is always consumed as the earliest available partner, so the
	// uncovered class never depends on textual pairing order.
	perms := []string{
		"M48\nMETRIC\nT01C0.1\n%\nT01\nX1Y0\nX1Y0\nX1Y0\nX-1Y0\nM30\n",
		"M48\nMETRIC\nT01C0.1\n%\nT01\nX1Y0\nX1Y0\nX-1Y0\nX1Y0\nM30\n",
		"M48\nMETRIC\nT01C0.1\n%\nT01\nX1Y0\nX-1Y0\nX1Y0\nX1Y0\nM30\n",
		"M48\nMETRIC\nT01C0.1\n%\nT01\nX-1Y0\nX1Y0\nX1Y0\nX1Y0\nM30\n",
	}
	for i, in := range perms {
		_, err := excellon.ParseWithAudits(in, nil, center("0", "0"))
		require.Error(t, err)
		pe := err.(*excellon.ParseError)
		assert.Equal(t, excellon.CodeAsymmetricPattern, pe.Code)
		require.Len(t, pe.UncoveredLines, 2, "permutation %d", i)
		docLines := strings.Split(in, "\n")
		for _, ln := range pe.UncoveredLines {
			assert.Equal(t, "X1Y0", docLines[ln-1], "permutation %d: only surplus (1,0) holes may be uncovered", i)
		}
		// Uncovered lines are reported in body line order.
		assert.Equal(t, pe.UncoveredLines[0], pe.Line, "permutation %d", i)
		assert.Less(t, pe.UncoveredLines[0], pe.UncoveredLines[1], "permutation %d", i)
	}
}

func TestParseWithAudits_SymmetryKeepsExistingErrorPriority(t *testing.T) {
	cases := []struct {
		name string
		in   string
		line int
		code string
	}{
		// Structural failure on line 1 beats the symmetry audit.
		{"structure error", "GARBAGE\n", 1, excellon.CodeLineOrder},
		// Lexicon failure on line 6 beats the missing partner.
		{"invalid number",
			"M48\nMETRIC\nT01C1.000\n%\nT01\nX01Y0\nX0Y0\nM30\n",
			6, excellon.CodeInvalidNumber},
		// Undefined tool reference beats the missing partner.
		{"undefined tool",
			"M48\nMETRIC\nT01C1.000\n%\nT09\nX1Y0\nM30\n",
			5, excellon.CodeUndefinedTool},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := excellon.ParseWithAudits(tc.in, nil, center("0", "0"))
			require.Error(t, err)
			pe := err.(*excellon.ParseError)
			assert.Equal(t, tc.code, pe.Code)
			assert.Equal(t, tc.line, pe.Line)
			assert.Empty(t, pe.UncoveredLines)
		})
	}
}

func TestParseWithAudits_ClearanceRunsBeforeSymmetry(t *testing.T) {
	// Two coincident holes off-center at (1,0): the clearance audit
	// fails (line 7 vs line 6) and the pair is also asymmetric about
	// the origin (no (-1,0) holes); the clearance audit must abort the
	// parse first.
	in := "M48\nMETRIC\nT01C1.000\n%\nT01\nX1Y0\nX1Y0\nM30\n"
	_, err := excellon.ParseWithAudits(in, dec("0.5"), center("0", "0"))
	require.Error(t, err)
	pe := err.(*excellon.ParseError)
	assert.Equal(t, excellon.CodeHoleClearance, pe.Code)
	assert.Equal(t, 7, pe.Line)
	assert.Equal(t, 6, pe.ConflictLine)
	assert.Empty(t, pe.UncoveredLines)
}

func TestParseWithAudits_BothAuditsPassTogether(t *testing.T) {
	in := "M48\nMETRIC\nT01C1.000\n%\nT01\nX0Y0\nX3Y0\nM30\n"
	// Center distance 3, need 1 + 0.5 = 1.5; half-turn pair about
	// (1.5, 0).
	r, err := excellon.ParseWithAudits(in, dec("0.5"), center("1.5", "0"))
	require.NoError(t, err)
	assert.Equal(t, 2, r.Total)
}

func TestParseWithAudits_NilCenterDisablesSymmetry(t *testing.T) {
	// A lone off-center hole would fail the symmetry audit but parses
	// identically to ParseWithClearance without one.
	in := "M48\nMETRIC\nT01C0.1\n%\nT01\nX1Y1\nM30\n"
	r, err := excellon.ParseWithAudits(in, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, r.Total)
}

func TestParseError_AsymmetryMessage(t *testing.T) {
	e := &excellon.ParseError{Line: 9, Code: excellon.CodeAsymmetricPattern, UncoveredLines: []int{9, 11}}
	assert.Equal(t, "line 9: ASYMMETRIC_PATTERN (uncovered lines [9 11])", e.Error())
}
