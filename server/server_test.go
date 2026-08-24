package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sylgarcia00/pondera"
	"github.com/sylgarcia00/pondera/server"
)

// seed writes one minimal decision owned by owner with the given title into
// store, failing the test on any error.
func seed(t *testing.T, store pondera.Store, owner, title string) {
	t.Helper()
	d := pondera.Decision{
		Title:    title,
		Owner:    owner,
		Criteria: []pondera.Criterion{{Name: "safety", Weight: 1}},
		Options:  []pondera.Option{{Name: "a", Scores: map[string]float64{"safety": 80}}},
	}
	if err := store.Save(d); err != nil {
		t.Fatalf("seeding %s/%s: %v", owner, title, err)
	}
}

// get issues a GET to the handler with the owner header set (omitted when
// owner is empty) and returns the response recorder.
func get(h http.Handler, path, owner string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if owner != "" {
		req.Header.Set("X-Pondera-Owner", owner)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestHandlerScopesToInjectedOwner is the fatia-3 behavior: a mountable handler
// serves decisions from a Store scoped to the owner the host injects (here via
// the request header), and that scoping is a real isolation boundary — an owner
// never sees or reads another owner's decision, even by guessing its title, and
// a request with no injected owner is rejected rather than served globally.
func TestHandlerScopesToInjectedOwner(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	seed(t, store, "alice", "hire-decision")
	seed(t, store, "bob", "buy-car")

	h := server.New(store, server.OwnerFromHeader("X-Pondera-Owner"))

	// List is scoped to the injected owner — alice sees only hers, bob only his.
	for _, tc := range []struct {
		owner string
		want  string
		deny  string
	}{
		{owner: "alice", want: "hire-decision", deny: "buy-car"},
		{owner: "bob", want: "buy-car", deny: "hire-decision"},
	} {
		rec := get(h, "/decisions", tc.owner)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /decisions as %s: status %d, want 200", tc.owner, rec.Code)
		}
		var titles []string
		if err := json.Unmarshal(rec.Body.Bytes(), &titles); err != nil {
			t.Fatalf("GET /decisions as %s: decoding body %q: %v", tc.owner, rec.Body.String(), err)
		}
		if len(titles) != 1 || titles[0] != tc.want {
			t.Fatalf("GET /decisions as %s: got %v, want [%s]", tc.owner, titles, tc.want)
		}
		for _, got := range titles {
			if got == tc.deny {
				t.Fatalf("GET /decisions as %s leaked %s's decision %q", tc.owner, "another owner", tc.deny)
			}
		}
	}

	// Reading one decision is owner-scoped too: bob cannot load alice's decision
	// even though he knows its exact title — isolation is not just list-filtering.
	if rec := get(h, "/decisions/hire-decision", "bob"); rec.Code != http.StatusNotFound {
		t.Fatalf("GET alice's decision as bob: status %d, want 404", rec.Code)
	}

	// Alice reads her own decision back, owner and title intact.
	rec := get(h, "/decisions/hire-decision", "alice")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /decisions/hire-decision as alice: status %d, want 200", rec.Code)
	}
	var got pondera.Decision
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding decision: %v", err)
	}
	if got.Title != "hire-decision" || got.Owner != "alice" {
		t.Fatalf("loaded decision = {title:%q owner:%q}, want {hire-decision alice}", got.Title, got.Owner)
	}

	// No injected owner: the host never authenticated the caller, so pondera
	// refuses rather than serving a global or empty view.
	if rec := get(h, "/decisions", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /decisions with no owner: status %d, want 401", rec.Code)
	}
}
