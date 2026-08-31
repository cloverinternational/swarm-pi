// Package plan implements Plan Mode for the Swarm SDK.
//
// Plan Mode is a structured workflow that separates exploration/design from
// implementation. When active, the agent is instructed to explore the codebase,
// design an approach, and present a plan for user approval before making any
// changes. This mirrors the Plan Mode architecture found in Claude Code v2.1.84.
//
// Architecture:
//
//	┌─────────────────────────────────────────────────────────────┐
//	│                     MODE HIERARCHY                          │
//	├─────────────────────────────────────────────────────────────┤
//	│                                                             │
//	│  DEFAULT ──▶ PLAN (design) ──▶ ACT (implement)             │
//	│                 │                    │                      │
//	│                 └──────────▶ AUTO ◀──┘                     │
//	│                                                             │
//	└─────────────────────────────────────────────────────────────┘
//
// Flow:
//  1. Agent calls enter_plan_mode → PlanBroker.EnterPlanMode() notifies TUI
//  2. TUI shows PLAN badge; ordinary tool authorization remains unchanged
//  3. Agent explores and writes a workspace-local text/Markdown plan
//  4. Agent calls exit_plan_mode with plan_file (or legacy inline content)
//  5. SDK copies the content to conversation storage and requests approval
//  6. On approval: optional context compaction, transition to ACT mode
//  7. On rejection: stay in PLAN mode, agent revises based on feedback
package plan

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// ─── State ───────────────────────────────────────────────────────────────────

// State represents the current plan mode lifecycle state.
type State string

const (
	// StateIdle means plan mode is not active.
	StateIdle State = "idle"
	// StateActive means the agent is in plan mode (exploring/designing).
	StateActive State = "active"
	// StateAwaitingApproval means the agent has submitted a plan for user review.
	StateAwaitingApproval State = "awaiting_approval"
)

// ─── Config ──────────────────────────────────────────────────────────────────

// Config holds plan mode configuration.
type Config struct {
	// Enabled controls whether the enter_plan_mode and exit_plan_mode tools
	// are registered for the agent.
	Enabled bool

	// PlanFileName is the base name of the canonical conversation copy.
	// Defaults to "plan.md".
	PlanFileName string

	// AutoClearContext controls whether context is compacted after plan approval
	// without prompting the user. When false (default), the user is asked.
	AutoClearContext bool

	// SessionID is the stable TUI session identifier (set at session start,
	// never changed by compaction). When non-empty, the canonical plan copy is
	// stored at ~/.swarm/conversations/<SessionID>/plan.md.
	SessionID string

	// WorkDir is the authoritative workspace root used to resolve and validate
	// agent-submitted plan files. It is also the legacy canonical-plan location
	// when SessionID is empty. Defaults to the current working directory.
	WorkDir string
}

// MaxSubmittedPlanBytes bounds plan files read from the workspace. Plans are
// copied into conversation storage and provider context, so accepting an
// unbounded file would amplify memory and context usage.
const MaxSubmittedPlanBytes int64 = 1 << 20 // 1 MiB

// DefaultConfig returns sensible defaults for plan mode configuration.
func DefaultConfig() Config {
	return Config{
		Enabled:          true,
		PlanFileName:     "plan.md",
		AutoClearContext: false,
		SessionID:        "",
		WorkDir:          "",
	}
}

// planFilePath returns the canonical conversation-copy path. When SessionID is
// set, the copy lives under the session-scoped conversation directory so
// multiple agents in one workspace never share canonical state. It falls back
// to WorkDir for headless and legacy callers.
func (c *Config) planFilePath() string {
	name := c.PlanFileName
	if name == "" {
		name = "plan.md"
	}

	if c.SessionID != "" {
		return filepath.Join(paths.ConversationsDir(), c.SessionID, name)
	}

	return filepath.Join(c.workspaceDir(), name)
}

