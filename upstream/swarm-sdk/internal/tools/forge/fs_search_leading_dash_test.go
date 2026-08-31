package forge

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// indexOf returns the position of want in args, or -1.
func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

// TestBuildSearchArgs_LeadingDashPatternIsData is the argv-level regression
// guard for issues #178/#198/#199/#206: a pattern that begins with "-" or "--"
// must reach ripgrep as pattern DATA, never as a command-line flag.
//
// The contract asserted is exactly the one rg documents: the pattern is the
// value of `-e`, and the search path is separated from options by `--`.
func TestBuildSearchArgs_LeadingDashPatternIsData(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
	}{
		{"long flag", "--dry-run"},
		{"css custom properties", "--ops-shell-muted|--ops-shell-secondary"},
		{"flag alternation with spaces", "--integrate|go test -race|GOOS=windows"},
		{"single dash alternation", "-infinity|infinity"},
		{"bare double dash", "--"},
		{"lone dash", "-"},
		{"looks like short flag", "-i"},
		{"ordinary pattern", "func main"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := buildSearchArgs(map[string]any{}, tc.pattern, "/workspace")

			// The pattern must appear immediately after a "-e".
			eIdx := indexOf(args, "-e")
			if eIdx < 0 {
				t.Fatalf("argv has no -e flag: %q", args)
			}
			if eIdx+1 >= len(args) || args[eIdx+1] != tc.pattern {
				t.Fatalf("pattern is not the value of -e; argv = %q", args)
			}

			// The pattern must never be the very first argument, which is the
			// position rg parses as an option.
			if args[0] == tc.pattern {
				t.Fatalf("pattern sits in the positional slot rg parses as a flag; argv = %q", args)
			}

			// The path must be last and be preceded by an end-of-options "--".
			if got := args[len(args)-1]; got != "/workspace" {
				t.Fatalf("search path is not the final argument; argv = %q", args)
			}
			if got := args[len(args)-2]; got != "--" {
				t.Fatalf("search path is not preceded by an end-of-options separator; argv = %q", args)
			}

			// The pattern must be passed through byte-for-byte: no escaping,
			// no rewriting, no defensive prefixing.
			if args[eIdx+1] != tc.pattern {
				t.Fatalf("pattern was rewritten: got %q want %q", args[eIdx+1], tc.pattern)
			}
		})
	}
}

// TestBuildSearchArgs_OptionsStillApply proves the -e/-- change did not
// displace the option flags or the path.
func TestBuildSearchArgs_OptionsStillApply(t *testing.T) {
	params := map[string]any{
		"output_mode": "content",
		"-i":          true,
		"-C":          float64(2),
		"glob":        "*.css",
	}
	args := buildSearchArgs(params, "--var-name", "/ws/src")

	for _, want := range []string{"--line-number", "-i", "-C", "2", "--glob", "*.css"} {
		if indexOf(args, want) < 0 {
			t.Fatalf("missing %q in argv %q", want, args)
		}
	}
	// Options must all precede the pattern.
	eIdx := indexOf(args, "-e")
	if g := indexOf(args, "--glob"); g > eIdx {
		t.Fatalf("--glob must precede -e; argv = %q", args)
	}
	if got := args[len(args)-1]; got != "/ws/src" {
		t.Fatalf("path not last; argv = %q", args)
	}
}

// TestFSSearch_LeadingDashPatternEndToEnd runs the real ripgrep binary through
// the tool. Before the fix these patterns produced
// "unrecognized flag --dry-run" or "inity|infinity: No such file or directory";
// now they must match content.
func TestFSSearch_LeadingDashPatternEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed")
	}

	dir := t.TempDir()
	writeFile(t, dir, "a.css", ":root { --ops-shell-muted: #111; }\n")
	writeFile(t, dir, "b.sh", "run --dry-run now\n")
	writeFile(t, dir, "c.txt", "value -infinity here\n")

	tool := NewFSSearch(dir)

	for _, pattern := range []string{"--dry-run", "--ops-shell-muted|--ops-shell-secondary", "-infinity|infinity"} {
		t.Run(pattern, func(t *testing.T) {
			res, err := tool.Execute(context.Background(), map[string]any{
				"pattern":     pattern,
				"output_mode": "content",
			})
			if err != nil {
				t.Fatalf("Execute(%q) returned error: %v", pattern, err)
			}
			out := res.Output
			if strings.Contains(out, "unrecognized flag") ||
				strings.Contains(out, "No such file or directory") ||
				strings.Contains(out, "wasn't expected") {
				t.Fatalf("pattern %q was parsed as a flag/path: %s", pattern, out)
			}
			if strings.Contains(out, "No matches found") {
				t.Fatalf("pattern %q found no matches; expected a hit. output: %s", pattern, out)
			}
		})
	}
}

// TestFSSearch_InvalidRegexStillErrors is an explicit acceptance criterion on
// the issues: the fix must not swallow or rewrite a genuinely invalid regex.
func TestFSSearch_InvalidRegexStillErrors(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed")
	}

	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "content\n")

	tool := NewFSSearch(dir)
	_, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "[unclosed",
	})
	if err == nil {
		t.Fatal("expected an error for an invalid regex, got nil")
	}
	if !strings.Contains(err.Error(), "regex parse error") &&
		!strings.Contains(err.Error(), "unclosed") {
		t.Fatalf("expected ripgrep's own regex error to surface, got: %v", err)
	}
}
