package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vingarcia/pondera"
	"github.com/vingarcia/pondera/server"
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

// post issues a POST to path with the given JSON body, setting the owner header
// when owner is non-empty, and returns the response recorder.
func post(h http.Handler, path, owner, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if owner != "" {
		req.Header.Set("X-Pondera-Owner", owner)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// put issues a PUT to path with the given JSON body, setting the owner header
// when owner is non-empty, and returns the response recorder.
func put(h http.Handler, path, owner, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
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

// TestRankEndpoint is the fatia-4 API contract the SPA consumes: GET
// /decisions/{title}/rank returns the owner's decision ranked most- to
// least-desirable, reusing the Go engine as the single source of truth rather
// than re-implementing the weighted sum in the browser. It is owner-scoped like
// the read endpoints, a missing title is 404, and a decision that exists but is
// not yet rankable (an option missing a score) is a 422 carrying the reason so
// the UI can tell the decider what to fix — not a 500 and not a silent empty
// ranking.
func TestRankEndpoint(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	h := server.New(store, server.OwnerFromHeader("X-Pondera-Owner"))

	// A rankable decision: safety outweighs price, so the safer option wins even
	// though it is pricier — the ranking must reflect the weighted engine, not
	// input order.
	rankable := pondera.Decision{
		Title: "buy-car",
		Owner: "alice",
		Criteria: []pondera.Criterion{
			{Name: "safety", Weight: 3},
			{Name: "price", Weight: 1, Direction: pondera.Cost},
		},
		Options: []pondera.Option{
			{Name: "cheap", Scores: map[string]float64{"safety": 40, "price": 20}},
			{Name: "safe", Scores: map[string]float64{"safety": 90, "price": 80}},
		},
	}
	if err := store.Save(rankable); err != nil {
		t.Fatalf("seeding rankable decision: %v", err)
	}

	rec := get(h, "/decisions/buy-car/rank", "alice")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET rank as alice: status %d, want 200; body %q", rec.Code, rec.Body.String())
	}
	var results []pondera.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
		t.Fatalf("decoding rank body %q: %v", rec.Body.String(), err)
	}
	if len(results) != 2 {
		t.Fatalf("rank returned %d results, want 2", len(results))
	}
	if results[0].Option != "safe" {
		t.Fatalf("top-ranked option = %q, want safe (weighted engine, not input order)", results[0].Option)
	}
	if !(results[0].Score > results[1].Score) {
		t.Fatalf("ranking not ordered: %v", results)
	}

	// Owner-scoping: bob cannot rank alice's decision even knowing the title.
	if r := get(h, "/decisions/buy-car/rank", "bob"); r.Code != http.StatusNotFound {
		t.Fatalf("rank alice's decision as bob: status %d, want 404", r.Code)
	}

	// A missing title is a 404, same as the read endpoints.
	if r := get(h, "/decisions/nope/rank", "alice"); r.Code != http.StatusNotFound {
		t.Fatalf("rank missing decision: status %d, want 404", r.Code)
	}

	// No injected owner: rejected, never served globally.
	if r := get(h, "/decisions/buy-car/rank", ""); r.Code != http.StatusUnauthorized {
		t.Fatalf("rank with no owner: status %d, want 401", r.Code)
	}

	// A decision that exists but is not rankable — an option missing a score —
	// is a 422 carrying the engine's reason, not a 500 and not an empty 200.
	unrankable := pondera.Decision{
		Title:    "half-built",
		Owner:    "alice",
		Criteria: []pondera.Criterion{{Name: "safety", Weight: 1}, {Name: "price", Weight: 1}},
		Options:  []pondera.Option{{Name: "a", Scores: map[string]float64{"safety": 80}}},
	}
	if err := store.Save(unrankable); err != nil {
		t.Fatalf("seeding unrankable decision: %v", err)
	}
	r := get(h, "/decisions/half-built/rank", "alice")
	if r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rank unrankable decision: status %d, want 422; body %q", r.Code, r.Body.String())
	}
	if !strings.Contains(r.Body.String(), "price") {
		t.Fatalf("422 body %q does not name the missing criterion the decider must fix", r.Body.String())
	}
}

