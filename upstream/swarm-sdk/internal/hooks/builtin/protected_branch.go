package builtin

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/gitprotect"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// ProtectedBranchPriority runs just below git-user-enforcement (96) and above
// task enforcement (95), so a protected-branch block is surfaced early but
// after steering.
const ProtectedBranchPriority = 94

// DefaultProtectedBranches are the branches guarded when a repo enables
// protection without naming specific branches.
var DefaultProtectedBranches = gitprotect.DefaultProtectedBranches

// ProtectedBranchHook blocks mutating tool calls (file writes/edits and
// mutating shell/git commands) while the workspace's git repo is checked out
// on a protected branch (e.g. main). It steers the agent to create/use a git
// worktree and merge consciously instead of editing the protected branch
// directly.
//
// Design goals (harness behavior):
//   - Hard block, no grace period: mutations on a protected branch are refused.
//   - Read-only tools are always allowed (read/grep/glob/status/log/diff...).
//   - Escape operations are always allowed so the agent is never trapped:
//     git checkout/switch/worktree/branch/stash/fetch/clone.
//   - Fail-open: any git/detection/config error results in Continue(), never a
//     spurious block.
//
// Protection is opt-in. Sources, highest priority first:
//  1. <repoRoot>/config.swarm  ("protected" + "protectedBranches")
//  2. the hook's constructor default (enabledByDefault + DefaultProtectedBranches)
type ProtectedBranchHook struct {
	enabledByDefault bool
	defaultBranches  []string

	// gitCommand runs git; overrideable for tests.
	gitCommand func(args ...string) (string, error)

	// loadConfig resolves protection config for a repo root; overrideable for tests.
	loadConfig func(repoRoot string) (gitprotect.Config, bool)

	mu sync.Mutex
}

// NewProtectedBranchHook creates a config-driven hook. Protection is inert
// unless <repoRoot>/config.swarm sets "protected": true. This is the
// constructor the TUI registers, so merely having the hook present does not
// block anyone until the user runs /protect.
func NewProtectedBranchHook() *ProtectedBranchHook {
	return newProtectedBranchHook(false)
}

// NewProtectedBranchHookEnabled creates a hook that protects the default
// branches even without a config file. The config file, when present, still
// wins (it can disable protection or override the branch list). This is the
// constructor the SDK's WithProtectedBranchGuard() option uses so that
// enabling the guard actively protects main/master out of the box.
func NewProtectedBranchHookEnabled() *ProtectedBranchHook {
	return newProtectedBranchHook(true)
}

func newProtectedBranchHook(enabledByDefault bool) *ProtectedBranchHook {
	branches := make([]string, len(DefaultProtectedBranches))
	copy(branches, DefaultProtectedBranches)
	h := &ProtectedBranchHook{
		enabledByDefault: enabledByDefault,
		defaultBranches:  branches,
		gitCommand:       runGit,
	}
	h.loadConfig = loadProtectedConfig
	return h
}

// Name returns the hook identifier.
func (h *ProtectedBranchHook) Name() string { return "protected-branch" }

// Priority returns the hook execution priority.
func (h *ProtectedBranchHook) Priority() int { return ProtectedBranchPriority }

// Filter returns true for tool-before-execute events.
func (h *ProtectedBranchHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventToolBeforeExecute
}

