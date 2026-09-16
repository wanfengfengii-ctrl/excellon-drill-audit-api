package excellon_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"drillapi/internal/excellon"
)

// lTemplate is an L-shaped three-hole template (lines 6-8: (0,0),
// (2,0), (0,1)) whose four quarter-turn rotations are pairwise
// distinct, so a panel built from one rotation matches exactly that
// angle.
const lTemplate = "M48\nMETRIC\nT01C0.500\n%\nT01\nX0Y0\nX2Y0\nX0Y1\nM30\n"

func mustHoles(t *testing.T, text string) []excellon.Hole {
	t.Helper()
	hs, err := excellon.ParseHoles(text)
	require.NoError(t, err)
	return hs
}

func TestParseHoles_RetainsLineDiameterAndCoordinates(t *testing.T) {
	in := "M48\nMETRIC\nT01C0.300\nT02C1.500\n%\nT01\nX1.000Y2.000\nX-0.500Y2.000\nT02\nX10Y-3.25\nM30\n"
	hs, err := excellon.ParseHoles(in)
	require.NoError(t, err)
	require.Len(t, hs, 3)

	d03 := decimal.RequireFromString("0.300")
	d15 := decimal.RequireFromString("1.500")
	want := []excellon.Hole{
		{Line: 7, Diameter: d03, X: decimal.RequireFromString("1.000"), Y: decimal.RequireFromString("2.000")},
		{Line: 8, Diameter: d03, X: decimal.RequireFromString("-0.500"), Y: decimal.RequireFromString("2.000")},
		{Line: 10, Diameter: d15, X: decimal.RequireFromString("10"), Y: decimal.RequireFromString("-3.25")},
	}
	require.Len(t, hs, len(want))
	for i, w := range want {
		assert.Equal(t, w.Line, hs[i].Line, "hole %d line", i)
		assert.True(t, w.Diameter.Equal(hs[i].Diameter), "hole %d diameter", i)
		assert.True(t, w.X.Equal(hs[i].X), "hole %d x", i)
		assert.True(t, w.Y.Equal(hs[i].Y), "hole %d y", i)
	}
}

func TestParseHoles_ErrorParityWithParse(t *testing.T) {
	cases := []string{
		"GARBAGE\n",
		"M48\nMETRIC\nT01C0\n%\nT01\nX1Y1\nM30\n",
		"M48\nMETRIC\nT01C0.1\nT01C0.2\n%\nT01\nX1Y1\nM30\n",
		"M48\nMETRIC\nT01C0.1\n%\nT09\nX1Y1\nM30\n",
		"M48\nMETRIC\nT01C0.1\n%\nM30\n",
		"M48\nMETRIC\nT01C0.1\n%\nT01\nX01Y1\nM30\n",
	}
	for _, in := range cases {
		_, wantErr := excellon.Parse(in)
		require.Error(t, wantErr, "input %q", in)
		_, gotErr := excellon.ParseHoles(in)
		require.Error(t, gotErr, "input %q", in)
		assert.Equal(t, wantErr, gotErr, "input %q", in)
	}
}

func TestAuditPanel_SingleInstance(t *testing.T) {
	// The panel is exactly the template translated by (10,10).
	panel := "M48\nMETRIC\nT01C0.500\n%\nT01\nX10Y10\nX12Y10\nX10Y11\nM30\n"
	layouts, failures := excellon.AuditPanel(mustHoles(t, lTemplate), mustHoles(t, panel))
	// One rotation matches; the other three report their failure.
	require.Len(t, failures, 3)
	require.Len(t, layouts, 1)
	assert.Equal(t, 0, layouts[0].Angle)
	require.Len(t, layouts[0].Instances, 1)
	assert.Equal(t, excellon.PanelInstance{
		OffsetX: "10.000", OffsetY: "10.000",
		AnchorX: "10.000", AnchorY: "10.000",
	}, layouts[0].Instances[0])
}

