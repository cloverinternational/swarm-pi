// Package hooks — shared tool classification helpers.
//
// Several built-in hooks (task-enforcement, task-maintenance-reminder,
// skill-budget-enforcement, and the autogenskills lifecycle hook) each need
// to answer the same handful of questions about a tool name or a Bash
// command: "is this a task tool?", "is this a skill tool?", "is this
// read-only research?". Before this file existed, every hook kept its own
// copy of these classifiers, and the copies drifted — most notably,
// task-enforcement exempted read-only exploration tools (Read, Grep,
// read-only git/bash, ...) from needing a task, but skill-budget-enforcement
// had no equivalent exemption, so pure research silently drained the skill
// budget and forced an unrelated "create a skill" detour. Centralizing the
// classifiers here means every hook that gates on "is this tool exempt from
// ceremony X" answers the same question the same way.
package hooks

import "strings"

// NormalizeToolName lowercases a tool name and strips underscores so
// "TaskManage", "task_manage", and "taskmanage" all compare equal.
func NormalizeToolName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "_", ""))
}

// IsTaskManagementTool reports whether name is one of the task-tracking
// tools: TaskManage (the canonical control-plane tool) and its legacy
// TaskCreate/TaskList/TaskGet/TaskUpdate/TodoWrite/TodoRead predecessors.
// These are always exempt from task-enforcement (an agent must be able to
// create a task to satisfy it) and from skill-budget-enforcement (planning
// must be able to continue even mid-budget).
func IsTaskManagementTool(name string) bool {
	switch NormalizeToolName(name) {
	case "taskcreate", "tasklist", "taskget", "taskupdate", "taskmanage", "todowrite", "todoread":
		return true
	}
	return false
}

// IsTaskManageTool reports whether name is specifically the TaskManage
// control-plane tool (as opposed to one of its legacy single-purpose
// predecessors). Hooks that need to parse a TaskManage batch's operations
// (see SuccessfulTaskManageOperations) use this to decide whether that
// parsing applies.
func IsTaskManageTool(name string) bool {
	return NormalizeToolName(name) == "taskmanage"
}

// IsSkillTool reports whether name is a skill management/invocation tool:
// SkillManage, or one of the Skill invocation aliases used across
// different harnesses/editors.
func IsSkillTool(name string) bool {
	switch NormalizeToolName(name) {
	case "skillmanage", "skillinvoke", "skillcall", "useskill", "skillexec", "skill":
		return true
	}
	return false
}

// IsPlanModeTool reports whether name is enter_plan_mode or exit_plan_mode.
func IsPlanModeTool(name string) bool {
	switch NormalizeToolName(name) {
	case "enterplanmode", "exitplanmode":
		return true
	}
	return false
}

// IsUserInteractionTool reports whether name is a tool that talks to the
// user/companion UI or defect-reporting control plane rather than the workspace — it changes no local state,
// so it is safe to exempt from both task-enforcement and
// skill-budget-enforcement.
func IsUserInteractionTool(name string) bool {
	switch NormalizeToolName(name) {
	case "askuserquestion", "pushagentupdate", "annoyed":
		return true
	}
	return false
}

// IsReadOnlyExplorationTool reports whether name is a read-only research
// tool that inspects the workspace without mutating it — file/content
// reads, searches, and read-only git/LSP/listing operations. Forcing a task
// or spending skill budget before "what's in this file?" turns simple
// exploration into mandatory ceremony, which is why both task-enforcement
// and skill-budget-enforcement exempt these.
func IsReadOnlyExplorationTool(name string) bool {
	switch NormalizeToolName(name) {
	case "read", "readfile",
		"grep", "glob", "search", "websearch", "webfetch", "codesearch",
		"gitlog", "gitstatus", "gitdiff", "gitshow", "gitblame",
		"lsp", "lspdefinition", "lspreferences", "lsphover",
		"ls", "list", "listdir":
		return true
	}
	return false
}

