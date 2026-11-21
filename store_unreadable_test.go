package pondera

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestFileStoreListSurfacesUnreadableFiles is the Fatia B regression: one
// malformed .toml in an owner's directory used to abort List, so every readable
// decision of that owner vanished behind a single error. List must now return the
// readable titles AND name each unreadable file in an *UnreadableError, so nothing
// disappears silently: not the readable decisions, and not the broken file.
func TestFileStoreListSurfacesUnreadableFiles(t *testing.T) {
	for _, tc := range []struct {
		name           string
		valid          []string
		corrupt        []string
		wantTitles     []string
		wantUnreadable []string
	}{
		{name: "no corrupt files", valid: []string{"lunch", "car"}, wantTitles: []string{"car", "lunch"}},
		{name: "one corrupt among valid", valid: []string{"lunch", "car"}, corrupt: []string{"garbage.toml"}, wantTitles: []string{"car", "lunch"}, wantUnreadable: []string{"garbage.toml"}},
		{name: "all corrupt", corrupt: []string{"a.toml", "b.toml"}, wantTitles: []string{}, wantUnreadable: []string{"a.toml", "b.toml"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewFileStore(t.TempDir())
			for _, title := range tc.valid {
				if err := s.Save(context.Background(), decisionFor("alice", title)); err != nil {
					t.Fatalf("Save %s: %v", title, err)
				}
			}
			ownerSlug, err := slug("alice")
			if err != nil {
				t.Fatalf("slug: %v", err)
			}
			dir := filepath.Join(s.root, ownerSlug)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("seed owner dir: %v", err)
			}
			for _, name := range tc.corrupt {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("title = = broken"), 0o644); err != nil {
					t.Fatalf("seed corrupt %s: %v", name, err)
				}
			}

			titles, err := s.List(context.Background(), "alice")
			sort.Strings(titles)
			if !reflect.DeepEqual(titles, tc.wantTitles) {
				t.Errorf("titles = %#v, want %#v", titles, tc.wantTitles)
			}
			if len(tc.wantUnreadable) == 0 {
				if err != nil {
					t.Fatalf("List: unexpected error %v", err)
				}
				return
			}
			var ue *UnreadableError
			if !errors.As(err, &ue) {
				t.Fatalf("List error = %v, want an *UnreadableError", err)
			}
			var names []string
			for _, f := range ue.Files {
				names = append(names, f.Name)
				if f.Reason == "" || f.Err == nil {
					t.Errorf("unreadable %s: empty Reason %q or nil Err", f.Name, f.Reason)
				}
				if strings.Contains(f.Reason, s.root) {
					t.Errorf("unreadable %s: Reason %q leaks the store path", f.Name, f.Reason)
				}
			}
			sort.Strings(names)
			if !reflect.DeepEqual(names, tc.wantUnreadable) {
				t.Errorf("unreadable = %v, want %v", names, tc.wantUnreadable)
			}
		})
	}
}

// TestFileStoreListUnreadableReasonOmitsPath proves a file that fails at the
// read step (not the decode step) still gets a short reason without the absolute
// on-disk path, which the error wrapping in Load carries and an HTTP response
// must not echo. A directory named *.toml is skipped, so a dangling symlink is
// used to force a read failure on an entry that looks like a decision file.
func TestFileStoreListUnreadableReasonOmitsPath(t *testing.T) {
	s := NewFileStore(t.TempDir())
	if err := s.Save(context.Background(), decisionFor("alice", "lunch")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ownerSlug, err := slug("alice")
	if err != nil {
		t.Fatalf("slug: %v", err)
	}
	dir := filepath.Join(s.root, ownerSlug)
	if err := os.Symlink(filepath.Join(dir, "missing-target"), filepath.Join(dir, "dangling.toml")); err != nil {
		t.Fatalf("seed dangling symlink: %v", err)
	}

	titles, err := s.List(context.Background(), "alice")
	if !reflect.DeepEqual(titles, []string{"lunch"}) {
		t.Errorf("titles = %v, want [lunch]", titles)
	}
	var ue *UnreadableError
	if !errors.As(err, &ue) || len(ue.Files) != 1 {
		t.Fatalf("List error = %v, want an *UnreadableError naming one file", err)
	}
	f := ue.Files[0]
	if f.Name != "dangling.toml" {
		t.Errorf("Name = %q, want dangling.toml", f.Name)
	}
	if f.Reason == "" || strings.Contains(f.Reason, s.root) {
		t.Errorf("Reason = %q: want a short reason without the store path", f.Reason)
	}
}