func TestAuditPanel_MultiInstanceSortedByAnchor(t *testing.T) {
	// Three copies of the L template at offsets (10,5), (20,3), (30,8).
	panel := "M48\nMETRIC\nT01C0.500\n%\nT01\n" +
		"X10Y5\nX12Y5\nX10Y6\n" +
		"X20Y3\nX22Y3\nX20Y4\n" +
		"X30Y8\nX32Y8\nX30Y9\n" +
		"M30\n"
	layouts, failures := excellon.AuditPanel(mustHoles(t, lTemplate), mustHoles(t, panel))
	require.Len(t, failures, 3)
	require.Len(t, layouts, 1)
	assert.Equal(t, 0, layouts[0].Angle)
	require.Len(t, layouts[0].Instances, 3)
	assert.Equal(t, []excellon.PanelInstance{
		{OffsetX: "10.000", OffsetY: "5.000", AnchorX: "10.000", AnchorY: "5.000"},
		{OffsetX: "20.000", OffsetY: "3.000", AnchorX: "20.000", AnchorY: "3.000"},
		{OffsetX: "30.000", OffsetY: "8.000", AnchorX: "30.000", AnchorY: "8.000"},
	}, layouts[0].Instances)
}

func TestAuditPanel_RotationRecognition(t *testing.T) {
	cases := []struct {
		name   string
		panel  string
		angle  int
		anchor [2]string
	}{
		// Each panel is the L template rotated by the case angle and
		// translated by (10,10); only that angle may match.
		{"0 degrees", "M48\nMETRIC\nT01C0.500\n%\nT01\nX10Y10\nX12Y10\nX10Y11\nM30\n", 0, [2]string{"10.000", "10.000"}},
		{"90 degrees", "M48\nMETRIC\nT01C0.500\n%\nT01\nX10Y10\nX10Y12\nX9Y10\nM30\n", 90, [2]string{"9.000", "10.000"}},
		{"180 degrees", "M48\nMETRIC\nT01C0.500\n%\nT01\nX10Y10\nX8Y10\nX10Y9\nM30\n", 180, [2]string{"8.000", "10.000"}},
		{"270 degrees", "M48\nMETRIC\nT01C0.500\n%\nT01\nX10Y10\nX10Y8\nX11Y10\nM30\n", 270, [2]string{"10.000", "8.000"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			layouts, failures := excellon.AuditPanel(mustHoles(t, lTemplate), mustHoles(t, tc.panel))
			require.Len(t, failures, 3, "the other three rotations must fail")
			require.Len(t, layouts, 1, "exactly one rotation must match")
			assert.Equal(t, tc.angle, layouts[0].Angle)
			require.Len(t, layouts[0].Instances, 1)
			inst := layouts[0].Instances[0]
			assert.Equal(t, "10.000", inst.OffsetX)
			assert.Equal(t, "10.000", inst.OffsetY)
			assert.Equal(t, tc.anchor[0], inst.AnchorX)
			assert.Equal(t, tc.anchor[1], inst.AnchorY)
		})
	}
}

func TestAuditPanel_OverlappingDuplicatesConserveCounts(t *testing.T) {
	// The template drills (0,0) twice and (1,0) once (lines 6-8). The
	// panel holds two overlapping copies at offsets (0,0) and (1,0):
	// (0,0)x2, (1,0)x3, (2,0)x1 — the (1,0) count of 3 is 1+2.
	template := "M48\nMETRIC\nT01C0.500\n%\nT01\nX0Y0\nX0Y0\nX1Y0\nM30\n"
	panel := "M48\nMETRIC\nT01C0.500\n%\nT01\nX0Y0\nX0Y0\nX1Y0\nX1Y0\nX1Y0\nX2Y0\nM30\n"
	layouts, failures := excellon.AuditPanel(mustHoles(t, template), mustHoles(t, panel))
	require.Len(t, failures, 3)
	require.Len(t, layouts, 1)
	assert.Equal(t, 0, layouts[0].Angle)
	require.Len(t, layouts[0].Instances, 2)
	assert.Equal(t, []excellon.PanelInstance{
		{OffsetX: "0.000", OffsetY: "0.000", AnchorX: "0.000", AnchorY: "0.000"},
		{OffsetX: "1.000", OffsetY: "0.000", AnchorX: "1.000", AnchorY: "0.000"},
	}, layouts[0].Instances)
}