// IsReadOnlyRecallTool reports whether name is a read-only recall/lookup tool
// that queries durable indexes (prior conversations, findings, skills, the
// web) rather than the live workspace. These mutate nothing, so gating them
// behind task ceremony or phase separation buys no safety.
//
// This exists as a separate classifier from IsReadOnlyExplorationTool because
// these tools do not read the working tree at all — but the practical effect
// for every hook that consumes them is identical, and omitting them was the
// direct cause of read-only HistorySearch being refused during planning
// (issue #267).
func IsReadOnlyRecallTool(name string) bool {
	switch NormalizeToolName(name) {
	case "historysearch", "historyget",
		"findingsquery",
		"websearch", "webfetch", "xaiwebsearch", "xsearch",
		"subagentoutput", "taskoutput", "delegateoutput",
		"vaultlist":
		return true
	}
	return false
}

// IsReadOnlyResearchTool is the union of workspace exploration and durable
// recall. Hooks that mean "this call cannot change anything" should prefer
// this over calling the two narrower classifiers separately.
func IsReadOnlyResearchTool(name string) bool {
	return IsReadOnlyExplorationTool(name) || IsReadOnlyRecallTool(name)
}

// IsBashTool reports whether name is a Bash/shell execution tool.
func IsBashTool(name string) bool {
	switch NormalizeToolName(name) {
	case "bash", "shell", "execute":
		return true
	}
	return false
}

type shellHeredoc struct {
	word      string
	stripTabs bool
}

// ExtractShellCommandWords returns the executable word from each top-level
// simple command in command. Separators inside quotes, command substitutions,
// backticks, and heredoc bodies are data rather than top-level commands and are
// ignored. Returned words are path-base names ("/usr/bin/grep" becomes "grep").
//
// This is intentionally a pragmatic shell tokenizer, not a POSIX parser. If
// quoting, substitution, or a heredoc delimiter cannot be parsed safely, it
// returns an empty slice rather than inventing command words.
func ExtractShellCommandWords(command string) []string {
	commands, ok := parseShellCommands(command)
	if !ok {
		return nil
	}

	words := make([]string, 0, len(commands))
	for _, fields := range commands {
		if i := shellExecutableIndex(fields); i >= 0 {
			words = append(words, shellBase(fields[i]))
		}
	}
	return words
}

// parseShellCommands splits top-level shell commands while retaining arguments
// for classifiers that need to inspect a command's subcommand.
func parseShellCommands(command string) ([][]string, bool) {
	var (
		commands        [][]string
		fields          []string
		word            strings.Builder
		pendingHeredocs []shellHeredoc
	)

	flushWord := func() {
		if word.Len() == 0 {
			return
		}
		fields = append(fields, word.String())
		word.Reset()
	}
	flushCommand := func() {
		flushWord()
		if len(fields) > 0 {
			commands = append(commands, fields)
			fields = nil
		}
	}

	for i := 0; i < len(command); i++ {
		switch command[i] {
		case '\\':
			if i+1 >= len(command) {
				return nil, false
			}
			i++
			if command[i] != '\n' {
				word.WriteByte(command[i])
			}
		case '\'':
			end := strings.IndexByte(command[i+1:], '\'')
			if end < 0 {
				return nil, false
			}
			end += i + 1
			word.WriteString(command[i+1 : end])
			i = end
		case '"':
			end, value, ok := scanDoubleQuoted(command, i)
			if !ok {
				return nil, false
			}
			word.WriteString(value)
			i = end
		case '`':
			end, ok := skipBackticks(command, i)
			if !ok {
				return nil, false
			}
			word.WriteByte('x') // Preserve that the substitution occupies a word.
			i = end
		case '$':
			if i+1 < len(command) && command[i+1] == '(' {
				end, ok := skipDollarSubstitution(command, i)
				if !ok {
					return nil, false
				}
				word.WriteByte('x')
				i = end
				continue
			}
			word.WriteByte(command[i])
		case '<':
			if i+1 < len(command) && command[i+1] == '<' {
				if i+2 < len(command) && command[i+2] == '<' {
					word.WriteString("<<<")
					i += 2
					continue
				}
				flushWord()
				heredoc, next, ok := parseShellHeredoc(command, i)
				if !ok {
					return nil, false
				}
				pendingHeredocs = append(pendingHeredocs, heredoc)
				i = next - 1
				continue
			}
			word.WriteByte(command[i])
		case ' ', '\t', '\r':
			flushWord()
		case '\n':
			flushCommand()
			if len(pendingHeredocs) > 0 {
				next, ok := skipShellHeredocBodies(command, i+1, pendingHeredocs)
				if !ok {
					return nil, false
				}
				pendingHeredocs = nil
				i = next - 1
			}
		case ';', '|', '&':
			flushCommand()
			if i+1 < len(command) && command[i+1] == command[i] {
				i++
			}
		default:
			word.WriteByte(command[i])
		}
	}
	flushCommand()
	return commands, true
}

