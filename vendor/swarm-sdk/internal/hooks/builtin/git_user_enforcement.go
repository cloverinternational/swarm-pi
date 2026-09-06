package builtin

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// GracePeriod is the number of tool calls allowed before hard blocking.
const GracePeriod = 5

// GitUserEnforcementPriority runs after steering (100) but before task enforcement (95).
const GitUserEnforcementPriority = 96

// GitUserEnforcementHook enforces that only the expected git user can make
// tool calls on the main branch. Others get a grace period of GracePeriod tool
// calls with a warning, then everything is blocked.
//
// The expected user is captured at hook creation time (from git config user.email).
// If the current branch is main and the current git user differs from the expected
// user, the agent is warned to use a different branch. After the grace period is
// exhausted, all tool calls are blocked.
type GitUserEnforcementHook struct {
	expectedGitUser string // git user.email captured at creation
	targetRepo      string // e.g. "Swarm-Code/mono"
	counter         int    // tool calls in violation
	mu              sync.Mutex

	// gitCommand is the command to run git; overrideable for tests
	gitCommand func(args ...string) (string, error)
}

// NewGitUserEnforcementHook creates a hook that auto-detects the expected git user
// from the current git config user.email and the target repo from the origin remote.
func NewGitUserEnforcementHook() *GitUserEnforcementHook {
	return NewGitUserEnforcementHookWithUser(detectGitUser())
}

// NewGitUserEnforcementHookWithUser creates a hook with a specific expected git user.
func NewGitUserEnforcementHookWithUser(expectedGitUser string) *GitUserEnforcementHook {
	return &GitUserEnforcementHook{
		expectedGitUser: expectedGitUser,
		targetRepo:      detectRepo(),
		gitCommand:      runGit,
	}
}

// Name returns the hook identifier.
func (h *GitUserEnforcementHook) Name() string {
	return "git-user-enforcement"
}

// Priority returns the hook execution priority.
func (h *GitUserEnforcementHook) Priority() int {
	return GitUserEnforcementPriority
}

// Filter returns true for tool-before-execute events.
func (h *GitUserEnforcementHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventToolBeforeExecute
}

// OnEvent is the main enforcement logic.
//
// Flow:
//  1. Detect current git branch.
//  2. If not on main branch, allow everything.
//  3. Detect current git user.
//  4. If current user matches expected user, allow everything.
//  5. If mismatch: warn and allow for GracePeriod tool calls.
//  6. After GracePeriod: block all tool calls.
func (h *GitUserEnforcementHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	// Skip if no expected user was configured (can't enforce without a baseline)
	if h.expectedGitUser == "" {
		return hooks.Continue(), nil
	}

	// Skip if not in the target repo
	currentRepo, err := h.detectRepo()
	if err != nil {
		return hooks.Continue(), nil
	}
	if currentRepo != h.targetRepo {
		return hooks.Continue(), nil
	}

	// Check current branch
	branch, err := h.gitCommand("branch", "--show-current")
	if err != nil {
		// Not in a git repo or git not available — fail-open
		return hooks.Continue(), nil
	}
	branch = strings.TrimSpace(branch)
	if branch != "main" {
		return hooks.Continue(), nil
	}

	// Check current git user
	currentUser, err := h.gitCommand("config", "user.email")
	if err != nil {
		return hooks.Continue(), nil
	}
	currentUser = strings.TrimSpace(currentUser)

	// If current user matches expected user, allow
	if currentUser == h.expectedGitUser {
		return hooks.Continue(), nil
	}

	// If git user is empty, we can't enforce — fail-open
	if currentUser == "" {
		return hooks.Continue(), nil
	}

	// Mismatch — enforce grace period
	h.mu.Lock()
	defer h.mu.Unlock()

	// Check if the tool is exempt from counting (read-only / safe tools)
	toolName, _ := event.Data["tool_name"].(string)
	if isReadOnlyToolEvent(toolName, event) {
		// Read-only tools don't count against the grace period
		// but we still show a warning if the grace period hasn't started
		if h.counter == 0 {
			return hooks.ContinueWithMessage(h.warningMessage(currentUser, 0)), nil
		}
		return hooks.Continue(), nil
	}

	h.counter++
	if h.counter <= GracePeriod {
		return hooks.ContinueWithMessage(h.warningMessage(currentUser, h.counter)), nil
	}

	// Grace period exhausted — hard block
	return hooks.Block(h.blockMessage(currentUser)), nil
}

