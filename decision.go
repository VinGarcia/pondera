// Package pondera scores decisions by weighted values: a single decider ranks
// M options across K weighted criteria. Every criterion turns its raw values
// into a 0-100 contribution through a Range — two anchors saying what maps to
// 0 and what maps to 100. Anchors are fixed numbers or the keywords
// "min"/"max" (the smallest/largest value across the options), so [0, 100] is
// a plain bounded quality score (the default), ["min", "max"] is
// field-relative, [0, "max"] is zero-anchored ratio-preserving, and [40, 80]
// rescales a custom window. The criterion direction decides whether more of
// the attribute is better or worse.
package pondera

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Direction says how a criterion's value maps to desirability. It applies to
// every criterion: a cost contribution of 90 ("90-much of a bad thing, e.g.
// price") counts as 10.
type Direction int

const (
	// Benefit means a higher raw value is more desirable.
	Benefit Direction = iota
	// Cost means a higher raw value is less desirable (subtractive).
	Cost
)

// MarshalText renders the direction as its TOML keyword ("benefit"/"cost") so a
// saved decision reads in words, not an opaque enum integer.
func (dir Direction) MarshalText() ([]byte, error) {
	switch dir {
	case Benefit:
		return []byte("benefit"), nil
	case Cost:
		return []byte("cost"), nil
	default:
		return nil, fmt.Errorf("pondera: unknown direction %d", int(dir))
	}
}

// UnmarshalText parses a direction keyword, rejecting anything but the two
// known words so a typo in a hand-edited file fails loudly instead of silently
// defaulting to benefit.
func (dir *Direction) UnmarshalText(text []byte) error {
	switch string(text) {
	case "benefit":
		*dir = Benefit
	case "cost":
		*dir = Cost
	default:
		return fmt.Errorf("pondera: unknown direction %q", text)
	}
	return nil
}

// Anchor is one end of a criterion's Range: a fixed number, or a keyword
// ("min"/"max") resolved against the options' values for that criterion at
// Rank time.
type Anchor struct {
	keyword string  // "" = fixed; otherwise "min" or "max"
	value   float64 // the fixed value when keyword is ""
}

// FixedAnchor returns an anchor pinned at a number.
func FixedAnchor(v float64) Anchor { return Anchor{value: v} }

// MinAnchor returns the anchor that resolves to the field's smallest value.
func MinAnchor() Anchor { return Anchor{keyword: "min"} }

// MaxAnchor returns the anchor that resolves to the field's largest value.
func MaxAnchor() Anchor { return Anchor{keyword: "max"} }

// ParseAnchor reads an anchor from its textual form: "min", "max", or a number.
func ParseAnchor(s string) (Anchor, error) {
	switch s {
	case "min":
		return MinAnchor(), nil
	case "max":
		return MaxAnchor(), nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return Anchor{}, fmt.Errorf("pondera: anchor must be a number, %q, or %q; got %q", "min", "max", s)
	}
	return FixedAnchor(v), nil
}

// String renders the anchor the way it appears inside a TOML range.
func (a Anchor) String() string {
	if a.keyword != "" {
		return strconv.Quote(a.keyword)
	}
	return strconv.FormatFloat(a.value, 'g', -1, 64)
}

// resolve returns the anchor's numeric value against the field's span.
func (a Anchor) resolve(s span) float64 {
	switch a.keyword {
	case "min":
		return s.min
	case "max":
		return s.max
	default:
		return a.value
	}
}

// dynamic reports whether the anchor depends on the field of options.
func (a Anchor) dynamic() bool { return a.keyword != "" }

// Range is the pair of anchors a criterion's values are normalized through:
// Lo maps to contribution 0, Hi to 100, values between interpolate and values
// outside clamp. The zero value (key absent in TOML) means the default
// [0, 100].
type Range struct {
	lo, hi Anchor
	set    bool
}

// NewRange builds a range from its two anchors.
func NewRange(lo Anchor, hi Anchor) Range { return Range{lo: lo, hi: hi, set: true} }

// anchors returns the effective pair, applying the [0, 100] default.
func (r Range) anchors() (Anchor, Anchor) {
	if !r.set {
		return FixedAnchor(0), FixedAnchor(100)
	}
	return r.lo, r.hi
}

// String renders the range as it appears in TOML, e.g. `[0, "max"]`.
func (r Range) String() string {
	lo, hi := r.anchors()
	return "[" + lo.String() + ", " + hi.String() + "]"
}

