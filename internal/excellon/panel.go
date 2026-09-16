package excellon

import (
	"sort"

	"github.com/shopspring/decimal"
)

// panelAngles lists the template rotations audited, in the order they
// are tried: 0, 90, 180 and 270 degrees counterclockwise.
var panelAngles = [4]int{0, 90, 180, 270}

// PanelInstance is one located copy of the template inside the panel:
// the translation applied to the rotated template and the resulting
// absolute coordinates of the template anchor hole.
type PanelInstance struct {
	OffsetX string `json:"offset_x"`
	OffsetY string `json:"offset_y"`
	AnchorX string `json:"anchor_x"`
	AnchorY string `json:"anchor_y"`
}

// PanelLayout groups every instance found at one rotation angle.
type PanelLayout struct {
	Angle     int             `json:"angle"`
	Instances []PanelInstance `json:"instances"`
}

// OrientationFailure records why one rotation angle could not tile the
// panel: the panel line of the anchor hole the failed iteration started
// from, the first template line (in template body line order) whose
// translated position had no remaining panel hole, and the offset in
// use. It is the evidence that the panel has extra or missing holes
// under that rotation.
type OrientationFailure struct {
	Angle       int    `json:"angle"`
	AnchorLine  int    `json:"anchor_line"`
	MissingLine int    `json:"missing_line"`
	OffsetX     string `json:"offset_x"`
	OffsetY     string `json:"offset_y"`
}

// AuditPanel audits whether the panel is an exact step-and-repeat
// tiling of the template under one of the four quarter-turn rotations
// (0, 90, 180, 270 degrees counterclockwise), tried in that order.
// template and panel must each hold at least one hole, as guaranteed by
// ParseHoles.
//
// For each rotation the template anchor is its smallest hole in
// (diameter, X, Y) numeric order. Repeatedly, the offset is derived as
// the smallest remaining panel hole in the same order minus the rotated
// anchor, and one copy of the template is deducted from the panel
// multiset — keyed by (diameter, transformed X, Y), so duplicate holes,
// same-diameter holes from different tools and overlapping instances
// are all handled purely by counting — in template body line order. A
// rotation succeeds when the deduction runs until the panel is exactly
// exhausted; every located copy becomes one instance, and instances are
// sorted by anchor coordinates. A rotation fails at the first template
// hole with no remaining panel match, recording the anchor's panel
// line, that template line and the offset in use.
//
// The result is deterministic: one layout per feasible rotation in
// angle order, or — when all four rotations fail — exactly one failure
// record per angle.
func AuditPanel(template, panel []Hole) (layouts []PanelLayout, failures []OrientationFailure) {
	for _, angle := range panelAngles {
		layout, failure := matchOrientation(rotateHoles(template, angle), panel, angle)
		if failure != nil {
			failures = append(failures, *failure)
		} else {
			layouts = append(layouts, *layout)
		}
	}
	return layouts, failures
}

// matchOrientation runs the greedy step-and-repeat deduction for one
// rotated view of the template. It returns either the layout with every
// located instance or the single failure that ended the deduction.
func matchOrientation(template []Hole, panel []Hole, angle int) (*PanelLayout, *OrientationFailure) {
	// The anchor is the template's smallest hole in (diameter, X, Y)
	// numeric order; the line tie-break keeps the choice deterministic.
	anchor := template[0]
	for _, h := range template[1:] {
		if holeLess(h, anchor) {
			anchor = h
		}
	}

	pool := newPanelPool(panel)
	var placed []placedInstance
	for {
		entry, anchorLine, ok := pool.minRemaining()
		if !ok {
			// The panel is exactly exhausted by whole template copies.
			return &PanelLayout{Angle: angle, Instances: formatInstances(placed)}, nil
		}
		offX := entry.x.Sub(anchor.X)
		offY := entry.y.Sub(anchor.Y)
		for _, th := range template {
			if !pool.consume(th.Diameter, th.X.Add(offX), th.Y.Add(offY)) {
				return nil, &OrientationFailure{
					Angle:       angle,
					AnchorLine:  anchorLine,
					MissingLine: th.Line,
					OffsetX:     formatDecimal(offX),
					OffsetY:     formatDecimal(offY),
				}
			}
		}
		placed = append(placed, placedInstance{offX: offX, offY: offY, anchorX: entry.x, anchorY: entry.y})
	}
}

// placedInstance is a located template copy before string formatting.
type placedInstance struct {
	offX, offY       decimal.Decimal
	anchorX, anchorY decimal.Decimal
}

