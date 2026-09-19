package pondera

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

// TestFileStoreRejectsUnaddressableIdentity covers the identity guards shared
// through path(): a title that is only punctuation has no filename-safe form and
// is tagged ErrInvalidTitle so an HTTP layer answers 400 without matching backend
// strings, while a punctuation-only or empty owner fails loudly rather than
// reading or writing under an empty path segment.
func TestFileStoreRejectsUnaddressableIdentity(t *testing.T) {
	s := NewFileStore(t.TempDir())

	// A punctuation-only title carries the port's sentinel on both write paths.
	if err := s.Save(context.Background(), decisionFor("alice", "!!!")); !errors.Is(err, ErrInvalidTitle) {
		t.Errorf("Save with punctuation title: got %v, want ErrInvalidTitle", err)
	}
	if err := s.Delete(context.Background(), "alice", "!!!"); !errors.Is(err, ErrInvalidTitle) {
		t.Errorf("Delete with punctuation title: got %v, want ErrInvalidTitle", err)
	}
	if _, err := s.Load(context.Background(), "alice", "!!!"); !errors.Is(err, ErrInvalidTitle) {
		t.Errorf("Load with punctuation title: got %v, want ErrInvalidTitle", err)
	}

	// A punctuation-only owner has no slug either, so it must fail rather than
	// write or list under "".
	if err := s.Save(context.Background(), decisionFor("!!!", "lunch")); err == nil {
		t.Error("Save with punctuation owner: want error, got nil")
	}
	if _, err := s.List(context.Background(), "!!!"); err == nil {
		t.Error("List with punctuation owner: want error, got nil")
	}
	if _, err := s.List(context.Background(), ""); err == nil {
		t.Error("List with empty owner: want error, got nil")
	}
}

// TestFileStoreSaveReportsDirCreationFailure proves Save surfaces a failure to
// create the owner directory instead of silently losing the write: a root that
// is a regular file cannot hold owner subdirectories, so MkdirAll fails and the
// error must propagate.
func TestFileStoreSaveReportsDirCreationFailure(t *testing.T) {
	rootAsFile := filepath.Join(t.TempDir(), "root-is-a-file")
	if err := os.WriteFile(rootAsFile, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seed root file: %v", err)
	}
	s := NewFileStore(rootAsFile)
	if err := s.Save(context.Background(), decisionFor("alice", "lunch")); err == nil {
		t.Fatal("Save under a file root: want error, got nil")
	}
}

// TestFileStoreListReportsIOErrors covers the two failures past the benign
// not-exist case: the owner path resolving to a regular file (ReadDir cannot
// list it) and a corrupt decision file inside the directory (Load rejects it).
// List must report both loudly, never returning a truncated or empty list that
// hides the corruption.
func TestFileStoreListReportsIOErrors(t *testing.T) {
	t.Run("owner path is a file", func(t *testing.T) {
		root := t.TempDir()
		ownerSlug, err := slug("alice")
		if err != nil {
			t.Fatalf("slug: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, ownerSlug), []byte("not a directory"), 0o644); err != nil {
			t.Fatalf("seed owner file: %v", err)
		}
		if _, err := NewFileStore(root).List(context.Background(), "alice"); err == nil {
			t.Fatal("List of an owner path that is a file: want error, got nil")
		}
	})

	t.Run("corrupt decision file", func(t *testing.T) {
		root := t.TempDir()
		ownerSlug, err := slug("alice")
		if err != nil {
			t.Fatalf("slug: %v", err)
		}
		dir := filepath.Join(root, ownerSlug)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("seed owner dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "garbage.toml"), []byte("title = = broken"), 0o644); err != nil {
			t.Fatalf("seed garbage file: %v", err)
		}
		if _, err := NewFileStore(root).List(context.Background(), "alice"); err == nil {
			t.Fatal("List over a corrupt decision file: want error, got nil")
		}
	})
}

// TestFileStoreListIgnoresNonDecisionEntries proves List filters an owner
// directory down to decision files: a stray non-.toml file and a subdirectory
// are skipped rather than listed or parsed as decisions.
func TestFileStoreListIgnoresNonDecisionEntries(t *testing.T) {
	s := NewFileStore(t.TempDir())
	if err := s.Save(context.Background(), decisionFor("alice", "lunch")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ownerSlug, err := slug("alice")
	if err != nil {
		t.Fatalf("slug: %v", err)
	}
	dir := filepath.Join(s.root, ownerSlug)
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("not a decision"), 0o644); err != nil {
		t.Fatalf("seed stray file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("seed subdir: %v", err)
	}

	got, err := s.List(context.Background(), "alice")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"lunch"}) {
		t.Errorf("List = %v, want only the .toml decision", got)
	}
}

// TestFileStoreLoadReportsCorruptFile proves Load of a file that exists but does
// not parse fails with the real decode error and a zero decision, never the
// ErrNotFound reserved for a missing file nor a misleading half-built value.
func TestFileStoreLoadReportsCorruptFile(t *testing.T) {
	s := NewFileStore(t.TempDir())
	p, err := s.path("alice", "lunch")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("seed owner dir: %v", err)
	}
	if err := os.WriteFile(p, []byte("title = = broken"), 0o644); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	got, err := s.Load(context.Background(), "alice", "lunch")
	if err == nil {
		t.Fatal("Load of a corrupt file: want error, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("corrupt file mis-mapped to ErrNotFound: %v", err)
	}
	if !reflect.DeepEqual(got, Decision{}) {
		t.Errorf("Load error returned a non-zero decision: %+v", got)
	}
}

// TestFileStoreSlugsMultiWordIdentity proves an owner and title with separators
// address a decision through a slugged path (spaces collapse to hyphens) while
// List and Load still return the real stored title, not a lossy slug.
func TestFileStoreSlugsMultiWordIdentity(t *testing.T) {
	s := NewFileStore(t.TempDir())
	d := decisionFor("Team Alpha", "New Laptop Choice")
	if err := s.Save(context.Background(), d); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.List(context.Background(), "Team Alpha")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"New Laptop Choice"}) {
		t.Errorf("List = %v, want the real stored title back", got)
	}

	back, err := s.Load(context.Background(), "Team Alpha", "New Laptop Choice")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if back.Title != "New Laptop Choice" {
		t.Errorf("Title = %q, want it preserved through the slugged path", back.Title)
	}
}

// TestFileStoreDeleteReportsIOError covers Delete's non-not-exist failure: when
// the target path is a non-empty directory os.Remove refuses it, and Delete must
// surface that error rather than the ErrNotFound it reserves for a decision the
// owner does not hold.
func TestFileStoreDeleteReportsIOError(t *testing.T) {
	s := NewFileStore(t.TempDir())
	p, err := s.path("alice", "lunch")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	// Make the decision path a non-empty directory so os.Remove fails with a
	// non-not-exist error.
	if err := os.MkdirAll(filepath.Join(p, "child"), 0o755); err != nil {
		t.Fatalf("seed dir at decision path: %v", err)
	}
	err = s.Delete(context.Background(), "alice", "lunch")
	if err == nil {
		t.Fatal("Delete of a non-empty directory: want error, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("Delete IO failure mis-mapped to ErrNotFound: %v", err)
	}
}
