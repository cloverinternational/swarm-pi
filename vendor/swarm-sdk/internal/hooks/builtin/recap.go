package builtin

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

// RecapConfig defines the configuration for recap behavior
type RecapConfig struct {
	// Enable automatic recap on session resume
	EnableRecap bool `json:"enableRecap"`

	// Force recap even with telemetry disabled
	ForceEnableRecap bool `json:"forceEnableRecap"`

	// Max messages to include in recap analysis
	MaxMessages int `json:"maxMessages"`

	// Format for recap output
	Format RecapFormat `json:"format"`

	// Include tool results in recap
	IncludeToolResults bool `json:"includeToolResults"`

	// Inactivity threshold for showing recap (in minutes)
	InactivityThresholdMinutes int `json:"inactivityThresholdMinutes"`
}

// RecapFormat defines the output format
type RecapFormat string

const (
	RecapFormatBrief    RecapFormat = "brief"
	RecapFormatDetailed RecapFormat = "detailed"
	RecapFormatSummary  RecapFormat = "summary"
)

// RecapResult represents the generated recap
type RecapResult struct {
	Summary       string    `json:"summary"`
	SessionID     string    `json:"sessionId"`
	LastActive    time.Time `json:"lastActive"`
	PendingTasks  []string  `json:"pendingTasks"`
	ModifiedFiles []string  `json:"modifiedFiles"`
	OriginalGoal  string    `json:"originalGoal"`
	ProgressMade  string    `json:"progressMade"`
	CurrentState  string    `json:"currentState"`
	NextSteps     []string  `json:"nextSteps"`
}

// RecapGenerator defines the interface for generating recaps
type RecapGenerator interface {
	Generate(ctx context.Context, messages []*conversation.Message, config RecapConfig) (RecapResult, error)
}

// DefaultRecapGenerator implements rule-based recap generation
type DefaultRecapGenerator struct {
	logger observability.Logger
}

// NewDefaultRecapGenerator creates a new recap generator
func NewDefaultRecapGenerator(logger observability.Logger) *DefaultRecapGenerator {
	if logger == nil {
		logger = noop.NewLogger()
	}
	return &DefaultRecapGenerator{logger: logger}
}

// Generate creates a recap from conversation messages
func (g *DefaultRecapGenerator) Generate(ctx context.Context, messages []*conversation.Message, config RecapConfig) (RecapResult, error) {
	if len(messages) == 0 {
		return RecapResult{
			Summary:    "No conversation history available.",
			SessionID:  "",
			LastActive: time.Now(),
		}, nil
	}

	// Get recent messages based on config
	maxMessages := config.MaxMessages
	if maxMessages <= 0 {
		maxMessages = 50 // Default
	}

	startIdx := 0
	if len(messages) > maxMessages {
		startIdx = len(messages) - maxMessages
	}
	recentMessages := messages[startIdx:]

	// Extract information from messages
	originalGoal := g.extractOriginalGoal(recentMessages)
	progressMade := g.extractProgress(recentMessages)
	currentState := g.extractCurrentState(recentMessages)
	pendingTasks := g.extractPendingTasks(recentMessages)
	modifiedFiles := g.extractModifiedFiles(recentMessages)
	nextSteps := g.extractNextSteps(recentMessages)

	// Build summary based on format
	summary := g.buildSummary(originalGoal, progressMade, currentState, pendingTasks, modifiedFiles, config.Format)

	return RecapResult{
		Summary:       summary,
		SessionID:     "",
		LastActive:    time.Now(),
		PendingTasks:  pendingTasks,
		ModifiedFiles: modifiedFiles,
		OriginalGoal:  originalGoal,
		ProgressMade:  progressMade,
		CurrentState:  currentState,
		NextSteps:     nextSteps,
	}, nil
}