// SetGitCommand overrides the git runner (used for testing).
func (h *ProtectedBranchHook) SetGitCommand(cmd func(args ...string) (string, error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.gitCommand = cmd
}

// SetConfigLoader overrides the config loader (used for testing).
func (h *ProtectedBranchHook) SetConfigLoader(fn func(repoRoot string) (gitprotect.Config, bool)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.loadConfig = fn
}

// OnEvent is the enforcement logic.
//
// Flow:
//  1. If the tool is read-only, allow (never block reads).
//  2. Detect current branch; on any git error, fail-open (allow).
//  3. Resolve protection config; if protection is off, allow.
//  4. If a Bash command explicitly pushes a protected destination ref, block
//     regardless of the current worktree branch.
//  5. Resolve the EFFECTIVE branch: the branch checked out at the tool's target
//     dir (bash `cwd` / file `file_path`) when it names a different worktree,
//     otherwise the process-cwd branch. This makes the decision follow what the
//     tool operates on rather than where Swarm was launched.
//  6. If the effective branch is empty or not protected, allow.
//     6b. If the tool is a mutating file tool, block.
//  7. If the tool is Bash: block recognized mutations before considering
//     escape/read-only commands, so a safe command cannot mask a later mutation.
//  8. Otherwise allow (unknown tools are not mutations we recognize).
func (h *ProtectedBranchHook) OnEvent(_ context.Context, event hooks.Event) (hooks.HookResult, error) {
	h.mu.Lock()
	gitCommand := h.gitCommand
	loadConfig := h.loadConfig
	enabledByDefault := h.enabledByDefault
	defaultBranches := h.defaultBranches
	h.mu.Unlock()

	toolName := eventToolName(event)
	if toolName == "" {
		return hooks.Continue(), nil
	}

	// (1) Read-only tools are always safe.
	if isReadOnlyTool(toolName) {
		return hooks.Continue(), nil
	}

	// (2) Detect the process-cwd branch. Fail-open only on a genuine git error
	// (git unavailable / not a repo). An empty result (detached HEAD) is handled
	// after we know whether the tool actually targets a different worktree.
	branchOut, err := gitCommand("branch", "--show-current")
	if err != nil {
		return hooks.Continue(), nil
	}
	branch := strings.TrimSpace(branchOut)

	// (3) Resolve protection config against the repo that owns the process cwd.
	// Linked worktrees share this config.swarm.
	repoRoot := h.repoRoot(gitCommand)
	enabled := enabledByDefault
	branches := defaultBranches
	if repoRoot != "" {
		if cfg, found := loadConfig(repoRoot); found {
			enabled = cfg.Protected
			if len(cfg.ProtectedBranches) > 0 {
				branches = cfg.ProtectedBranches
			}
		}
	}
	if !enabled {
		return hooks.Continue(), nil
	}

	// (4) A protected push destination remains protected even when the command
	// runs from a feature worktree. Worktrees share refs, so checking only the
	// current branch would allow `git push origin main` from another worktree.
	bashCommand := ""
	if isBashTool(toolName) {
		bashCommand = eventBashCommand(event)
		if target := pushedProtectedBranch(bashCommand, branches); target != "" {
			result := hooks.Block(h.protectedRefBlockMessage(branch, target, bashCommand))
			result.Metadata = map[string]any{
				"reason":             "protected_ref_update",
				"project_root":       repoRoot,
				"execution_root":     effectiveTargetDir(event, repoRoot),
				"current_branch":     branch,
				"target_branch":      target,
				"protected_branches": append([]string(nil), branches...),
			}
			return result, nil
		}
	}

	// (5) Resolve the branch the tool ACTUALLY operates on. The process-cwd
	// branch is only a fallback: a bash call may set `cwd`, and a file tool
	// carries a `file_path`, either of which can point at a different worktree.
	// Keying the decision off the target branch (not the launch dir) is what
	// removes the intermittency where the same mutation was blocked or allowed
	// depending only on where Swarm happened to be started. It closes the
	// bypass where the process sits on a feature branch while the tool mutates a
	// protected checkout, and it preserves worktree-compliance (a tool targeting
	// a non-protected worktree from a protected process cwd stays allowed).
	effectiveBranch := branch
	if targetDir := resolveTargetDir(event, repoRoot); targetDir != "" && targetDir != repoRoot {
		if wtBranch, wtErr := branchAtPath(gitCommand, targetDir); wtErr == nil {
			// Confirmed target branch (protected or not); it wins over the cwd.
			effectiveBranch = wtBranch
		}
		// On a git error we cannot confirm the target branch, so we keep the
		// process-cwd branch as a conservative fallback rather than fail-open
		// purely because a `git -C` lookup failed. The agent is never trapped
		// because escape/read-only commands are always allowed (step 7).
	}

	// (6) Nothing to protect if the effective branch is empty (detached HEAD /
	// unknown) or simply not in the protected set.
	if effectiveBranch == "" || !branchInList(effectiveBranch, branches) {
		return hooks.Continue(), nil
	}

	// (6b) Mutating file tools are always blocked on a protected branch.
	if isMutatingFileTool(toolName) {
		return h.workspaceHandoffBlock(effectiveBranch, branches, repoRoot, toolName, "", event), nil
	}

	// (7) Bash: inspect the command. Mutation takes precedence across the
	// entire compound command: `git status && git merge ...` must not be
	// exempted merely because it contains an allowed read-only operation.
	if isBashTool(toolName) {
		if bashCommand == "" {
			return hooks.Continue(), nil
		}
		if isMutatingCommand(bashCommand) {
			return h.workspaceHandoffBlock(effectiveBranch, branches, repoRoot, toolName, bashCommand, event), nil
		}
		// Escape/navigation/read-only shell commands are allowed only after the
		// full command has been checked for recognized mutations.
		if isEscapeOrReadOnlyCommand(bashCommand) {
			return hooks.Continue(), nil
		}
		// Unknown shell command on a protected branch: fail-open (allow) to
		// avoid trapping the agent on legitimate read commands we don't model.
		return hooks.Continue(), nil
	}

	// (8) Any other tool: not a recognized mutation.
	return hooks.Continue(), nil
}

// repoRoot resolves the git worktree root via the hook's git runner.
func (h *ProtectedBranchHook) repoRoot(gitCommand func(args ...string) (string, error)) string {
	out, err := gitCommand("rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// blockMessage returns an actionable, agent-facing hard-block message that steers
// the agent to create a git worktree and target it for subsequent operations.
func (h *ProtectedBranchHook) blockMessage(branch, toolName, command string, event hooks.Event) string {
	suggestedBranch := suggestBranchName(event)
	worktreeDir := filepath.Join(".worktrees", suggestedBranch)
	var what string
	switch {
	case command != "":
		what = fmt.Sprintf("the shell command %q", truncateForMessage(command, 120))
	default:
		what = fmt.Sprintf("the %q tool", toolName)
	}

	// Detect whether the tool is a file tool or bash tool so the steering
	// message tells the agent exactly which parameter to redirect.
	var paramHint string
	if isBashTool(toolName) {
		paramHint = fmt.Sprintf("Re-run your command with cwd: %q", worktreeDir)
	} else {
		paramHint = fmt.Sprintf("Re-run with file_path pointing inside %q (e.g. %s/<your-file>)", worktreeDir, worktreeDir)
	}

	return fmt.Sprintf(
		"[PROTECTED BRANCH — BLOCKED] The workspace is on protected branch '%s', so %s was refused. "+
			"Direct edits to '%s' are not allowed here. Move your work onto a git worktree and merge consciously.\n\n"+
			"To get unblocked:\n"+
			"  1. Create a worktree on a new branch:\n"+
			"       git worktree add -b %s %s\n"+
			"  2. %s, where edits are allowed.\n"+
			"  3. When done, review and merge back deliberately:\n"+
			"       git checkout %s && git merge --no-ff %s\n\n"+
			"Reads, searches, git status/log/diff, and git checkout/switch/worktree/branch remain available on '%s'.",
		branch, what, branch, suggestedBranch, worktreeDir, paramHint, branch, suggestedBranch, branch,
	)
}

func (h *ProtectedBranchHook) protectedRefBlockMessage(currentBranch, targetBranch, command string) string {
	return fmt.Sprintf(
		"[PROTECTED BRANCH — BLOCKED] The shell command %q was refused because it targets protected branch '%s', even though the current worktree is on '%s'. "+
			"Run /workspace to select an editable feature worktree, then use the repository's sanctioned review or approval flow to update '%s'. "+
			"Do not substitute an equivalent Git command or push the protected ref from another worktree.",
		truncateForMessage(command, 120), targetBranch, currentBranch, targetBranch,
	)
}

// suggestBranchName derives a branch name from the event context, falling back to
// a simple "feature-work" default. A good suggestion makes the steering message
// actionable so the agent can copy-paste the git worktree add command.
func suggestBranchName(event hooks.Event) string {
	// Best-effort: no conversation context available in the hook, so use a
	// stable default. The agent is free to choose its own name.
	return "feature-work"
}

// ---- helpers -------------------------------------------------------------

// loadProtectedConfig reads <repoRoot>/config.swarm via the SDK's gitprotect
// package. The bool result reports whether the file was found and parsed; a
// missing or malformed file is (zero, false) so callers fall back to
// constructor defaults.
func loadProtectedConfig(repoRoot string) (gitprotect.Config, bool) {
	cfg, found, err := gitprotect.Load(repoRoot)
	if err != nil {
		return gitprotect.Config{}, false
	}
	return cfg, found
}

// eventToolName extracts the tool name from event data, tolerating both the
// "tool_name" and "name" keys used across managers.
func eventToolName(event hooks.Event) string {
	if tn, ok := event.Data["tool_name"].(string); ok && tn != "" {
		return tn
	}
	if tn, ok := event.Data["name"].(string); ok {
		return tn
	}
	return ""
}

// eventBashCommand extracts the bash command string from event params,
// tolerating both the "params" and "tool_input" keys.
func eventBashCommand(event hooks.Event) string {
	params, _ := event.Data["params"].(map[string]any)
	if params == nil {
		params, _ = event.Data["tool_input"].(map[string]any)
	}
	if params == nil {
		return ""
	}
	command, _ := params["command"].(string)
	return command
}

// branchInList reports whether branch equals any entry in list (case-sensitive
// match after trimming; branch names are case-sensitive in git).
func branchInList(branch string, list []string) bool {
	for _, b := range list {
		if strings.TrimSpace(b) == branch {
			return true
		}
	}
	return false
}

// isMutatingFileTool reports whether a tool writes/edits files in the workspace.
func isMutatingFileTool(toolName string) bool {
	lower := strings.ToLower(strings.ReplaceAll(toolName, "_", ""))
	switch lower {
	case "write", "writefile", "filewrite",
		"edit", "editfile", "fileedit", "multiedit", "multieditfile",
		"strreplace", "strreplaceeditor",
		"applypatch", "patch", "apply":
		return true
	}
	return false
}

// escapeCommandPattern matches git subcommands that let the agent get off the
// protected branch (or are inherently read-only). These are always allowed.
var escapeCommandPattern = regexp.MustCompile(
	`\bgit\s+(checkout|switch|worktree|branch|stash|fetch|clone|status|log|diff|show|rev-parse|remote|config|blame|describe|ls-files|ls-tree|for-each-ref|symbolic-ref)\b`,
)

// mutatingGitPattern matches git subcommands that write to the repo/branch.
var mutatingGitPattern = regexp.MustCompile(
	`\bgit\s+(commit|merge|pull|rebase|push|reset|cherry-pick|revert|am|apply|restore|rm|mv|clean|tag|update-ref|gc|filter-branch)\b`,
)

// mutatingShellPattern matches common file-mutation shell operations. Redirects
// (> and >>) and in-place editors are the frequent ways to bypass file tools.
var mutatingShellPattern = regexp.MustCompile(
	`(^|[\s;&|])(rm|mv|cp|dd|truncate|tee|install|mkdir|rmdir|touch|chmod|chown|ln)\s|` + // file ops
		`\bsed\s+-i|\bperl\s+-i|` + // in-place edits
		`>>?[^|&]`, // output redirection into a file
)

// isEscapeOrReadOnlyCommand reports whether a shell command is an allowed
// escape/navigation/read-only operation. Escape git commands win even if the
// command also contains a mutating-looking token elsewhere.
func isEscapeOrReadOnlyCommand(command string) bool {
	return escapeCommandPattern.MatchString(command)
}

// shellCommandSeparatorPattern separates the common compound-command forms
// needed for protected-ref inspection. Mutation precedence itself still applies
// to the full raw command via isMutatingCommand.
var shellCommandSeparatorPattern = regexp.MustCompile(`(?:&&|\|\||[;\n])`)

// pushedProtectedBranch returns the protected destination branch explicitly
// named by a git-push refspec. It intentionally inspects the destination rather
// than the current worktree: all worktrees share the same underlying refs.
func pushedProtectedBranch(command string, protectedBranches []string) string {
	for _, segment := range shellCommandSeparatorPattern.Split(command, -1) {
		fields := strings.Fields(strings.TrimSpace(segment))
		if len(fields) < 4 || fields[0] != "git" || fields[1] != "push" {
			continue
		}

		// The first non-option argument is the remote; later positional
		// arguments are refspecs. This covers the explicit ref forms used by
		// agents while leaving implicit-upstream pushes unchanged.
		remoteSeen := false
		for _, arg := range fields[2:] {
			if strings.HasPrefix(arg, "-") {
				continue
			}
			if !remoteSeen {
				remoteSeen = true
				continue
			}

			destination := strings.Trim(strings.TrimPrefix(arg, "+"), `"'`)
			if _, after, ok := strings.Cut(destination, ":"); ok {
				destination = after
			}
			destination = strings.TrimPrefix(destination, "refs/heads/")
			if branchInList(destination, protectedBranches) {
				return destination
			}
		}
	}
	return ""
}

// isMutatingCommand reports whether a shell command mutates the repo/files.
func isMutatingCommand(command string) bool {
	return mutatingGitPattern.MatchString(command) || mutatingShellPattern.MatchString(command)
}

// truncateForMessage shortens a string for inclusion in a block message.
func truncateForMessage(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// ---- worktree-awareness helpers -----------------------------------------

// eventBashCwd extracts the bash tool's cwd parameter from event params,
// tolerating both the "params" and "tool_input" keys used across managers.
func eventBashCwd(event hooks.Event) string {
	params, _ := event.Data["params"].(map[string]any)
	if params == nil {
		params, _ = event.Data["tool_input"].(map[string]any)
	}
	if params == nil {
		return ""
	}
	cwd, _ := params["cwd"].(string)
	return cwd
}

// eventFilePath extracts the file path parameter from event params for file
// tools, tolerating both the "params"/"tool_input" keys and multiple param
// names (file_path, path, filename) used by different tool implementations.
func eventFilePath(event hooks.Event) string {
	params, _ := event.Data["params"].(map[string]any)
	if params == nil {
		params, _ = event.Data["tool_input"].(map[string]any)
	}
	if params == nil {
		return ""
	}
	for _, key := range []string{"file_path", "path", "filename"} {
		if v, ok := params[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// resolveTargetDir determines the effective working directory of a tool call:
// - bash tool: the cwd parameter if provided
// - file tools: the directory containing the file_path, if provided
// - otherwise: empty string (the tool operates in the process cwd / repo root)
// Relative paths are resolved against repoRoot.
func resolveTargetDir(event hooks.Event, repoRoot string) string {
	if cwd := eventBashCwd(event); cwd != "" {
		return resolveAgainst(cwd, repoRoot)
	}
	if fp := eventFilePath(event); fp != "" {
		dir := filepath.Dir(fp)
		return resolveAgainst(dir, repoRoot)
	}
	return ""
}

// resolveAgainst resolves a path (which may be relative) against repoRoot,
// returning an absolute path. If the input is already absolute it is returned
// as-is (after cleaning).
func resolveAgainst(p, repoRoot string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if repoRoot == "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return filepath.Clean(p)
		}
		return abs
	}
	return filepath.Clean(filepath.Join(repoRoot, p))
}

// branchAtPath runs `git -C <path> branch --show-current` to determine the
// checked-out branch at a specific filesystem path. Returns ("", error) on any
// git error (not a git repo, git unavailable, etc.) so callers can distinguish
// "can't determine" from "detached HEAD" (which returns ("", nil)).
func branchAtPath(gitCommand func(args ...string) (string, error), path string) (string, error) {
	out, err := gitCommand("-C", path, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
