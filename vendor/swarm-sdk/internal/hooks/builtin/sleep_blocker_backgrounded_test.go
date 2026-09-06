package builtin

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// TestSleepBlockerHook_BackgroundedSleepAllowed proves issue #205: a sleep
// that is explicitly backgrounded with a lone `&` — used purely to hold a
// live PID as lock-ownership evidence — is allowed, because the Bash call
// returns immediately instead of idling on it.
func TestSleepBlockerHook_BackgroundedSleepAllowed(t *testing.T) {
	hook := NewSleepBlockerHook()
	tests := []struct {
		name    string
		command string
	}{
		{
			name:    "exact issue reproduction: backgrounded sleep as lock keeper",
			command: `mkdir .tick-lock && sleep 1800 >/dev/null 2>&1 &; keeper=$!; printf "%d" "$keeper" > .tick-lock/pid`,
		},
		{
			name:    "simple backgrounded sleep",
			command: `sleep 1800 &`,
		},
		{
			name:    "backgrounded sleep followed by pid capture on next statement",
			command: `sleep 300 & echo $!`,
		},
		{
			name:    "backgrounded sleep with redirects",
			command: `sleep 60 >/tmp/out 2>&1 &`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runSleepBlocker(t, hook, tt.command)
			if result.Action == hooks.ActionBlock {
				t.Errorf("backgrounded sleep should NOT be blocked: %q\nmessage: %s", tt.command, result.Message)
			}
		})
	}
}

// TestSleepBlockerHook_ForegroundSleepStillBlockedNearBackgroundedOnes proves
// the exemption is per-invocation, not per-command: if ANY matched sleep in
// the command is foreground (blocking), the command is still blocked even
// when another sleep in the same command is backgrounded.
func TestSleepBlockerHook_ForegroundSleepStillBlockedNearBackgroundedOnes(t *testing.T) {
	hook := NewSleepBlockerHook()
	tests := []struct {
		name    string
		command string
	}{
		{
			name:    "one backgrounded sleep, one foreground sleep",
			command: `sleep 60 & sleep 30`,
		},
		{
			name: "trailing bare foreground sleep after a backgrounded one",
			command: `sleep 5 &
sleep 10`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runSleepBlocker(t, hook, tt.command)
			if result.Action != hooks.ActionBlock {
				t.Errorf("command with a foreground sleep SHOULD still be blocked: %q", tt.command)
			}
		})
	}
}

// TestIsBackgroundedSleepClause is a narrow unit test on the clause-boundary
// logic itself, covering the specific false-positive traps called out in its
// doc comment (a `&&` chain, and a `2>&1` redirect earlier in the clause).
func TestIsBackgroundedSleepClause(t *testing.T) {
	cases := []struct {
		name string
		rest string
		want bool
	}{
		{"plain duration, no background", "1800", false},
		{"chained with &&, not backgrounded", "5 && tail f", false},
		{"semicolon terminated, not backgrounded", "30; echo done", false},
		{"backgrounded with trailing &", "1800 &", true},
		{"backgrounded then semicolon", "1800 &; echo next", true},
		{"backgrounded with redirects", "1800 >/dev/null 2>&1 &", true},
		{"redirect only, not backgrounded (ends in digit not &)", "1800 2>&1", false},
		{"newline terminated, not backgrounded", "5\necho ok", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isBackgroundedSleepClause(tc.rest)
			if got != tc.want {
				t.Errorf("isBackgroundedSleepClause(%q) = %v, want %v", tc.rest, got, tc.want)
			}
		})
	}
}
