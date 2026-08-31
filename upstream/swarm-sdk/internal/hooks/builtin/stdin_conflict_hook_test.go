package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// bashEvent builds a pre-tool-execute event carrying a bash command.
func bashEvent(command string) hooks.Event {
	return hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name": "Bash",
			"params": map[string]any{
				"command": command,
			},
		},
	}
}

func TestStdinConflictHook_Filter(t *testing.T) {
	tests := []struct {
		name     string
		event    hooks.Event
		disabled bool
		want     bool
	}{
		{
			name:  "pre-tool event with Bash tool",
			event: hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"tool_name": "Bash"}},
			want:  true,
		},
		{
			name:  "pre-tool event with lowercase bash tool",
			event: hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"tool_name": "bash"}},
			want:  true,
		},
		{
			name:  "tool name via 'name' fallback key",
			event: hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"name": "Bash"}},
			want:  true,
		},
		{
			name:  "pre-tool event with Read tool",
			event: hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"tool_name": "Read"}},
			want:  false,
		},
		{
			name:  "post-tool event with Bash tool",
			event: hooks.Event{Type: hooks.EventToolAfterExecute, Data: map[string]any{"tool_name": "Bash"}},
			want:  false,
		},
		{
			name:     "disabled hook",
			event:    hooks.Event{Type: hooks.EventToolBeforeExecute, Data: map[string]any{"tool_name": "Bash"}},
			disabled: true,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook := NewStdinConflictHook()
			if tt.disabled {
				hook.SetEnabled(false)
				if hook.IsEnabled() {
					t.Fatal("IsEnabled() = true after SetEnabled(false)")
				}
			}
			if got := hook.Filter(tt.event); got != tt.want {
				t.Errorf("Filter() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestStdinConflictHook_OnEvent is the core table. Every row asserts BOTH the
// expected advisory behaviour AND — unconditionally — that the hook never
// returns ActionBlock. This hook is advisory by design (issue #116): a false
// positive must cost one extra sentence, never a blocked command.
func TestStdinConflictHook_OnEvent(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		wantAdvise bool
		why        string
	}{
		// ---- SHOULD ADVISE: genuine pipe + heredoc stdin conflict ----
		{
			name:       "ssh pipeline with quoted heredoc (the issue #116 shape)",
			command:    "cat f.txt | ssh host 'cmd' <<'EOF'\nbody\nEOF",
			wantAdvise: true,
			why:        "pipe feeds ssh stdin and heredoc also feeds ssh stdin",
		},
		{
			name:       "tee with unquoted heredoc after a pipe",
			command:    "echo hi | tee out <<EOF\nx\nEOF",
			wantAdvise: true,
			why:        "pipe feeds tee stdin and heredoc also feeds tee stdin",
		},
		{
			name:       "tab-stripping heredoc after a pipe",
			command:    "cat f | ssh host <<-EOF\n\tbody\n\tEOF",
			wantAdvise: true,
			why:        "<<- is still a heredoc",
		},
		{
			name:       "heredoc on a middle pipeline segment",
			command:    "cat a | ssh host <<EOF | grep x\nbody\nEOF",
			wantAdvise: true,
			why:        "ssh is fed by both the pipe and the heredoc",
		},

		// ---- MUST STAY SILENT: false-positive guards ----
		{
			name:       "empty command",
			command:    "",
			wantAdvise: false,
			why:        "nothing to inspect",
		},
		{
			name:       "pipe with no heredoc",
			command:    "cat f | grep x",
			wantAdvise: false,
			why:        "only one stdin source",
		},
		{
			name:       "heredoc with no pipe",
			command:    "ssh host 'cmd' <<'EOF'\nbody\nEOF",
			wantAdvise: false,
			why:        "only one stdin source",
		},
		{
			name:       "heredoc attached to an earlier pipeline segment",
			command:    "cmd <<EOF\nbody\nEOF | grep x",
			wantAdvise: false,
			why:        "grep is fed only by the pipe; cmd only by the heredoc",
		},
		{
			name:       "heredoc opener before the pipe on the same line",
			command:    "cmd <<EOF | grep x\nbody\nEOF",
			wantAdvise: false,
			why:        "the heredoc belongs to cmd, which precedes the pipe",
		},
		{
			name:       "logical or with a quoted shift operator",
			command:    "grep -q x f || echo \"a << b\"",
			wantAdvise: false,
			why:        "|| is not a pipe and the << is inside double quotes",
		},
		{
			name:       "logical or with a single-quoted shift operator",
			command:    "grep -q x f || echo 'a << b'",
			wantAdvise: false,
			why:        "|| is not a pipe and the << is inside single quotes",
		},
		{
			name:       "pipe character inside a heredoc body",
			command:    "python3 - <<'PY'\nprint(\"a | b\")\nPY",
			wantAdvise: false,
			why:        "the | is heredoc payload, not shell syntax",
		},
		{
			name:       "C++ stream operators inside a heredoc body",
			command:    "cat > main.cpp <<'EOF'\nint main() { std::cout << x << \" | \" << y; }\nEOF",
			wantAdvise: false,
			why:        "<< and | in the body are C++ source, not shell syntax",
		},
		{
			name:       "Go source with shift operators inside a heredoc body",
			command:    "cat > x.go <<'EOF'\nconst a = 1 << 20\nfunc f() { b := c | d }\nEOF",
			wantAdvise: false,
			why:        "<< and | in the body are Go source, not shell syntax",
		},
		{
			name:       "here-string piped onward",
			command:    "cat f <<< \"here string\" | wc -l",
			wantAdvise: false,
			why:        "<<< is a here-string, explicitly out of scope",
		},
		{
			name:       "here-string on the right of a pipe",
			command:    "echo hi | cat <<< \"here string\"",
			wantAdvise: false,
			why:        "<<< is a here-string, explicitly out of scope",
		},
		{
			name:       "heredoc inside a command substitution",
			command:    "out=$(cat f | grep x) && cat <<EOF\n$out\nEOF",
			wantAdvise: false,
			why:        "the pipe is nested in $(...) and && separates the commands",
		},
		{
			name:       "shift operator inside backticks",
			command:    "echo `expr 1 + 1` | cat; echo \"1 << 2\"",
			wantAdvise: false,
			why:        "no real heredoc anywhere",
		},
		{
			name:       "pipe then semicolon then heredoc",
			command:    "cat a | grep b ; cat <<EOF\nbody\nEOF",
			wantAdvise: false,
			why:        "the semicolon ends the pipeline before the heredoc",
		},
		{
			name:       "pipe then && then heredoc",
			command:    "cat a | grep b && cat <<EOF\nbody\nEOF",
			wantAdvise: false,
			why:        "&& ends the pipeline before the heredoc",
		},
		{
			name:       "pipeline on one line, heredoc command on the next",
			command:    "cat a | grep b\ncat <<EOF\nbody\nEOF",
			wantAdvise: false,
			why:        "a hard newline ends the pipeline",
		},
		{
			name:       "escaped pipe character",
			command:    "echo \\| && cat <<EOF\nbody\nEOF",
			wantAdvise: false,
			why:        "the pipe is escaped, so it is literal text",
		},
		{
			name:       "heredoc body containing a nested pipeline and heredoc",
			command:    "cat > script.sh <<'EOF'\ncat f | ssh host 'cmd' <<'INNER'\nx\nINNER\nEOF",
			wantAdvise: false,
			why:        "the whole conflicting command is inert heredoc payload",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook := NewStdinConflictHook()
			result, err := hook.OnEvent(context.Background(), bashEvent(tt.command))
			if err != nil {
				t.Fatalf("OnEvent() unexpected error: %v", err)
			}

			// Invariant asserted on EVERY row: this hook is advisory only and
			// must never block, whatever the command looks like.
			if result.Action == hooks.ActionBlock {
				t.Fatalf("OnEvent() returned ActionBlock for %q; this hook must never block", tt.command)
			}
			if result.Action != hooks.ActionContinue {
				t.Fatalf("OnEvent() Action = %v, want ActionContinue for %q", result.Action, tt.command)
			}

			advised := result.Message != ""
			if advised != tt.wantAdvise {
				t.Errorf("advisory = %v, want %v (%s)\ncommand: %q\nmessage: %q",
					advised, tt.wantAdvise, tt.why, tt.command, result.Message)
			}
			if advised && !strings.Contains(result.Message, "stdin conflict") {
				t.Errorf("advisory message missing marker, got %q", result.Message)
			}
		})
	}
}

// TestStdinConflictHook_NeverBlocksOnHostileInput fuzzes the scanner with
// malformed and adversarial shell fragments. The only assertion that matters is
// that nothing blocks and nothing panics.
func TestStdinConflictHook_NeverBlocksOnHostileInput(t *testing.T) {
	commands := []string{
		"",
		"|",
		"||",
		"<<",
		"<<<",
		"<<-",
		"| <<",
		"cat 'unterminated | <<EOF",
		"cat \"unterminated | <<EOF",
		"cat `unterminated | <<EOF",
		"a | b <<",
		"a | b <<-",
		"a | b << ",
		"a | b <<''",
		"$(",
		"a | b $(c | d <<EOF)",
		"echo 'héllo ünicode' | cat <<'É'\nx\nÉ",
		strings.Repeat("a | b <<EOF\n", 50),
	}

	for _, cmd := range commands {
		t.Run(strings.ReplaceAll(cmd, "\n", "\\n"), func(t *testing.T) {
			hook := NewStdinConflictHook()
			result, err := hook.OnEvent(context.Background(), bashEvent(cmd))
			if err != nil {
				t.Fatalf("OnEvent() unexpected error: %v", err)
			}
			if result.Action == hooks.ActionBlock {
				t.Fatalf("OnEvent() returned ActionBlock for %q; this hook must never block", cmd)
			}
		})
	}
}

// TestStdinConflictHook_MalformedEventData covers missing/oddly-shaped event
// payloads: the hook must degrade to silence, never to a block.
func TestStdinConflictHook_MalformedEventData(t *testing.T) {
	tests := []struct {
		name string
		data map[string]any
	}{
		{name: "no params at all", data: map[string]any{"tool_name": "Bash"}},
		{name: "params is not a map", data: map[string]any{"tool_name": "Bash", "params": "nope"}},
		{name: "command missing", data: map[string]any{"tool_name": "Bash", "params": map[string]any{}}},
		{name: "command not a string", data: map[string]any{"tool_name": "Bash", "params": map[string]any{"command": 42}}},
		{name: "empty command", data: map[string]any{"tool_name": "Bash", "params": map[string]any{"command": ""}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook := NewStdinConflictHook()
			result, err := hook.OnEvent(context.Background(), hooks.Event{Type: hooks.EventToolBeforeExecute, Data: tt.data})
			if err != nil {
				t.Fatalf("OnEvent() unexpected error: %v", err)
			}
			if result.Action == hooks.ActionBlock {
				t.Fatal("OnEvent() returned ActionBlock; this hook must never block")
			}
			if result.Message != "" {
				t.Errorf("expected silence, got message %q", result.Message)
			}
		})
	}
}

// TestStdinConflictHook_ReadsToolInputFallback verifies the tool_input fallback
// path used by callers that do not populate "params".
func TestStdinConflictHook_ReadsToolInputFallback(t *testing.T) {
	hook := NewStdinConflictHook()
	event := hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name":  "Bash",
			"tool_input": map[string]any{"command": "cat f.txt | ssh host 'cmd' <<'EOF'\nbody\nEOF"},
		},
	}

	result, err := hook.OnEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("OnEvent() unexpected error: %v", err)
	}
	if result.Action == hooks.ActionBlock {
		t.Fatal("OnEvent() returned ActionBlock; this hook must never block")
	}
	if result.Message == "" {
		t.Error("expected advisory via tool_input fallback, got silence")
	}
}

