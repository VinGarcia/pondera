package main

import (
	"bytes"
	"io"
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
	mustRun(t, &sink, "add-criterion", "--name", "price", "--weight", "2", "--cost", "--absolute", file)
	mustRun(t, &sink, "lock", file)
	mustRun(t, &sink, "add-option", "--name", "A", file)
	mustRun(t, &sink, "add-option", "--name", "B", file)
	mustRun(t, &sink, "score", "--option", "A", "--criterion", "safety", "--value", "90", file)
	mustRun(t, &sink, "score", "--option", "A", "--criterion", "price", "--value", "40000", file)
	mustRun(t, &sink, "score", "--option", "B", "--criterion", "safety", "--value", "70", file)
	mustRun(t, &sink, "score", "--option", "B", "--criterion", "price", "--value", "20000", file)

	// safety weight 3, price weight 2 (cost, absolute over {40000,20000}).
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
		{"score", "--option", "A", "--criterion", "x", file}, // missing --value
		{"rank", "missing.toml"},                             // load nonexistent file
	}
	for _, args := range cases {
		if err := run(args, &sink); err == nil {
			t.Errorf("run(%v) should have errored, got nil", args)
		}
	}
}
