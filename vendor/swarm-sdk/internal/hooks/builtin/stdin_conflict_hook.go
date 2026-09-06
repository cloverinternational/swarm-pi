package builtin

import (
	"context"
	"os"
	"strings"
	"sync/atomic"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// StdinConflictHook reacts when a bash command feeds the same simple command
// two different stdin sources: a pipe on the left and a heredoc on the right,
// e.g.
//
//	cat f.txt | ssh host 'cmd' <<'EOF'
//	body
//	EOF
//
// Bash resolves this by letting the last redirection win, so one of the two
// input sources is silently discarded and the command fails in a confusing way
// (GitHub issue #116).
//
// The reaction is selected by Mode. Both modes share one detector, so the
// question "does blocking beat advising?" can be answered by measurement on a
// single binary rather than by argument — see SWARM_STDIN_CONFLICT_MODE.
//
// Design constraints (deliberate, do not relax):
//
//   - There is nothing to repair in the executor — the Bash tool already pins
//     the child's stdin to /dev/null (internal/tools/builtin/bash.go), so the
//     conflict lives entirely inside the model-supplied command string. The
//     only available actions are to describe what bash is about to do
//     silently, or to refuse the command and make the model rewrite it.
//   - A false positive must cost the agent one extra sentence, never a blocked
//     command. Under-triggering is correct; over-triggering is a bug. When the
//     scanner is unsure it stays silent. This is why the default is advisory:
//     blocking raises the price of a false positive, so it may only be enabled
//     deliberately.
//   - Detection is a real (small) quote/heredoc-aware scanner, not a regex.
//     Issue #118 is an active false-positive bug caused by naive regex matching
//     over command strings; shipping a second naive matcher would be a
//     self-inflicted regression.
type StdinConflictHook struct {
	enabled bool
	mode    StdinConflictMode

	// advisoryCount rate-limits the advisory so a session that legitimately
	// uses this shape repeatedly is not nagged on every call.
	advisoryCount atomic.Int64
}

// StdinConflictMode selects what the hook does when it detects a conflict.
type StdinConflictMode int

const (
	// StdinConflictAdvise appends a warning and lets the command run. The
	// command still executes with one of its two inputs silently discarded.
	StdinConflictAdvise StdinConflictMode = iota

	// StdinConflictBlock refuses the command and asks the model to rewrite it.
	// The argument for blocking is that an advisory arrives too late: by the
	// time the model reads it, the tool has already returned plausible output
	// that is missing half its input, and downstream reasoning may consume that
	// output before the warning is acted on.
	StdinConflictBlock
)

// stdinConflictModeEnv selects the mode at construction time. It exists so that
// both arms of an A/B trial can run the SAME binary with only this variable
// changed, which is the only way to attribute a difference in task outcomes to
// the policy rather than to the build.
//
// Recognized values: "block" enables blocking; "advise", empty, or anything
// unrecognized keeps the advisory default. An unrecognized value must not
// escalate to blocking — a typo should never start refusing commands.
const stdinConflictModeEnv = "SWARM_STDIN_CONFLICT_MODE"

// maxStdinConflictAdvisories caps how many times this hook speaks per hook
// instance (i.e. per session). The message is purely informational, so a small
// budget is enough to be useful without stuffing the context window.
//
// The cap applies to advisories ONLY. A blocking policy that stopped enforcing
// after three refusals would be neither a block nor an advisory, and would make
// a benchmark of the two policies meaningless after the third occurrence.
const maxStdinConflictAdvisories = 3

// NewStdinConflictHook creates a new stdin-conflict hook. It defaults to
// advisory and upgrades to blocking only when SWARM_STDIN_CONFLICT_MODE=block.
func NewStdinConflictHook() *StdinConflictHook {
	return &StdinConflictHook{
		enabled: true,
		mode:    stdinConflictModeFromEnv(),
	}
}

// stdinConflictModeFromEnv reads the mode from the environment, defaulting to
// advisory for any value it does not recognize.
func stdinConflictModeFromEnv() StdinConflictMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(stdinConflictModeEnv))) {
	case "block":
		return StdinConflictBlock
	default:
		return StdinConflictAdvise
	}
}

