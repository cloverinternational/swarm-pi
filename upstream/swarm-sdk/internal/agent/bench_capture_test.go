package agent_test

// bench_capture_test.go — the PLAN.md §4 C1 acceptance test: an agent run
// with hooks disabled still produces effect-ledger rows, because the capture
// seam (internal/agent/bench_capture.go) never reads a.hooksManager or
// a.reqDisableHooks at all. This is the deciding constraint the whole task
// exists to satisfy — a hooks-derived collector was proven structurally
// incapable of this in G3 (PLAN.md §4 C1).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent/agenttest"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/bench"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/toolout"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// mutatingNonceTool is a mutating tool that actually writes a real file to
// disk and reports a typed FileEffect for it, keyed by a per-test nonce so
// no assertion below can be satisfied without the tool genuinely having run
// (the anti-vacuity guard PLAN.md's verification section calls out by name:
// a prior attempt "passed" while the tool never executed, denied by a
// permission gate).
type mutatingNonceTool struct {
	nonce string
	path  string

	calls int
}

func newMutatingNonceTool(t *testing.T, nonce string) *mutatingNonceTool {
	t.Helper()
	return &mutatingNonceTool{
		nonce: nonce,
		path:  filepath.Join(t.TempDir(), "effect-"+nonce+".txt"),
	}
}

func (m *mutatingNonceTool) Name() string        { return "mutating_nonce_tool" }
func (m *mutatingNonceTool) Description() string { return "writes a real file and reports its effect" }
func (m *mutatingNonceTool) Parameters() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (m *mutatingNonceTool) Execute(_ context.Context, _ map[string]any) (*tools.ToolResult, error) {
	m.calls++
	content := "produced by " + m.nonce
	if err := os.WriteFile(m.path, []byte(content), 0o644); err != nil {
		return nil, err
	}
	res := tools.NewToolResult("wrote " + m.nonce)
	res.Outcome = toolout.Files(toolout.FileEffect{
		Path:         m.path,
		Op:           toolout.FileOpCreate,
		BytesWritten: int64(len(content)),
		PostBlobSHA1: bench.BlobHash([]byte(content)),
	})
	return res, nil
}

// countingHooksManager is a full, blocking-CAPABLE agent.HooksManager whose
// emission counts are the ground truth for "did the full hook path actually
// run for this turn?". Its presence is the point: this test attaches a REAL
// hooks manager, then proves the effect ledger still fires when that manager
// is prevented from running via DisableHooks — exactly the --no-hooks shape.
type countingHooksManager struct {
	before, after int
}

func (c *countingHooksManager) EmitToolBeforeExecute(context.Context, string, map[string]any) ([]agent.HookResult, error) {
	c.before++
	return nil, nil
}
func (c *countingHooksManager) EmitToolAfterExecute(context.Context, string, map[string]any, any, error) []agent.HookResult {
	c.after++
	return nil
}
func (c *countingHooksManager) EmitProviderResponse(context.Context, string, string, int, int, int64) {
}

var _ agent.HooksManager = (*countingHooksManager)(nil)

func newBenchToolCallingAgent(t *testing.T, tool tools.Tool) *agent.Agent {
	t.Helper()
	mock := agenttest.NewMockProvider().
		ThenCallTool(tool.Name(), map[string]any{}).
		ThenRespondWith("Done.")
	ag, err := agent.New(agent.Config{
		Definition: &agent.Definition{ID: "bench-capture-test", Provider: "mock", Model: "mock"},
		Provider:   mock,
		Tools:      []tools.Tool{tool},
		// A real WorkspacePath matters here: internal/bench buckets rows by
		// git-common-dir-or-workspace-path (see internal/bench/effect.go's
		// repoCoords), and an empty workspace path deliberately resolves to
		// no bucket at all (repoCoords("") == ("", "")) — mirroring
		// GitSHA("")'s "empty is a normal non-answer, never a fabricated
		// one" rule. Every real caller (interactive, headless, sub-agent)
		// always has a workspace, so the test fixture must too.
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	return ag
}

// readBenchRowsForTest re-reads every *.jsonl under dir and unmarshals it,
// waiting (bounded) for the async ledger writer to catch up rather than
// sleeping a fixed amount — bench.FlushForTest gives a real completion
// signal.
func readBenchRowsForTest(t *testing.T, dir string) []bench.Effect {
	t.Helper()
	bench.FlushForTest()
	var rows []bench.Effect
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatalf("ReadFile(%s): %v", path, rerr)
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var e bench.Effect
			if uerr := json.Unmarshal([]byte(line), &e); uerr != nil {
				t.Fatalf("%s: corrupted JSON line: %v (line=%q)", path, uerr, line)
			}
			rows = append(rows, e)
		}
		return nil
	})
	return rows
}

