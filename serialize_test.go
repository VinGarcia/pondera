package pondera

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// sampleDecision is a locked decision exercising every field that must survive
// serialization: bounded benefit, bounded cost, a min-max cost criterion,
// multiple options with float scores, and a non-zero LockedAt stamp. The time
// is a fixed UTC instant so the round-trip is deterministic (no monotonic clock
// or local-zone artifacts from time.Now).
func sampleDecision() Decision {
	return Decision{
		Title:    "Qual carro comprar",
		LockedAt: time.Date(2026, 8, 21, 15, 0, 0, 0, time.UTC),
		Criteria: []Criterion{
			{Name: "seguranca", Weight: 3, Direction: Benefit, Range: NewRange(FixedAnchor(0), FixedAnchor(100))},
			{Name: "preco", Weight: 1.5, Direction: Cost, Range: NewRange(FixedAnchor(0), FixedAnchor(100))},
			{Name: "km_rodados", Weight: 1.2, Direction: Cost, Range: NewRange(MinAnchor(), MaxAnchor())},
		},
		Options: []Option{
			{Name: "Modelo A", Scores: map[string]float64{"seguranca": 80, "preco": 60, "km_rodados": 40000}},
			{Name: "Modelo B", Scores: map[string]float64{"seguranca": 55, "preco": 30, "km_rodados": 90000}},
		},
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	orig := sampleDecision()
	path := filepath.Join(t.TempDir(), "carro.toml")

	if err := Save(path, orig); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Time carries a location/monotonic reading that reflect.DeepEqual would
	// reject, so compare it with Equal and the rest field-by-field.
	if !got.LockedAt.Equal(orig.LockedAt) {
		t.Errorf("LockedAt: got %v, want %v", got.LockedAt, orig.LockedAt)
	}
	if got.Title != orig.Title {
		t.Errorf("Title: got %q, want %q", got.Title, orig.Title)
	}
	if !reflect.DeepEqual(got.Criteria, orig.Criteria) {
		t.Errorf("Criteria: got %+v, want %+v", got.Criteria, orig.Criteria)
	}
	if !reflect.DeepEqual(got.Options, orig.Options) {
		t.Errorf("Options: got %+v, want %+v", got.Options, orig.Options)
	}

	// The point of persistence is that the decision the file encodes ranks
	// identically to the one that was saved.
	wantRank, err := orig.Rank()
	if err != nil {
		t.Fatalf("orig.Rank: %v", err)
	}
	gotRank, err := got.Rank()
	if err != nil {
		t.Fatalf("got.Rank: %v", err)
	}
	if !reflect.DeepEqual(gotRank, wantRank) {
		t.Errorf("Rank after round-trip: got %+v, want %+v", gotRank, wantRank)
	}
}

func TestMarshalIsStableAndReadable(t *testing.T) {
	d := sampleDecision()
	data, err := Marshal(d)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Directions serialize as words, not enum integers, and ranges as their
	// anchor arrays — including the explicit [0, 100] default.
	toml := string(data)
	if !strings.Contains(toml, `direction = "benefit"`) || !strings.Contains(toml, `direction = "cost"`) {
		t.Errorf("expected word directions in output, got:\n%s", toml)
	}
	if !strings.Contains(toml, `range = ["min", "max"]`) || !strings.Contains(toml, `range = [0, 100]`) {
		t.Errorf("expected anchor-array ranges in output, got:\n%s", toml)
	}

	// Re-encoding the decoded value reproduces the bytes exactly (map keys and
	// field order are deterministic), so files don't churn under save/load.
	round, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	data2, err := Marshal(round)
	if err != nil {
		t.Fatalf("Marshal round: %v", err)
	}
	if string(data2) != string(data) {
		t.Errorf("marshal not byte-stable:\nfirst:\n%s\nsecond:\n%s", data, data2)
	}
}

func TestRoundTripOpenDecision(t *testing.T) {
	// An open (unlocked) decision has a zero LockedAt; it must round-trip as
	// still-open, not gain a spurious timestamp.
	open := Decision{
		Title:    "rascunho",
		Criteria: []Criterion{{Name: "a", Weight: 1, Direction: Benefit}},
	}
	data, err := Marshal(open)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "locked_at") {
		t.Errorf("open decision should omit locked_at, got:\n%s", data)
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Locked() {
		t.Errorf("round-tripped open decision reports locked; LockedAt=%v", got.LockedAt)
	}
}

func TestUnmarshalErrors(t *testing.T) {
	tests := []struct {
		name string
		toml string
	}{
		{
			name: "unknown direction keyword",
			toml: "title = \"x\"\n[[criteria]]\nname = \"a\"\nweight = 1\ndirection = \"neutral\"\n",
		},
		{
			name: "unknown range keyword",
			toml: "title = \"x\"\n[[criteria]]\nname = \"a\"\nweight = 1\ndirection = \"benefit\"\nrange = [0, \"avg\"]\n",
		},
		{
			name: "range with wrong arity",
			toml: "title = \"x\"\n[[criteria]]\nname = \"a\"\nweight = 1\ndirection = \"benefit\"\nrange = [0, 50, 100]\n",
		},
		{
			name: "unknown key typo",
			toml: "title = \"x\"\nweght = 2\n",
		},
		{
			name: "malformed toml",
			toml: "title = = broken",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Unmarshal([]byte(tt.toml)); err == nil {
				t.Errorf("expected error for %s, got nil", tt.name)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.toml")); err == nil {
		t.Error("expected error loading missing file, got nil")
	}
}