// SetMode overrides the detection response. Used by tests and by callers that
// configure the policy explicitly rather than through the environment.
func (h *StdinConflictHook) SetMode(mode StdinConflictMode) {
	h.mode = mode
}

// Mode reports the current detection response.
func (h *StdinConflictHook) Mode() StdinConflictMode {
	return h.mode
}

// Name returns the hook name.
func (h *StdinConflictHook) Name() string {
	return "stdin-conflict-advisory"
}

// Priority runs this hook alongside the other pre-execution bash inspectors.
// It is advisory only, so it sits just below the sleep blocker (85): anything
// that actually blocks should get to decide first, and there is no point
// annotating a command that another hook is about to reject.
func (h *StdinConflictHook) Priority() int {
	return 84
}

// Filter selects pre-tool events for the Bash tool only.
func (h *StdinConflictHook) Filter(event hooks.Event) bool {
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

// OnEvent inspects the bash command for a pipe-plus-heredoc stdin conflict. In
// advisory mode it returns Continue (silent) or ContinueWithMessage; in
// blocking mode a detected conflict returns hooks.Block.
func (h *StdinConflictHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
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

	if !hasPipeHeredocStdinConflict(command) {
		return hooks.Continue(), nil
	}

	// Blocking is unconditional: the whole point is that the command must not
	// run, so it is deliberately not subject to the advisory budget.
	if h.mode == StdinConflictBlock {
		return hooks.Block(stdinConflictBlockMessage()), nil
	}

	if h.advisoryCount.Add(1) > maxStdinConflictAdvisories {
		return hooks.Continue(), nil
	}

	return hooks.ContinueWithMessage(stdinConflictAdvisoryMessage()), nil
}

// stdinConflictAdvisoryMessage returns the advisory text. Kept short: this is a
// heads-up, not a lecture, and the command runs regardless.
func stdinConflictAdvisoryMessage() string {
	return "[stdin conflict] This command gives the same command two stdin sources: a pipe on the left and a heredoc on the right. " +
		"Bash applies the last redirection and silently discards the other input, which usually fails in a confusing way.\n\n" +
		"If you meant to send the piped data, drop the heredoc. If you meant to send the heredoc, drop the pipe " +
		"(e.g. inline the data, or use `ssh host 'cmd' < file`). Running as-is — this is advice, not a block."
}

// stdinConflictBlockMessage returns the refusal text. Unlike the advisory it
// must be self-sufficient: the model gets no tool output to reason from, so the
// message carries the diagnosis, both repairs, and the escape hatch.
func stdinConflictBlockMessage() string {
	return "[stdin conflict] Refusing to run: this command gives the same command two stdin sources, a pipe on the left and a heredoc on the right. " +
		"Bash applies the last redirection and silently discards the other input, so this would return plausible output that is missing half its input.\n\n" +
		"Rewrite it one of two ways:\n" +
		"  - keep the piped data: drop the heredoc, e.g. `printf '%s' \"$data\" | ssh host 'cmd'`\n" +
		"  - keep the heredoc: drop the pipe, e.g. `ssh host 'cmd' <<'EOF' ... EOF`, or read a file with `ssh host 'cmd' < file`\n\n" +
		"If the two inputs are genuinely for different commands, separate them so each simple command has one stdin source. " +
		"To disable this refusal for the session, set " + stdinConflictModeEnv + "=advise."
}

// SetEnabled toggles the hook at runtime.
func (h *StdinConflictHook) SetEnabled(enabled bool) {
	h.enabled = enabled
}

// IsEnabled reports whether the hook is active.
func (h *StdinConflictHook) IsEnabled() bool {
	return h.enabled
}

// ---------------------------------------------------------------------------
// Scanner
// ---------------------------------------------------------------------------

// hasPipeHeredocStdinConflict reports whether command contains a simple command
// that receives stdin from BOTH an unquoted pipe and an unquoted heredoc.
//
// The scanner walks the string once, tracking:
//
//   - single-quote and double-quote state (a `<<` or `|` inside quotes is text);
//   - `$(...)` / backtick nesting depth (a nested pipeline is its own scope, and
//     we cannot tell which of its segments owns what, so we ignore it);
//   - heredoc bodies, which are consumed wholesale so that `|`, `<<` and any
//     C++/Go source being written into a file cannot be mistaken for shell
//     syntax.
//
// It reports a conflict only when a heredoc opener is found while "inside the
// right-hand side of a pipeline" — that is, after a top-level `|` with no
// intervening command separator (`;`, `&&`, `||`, `&`, or a hard newline).
// That condition is exactly "this simple command is fed by a pipe AND by a
// heredoc".
func hasPipeHeredocStdinConflict(command string) bool {
	if command == "" {
		return false
	}
	// Cheap pre-filter: both ingredients must literally appear somewhere. This
	// is only an optimisation; the scanner below is the real decision.
	if !strings.Contains(command, "|") || !strings.Contains(command, "<<") {
		return false
	}

	s := []rune(command)
	n := len(s)

	// depth tracks $( ) and backtick nesting.
	depth := 0
	// pipedSegment is true when we are lexing a simple command whose stdin
	// comes from a pipe.
	pipedSegment := false
	// segHasContent is true once the current segment has seen a non-whitespace
	// character. Used so that a newline immediately after a trailing `|` (a
	// legal line continuation in bash) does not end the segment.
	segHasContent := false
	// pendingHeredocs holds delimiters opened on the current line whose bodies
	// start at the next newline.
	var pendingHeredocs []heredocDelim

	for i := 0; i < n; i++ {
		c := s[i]

		switch {
		case c == '\\':
			// Backslash escapes the next character (including a newline, which
			// is a line continuation). Skip both.
			i++
			segHasContent = true

		case c == '\'':
			j := skipSingleQuoted(s, i)
			if j < 0 {
				// Unterminated quote: the command is malformed, so we cannot
				// reason about it. Stay silent.
				return false
			}
			i = j
			segHasContent = true

		case c == '"':
			j := skipDoubleQuoted(s, i)
			if j < 0 {
				return false
			}
			i = j
			segHasContent = true

		case c == '`':
			j := skipBackquoted(s, i)
			if j < 0 {
				return false
			}
			i = j
			segHasContent = true

		case c == '$' && i+1 < n && s[i+1] == '(':
			depth++
			i++
			segHasContent = true

		case c == '(' && depth > 0:
			depth++
			segHasContent = true

		case c == ')' && depth > 0:
			depth--
			segHasContent = true

		case depth > 0:
			// Inside a command substitution: consume without interpreting.
			if !isShellSpace(c) {
				segHasContent = true
			}

		case c == '\n':
			// Heredoc bodies queued on this line start now.
			if len(pendingHeredocs) > 0 {
				i = consumeHeredocBodies(s, i+1, pendingHeredocs) - 1
				pendingHeredocs = pendingHeredocs[:0]
			}
			// A newline ends the current simple command, unless the segment is
			// still empty (i.e. the line ended with `|`, which continues).
			if segHasContent {
				pipedSegment = false
				segHasContent = false
			}

		case c == '<':
			// `<<<` is a here-string, not a heredoc. It supplies stdin, but it
			// is a single unambiguous redirection and the classic #116 shape
			// does not involve it, so we deliberately ignore it.
			if i+2 < n && s[i+1] == '<' && s[i+2] == '<' {
				i += 2
				segHasContent = true
				break
			}
			if i+1 < n && s[i+1] == '<' {
				// Heredoc opener: `<<` or `<<-`.
				delim, next, ok := parseHeredocDelim(s, i)
				if !ok {
					// Could not read a delimiter (e.g. `a << b` used as text in
					// an unquoted context we do not understand). Stay silent
					// rather than guess.
					return false
				}
				if pipedSegment {
					// Pipe on the left, heredoc on the right, same simple
					// command. This is the conflict.
					return true
				}
				pendingHeredocs = append(pendingHeredocs, delim)
				i = next - 1
				segHasContent = true
				break
			}
			// Plain `<` redirection or `<(` process substitution.
			segHasContent = true

		case c == '|':
			if i+1 < n && s[i+1] == '|' {
				// Logical OR — a command separator, never a pipe.
				i++
				pipedSegment = false
				segHasContent = false
				break
			}
			// `|` or `|&`: the next simple command reads from a pipe.
			pipedSegment = true
			segHasContent = false

		case c == '&':
			// `&&` or background `&`: both end the current simple command.
			if i+1 < n && s[i+1] == '&' {
				i++
			}
			pipedSegment = false
			segHasContent = false

		case c == ';':
			pipedSegment = false
			segHasContent = false

		default:
			if !isShellSpace(c) {
				segHasContent = true
			}
		}
	}

	return false
}

// heredocDelim describes one queued heredoc body.
type heredocDelim struct {
	word      string // the terminator word, with quotes removed
	stripTabs bool   // `<<-` strips leading tabs from the terminator line
}

// parseHeredocDelim reads a heredoc opener starting at s[i] == '<' (with
// s[i+1] == '<'). It returns the delimiter and the index just past it. ok is
// false when no plausible delimiter word follows.
func parseHeredocDelim(s []rune, i int) (heredocDelim, int, bool) {
	n := len(s)
	j := i + 2 // skip "<<"
	d := heredocDelim{}

	if j < n && s[j] == '-' {
		d.stripTabs = true
		j++
	}

	// Optional whitespace between the operator and the delimiter word.
	for j < n && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	if j >= n {
		return d, j, false
	}

	var word strings.Builder
	for j < n {
		c := s[j]
		switch {
		case c == '\'':
			k := skipSingleQuoted(s, j)
			if k < 0 {
				return d, j, false
			}
			word.WriteString(string(s[j+1 : k]))
			j = k + 1
		case c == '"':
			k := skipDoubleQuoted(s, j)
			if k < 0 {
				return d, j, false
			}
			word.WriteString(string(s[j+1 : k]))
			j = k + 1
		case c == '\\':
			if j+1 >= n {
				return d, j, false
			}
			word.WriteRune(s[j+1])
			j += 2
		case isDelimTerminator(c):
			d.word = word.String()
			return d, j, d.word != ""
		default:
			word.WriteRune(c)
			j++
		}
	}

	d.word = word.String()
	return d, j, d.word != ""
}

// isDelimTerminator reports whether c ends a heredoc delimiter word.
func isDelimTerminator(c rune) bool {
	switch c {
	case ' ', '\t', '\n', ';', '|', '&', '<', '>', '(', ')':
		return true
	}
	return false
}

// consumeHeredocBodies skips the bodies of every queued heredoc, starting at
// index start (the first character of the line after the opener line). It
// returns the index just past the last consumed terminator line, or len(s) if a
// terminator is never found (an unterminated heredoc swallows the rest).
func consumeHeredocBodies(s []rune, start int, delims []heredocDelim) int {
	n := len(s)
	pos := start

	for _, d := range delims {
		for pos <= n {
			// Find the end of the current line.
			lineEnd := pos
			for lineEnd < n && s[lineEnd] != '\n' {
				lineEnd++
			}
			line := string(s[pos:lineEnd])
			if d.stripTabs {
				line = strings.TrimLeft(line, "\t")
			}
			if strings.TrimRight(line, "\r") == d.word {
				// Terminator found; body ends here.
				pos = lineEnd
				if pos < n {
					pos++ // step over the newline
				}
				break
			}
			if lineEnd >= n {
				// Ran out of input without a terminator.
				return n
			}
			pos = lineEnd + 1
		}
	}

	return pos
}

// skipSingleQuoted returns the index of the closing `'` for the quote opening
// at s[i], or -1 if unterminated. Single quotes have no escapes.
func skipSingleQuoted(s []rune, i int) int {
	for j := i + 1; j < len(s); j++ {
		if s[j] == '\'' {
			return j
		}
	}
	return -1
}

// skipDoubleQuoted returns the index of the closing `"` for the quote opening
// at s[i], or -1 if unterminated. Backslash escapes the next character.
func skipDoubleQuoted(s []rune, i int) int {
	for j := i + 1; j < len(s); j++ {
		if s[j] == '\\' {
			j++
			continue
		}
		if s[j] == '"' {
			return j
		}
	}
	return -1
}

// skipBackquoted returns the index of the closing backtick for the one opening
// at s[i], or -1 if unterminated.
func skipBackquoted(s []rune, i int) int {
	for j := i + 1; j < len(s); j++ {
		if s[j] == '\\' {
			j++
			continue
		}
		if s[j] == '`' {
			return j
		}
	}
	return -1
}

// isShellSpace reports whether c is shell whitespace.
func isShellSpace(c rune) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}
