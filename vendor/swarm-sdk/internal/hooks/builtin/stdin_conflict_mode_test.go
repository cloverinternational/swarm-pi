package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// conflictingCommand is the exact shape from issue #116: one simple command fed
// by a pipe on the left and a heredoc on the right.
const conflictingCommand = "printf 'piped\\n' | cat <<'EOF'\nheredoc\nEOF\n"

// benignCommand pipes but has no heredoc, so it must never trigger in any mode.
const benignCommand = "printf 'piped\\n' | cat"

func stdinModeEvent(command string) hooks.Event {
	return hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name": "Bash",
			"params":    map[string]any{"command": command},
		},
	}
}

func TestStdinConflictDefaultsToAdvise(t *testing.T) {
	// The default must stay advisory: blocking raises the cost of a false
	// positive, so it may only be reached deliberately.
	if got := NewStdinConflictHook().Mode(); got != StdinConflictAdvise {
		t.Fatalf("default mode = %v, want StdinConflictAdvise", got)
	}
}

func TestStdinConflictModeFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		want StdinConflictMode
	}{
		{"unset", "", StdinConflictAdvise},
		{"block", "block", StdinConflictBlock},
		{"block mixed case and spaces", "  BLOCK ", StdinConflictBlock},
		{"advise", "advise", StdinConflictAdvise},
		// A typo must never escalate to blocking commands.
		{"unrecognized", "blcok", StdinConflictAdvise},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(stdinConflictModeEnv, tc.env)
			if got := NewStdinConflictHook().Mode(); got != tc.want {
				t.Fatalf("mode for %q = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}

func TestStdinConflictBlockModeRefusesConflict(t *testing.T) {
	hook := NewStdinConflictHook()
	hook.SetMode(StdinConflictBlock)

	result, err := hook.OnEvent(context.Background(), stdinModeEvent(conflictingCommand))
	if err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}
	if result.Action != hooks.ActionBlock {
		t.Fatalf("action = %v, want ActionBlock", result.Action)
	}

	// The refusal replaces tool output entirely, so it must carry the diagnosis,
	// both repairs, and the escape hatch or the model cannot recover.
	for _, want := range []string{"Refusing to run", "drop the heredoc", "drop the pipe", stdinConflictModeEnv} {
		if !strings.Contains(result.Message, want) {
			t.Errorf("block message missing %q:\n%s", want, result.Message)
		}
	}
}

func TestStdinConflictBlockModeIgnoresBenignCommand(t *testing.T) {
	hook := NewStdinConflictHook()
	hook.SetMode(StdinConflictBlock)

	result, err := hook.OnEvent(context.Background(), stdinModeEvent(benignCommand))
	if err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}
	if result.Action != hooks.ActionContinue {
		t.Fatalf("action = %v, want ActionContinue for a command with no heredoc", result.Action)
	}
	if result.Message != "" {
		t.Fatalf("unexpected message for benign command: %q", result.Message)
	}
}

func TestStdinConflictBlockingIsNotRateLimited(t *testing.T) {
	// The advisory budget exists so a session is not nagged. Applying it to
	// blocking would silently reopen the hole after three refusals and would
	// make an A/B trial of the two policies meaningless past that point.
	hook := NewStdinConflictHook()
	hook.SetMode(StdinConflictBlock)

	for i := 0; i < maxStdinConflictAdvisories+3; i++ {
		result, err := hook.OnEvent(context.Background(), stdinModeEvent(conflictingCommand))
		if err != nil {
			t.Fatalf("call %d returned error: %v", i, err)
		}
		if result.Action != hooks.ActionBlock {
			t.Fatalf("call %d action = %v, want ActionBlock on every occurrence", i, result.Action)
		}
	}
}

func TestStdinConflictAdviseModeStillContinues(t *testing.T) {
	// Regression guard: adding a blocking mode must not change what the default
	// policy does, since that is the control arm of the benchmark.
	hook := NewStdinConflictHook()
	hook.SetMode(StdinConflictAdvise)

	result, err := hook.OnEvent(context.Background(), stdinModeEvent(conflictingCommand))
	if err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}
	if result.Action != hooks.ActionContinue {
		t.Fatalf("action = %v, want ActionContinue in advise mode", result.Action)
	}
	if !strings.Contains(result.Message, "advice, not a block") {
		t.Fatalf("advisory message changed unexpectedly:\n%s", result.Message)
	}
}
