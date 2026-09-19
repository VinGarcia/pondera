package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mustRun runs a command and fails the test if it errors.
func mustRun(t *testing.T, out io.Writer, args ...string) {
	t.Helper()
	if err := run(args, out); err != nil {
		t.Fatalf("run(%v) errored: %v", args, err)
	}
}

// TestFullFlow drives a complete disciplined decision from new through rank and
// asserts the ranking the engine produces, proving the CLI wires the builder and
// the file round-trip together correctly.
func TestFullFlow(t *testing.T) {
	file := filepath.Join(t.TempDir(), "car.toml")
	var sink bytes.Buffer

	mustRun(t, &sink, "new", "--title", "New car", file)
	mustRun(t, &sink, "add-criterion", "--name", "safety", "--weight", "3", file)
	mustRun(t, &sink, "add-criterion", "--name", "price", "--weight", "2", "--cost", "--range", "min,max", file)
	mustRun(t, &sink, "lock", file)
	mustRun(t, &sink, "add-option", "--name", "A", file)
	mustRun(t, &sink, "add-option", "--name", "B", file)
	mustRun(t, &sink, "score", "--option", "A", "--criterion", "safety", "--value", "90", file)
	mustRun(t, &sink, "score", "--option", "A", "--criterion", "price", "--value", "40000", file)
	mustRun(t, &sink, "score", "--option", "B", "--criterion", "safety", "--value", "70", file)
	mustRun(t, &sink, "score", "--option", "B", "--criterion", "price", "--value", "20000", file)

	// safety weight 3, price weight 2 (cost, min-max over {40000,20000}).
	// A: safety 90; price min-max -> 100, cost -> 0.  (90*3 + 0*2)/5 = 54
	// B: safety 70; price -> 0, cost -> 100.          (70*3 + 100*2)/5 = 82
	var out bytes.Buffer
	mustRun(t, &out, "rank", file)
	got := out.String()
	wantLines := []string{
		`Ranking for "New car":`,
		"1. B   82.00",
		"2. A   54.00",
	}
	for _, w := range wantLines {
		if !strings.Contains(got, w) {
			t.Errorf("rank output missing %q\ngot:\n%s", w, got)
		}
	}
}

// TestSetWeightUpdatesWeight proves the happy path: pre-lock, set-weight actually
// changes a criterion's weight and the change is observable in show output. The
// error paths (post-lock, missing --weight) are covered elsewhere; this pins the
// success case so a regression that silently no-ops set-weight would be caught.
func TestSetWeightUpdatesWeight(t *testing.T) {
	file := filepath.Join(t.TempDir(), "w.toml")
	var sink bytes.Buffer
	mustRun(t, &sink, "new", "--title", "T", file)
	mustRun(t, &sink, "add-criterion", "--name", "safety", "--weight", "1", file)

	var before bytes.Buffer
	mustRun(t, &before, "show", file)
	if !strings.Contains(before.String(), "safety (weight 1,") {
		t.Fatalf("expected initial weight 1 in show output, got:\n%s", before.String())
	}

	mustRun(t, &sink, "set-weight", "--name", "safety", "--weight", "4", file)

	var after bytes.Buffer
	mustRun(t, &after, "show", file)
	if !strings.Contains(after.String(), "safety (weight 4,") {
		t.Errorf("expected updated weight 4 in show output, got:\n%s", after.String())
	}
}

// TestDisciplineEnforced proves the CLI inherits the builder's ordering rule:
// an option cannot be added before the weights are locked, and a weight cannot
// move after. This is the anti-rationalization guarantee, checked end-to-end.
func TestDisciplineEnforced(t *testing.T) {
	file := filepath.Join(t.TempDir(), "d.toml")
	var sink bytes.Buffer
	mustRun(t, &sink, "new", "--title", "T", file)
	mustRun(t, &sink, "add-criterion", "--name", "x", "--weight", "1", file)

	if err := run([]string{"add-option", "--name", "O", file}, &sink); err == nil {
		t.Fatal("expected add-option before lock to error, got nil")
	}

	mustRun(t, &sink, "lock", file)

	if err := run([]string{"set-weight", "--name", "x", "--weight", "5", file}, &sink); err == nil {
		t.Fatal("expected set-weight after lock to error, got nil")
	}
	if err := run([]string{"add-criterion", "--name", "y", "--weight", "1", file}, &sink); err == nil {
		t.Fatal("expected add-criterion after lock to error, got nil")
	}
}

// TestArgErrors covers the CLI's own guards, distinct from the engine's.
func TestArgErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "e.toml")
	var sink bytes.Buffer
	mustRun(t, &sink, "new", "--title", "T", file)

	cases := [][]string{
		{},                                  // no command
		{"bogus"},                           // unknown command
		{"new", "--title", "T", file},       // file already exists
		{"new", file},                       // missing --title
		{"lock"},                            // missing <file>
		{"set-weight", "--name", "x", file}, // missing --weight
		{"score", "--option", "A", "--criterion", "x", file},       // missing --value
		{"rank", "missing.toml"},                                   // load nonexistent file
		{"add-criterion", "--name", "z", "--range", "0,avg", file}, // unknown range keyword
	}
	for _, args := range cases {
		if err := run(args, &sink); err == nil {
			t.Errorf("run(%v) should have errored, got nil", args)
		}
	}
}

