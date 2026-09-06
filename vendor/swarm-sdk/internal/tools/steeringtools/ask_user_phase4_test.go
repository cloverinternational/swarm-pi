package steeringtools_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/steeringtools"
)

// fakeAskUserPusher records the args it's called with and returns a
// scripted answer/error pair.
type fakeAskUserPusher struct {
	gotQuestion string
	gotUrgency  string
	gotTimeout  time.Duration
	answer      string
	err         error
}

func (f *fakeAskUserPusher) Ask(ctx context.Context, q, u string, ttl time.Duration) (string, error) {
	f.gotQuestion = q
	f.gotUrgency = u
	f.gotTimeout = ttl
	return f.answer, f.err
}

// TestAskUser_Phase4SyncAnswer: when a pusher is present in ctx, the
// tool calls it synchronously and surfaces the user's answer.
func TestAskUser_Phase4SyncAnswer(t *testing.T) {
	pusher := &fakeAskUserPusher{answer: "yes"}
	ctx := agent.WithAskUserPusher(context.Background(), pusher)

	tool := steeringtools.NewAskUserTool()
	res, err := tool.Execute(ctx, map[string]any{
		"question":        "Continue?",
		"timeout_seconds": 30,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if pusher.gotQuestion != "Continue?" {
		t.Errorf("expected question='Continue?', got %q", pusher.gotQuestion)
	}
	if pusher.gotUrgency != "normal" {
		t.Errorf("expected urgency='normal', got %q", pusher.gotUrgency)
	}
	if pusher.gotTimeout != 30*time.Second {
		t.Errorf("expected timeout=30s, got %v", pusher.gotTimeout)
	}
	if got := res.Metadata["answer"]; got != "yes" {
		t.Errorf("expected answer='yes' in metadata, got %v", got)
	}
}

// TestAskUser_Phase4SyncErrorFallsBackToDefault: when the pusher errors,
// the tool surfaces params.Default as the answer and includes the error.
func TestAskUser_Phase4SyncErrorFallsBackToDefault(t *testing.T) {
	pusher := &fakeAskUserPusher{err: errors.New("user-cancelled")}
	ctx := agent.WithAskUserPusher(context.Background(), pusher)

	tool := steeringtools.NewAskUserTool()
	res, err := tool.Execute(ctx, map[string]any{
		"question": "Continue?",
		"default":  "stop",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := res.Metadata["answer"]; got != "stop" {
		t.Errorf("expected answer='stop' (default) on error, got %v", got)
	}
	if got := res.Metadata["error"]; got != "user-cancelled" {
		t.Errorf("expected error to be surfaced in metadata, got %v", got)
	}
}

// TestAskUser_Phase4EmptyAnswerFallsBackToDefault: when the pusher returns
// ("", nil) (e.g., nil-fn fallback inside the adapter), the tool uses
// params.Default.
func TestAskUser_Phase4EmptyAnswerFallsBackToDefault(t *testing.T) {
	pusher := &fakeAskUserPusher{answer: ""}
	ctx := agent.WithAskUserPusher(context.Background(), pusher)

	tool := steeringtools.NewAskUserTool()
	res, err := tool.Execute(ctx, map[string]any{
		"question": "Continue?",
		"default":  "fallback",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := res.Metadata["answer"]; got != "fallback" {
		t.Errorf("expected answer='fallback' on empty pusher answer, got %v", got)
	}
}

// TestAskUser_Phase3NoPusherFallback: when no pusher is in ctx, the tool
// uses Phase 3 behavior: record intent on the target, return default.
func TestAskUser_Phase3NoPusherFallback(t *testing.T) {
	target := agent.NewDefaultSteeringTarget()
	ctx := agent.WithSteeringTarget(context.Background(), target)

	tool := steeringtools.NewAskUserTool()
	res, err := tool.Execute(ctx, map[string]any{
		"question": "Continue?",
		"default":  "continue",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := res.Metadata["answer"]; got != "continue" {
		t.Errorf("expected default='continue', got %v", got)
	}

	// Phase 3 behavior: intent should be recorded on the target.
	insp := target.(agent.DefaultSteeringTargetInspector)
	if got := insp.SnapshotAsks(); len(got) != 1 {
		t.Errorf("expected 1 recorded ask, got %d", len(got))
	}
}

// TestAskUser_Phase4NoPusherNoTarget: when neither pusher nor target is
// in ctx, the tool still returns a clean result with the default.
func TestAskUser_Phase4NoPusherNoTarget(t *testing.T) {
	tool := steeringtools.NewAskUserTool()
	res, err := tool.Execute(context.Background(), map[string]any{
		"question": "Continue?",
		"default":  "ok",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := res.Metadata["answer"]; got != "ok" {
		t.Errorf("expected default='ok', got %v", got)
	}
}
