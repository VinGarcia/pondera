package pondera

import "testing"

// TestBuildFlow walks the disciplined path — declare criteria and weights, lock,
// then add options and scores — and confirms the built decision ranks correctly
// and records that it was locked.
func TestBuildFlow(t *testing.T) {
	var d Decision
	d.Title = "commute"

	if err := d.AddCriterion(Criterion{Name: "safety", Weight: 1}); err != nil {
		t.Fatalf("AddCriterion safety: %v", err)
	}
	if err := d.AddCriterion(Criterion{Name: "price", Weight: 1, Direction: Cost}); err != nil {
		t.Fatalf("AddCriterion price: %v", err)
	}
	// Reweight before lock: safety matters more than price.
	if err := d.SetWeight("safety", 3); err != nil {
		t.Fatalf("SetWeight: %v", err)
	}

	if d.Locked() {
		t.Fatal("decision should be open before Lock")
	}
	if err := d.Lock(); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if !d.Locked() || d.LockedAt.IsZero() {
		t.Fatal("decision should be locked with a LockedAt after Lock")
	}

	// Options only after lock. "train" is safer, "car" is pricier.
	if err := d.AddOption(Option{Name: "train", Scores: map[string]float64{"safety": 90}}); err != nil {
		t.Fatalf("AddOption train: %v", err)
	}
	if err := d.SetScore("train", "price", 30); err != nil {
		t.Fatalf("SetScore train price: %v", err)
	}
	if err := d.AddOption(Option{Name: "car", Scores: map[string]float64{"safety": 50, "price": 80}}); err != nil {
		t.Fatalf("AddOption car: %v", err)
	}

	got, err := d.Rank()
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	// weighted mean, weights safety=3 price=1 (cost inverts): train=(90*3+70*1)/4=85,
	// car=(50*3+20*1)/4=42.5. train wins.
	if len(got) != 2 || got[0].Option != "train" {
		t.Fatalf("expected train first, got %+v", got)
	}
}

// TestBuildGuards checks every ordering / integrity guard rejects out-of-order or
// malformed mutations. Each case mutates a decision seeded to the right state and
// asserts the guard returns an error.
func TestBuildGuards(t *testing.T) {
	// open builds a decision with one criterion, still unlocked.
	open := func() *Decision {
		d := &Decision{}
		if err := d.AddCriterion(Criterion{Name: "safety", Weight: 1}); err != nil {
			t.Fatalf("seed AddCriterion: %v", err)
		}
		return d
	}
	// locked builds a locked decision with one criterion.
	locked := func() *Decision {
		d := open()
		if err := d.Lock(); err != nil {
			t.Fatalf("seed Lock: %v", err)
		}
		return d
	}

	cases := []struct {
		name string
		act  func() error
	}{
		{"add option before lock", func() error {
			return open().AddOption(Option{Name: "x", Scores: map[string]float64{"safety": 1}})
		}},
		{"set score before lock", func() error {
			return open().SetScore("x", "safety", 1)
		}},
		{"add criterion after lock", func() error {
			return locked().AddCriterion(Criterion{Name: "price", Weight: 1})
		}},
		{"set weight after lock", func() error {
			return locked().SetWeight("safety", 2)
		}},
		{"lock with no criteria", func() error {
			return (&Decision{}).Lock()
		}},
		{"lock twice", func() error {
			return locked().Lock()
		}},
		{"duplicate criterion", func() error {
			return open().AddCriterion(Criterion{Name: "safety", Weight: 1})
		}},
		{"empty criterion name", func() error {
			return open().AddCriterion(Criterion{Name: "", Weight: 1})
		}},
		{"non-positive weight on add", func() error {
			return open().AddCriterion(Criterion{Name: "price", Weight: 0})
		}},
		{"non-positive weight on set", func() error {
			return open().SetWeight("safety", -1)
		}},
		{"set weight unknown criterion", func() error {
			return open().SetWeight("ghost", 2)
		}},
		{"duplicate option", func() error {
			d := locked()
			if err := d.AddOption(Option{Name: "x", Scores: map[string]float64{"safety": 1}}); err != nil {
				t.Fatalf("seed AddOption: %v", err)
			}
			return d.AddOption(Option{Name: "x", Scores: map[string]float64{"safety": 2}})
		}},
		{"empty option name", func() error {
			return locked().AddOption(Option{Name: "", Scores: map[string]float64{"safety": 1}})
		}},
		{"option scores unknown criterion", func() error {
			return locked().AddOption(Option{Name: "x", Scores: map[string]float64{"ghost": 1}})
		}},
		{"set score unknown criterion", func() error {
			d := locked()
			if err := d.AddOption(Option{Name: "x", Scores: map[string]float64{"safety": 1}}); err != nil {
				t.Fatalf("seed AddOption: %v", err)
			}
			return d.SetScore("x", "ghost", 1)
		}},
		{"set score unknown option", func() error {
			return locked().SetScore("ghost", "safety", 1)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.act(); err == nil {
				t.Fatalf("%s: expected error, got nil", tc.name)
			}
		})
	}
}

// TestSetScoreThenRank confirms SetScore can complete an option added with no
// scores, and that Rank still enforces completeness when a score is left unset.
func TestSetScoreThenRank(t *testing.T) {
	d := &Decision{}
	if err := d.AddCriterion(Criterion{Name: "a", Weight: 1}); err != nil {
		t.Fatal(err)
	}
	if err := d.AddCriterion(Criterion{Name: "b", Weight: 1}); err != nil {
		t.Fatal(err)
	}
	if err := d.Lock(); err != nil {
		t.Fatal(err)
	}
	if err := d.AddOption(Option{Name: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetScore("x", "a", 70); err != nil {
		t.Fatal(err)
	}
	// b still unset: Rank must reject the incomplete field, not treat b as 0.
	if _, err := d.Rank(); err == nil {
		t.Fatal("expected Rank to reject option missing a score")
	}
	if err := d.SetScore("x", "b", 40); err != nil {
		t.Fatal(err)
	}
	got, err := d.Rank()
	if err != nil {
		t.Fatalf("Rank after completing scores: %v", err)
	}
	if len(got) != 1 || got[0].Score != 55 {
		t.Fatalf("expected x score 55, got %+v", got)
	}
}
