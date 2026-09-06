package builtin

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// SleepBlockerHook detects and blocks bare sleep commands in bash,
// actively steering agents toward a proper polling pattern instead of blocking
// mid-execution.  It never blocks legitimate polling loops
// (until/while/for ... sleep ... done) and, when it blocks a "sleep N && <cmd>"
// chain, it derives a ready-to-paste until-loop rewrite from the attempted
// command.
type SleepBlockerHook struct {
	enabled bool
}

// sleepCommandPattern matches sleep only when it appears in *command
// position*, i.e. at the start of the command string or immediately after a
// shell command separator:
//
//	sleep 5
//	sleep 0.5
//	sleep "$timeout"
//	cd /tmp; sleep 30
//	make build && sleep 5
//	make build<newline>sleep 30
//	until ...; do sleep 5; done      (matched, then exempted by isPollingLoop)
//
// It deliberately does NOT match the word "sleep"/"timeout" used as an
// ordinary identifier or argument, e.g. `timeout := time.Until(deadline)`,
// `sleep_interval = 5`, or `go test -run TestSleepTimeout ./...`. See #118.
//
// A newline counts as a command separator (it genuinely is one in shell), which
// is only safe because heredoc bodies are removed by stripHeredocs before this
// pattern is applied — otherwise every line of embedded file content would be
// treated as shell code.
//
// The optional `do|then|else|!` prefixes keep loop/conditional bodies matching
// so the pre-existing isPollingLoop exemption still gets a chance to run.
//
// A shell `timeout N command...` is deliberately NOT matched. It bounds a real
// command rather than idling, and is often the exact safety mechanism that
// prevents a process from hanging. Treating it as bare sleep creates the
// perverse result that the hook blocks the bounded form while allowing the
// unbounded command.
var sleepCommandPattern = regexp.MustCompile("(?:^|[\n;&|(`{])\\s*(?:(?:do|then|else|!)\\s+)*sleep\\s")

// heredocTag describes one pending heredoc body awaiting its terminator line.
type heredocTag struct {
	word string // the delimiter word, with any quotes removed
	dash bool   // <<- form: the terminator may be indented with tabs
}

// isHeredocWordChar reports whether c can appear in an unquoted heredoc
// delimiter word.
func isHeredocWordChar(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

// heredocOpeners returns every heredoc opener on a single line, in the order
// bash would consume their bodies. It understands <<WORD, <<-WORD, <<'WORD'
// and <<"WORD", and ignores <<< here-strings (which have no body).
func heredocOpeners(line string) []heredocTag {
	var tags []heredocTag
	for i := 0; i+1 < len(line); i++ {
		if line[i] != '<' || line[i+1] != '<' {
			continue
		}
		// `<<<` is a here-string, not a heredoc: no body follows. Skip the
		// whole operator so we don't rescan its trailing "<<" as an opener.
		if i+2 < len(line) && line[i+2] == '<' {
			i += 2
			continue
		}
		if i > 0 && line[i-1] == '<' {
			continue
		}

		j := i + 2
		var tag heredocTag
		if j < len(line) && line[j] == '-' {
			tag.dash = true
			j++
		}
		for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
			j++
		}
		// <<\EOF is the same as <<'EOF' (quoted, no expansion).
		if j < len(line) && line[j] == '\\' {
			j++
		}
		if j < len(line) && (line[j] == '\'' || line[j] == '"') {
			quote := line[j]
			j++
			start := j
			for j < len(line) && line[j] != quote {
				j++
			}
			if j >= len(line) {
				// Unterminated quote: not a usable delimiter, stop scanning.
				break
			}
			tag.word = line[start:j]
			j++
		} else {
			start := j
			for j < len(line) && isHeredocWordChar(line[j]) {
				j++
			}
			tag.word = line[start:j]
		}

		if tag.word == "" {
			// Something like `cmd << $var` — not a delimiter we can track.
			i = j - 1
			continue
		}
		tags = append(tags, tag)
		i = j - 1
	}
	return tags
}

