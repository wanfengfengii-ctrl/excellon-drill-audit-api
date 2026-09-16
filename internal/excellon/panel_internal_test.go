package excellon

import (
	"math/rand"
	"testing"

	"github.com/shopspring/decimal"
)

// TestAuditPanel_RandomizedTilings builds random panels that ARE exact
// tilings (random rotation, random overlapping offsets, duplicate holes)
// and requires the audit to succeed; then it perturbs one hole and
// requires all four rotations to fail. It guards the greedy deduction
// against panics, non-termination and count-conservation bugs.
func TestAuditPanel_RandomizedTilings(t *testing.T) {
	rng := rand.New(rand.NewSource(20260916))
	for trial := 0; trial < 400; trial++ {
		// Random template: 1..5 holes on a small grid, duplicates allowed.
		tn := 1 + rng.Intn(5)
		template := make([]Hole, tn)
		for i := range template {
			template[i] = Hole{
				Line:     6 + i,
				Diameter: decimal.NewFromInt(1),
				X:        decimal.NewFromInt(int64(rng.Intn(5))),
				Y:        decimal.NewFromInt(int64(rng.Intn(5))),
			}
		}
		angle := panelAngles[rng.Intn(4)]
		rotated := rotateHoles(template, angle)
		// Random instances: 1..4 offsets, possibly overlapping.
		in := 1 + rng.Intn(4)
		var panel []Hole
		line := 6
		for k := 0; k < in; k++ {
			ox := decimal.NewFromInt(int64(rng.Intn(9) - 4))
			oy := decimal.NewFromInt(int64(rng.Intn(9) - 4))
			for _, th := range rotated {
				panel = append(panel, Hole{
					Line:     line,
					Diameter: th.Diameter,
					X:        th.X.Add(ox),
					Y:        th.Y.Add(oy),
				})
				line++
			}
		}
		// Shuffle the panel body order; matching must not depend on it.
		rng.Shuffle(len(panel), func(i, j int) { panel[i], panel[j] = panel[j], panel[i] })

		layouts, failures := AuditPanel(template, panel)
		if len(layouts) == 0 {
			t.Fatalf("trial %d: tiling rejected, failures=%v", trial, failures)
		}
		// Count conservation: every successful angle must account for
		// exactly the panel's hole count.
		found := false
		for _, l := range layouts {
			if len(l.Instances)*tn != len(panel) {
				t.Fatalf("trial %d: angle %d conserves %d holes, panel has %d",
					trial, l.Angle, len(l.Instances)*tn, len(panel))
			}
			found = true
		}
		if !found {
			t.Fatalf("trial %d: no layout", trial)
		}

		// Perturb: append one extra hole at the half-integer position
		// (1000.5, 1000.5). Every template and panel coordinate is an
		// integer and quarter-turn rotations preserve integrality, so
		// no instance of any rotation can ever cover it; all four
		// rotations must fail with one evidence record each.
		half, _ := decimal.NewFromString("1000.5")
		panel = append(panel, Hole{
			Line:     line,
			Diameter: decimal.NewFromInt(1),
			X:        half,
			Y:        half,
		})
		layouts, failures = AuditPanel(template, panel)
		if tn == 1 {
			// A single-hole template tiles every panel, so even the
			// perturbed panel is a valid layout.
			if len(layouts) == 0 {
				t.Fatalf("trial %d: single-hole template must tile any panel", trial)
			}
			continue
		}
		if len(layouts) != 0 {
			t.Fatalf("trial %d: perturbed panel accepted: %+v", trial, layouts)
		}
		if len(failures) != 4 {
			t.Fatalf("trial %d: want 4 failure records, got %d", trial, len(failures))
		}
		for i, f := range failures {
			if f.Angle != panelAngles[i] {
				t.Fatalf("trial %d: failure %d angle = %d", trial, i, f.Angle)
			}
			if f.AnchorLine == 0 || f.MissingLine == 0 {
				t.Fatalf("trial %d: failure %d missing evidence: %+v", trial, i, f)
			}
			if f.OffsetX == "" || f.OffsetY == "" {
				t.Fatalf("trial %d: failure %d missing offset: %+v", trial, i, f)
			}
		}
	}
}
