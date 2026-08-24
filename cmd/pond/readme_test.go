package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestReadmeExample runs the worked example in README.md verbatim and asserts the
// documented output. It is docs-as-code: the README cannot claim a ranking the
// engine does not actually produce (the sprint-#28 anti-pattern, a doc promising
// behavior the code lacks). The only thing not pinned is the `locked` wall-clock
// stamp, which is normalized away on both sides.
func TestReadmeExample(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	cmds := parseVerifiedExample(t, string(raw))
	if len(cmds) < 10 {
		t.Fatalf("parsed only %d commands from README example; block missing or malformed", len(cmds))
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "car.toml")
	nonEmpty := 0
	for _, c := range cmds {
		args := make([]string, len(c.args))
		for i, a := range c.args {
			if a == "car.toml" {
				a = file
			}
			args[i] = a
		}
		var out bytes.Buffer
		if err := run(args, &out); err != nil {
			t.Fatalf("command %q failed: %v", strings.Join(c.args, " "), err)
		}
		got := normalize(out.String())
		want := normalize(c.want)
		if got != want {
			t.Fatalf("command %q output mismatch:\n--- got ---\n%s\n--- want ---\n%s",
				strings.Join(c.args, " "), got, want)
		}
		if want != "" {
			nonEmpty++
		}
	}
	// Guard against a vacuous pass: the show and rank commands must have produced
	// (and matched) real output, not just a wall of silent mutations.
	if nonEmpty < 2 {
		t.Fatalf("expected at least 2 commands with documented output (show, rank), got %d", nonEmpty)
	}
}

type exampleCmd struct {
	args []string // arguments after "pondera"
	want string   // documented stdout, surrounding blank lines stripped
}

var lockedStamp = regexp.MustCompile(`locked \d{4}-\d{2}-\d{2} \d{2}:\d{2}`)

func normalize(s string) string {
	s = lockedStamp.ReplaceAllString(s, "locked <ts>")
	return strings.TrimRight(s, "\n")
}

// parseVerifiedExample extracts the fenced console block between the VERIFIED
// EXAMPLE markers. Lines beginning "$ pond " are commands; the lines that
// follow (until the next command) are that command's expected stdout, with
// surrounding blank lines stripped so visual separators do not count as output.
func parseVerifiedExample(t *testing.T, md string) []exampleCmd {
	t.Helper()
	begin := strings.Index(md, "BEGIN VERIFIED EXAMPLE")
	end := strings.Index(md, "END VERIFIED EXAMPLE")
	if begin < 0 || end < 0 || end < begin {
		t.Fatalf("VERIFIED EXAMPLE markers not found in README")
	}
	block := md[begin:end]
	// Keep only what is inside the ```console ... ``` fence.
	if i := strings.Index(block, "```console\n"); i >= 0 {
		block = block[i+len("```console\n"):]
	} else {
		t.Fatalf("```console fence not found inside example block")
	}
	if i := strings.Index(block, "```"); i >= 0 {
		block = block[:i]
	}

	var cmds []exampleCmd
	var cur *exampleCmd
	var wantLines []string
	flush := func() {
		if cur != nil {
			cur.want = strings.Join(trimBlank(wantLines), "\n")
			cmds = append(cmds, *cur)
		}
		wantLines = nil
	}
	for _, line := range strings.Split(block, "\n") {
		if s, ok := strings.CutPrefix(line, "$ pond "); ok {
			flush()
			cur = &exampleCmd{args: shellSplit(s)}
			continue
		}
		if cur != nil {
			wantLines = append(wantLines, line)
		}
	}
	flush()
	return cmds
}

// trimBlank drops leading and trailing empty lines (visual separators between
// commands), keeping interior content intact.
func trimBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// shellSplit is a minimal quote-aware splitter: it honors double quotes so a
// flag like --title "New car" stays a single argument.
func shellSplit(s string) []string {
	var args []string
	var cur strings.Builder
	inQuote, hasTok := false, false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			hasTok = true
		case r == ' ' && !inQuote:
			if hasTok {
				args = append(args, cur.String())
				cur.Reset()
				hasTok = false
			}
		default:
			cur.WriteRune(r)
			hasTok = true
		}
	}
	if hasTok {
		args = append(args, cur.String())
	}
	return args
}