// extractOriginalGoal extracts the user's initial goal from the first few messages
func (g *DefaultRecapGenerator) extractOriginalGoal(messages []*conversation.Message) string {
	if len(messages) == 0 {
		return "Unknown"
	}

	// Look for the first user message that contains a task/request
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if msg.Role == conversation.RoleUser {
			content := strings.TrimSpace(msg.Content)
			if len(content) > 10 { // Filter out short greetings
				// Truncate if too long
				if len(content) > 200 {
					return content[:200] + "..."
				}
				return content
			}
		}
	}

	return "General conversation"
}

// extractProgress extracts progress made from messages
func (g *DefaultRecapGenerator) extractProgress(messages []*conversation.Message) string {
	var progress []string

	for _, msg := range messages {
		if msg == nil {
			continue
		}
		content := strings.ToLower(msg.Content)

		// Look for completion indicators
		if strings.Contains(content, "completed") ||
			strings.Contains(content, "finished") ||
			strings.Contains(content, "done") ||
			strings.Contains(content, "success") ||
			strings.Contains(content, "created") ||
			strings.Contains(content, "updated") ||
			strings.Contains(content, "fixed") {
			progress = append(progress, g.extractSentence(msg.Content))
		}
	}

	if len(progress) == 0 {
		return "Progress not explicitly documented"
	}

	if len(progress) > 3 {
		progress = progress[len(progress)-3:] // Last 3 progress items
	}

	return strings.Join(progress, "; ")
}

// extractCurrentState extracts the current state from messages
func (g *DefaultRecapGenerator) extractCurrentState(messages []*conversation.Message) string {
	if len(messages) == 0 {
		return "Unknown state"
	}

	// Look at the last few messages for state
	start := max(len(messages)-5, 0)

	var stateParts []string
	for i := start; i < len(messages); i++ {
		msg := messages[i]
		if msg == nil {
			continue
		}
		content := strings.ToLower(msg.Content)

		// Check for pending/blocking states
		if strings.Contains(content, "waiting") ||
			strings.Contains(content, "pending") ||
			strings.Contains(content, "in progress") ||
			strings.Contains(content, "need to") ||
			strings.Contains(content, "should") {
			stateParts = append(stateParts, g.extractSentence(msg.Content))
		}
	}

	if len(stateParts) == 0 {
		return "Conversation ongoing"
	}

	return strings.Join(stateParts, "; ")
}

// extractPendingTasks extracts pending tasks from messages
func (g *DefaultRecapGenerator) extractPendingTasks(messages []*conversation.Message) []string {
	var tasks []string

	for _, msg := range messages {
		if msg == nil {
			continue
		}
		content := strings.ToLower(msg.Content)

		// Look for task indicators
		if strings.Contains(content, "todo") ||
			strings.Contains(content, "task") ||
			strings.Contains(content, "need to") ||
			strings.Contains(content, "should") ||
			strings.Contains(content, "pending") ||
			strings.Contains(content, "unchecked") {
			task := g.extractSentence(msg.Content)
			if len(task) > 10 { // Filter out short fragments
				tasks = append(tasks, task)
			}
		}
	}

	// Remove duplicates and limit
	tasks = g.deduplicateStrings(tasks)
	if len(tasks) > 10 {
		tasks = tasks[:10]
	}

	return tasks
}

// extractModifiedFiles extracts file references from messages
func (g *DefaultRecapGenerator) extractModifiedFiles(messages []*conversation.Message) []string {
	fileMap := make(map[string]bool)

	for _, msg := range messages {
		if msg == nil {
			continue
		}
		content := msg.Content

		// Look for file patterns
		lines := strings.SplitSeq(content, "\n")
		for line := range lines {
			lower := strings.ToLower(line)

			// Check for file operations. Match verb stems so common
			// inflections are caught too: "modify"/"modified"/"modifies",
			// "update"/"updated", "create"/"created", "edit"/"edited".
			if strings.Contains(lower, "write") ||
				strings.Contains(lower, "edit") ||
				strings.Contains(lower, "modif") ||
				strings.Contains(lower, "updat") ||
				strings.Contains(lower, "creat") ||
				strings.Contains(lower, "file") {

				// Extract file paths (simplified)
				words := strings.FieldsSeq(line)
				for word := range words {
					word = strings.Trim(word, "\"'`[](){}<>")
					if g.looksLikeFilePath(word) && !fileMap[word] {
						fileMap[word] = true
					}
				}
			}
		}
	}

	// Convert map to slice
	files := make([]string, 0, len(fileMap))
	for file := range fileMap {
		files = append(files, file)
	}

	// Limit results
	if len(files) > 20 {
		files = files[:20]
	}

	return files
}