// bodyFor renders a JSON POST body for a decision, letting the test set the
// owner field to something OTHER than the authenticated caller — the whole
// point is proving the server ignores it.
func bodyFor(title, bodyOwner string) string {
	return bodyScored(title, bodyOwner, 80)
}

// bodyScored is bodyFor with a caller-chosen score, so a test can tell a first
// write apart from a would-be overwrite by reading the score back.
func bodyScored(title, bodyOwner string, score float64) string {
	d := pondera.Decision{
		Title:    title,
		Owner:    bodyOwner,
		Criteria: []pondera.Criterion{{Name: "safety", Weight: 1}},
		Options:  []pondera.Option{{Name: "a", Scores: map[string]float64{"safety": score}}},
	}
	b, err := json.Marshal(d)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestCreateScopesToInjectedOwner is the fatia-3b behavior: POST /decisions
// persists a decision under the INJECTED owner, never the owner named in the
// body — so an authenticated caller cannot write a decision on someone else's
// behalf by lying in the payload. It also rejects an unauthenticated write and
// a body with no title, and refuses to invent an owner from the request.
func TestCreateScopesToInjectedOwner(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	h := server.New(store, server.OwnerFromHeader("X-Pondera-Owner"))

	// alice creates a decision whose BODY claims bob owns it. The injected owner
	// (alice) must win: the decision is saved under alice, not bob.
	rec := post(h, "/decisions", "alice", bodyFor("raise-ask", "bob"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /decisions as alice: status %d, want 201; body %q", rec.Code, rec.Body.String())
	}

	// The persisted decision belongs to alice, with the body's owner overridden.
	got, err := store.Load("alice", "raise-ask")
	if err != nil {
		t.Fatalf("alice's decision was not saved under her: %v", err)
	}
	if got.Owner != "alice" {
		t.Fatalf("saved owner = %q, want alice (body owner must be ignored)", got.Owner)
	}
	// bob must not have received the decision the body tried to attribute to him.
	if _, err := store.Load("bob", "raise-ask"); err == nil {
		t.Fatal("decision leaked to bob: body owner was trusted over the injected owner")
	}
	// And bob cannot read it over HTTP either.
	if r := get(h, "/decisions/raise-ask", "bob"); r.Code != http.StatusNotFound {
		t.Fatalf("GET alice's created decision as bob: status %d, want 404", r.Code)
	}

	// No injected owner: the write is refused, nothing is persisted.
	if r := post(h, "/decisions", "", bodyFor("ghost", "")); r.Code != http.StatusUnauthorized {
		t.Fatalf("POST with no owner: status %d, want 401", r.Code)
	}

	// A body with no title is a client error, not a 500 and not a silent save.
	if r := post(h, "/decisions", "alice", bodyFor("", "alice")); r.Code != http.StatusBadRequest {
		t.Fatalf("POST with empty title: status %d, want 400", r.Code)
	}

	// Malformed JSON is a client error too.
	if r := post(h, "/decisions", "alice", "{not json"); r.Code != http.StatusBadRequest {
		t.Fatalf("POST with malformed JSON: status %d, want 400", r.Code)
	}
}

// TestCreateRejectsDuplicateTitle is the fatia-3b debt fix: POST creates, it
// does not silently overwrite. A second POST to a title the owner already owns
// is a 409 Conflict and the stored decision is left untouched — no data loss.
// The conflict is owner-scoped, so a different owner may hold the same title
// without collision.
func TestCreateRejectsDuplicateTitle(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	h := server.New(store, server.OwnerFromHeader("X-Pondera-Owner"))

	// First write for alice succeeds and records safety score 80.
	if r := post(h, "/decisions", "alice", bodyScored("buy-car", "alice", 80)); r.Code != http.StatusCreated {
		t.Fatalf("first POST: status %d, want 201; body %q", r.Code, r.Body.String())
	}

	// Second POST to the same title is refused with 409, not another 201.
	if r := post(h, "/decisions", "alice", bodyScored("buy-car", "alice", 30)); r.Code != http.StatusConflict {
		t.Fatalf("duplicate POST: status %d, want 409; body %q", r.Code, r.Body.String())
	}

	// The rejected write must not have clobbered the original — the score is
	// still 80, proving the 409 protected existing data rather than merely
	// returning a different status after overwriting.
	got, err := store.Load("alice", "buy-car")
	if err != nil {
		t.Fatalf("loading after rejected duplicate: %v", err)
	}
	if s := got.Options[0].Scores["safety"]; s != 80 {
		t.Fatalf("stored safety score = %v, want 80 (rejected write must not overwrite)", s)
	}

	// The conflict is owner-scoped: bob may create HIS own "buy-car" — a global
	// title index would wrongly reject this.
	if r := post(h, "/decisions", "bob", bodyScored("buy-car", "bob", 50)); r.Code != http.StatusCreated {
		t.Fatalf("bob's same-title POST: status %d, want 201 (conflict must be owner-scoped)", r.Code)
	}
}

// TestCreateRejectsUnusableTitle covers a title that is non-empty but has no
// filename-safe characters (only punctuation): the store cannot slug it, so
// what would otherwise surface as a 500 must be reported as a 400 — the caller
// sent a bad title, not the server failing. The bad request must also persist
// nothing, so a later well-formed title is unaffected.
func TestCreateRejectsUnusableTitle(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	h := server.New(store, server.OwnerFromHeader("X-Pondera-Owner"))

	// A punctuation-only title cannot become a path segment. It is the caller's
	// fault, so it is a 400 — not a 500 leaking an internal slug error.
	if r := post(h, "/decisions", "alice", bodyFor("!!!", "alice")); r.Code != http.StatusBadRequest {
		t.Fatalf("unusable-title POST: status %d, want 400; body %q", r.Code, r.Body.String())
	}

	// The rejected write saved nothing: alice still owns no decisions.
	rec := get(h, "/decisions", "alice")
	var titles []string
	if err := json.Unmarshal(rec.Body.Bytes(), &titles); err != nil {
		t.Fatalf("listing alice after rejected POST: %v; body %q", err, rec.Body.String())
	}
	if len(titles) != 0 {
		t.Fatalf("alice owns %v after a rejected POST, want none", titles)
	}
}

// mustJSON marshals a decision to a JSON request body, panicking on the
// impossible encode error so a test reads as a straight line.
func mustJSON(d pondera.Decision) string {
	b, err := json.Marshal(d)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestUpdateEndpoint is the edit-and-re-rank loop — the central operation of a
// decision tool. PUT /decisions/{title} REPLACES an existing decision owned by
// the injected owner, so the decider can adjust a weight and the ranking
// reflects it immediately from the single source of truth. Update is distinct
// from create: PUT to a title the owner does not hold is a 404 (it never
// creates), just as POST to one they do hold is a 409. It is owner-scoped, the
// body's owner is ignored in favor of the injected one, and a malformed body is
// a 400.
func TestUpdateEndpoint(t *testing.T) {
	store := pondera.NewFileStore(t.TempDir())
	h := server.New(store, server.OwnerFromHeader("X-Pondera-Owner"))

	// Alice's decision: with safety weighted far above price, the safe-but-pricey
	// option wins.
	initial := pondera.Decision{
		Title: "buy-car",
		Owner: "alice",
		Criteria: []pondera.Criterion{
			{Name: "safety", Weight: 3},
			{Name: "price", Weight: 1, Direction: pondera.Cost},
		},
		Options: []pondera.Option{
			{Name: "cheap", Scores: map[string]float64{"safety": 40, "price": 20}},
			{Name: "safe", Scores: map[string]float64{"safety": 90, "price": 80}},
		},
	}
	if err := store.Save(initial); err != nil {
		t.Fatalf("seeding initial decision: %v", err)
	}
	if r := get(h, "/decisions/buy-car/rank", "alice"); r.Code == http.StatusOK {
		var res []pondera.Result
		_ = json.Unmarshal(r.Body.Bytes(), &res)
		if res[0].Option != "safe" {
			t.Fatalf("precondition: top option = %q, want safe before the edit", res[0].Option)
		}
	} else {
		t.Fatalf("precondition rank: status %d, want 200", r.Code)
	}

	// The decider changes their mind: price now dominates safety. Same options,
	// only the weights flip. The body claims bob owns it — the injected owner
	// (alice) must still win.
	edited := initial
	edited.Owner = "bob"
	edited.Criteria = []pondera.Criterion{
		{Name: "safety", Weight: 1},
		{Name: "price", Weight: 3, Direction: pondera.Cost},
	}
	if r := put(h, "/decisions/buy-car", "alice", mustJSON(edited)); r.Code != http.StatusOK {
		t.Fatalf("PUT edit: status %d, want 200; body %q", r.Code, r.Body.String())
	}

	// The stored decision is still alice's (body owner ignored), and the ranking
	// now reflects the new weights: the cheaper option wins. This is the whole
	// value — the engine re-ranks from the updated source of truth, not a stale
	// copy.
	if got, err := store.Load("alice", "buy-car"); err != nil {
		t.Fatalf("loading edited decision: %v", err)
	} else if got.Owner != "alice" {
		t.Fatalf("stored owner = %q, want alice (body owner must be ignored)", got.Owner)
	}
	if _, err := store.Load("bob", "buy-car"); err == nil {
		t.Fatal("edit leaked to bob: body owner was trusted over the injected owner")
	}
	rec := get(h, "/decisions/buy-car/rank", "alice")
	if rec.Code != http.StatusOK {
		t.Fatalf("rank after edit: status %d, want 200; body %q", rec.Code, rec.Body.String())
	}
	var res []pondera.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decoding rank after edit %q: %v", rec.Body.String(), err)
	}
	if res[0].Option != "cheap" {
		t.Fatalf("top option after weight edit = %q, want cheap (re-ranked from the edit)", res[0].Option)
	}

	// PUT never creates: updating a title the owner does not hold is a 404, the
	// mirror of POST's 409-on-existing. This keeps create and update explicit.
	missing := pondera.Decision{Title: "no-such", Owner: "alice", Criteria: initial.Criteria, Options: initial.Options}
	if r := put(h, "/decisions/no-such", "alice", mustJSON(missing)); r.Code != http.StatusNotFound {
		t.Fatalf("PUT to missing decision: status %d, want 404 (update must not create)", r.Code)
	}
	if _, err := store.Load("alice", "no-such"); err == nil {
		t.Fatal("PUT to a missing title created it: update must be explicit, not upsert")
	}

	// Owner-scoping: bob cannot edit alice's decision even knowing its title. It
	// is a 404 (never confirming her decision exists) and her data is untouched.
	bobEdit := initial
	bobEdit.Options = []pondera.Option{
		{Name: "cheap", Scores: map[string]float64{"safety": 1, "price": 1}},
		{Name: "safe", Scores: map[string]float64{"safety": 1, "price": 1}},
	}
	if r := put(h, "/decisions/buy-car", "bob", mustJSON(bobEdit)); r.Code != http.StatusNotFound {
		t.Fatalf("bob editing alice's decision: status %d, want 404", r.Code)
	}
	if got, err := store.Load("alice", "buy-car"); err != nil {
		t.Fatalf("reloading alice's decision after bob's attempt: %v", err)
	} else if got.Options[1].Scores["safety"] != 90 {
		t.Fatalf("alice's decision was clobbered by bob's edit: safe safety = %v, want 90", got.Options[1].Scores["safety"])
	}

	// No injected owner: refused, nothing changes.
	if r := put(h, "/decisions/buy-car", "", mustJSON(edited)); r.Code != http.StatusUnauthorized {
		t.Fatalf("PUT with no owner: status %d, want 401", r.Code)
	}

	// Malformed JSON is a client error, not a 500 and not a silent no-op.
	if r := put(h, "/decisions/buy-car", "alice", "{not json"); r.Code != http.StatusBadRequest {
		t.Fatalf("PUT with malformed JSON: status %d, want 400", r.Code)
	}
}