// TestRecordBenchEffects_SurvivesNoHooks is THE acceptance criterion
// (PLAN.md §4 C1 / task verification (a)): a turn run with DisableHooks=true
// — the in-process shape of the CLI's --no-hooks flag (see
// internal/agent/agent_execute.go: req.DisableHooks -> a.reqDisableHooks,
// and agent_tools.go: hooksEnabled() reads exactly that field) — must still
// produce an effect row.
func TestRecordBenchEffects_SurvivesNoHooks(t *testing.T) {
	restoreGate := bench.SetObservationalHooksEnabled(true)
	defer restoreGate()
	ledgerDir := t.TempDir()
	restoreLedger := bench.SetBaseDirForTest(ledgerDir)
	defer restoreLedger()

	nonce := fmt.Sprintf("no-hooks-%d", time.Now().UnixNano())
	tool := newMutatingNonceTool(t, nonce)
	ag := newBenchToolCallingAgent(t, tool)

	hm := &countingHooksManager{}
	ag.SetHooksManager(hm) // a REAL hooks manager is attached...

	if _, err := ag.Execute(context.Background(), agent.ExecuteRequest{
		Message:      "call the tool",
		DisableHooks: true, // ...but this turn runs with it suppressed: --no-hooks.
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Anti-vacuity gate: the tool must have actually run, or everything below
	// is meaningless (this is the exact trap PLAN.md's verification section
	// warns about — "the tool never executed, permission denied for tool
	// bash").
	if tool.calls != 1 {
		t.Fatalf("tool did not actually execute (calls=%d); rest of test is vacuous", tool.calls)
	}
	if _, err := os.Stat(tool.path); err != nil {
		t.Fatalf("tool's file was not actually written to disk: %v", err)
	}

	// Confirm the hooks manager genuinely did NOT fire — otherwise this test
	// would not actually be exercising --no-hooks semantics at all.
	if hm.before != 0 || hm.after != 0 {
		t.Fatalf("hooks manager fired (before=%d after=%d) despite DisableHooks=true; this test is not exercising --no-hooks", hm.before, hm.after)
	}

	rows := readBenchRowsForTest(t, ledgerDir)
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 effect row from a --no-hooks turn, got %d: %+v", len(rows), rows)
	}
	got := rows[0]
	if got.Path != tool.path {
		t.Fatalf("row path = %q, want %q", got.Path, tool.path)
	}
	if got.Tool != "mutating_nonce_tool" {
		t.Fatalf("row tool = %q, want %q", got.Tool, "mutating_nonce_tool")
	}
	wantHash := bench.BlobHash([]byte("produced by " + nonce))
	if got.PostBlob != wantHash {
		t.Fatalf("row PostBlob = %q, want %q (real git blob hash of the written content)", got.PostBlob, wantHash)
	}
}

// TestRecordBenchEffects_SurvivesNoHooksManagerAtAll covers the OTHER shape
// of "no hooks": an agent that never had a HooksManager attached in the
// first place (the sub-agent / background configuration PLAN.md G3
// describes), as opposed to one that has a manager but suppressed it for one
// turn.
func TestRecordBenchEffects_SurvivesNoHooksManagerAtAll(t *testing.T) {
	restoreGate := bench.SetObservationalHooksEnabled(true)
	defer restoreGate()
	ledgerDir := t.TempDir()
	restoreLedger := bench.SetBaseDirForTest(ledgerDir)
	defer restoreLedger()

	nonce := fmt.Sprintf("no-manager-%d", time.Now().UnixNano())
	tool := newMutatingNonceTool(t, nonce)
	ag := newBenchToolCallingAgent(t, tool) // SetHooksManager is never called.

	if _, err := ag.Run(context.Background(), "call the tool"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tool.calls != 1 {
		t.Fatalf("tool did not actually execute (calls=%d)", tool.calls)
	}

	rows := readBenchRowsForTest(t, ledgerDir)
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 effect row with no HooksManager attached, got %d", len(rows))
	}
}

// TestRecordBenchEffects_GateOffProducesNothingEvenWithoutHooks is the
// control: proves the seam is still gate-respecting under --no-hooks, not
// "always on" — the drop from the acceptance test above to zero here must be
// caused ONLY by the gate, with DisableHooks held constant.
func TestRecordBenchEffects_GateOffProducesNothingEvenWithoutHooks(t *testing.T) {
	restoreGate := bench.SetObservationalHooksEnabled(false)
	defer restoreGate()
	ledgerDir := t.TempDir()
	restoreLedger := bench.SetBaseDirForTest(ledgerDir)
	defer restoreLedger()

	nonce := fmt.Sprintf("gate-off-%d", time.Now().UnixNano())
	tool := newMutatingNonceTool(t, nonce)
	ag := newBenchToolCallingAgent(t, tool)

	if _, err := ag.Execute(context.Background(), agent.ExecuteRequest{
		Message:      "call the tool",
		DisableHooks: true,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if tool.calls != 1 {
		t.Fatalf("tool did not actually execute (calls=%d); test is vacuous", tool.calls)
	}

	rows := readBenchRowsForTest(t, ledgerDir)
	if len(rows) != 0 {
		t.Fatalf("gate OFF must produce zero rows even under --no-hooks, got %d: %+v", len(rows), rows)
	}
}
