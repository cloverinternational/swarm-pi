package builtin

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/findings"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
	"github.com/google/uuid"
)

// AnalysisTriggerMode controls when the LLM evaluates accumulated findings.
type AnalysisTriggerMode string

const (
	// TriggerOnCompaction runs analysis when context is compacted (natural checkpoint).
	TriggerOnCompaction AnalysisTriggerMode = "on_compaction"

	// TriggerOnSessionEnd runs analysis when the session ends (like autoDream).
	TriggerOnSessionEnd AnalysisTriggerMode = "on_session_end"

	// TriggerEveryNTools runs analysis every N tool executions.
	TriggerEveryNTools AnalysisTriggerMode = "every_n_tools"

	// TriggerDisabled disables LLM analysis entirely (Bronze capture only).
	TriggerDisabled AnalysisTriggerMode = "disabled"
)

// FindingsAnalysisConfig configures the findings analysis hook behavior.
type FindingsAnalysisConfig struct {
	// TriggerMode controls when analysis runs
	TriggerMode AnalysisTriggerMode

	// ToolInterval is the number of tool calls between analysis runs
	// (only used when TriggerMode is TriggerEveryNTools)
	ToolInterval int

	// BatchSize is the maximum number of findings to evaluate in one LLM call
	BatchSize int
}

// DefaultFindingsAnalysisConfig returns sensible defaults.
func DefaultFindingsAnalysisConfig() FindingsAnalysisConfig {
	return FindingsAnalysisConfig{
		TriggerMode:  TriggerOnCompaction,
		ToolInterval: 10,
		BatchSize:    20,
	}
}

// AnalysisContextProvider supplies rich session context for findings evaluation.
// Implemented by the TUI layer which has access to conversation, dreams, etc.
type AnalysisContextProvider interface {
	// GetRecentMessages returns the last N conversation messages (role + content summary).
	GetRecentMessages(n int) []string
	// GetActivePlan returns the current plan content if in plan mode, empty otherwise.
	GetActivePlan() string
	// GetTaskHistory returns completed + in-progress task summaries.
	GetTaskHistory() []string
}

// FindingsAnalysisHook triggers LLM-based evaluation of Bronze findings.
type FindingsAnalysisHook struct {
	steeringAgent   *agent.SteeringAgent
	cache           findings.Cache
	logger          observability.Logger
	config          FindingsAnalysisConfig
	contextProvider AnalysisContextProvider // Optional: rich session context
	debugDir        string                  // Directory for debug log files
	eventEmitter    SteeringEventEmitter

	// State
	mu                 sync.Mutex
	pendingFindings    []findings.Finding
	toolsSinceAnalysis int
	currentPhase       string // Current workflow phase for sequence tracking
	phaseToolCount     int    // Tool call count within current phase
	lastSessionSummary string
}

// NewFindingsAnalysisHook creates a new findings analysis hook.
// debugDir is the base findings directory; debug logs are written to debugDir/debug/.
func NewFindingsAnalysisHook(
	steeringAgent *agent.SteeringAgent,
	cache findings.Cache,
	logger observability.Logger,
	config FindingsAnalysisConfig,
	debugDir string,
) *FindingsAnalysisHook {
	if config.BatchSize == 0 {
		config.BatchSize = 20
	}
	if config.ToolInterval == 0 {
		config.ToolInterval = 10
	}
	return &FindingsAnalysisHook{
		steeringAgent:   steeringAgent,
		cache:           cache,
		logger:          logger,
		config:          config,
		debugDir:        debugDir,
		pendingFindings: make([]findings.Finding, 0),
	}
}

// Name returns the hook name.
func (h *FindingsAnalysisHook) Name() string {
	return "findings-analysis"
}

// Priority returns the hook priority (lower than findings capture).
func (h *FindingsAnalysisHook) Priority() int {
	return 75 // After local-findings (85) and tool-result-analysis (90)
}

// Filter returns true for events that this hook should process.
func (h *FindingsAnalysisHook) Filter(event hooks.Event) bool {
	if h.config.TriggerMode == TriggerDisabled {
		return false
	}

	switch event.Type {
	case hooks.EventToolAfterExecute:
		// Always listen to accumulate findings; trigger check happens in OnEvent
		return true
	case hooks.EventAgentStopped:
		// Always flush pending findings at session end unless analysis is
		// disabled. This keeps short sessions useful even when the primary
		// trigger is compaction or every-N-tools.
		return true
	case hooks.EventContextTrimmed:
		return h.config.TriggerMode == TriggerOnCompaction
	}
	return false
}

