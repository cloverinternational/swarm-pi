package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

func TestSleepBlockerHook_Filter(t *testing.T) {
	hook := NewSleepBlockerHook()

	tests := []struct {
		name     string
		event    hooks.Event
		expected bool
	}{
		{
			name: "pre-tool event with bash tool",
			event: hooks.Event{
				Type: hooks.EventToolBeforeExecute,
				Data: map[string]any{
					"tool_name": "Bash",
				},
			},
			expected: true,
		},
		{
			name: "pre-tool event with Read tool",
			event: hooks.Event{
				Type: hooks.EventToolBeforeExecute,
				Data: map[string]any{
					"tool_name": "Read",
				},
			},
			expected: false,
		},
		{
			name: "post-tool event with bash",
			event: hooks.Event{
				Type: hooks.EventToolAfterExecute,
				Data: map[string]any{
					"tool_name": "Bash",
				},
			},
			expected: false,
		},
		{
			name: "disabled hook",
			event: hooks.Event{
				Type: hooks.EventToolBeforeExecute,
				Data: map[string]any{
					"tool_name": "Bash",
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "disabled hook" {
				hook.SetEnabled(false)
			}
			result := hook.Filter(tt.event)
			if result != tt.expected {
				t.Errorf("Filter() = %v, want %v", result, tt.expected)
			}
			if tt.name == "disabled hook" {
				hook.SetEnabled(true) // reset
			}
		})
	}
}

func TestSleepBlockerHook_OnEvent_BlocksSleep(t *testing.T) {
	hook := NewSleepBlockerHook()
	ctx := context.Background()

	tests := []struct {
		name        string
		command     string
		shouldBlock bool
	}{
		{
			name:        "sleep command",
			command:     "sleep 5",
			shouldBlock: true,
		},
		{
			name:        "sleep with variable",
			command:     "sleep $timeout",
			shouldBlock: true,
		},
		{
			name:        "bounded timeout command is allowed",
			command:     "timeout 10 command",
			shouldBlock: false,
		},
		{
			name:        "bounded timeout with seconds is allowed",
			command:     "timeout 60s long-running-task",
			shouldBlock: false,
		},
		{
			name:        "session reproduction: bounded python analysis",
			command:     "timeout 300 python3 scripts/results.py",
			shouldBlock: false,
		},
		{
			name:        "timeout word inside test selector",
			command:     "go test -run TestSleepTimeout ./...",
			shouldBlock: false,
		},
		{
			name:        "grep command (no block)",
			command:     "grep pattern file.txt",
			shouldBlock: false,
		},
		{
			name:        "echo command (no block)",
			command:     "echo 'hello world'",
			shouldBlock: false,
		},
		{
			name:        "empty command",
			command:     "",
			shouldBlock: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := hooks.Event{
				Type: hooks.EventToolBeforeExecute,
				Data: map[string]any{
					"tool_name": "Bash",
					"params": map[string]any{
						"command": tt.command,
					},
				},
			}

			result, err := hook.OnEvent(ctx, event)
			if err != nil {
				t.Fatalf("OnEvent() error = %v", err)
			}

			if tt.shouldBlock {
				if result.Action != hooks.ActionBlock {
					t.Errorf("OnEvent() action = %v, want %v for command %q",
						result.Action, hooks.ActionBlock, tt.command)
				}
				if result.Message == "" {
					t.Errorf("OnEvent() message is empty for blocked command %q", tt.command)
				}
			} else {
				if result.Action != hooks.ActionContinue {
					t.Errorf("OnEvent() action = %v, want %v for command %q",
						result.Action, hooks.ActionContinue, tt.command)
				}
			}
		})
	}
}

func TestSleepBlockerHook_BlockMessage(t *testing.T) {
	hook := NewSleepBlockerHook()
	ctx := context.Background()

	event := hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name": "Bash",
			"params": map[string]any{
				"command": "sleep 10",
			},
		},
	}

	result, err := hook.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}

	if result.Action != hooks.ActionBlock {
		t.Fatalf("OnEvent() action = %v, want %v", result.Action, hooks.ActionBlock)
	}

	// Check that message contains helpful suggestions
	msg := result.Message
	if msg == "" {
		t.Error("OnEvent() message is empty")
	}

	// Verify the message steers toward the desired until-loop polling pattern.
	if !strings.Contains(msg, "until") {
		t.Errorf("OnEvent() message doesn't mention an until-loop: %s", msg)
	}

	// Verify the message offers run_in_background for started commands.
	if !strings.Contains(msg, "run_in_background") {
		t.Errorf("OnEvent() message doesn't mention run_in_background: %s", msg)
	}

	// Verify the anti-workaround prohibition is preserved.
	if !strings.Contains(msg, "Do not chain shorter sleeps") {
		t.Errorf("OnEvent() message doesn't preserve the no-chaining prohibition: %s", msg)
	}
}

