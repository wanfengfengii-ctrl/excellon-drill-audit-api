// Package excellon parses a restricted subset of Excellon drill files.
//
// Accepted grammar (one statement per line, no blank lines):
//
//	M48                       (line 1)
//	METRIC                    (line 2)
//	TnnC<diameter>            (one or more, header only)
//	%                         (header terminator)
//	Tnn                       (body only, selects a defined tool)
//	X<coord>Y<coord>          (body only, exactly one X and one Y)
//	M30                       (final line)
//
// Numbers are canonical decimals: an integer part of 0 or a positive
// integer without leading zeros, optionally followed by one to three
// fractional digits. A leading minus is allowed only for non-zero
// coordinates (never for diameters).
//
// ParseWithClearance optionally audits the minimum edge-to-edge spacing
// between every pair of drilled holes; Parse skips that audit.
package excellon

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// Error codes returned by Parse. They are stable API values.
const (
	CodeLineOrder         = "LINE_ORDER"
	CodeInvalidNumber     = "INVALID_NUMBER"
	CodeDuplicateTool     = "DUPLICATE_TOOL"
	CodeUndefinedTool     = "UNDEFINED_TOOL"
	CodeNoHoles           = "NO_HOLES"
	CodeHoleClearance     = "HOLE_CLEARANCE"
	CodeAsymmetricPattern = "ASYMMETRIC_PATTERN"
)

// ParseError identifies the first invalid line of a document.
type ParseError struct {
	Line int    // 1-based line number
	Code string // one of the Code* constants
	// ConflictLine is set only for CodeHoleClearance: the 1-based line of
	// the earlier hole whose clearance zone the hole at Line violates.
	ConflictLine int
	// UncoveredLines is set only for CodeAsymmetricPattern: the 1-based
	// lines of the holes left without a rotation partner, in body line
	// order after deterministic pairing. Line is the first of them.
	UncoveredLines []int
}

func (e *ParseError) Error() string {
	switch {
	case e.Code == CodeHoleClearance:
		return fmt.Sprintf("line %d: %s (conflicts with line %d)", e.Line, e.Code, e.ConflictLine)
	case len(e.UncoveredLines) > 0:
		return fmt.Sprintf("line %d: %s (uncovered lines %v)", e.Line, e.Code, e.UncoveredLines)
	default:
		return fmt.Sprintf("line %d: %s", e.Line, e.Code)
	}
}

// SymmetryCenter is the pivot of the optional half-turn audit: every hole
// at point p must be matched by a hole drilled with the same tool at the
// rotated point 2*center - p.
type SymmetryCenter struct {
	X, Y decimal.Decimal
}

// ToolCount reports how many holes were drilled with one tool.
type ToolCount struct {
	Tool  string `json:"tool"`
	Holes int    `json:"holes"`
}

// Report is the statistics of a successfully parsed file.
type Report struct {
	Tools []ToolCount `json:"tools"`
	Total int         `json:"total_holes"`
	MinX  string      `json:"min_x"`
	MinY  string      `json:"min_y"`
	MaxX  string      `json:"max_x"`
	MaxY  string      `json:"max_y"`
}

type phase int

const (
	phaseHeader phase = iota
	phaseBody
)

// Parse validates the complete document and, only when every line is
// valid and at least one hole exists, returns its statistics. The first
// error encountered in textual order wins; within a line the order is
// structural shape, number lexicon, then duplicate/undefined tool.
func Parse(text string) (*Report, error) {
	return ParseWithClearance(text, nil)
}

// ParseWithClearance behaves like Parse and, when minClearance is
// non-nil, additionally audits hole-to-hole spacing: every validated
// hole's center must be at least (own radius + earlier radius +
// minClearance) away from every previously validated hole, compared in
// body line order. The audit runs only after the line's structure,
// number lexicon and tool reference have all passed, so every
// pre-existing error keeps its priority; the first conflicting pair
// aborts the parse with CodeHoleClearance, reporting the current line
// and the earlier ConflictLine. A nil minClearance disables the audit
// entirely.
func ParseWithClearance(text string, minClearance *decimal.Decimal) (*Report, error) {
	return ParseWithAudits(text, minClearance, nil)
}

