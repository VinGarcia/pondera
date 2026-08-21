// Package pondera scores decisions by weighted values: a single decider ranks
// M options across K weighted criteria. Each criterion is either bounded
// (score measures "how much of the attribute" on a 0-100 scale, so units can't
// smuggle weight past the declared Weight) or absolute (raw scalar min-max
// normalized across the options). The criterion direction decides whether more
// of the attribute is better or worse, in both modes.
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

// Criterion is one weighted value the decision is scored against.
type Criterion struct {
	Name      string    `toml:"name"`
	Weight    float64   `toml:"weight"` // must be > 0
	Direction Direction `toml:"direction"`
	// Absolute switches the scoring mode. When false (default), the option's
	// score is a bounded 0-100 measure of the attribute. When true, the score
	// is a raw scalar that is min-max normalized across the options before
	// weighting.
	Absolute bool `toml:"absolute,omitempty"`
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

	bounds, err := d.absoluteBounds()
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

// span holds the min and max raw value of an absolute criterion across options.
type span struct{ min, max float64 }

// absoluteBounds precomputes the value range of every absolute criterion so a
// single option's contribution can be normalized against the whole field.
func (d Decision) absoluteBounds() (map[string]span, error) {
	bounds := make(map[string]span)
	for _, c := range d.Criteria {
		if !c.Absolute {
			continue
		}
		var s span
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
		bounds[c.Name] = s
	}
	return bounds, nil
}

// contribution maps one option's value for a criterion to a 0-100 desirability
// contribution, applying the criterion's mode and direction.
func contribution(c Criterion, v float64, s span) float64 {
	if !c.Absolute {
		// Bounded: the score is already on the 0-100 scale; only direction applies.
		v = clamp(v, 0, 100)
		if c.Direction == Cost {
			v = 100 - v
		}
		return v
	}
	// Absolute: min-max normalize across the field. When every option ties,
	// the criterion cannot discriminate, so it contributes neutrally (50)
	// regardless of direction.
	var norm float64
	if s.max == s.min {
		norm = 50
	} else {
		norm = (v - s.min) / (s.max - s.min) * 100
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