// extractNextSteps extracts suggested next steps from messages
func (g *DefaultRecapGenerator) extractNextSteps(messages []*conversation.Message) []string {
	var steps []string

	for _, msg := range messages {
		if msg == nil {
			continue
		}
		content := strings.ToLower(msg.Content)

		// Look for next step indicators
		if strings.Contains(content, "next") ||
			strings.Contains(content, "then") ||
			strings.Contains(content, "after that") ||
			strings.Contains(content, "following") ||
			strings.Contains(content, "step") {
			step := g.extractSentence(msg.Content)
			if len(step) > 10 {
				steps = append(steps, step)
			}
		}
	}

	steps = g.deduplicateStrings(steps)
	if len(steps) > 5 {
		steps = steps[:5]
	}

	return steps
}

// looksLikeFilePath checks if a string looks like a file path
func (g *DefaultRecapGenerator) looksLikeFilePath(s string) bool {
	// Simple heuristics
	if strings.Contains(s, "/") || strings.Contains(s, "\\") {
		// Contains path separators
		if strings.Contains(s, ".") {
			// Has file extension
			return true
		}
	}
	if strings.HasSuffix(s, ".go") ||
		strings.HasSuffix(s, ".ts") ||
		strings.HasSuffix(s, ".js") ||
		strings.HasSuffix(s, ".json") ||
		strings.HasSuffix(s, ".md") ||
		strings.HasSuffix(s, ".yaml") ||
		strings.HasSuffix(s, ".yml") {
		return true
	}
	return false
}

// extractSentence extracts a readable sentence from text
func (g *DefaultRecapGenerator) extractSentence(text string) string {
	// Clean up the text
	text = strings.TrimSpace(text)
	// Remove markdown formatting
	text = strings.ReplaceAll(text, "**", "")
	text = strings.ReplaceAll(text, "*", "")
	// Limit length
	if len(text) > 200 {
		return text[:200] + "..."
	}
	return text
}

// deduplicateStrings removes duplicate strings
func (g *DefaultRecapGenerator) deduplicateStrings(items []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(items))
	for _, item := range items {
		clean := strings.TrimSpace(item)
		if !seen[clean] && clean != "" {
			seen[clean] = true
			result = append(result, clean)
		}
	}
	return result
}

// buildSummary constructs the final summary text
func (g *DefaultRecapGenerator) buildSummary(goal, progress, state string, tasks, files []string, format RecapFormat) string {
	switch format {
	case RecapFormatBrief:
		return fmt.Sprintf("Goal: %s. Progress: %s. Current: %s.", goal, progress, state)

	case RecapFormatSummary:
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("**Goal:** %s\n\n", goal))
		sb.WriteString(fmt.Sprintf("**Progress:** %s\n\n", progress))
		sb.WriteString(fmt.Sprintf("**Current State:** %s", state))
		return sb.String()

	case RecapFormatDetailed:
		fallthrough
	default:
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("## Session Recap\n\n"))
		sb.WriteString(fmt.Sprintf("**Goal:** %s\n\n", goal))
		sb.WriteString(fmt.Sprintf("**Progress Made:** %s\n\n", progress))
		sb.WriteString(fmt.Sprintf("**Current State:** %s\n", state))

		if len(tasks) > 0 {
			sb.WriteString("\n**Pending Tasks:**\n")
			for _, task := range tasks {
				sb.WriteString(fmt.Sprintf("- %s\n", task))
			}
		}

		if len(files) > 0 {
			sb.WriteString("\n**Files Referenced:**\n")
			for i, file := range files {
				if i >= 10 {
					break
				}
				sb.WriteString(fmt.Sprintf("- %s\n", file))
			}
		}

		return sb.String()
	}
}

