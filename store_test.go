package pondera

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
)

// decisionFor builds a minimal valid decision owned by owner. The store cares
// about identity (owner+title) and round-trip fidelity, not the ranking body,
// so one criterion/option is enough to prove the payload survives.
func decisionFor(owner, title string) Decision {
	return Decision{
		Title: title,
		Owner: owner,
		Criteria: []Criterion{
			// Explicit identity range: Marshal materializes [0,100], so an
			// omitted range would round-trip as set and defeat DeepEqual — a
			// pre-existing Range quirk, orthogonal to what this test covers.
			{Name: "value", Weight: 1, Direction: Benefit, Range: NewRange(FixedAnchor(0), FixedAnchor(100))},
		},
		Options: []Option{
			{Name: "a", Scores: map[string]float64{"value": 80}},
		},
	}
}

// TestFileStoreListIsolatesByOwner is the core fatia-2 behavior: decisions are
// persisted keyed by owner, and List(owner) returns only that owner's
// decisions. A store that leaked one owner's decisions into another's list
// would be the failure this guards against.
func TestFileStoreListIsolatesByOwner(t *testing.T) {
	s := NewFileStore(t.TempDir())

	for _, d := range []Decision{
		decisionFor("alice", "lunch"),
		decisionFor("bob", "hire"),
		decisionFor("alice", "stack"),
	} {
		if err := s.Save(context.Background(), d); err != nil {
			t.Fatalf("Save(%s/%s): %v", d.Owner, d.Title, err)
		}
	}

	cases := []struct {
		owner string
		want  []string
	}{
		{"alice", []string{"lunch", "stack"}},
		{"bob", []string{"hire"}},
		{"nobody", []string{}}, // unknown owner: empty, not an error
	}
	for _, c := range cases {
		got, err := s.List(context.Background(), c.owner)
		if err != nil {
			t.Fatalf("List(%s): %v", c.owner, err)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("List(%s) = %v, want %v", c.owner, got, c.want)
		}
	}
}

// TestFileStoreRoundTrip proves the persisted format carries every field,
// including the new Owner — the point of adding owner "cedo pra não migrar
// formato": a decision saved and loaded back is byte-for-value identical.
func TestFileStoreRoundTrip(t *testing.T) {
	s := NewFileStore(t.TempDir())
	want := decisionFor("alice", "lunch")
	if err := s.Save(context.Background(), want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Load(context.Background(), "alice", "lunch")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	if got.Owner != "alice" {
		t.Errorf("Owner not preserved: got %q", got.Owner)
	}
}

// TestFileStoreHonorsCancelledContext proves the context threaded through the
// port is actually consulted: a cancelled context short-circuits every method
// before it touches the disk. The FileStore gains nothing from cancellation
// itself, but the guard is what lets a database-backed Store honor deadlines
// behind the same interface, so it must not be a decorative parameter.
func TestFileStoreHonorsCancelledContext(t *testing.T) {
	s := NewFileStore(t.TempDir())
	// Seed a decision with a live context so there is something for the reads to
	// find — proving the cancellation, not a missing file, is what fails them.
	if err := s.Save(context.Background(), decisionFor("alice", "lunch")); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.Save(ctx, decisionFor("alice", "dinner")); !errors.Is(err, context.Canceled) {
		t.Errorf("Save with cancelled context: got %v, want context.Canceled", err)
	}
	if _, err := s.Load(ctx, "alice", "lunch"); !errors.Is(err, context.Canceled) {
		t.Errorf("Load with cancelled context: got %v, want context.Canceled", err)
	}
	if _, err := s.List(ctx, "alice"); !errors.Is(err, context.Canceled) {
		t.Errorf("List with cancelled context: got %v, want context.Canceled", err)
	}
	if err := s.Delete(ctx, "alice", "lunch"); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete with cancelled context: got %v, want context.Canceled", err)
	}

	// The cancellation guarded the disk: the seeded decision is untouched.
	if _, err := s.Load(context.Background(), "alice", "lunch"); err != nil {
		t.Errorf("decision after cancelled Delete: got %v, want it intact", err)
	}
}

// TestFileStoreLoadUnknown reports a missing decision as an error, not a zero
// value that a caller would mistake for an empty-but-real decision.
func TestFileStoreLoadUnknown(t *testing.T) {
	s := NewFileStore(t.TempDir())
	if _, err := s.Load(context.Background(), "alice", "ghost"); err == nil {
		t.Fatal("Load of a missing decision: want error, got nil")
	}
}

// TestFileStoreSaveRequiresIdentity refuses to persist a decision that cannot
// be addressed later: an empty owner or title has no stable key, so it fails
// loudly instead of writing an unreachable file.
func TestFileStoreSaveRequiresIdentity(t *testing.T) {
	s := NewFileStore(t.TempDir())
	if err := s.Save(context.Background(), decisionFor("", "lunch")); err == nil {
		t.Error("Save with empty owner: want error, got nil")
	}
	if err := s.Save(context.Background(), decisionFor("alice", "")); err == nil {
		t.Error("Save with empty title: want error, got nil")
	}
}

// TestFileStoreDelete removes a decision by its (owner, title) identity: after
// Delete the decision is gone (Load reports ErrNotFound), deleting a decision
// the owner does not hold is ErrNotFound rather than a silent success, and the
// delete is owner-scoped — removing alice's decision never touches bob's under
// the same title.
func TestFileStoreDelete(t *testing.T) {
	s := NewFileStore(t.TempDir())
	for _, d := range []Decision{
		decisionFor("alice", "lunch"),
		decisionFor("bob", "lunch"),
	} {
		if err := s.Save(context.Background(), d); err != nil {
			t.Fatalf("Save(%s/%s): %v", d.Owner, d.Title, err)
		}
	}

	if err := s.Delete(context.Background(), "alice", "lunch"); err != nil {
		t.Fatalf("Delete(alice/lunch): %v", err)
	}
	if _, err := s.Load(context.Background(), "alice", "lunch"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Load after Delete: got %v, want ErrNotFound", err)
	}

	// Owner scoping: bob's decision under the same title is untouched.
	if _, err := s.Load(context.Background(), "bob", "lunch"); err != nil {
		t.Errorf("bob's decision after deleting alice's: got %v, want it intact", err)
	}

	// Deleting what the owner does not hold is ErrNotFound, not a no-op success.
	if err := s.Delete(context.Background(), "alice", "lunch"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete of already-gone decision: got %v, want ErrNotFound", err)
	}
}