// OnEvent processes events and triggers analysis when appropriate.
func (h *FindingsAnalysisHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	switch event.Type {
	case hooks.EventToolAfterExecute:
		return h.handleToolAfterExecute(ctx, event)
	case hooks.EventAgentStopped:
		return h.handleSessionEnd(ctx, event)
	case hooks.EventContextTrimmed:
		return h.handleCompaction(ctx, event)
	}
	return hooks.Continue(), nil
}

// handleToolAfterExecute accumulates the finding and checks if analysis should trigger.
func (h *FindingsAnalysisHook) handleToolAfterExecute(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	// Extract finding info from the event to build a pending finding
	finding := h.extractFindingFromEvent(event)
	if finding.ToolName == "" {
		return hooks.Continue(), nil
	}

	h.mu.Lock()
	h.pendingFindings = append(h.pendingFindings, finding)
	h.toolsSinceAnalysis++
	shouldTrigger := h.config.TriggerMode == TriggerEveryNTools &&
		h.toolsSinceAnalysis >= h.config.ToolInterval
	h.mu.Unlock()

	if shouldTrigger {
		return h.runAnalysis(ctx, "every_n_tools")
	}

	return hooks.Continue(), nil
}

// handleSessionEnd triggers analysis on all accumulated findings.
func (h *FindingsAnalysisHook) handleSessionEnd(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	h.captureSessionSummaryFinding(ctx, "session_end", event)
	return h.runAnalysis(ctx, "session_end")
}

// handleCompaction triggers analysis on all accumulated findings.
func (h *FindingsAnalysisHook) handleCompaction(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	h.captureSessionSummaryFinding(ctx, "compaction", event)
	return h.runAnalysis(ctx, "compaction")
}

// runAnalysis is disabled — LLM evaluation of findings is turned off.
// Findings are still captured by local_findings.go and flow to ClickHouse
// via bronze events, but the per-session SteeringAgent scoring step is skipped.
func (h *FindingsAnalysisHook) runAnalysis(ctx context.Context, trigger string) (hooks.HookResult, error) {
	return hooks.Continue(), nil
}

func (h *FindingsAnalysisHook) captureSessionSummaryFinding(ctx context.Context, trigger string, event hooks.Event) {
	h.mu.Lock()
	cp := h.contextProvider
	h.mu.Unlock()
	if cp == nil {
		return
	}

	recentMsgs := cp.GetRecentMessages(0)
	if len(recentMsgs) == 0 {
		return
	}
	activePlan := cp.GetActivePlan()
	taskHistory := cp.GetTaskHistory()
	key := strings.Join(recentMsgs, "\n") + "\n---plan---\n" + activePlan + "\n---tasks---\n" + strings.Join(taskHistory, "\n")

	h.mu.Lock()
	if key == h.lastSessionSummary {
		h.mu.Unlock()
		return
	}
	h.lastSessionSummary = key
	h.mu.Unlock()

	conversationID := event.ConversationID
	if conversationID == "" {
		conversationID, _ = event.Data["session_id"].(string)
	}

	outputPreview := buildSessionSummaryPreview(recentMsgs, activePlan, taskHistory)
	finding := findings.Finding{
		FindingID:      uuid.New().String(),
		ToolName:       "conversation-summary",
		ContextSummary: fmt.Sprintf("Conversation summary at %s", trigger),
		ToolInput: map[string]any{
			"trigger": trigger,
		},
		ToolOutput: map[string]any{
			"summary": outputPreview,
			"success": true,
		},
		Timestamp:      time.Now(),
		AgentID:        event.AgentID,
		ConversationID: conversationID,
		Tags:           []string{"conversation-summary", "insight", "memory"},
		Metadata: findings.FindingMetadata{
			Success:  true,
			Priority: 72,
			Source:   "session_summary",
			Custom: map[string]any{
				"phase":      "documenting",
				"task":       "session summary",
				"trigger":    trigger,
				"llm_source": "session_summary",
			},
		},
	}

	if h.cache != nil {
		if err := h.cache.Write(ctx, finding); err != nil && h.logger != nil {
			h.logger.Warn(ctx, "findings_analysis.session_summary_cache_write_failed",
				observability.F("error", err.Error()),
				observability.F("finding_id", finding.FindingID))
		}
	}
	h.emitCaptured(ctx, finding)

	h.mu.Lock()
	h.pendingFindings = append(h.pendingFindings, finding)
	h.mu.Unlock()
}