// TestHelpPrintsUsage pins that the three help spellings print the banner and
// exit success — help is not an error, so `pond help` must not return non-nil
// (which main would turn into exit code 1).
func TestHelpPrintsUsage(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		var out bytes.Buffer
		if err := run([]string{arg}, &out); err != nil {
			t.Errorf("run(%q) errored: %v", arg, err)
		}
		if !strings.Contains(out.String(), "pond — rank options by weighted values") {
			t.Errorf("run(%q) did not print the usage banner, got:\n%s", arg, out.String())
		}
	}
}

// TestExtraFileArgRejected proves the CLI refuses a stray second positional
// argument instead of silently operating on only the first. Without this guard a
// typo like `pond lock a.toml b.toml` would quietly ignore b.toml.
func TestExtraFileArgRejected(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "x.toml")
	extra := filepath.Join(dir, "y.toml")
	var sink bytes.Buffer
	mustRun(t, &sink, "new", "--title", "T", file)

	cases := [][]string{
		{"lock", file, extra},
		{"show", file, extra},
		{"rank", file, extra},
		{"add-option", "--name", "O", file, extra},
	}
	for _, args := range cases {
		if err := run(args, &sink); err == nil {
			t.Errorf("run(%v) with an extra file arg should have errored, got nil", args)
		}
	}
}

// TestMissingFileArgRejected proves each file-scoped subcommand requires the
// positional <file> and reports its absence rather than acting on an empty path.
func TestMissingFileArgRejected(t *testing.T) {
	commands := [][]string{
		{"new", "--title", "T"},
		{"add-criterion", "--name", "x"},
		{"set-weight", "--name", "x", "--weight", "2"},
		{"add-option", "--name", "O"},
		{"score", "--option", "O", "--criterion", "x", "--value", "1"},
		{"rank"},
		{"show"},
	}
	var sink bytes.Buffer
	for _, args := range commands {
		if err := run(args, &sink); err == nil {
			t.Errorf("run(%v) with no <file> should have errored, got nil", args)
		}
	}
}

// TestMutationsRejectMissingFile proves every mutating command fails loudly when
// the target file does not exist and — critically — does not create it as a side
// effect. A silently-created empty decision would be a subtle robustness hole.
func TestMutationsRejectMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.toml")
	var sink bytes.Buffer
	cases := [][]string{
		{"add-criterion", "--name", "x", missing},
		{"set-weight", "--name", "x", "--weight", "2", missing},
		{"lock", missing},
		{"add-option", "--name", "O", missing},
		{"score", "--option", "O", "--criterion", "x", "--value", "1", missing},
	}
	for _, args := range cases {
		err := run(args, &sink)
		if err == nil {
			t.Errorf("run(%v) on a missing file should have errored, got nil", args)
			continue
		}
		if !strings.Contains(err.Error(), "reading") {
			t.Errorf("run(%v) expected a read error, got: %v", args, err)
		}
	}
	if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
		t.Errorf("a failed mutation must not create the target file, but %s now exists", missing)
	}
}

// TestUnknownFlagRejected proves every subcommand rejects an unrecognized flag
// (flag.ContinueOnError surfaces the parse error) rather than ignoring it.
func TestUnknownFlagRejected(t *testing.T) {
	file := filepath.Join(t.TempDir(), "x.toml")
	commands := []string{"new", "add-criterion", "set-weight", "lock", "add-option", "score", "rank", "show"}
	var sink bytes.Buffer
	for _, cmd := range commands {
		args := []string{cmd, "--nonsense", file}
		if err := run(args, &sink); err == nil {
			t.Errorf("run(%v) with an unknown flag should have errored, got nil", args)
		}
	}
}

// TestRankErrors proves rank refuses to print a ranking from an ill-formed sheet:
// a decision with no criteria (which would divide by a zero total weight) and a
// locked decision with an option that has not been fully scored.
func TestRankErrors(t *testing.T) {
	dir := t.TempDir()
	var sink bytes.Buffer

	empty := filepath.Join(dir, "empty.toml")
	mustRun(t, &sink, "new", "--title", "Empty", empty)
	if err := run([]string{"rank", empty}, &sink); err == nil {
		t.Error("rank on a decision with no criteria should error, got nil")
	}

	partial := filepath.Join(dir, "partial.toml")
	mustRun(t, &sink, "new", "--title", "Partial", partial)
	mustRun(t, &sink, "add-criterion", "--name", "x", "--weight", "1", partial)
	mustRun(t, &sink, "lock", partial)
	mustRun(t, &sink, "add-option", "--name", "O", partial)
	if err := run([]string{"rank", partial}, &sink); err == nil {
		t.Error("rank with an unscored option should error, got nil")
	}
}

// TestShowMissingFile proves show reports a missing file instead of printing an
// empty decision.
func TestShowMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.toml")
	var sink bytes.Buffer
	if err := run([]string{"show", missing}, &sink); err == nil {
		t.Error("show on a missing file should error, got nil")
	}
}

// TestShowMarksUnscoredOptions proves show renders the "(unscored)" placeholder
// for a criterion an option has no value for, so a half-filled sheet is legible
// rather than showing a phantom zero or omitting the row.
func TestShowMarksUnscoredOptions(t *testing.T) {
	file := filepath.Join(t.TempDir(), "s.toml")
	var sink bytes.Buffer
	mustRun(t, &sink, "new", "--title", "T", file)
	mustRun(t, &sink, "add-criterion", "--name", "safety", "--weight", "1", file)
	mustRun(t, &sink, "lock", file)
	mustRun(t, &sink, "add-option", "--name", "A", file)

	var out bytes.Buffer
	mustRun(t, &out, "show", file)
	if !strings.Contains(out.String(), "safety = (unscored)") {
		t.Errorf("show should mark an unscored criterion, got:\n%s", out.String())
	}
}