// ParseWithAudits behaves like ParseWithClearance and, when center is
// non-nil, additionally verifies half-turn (180-degree) rotational
// symmetry about that center, keyed by tool and exact normalized
// coordinates. The symmetry audit runs only once the whole document has
// passed syntax, number lexicon, tool references and the clearance
// audit, so every pre-existing error keeps its priority; a pattern that
// cannot be paired under rotation fails with
// CodeAsymmetricPattern. A nil center disables the symmetry audit.
func ParseWithAudits(text string, minClearance *decimal.Decimal, center *SymmetryCenter) (*Report, error) {
	lines := splitLines(text)

	diameters := make(map[string]decimal.Decimal)
	var toolOrder []string
	counts := make(map[string]int)
	selected := ""
	holeCount := 0
	// Validated holes in body line order; only populated while at least
	// one post-parse audit is active. The clearance audit reads radii,
	// the symmetry audit reads tool and position.
	var holes []placedHole
	auditsOn := minClearance != nil || center != nil

	ph := phaseHeader
	var minX, minY, maxX, maxY decimal.Decimal

	for i, line := range lines {
		lineNo := i + 1

		if ph == phaseHeader {
			switch {
			case i == 0:
				if line != "M48" {
					return nil, &ParseError{Line: lineNo, Code: CodeLineOrder}
				}
			case i == 1:
				if line != "METRIC" {
					return nil, &ParseError{Line: lineNo, Code: CodeLineOrder}
				}
			case line == "%":
				// The header requires one or more tool definitions.
				if len(toolOrder) == 0 {
					return nil, &ParseError{Line: lineNo, Code: CodeLineOrder}
				}
				ph = phaseBody
			default:
				tool, diam, ok := parseToolDef(line)
				if !ok {
					return nil, &ParseError{Line: lineNo, Code: CodeLineOrder}
				}
				// Structural shape matched: lexicon/range of the diameter
				// wins over the duplicate-tool check.
				if !isCanonicalNumber(diam, false, false) {
					return nil, &ParseError{Line: lineNo, Code: CodeInvalidNumber}
				}
				if _, dup := diameters[tool]; dup {
					return nil, &ParseError{Line: lineNo, Code: CodeDuplicateTool}
				}
				d, _ := decimal.NewFromString(diam)
				diameters[tool] = d
				toolOrder = append(toolOrder, tool)
			}
			continue
		}

		// Body.
		if line == "M30" {
			if lineNo != len(lines) {
				return nil, &ParseError{Line: lineNo, Code: CodeLineOrder}
			}
			if holeCount == 0 {
				return nil, &ParseError{Line: lineNo, Code: CodeNoHoles}
			}
			if center != nil {
				if uncovered := asymmetricLines(holes, *center); len(uncovered) > 0 {
					return nil, &ParseError{Line: uncovered[0], Code: CodeAsymmetricPattern, UncoveredLines: uncovered}
				}
			}
			return buildReport(toolOrder, counts, holeCount, minX, minY, maxX, maxY), nil
		}

		if tool, ok := parseToolSelect(line); ok {
			if _, defined := diameters[tool]; !defined {
				return nil, &ParseError{Line: lineNo, Code: CodeUndefinedTool}
			}
			selected = tool
			continue
		}

		xs, ys, ok := parseHole(line)
		if !ok {
			return nil, &ParseError{Line: lineNo, Code: CodeLineOrder}
		}
		// Both numbers share one lexicon check; the X axis is read first.
		if !isCanonicalNumber(xs, true, true) || !isCanonicalNumber(ys, true, true) {
			return nil, &ParseError{Line: lineNo, Code: CodeInvalidNumber}
		}
		if selected == "" {
			return nil, &ParseError{Line: lineNo, Code: CodeUndefinedTool}
		}
		x, _ := decimal.NewFromString(xs)
		y, _ := decimal.NewFromString(ys)
		if auditsOn {
			h := placedHole{line: lineNo, tool: selected, x: x, y: y}
			if minClearance != nil {
				h.radius = diameters[selected].Div(two)
				for _, prev := range holes {
					// Squared comparison only: conflict iff
					//   dx² + dy² < (r₁ + r₂ + minClearance)²
					// Exact decimal arithmetic (halving a diameter is exact
					// in decimal) avoids square roots and rounding error;
					// equality — exactly tangent holes — passes.
					need := prev.radius.Add(h.radius).Add(*minClearance)
					dx := x.Sub(prev.x)
					dy := y.Sub(prev.y)
					if dx.Mul(dx).Add(dy.Mul(dy)).Cmp(need.Mul(need)) < 0 {
						return nil, &ParseError{Line: lineNo, Code: CodeHoleClearance, ConflictLine: prev.line}
					}
				}
			}
			holes = append(holes, h)
		}
		counts[selected]++
		if holeCount == 0 {
			minX, maxX, minY, maxY = x, x, y, y
		} else {
			if x.Cmp(minX) < 0 {
				minX = x
			}
			if x.Cmp(maxX) > 0 {
				maxX = x
			}
			if y.Cmp(minY) < 0 {
				minY = y
			}
			if y.Cmp(maxY) > 0 {
				maxY = y
			}
		}
		holeCount++
	}

	// Ran out of lines before reaching M30 (includes an empty document).
	return nil, &ParseError{Line: len(lines) + 1, Code: CodeLineOrder}
}

