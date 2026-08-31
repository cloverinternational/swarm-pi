package builtin

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

func annoyanceEvent(conversation, tool, failure string) hooks.Event {
	data := map[string]any{"tool_name": tool}
	if failure != "" {
		data["error"] = failure
	}
	return hooks.Event{
		Type:           hooks.EventToolAfterExecute,
		ConversationID: conversation,
		Data:           data,
	}
}

func TestAnnoyanceNudgeContentSanitizationAndSkipAnnoyed(t *testing.T) {
	h := NewAnnoyanceNudgeHook()
	result, err := h.OnEvent(context.Background(), annoyanceEvent(
		"c",
		`Bash"><script>alert(1)</script>`,
		"secret raw failure detail",
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`Tool: "bash-script-alert-1-script"`,
		`Friction class: "tool-failure"`,
		"Fingerprint:",
		"call annoyed ONCE",
		"objective acceptance tests",
		"one near-miss that must remain allowed",
	} {
		if !strings.Contains(result.Message, want) {
			t.Errorf("message %q does not contain %q", result.Message, want)
		}
	}
	for _, unwanted := range []string{"secret raw failure detail", "<script>", `">`} {
		if strings.Contains(result.Message, unwanted) {
			t.Errorf("message %q contains unsanitized detail %q", result.Message, unwanted)
		}
	}

	result, err = h.OnEvent(context.Background(), annoyanceEvent("c", "annoyed", "report failed"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Message != "" {
		t.Fatalf("annoyed tool produced nudge %q", result.Message)
	}
}

func TestAnnoyanceNudgeIgnoresSuccessfulOutputProse(t *testing.T) {
	h := NewAnnoyanceNudgeHook()
	result, err := h.OnEvent(context.Background(), hooks.Event{
		Type:           hooks.EventToolAfterExecute,
		ConversationID: "c",
		Data: map[string]any{
			"tool_name": "Read",
			"output":    "source mentions fallback, unavailable, retry, workaround, and no output",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message != "" {
		t.Fatalf("successful output produced nudge %q", result.Message)
	}
}

func TestAnnoyanceNudgeIgnoresPhraseFalsePositives(t *testing.T) {
	for _, output := range []string{
		"fallback was selected because of invalid input",
		"retry is expected for this expected test failure",
		"workaround unavailable due to permission denial",
		"no output because the user cancelled",
	} {
		t.Run(output, func(t *testing.T) {
			h := NewAnnoyanceNudgeHook()
			result, err := h.OnEvent(context.Background(), hooks.Event{
				Type:           hooks.EventToolAfterExecute,
				ConversationID: "c",
				Data:           map[string]any{"tool_name": "vault_exec", "output": output},
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Message != "" {
				t.Fatalf("false positive produced nudge %q", result.Message)
			}
		})
	}
}

func TestAnnoyanceNudgeSuppressesOnlyConsecutiveIdenticalFailures(t *testing.T) {
	h := NewAnnoyanceNudgeHook()
	call := func(event hooks.Event) string {
		t.Helper()
		result, err := h.OnEvent(context.Background(), event)
		if err != nil {
			t.Fatal(err)
		}
		return result.Message
	}
	first := annoyanceEvent("c", "Bash", "failure A")
	if call(first) == "" {
		t.Fatal("first failure was not nudged")
	}
	if call(first) != "" {
		t.Fatal("identical consecutive failure was not suppressed")
	}
	if call(annoyanceEvent("c", "Bash", "failure B")) == "" {
		t.Fatal("different consecutive failure was suppressed")
	}
	if call(first) == "" {
		t.Fatal("non-consecutive repeated failure was suppressed")
	}
	if call(annoyanceEvent("other", "Bash", "failure A")) == "" {
		t.Fatal("failure in another conversation was suppressed")
	}
	if call(annoyanceEvent("c", "Read", "failure A")) == "" {
		t.Fatal("failure from another tool was suppressed")
	}
	if call(annoyanceEvent("c", "Bash", "")) != "" {
		t.Fatal("success produced a nudge")
	}
	if call(first) == "" {
		t.Fatal("success did not clear the failure fingerprint")
	}
}

func TestAnnoyanceNudgeConcurrentAccess(t *testing.T) {
	h := NewAnnoyanceNudgeHook()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = h.OnEvent(context.Background(), annoyanceEvent("c", "Bash", "failure"))
		}()
	}
	wg.Wait()
}