func TestAuditPanel_SameDiameterDifferentToolsShareCounts(t *testing.T) {
	// The template drills its three holes with two tools of equal
	// diameter; the panel drills all three with a single tool of that
	// diameter. Matching is keyed by diameter, not tool number, so the
	// L-shaped instance is found at angle 0.
	template := "M48\nMETRIC\nT01C0.500\nT02C0.500\n%\nT01\nX0Y0\nT02\nX2Y0\nX0Y1\nM30\n"
	panel := "M48\nMETRIC\nT01C0.500\n%\nT01\nX10Y5\nX12Y5\nX10Y6\nM30\n"
	layouts, failures := excellon.AuditPanel(mustHoles(t, template), mustHoles(t, panel))
	require.Len(t, failures, 3)
	require.Len(t, layouts, 1)
	assert.Equal(t, 0, layouts[0].Angle)
	require.Len(t, layouts[0].Instances, 1)
	assert.Equal(t, excellon.PanelInstance{
		OffsetX: "10.000", OffsetY: "5.000",
		AnchorX: "10.000", AnchorY: "5.000",
	}, layouts[0].Instances[0])
}

// twoHoleTemplate drills (0,0) and (2,0) on lines 6-7.
const twoHoleTemplate = "M48\nMETRIC\nT01C0.500\n%\nT01\nX0Y0\nX2Y0\nM30\n"

func TestAuditPanel_ExtraHoleFailsAllFourRotations(t *testing.T) {
	// One clean instance at (10,5) plus an extra hole at (99,99) on
	// line 8: every rotation fails, and the failure record locates the
	// anchor panel line, the first missing template line and the
	// offset in use.
	panel := "M48\nMETRIC\nT01C0.500\n%\nT01\nX10Y5\nX12Y5\nX99Y99\nM30\n"
	layouts, failures := excellon.AuditPanel(mustHoles(t, twoHoleTemplate), mustHoles(t, panel))
	require.Empty(t, layouts)
	require.Len(t, failures, 4)
	assert.Equal(t, []excellon.OrientationFailure{
		{Angle: 0, AnchorLine: 8, MissingLine: 7, OffsetX: "99.000", OffsetY: "99.000"},
		{Angle: 90, AnchorLine: 6, MissingLine: 7, OffsetX: "10.000", OffsetY: "5.000"},
		{Angle: 180, AnchorLine: 8, MissingLine: 6, OffsetX: "101.000", OffsetY: "99.000"},
		{Angle: 270, AnchorLine: 6, MissingLine: 6, OffsetX: "10.000", OffsetY: "7.000"},
	}, failures)
}

func TestAuditPanel_MissingHoleFailsAllFourRotations(t *testing.T) {
	// The panel holds only the first hole of the instance at (10,5);
	// the (12,5) hole is missing.
	panel := "M48\nMETRIC\nT01C0.500\n%\nT01\nX10Y5\nM30\n"
	layouts, failures := excellon.AuditPanel(mustHoles(t, twoHoleTemplate), mustHoles(t, panel))
	require.Empty(t, layouts)
	require.Len(t, failures, 4)
	assert.Equal(t, []excellon.OrientationFailure{
		{Angle: 0, AnchorLine: 6, MissingLine: 7, OffsetX: "10.000", OffsetY: "5.000"},
		{Angle: 90, AnchorLine: 6, MissingLine: 7, OffsetX: "10.000", OffsetY: "5.000"},
		{Angle: 180, AnchorLine: 6, MissingLine: 6, OffsetX: "12.000", OffsetY: "5.000"},
		{Angle: 270, AnchorLine: 6, MissingLine: 6, OffsetX: "10.000", OffsetY: "7.000"},
	}, failures)
}

func TestAuditPanel_PanelHoleOrderDoesNotChangeLayouts(t *testing.T) {
	// The same nine holes as the multi-instance panel, listed in a
	// scrambled body order: the layouts carry no panel lines and must
	// be identical.
	ordered := "M48\nMETRIC\nT01C0.500\n%\nT01\n" +
		"X10Y5\nX12Y5\nX10Y6\nX20Y3\nX22Y3\nX20Y4\nX30Y8\nX32Y8\nX30Y9\nM30\n"
	scrambled := "M48\nMETRIC\nT01C0.500\n%\nT01\n" +
		"X30Y9\nX20Y4\nX10Y6\nX32Y8\nX22Y3\nX12Y5\nX30Y8\nX20Y3\nX10Y5\nM30\n"
	// Layouts carry no panel lines, so they must be identical; the
	// failure records do reference panel lines and may differ.
	want, _ := excellon.AuditPanel(mustHoles(t, lTemplate), mustHoles(t, ordered))
	got, _ := excellon.AuditPanel(mustHoles(t, lTemplate), mustHoles(t, scrambled))
	require.Len(t, want, 1)
	assert.Equal(t, want, got)
}
