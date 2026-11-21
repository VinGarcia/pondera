package server_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/vingarcia/pondera"
	"github.com/vingarcia/pondera/server"
)

// seedCorrupt drops a malformed decision file named name into owner's directory
// of a FileStore rooted at root. "alice" slugs to "alice", so the directory is
// root/alice — the layout FileStore documents.
func seedCorrupt(t *testing.T, root string, owner string, name string) {
	t.Helper()
	dir := filepath.Join(root, owner)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("seed owner dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("title = = broken"), 0o644); err != nil {
		t.Fatalf("seed corrupt %s: %v", name, err)
	}
}

// TestListSurvivesUnreadableDecision is the Fatia B regression at the HTTP edge:
// one malformed .toml used to turn GET /decisions into a 500, hiding every
// readable decision of the owner. The readable titles must now still be served,
// the default body keeps its JSON-array shape (existing clients keep working),
// and the unreadable files are surfaced rather than dropped silently: as a count
// header on the default shape, and by name and reason on the opt-in object shape.
func TestListSurvivesUnreadableDecision(t *testing.T) {
	root := t.TempDir()
	store := pondera.NewFileStore(root)
	seed(t, store, "alice", "lunch")
	seed(t, store, "alice", "car")
	seedCorrupt(t, root, "alice", "garbage.toml")
	h := server.New(store, server.OwnerFromHeader("X-Pondera-Owner"))

	t.Run("default array shape", func(t *testing.T) {
		rec := get(h, "/decisions", "alice")
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200; body %q", rec.Code, rec.Body.String())
		}
		var titles []string
		if err := json.Unmarshal(rec.Body.Bytes(), &titles); err != nil {
			t.Fatalf("body is not a JSON array of titles: %v; body %q", err, rec.Body.String())
		}
		sort.Strings(titles)
		if !reflect.DeepEqual(titles, []string{"car", "lunch"}) {
			t.Errorf("titles = %v, want [car lunch]", titles)
		}
		if got := rec.Header().Get("X-Pondera-Unreadable-Count"); got != "1" {
			t.Errorf("X-Pondera-Unreadable-Count = %q, want \"1\"", got)
		}
	})

	t.Run("opt-in object shape", func(t *testing.T) {
		rec := get(h, "/decisions?include=unreadable", "alice")
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200; body %q", rec.Code, rec.Body.String())
		}
		var body struct {
			Titles     []string `json:"titles"`
			Unreadable []struct {
				File   string `json:"file"`
				Reason string `json:"reason"`
			} `json:"unreadable"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("body is not the list object: %v; body %q", err, rec.Body.String())
		}
		sort.Strings(body.Titles)
		if !reflect.DeepEqual(body.Titles, []string{"car", "lunch"}) {
			t.Errorf("titles = %v, want [car lunch]", body.Titles)
		}
		if len(body.Unreadable) != 1 || body.Unreadable[0].File != "garbage.toml" || body.Unreadable[0].Reason == "" {
			t.Fatalf("unreadable = %+v, want one garbage.toml entry with a reason", body.Unreadable)
		}
		if strings.Contains(rec.Body.String(), root) {
			t.Errorf("response leaks the server-side store path %q: %s", root, rec.Body.String())
		}
	})
}

// TestListShapesWithoutUnreadable pins the clean case: with nothing unreadable
// the default body is the same JSON array as before, without the count header,
// and the opt-in object carries an empty (not null) unreadable list so a client
// can read its length without a null check.
func TestListShapesWithoutUnreadable(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	seed(t, store, "alice", "lunch")
	h := server.New(store, server.OwnerFromHeader("X-Pondera-Owner"))

	rec := get(h, "/decisions", "alice")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `["lunch"]` {
		t.Fatalf("default list: status %d body %q, want 200 [\"lunch\"]", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Pondera-Unreadable-Count"); got != "" {
		t.Errorf("X-Pondera-Unreadable-Count = %q on a clean list, want absent", got)
	}

	rec = get(h, "/decisions?include=unreadable", "alice")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"titles":["lunch"],"unreadable":[]}` {
		t.Fatalf("object list: status %d body %q", rec.Code, rec.Body.String())
	}
}