// stripHeredocs removes heredoc *bodies* (and their terminator lines) from a
// command, leaving the shell code around them intact. The opener line itself is
// preserved because it is real shell code.
//
// This is what stops issue #118: a `python3 - <<'PYEOF' ... PYEOF` heredoc that
// writes Go source containing `timeout := time.Until(deadline)` is file
// content, not a command, and must not be scanned for sleep invocations.
//
// Multiple heredocs on one line, and multiple heredocs across a command, are
// consumed in order — deliberately not a single greedy regex, which would eat
// everything between the first opener and the last terminator.
func stripHeredocs(command string) string {
	if !strings.Contains(command, "<<") {
		return command
	}

	lines := strings.Split(command, "\n")
	kept := make([]string, 0, len(lines))

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		kept = append(kept, line)

		for _, tag := range heredocOpeners(line) {
			// Consume body lines until the terminator line, or EOF for an
			// unterminated heredoc (in which case the rest is all body).
			for i+1 < len(lines) {
				i++
				candidate := lines[i]
				if tag.dash {
					candidate = strings.TrimLeft(candidate, "\t")
				}
				if strings.TrimRight(candidate, " \t\r") == tag.word {
					break
				}
			}
		}
	}

	return strings.Join(kept, "\n")
}

// loopKeywordPattern matches the start of a shell polling loop. A bash loop
// that uses sleep to pace its iterations (until/while/for ... do ... done) is
// the *desired* pattern, so we must never block it.
var loopKeywordPattern = regexp.MustCompile(`\b(until|while|for)\b`)

// loopBodyPattern confirms the command is structured as an actual loop body
// (has a "do" and a "done"), so a stray variable named "while_count" or a file
// called "for.txt" doesn't get mistaken for a loop.
var loopBodyPattern = regexp.MustCompile(`\bdo\b[\s\S]*\bdone\b`)

// leadingSleepChainPattern captures a leading "sleep N" that is immediately
// chained (via &&, ;, or |) to a follow-up command. Group 1 is the sleep
// duration token, group 2 is the chained command. This is the classic
// "sleep 60 && tail -20 /tmp/x.log" anti-pattern we want to rewrite.
var leadingSleepChainPattern = regexp.MustCompile(`^\s*sleep\s+(\S+)\s*(?:&&|;)\s*(.+)$`)

// filePathPattern extracts the first plausible file path argument from a
// follow-up command (e.g. the "/tmp/x.log" in "tail -20 /tmp/x.log"). It looks
// for an absolute or dotted/relative path that contains a dot or slash so we
// don't mistake a flag or a bare word for a file.
var filePathPattern = regexp.MustCompile(`(?:^|\s)((?:\.{0,2}/|~/)?[\w./@-]*[\w]/[\w./@-]+|/[\w./@-]+|[\w.@-]+\.[\w]{1,8})`)

// allowedSleepPatterns contains commands that legitimately wait on long
// subprocesses (e.g. TeX package installation).  When the command contains
// one of these patterns, the hook allows the sleep/timeout through.
var allowedSleepPatterns = []string{
	"tlmgr",        // TeX Live package manager (slow installs)
	"pdflatex",     // LaTeX compilation (can be slow)
	"xelatex",      // LaTeX compilation
	"lualatex",     // LuaLaTeX compilation
	"npm install",  // Node package installation
	"pip install",  // Python package installation
	"go mod tidy",  // Go module sync
	"cargo build",  // Rust compilation
	"apt-get",      // System package installation
	"dnf install",  // System package installation
	"brew install", // Homebrew package installation
}

// isAllowedSleepCommand checks whether a command is on the allow-list.
func isAllowedSleepCommand(command string) bool {
	cmd := strings.ToLower(command)
	for _, pattern := range allowedSleepPatterns {
		if strings.Contains(cmd, strings.ToLower(pattern)) {
			return true
		}
	}
	// SWARM_ALLOW_SLEEP env var can override the blocker entirely.
	if os.Getenv("SWARM_ALLOW_SLEEP") == "1" {
		return true
	}
	return false
}