// formatInstances sorts instances by anchor coordinates (the greedy
// deduction already yields them in that order; the stable sort makes
// the contract explicit) and renders all numbers as fixed
// three-decimal strings.
func formatInstances(placed []placedInstance) []PanelInstance {
	sort.SliceStable(placed, func(i, j int) bool {
		if c := placed[i].anchorX.Cmp(placed[j].anchorX); c != 0 {
			return c < 0
		}
		return placed[i].anchorY.Cmp(placed[j].anchorY) < 0
	})
	instances := make([]PanelInstance, len(placed))
	for i, p := range placed {
		instances[i] = PanelInstance{
			OffsetX: formatDecimal(p.offX),
			OffsetY: formatDecimal(p.offY),
			AnchorX: formatDecimal(p.anchorX),
			AnchorY: formatDecimal(p.anchorY),
		}
	}
	return instances
}

// holeLess orders holes by (diameter, X, Y) numeric value, breaking
// ties by source line so the anchor choice is deterministic.
func holeLess(a, b Hole) bool {
	if c := a.Diameter.Cmp(b.Diameter); c != 0 {
		return c < 0
	}
	if c := a.X.Cmp(b.X); c != 0 {
		return c < 0
	}
	if c := a.Y.Cmp(b.Y); c != 0 {
		return c < 0
	}
	return a.Line < b.Line
}

// rotateHoles returns the template holes with each coordinate rotated
// counterclockwise by angle degrees (a multiple of 90), keeping the
// template body line order. Rotations are exact sign swaps on the
// decimal coordinates, so no rounding is involved.
func rotateHoles(holes []Hole, angle int) []Hole {
	out := make([]Hole, len(holes))
	for i, h := range holes {
		switch angle {
		case 90:
			h.X, h.Y = h.Y.Neg(), h.X
		case 180:
			h.X, h.Y = h.X.Neg(), h.Y.Neg()
		case 270:
			h.X, h.Y = h.Y, h.X.Neg()
		}
		out[i] = h
	}
	return out
}

// poolKey identifies one class of remaining panel holes: the tool
// diameter and the exact normalized (three-decimal) coordinates. Tool
// numbers are deliberately not part of the key, so same-diameter holes
// drilled with different tools share one count.
type poolKey struct {
	diam, x, y string
}

func poolKeyOf(diam, x, y decimal.Decimal) poolKey {
	return poolKey{formatDecimal(diam), formatDecimal(x), formatDecimal(y)}
}

// poolEntry is one key class of the panel multiset: the class decimals
// and the still-unconsumed source lines in ascending order.
type poolEntry struct {
	diam, x, y decimal.Decimal
	lines      []int
	head       int // index of the first unconsumed line
}

// panelPool is the remaining-hole multiset of one orientation match.
// Key classes are consumed from the front in (diameter, X, Y) sorted
// order, so the minimum remaining class is found in amortized constant
// time.
type panelPool struct {
	order []*poolEntry // unique key classes, sorted by (diameter, X, Y)
	byKey map[poolKey]*poolEntry
	min   int // first index in order that may still hold unconsumed holes
}

func newPanelPool(holes []Hole) *panelPool {
	byKey := make(map[poolKey]*poolEntry, len(holes))
	for _, h := range holes {
		k := poolKeyOf(h.Diameter, h.X, h.Y)
		e := byKey[k]
		if e == nil {
			e = &poolEntry{diam: h.Diameter, x: h.X, y: h.Y}
			byKey[k] = e
		}
		// Holes arrive in body line order, so each lines slice is
		// ascending without an extra sort.
		e.lines = append(e.lines, h.Line)
	}
	order := make([]*poolEntry, 0, len(byKey))
	for _, e := range byKey {
		order = append(order, e)
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if c := a.diam.Cmp(b.diam); c != 0 {
			return c < 0
		}
		if c := a.x.Cmp(b.x); c != 0 {
			return c < 0
		}
		return a.y.Cmp(b.y) < 0
	})
	return &panelPool{order: order, byKey: byKey}
}

// minRemaining returns the smallest key class still holding unconsumed
// holes, together with its earliest remaining source line. ok is false
// once the panel is exhausted.
func (p *panelPool) minRemaining() (e *poolEntry, line int, ok bool) {
	for p.min < len(p.order) && p.order[p.min].head == len(p.order[p.min].lines) {
		p.min++
	}
	if p.min == len(p.order) {
		return nil, 0, false
	}
	e = p.order[p.min]
	return e, e.lines[e.head], true
}

// consume deducts one hole of the given class. It reports false when no
// hole of that class remains.
func (p *panelPool) consume(diam, x, y decimal.Decimal) bool {
	e := p.byKey[poolKeyOf(diam, x, y)]
	if e == nil || e.head == len(e.lines) {
		return false
	}
	e.head++
	return true
}
