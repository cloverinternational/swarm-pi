package steeringtools_test

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/steeringtools"
)

// runTyped invokes a tool's typed Run via the generic adapter the
// Typed() factory wraps. It mirrors how the agent runtime dispatches
// tools: by calling Execute(ctx, map[string]any) on the produced
// tools.Tool. Kept as a documented anchor for future tests that need
// to assert errors out of typed-tool dispatch — currently no caller.
var _ = runTyped

func runTyped(t *testing.T, ctx context.Context, tool any, params map[string]any) {
	t.Helper()
	type executor interface {
		Execute(ctx context.Context, params map[string]any) (any, error)
	}
	exec, ok := tool.(executor)
	if !ok {
		t.Fatalf("tool does not implement Execute(ctx, map[string]any): %T", tool)
	}
	if _, err := exec.Execute(ctx, params); err != nil {
		t.Fatalf("tool Execute: %v", err)
	}
}

// inspector is the local view of the target methods we want to assert
// against in this test package.
type inspector interface {
	ConsumeBlockFor(toolName string) (*agent.PendingBlock, bool)
	DrainNotes() []agent.PendingNote
	ConsumeRefocus() (*agent.PendingRefocus, bool)
	SnapshotHalts() []agent.PendingHalt
	SnapshotConcerns() []agent.LoggedConcern
	SnapshotAsks() []agent.RecordedAskUser
}

func TestBlockNextTool_ArmsTargetWhenPresent(t *testing.T) {
	tgt := agent.NewDefaultSteeringTarget()
	ctx := agent.WithSteeringTarget(context.Background(), tgt)

	tool := steeringtools.NewBlockNextToolTool()
	if _, err := tool.Execute(ctx, map[string]any{
		"tool_name": "Bash",
		"reason":    "wasteful",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, ok := tgt.(inspector).ConsumeBlockFor("Bash")
	if !ok {
		t.Fatal("block not armed on target")
	}
	if got.Reason != "wasteful" {
		t.Errorf("wrong reason: %+v", got)
	}
}

func TestBlockNextTool_NoTargetFallsBackToStub(t *testing.T) {
	// No target in ctx — the tool must still return ok without panic.
	tool := steeringtools.NewBlockNextToolTool()
	res, err := tool.Execute(context.Background(), map[string]any{
		"reason": "test fallback",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
}

func TestInjectSystemNoteTool_QueuesOnTarget(t *testing.T) {
	tgt := agent.NewDefaultSteeringTarget()
	ctx := agent.WithSteeringTarget(context.Background(), tgt)

	tool := steeringtools.NewInjectSystemNoteTool()
	if _, err := tool.Execute(ctx, map[string]any{
		"text":     "stay focused",
		"priority": 7,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	notes := tgt.(inspector).DrainNotes()
	if len(notes) != 1 {
		t.Fatalf("expected 1 note queued, got %d", len(notes))
	}
	if notes[0].Text != "stay focused" || notes[0].Priority != 7 {
		t.Errorf("unexpected note: %+v", notes[0])
	}
}

func TestRefocusTool_QueuesOnTarget(t *testing.T) {
	tgt := agent.NewDefaultSteeringTarget()
	ctx := agent.WithSteeringTarget(context.Background(), tgt)

	tool := steeringtools.NewRefocusTool()
	if _, err := tool.Execute(ctx, map[string]any{
		"anchor":   "phase-2",
		"reminder": "wire the pump",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, ok := tgt.(inspector).ConsumeRefocus()
	if !ok {
		t.Fatal("refocus not queued")
	}
	if got.Anchor != "phase-2" || got.Reminder != "wire the pump" {
		t.Errorf("unexpected refocus: %+v", got)
	}
}

func TestHaltPeerLoopTool_RecordsOnTarget(t *testing.T) {
	tgt := agent.NewDefaultSteeringTarget()
	ctx := agent.WithSteeringTarget(context.Background(), tgt)

	tool := steeringtools.NewHaltPeerLoopTool()
	if _, err := tool.Execute(ctx, map[string]any{
		"peer":        "peer-a",
		"reason":      "ping-pong",
		"ttl_seconds": 120,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	halts := tgt.(inspector).SnapshotHalts()
	if len(halts) != 1 {
		t.Fatalf("expected 1 halt, got %d", len(halts))
	}
	if halts[0].Peer != "peer-a" || halts[0].TTLSeconds != 120 {
		t.Errorf("unexpected halt: %+v", halts[0])
	}
}

func TestAskUserTool_RecordsOnTarget(t *testing.T) {
	tgt := agent.NewDefaultSteeringTarget()
	ctx := agent.WithSteeringTarget(context.Background(), tgt)

	tool := steeringtools.NewAskUserTool()
	if _, err := tool.Execute(ctx, map[string]any{
		"question": "continue?",
		"default":  "yes",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	asks := tgt.(inspector).SnapshotAsks()
	if len(asks) != 1 {
		t.Fatalf("expected 1 ask, got %d", len(asks))
	}
	if asks[0].Question != "continue?" {
		t.Errorf("unexpected ask: %+v", asks[0])
	}
}

func TestLogConcernTool_RecordsOnTarget(t *testing.T) {
	tgt := agent.NewDefaultSteeringTarget()
	ctx := agent.WithSteeringTarget(context.Background(), tgt)

	tool := steeringtools.NewLogConcernTool()
	if _, err := tool.Execute(ctx, map[string]any{
		"tag":  "drift",
		"note": "subject wandered",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	cs := tgt.(inspector).SnapshotConcerns()
	if len(cs) != 1 {
		t.Fatalf("expected 1 concern, got %d", len(cs))
	}
	if cs[0].Text != "subject wandered" || cs[0].Severity != "drift" {
		t.Errorf("unexpected concern: %+v", cs[0])
	}
}

func TestObserveOnlyTool_NoTargetSideEffects(t *testing.T) {
	tgt := agent.NewDefaultSteeringTarget()
	ctx := agent.WithSteeringTarget(context.Background(), tgt)

	tool := steeringtools.NewObserveOnlyTool()
	if _, err := tool.Execute(ctx, map[string]any{"note": "all good"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Target must remain completely empty.
	insp := tgt.(inspector)
	if got := insp.SnapshotConcerns(); len(got) != 0 {
		t.Errorf("observe_only must not log a concern: %+v", got)
	}
	if got := insp.SnapshotAsks(); len(got) != 0 {
		t.Errorf("observe_only must not ask user: %+v", got)
	}
	if got, ok := insp.ConsumeBlockFor(""); ok {
		t.Errorf("observe_only must not arm a block: %+v", got)
	}
	if got, ok := insp.ConsumeRefocus(); ok {
		t.Errorf("observe_only must not refocus: %+v", got)
	}
	if got := insp.DrainNotes(); len(got) != 0 {
		t.Errorf("observe_only must not queue notes: %+v", got)
	}
}