// isPollingLoop reports whether the command is a legitimate polling loop that
// uses sleep to pace its iterations, e.g.:
//
//	until grep -q SUCCESS f; do sleep 5; done
//	while ! curl -s url; do sleep 2; done
//	for i in 1 2 3; do work; sleep 1; done
//
// These are the desired pattern (including when followed by && <cmd>) and must
// never be blocked, even though they contain "sleep".
func isPollingLoop(command string) bool {
	return loopKeywordPattern.MatchString(command) && loopBodyPattern.MatchString(command)
}

// isBackgroundedSleepClause reports whether the shell clause beginning right
// after a matched "sleep " keyword is terminated by a lone job-control `&`
// (backgrounded) rather than `;`, a newline, or `&&` (all of which mean the
// sleep runs in the foreground and blocks whatever follows). rest is the
// command text starting immediately after "sleep ".
//
// This scans left to right for the first character that decides the
// question, which is what makes it safe against the two traps a naive
// suffix check falls into:
//   - `2>&1`-style fd-duplication redirects contain a `&` that is NOT a
//     job-control operator — it's always immediately preceded by `>`, so
//     that occurrence is skipped rather than treated as backgrounding.
//   - `&&` is a chain, not backgrounding: if the character right after `&`
//     is another `&`, this reports "not backgrounded" instead of continuing
//     to scan (which would otherwise find a LATER unrelated `&` and
//     misreport a foreground `sleep 5 && cmd &` as backgrounded).
func isBackgroundedSleepClause(rest string) bool {
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case ';', '\n':
			return false
		case '&':
			if i+1 < len(rest) && rest[i+1] == '&' {
				return false // "&&" chain, not backgrounding
			}
			if i > 0 && rest[i-1] == '>' {
				continue // "2>&1" / ">&2" fd duplication, not backgrounding
			}
			return true
		}
	}
	return false
}

// allSleepInvocationsBackgrounded reports whether every sleep invocation
// matched in command position is immediately backgrounded with a lone `&`
// job-control operator, e.g. `sleep 1800 >/dev/null 2>&1 &`, rather than run
// in the foreground where it would actually block the caller.
//
// A backgrounded sleep cannot function as the bare-wait anti-pattern this
// hook exists to catch: the Bash call returns immediately once the `&`
// backgrounds the process, and the sleeping process typically only serves as
// a live PID for something else — a lock's lifetime keeper, a timeout
// watchdog killed later, etc. See issue #205 (an atomic orchestration lock
// used `sleep 1800 >/dev/null 2>&1 &; keeper=$!; ...` purely to hold a PID,
// and was rejected outright before ever executing).
//
// If even ONE matched sleep invocation is NOT backgrounded, this returns
// false and the command is still blocked — a loop of bare foreground sleeps,
// or a single foreground `sleep 1800`, must remain blocked exactly as before.
func allSleepInvocationsBackgrounded(scanned string) bool {
	matches := sleepCommandPattern.FindAllStringIndex(scanned, -1)
	if len(matches) == 0 {
		return false
	}
	for _, m := range matches {
		if !isBackgroundedSleepClause(scanned[m[1]:]) {
			return false
		}
	}
	return true
}

// deriveUntilRewrite builds a concrete, copy-pasteable until-loop rewrite from a
// blocked "sleep N && <cmd>" command. If the chained command references a file,
// the rewrite polls that file for a done-marker; otherwise an empty string is
// returned and the caller falls back to generic guidance.
func deriveUntilRewrite(command string) string {
	m := leadingSleepChainPattern.FindStringSubmatch(command)
	if m == nil {
		return ""
	}
	chained := strings.TrimSpace(m[2])
	if chained == "" {
		return ""
	}

	// Try to pull a file path out of the chained command so we can poll it.
	if file := firstFilePath(chained); file != "" {
		return fmt.Sprintf(`until grep -q "SUCCESS\|TIMEOUT\|done" %s; do sleep 5; done && %s`, file, chained)
	}

	// No file reference: still give a runnable loop scaffold around the cmd.
	return fmt.Sprintf(`until <check>; do sleep 5; done && %s`, chained)
}

