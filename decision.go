// Package pondera scores decisions by weighted values: a single decider ranks
// M options across K weighted criteria. Each criterion is either a
// quality-percent guideline (score already means "how good, 0-100") or an
// absolute scalar (raw value min-max normalized across the options, with the
// criterion direction deciding whether higher is better or worse).
package pondera

import (
	"fmt"
	"sort"
)

// Direction says how a criterion's raw value maps to desirability. It is only
// consulted for absolute criteria; for percent-guideline criteria the score
// already embeds the sense of "good", so direction is inert.
type Direction int

const (
	// Benefit means a higher raw value is more desirable.
	Benefit Direction = iota
	// Cost means a higher raw value is less desirable (subtractive).
	Cost
)

// Criterion is one weighted value the decision is scored against.
type Criterion struct {
	Name      string
	Weight    float64   // must be > 0
	Direction Direction // active only when Absolute is true
	// Absolute switches the scoring mode. When false (default), the option's
	// score is a 0-100 quality percentage used as-is. When true, the score is a
	// raw scalar that is min-max normalized across the options before weighting.
	Absolute bool
}

// Option is one alternative being ranked; Scores maps criterion name to the
// option's value for that criterion (a 0-100 quality-% or a raw scalar).
type Option struct {
	Name   string
	Scores map[string]float64
}

// Decision is a single decider's weighted comparison of options.
type Decision struct {
	Title    string
	Criteria []Criterion
	Options  []Option
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
		// Percent guideline: the score already means "how good", used as-is.
		return clamp(v, 0, 100)
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
