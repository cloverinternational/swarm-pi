package agent

import "testing"

func TestBackgroundAgentResultReturnsSnapshot(t *testing.T) {
	t.Parallel()

	background := &BackgroundAgent{
		result: &BackgroundAgentResult{
			AgentID: "agent-1",
			Status:  StatusRunning,
			Metadata: map[string]any{
				"phase": "reading",
			},
		},
	}

	first := background.Result()
	first.AgentID = "mutated"
	first.Status = StatusFailed
	first.Metadata["phase"] = "mutated"

	second := background.Result()
	if second.AgentID != "agent-1" {
		t.Fatalf("AgentID = %q, want agent-1", second.AgentID)
	}
	if second.Status != StatusRunning {
		t.Fatalf("Status = %q, want %q", second.Status, StatusRunning)
	}
	if got := second.Metadata["phase"]; got != "reading" {
		t.Fatalf("metadata phase = %v, want reading", got)
	}
}