// RecapHook generates session recaps when conversations are resumed
type RecapHook struct {
	config       RecapConfig
	generator    RecapGenerator
	storage      storage.Storage
	logger       observability.Logger
	priority     int
	enabled      bool
	sessionCache map[string]*sessionInfo
}

// sessionInfo tracks session metadata
type sessionInfo struct {
	lastActive    time.Time
	lastRecapTime time.Time
	messageCount  int
}

// RecapOption configures the recap hook
type RecapOption func(*RecapHook)

// WithRecapConfig sets the recap configuration
func WithRecapConfig(config RecapConfig) RecapOption {
	return func(h *RecapHook) {
		h.config = config
	}
}

// WithRecapGenerator sets a custom recap generator
func WithRecapGenerator(generator RecapGenerator) RecapOption {
	return func(h *RecapHook) {
		h.generator = generator
	}
}

// WithRecapStorage sets the conversation storage
func WithRecapStorage(s storage.Storage) RecapOption {
	return func(h *RecapHook) {
		h.storage = s
	}
}

// WithRecapPriority sets the hook priority
func WithRecapPriority(priority int) RecapOption {
	return func(h *RecapHook) {
		h.priority = priority
	}
}

// WithRecapEnabled sets whether recap is enabled
func WithRecapEnabled(enabled bool) RecapOption {
	return func(h *RecapHook) {
		h.enabled = enabled
	}
}

// NewRecapHook creates a new recap hook
func NewRecapHook(logger observability.Logger, opts ...RecapOption) *RecapHook {
	if logger == nil {
		logger = noop.NewLogger()
	}

	h := &RecapHook{
		config: RecapConfig{
			EnableRecap:                true,
			ForceEnableRecap:           false,
			MaxMessages:                50,
			Format:                     RecapFormatDetailed,
			IncludeToolResults:         true,
			InactivityThresholdMinutes: 5,
		},
		generator:    NewDefaultRecapGenerator(logger),
		logger:       logger,
		priority:     80, // Medium-high priority
		enabled:      true,
		sessionCache: make(map[string]*sessionInfo),
	}

	// Apply options
	for _, opt := range opts {
		opt(h)
	}

	// Check environment variables
	if os.Getenv("SWARM_RECAP_ENABLED") == "0" {
		h.enabled = false
		h.config.EnableRecap = false
	}
	if os.Getenv("SWARM_RECAP_ENABLED") == "1" {
		h.enabled = true
		h.config.EnableRecap = true
		h.config.ForceEnableRecap = true
	}

	return h
}

// Name implements hooks.Hook
func (h *RecapHook) Name() string { return "recap" }

// Priority implements hooks.Hook
func (h *RecapHook) Priority() int { return h.priority }

// Filter implements hooks.Hook
// Processes conversation resumed events and context restoration events
func (h *RecapHook) Filter(event hooks.Event) bool {
	if !h.enabled || !h.config.EnableRecap {
		return false
	}

	// Skip if explicitly disabled via env
	if os.Getenv("SWARM_RECAP_ENABLED") == "0" && !h.config.ForceEnableRecap {
		return false
	}

	return event.Type == hooks.EventConversationResumed ||
		event.Type == hooks.EventContextRestored
}