// SetConfig updates the analysis configuration at runtime.
func (h *FindingsAnalysisHook) SetConfig(config FindingsAnalysisConfig) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.config = config
}

// SetContextProvider sets the rich session context provider.
func (h *FindingsAnalysisHook) SetContextProvider(cp AnalysisContextProvider) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.contextProvider = cp
}

// SetEventEmitter wires evaluation events back into the hook stream for local
// bronze capture and remote artifact mirroring.
func (h *FindingsAnalysisHook) SetEventEmitter(emitter SteeringEventEmitter) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.eventEmitter = emitter
}

func (h *FindingsAnalysisHook) emitCaptured(ctx context.Context, finding findings.Finding) {
	h.mu.Lock()
	emitter := h.eventEmitter
	h.mu.Unlock()
	if emitter == nil {
		return
	}
	evt := hooks.Event{
		Type:           hooks.EventFindingsCaptured,
		Timestamp:      finding.Timestamp,
		ConversationID: finding.ConversationID,
		AgentID:        finding.AgentID,
		Data: map[string]any{
			"artifact_type": "finding",
			"artifact_id":   finding.FindingID,
			"finding_id":    finding.FindingID,
			"tool_name":     finding.ToolName,
			"priority":      finding.Metadata.Priority,
			"tags":          finding.Tags,
			"finding":       finding,
		},
	}
	if _, err := emitter.Emit(ctx, evt); err != nil && h.logger != nil {
		h.logger.Warn(ctx, "findings_analysis.emit_captured_failed",
			observability.F("error", err.Error()),
			observability.F("finding_id", finding.FindingID))
	}
}

// GetConfig returns the current analysis configuration.
func (h *FindingsAnalysisHook) GetConfig() FindingsAnalysisConfig {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.config
}

// GetPendingCount returns the number of findings waiting for analysis.
func (h *FindingsAnalysisHook) GetPendingCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.pendingFindings)
}

