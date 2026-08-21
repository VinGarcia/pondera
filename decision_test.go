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
			// Percent guideline: score is already "how good, 0-100"; direction
			// is inert. Weighted index = (80*3 + 60*1.5) / 4.5 = 73.333...
			name: "percent guideline weighted sum",
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
			// Absolute cost: raw prices min-max normalized, then inverted
			// because higher price is worse. Cheapest option (100k -> norm 0 ->
			// cost 100) beats the priciest (200k -> norm 100 -> cost 0).
			name: "absolute cost inverts direction",
			decision: Decision{
				Criteria: []Criterion{
					{Name: "preco", Weight: 1, Direction: Cost, Absolute: true},
				},
				Options: []Option{
					{Name: "barato", Scores: map[string]float64{"preco": 100000}},
					{Name: "caro", Scores: map[string]float64{"preco": 200000}},
				},
			},
			want: []Result{{Option: "barato", Score: 100}, {Option: "caro", Score: 0}},
		},
		{
			// Absolute benefit: higher raw value is better, no inversion.
			name: "absolute benefit keeps direction",
			decision: Decision{
				Criteria: []Criterion{
					{Name: "autonomia", Weight: 1, Direction: Benefit, Absolute: true},
				},
				Options: []Option{
					{Name: "curta", Scores: map[string]float64{"autonomia": 300}},
					{Name: "longa", Scores: map[string]float64{"autonomia": 600}},
				},
			},
			want: []Result{{Option: "longa", Score: 100}, {Option: "curta", Score: 0}},
		},
		{
			// Absolute criterion where all options tie: cannot discriminate, so
			// it contributes a neutral 50 to every option regardless of direction.
			name: "absolute tie is neutral",
			decision: Decision{
				Criteria: []Criterion{
					{Name: "preco", Weight: 1, Direction: Cost, Absolute: true},
				},
				Options: []Option{
					{Name: "X", Scores: map[string]float64{"preco": 50000}},
					{Name: "Y", Scores: map[string]float64{"preco": 50000}},
				},
			},
			want: []Result{{Option: "X", Score: 50}, {Option: "Y", Score: 50}},
		},
		{
			// Mixed percent + absolute, and the winner must sort first even
			// though it is declared second.
			name: "mixed criteria ranked descending",
			decision: Decision{
				Criteria: []Criterion{
					{Name: "conforto", Weight: 2}, // percent
					{Name: "preco", Weight: 1, Direction: Cost, Absolute: true},
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
			// Percent scores are clamped into 0-100 so a stray out-of-range
			// value cannot blow past the index bounds.
			name: "percent score clamped",
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
			name: "missing score on absolute criterion",
			decision: Decision{
				Criteria: []Criterion{{Name: "x", Weight: 1, Absolute: true}},
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
