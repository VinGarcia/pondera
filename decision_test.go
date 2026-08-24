package pondera

import (
	"math"
	"testing"
)

const eps = 1e-9

func TestRank(t *testing.T) {
	tests := []struct {
		name     string
		decision Decision
		want     []Result // in expected order
	}{
		{
			// Bounded benefit criteria: scores are 0-100 measures of the
			// attribute. Weighted index = (80*3 + 60*1.5) / 4.5 = 73.333...
			name: "bounded benefit weighted sum",
			decision: Decision{
				Title: "carro",
				Criteria: []Criterion{
					{Name: "seguranca", Weight: 3},
					{Name: "preco", Weight: 1.5},
				},
				Options: []Option{
					{Name: "A", Scores: map[string]float64{"seguranca": 80, "preco": 60}},
				},
			},
			want: []Result{{Option: "A", Score: 330.0 / 4.5}},
		},
		{
			// Bounded cost: direction applies to bounded criteria too — a 0-100
			// price-ness of 90 ("very expensive") contributes 100-90=10, so the
			// cheaper option wins. Index barato = (70*2 + (100-20)*1)/3 = 73.33.
			name: "bounded cost inverts direction",
			decision: Decision{
				Criteria: []Criterion{
					{Name: "conforto", Weight: 2},
					{Name: "preco", Weight: 1, Direction: Cost},
				},
				Options: []Option{
					{Name: "caro", Scores: map[string]float64{"conforto": 80, "preco": 90}},
					{Name: "barato", Scores: map[string]float64{"conforto": 70, "preco": 20}},
				},
			},
			want: []Result{
				{Option: "barato", Score: (70*2 + 80) / 3.0},
				{Option: "caro", Score: (80*2 + 10) / 3.0},
			},
		},
		{
			// Min-max cost: raw prices min-max normalized, then inverted
			// because higher price is worse. Cheapest option (100k -> norm 0 ->
			// cost 100) beats the priciest (200k -> norm 100 -> cost 0).
			name: "min-max cost inverts direction",
			decision: Decision{
				Criteria: []Criterion{
					{Name: "preco", Weight: 1, Direction: Cost, Range: NewRange(MinAnchor(), MaxAnchor())},
				},
				Options: []Option{
					{Name: "barato", Scores: map[string]float64{"preco": 100000}},
					{Name: "caro", Scores: map[string]float64{"preco": 200000}},
				},
			},
			want: []Result{{Option: "barato", Score: 100}, {Option: "caro", Score: 0}},
		},
		{
			// Min-max benefit: higher raw value is better, no inversion.
			name: "min-max benefit keeps direction",
			decision: Decision{
				Criteria: []Criterion{
					{Name: "autonomia", Weight: 1, Direction: Benefit, Range: NewRange(MinAnchor(), MaxAnchor())},
				},
				Options: []Option{
					{Name: "curta", Scores: map[string]float64{"autonomia": 300}},
					{Name: "longa", Scores: map[string]float64{"autonomia": 600}},
				},
			},
			want: []Result{{Option: "longa", Score: 100}, {Option: "curta", Score: 0}},
		},
		{
			// Min-max criterion where all options tie: cannot discriminate, so
			// it contributes a neutral 50 to every option regardless of direction.
			name: "min-max tie is neutral",
			decision: Decision{
				Criteria: []Criterion{
					{Name: "preco", Weight: 1, Direction: Cost, Range: NewRange(MinAnchor(), MaxAnchor())},
				},
				Options: []Option{
					{Name: "X", Scores: map[string]float64{"preco": 50000}},
					{Name: "Y", Scores: map[string]float64{"preco": 50000}},
				},
			},
			want: []Result{{Option: "X", Score: 50}, {Option: "Y", Score: 50}},
		},
		{
			// Mixed percent + min-max, and the winner must sort first even
			// though it is declared second.
			name: "mixed criteria ranked descending",
			decision: Decision{
				Criteria: []Criterion{
					{Name: "conforto", Weight: 2}, // percent
					{Name: "preco", Weight: 1, Direction: Cost, Range: NewRange(MinAnchor(), MaxAnchor())},
				},
				Options: []Option{
					// conforto 40; preco 100k -> cost 100. index=(40*2+100*1)/3=60
					{Name: "confortavel-caro", Scores: map[string]float64{"conforto": 40, "preco": 200000}},
					// conforto 60; preco 100k -> cost 100. index=(60*2+100*1)/3=73.33
					{Name: "bom-barato", Scores: map[string]float64{"conforto": 60, "preco": 100000}},
				},
			},
			want: []Result{
				{Option: "bom-barato", Score: (60*2 + 100) / 3.0},
				{Option: "confortavel-caro", Score: (40*2 + 0) / 3.0},
			},
		},
		{
			// Zero-max preserves ratios: anchors are 0 -> 0 and field max -> 100,
			// so 300 vs 600 contribute 50 vs 100 (min-max would give 0 vs 100).
			name: "zero-max preserves magnitude",
			decision: Decision{
				Criteria: []Criterion{
					{Name: "autonomia", Weight: 1, Direction: Benefit, Range: NewRange(FixedAnchor(0), MaxAnchor())},
				},
				Options: []Option{
					{Name: "curta", Scores: map[string]float64{"autonomia": 300}},
					{Name: "longa", Scores: map[string]float64{"autonomia": 600}},
				},
			},
			want: []Result{{Option: "longa", Score: 100}, {Option: "curta", Score: 50}},
		},
		{
			// Zero-max cost: near-identical prices contribute near-identically —
			// 100 vs 101 inverts to ~0.99 vs 0, not the min-max 100 vs 0.
			name: "zero-max cost inverts direction",
			decision: Decision{
				Criteria: []Criterion{
					{Name: "preco", Weight: 1, Direction: Cost, Range: NewRange(FixedAnchor(0), MaxAnchor())},
				},
				Options: []Option{
					{Name: "barato", Scores: map[string]float64{"preco": 100}},
					{Name: "caro", Scores: map[string]float64{"preco": 101}},
				},
			},
			want: []Result{
				{Option: "barato", Score: 100 - 100.0/101.0*100},
				{Option: "caro", Score: 0},
			},
		},
		{
			// Zero-max where every option is at zero: the field cannot
			// discriminate, so it contributes the same neutral 50 as a min-max tie.
			name: "zero-max all-zero is neutral",
			decision: Decision{
				Criteria: []Criterion{
					{Name: "custo", Weight: 1, Direction: Cost, Range: NewRange(FixedAnchor(0), MaxAnchor())},
				},
				Options: []Option{
					{Name: "X", Scores: map[string]float64{"custo": 0}},
					{Name: "Y", Scores: map[string]float64{"custo": 0}},
				},
			},
			want: []Result{{Option: "X", Score: 50}, {Option: "Y", Score: 50}},
		},
		{
			// A custom fixed window rescales inside it and clamps outside it:
			// with [40, 80], 30 -> 0, 60 -> 50, 90 -> 100.
			name: "custom fixed range interpolates and clamps",
			decision: Decision{
				Criteria: []Criterion{
					{Name: "nota", Weight: 1, Range: NewRange(FixedAnchor(40), FixedAnchor(80))},
				},
				Options: []Option{
					{Name: "abaixo", Scores: map[string]float64{"nota": 30}},
					{Name: "meio", Scores: map[string]float64{"nota": 60}},
					{Name: "acima", Scores: map[string]float64{"nota": 90}},
				},
			},
			want: []Result{
				{Option: "acima", Score: 100},
				{Option: "meio", Score: 50},
				{Option: "abaixo", Score: 0},
			},
		},
		{
			// Bounded scores are clamped into 0-100 so a stray out-of-range
			// value cannot blow past the index bounds.
			name: "bounded score clamped",
			decision: Decision{
				Criteria: []Criterion{{Name: "x", Weight: 1}},
				Options: []Option{
					{Name: "over", Scores: map[string]float64{"x": 150}},
					{Name: "under", Scores: map[string]float64{"x": -20}},
				},
			},
			want: []Result{{Option: "over", Score: 100}, {Option: "under", Score: 0}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.decision.Rank()
			if err != nil {
				t.Fatalf("Rank() unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Rank() returned %d results, want %d: %+v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i].Option != tt.want[i].Option {
					t.Errorf("result[%d].Option = %q, want %q (full: %+v)", i, got[i].Option, tt.want[i].Option, got)
				}
				if math.Abs(got[i].Score-tt.want[i].Score) > eps {
					t.Errorf("result[%d] (%s).Score = %v, want %v", i, got[i].Option, got[i].Score, tt.want[i].Score)
				}
			}
		})
	}
}