// placedHole is a validated hole kept, in body line order, for the
// post-parse audits. radius is only populated while the clearance audit
// is active.
type placedHole struct {
	line   int
	tool   string
	x, y   decimal.Decimal
	radius decimal.Decimal
}

// asymKey identifies one class of hole: the selected tool and the exact
// normalized (three-decimal, sign-preserving) coordinates.
type asymKey struct {
	tool string
	x, y string
}

// asymmetricLines consumes rotatable pairs of validated holes and
// returns the lines of holes left without a partner, in body line
// order. Each hole at point p (tool t) pairs with a hole of the same
// tool at 2*center - p; holes exactly at the center rotate to
// themselves and therefore must occur an even number of times — they
// pair with each other under a half-turn and an odd count leaves the
// last occurrence uncovered. Duplicate holes are fungible: partners
// are always the earliest still-available occurrence of the rotated
// key (FIFO in body line order), so the uncovered source lines are
// deterministic regardless of textual pairing order.
func asymmetricLines(holes []placedHole, center SymmetryCenter) []int {
	// Body-order indexes of every hole per class key, and the front of
	// each queue while partners are consumed.
	indexes := make(map[asymKey][]int)
	for i, h := range holes {
		k := keyOf(h)
		indexes[k] = append(indexes[k], i)
	}
	consumed := make([]bool, len(holes))
	front := make(map[asymKey]int, len(indexes))

	// Self-mapping classes (points exactly at the center) pair among
	// themselves in body order: 1st with 2nd, 3rd with 4th, …; an odd
	// count leaves the final occurrence uncovered.
	for k, ids := range indexes {
		if !keyIsCenter(k, center) {
			continue
		}
		for j := 0; j+1 < len(ids); j += 2 {
			consumed[ids[j]] = true
			consumed[ids[j+1]] = true
		}
	}

	cx := center.X.Mul(two)
	cy := center.Y.Mul(two)

	var uncovered []int
	for i, h := range holes {
		if consumed[i] {
			// Already consumed as another hole's partner.
			continue
		}
		k := keyOf(h)
		if keyIsCenter(k, center) {
			// The lone survivor of an odd-sized center class.
			uncovered = append(uncovered, h.line)
			continue
		}
		rk := asymKey{
			tool: h.tool,
			x:    formatDecimal(cx.Sub(h.x)),
			y:    formatDecimal(cy.Sub(h.y)),
		}
		// Consume the earliest unconsumed rotated occurrence, which may
		// appear later in the file; advancing the FIFO first is what
		// keeps duplicate pairing deterministic.
		for front[rk] < len(indexes[rk]) {
			j := indexes[rk][front[rk]]
			front[rk]++
			if !consumed[j] {
				consumed[i] = true
				consumed[j] = true
				break
			}
		}
		if !consumed[i] {
			uncovered = append(uncovered, h.line)
		}
	}
	return uncovered
}

func keyOf(h placedHole) asymKey {
	return asymKey{tool: h.tool, x: formatDecimal(h.x), y: formatDecimal(h.y)}
}

// keyIsCenter reports whether the key's coordinates are numerically
// equal to the symmetry center (exact decimal equality, so "0" and
// "0.000" are the same class).
func keyIsCenter(k asymKey, center SymmetryCenter) bool {
	x, errX := decimal.NewFromString(k.x)
	y, errY := decimal.NewFromString(k.y)
	return errX == nil && errY == nil && x.Equal(center.X) && y.Equal(center.Y)
}

// two is the exact divisor turning a tool diameter into its radius.
var two = decimal.NewFromInt(2)

// ParseClearance validates a min_clearance query value against the same
// canonical decimal contract used inside drill files — no sign, zero
// allowed (the clearance only has to be >= 0) — and returns its exact
// decimal value.
func ParseClearance(s string) (decimal.Decimal, bool) {
	if !isCanonicalNumber(s, false, true) {
		return decimal.Decimal{}, false
	}
	d, _ := decimal.NewFromString(s)
	return d, true
}

// ParseSymmetryCenter validates a symmetry_center query value as
// "x,y", each half using the file coordinate lexicon (canonical
// decimal, sign and zero allowed), and returns the exact center.
func ParseSymmetryCenter(s string) (SymmetryCenter, bool) {
	comma := strings.IndexByte(s, ',')
	// Exactly one comma, both halves non-empty.
	if comma < 0 || comma != strings.LastIndexByte(s, ',') {
		return SymmetryCenter{}, false
	}
	xs, ys := s[:comma], s[comma+1:]
	if !isCanonicalNumber(xs, true, true) || !isCanonicalNumber(ys, true, true) {
		return SymmetryCenter{}, false
	}
	x, _ := decimal.NewFromString(xs)
	y, _ := decimal.NewFromString(ys)
	return SymmetryCenter{X: x, Y: y}, true
}