// firstFilePath returns the first plausible file-path argument in a command, or
// "" if none is found.
func firstFilePath(cmd string) string {
	m := filePathPattern.FindStringSubmatch(cmd)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// NewSleepBlockerHook creates a new sleep blocker hook.
func NewSleepBlockerHook() *SleepBlockerHook {
	return &SleepBlockerHook{
		enabled: true,
	}
}

// Name returns the hook name.
func (h *SleepBlockerHook) Name() string {
	return "sleep-blocker"
}

// Priority runs this hook early (before the command executes).
// Using 85 so it runs before general pre-tool evaluation but after
// critical task enforcement.
func (h *SleepBlockerHook) Priority() int {
	return 85
}

// Filter selects pre-tool events for Bash tool only.
func (h *SleepBlockerHook) Filter(event hooks.Event) bool {
	if !h.enabled {
		return false
	}

	// Only filter pre-tool events
	if event.Type != hooks.EventToolBeforeExecute {
		return false
	}

	// Extract tool name
	toolName, _ := event.Data["tool_name"].(string)
	if toolName == "" {
		if tn, ok := event.Data["name"].(string); ok {
			toolName = tn
		}
	}

	// Only apply to bash tool
	return toolName == "Bash" || toolName == "bash"
}

// OnEvent checks if the bash command contains a sleep directive.
func (h *SleepBlockerHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	// Extract command from event data
	params, _ := event.Data["params"].(map[string]any)
	if params == nil {
		params, _ = event.Data["tool_input"].(map[string]any)
	}

	if params == nil {
		return hooks.Continue(), nil
	}

	command, ok := params["command"].(string)
	if !ok || command == "" {
		return hooks.Continue(), nil
	}

	// Scan only the shell portion of the command: heredoc bodies are file
	// content, not commands, and must never trigger the blocker (#118).
	scanned := stripHeredocs(command)

	// Check whether sleep/timeout is invoked in command position.
	if sleepCommandPattern.MatchString(scanned) {
		// Allow-list: commands that legitimately wait on long subprocesses.
		if isAllowedSleepCommand(scanned) {
			return hooks.Continue(), nil
		}
		// Never block legitimate polling loops — they ARE the desired pattern.
		if isPollingLoop(scanned) {
			return hooks.Continue(), nil
		}
		// Never block a sleep that is explicitly backgrounded — it can't be
		// the bare-wait anti-pattern this hook exists to catch, since the
		// Bash call returns immediately instead of idling on it (#205).
		if allSleepInvocationsBackgrounded(scanned) {
			return hooks.Continue(), nil
		}
		// The block message is derived from the ORIGINAL command so the
		// "sleep N && <cmd>" rewrite suggestion stays verbatim-accurate.
		return hooks.Block(h.buildBlockMessage(command)), nil
	}

	return hooks.Continue(), nil
}

// buildBlockMessage produces a compact, model-facing block message. When the
// attempted command is a "sleep N && <cmd>" chain it embeds a concrete,
// copy-pasteable until-loop rewrite derived from <cmd>; otherwise it gives
// generic guidance (run_in_background or a hand-written until-loop).
func (h *SleepBlockerHook) buildBlockMessage(command string) string {
	rewrite := deriveUntilRewrite(command)

	if rewrite != "" {
		return fmt.Sprintf(`Blocked: a bare sleep can't wait for a condition. To wait until something is ready, poll with an until-loop instead. Replace this command with:

  %s

(Adjust the marker/condition and the trailing command for your case. To wait for a command you started, use run_in_background: true. Do not chain shorter sleeps to work around this block — set timeout_seconds on the Bash call if you only need a hard cap.)`, rewrite)
	}

	return `Blocked: a bare sleep can't wait for a condition. Use one of these instead:

  - Poll until a condition holds:  until <check>; do sleep 5; done && <next-command>
  - Wait for a command you started: run_in_background: true, then read its output when notified.
  - Just need a hard cap on a single command? Set timeout_seconds on the Bash call.

Do not chain shorter sleeps to work around this block.`
}

// SetEnabled toggles the hook at runtime.
func (h *SleepBlockerHook) SetEnabled(enabled bool) {
	h.enabled = enabled
}

// IsEnabled reports whether the hook is active.
func (h *SleepBlockerHook) IsEnabled() bool {
	return h.enabled
}