// TestStdinConflictHook_RateLimited verifies the advisory stops after the
// per-session budget so a repeated pattern cannot flood the context window.
func TestStdinConflictHook_RateLimited(t *testing.T) {
	hook := NewStdinConflictHook()
	cmd := "cat f.txt | ssh host 'cmd' <<'EOF'\nbody\nEOF"

	advisories := 0
	for i := 0; i < maxStdinConflictAdvisories+3; i++ {
		result, err := hook.OnEvent(context.Background(), bashEvent(cmd))
		if err != nil {
			t.Fatalf("OnEvent() unexpected error: %v", err)
		}
		if result.Action == hooks.ActionBlock {
			t.Fatal("OnEvent() returned ActionBlock; this hook must never block")
		}
		if result.Message != "" {
			advisories++
		}
	}

	if advisories != maxStdinConflictAdvisories {
		t.Errorf("advisories = %d, want %d", advisories, maxStdinConflictAdvisories)
	}
}

func TestStdinConflictHook_Metadata(t *testing.T) {
	hook := NewStdinConflictHook()
	if got := hook.Name(); got != "stdin-conflict-advisory" {
		t.Errorf("Name() = %q", got)
	}
	if got := hook.Priority(); got != 84 {
		t.Errorf("Priority() = %d, want 84", got)
	}
	if !hook.IsEnabled() {
		t.Error("IsEnabled() = false for a new hook")
	}
}
