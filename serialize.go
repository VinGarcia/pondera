package pondera

import (
	"bytes"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// A decision is persisted one-file-per-decision as TOML: diff-friendly,
// commentable, and versionable. The ranking is never stored — it is always
// recomputed from the file so it can't drift out of sync with the inputs. The
// LockedAt stamp travels with the file as the audit record of when the weights
// were frozen.

// Marshal encodes a decision as TOML bytes.
func Marshal(d Decision) ([]byte, error) {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(d); err != nil {
		return nil, fmt.Errorf("pondera: encoding decision %q: %w", d.Title, err)
	}
	return buf.Bytes(), nil
}

// Unmarshal decodes a decision from TOML bytes. A field the schema does not
// know (a typo'd key) is reported rather than silently dropped.
func Unmarshal(data []byte) (Decision, error) {
	var d Decision
	meta, err := toml.Decode(string(data), &d)
	if err != nil {
		return Decision{}, fmt.Errorf("pondera: decoding decision: %w", err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return Decision{}, fmt.Errorf("pondera: unknown key %q in decision file", undecoded[0])
	}
	return d, nil
}

// Save writes the decision to path as TOML.
func Save(path string, d Decision) error {
	data, err := Marshal(d)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("pondera: writing %s: %w", path, err)
	}
	return nil
}

// Load reads a decision from the TOML file at path.
func Load(path string) (Decision, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Decision{}, fmt.Errorf("pondera: reading %s: %w", path, err)
	}
	return Unmarshal(data)
}