// OnEvent implements hooks.Hook
// Generates and attaches recap when appropriate
func (h *RecapHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	convID := event.ConversationID

	h.logger.Debug(ctx, "recap.checking",
		observability.F("conversation_id", convID),
		observability.F("event_type", event.Type))

	// Check if we should show recap for this session
	if !h.shouldShowRecap(convID) {
		return hooks.Continue(), nil
	}

	// Load conversation from storage if available
	var messages []*conversation.Message
	if h.storage != nil && convID != "" {
		conv, err := h.storage.Load(ctx, convID)
		if err == nil && conv != nil {
			messages = conv.Messages
		} else {
			h.logger.Warn(ctx, "recap.load_conversation_failed",
				observability.F("conversation_id", convID),
				observability.F("error", err))
		}
	}

	// Also check for messages in event data
	if eventMsgs, ok := event.Data["messages"].([]*conversation.Message); ok {
		messages = eventMsgs
	}

	if len(messages) == 0 {
		h.logger.Debug(ctx, "recap.no_messages", observability.F("conversation_id", convID))
		return hooks.Continue(), nil
	}

	// Generate recap
	recap, err := h.generator.Generate(ctx, messages, h.config)
	if err != nil {
		h.logger.Warn(ctx, "recap.generation_failed",
			observability.F("error", err),
			observability.F("conversation_id", convID))
		return hooks.Continue(), nil
	}

	recap.SessionID = convID

	// Update session cache
	h.updateSessionCache(convID, len(messages))

	h.logger.Info(ctx, "recap.generated",
		observability.F("conversation_id", convID),
		observability.F("format", h.config.Format))

	// Create modified event with recap attachment
	modified := event.Clone()
	if modified.Data == nil {
		modified.Data = make(map[string]any)
	}
	modified.Data["recap"] = recap
	modified.Data["recap_generated"] = true
	modified.Data["recap_timestamp"] = time.Now().UTC()

	return hooks.ModifyWithMessage(modified, fmt.Sprintf("Recap generated (%d messages analyzed)", len(messages))), nil
}

// shouldShowRecap determines if recap should be shown for a session
func (h *RecapHook) shouldShowRecap(convID string) bool {
	if convID == "" {
		return false
	}

	info, exists := h.sessionCache[convID]
	if !exists {
		// First time seeing this session - show recap
		return true
	}

	// Check inactivity threshold
	inactiveDuration := time.Since(info.lastActive)
	threshold := time.Duration(h.config.InactivityThresholdMinutes) * time.Minute
	if h.config.InactivityThresholdMinutes <= 0 {
		threshold = 5 * time.Minute // Default
	}

	return inactiveDuration > threshold
}

// updateSessionCache updates the session cache
func (h *RecapHook) updateSessionCache(convID string, messageCount int) {
	h.sessionCache[convID] = &sessionInfo{
		lastActive:    time.Now(),
		lastRecapTime: time.Now(),
		messageCount:  messageCount,
	}
}

// SetEnabled enables or disables the recap hook
func (h *RecapHook) SetEnabled(enabled bool) {
	h.enabled = enabled
}

// IsEnabled returns whether recap is enabled
func (h *RecapHook) IsEnabled() bool {
	if os.Getenv("SWARM_RECAP_ENABLED") == "0" && !h.config.ForceEnableRecap {
		return false
	}
	return h.enabled && h.config.EnableRecap
}

// GetConfig returns the current recap configuration
func (h *RecapHook) GetConfig() RecapConfig {
	return h.config
}

// UpdateConfig updates the recap configuration
func (h *RecapHook) UpdateConfig(config RecapConfig) {
	h.config = config
}

// GenerateRecap manually generates a recap for a session
func (h *RecapHook) GenerateRecap(ctx context.Context, convID string) (*RecapResult, error) {
	if h.storage == nil || convID == "" {
		return nil, fmt.Errorf("storage not available or invalid conversation ID")
	}

	conv, err := h.storage.Load(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("failed to load conversation: %w", err)
	}
	if conv == nil {
		return nil, fmt.Errorf("conversation not found")
	}

	recap, err := h.generator.Generate(ctx, conv.Messages, h.config)
	if err != nil {
		return nil, fmt.Errorf("failed to generate recap: %w", err)
	}

	recap.SessionID = convID
	return &recap, nil
}