func (c *Config) workspaceDir() string {
	if c.WorkDir != "" {
		return c.WorkDir
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// ─── PlanBroker ──────────────────────────────────────────────────────────────

// ApprovalResponse is the result of a plan approval request.
type ApprovalResponse struct {
	// Approved is true if the user approved the plan.
	Approved bool
	// EditedPlan contains the user-edited plan (may differ from submitted plan).
	// If empty, the original plan was accepted as-is.
	EditedPlan string
	// ClearContext indicates the user wants context compacted before implementation.
	ClearContext bool
	// Feedback contains the user's rejection feedback (when Approved == false).
	Feedback string
	// RespondedAt is when the user responded.
	RespondedAt time.Time
}

// PlanBroker is the interface that the TUI (or any frontend) must implement
// to support Plan Mode interaction. The SDK plan tools call these methods to
// communicate mode transitions to the UI layer.
type PlanBroker interface {
	// EnterPlanMode notifies the UI that the agent has entered plan mode.
	// This is non-blocking from the tool's perspective – it sends a signal
	// and returns immediately.
	EnterPlanMode(ctx context.Context) error

	// RequestPlanApproval presents the plan to the user and blocks until
	// they approve, reject, or the context is cancelled.
	// Returns an ApprovalResponse describing the user's decision.
	RequestPlanApproval(ctx context.Context, plan string) (ApprovalResponse, error)
}

// ─── Plan File Helpers ────────────────────────────────────────────────────────

// ReadPlanFile reads the plan file from the configured path.
// Returns ("", nil) if the file doesn't exist.
func ReadPlanFile(cfg Config) (string, error) {
	path := cfg.planFilePath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// WritePlanFile writes the plan content to the configured path.
func WritePlanFile(cfg Config, content string) error {
	path := cfg.planFilePath()
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Atomic write (CreateTemp -> fsync -> rename) so a crashed/racing write can
	// never leave a truncated plan.md behind.
	return atomicfile.Write(path, []byte(content+"\n"), atomicfile.WithPerm(0o644))
}

// PlanFilePath returns the absolute path of the plan file.
func PlanFilePath(cfg Config) string {
	return cfg.planFilePath()
}

// ReadSubmittedPlanFile reads a workspace-local text file for submission to
// exit_plan_mode. Relative paths resolve from Config.WorkDir. Both the
// workspace and candidate are evaluated through symlinks before containment is
// checked, preventing a workspace symlink from escaping the allowed root.
func ReadSubmittedPlanFile(cfg Config, submittedPath string) (string, error) {
	submittedPath = strings.TrimSpace(submittedPath)
	if submittedPath == "" {
		return "", fmt.Errorf("plan file path is empty")
	}

	rootPath, err := filepath.Abs(cfg.workspaceDir())
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return "", fmt.Errorf("open workspace root %q: %w", rootPath, err)
	}
	defer root.Close()

	candidate := submittedPath
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Clean(candidate)
	} else {
		candidate, err = filepath.Abs(candidate)
		if err != nil {
			return "", fmt.Errorf("resolve plan file %q: %w", submittedPath, err)
		}
		candidate, err = filepath.Rel(rootPath, candidate)
		if err != nil {
			return "", fmt.Errorf("resolve plan file %q relative to workspace: %w", submittedPath, err)
		}
	}
	if filepath.IsAbs(candidate) || candidate == ".." ||
		strings.HasPrefix(candidate, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("plan file %q is outside workspace %q", submittedPath, rootPath)
	}

	// Root binds traversal and opening to the workspace directory handle.
	// Symlinks are allowed only when they remain inside the root. O_NONBLOCK
	// prevents a FIFO swapped into place before open from blocking the tool;
	// it has no effect on regular-file reads.
	f, err := root.OpenFile(candidate, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("open plan file %q inside workspace %q: %w", submittedPath, rootPath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect plan file %q: %w", submittedPath, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("plan file %q is not a regular file", submittedPath)
	}
	if info.Size() > MaxSubmittedPlanBytes {
		return "", fmt.Errorf("plan file %q exceeds the %d-byte limit", submittedPath, MaxSubmittedPlanBytes)
	}

	data, err := io.ReadAll(io.LimitReader(f, MaxSubmittedPlanBytes+1))
	if err != nil {
		return "", fmt.Errorf("read plan file %q: %w", submittedPath, err)
	}
	if int64(len(data)) > MaxSubmittedPlanBytes {
		return "", fmt.Errorf("plan file %q exceeds the %d-byte limit", submittedPath, MaxSubmittedPlanBytes)
	}
	return ValidatePlanContent(string(data), fmt.Sprintf("plan file %q", submittedPath))
}

// ValidatePlanContent applies one storage/context contract to file, inline,
// legacy, and user-edited plans.
func ValidatePlanContent(content, source string) (string, error) {
	if source == "" {
		source = "plan"
	}
	if int64(len(content)) > MaxSubmittedPlanBytes {
		return "", fmt.Errorf("%s exceeds the %d-byte limit", source, MaxSubmittedPlanBytes)
	}
	if bytes.IndexByte([]byte(content), 0) >= 0 || !utf8.ValidString(content) {
		return "", fmt.Errorf("%s is not valid UTF-8 text", source)
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("%s is empty", source)
	}
	return content, nil
}
