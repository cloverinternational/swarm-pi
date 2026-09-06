package bgprocess

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
)

// resultText flattens a tool result's text content blocks, which is what the
// model ultimately receives.
func resultText(r *tools.ToolResult) string {
	if r == nil {
		return ""
	}
	if r.Output != "" {
		return r.Output
	}
	var b strings.Builder
	for _, block := range r.Content {
		if block.Type == tools.ContentTypeText {
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

// TestBackgroundBashToolPreservesBlankLines is the end-to-end regression for
// the defect that corrupted the agent's view of every file it read through the
// shell. The buffer-level tests prove the storage layer keeps empty lines; this
// one proves the actual tool a model calls returns them.
//
// The original symptom: `cat file` on a file containing blank lines returned
// the content with every blank line removed. An agent that then wrote a patch
// using that content as context produced a patch that could not match the bytes
// on disk, and reported `context not found` for a block it could plainly see.
func TestBackgroundBashToolPreservesBlankLines(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "blank.txt")
	// Blank line leading, interior, doubled, and trailing.
	const content = "\npackage main\n\nimport \"fmt\"\n\n\nfunc main() {}\n"
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig: builtin.DefaultBashConfig(),
		Manager:    mgr,
	})

	result, err := tool.Execute(testContext(), map[string]any{
		"command":         "cat " + file,
		"timeout_seconds": 30.0,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("nil result")
	}

	out := resultText(result)

	// The decisive assertion: the file has two adjacent newlines in several
	// places, and the delivered output must too.
	if !strings.Contains(out, "package main\n\nimport") {
		t.Errorf("interior blank line was dropped from tool output.\ngot: %q", out)
	}
	if !strings.Contains(out, "\n\n\nfunc main()") {
		t.Errorf("doubled blank line was dropped from tool output.\ngot: %q", out)
	}

	// Every non-empty source line must still be present and in order.
	for _, want := range []string{"package main", `import "fmt"`, "func main() {}"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lost content line %q.\ngot: %q", want, out)
		}
	}
}

// TestBackgroundBashToolBlankLineCount pins the exact newline count so a
// partial fix that collapses runs of blank lines into one cannot pass.
func TestBackgroundBashToolBlankLineCount(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "runs.txt")
	if err := os.WriteFile(file, []byte("a\n\n\n\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig: builtin.DefaultBashConfig(),
		Manager:    mgr,
	})

	result, err := tool.Execute(testContext(), map[string]any{
		"command":         "cat " + file,
		"timeout_seconds": 30.0,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// "a\n\n\n\nb" — three blank lines between a and b must survive as a run.
	if out := resultText(result); !strings.Contains(out, "a\n\n\n\nb") {
		t.Errorf("run of blank lines not preserved verbatim.\ngot: %q", out)
	}
}
