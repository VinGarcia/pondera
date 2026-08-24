// Package server exposes a pondera Store over HTTP as a mountable handler.
// pondera does not authenticate: the owner scoping every request is supplied by
// the host that mounts the handler, never read from the request body or query,
// so a caller cannot impersonate another owner. Mount it behind a proxy (or
// middleware) that authenticates the caller and injects their identity.
package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/sylgarcia00/pondera"
)

// OwnerFunc extracts the owner identity from a request. The host chooses how:
// OwnerFromHeader when a trusted proxy sets a header, or a custom func reading a
// context value some middleware placed there. Returning "" means "no
// authenticated owner", which the handler rejects.
type OwnerFunc func(r *http.Request) string

// OwnerFromHeader returns an OwnerFunc that reads the owner from the named
// request header. It is safe only behind a proxy that authenticates the caller
// and sets the header itself — a raw header is client-controlled otherwise, and
// documenting that is the whole point of forcing the host to opt in.
func OwnerFromHeader(name string) OwnerFunc {
	return func(r *http.Request) string {
		return r.Header.Get(name)
	}
}

// Handler serves a Store's decisions over HTTP, scoping every request to the
// owner ownerFn extracts. It implements http.Handler, so the host mounts it
// wherever it likes (root, a subpath, an iframe route).
type Handler struct {
	store   pondera.Store
	ownerFn OwnerFunc
	mux     *http.ServeMux
}

// New builds a Handler backed by store, taking the owner of each request from
// ownerFn. Both are required.
func New(store pondera.Store, ownerFn OwnerFunc) *Handler {
	h := &Handler{store: store, ownerFn: ownerFn}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /decisions", h.list)
	mux.HandleFunc("POST /decisions", h.create)
	mux.HandleFunc("GET /decisions/{title}", h.get)
	mux.HandleFunc("PUT /decisions/{title}", h.update)
	mux.HandleFunc("GET /decisions/{title}/rank", h.rank)
	h.mux = mux
	return h
}

// ServeHTTP routes to the read endpoints; the owner check happens per-handler
// so every path is scoped, not just the ones a router happens to reach.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// owner returns the authenticated owner, writing a 401 and reporting false when
// the host injected none — pondera refuses to serve a global view.
func (h *Handler) owner(w http.ResponseWriter, r *http.Request) (string, bool) {
	owner := h.ownerFn(r)
	if owner == "" {
		http.Error(w, "no authenticated owner", http.StatusUnauthorized)
		return "", false
	}
	return owner, true
}

// list serves GET /decisions: the titles the injected owner owns, as a JSON
// array.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.owner(w, r)
	if !ok {
		return
	}
	titles, err := h.store.List(owner)
	if err != nil {
		http.Error(w, "listing decisions", http.StatusInternalServerError)
		return
	}
	writeJSON(w, titles)
}

// get serves GET /decisions/{title}: one decision owned by the injected owner.
// A title the owner does not own is a 404 — the same answer whether the
// decision is missing or belongs to someone else, so the endpoint never
// confirms another owner's decision exists.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.owner(w, r)
	if !ok {
		return
	}
	d, err := h.store.Load(owner, r.PathValue("title"))
	if errors.Is(err, pondera.ErrNotFound) {
		http.Error(w, "decision not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "loading decision", http.StatusInternalServerError)
		return
	}
	writeJSON(w, d)
}

// rank serves GET /decisions/{title}/rank: the injected owner's decision ranked
// most- to least-desirable by the engine, so the SPA renders the ranking from
// the single source of truth instead of re-implementing the weighted sum. A
// title the owner does not own is a 404 (same as get). A decision that loads but
// cannot be ranked yet — an option missing a score, no criteria, a bad weight —
// is a 422 carrying the engine's reason, a user-fixable state, not a 500.
func (h *Handler) rank(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.owner(w, r)
	if !ok {
		return
	}
	d, err := h.store.Load(owner, r.PathValue("title"))
	if errors.Is(err, pondera.ErrNotFound) {
		http.Error(w, "decision not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "loading decision", http.StatusInternalServerError)
		return
	}
	results, err := d.Rank()
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	writeJSON(w, results)
}

// create serves POST /decisions: it saves the decision in the request body
// under the INJECTED owner. The owner named in the body is overwritten, never
// trusted — an authenticated caller cannot persist a decision on another
// owner's behalf by naming them in the payload. A missing title or malformed
// body is a 400; a stored decision answers 201.
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.owner(w, r)
	if !ok {
		return
	}
	var d pondera.Decision
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		http.Error(w, "invalid decision body", http.StatusBadRequest)
		return
	}
	if d.Title == "" {
		http.Error(w, "decision has no title", http.StatusBadRequest)
		return
	}
	// The host authenticated the caller; that identity — not the body — owns the
	// decision. Overwriting here is the security boundary, not a convenience.
	d.Owner = owner
	// POST creates; it must not silently overwrite. If the owner already holds a
	// decision under this title, refuse with 409 rather than clobbering it — an
	// update is a different, explicit operation. The lookup is owner-scoped, so a
	// title another owner holds does not collide. (There is a check-then-save
	// TOCTOU window; acceptable for the single-host FileStore, logged as a debt.)
	if _, err := h.store.Load(d.Owner, d.Title); err == nil {
		http.Error(w, "decision already exists", http.StatusConflict)
		return
	} else if errors.Is(err, pondera.ErrInvalidTitle) {
		http.Error(w, "decision title has no usable characters", http.StatusBadRequest)
		return
	} else if !errors.Is(err, pondera.ErrNotFound) {
		http.Error(w, "checking for existing decision", http.StatusInternalServerError)
		return
	}
	if err := h.store.Save(d); err != nil {
		http.Error(w, "saving decision", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, d)
}

// update serves PUT /decisions/{title}: it REPLACES an existing decision the
// injected owner holds with the request body — the edit-and-re-rank loop. The
// title in the path identifies the resource; the owner and title from the body
// are ignored so a caller can neither reattribute the decision nor rename it
// into a second one. Update is the explicit mirror of create: a title the owner
// does not already hold is a 404 (it never creates), just as POST to an existing
// title is a 409. A missing owner is a 401 and a malformed body is a 400.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.owner(w, r)
	if !ok {
		return
	}
	title := r.PathValue("title")
	// The decision must already exist for this owner; PUT edits, it does not
	// create. The lookup is owner-scoped, so a title another owner holds reads as
	// not-found here — bob cannot edit alice's decision, nor learn it exists.
	if _, err := h.store.Load(owner, title); errors.Is(err, pondera.ErrNotFound) {
		http.Error(w, "decision not found", http.StatusNotFound)
		return
	} else if errors.Is(err, pondera.ErrInvalidTitle) {
		http.Error(w, "decision title has no usable characters", http.StatusBadRequest)
		return
	} else if err != nil {
		http.Error(w, "loading decision", http.StatusInternalServerError)
		return
	}
	var d pondera.Decision
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		http.Error(w, "invalid decision body", http.StatusBadRequest)
		return
	}
	// Identity comes from the authenticated owner and the path, never the body:
	// the resource being replaced is (owner, path title), so a body naming another
	// owner or a different title cannot escape that scope.
	d.Owner = owner
	d.Title = title
	if err := h.store.Save(d); err != nil {
		http.Error(w, "saving decision", http.StatusInternalServerError)
		return
	}
	writeJSON(w, d)
}

// writeJSON encodes v as the JSON response body. An encoding failure is logged
// to the client as a 500 only if nothing has been written yet; here the bodies
// are plain data that always encode.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
