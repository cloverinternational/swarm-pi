package bgprocess

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
)

// TestBackgroundBashToolManyBlankLines forces the file-tailing path
// (executor.go's tailOutputFile/readFileChunk) to poll across multiple reads
// by producing output slowly, which is the scenario chunk boundaries actually
// matter for. It also pins a case with dozens of consecutive blank lines,
// which a chunk-boundary off-by-one could plausibly still miscount.
func TestBackgroundBashToolManyBlankLines(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig: builtin.DefaultBashConfig(),
		Manager:    mgr,
	})

	// printf with 20 blank lines between two markers, each write flushed
	// separately so the poller has to catch up across several ticks.
	cmd := "printf 'START\\n'; for i in $(seq 1 20); do printf '\\n'; sleep 0.02; done; printf 'END\\n'"
	result, err := tool.Execute(testContext(), map[string]any{
		"command":         cmd,
		"timeout_seconds": 30.0,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := resultText(result)

	blankRun := strings.Repeat("\n", 21) // 20 blank lines = 21 consecutive newlines
	if !strings.Contains(out, "START"+blankRun+"END") {
		count := strings.Count(out, "\n")
		t.Fatalf("expected START followed by 20 blank lines then END; total newlines=%d\ngot: %q", count, out)
	}
}