// fromList decodes a two-element anchor array (numbers or the keywords
// "min"/"max"), rejecting anything else so a typo in a hand-edited file or an
// API payload fails loudly. It is shared by the TOML and JSON decoders, which
// both hand it a []interface{} of numbers and strings.
func (r *Range) fromList(list []interface{}) error {
	if len(list) != 2 {
		return fmt.Errorf("pondera: range must be a two-element array like [0, 100], got %v", list)
	}
	parse := func(e interface{}) (Anchor, error) {
		switch x := e.(type) {
		case int64:
			return FixedAnchor(float64(x)), nil
		case float64:
			return FixedAnchor(x), nil
		case string:
			if x != "min" && x != "max" {
				return Anchor{}, fmt.Errorf("pondera: unknown range keyword %q (want %q or %q)", x, "min", "max")
			}
			return Anchor{keyword: x}, nil
		default:
			return Anchor{}, fmt.Errorf("pondera: range entry must be a number, %q, or %q; got %v", "min", "max", e)
		}
	}
	lo, err := parse(list[0])
	if err != nil {
		return err
	}
	hi, err := parse(list[1])
	if err != nil {
		return err
	}
	*r = NewRange(lo, hi)
	return nil
}

// UnmarshalTOML decodes a two-element TOML array whose entries are numbers or
// the keywords "min"/"max", rejecting anything else so a typo in a hand-edited
// file fails loudly.
func (r *Range) UnmarshalTOML(v interface{}) error {
	list, ok := v.([]interface{})
	if !ok {
		return fmt.Errorf("pondera: range must be a two-element array like [0, 100], got %v", v)
	}
	return r.fromList(list)
}

// MarshalTOML renders the range as its array form; an unset range is written
// out as the explicit default [0, 100], so a saved file always shows the
// anchors in effect.
func (r Range) MarshalTOML() ([]byte, error) {
	return []byte(r.String()), nil
}

// jsonValue returns the anchor as it appears inside a JSON array: a bare number
// for a fixed anchor, or the "min"/"max" keyword string for a dynamic one.
func (a Anchor) jsonValue() interface{} {
	if a.keyword != "" {
		return a.keyword
	}
	return a.value
}

// MarshalJSON renders the range as its two-element array — the same shape TOML
// uses — so the HTTP/JSON API round-trips it. Without this the struct's fields
// are unexported and it would serialize as an empty object, hiding the range
// from the SPA. An unset range writes the explicit default [0, 100].
func (r Range) MarshalJSON() ([]byte, error) {
	lo, hi := r.anchors()
	return json.Marshal([]interface{}{lo.jsonValue(), hi.jsonValue()})
}

// UnmarshalJSON decodes the two-element array form the SPA sends. A JSON null or
// an absent field leaves the range unset (the [0, 100] default), so a client
// that never touches the range does not have to send one.
func (r *Range) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var list []interface{}
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("pondera: range must be a two-element JSON array like [0, 100]: %w", err)
	}
	return r.fromList(list)
}

// Criterion is one weighted value the decision is scored against.
type Criterion struct {
	Name      string    `toml:"name" json:"name"`
	Weight    float64   `toml:"weight" json:"weight"` // must be > 0
	Direction Direction `toml:"direction" json:"direction"`
	// Range sets the normalization anchors; absent in TOML means [0, 100].
	Range Range `toml:"range" json:"range"`
}

// Option is one alternative being ranked; Scores maps criterion name to the
// option's value for that criterion (a 0-100 quality-% or a raw scalar,
// depending on the criterion's range).
type Option struct {
	Name   string             `toml:"name" json:"name"`
	Scores map[string]float64 `toml:"scores" json:"scores"`
}

// Decision is a single decider's weighted comparison of options.
type Decision struct {
	Title string `toml:"title" json:"title"`
	// Owner is who the decision belongs to; it scopes a Store so one person's
	// decisions never appear in another's list. Persisted from the start so the
	// on-disk format never has to be migrated to add multi-owner support later.
	Owner    string      `toml:"owner,omitempty" json:"owner,omitempty"`
	Criteria []Criterion `toml:"criteria" json:"criteria"`
	Options  []Option    `toml:"options" json:"options"`
	// LockedAt records when the criteria and weights were frozen. Its zero value
	// means the decision is still open for criteria/weight edits and closed to
	// options; see the builder methods in build.go for the ordering discipline.
	LockedAt time.Time `toml:"locked_at,omitempty" json:"lockedAt,omitempty"`
}