// TestSleepBlockerHook_DerivedUntilSuggestion verifies that blocking a
// "sleep N && <cmd>" chain that references a file emits a concrete,
// copy-pasteable until-loop rewrite derived from the attempted command.
func TestSleepBlockerHook_DerivedUntilSuggestion(t *testing.T) {
	hook := NewSleepBlockerHook()
	ctx := context.Background()

	event := hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name": "Bash",
			"params": map[string]any{
				"command": "sleep 60 && tail -20 /tmp/tequesta_probe.log",
			},
		},
	}

	result, err := hook.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}
	if result.Action != hooks.ActionBlock {
		t.Fatalf("OnEvent() action = %v, want %v", result.Action, hooks.ActionBlock)
	}

	msg := result.Message
	// The derived rewrite must poll the actual referenced file with an
	// until-loop and preserve the original chained command verbatim.
	want := `until grep -q "SUCCESS\|TIMEOUT\|done" /tmp/tequesta_probe.log; do sleep 5; done && tail -20 /tmp/tequesta_probe.log`
	if !strings.Contains(msg, want) {
		t.Errorf("OnEvent() message missing derived until-loop rewrite.\nwant substring: %s\ngot: %s", want, msg)
	}
}

// TestSleepBlockerHook_BareSleepBlocked verifies a bare "sleep 300" with no
// chained command is still blocked (no threshold change) and falls back to
// generic guidance (no derived per-file rewrite).
func TestSleepBlockerHook_BareSleepBlocked(t *testing.T) {
	hook := NewSleepBlockerHook()
	ctx := context.Background()

	event := hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name": "Bash",
			"params": map[string]any{
				"command": "sleep 300",
			},
		},
	}

	result, err := hook.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}
	if result.Action != hooks.ActionBlock {
		t.Fatalf("OnEvent() action = %v, want %v for bare sleep", result.Action, hooks.ActionBlock)
	}
	if !strings.Contains(result.Message, "run_in_background") {
		t.Errorf("bare-sleep block message should offer run_in_background: %s", result.Message)
	}
}

// TestSleepBlockerHook_PollingLoopsAllowed verifies legitimate polling loops
// pass the hook even though they contain "sleep", including when chained to a
// follow-up command with &&.
func TestSleepBlockerHook_PollingLoopsAllowed(t *testing.T) {
	hook := NewSleepBlockerHook()
	ctx := context.Background()

	allowed := []struct {
		name    string
		command string
	}{
		{
			name:    "until grep loop then tail",
			command: `until grep -q X f; do sleep 5; done && tail f`,
		},
		{
			name:    "while-not curl loop",
			command: `while ! curl -s url; do sleep 2; done`,
		},
		{
			name:    "for loop with sleep",
			command: `for i in 1 2 3; do work; sleep 1; done`,
		},
		{
			name:    "until loop polling a file then cat",
			command: `until grep -q "SUCCESS\|TIMEOUT" /tmp/probe.log; do sleep 5; done && tail -30 /tmp/probe.log`,
		},
	}

	for _, tt := range allowed {
		t.Run(tt.name, func(t *testing.T) {
			event := hooks.Event{
				Type: hooks.EventToolBeforeExecute,
				Data: map[string]any{
					"tool_name": "Bash",
					"params": map[string]any{
						"command": tt.command,
					},
				},
			}
			result, err := hook.OnEvent(ctx, event)
			if err != nil {
				t.Fatalf("OnEvent() error = %v", err)
			}
			if result.Action != hooks.ActionContinue {
				t.Errorf("polling loop should be allowed but was %v: %q\nmsg: %s",
					result.Action, tt.command, result.Message)
			}
		})
	}
}

// TestSleepBlockerHook_ShortStandaloneSleep documents the current threshold
// behavior: there is NO numeric threshold; any bare standalone sleep (even a
// short "sleep 2") is blocked. This pins existing behavior so it isn't changed
// accidentally.
func TestSleepBlockerHook_ShortStandaloneSleep(t *testing.T) {
	hook := NewSleepBlockerHook()
	ctx := context.Background()

	event := hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name": "Bash",
			"params": map[string]any{
				"command": "sleep 2",
			},
		},
	}

	result, err := hook.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}
	if result.Action != hooks.ActionBlock {
		t.Errorf("short standalone 'sleep 2' should be blocked (no threshold), got %v", result.Action)
	}
}

func TestSleepBlockerHook_SetEnabledToggle(t *testing.T) {
	hook := NewSleepBlockerHook()

	if !hook.IsEnabled() {
		t.Error("NewSleepBlockerHook() should start enabled")
	}

	hook.SetEnabled(false)
	if hook.IsEnabled() {
		t.Error("SetEnabled(false) didn't disable hook")
	}

	hook.SetEnabled(true)
	if !hook.IsEnabled() {
		t.Error("SetEnabled(true) didn't re-enable hook")
	}
}