func TestRankErrors(t *testing.T) {
	tests := []struct {
		name     string
		decision Decision
	}{
		{
			name:     "no criteria",
			decision: Decision{Options: []Option{{Name: "A"}}},
		},
		{
			name: "non-positive weight",
			decision: Decision{
				Criteria: []Criterion{{Name: "x", Weight: 0}},
				Options:  []Option{{Name: "A", Scores: map[string]float64{"x": 10}}},
			},
		},
		{
			name: "missing score",
			decision: Decision{
				Criteria: []Criterion{{Name: "x", Weight: 1}},
				Options:  []Option{{Name: "A", Scores: map[string]float64{}}},
			},
		},
		{
			// [0, "max"] over an all-negative field resolves to hi < lo: the
			// data contradicts the anchors — bad data, not a valid case.
			name: "zero-max anchors contradicted by all-negative values",
			decision: Decision{
				Criteria: []Criterion{{Name: "x", Weight: 1, Range: NewRange(FixedAnchor(0), MaxAnchor())}},
				Options:  []Option{{Name: "A", Scores: map[string]float64{"x": -5}}},
			},
		},
		{
			// The mirror case: ["min", 0] over an all-positive field also
			// resolves to hi < lo — the inverted-resolution check is symmetric,
			// not special-cased to which anchor is dynamic.
			name: "min-zero anchors contradicted by all-positive values",
			decision: Decision{
				Criteria: []Criterion{{Name: "x", Weight: 1, Range: NewRange(MinAnchor(), FixedAnchor(0))}},
				Options:  []Option{{Name: "A", Scores: map[string]float64{"x": 5}}},
			},
		},
		{
			// A fixed pair with hi <= lo can never map values: config error.
			name: "fixed range with hi below lo",
			decision: Decision{
				Criteria: []Criterion{{Name: "x", Weight: 1, Range: NewRange(FixedAnchor(80), FixedAnchor(40))}},
				Options:  []Option{{Name: "A", Scores: map[string]float64{"x": 50}}},
			},
		},
		{
			name: "missing score on normalized criterion",
			decision: Decision{
				Criteria: []Criterion{{Name: "x", Weight: 1, Range: NewRange(MinAnchor(), MaxAnchor())}},
				Options: []Option{
					{Name: "A", Scores: map[string]float64{"x": 10}},
					{Name: "B", Scores: map[string]float64{}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.decision.Rank(); err == nil {
				t.Fatalf("Rank() expected an error, got nil")
			}
		})
	}
}