// Result is one option's desirability index, in 0-100 (weights normalized).
type Result struct {
	Option string  `json:"option"`
	Score  float64 `json:"score"`
}

// Rank computes each option's desirability and returns the options ordered from
// most to least desirable (stable on ties). It errors on an empty criteria set,
// a non-positive weight, an invalid range, or an option missing a score for any
// criterion — the engine never silently treats a missing value as zero.
func (d Decision) Rank() ([]Result, error) {
	if len(d.Criteria) == 0 {
		return nil, fmt.Errorf("pondera: decision %q has no criteria", d.Title)
	}

	var totalWeight float64
	for _, c := range d.Criteria {
		if c.Weight <= 0 {
			return nil, fmt.Errorf("pondera: criterion %q has non-positive weight %g", c.Name, c.Weight)
		}
		totalWeight += c.Weight
	}

	bounds, err := d.resolveBounds()
	if err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(d.Options))
	for _, o := range d.Options {
		var acc float64
		for _, c := range d.Criteria {
			v, ok := o.Scores[c.Name]
			if !ok {
				return nil, fmt.Errorf("pondera: option %q missing score for criterion %q", o.Name, c.Name)
			}
			acc += contribution(c, v, bounds[c.Name]) * c.Weight
		}
		results = append(results, Result{Option: o.Name, Score: acc / totalWeight})
	}

	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results, nil
}

// span holds the min and max raw value of a criterion across options.
type span struct{ min, max float64 }

// bounds is a criterion's range with its anchors resolved to numbers.
type bounds struct{ lo, hi float64 }

// resolveBounds turns every criterion's range into numeric lo/hi, scanning the
// options' values once for the criteria whose anchors are dynamic. It rejects
// a range that cannot map values sensibly: a fixed pair with hi <= lo is a
// config error, and a dynamic pair resolving to hi < lo means the data
// contradicts the anchors (e.g. [0, "max"] over all-negative values).
func (d Decision) resolveBounds() (map[string]bounds, error) {
	resolved := make(map[string]bounds, len(d.Criteria))
	for _, c := range d.Criteria {
		lo, hi := c.Range.anchors()
		if !lo.dynamic() && !hi.dynamic() && hi.value <= lo.value {
			return nil, fmt.Errorf("pondera: criterion %q has range %s with hi <= lo", c.Name, c.Range)
		}
		var s span
		if lo.dynamic() || hi.dynamic() {
			seen := false
			for _, o := range d.Options {
				v, ok := o.Scores[c.Name]
				if !ok {
					return nil, fmt.Errorf("pondera: option %q missing score for criterion %q", o.Name, c.Name)
				}
				if !seen {
					s = span{min: v, max: v}
					seen = true
					continue
				}
				if v < s.min {
					s.min = v
				}
				if v > s.max {
					s.max = v
				}
			}
		}
		b := bounds{lo: lo.resolve(s), hi: hi.resolve(s)}
		if b.hi < b.lo {
			return nil, fmt.Errorf("pondera: criterion %q range %s resolved to [%g, %g]; the options' values contradict the anchors", c.Name, c.Range, b.lo, b.hi)
		}
		resolved[c.Name] = b
	}
	return resolved, nil
}

// contribution maps one option's value for a criterion to a 0-100 desirability
// contribution: interpolate between the resolved anchors (clamping outside
// them), then apply direction. Anchors that resolve to the same point cannot
// discriminate, so the criterion contributes neutrally (50) to every option.
func contribution(c Criterion, v float64, b bounds) float64 {
	var norm float64
	if b.hi == b.lo {
		norm = 50
	} else {
		norm = clamp((v-b.lo)/(b.hi-b.lo), 0, 1) * 100
	}
	if c.Direction == Cost {
		norm = 100 - norm
	}
	return norm
}

func clamp(v, lo, hi float64) float64 {
	switch {
	case v < lo:
		return lo
	case v > hi:
		return hi
	default:
		return v
	}
}

// ParseRange reads a range from its CLI form "lo,hi", e.g. "0,100" or
// "min,max" or "0,max".
func ParseRange(s string) (Range, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return Range{}, fmt.Errorf("pondera: range must be two comma-separated anchors like 0,100 or min,max; got %q", s)
	}
	lo, err := ParseAnchor(strings.TrimSpace(parts[0]))
	if err != nil {
		return Range{}, err
	}
	hi, err := ParseAnchor(strings.TrimSpace(parts[1]))
	if err != nil {
		return Range{}, err
	}
	return NewRange(lo, hi), nil
}