func TestSleepBlockerHook_Name(t *testing.T) {
	hook := NewSleepBlockerHook()
	expected := "sleep-blocker"

	if hook.Name() != expected {
		t.Errorf("Name() = %q, want %q", hook.Name(), expected)
	}
}

func TestSleepBlockerHook_Priority(t *testing.T) {
	hook := NewSleepBlockerHook()
	priority := hook.Priority()

	if priority <= 0 {
		t.Errorf("Priority() = %d, want > 0", priority)
	}

	if priority >= 100 {
		t.Errorf("Priority() = %d, want < 100", priority)
	}
}

// Helper function to check if a string contains a substring
func sleepBlockerContains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// runSleepBlocker is a small helper that pushes one command through the hook.
func runSleepBlocker(t *testing.T, hook *SleepBlockerHook, command string) hooks.HookResult {
	t.Helper()
	result, err := hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name": "Bash",
			"params": map[string]any{
				"command": command,
			},
		},
	})
	if err != nil {
		t.Fatalf("OnEvent() error = %v", err)
	}
	return result
}

// TestSleepBlockerHook_HeredocFalsePositives is the regression suite for #118.
//
// The hook used to match `\b(sleep|timeout)\s+` against the RAW command, so any
// heredoc that embedded file content with an identifier named `timeout` or
// `sleep` was blocked even though it invoked neither command. The reported case
// was a `python3 - <<'PYEOF'` heredoc writing Go source, followed by go build /
// go test.
func TestSleepBlockerHook_HeredocFalsePositives(t *testing.T) {
	hook := NewSleepBlockerHook()

	tests := []struct {
		name    string
		command string
	}{
		{
			name: "python3 heredoc writing go source with timeout identifier",
			command: `python3 - <<'PYEOF'
import pathlib
src = pathlib.Path("internal/run/wait.go")
src.write_text("""
package run

func wait(deadline time.Time) {
	timeout := time.Until(deadline)
	_ = timeout
}
""")
PYEOF
go build ./... && go test ./internal/run/ -count=1`,
		},
		{
			name: "heredoc body with python sleep_interval assignment",
			command: `cat > poll.py <<'EOF'
sleep_interval = 5
timeout = 30
print(sleep_interval, timeout)
EOF
python3 poll.py`,
		},
		{
			name: "heredoc writing a struct field named timeout",
			command: `cat > f.go <<'EOF'
package cfg

type Config struct {
	timeout time.Duration
}
EOF
gofmt -w f.go`,
		},
		{
			name: "two heredocs where the second body contains timeout",
			command: `cat > a.txt <<'EOF1'
just some text
EOF1
cat > b.go <<'EOF2'
timeout := time.Until(deadline)
EOF2
go build ./...`,
		},
		{
			name: "indented heredoc terminator with dash form",
			command: `cat > c.go <<-EOF
		timeout := 5
		EOF
go vet ./...`,
		},
		{
			name:    "sleep and timeout appear inside a test name",
			command: `go test -run TestSleepTimeout ./...`,
		},
		{
			name:    "until-loop polling localhost",
			command: `until curl -sf localhost:8080; do sleep 2; done`,
		},
		{
			name:    "here-string is not a heredoc and contains no sleep",
			command: `sudo -S mkdir -p /opt/x <<< "passwd"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runSleepBlocker(t, hook, tt.command)
			if result.Action != hooks.ActionContinue {
				t.Errorf("command should NOT be blocked (#118) but was %v:\n--- command ---\n%s\n--- message ---\n%s",
					result.Action, tt.command, result.Message)
			}
		})
	}
}

// TestSleepBlockerHook_StillBlocksRealSleeps guards against over-correcting
// #118 into a hook that no longer blocks anything. Every case here is a genuine
// sleep invocation in command position. GNU timeout is intentionally absent:
// `timeout N command` bounds useful work rather than idling.
func TestSleepBlockerHook_StillBlocksRealSleeps(t *testing.T) {
	hook := NewSleepBlockerHook()

	tests := []struct {
		name    string
		command string
	}{
		{
			name:    "leading sleep chained to tail",
			command: `sleep 60 && tail -20 /tmp/x.log`,
		},
		{
			name:    "sleep after semicolon",
			command: `cd /tmp; sleep 30`,
		},
		{
			name:    "sleep after and-and",
			command: `make build && sleep 5`,
		},
		{
			// False-negative guard: a newline IS a shell command separator, so
			// a bare sleep on its own line must still be caught. This is the
			// case a naive `(^|[;&|])` anchor would silently let through.
			name: "bare sleep on its own line in a multi-line command",
			command: `make build
sleep 30
tail -5 out.log`,
		},
		{
			name:    "sleep in a subshell",
			command: `(sleep 20; echo done)`,
		},
		{
			name: "real sleep after a heredoc block",
			command: `cat > f.go <<'EOF'
package main
EOF
sleep 45`,
		},
		{
			name:    "sleep piped",
			command: `echo hi | sleep 5`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runSleepBlocker(t, hook, tt.command)
			if result.Action != hooks.ActionBlock {
				t.Errorf("command SHOULD be blocked but was %v:\n--- command ---\n%s",
					result.Action, tt.command)
			}
			if result.Message == "" {
				t.Errorf("blocked command produced an empty message: %q", tt.command)
			}
		})
	}
}

// TestSleepBlockerHook_RewriteSurvivesHeredocStripping pins the requirement that
// the derived until-loop rewrite is still built from the ORIGINAL command text,
// not from the heredoc-stripped copy used for detection.
func TestSleepBlockerHook_RewriteSurvivesHeredocStripping(t *testing.T) {
	hook := NewSleepBlockerHook()

	result := runSleepBlocker(t, hook, "sleep 60 && tail -20 /tmp/x.log")
	if result.Action != hooks.ActionBlock {
		t.Fatalf("OnEvent() action = %v, want %v", result.Action, hooks.ActionBlock)
	}

	want := `until grep -q "SUCCESS\|TIMEOUT\|done" /tmp/x.log; do sleep 5; done && tail -20 /tmp/x.log`
	if !strings.Contains(result.Message, want) {
		t.Errorf("missing derived until-loop rewrite.\nwant substring: %s\ngot: %s", want, result.Message)
	}
}

// TestStripHeredocs exercises the heredoc scanner directly, including the
// ordering behaviour that a single greedy regex would get wrong.
func TestStripHeredocs(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{
			name:    "no heredoc is returned unchanged",
			command: "go build ./... && go test ./...",
			want:    "go build ./... && go test ./...",
		},
		{
			name:    "quoted delimiter",
			command: "cat <<'EOF'\ntimeout := 1\nEOF\ngo build ./...",
			want:    "cat <<'EOF'\ngo build ./...",
		},
		{
			name:    "double-quoted delimiter",
			command: "cat <<\"EOF\"\nsleep 5\nEOF\necho ok",
			want:    "cat <<\"EOF\"\necho ok",
		},
		{
			name:    "unquoted delimiter",
			command: "cat <<EOF\nsleep 5\nEOF\necho ok",
			want:    "cat <<EOF\necho ok",
		},
		{
			name:    "dash form with tab-indented terminator",
			command: "cat <<-EOF\n\tsleep 5\n\tEOF\necho ok",
			want:    "cat <<-EOF\necho ok",
		},
		{
			name:    "two sequential heredocs are consumed independently",
			command: "cat <<'A'\nbody a\nA\ncat <<'B'\ntimeout 9 x\nB\necho ok",
			want:    "cat <<'A'\ncat <<'B'\necho ok",
		},
		{
			name:    "two heredocs opened on one line consume bodies in order",
			command: "diff <<'A' <<'B'\nfirst\nA\nsecond\nB\necho ok",
			want:    "diff <<'A' <<'B'\necho ok",
		},
		{
			name:    "here-string is left alone",
			command: "sudo -S id <<< \"passwd\"\necho ok",
			want:    "sudo -S id <<< \"passwd\"\necho ok",
		},
		{
			name:    "unterminated heredoc swallows the remainder",
			command: "cat <<'EOF'\ntimeout := 1\nstill body",
			want:    "cat <<'EOF'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripHeredocs(tt.command)
			if got != tt.want {
				t.Errorf("stripHeredocs()\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// TestSleepBlockerHook_AllowListAndEnvOverride pins the pre-existing allow-list
// and SWARM_ALLOW_SLEEP escape hatch, which must survive the #118 fix.
func TestSleepBlockerHook_AllowListAndEnvOverride(t *testing.T) {
	hook := NewSleepBlockerHook()

	t.Run("allow-listed slow installer", func(t *testing.T) {
		result := runSleepBlocker(t, hook, "npm install && sleep 5")
		if result.Action != hooks.ActionContinue {
			t.Errorf("allow-listed command should pass, got %v", result.Action)
		}
	})

	t.Run("SWARM_ALLOW_SLEEP override", func(t *testing.T) {
		t.Setenv("SWARM_ALLOW_SLEEP", "1")
		result := runSleepBlocker(t, hook, "sleep 30")
		if result.Action != hooks.ActionContinue {
			t.Errorf("SWARM_ALLOW_SLEEP=1 should allow sleep, got %v", result.Action)
		}
	})
}
