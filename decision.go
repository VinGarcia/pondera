// Package pondera scores decisions by weighted values: a single decider ranks
// M options across K weighted criteria. Each criterion is either bounded
// (score measures "how much of the attribute" on a 0-100 scale, so units can't
// smuggle weight past the declared Weight) or a raw scalar normalized across
// the options — min-max (field-relative) or zero-max (ratio-preserving); see
// Normalization. The criterion direction decides whether more of the attribute
// is better or worse, in every mode.
package pondera

import (
	"fmt"
	"sort"
	"time"
)

// Direction says how a criterion's value maps to desirability. It applies to
// every criterion: a bounded cost score of 90 ("90-much of a bad thing, e.g.
// price") contributes 10.
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

// Normalization says how a criterion's raw values become a 0-100 contribution.
// The variant names spell out the anchors: what maps to 0 and what maps to 100.
type Normalization int

const (
	// Bounded (the default) means the score is already a 0-100 measure of the
	// attribute; it is clamped, never rescaled.
	Bounded Normalization = iota
	// MinMax means the score is a raw scalar anchored to the field: the
	// smallest value across options maps to 0, the largest to 100. Maximally
	// discriminating, but it erases magnitude — two near-identical values
	// still land on 0 and 100.
	MinMax
	// ZeroMax means the score is a raw scalar anchored at zero: 0 maps to 0,
	// the largest value across options to 100 (v/max·100). Preserves ratios —
	// near-identical values contribute near-identically — so it requires a
	// scale where zero means "none of the attribute" (price, km, tokens);
	// negative values are rejected.
	ZeroMax
)

// MarshalText renders the normalization as its TOML keyword so a saved
// decision reads in words, not an opaque enum integer.
func (n Normalization) MarshalText() ([]byte, error) {
	switch n {
	case Bounded:
		return []byte("bounded"), nil
	case MinMax:
		return []byte("min-max"), nil
	case ZeroMax:
		return []byte("zero-max"), nil
	default:
		return nil, fmt.Errorf("pondera: unknown normalization %d", int(n))
	}
}

// UnmarshalText parses a normalization keyword, rejecting anything but the
// known words so a typo in a hand-edited file fails loudly instead of silently
// defaulting to bounded.
func (n *Normalization) UnmarshalText(text []byte) error {
	switch string(text) {
	case "bounded":
		*n = Bounded
	case "min-max":
		*n = MinMax
	case "zero-max":
		*n = ZeroMax
	default:
		return fmt.Errorf("pondera: unknown normalization %q", text)
	}
	return nil
}

// Criterion is one weighted value the decision is scored against.
type Criterion struct {
	Name      string    `toml:"name"`
	Weight    float64   `toml:"weight"` // must be > 0
	Direction Direction `toml:"direction"`
	// Normalization switches the scoring mode; absent in TOML means Bounded.
	Normalization Normalization `toml:"normalization,omitempty"`
}

// Option is one alternative being ranked; Scores maps criterion name to the
// option's value for that criterion (a 0-100 quality-% or a raw scalar).
type Option struct {
	Name   string             `toml:"name"`
	Scores map[string]float64 `toml:"scores"`
}

// Decision is a single decider's weighted comparison of options.
type Decision struct {
	Title    string      `toml:"title"`
	Criteria []Criterion `toml:"criteria"`
	Options  []Option    `toml:"options"`
	// LockedAt records when the criteria and weights were frozen. Its zero value
	// means the decision is still open for criteria/weight edits and closed to
	// options; see the builder methods in build.go for the ordering discipline.
	LockedAt time.Time `toml:"locked_at,omitempty"`
}

// Result is one option's desirability index, in 0-100 (weights normalized).
type Result struct {
	Option string
	Score  float64
}

// Rank computes each option's desirability and returns the options ordered from
// most to least desirable (stable on ties). It errors on an empty criteria set,
// a non-positive weight, or an option missing a score for any criterion — the
// engine never silently treats a missing value as zero.
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

	bounds, err := d.normalizationSpans()
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

// span holds the min and max raw value of a normalized criterion across options.
type span struct{ min, max float64 }

// normalizationSpans precomputes the value range of every non-bounded
// criterion so a single option's contribution can be normalized against the
// whole field. It also rejects negative values on zero-max criteria: on a
// zero-anchored scale a negative is bad data, not a valid case.
func (d Decision) normalizationSpans() (map[string]span, error) {
	bounds := make(map[string]span)
	for _, c := range d.Criteria {
		if c.Normalization == Bounded {
			continue
		}
		var s span
		seen := false
		for _, o := range d.Options {
			v, ok := o.Scores[c.Name]
			if !ok {
				return nil, fmt.Errorf("pondera: option %q missing score for criterion %q", o.Name, c.Name)
			}
			if c.Normalization == ZeroMax && v < 0 {
				return nil, fmt.Errorf("pondera: option %q has negative value %g on zero-max criterion %q", o.Name, v, c.Name)
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
		bounds[c.Name] = s
	}
	return bounds, nil
}

// contribution maps one option's value for a criterion to a 0-100 desirability
// contribution, applying the criterion's normalization and direction.
func contribution(c Criterion, v float64, s span) float64 {
	var norm float64
	switch c.Normalization {
	case MinMax:
		// Anchors: field min → 0, field max → 100. When every option ties the
		// criterion cannot discriminate, so it contributes neutrally (50).
		if s.max == s.min {
			norm = 50
		} else {
			norm = (v - s.min) / (s.max - s.min) * 100
		}
	case ZeroMax:
		// Anchors: 0 → 0, field max → 100. All-zero fields tie, same neutral 50.
		if s.max == 0 {
			norm = 50
		} else {
			norm = v / s.max * 100
		}
	default:
		// Bounded: the score is already on the 0-100 scale; clamp only.
		norm = clamp(v, 0, 100)
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