// detectRepo uses the hook's gitCommand to detect the current repo identifier.
func (h *GitUserEnforcementHook) detectRepo() (string, error) {
	out, err := h.gitCommand("remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	url := strings.TrimSpace(out)

	// Normalize SSH url: git@github.com:Swarm-Code/mono.git
	if strings.HasPrefix(url, "git@github.com:") {
		url = strings.TrimPrefix(url, "git@github.com:")
	} else if strings.HasPrefix(url, "git@github.com/") {
		url = strings.TrimPrefix(url, "git@github.com/")
	} else if strings.HasPrefix(url, "https://github.com/") {
		url = strings.TrimPrefix(url, "https://github.com/")
	}

	// Remove .git suffix and trailing slash
	url = strings.TrimSuffix(url, ".git")
	url = strings.TrimSuffix(url, "/")

	// Validate it looks like owner/repo
	parts := strings.Split(url, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return url, nil
	}

	return "", fmt.Errorf("unrecognized remote URL format: %s", url)
}

// SetGitCommand sets the git runner (used for testing).
func (h *GitUserEnforcementHook) SetGitCommand(cmd func(args ...string) (string, error)) {
	h.gitCommand = cmd
}

// GetExpectedUser returns the configured expected git user.
func (h *GitUserEnforcementHook) GetExpectedUser() string {
	return h.expectedGitUser
}

// GetTargetRepo returns the configured target repo identifier.
func (h *GitUserEnforcementHook) GetTargetRepo() string {
	return h.targetRepo
}

// GetCounter returns the current violation counter.
func (h *GitUserEnforcementHook) GetCounter() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.counter
}

// ResetCounter resets the violation counter.
func (h *GitUserEnforcementHook) ResetCounter() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.counter = 0
}

// isReadOnlyTool returns true for tools that are read-only and safe.
func isReadOnlyTool(toolName string) bool {
	lower := strings.ToLower(strings.ReplaceAll(toolName, "_", ""))
	readOnlyTools := map[string]bool{
		"read": true, "readfile": true,
		"grep": true, "glob": true, "search": true,
		"websearch": true, "webfetch": true,
		"gitlog": true, "gitstatus": true, "gitdiff": true,
		"gitshow": true, "gitblame": true, "gitbranch": true,
		"ls": true, "list": true, "listdir": true,
		"tasklist": true, "taskget": true,
		"todo":            true,
		"askuserquestion": true,
		"enterplanmode":   true, "exitplanmode": true,
	}
	return readOnlyTools[lower]
}

func isReadOnlyToolEvent(toolName string, event hooks.Event) bool {
	if isTaskManageTool(toolName) {
		return taskManageIsReadOnly(event)
	}
	return isReadOnlyTool(toolName)
}

// warningMessage returns the warning shown during the grace period.
func (h *GitUserEnforcementHook) warningMessage(currentUser string, count int) string {
	if count == 0 {
		return fmt.Sprintf(
			"[GIT USER ENFORCEMENT] You are on branch 'main' as git user '%s', but the expected user is '%s'. "+
				"Please switch to a different branch or set git user.email to the expected user.",
			currentUser, h.expectedGitUser,
		)
	}
	return fmt.Sprintf(
		"[GIT USER ENFORCEMENT — WARNING %d/%d] You are on branch 'main' as git user '%s', but the expected user is '%s'. "+
			"Please switch to a different branch or change git user. Tool call %d of %d before hard block.",
		count, GracePeriod, currentUser, h.expectedGitUser, count, GracePeriod,
	)
}

// blockMessage returns the hard-block message after grace period.
func (h *GitUserEnforcementHook) blockMessage(currentUser string) string {
	return fmt.Sprintf(
		"[GIT USER ENFORCEMENT — BLOCKED] You are on branch 'main' as git user '%s', but the expected user is '%s'. "+
			"You have exceeded the %d tool call grace period. All tools are now blocked. "+
			"Switch to a different branch or update git user.email to the expected user.",
		currentUser, h.expectedGitUser, GracePeriod,
	)
}

// runGit executes a git command and returns stdout.
func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// detectGitUser runs git config user.email and returns the value.
func detectGitUser() string {
	out, err := runGit("config", "user.email")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// detectRepo runs git remote get-url origin and returns the owner/repo identifier
// (e.g. "Swarm-Code/mono"). It normalizes HTTPS and SSH URLs.
func detectRepo() string {
	out, err := runGit("remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(out)

	// Normalize SSH url: git@github.com:Swarm-Code/mono.git
	if strings.HasPrefix(url, "git@github.com:") {
		url = strings.TrimPrefix(url, "git@github.com:")
	} else if strings.HasPrefix(url, "git@github.com/") {
		url = strings.TrimPrefix(url, "git@github.com/")
	} else if strings.HasPrefix(url, "https://github.com/") {
		url = strings.TrimPrefix(url, "https://github.com/")
	} else if strings.HasPrefix(url, "https://github.com:") {
		url = strings.TrimPrefix(url, "https://github.com:")
	}

	// Remove .git suffix
	url = strings.TrimSuffix(url, ".git")

	// Remove trailing slash
	url = strings.TrimSuffix(url, "/")

	// Validate it looks like owner/repo
	parts := strings.Split(url, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return url
	}

	return ""
}