func scanDoubleQuoted(command string, start int) (int, string, bool) {
	var value strings.Builder
	for i := start + 1; i < len(command); i++ {
		switch command[i] {
		case '"':
			return i, value.String(), true
		case '\\':
			if i+1 >= len(command) {
				return 0, "", false
			}
			i++
			value.WriteByte(command[i])
		case '`':
			end, ok := skipBackticks(command, i)
			if !ok {
				return 0, "", false
			}
			value.WriteByte('x')
			i = end
		case '$':
			if i+1 < len(command) && command[i+1] == '(' {
				end, ok := skipDollarSubstitution(command, i)
				if !ok {
					return 0, "", false
				}
				value.WriteByte('x')
				i = end
				continue
			}
			value.WriteByte(command[i])
		default:
			value.WriteByte(command[i])
		}
	}
	return 0, "", false
}

func skipBackticks(command string, start int) (int, bool) {
	for i := start + 1; i < len(command); i++ {
		if command[i] == '\\' {
			i++
			continue
		}
		if command[i] == '`' {
			return i, true
		}
	}
	return 0, false
}

func skipDollarSubstitution(command string, start int) (int, bool) {
	depth := 1
	for i := start + 2; i < len(command); i++ {
		switch command[i] {
		case '\\':
			i++
		case '\'':
			end := strings.IndexByte(command[i+1:], '\'')
			if end < 0 {
				return 0, false
			}
			i += end + 1
		case '"':
			end, _, ok := scanDoubleQuoted(command, i)
			if !ok {
				return 0, false
			}
			i = end
		case '`':
			end, ok := skipBackticks(command, i)
			if !ok {
				return 0, false
			}
			i = end
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

func parseShellHeredoc(command string, start int) (shellHeredoc, int, bool) {
	i := start + 2
	var heredoc shellHeredoc
	if i < len(command) && command[i] == '-' {
		heredoc.stripTabs = true
		i++
	}
	for i < len(command) && (command[i] == ' ' || command[i] == '\t') {
		i++
	}
	if i < len(command) && command[i] == '\\' {
		i++
	}
	if i >= len(command) {
		return shellHeredoc{}, 0, false
	}

	if command[i] == '\'' || command[i] == '"' {
		quote := command[i]
		i++
		startWord := i
		for i < len(command) && command[i] != quote {
			i++
		}
		if i >= len(command) || i == startWord {
			return shellHeredoc{}, 0, false
		}
		heredoc.word = command[startWord:i]
		return heredoc, i + 1, true
	}

	startWord := i
	for i < len(command) && !strings.ContainsRune(" \t\r\n;|&<>", rune(command[i])) {
		i++
	}
	if i == startWord {
		return shellHeredoc{}, 0, false
	}
	heredoc.word = command[startWord:i]
	return heredoc, i, true
}

func skipShellHeredocBodies(command string, start int, heredocs []shellHeredoc) (int, bool) {
	pos := start
	for _, heredoc := range heredocs {
		found := false
		for pos <= len(command) {
			end := strings.IndexByte(command[pos:], '\n')
			if end < 0 {
				end = len(command)
			} else {
				end += pos
			}
			line := strings.TrimRight(command[pos:end], " \t\r")
			if heredoc.stripTabs {
				line = strings.TrimLeft(line, "\t")
			}
			if line == heredoc.word {
				found = true
				if end < len(command) {
					pos = end + 1
				} else {
					pos = end
				}
				break
			}
			if end == len(command) {
				break
			}
			pos = end + 1
		}
		if !found {
			return 0, false
		}
	}
	return pos, true
}

func shellExecutableIndex(fields []string) int {
	for i, field := range fields {
		if !isShellAssignment(field) {
			return i
		}
	}
	return -1
}

func isShellAssignment(field string) bool {
	eq := strings.IndexByte(field, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		c := field[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '_' && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func shellBase(word string) string {
	if slash := strings.LastIndexByte(word, '/'); slash >= 0 {
		return word[slash+1:]
	}
	return word
}

// IsBashReadOnly reports whether a Bash/shell command is read-only (no
// mutation). Errs on the side of caution — only recognizes well-known read
// commands plus read-only git/go subcommands; anything else is treated as
// potentially mutating.
func IsBashReadOnly(cmd string) bool {
	commands, ok := parseShellCommands(cmd)
	if !ok || len(commands) == 0 {
		return false
	}

	// NOTE: "go" and "git" are deliberately absent from readOnlyBinaries —
	// they are NOT uniformly read-only (go build/git commit mutate), so
	// they must fall through to the subcommand-specific checks below. A
	// prior version of this logic listed them here with an explicit
	// `false` value, which made the map lookup below "hit" and return
	// early with `false` before ever reaching those subcommand checks —
	// silently defeating the git/go read-only allowlist entirely (e.g.
	// `git status` and `go list` were never actually recognized as
	// read-only). Keep them out of this map.
	readOnlyBinaries := map[string]bool{
		"ls": true, "cat": true, "head": true, "tail": true,
		"find": true, "grep": true, "rg": true, "ag": true,
		"wc": true, "file": true, "stat": true, "which": true,
		"tree": true, "du": true, "df": true, "pwd": true,
		"echo": true, "printf": true, "date": true,
		"cd": true, "pushd": true, "popd": true,
	}

	for _, fields := range commands {
		executable := shellExecutableIndex(fields)
		if executable < 0 {
			return false
		}
		binary := shellBase(fields[executable])
		if readOnlyBinaries[binary] {
			continue
		}

		args := fields[executable+1:]
		if binary == "git" {
			if len(args) == 0 {
				return false
			}
			switch args[0] {
			case "status", "log", "diff", "show", "blame", "branch", "tag",
				"remote", "ls-files", "ls-tree", "rev-parse", "describe", "shortlog":
				continue
			case "stash":
				if len(args) > 1 && args[1] == "list" {
					continue
				}
			}
			return false
		}

		if binary == "go" {
			if len(args) > 0 {
				switch args[0] {
				case "doc", "list", "version", "env", "vet":
					continue
				}
			}
			return false
		}

		return false
	}
	return true
}

// mutatingBinaries are executables whose ordinary use changes durable state:
// the filesystem, installed packages, running services, or a remote. This is
// the blocklist half of the shell gate.
var mutatingBinaries = map[string]bool{
	// Filesystem destruction / mutation
	"rm": true, "rmdir": true, "mv": true, "cp": true, "dd": true,
	"truncate": true, "shred": true, "chmod": true, "chown": true,
	"chgrp": true, "ln": true, "mkfs": true, "mount": true, "umount": true,
	// Package and system managers
	"apt": true, "apt-get": true, "yum": true, "dnf": true, "pacman": true,
	"brew": true, "npm": true, "pnpm": true, "yarn": true, "pip": true,
	"pip3": true, "gem": true, "cargo": true, "go-install": true,
	"systemctl": true, "service": true, "launchctl": true,
	// Container / infra mutation
	"docker": true, "podman": true, "kubectl": true, "helm": true,
	"terraform": true, "ansible": true,
	// Process control
	"kill": true, "pkill": true, "killall": true, "reboot": true,
	"shutdown": true, "halt": true,
}

// mutatingGitSubcommands are git subcommands that write to the working tree,
// the index, refs, or a remote.
var mutatingGitSubcommands = map[string]bool{
	"commit": true, "push": true, "merge": true, "rebase": true,
	"reset": true, "revert": true, "checkout": true, "switch": true,
	"restore": true, "clean": true, "rm": true, "mv": true, "add": true,
	"apply": true, "am": true, "cherry-pick": true, "filter-branch": true,
	"gc": true, "prune": true, "worktree": true, "submodule": true,
	"init": true, "clone": true, "fetch": true, "pull": true, "tag": true,
}

// IsBashClearlyMutating reports whether cmd contains a command that plainly
// changes durable state. It is the deliberate inverse of IsBashReadOnly:
//
//	IsBashReadOnly       — "prove this is safe", unknown ⇒ NOT read-only
//	IsBashClearlyMutating — "prove this is dangerous", unknown ⇒ NOT mutating
//
// Both classifiers exist because they answer different questions for
// different callers. A gate that must not leak side effects (task
// enforcement in ACT mode) needs proof of safety. A gate that only enforces
// phase discipline (plan mode) needs proof of harm — demanding proof of
// safety there made planning unusable, because every command the classifier
// merely did not recognize (`python3 x.py`, `jq . f`, `go build`, anything
// with a pipe or subshell it declined to parse) was refused as if it were
// destructive. See issues #277 and #294.
//
// Shell redirection that writes to a file (`>`, `>>`) counts as mutating
// regardless of the binary, since `echo x > src.go` is a source edit.
func IsBashClearlyMutating(cmd string) bool {
	if containsWriteRedirection(cmd) {
		return true
	}
	commands, ok := parseShellCommands(cmd)
	if !ok {
		// Unparseable input is NOT assumed hostile here. This classifier's
		// contract is "clearly mutating"; an unparseable command is by
		// definition not clear. Callers needing fail-closed behavior must
		// use IsBashReadOnly instead.
		return false
	}
	for _, fields := range commands {
		executable := shellExecutableIndex(fields)
		if executable < 0 {
			continue
		}
		binary := shellBase(fields[executable])
		if mutatingBinaries[binary] {
			return true
		}
		args := fields[executable+1:]
		if binary == "git" && len(args) > 0 && mutatingGitSubcommands[args[0]] {
			return true
		}
		// `sed -i` / `perl -i` edit files in place; without -i they stream.
		if binary == "sed" || binary == "perl" {
			for _, a := range args {
				if a == "-i" || strings.HasPrefix(a, "-i.") || strings.HasPrefix(a, "--in-place") {
					return true
				}
			}
		}
		// `tee` writes its operands.
		if binary == "tee" && len(args) > 0 {
			return true
		}
	}
	return false
}

// containsWriteRedirection reports whether cmd contains a `>` or `>>`
// redirection outside quotes. Redirections to /dev/null and to standard
// stream duplications (`2>&1`) do not write a file and are ignored.
func containsWriteRedirection(cmd string) bool {
	var quote byte
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' {
				i++
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '\\':
			i++
		case '>':
			// Skip a duplication like 2>&1 or >&2 — no file is written.
			j := i + 1
			if j < len(cmd) && cmd[j] == '>' {
				j++
			}
			for j < len(cmd) && (cmd[j] == ' ' || cmd[j] == '\t') {
				j++
			}
			if j < len(cmd) && cmd[j] == '&' {
				continue
			}
			rest := strings.TrimSpace(cmd[j:])
			if strings.HasPrefix(rest, "/dev/null") {
				continue
			}
			return true
		}
	}
	return false
}
