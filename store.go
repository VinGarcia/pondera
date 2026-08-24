package pondera

import (
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

// A Store persists decisions scoped by owner. Save and Load address a decision
// by its (Owner, Title) identity; List returns the titles a single owner owns,
// never leaking another owner's decisions. The interface keeps the persistence
// medium open — a filesystem today, a database behind a server tomorrow —
// without the callers changing.
type Store interface {
	Save(d Decision) error
	Load(owner, title string) (Decision, error)
	List(owner string) ([]string, error)
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
		return "", err
	}
	return filepath.Join(s.root, ownerSlug, titleSlug+".toml"), nil
}

// Save writes the decision under its owner's directory, creating the directory
// if needed. It refuses a decision without a usable owner and title.
func (s *FileStore) Save(d Decision) error {
	p, err := s.path(d.Owner, d.Title)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("pondera: creating owner directory: %w", err)
	}
	return Save(p, d)
}

// Load reads back the decision owned by owner with the given title, returning
// ErrNotFound when no such file exists so callers need not match on fs errors.
func (s *FileStore) Load(owner, title string) (Decision, error) {
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

// List returns the titles of the decisions owned by owner, sorted by nothing in
// particular (callers sort as they need). An unknown owner lists as empty with
// no error. Each title is read from the file itself, so it reflects the real
// stored Title, not a lossy slug of the filename.
func (s *FileStore) List(owner string) ([]string, error) {
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