// splitLines splits on LF. A single trailing LF is treated as the line
// terminator of the last line (standard text-file convention) rather
// than an extra blank line; doubled trailing LFs or interior blank lines
// still surface as invalid lines. CR is never stripped, so CRLF fails.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	if strings.HasSuffix(text, "\n") {
		text = text[:len(text)-1]
	}
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// parseToolDef matches TnnC<diameter>, nn in 01..99.
func parseToolDef(line string) (tool, diam string, ok bool) {
	if len(line) < 5 || line[0] != 'T' || line[3] != 'C' {
		return "", "", false
	}
	tool = line[1:3]
	if !isToolNumber(tool) {
		return "", "", false
	}
	diam = line[4:]
	if diam == "" {
		return "", "", false
	}
	return tool, diam, true
}

// parseToolSelect matches a bare Tnn line, nn in 01..99.
func parseToolSelect(line string) (string, bool) {
	if len(line) != 3 || line[0] != 'T' {
		return "", false
	}
	tool := line[1:3]
	if !isToolNumber(tool) {
		return "", false
	}
	return tool, true
}

// parseHole matches X<coord>Y<coord> with exactly one X and one Y axis,
// in that order, each carrying a non-empty numeric word.
func parseHole(line string) (x, y string, ok bool) {
	if len(line) < 3 || line[0] != 'X' {
		return "", "", false
	}
	i := 1
	xStart := i
	for i < len(line) && line[i] != 'Y' {
		if line[i] == 'X' {
			return "", "", false
		}
		i++
	}
	if i == len(line) || i == xStart {
		return "", "", false
	}
	x = line[xStart:i]
	i++ // consume Y
	yStart := i
	if yStart >= len(line) {
		return "", "", false
	}
	rest := line[yStart:]
	if strings.ContainsRune(rest, 'X') || strings.ContainsRune(rest, 'Y') {
		return "", "", false
	}
	return x, rest, true
}

func isToolNumber(s string) bool {
	return len(s) == 2 && s[0] >= '0' && s[0] <= '9' && s[1] >= '0' && s[1] <= '9' && s != "00"
}

// isCanonicalNumber checks the canonical decimal contract:
// optional minus, then "0" or a no-leading-zero positive integer,
// then an optional 1..3 digit fraction. A minus is only accepted when
// allowNegative is set and the magnitude is non-zero; a zero magnitude
// is only accepted when allowZero is set (tool diameters must be > 0).
func isCanonicalNumber(s string, allowNegative, allowZero bool) bool {
	if s == "" {
		return false
	}
	neg := false
	if s[0] == '-' {
		if !allowNegative {
			return false
		}
		neg = true
		s = s[1:]
	}
	if s == "" {
		return false
	}

	intPart := s
	fracPart := ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart = s[:dot]
		fracPart = s[dot+1:]
		if len(fracPart) < 1 || len(fracPart) > 3 {
			return false
		}
		for _, c := range fracPart {
			if c < '0' || c > '9' {
				return false
			}
		}
	}

	switch {
	case len(intPart) == 1 && intPart[0] == '0':
	case len(intPart) >= 1 && intPart[0] >= '1' && intPart[0] <= '9':
		for i := 1; i < len(intPart); i++ {
			if intPart[i] < '0' || intPart[i] > '9' {
				return false
			}
		}
	default:
		return false
	}

	if isZeroMagnitude(intPart, fracPart) {
		// Diameters must be strictly positive; negative zero is
		// never a well-formed number.
		if !allowZero || neg {
			return false
		}
	}
	return true
}

func isZeroMagnitude(intPart, fracPart string) bool {
	if intPart != "0" {
		return false
	}
	for i := 0; i < len(fracPart); i++ {
		if fracPart[i] != '0' {
			return false
		}
	}
	return true
}

func buildReport(
	toolOrder []string,
	counts map[string]int,
	holeCount int,
	minX, minY, maxX, maxY decimal.Decimal,
) *Report {
	tools := make([]ToolCount, 0, len(toolOrder))
	for _, t := range toolOrder {
		if n := counts[t]; n > 0 {
			tools = append(tools, ToolCount{Tool: "T" + t, Holes: n})
		}
	}
	return &Report{
		Tools: tools,
		Total: holeCount,
		MinX:  formatDecimal(minX),
		MinY:  formatDecimal(minY),
		MaxX:  formatDecimal(maxX),
		MaxY:  formatDecimal(maxY),
	}
}

// formatDecimal renders exactly three fractional digits without
// scientific notation, preserving the sign of negative coordinates.
func formatDecimal(d decimal.Decimal) string {
	return d.StringFixed(3)
}
