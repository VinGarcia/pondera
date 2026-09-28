package pondera

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned by a Store's Load when no decision matches the given
// (owner, title). The port defines its own not-found so callers (an HTTP layer,
// say) can answer "not found" without knowing whether the backend is a
// filesystem, a database, or anything else.
var ErrNotFound = errors.New("pondera: decision not found")

// ErrInvalidTitle is returned by a Store when a title, though non-empty, has no
// filename-safe characters (only punctuation) and so cannot address a decision.
// Like ErrNotFound it is part of the port so a caller (an HTTP layer, say) can
// map it to a client error — a 400 — without matching on the backend's own
// error strings.
var ErrInvalidTitle = errors.New("pondera: title has no filename-safe characters")

// ErrTitleConflict is returned by Save when a *different* decision already
// occupies the storage identity the decision being saved resolves to. Two
// distinct (owner, title) pairs can collapse to the same key — the FileStore
// slugs both into one path, so "Lunch" and "lunch!" address the same file — and
// blindly writing would clobber the decision already there, losing it and making
// a later Load of the original title return the survivor's contents. Like
// ErrNotFound it is part of the port so a caller (an HTTP layer) can map it to a
// 409 without matching backend error strings. Re-saving the same (owner, title)
// is an update, not a conflict.
var ErrTitleConflict = errors.New("pondera: a different decision already occupies this title")

// A Store persists decisions scoped by owner. Save and Load address a decision
// by its (Owner, Title) identity; List returns the titles a single owner owns,
// never leaking another owner's decisions. The interface keeps the persistence
// medium open — a filesystem today, a database behind a server tomorrow —
// without the callers changing.
//
// Every method takes a context.Context: the FileStore only honors cancellation,
// but the port is deliberately backend-agnostic, and a database- or
// network-backed Store needs the context to carry deadlines and cancellation
// per request. Threading it here means an HTTP handler can pass r.Context()
// straight through without an adapter re-declaring the port to add it.
type Store interface {
	Save(ctx context.Context, d Decision) error
	Load(ctx context.Context, owner, title string) (Decision, error)
	List(ctx context.Context, owner string) ([]string, error)
	Delete(ctx context.Context, owner, title string) error
}

// FileStore is a Store backed by one TOML file per decision on disk, laid out
// as <root>/<owner-slug>/<title-slug>.toml. The owner directory is the key
// List reads for isolation; the owner and title also live inside the file so
// the format is self-describing and round-trips without loss (owner persisted
// early to avoid a format migration when a server/db backend arrives).
type FileStore struct {
	root string
}

// NewFileStore returns a FileStore rooted at dir. The directory is created
// lazily on the first Save, so an empty or not-yet-existing root lists as
// empty rather than erroring.
func NewFileStore(dir string) *FileStore {
	return &FileStore{root: dir}
}

// slug turns an owner or title into a filesystem-safe path segment: lowercase,
// with every run of non-alphanumeric characters collapsed to a single hyphen
// and the ends trimmed. It returns an error when nothing usable remains, so a
// title of only punctuation fails loudly instead of writing to "".
func slug(s string) (string, error) {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevHyphen = false
		case !prevHyphen && b.Len() > 0:
			b.WriteRune('-')
			prevHyphen = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		return "", fmt.Errorf("pondera: %q has no filename-safe characters", s)
	}
	return out, nil
}

// path resolves the on-disk location of a decision from its owner and title,
// erroring when either is empty or has no filename-safe form.
func (s *FileStore) path(owner, title string) (string, error) {
	if owner == "" {
		return "", fmt.Errorf("pondera: decision has no owner")
	}
	if title == "" {
		return "", fmt.Errorf("pondera: decision has no title")
	}
	ownerSlug, err := slug(owner)
	if err != nil {
		return "", err
	}
	titleSlug, err := slug(title)
	if err != nil {
		// A punctuation-only title is the caller's mistake, not a backend fault;
		// tag it with the port's sentinel so an HTTP layer can answer 400.
		return "", fmt.Errorf("%w: %q", ErrInvalidTitle, title)
	}
	return filepath.Join(s.root, ownerSlug, titleSlug+".toml"), nil
}

// Save writes the decision under its owner's directory, creating the directory
// if needed. It refuses a decision without a usable owner and title, and a
// cancelled context before touching the disk.
func (s *FileStore) Save(ctx context.Context, d Decision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := s.path(d.Owner, d.Title)
	if err != nil {
		return err
	}
	// Distinct (owner, title) pairs can slug to the same path ("Lunch" and
	// "lunch!" collide), so a blind write would silently clobber a *different*
	// decision and lose it — a later Load of the original title would then return
	// the survivor's contents. Refuse the write unless the file already there is
	// the same decision (a legitimate update). A missing file is a fresh create;
	// a corrupt or unreadable file is surfaced rather than overwritten. This
	// closes the common sequential collision; concurrent Saves still race, the
	// same TOCTOU the single-host FileStore already carries.
	switch existing, loadErr := Load(p); {
	case loadErr == nil:
		if existing.Owner != d.Owner || existing.Title != d.Title {
			return fmt.Errorf("%w: %q (owner %q) collides with the stored %q (owner %q)", ErrTitleConflict, d.Title, d.Owner, existing.Title, existing.Owner)
		}
	case !errors.Is(loadErr, fs.ErrNotExist):
		return loadErr
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("pondera: creating owner directory: %w", err)
	}
	return Save(p, d)
}

// Load reads back the decision owned by owner with the given title, returning
// ErrNotFound when no such file exists so callers need not match on fs errors.
func (s *FileStore) Load(ctx context.Context, owner, title string) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	p, err := s.path(owner, title)
	if err != nil {
		return Decision{}, err
	}
	d, err := Load(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Decision{}, fmt.Errorf("%w: %s owns no %q", ErrNotFound, owner, title)
	}
	return d, err
}

// Delete removes the decision owned by owner with the given title. A title the
// owner does not hold is ErrNotFound — the same answer Load gives, so a caller
// (an HTTP layer) maps deleting a missing decision, or another owner's decision,
// to the same 404 without distinguishing the two. Deleting the last decision
// leaves an empty owner directory behind; List reads it as empty, so no cleanup
// is needed for correctness.
func (s *FileStore) Delete(ctx context.Context, owner, title string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := s.path(owner, title)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s owns no %q", ErrNotFound, owner, title)
		}
		return fmt.Errorf("pondera: deleting %s: %w", title, err)
	}
	return nil
}

// List returns the titles of the decisions owned by owner, sorted by nothing in
// particular (callers sort as they need). An unknown owner lists as empty with
// no error. Each title is read from the file itself, so it reflects the real
// stored Title, not a lossy slug of the filename.
func (s *FileStore) List(ctx context.Context, owner string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if owner == "" {
		return nil, fmt.Errorf("pondera: cannot list decisions of an empty owner")
	}
	ownerSlug, err := slug(owner)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.root, ownerSlug)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pondera: listing %s: %w", dir, err)
	}
	titles := []string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		d, err := Load(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		titles = append(titles, d.Title)
	}
	return titles, nil
}