// extractFindingFromEvent builds a Finding from a tool.after_execute event.
func (h *FindingsAnalysisHook) extractFindingFromEvent(event hooks.Event) findings.Finding {
	toolName, _ := event.Data["tool_name"].(string)
	toolInput, _ := event.Data["tool_input"].(map[string]any)
	toolOutput, _ := event.Data["tool_output"].(map[string]any)

	success := true
	if s, ok := toolOutput["success"].(bool); ok {
		success = s
	}

	var durationMs int64
	if d, ok := toolOutput["duration_ms"].(float64); ok {
		durationMs = int64(d)
	}

	var errMsg string
	if e, ok := toolOutput["error"].(string); ok {
		errMsg = e
	} else if e, ok := toolOutput["error_message"].(string); ok {
		errMsg = e
	}

	// Extract tags from metadata if available
	var tags []string
	priority := 50.0
	if event.Metadata != nil {
		if t, ok := event.Metadata["finding_tags"].([]string); ok {
			tags = t
		}
		priority = extractFindingPriority(event.Metadata)
	}

	// Get current workflow phase from task system
	phase, taskSubject := h.getCurrentPhase()

	// Track tool sequence within phase
	h.mu.Lock()
	if phase != h.currentPhase {
		h.currentPhase = phase
		h.phaseToolCount = 0
	}
	h.phaseToolCount++
	toolIndex := h.phaseToolCount
	h.mu.Unlock()

	// Build meaningful context summary from tool input
	var contextSummary string
	switch toolName {
	case "Read", "file_read":
		if fp, ok := toolInput["file_path"].(string); ok {
			contextSummary = fmt.Sprintf("Read %s", fp)
			if sl, ok := toolInput["start_line"]; ok {
				contextSummary += fmt.Sprintf(" (lines %v-%v)", sl, toolInput["end_line"])
			}
		}
	case "Shell", "Bash", "bash":
		if cmd, ok := toolInput["command"].(string); ok {
			if len(cmd) > 100 {
				cmd = cmd[:100] + "..."
			}
			contextSummary = fmt.Sprintf("Shell: %s", cmd)
		}
	case "Edit", "str_replace":
		if fp, ok := toolInput["file_path"].(string); ok {
			contextSummary = fmt.Sprintf("Edit %s", fp)
		}
	case "Write", "file_write":
		if fp, ok := toolInput["file_path"].(string); ok {
			contextSummary = fmt.Sprintf("Write %s", fp)
		}
	case "semantic_grep", "grep":
		if pat, ok := toolInput["pattern"].(string); ok {
			contextSummary = fmt.Sprintf("Search: %s", pat)
		}
	case "TaskCreate", "task_create":
		if subj, ok := toolInput["subject"].(string); ok {
			contextSummary = fmt.Sprintf("Task: %s", subj)
		}
	case "TaskUpdate", "task_update":
		if status, ok := toolInput["status"].(string); ok {
			contextSummary = fmt.Sprintf("TaskUpdate: %s", status)
		}
	case "TaskManage", "task_manage":
		if operations, err := ii.InspectTaskManageOperations(toolInput); err == nil {
			for _, operation := range operations {
				switch operation.Kind {
				case ii.TaskOperationCreate:
					contextSummary = fmt.Sprintf("Task: %s", operation.Subject)
				case ii.TaskOperationUpdate:
					contextSummary = fmt.Sprintf("TaskUpdate: %s", operation.Status)
				}
				if contextSummary != "" {
					break
				}
			}
		}
	default:
		if desc, ok := toolInput["description"].(string); ok {
			contextSummary = fmt.Sprintf("%s: %s", toolName, desc)
		}
	}

	return findings.Finding{
		FindingID:      uuid.New().String(),
		ToolName:       toolName,
		ContextSummary: contextSummary,
		ToolInput:      toolInput,
		ToolOutput:     toolOutput,
		Timestamp:      time.Now(),
		AgentID:        event.AgentID,
		ConversationID: event.ConversationID,
		Tags:           tags,
		Metadata: findings.FindingMetadata{
			DurationMs:   durationMs,
			Success:      success,
			ErrorMessage: errMsg,
			Priority:     priority,
			Source:       "local",
			Custom: map[string]any{
				"phase":      phase,
				"task":       taskSubject,
				"tool_index": toolIndex,
			},
		},
	}
}

// getCurrentPhase reads the current workflow phase from the global task manager.
// Returns the category of the in-progress task and the task subject.
func (h *FindingsAnalysisHook) getCurrentPhase() (phase string, taskSubject string) {
	mgr := ii.GetTodoManager()
	if mgr == nil {
		// Fallback to last known phase
		h.mu.Lock()
		p := h.currentPhase
		h.mu.Unlock()
		return p, ""
	}
	for _, task := range mgr.Todos() {
		if task.Status == ii.TodoStatusInProgress {
			return string(task.Category), task.Content
		}
	}
	// No in-progress task found -- use cached phase
	h.mu.Lock()
	p := h.currentPhase
	h.mu.Unlock()
	return p, ""
}

func buildSessionSummaryPreview(messages []string, activePlan string, taskHistory []string) string {
	var parts []string
	if len(messages) > 0 {
		start := 0
		if len(messages) > 12 {
			start = len(messages) - 12
		}
		parts = append(parts, "Recent messages:\n"+strings.Join(messages[start:], "\n"))
	}
	if activePlan != "" {
		parts = append(parts, "Active plan:\n"+activePlan)
	}
	if len(taskHistory) > 0 {
		parts = append(parts, "Task history:\n"+strings.Join(taskHistory, "\n"))
	}
	preview := strings.Join(parts, "\n\n")
	if len(preview) > 4000 {
		return preview[:4000] + "..."
	}
	return preview
}

func extractFindingPriority(metadata map[string]any) float64 {
	if metadata == nil {
		return 50
	}
	switch p := metadata["finding_priority"].(type) {
	case float64:
		return p
	case float32:
		return float64(p)
	case int:
		return float64(p)
	case int64:
		return float64(p)
	case uint:
		return float64(p)
	case uint64:
		return float64(p)
	default:
		return 50
	}
}
