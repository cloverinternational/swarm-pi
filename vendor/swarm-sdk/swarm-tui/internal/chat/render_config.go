package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// ToolDisplayMode defines how tool output is rendered
type ToolDisplayMode string

const (
	DisplayVerbose ToolDisplayMode = "verbose" // Show full output
	DisplayCompact ToolDisplayMode = "compact" // Show truncated (default)
	DisplayMinimal ToolDisplayMode = "minimal" // Only show tool name + 1 line summary
	DisplayHidden  ToolDisplayMode = "hidden"  // Don't show output, only name
	DisplayDiff    ToolDisplayMode = "diff"    // Special diff view for file operations
)

// ToolRenderConfig defines rendering preferences for a specific tool
type ToolRenderConfig struct {
	Name           string          `json:"name"`             // Tool name (bash, Read, etc)
	DisplayMode    ToolDisplayMode `json:"display_mode"`     // How to show output
	MaxLines       int             `json:"max_lines"`        // Max lines in compact mode (0 = use default)
	ShowParams     bool            `json:"show_params"`      // Show tool parameters
	Color          string          `json:"color"`            // Custom color for this tool's output
	AlwaysShowFull bool            `json:"always_show_full"` // Always show full output (ignore line limits)
}

// GetName returns the tool name
func (t *ToolRenderConfig) GetName() string {
	return t.Name
}

// GetDisplayMode returns the display mode as a string
func (t *ToolRenderConfig) GetDisplayMode() string {
	return string(t.DisplayMode)
}

// GetMaxLines returns the max lines
func (t *ToolRenderConfig) GetMaxLines() int {
	return t.MaxLines
}

// GetShowParams returns whether to show parameters
func (t *ToolRenderConfig) GetShowParams() bool {
	return t.ShowParams
}

// RenderSettings holds all rendering customization
type RenderSettings struct {
	// Global settings
	DefaultMode        ToolDisplayMode `json:"default_mode"`          // Default for all tools
	DefaultMaxLines    int             `json:"default_max_lines"`     // Default truncation (6)
	ShowThinking       bool            `json:"show_thinking"`         // Show thinking blocks
	ThinkingBudget     int             `json:"thinking_budget"`       // Token budget for thinking (default: 2048)
	ShowFullToolOutput bool            `json:"show_full_tool_output"` // Verbose tool output
	ShowHooks          bool            `json:"show_hooks"`            // Show hook executions (Ctrl+H to toggle)
	RichAnimations     bool            `json:"rich_animations"`       // Enable animated UI flourishes

	// Display settings (persisted)
	SpinnerType              string `json:"spinner_type"`                 // Loading animation style (Matrix, Braille, etc.)
	CopySelectionShortcut    bool   `json:"copy_selection_shortcut"`      // Enable Ctrl+Shift+C to copy
	AutoCopySelectionOnMouse bool   `json:"auto_copy_selection_on_mouse"` // Auto-copy on mouse selection

	// Startup view preference
	StartupView string `json:"startup_view"` // "simple" = go straight to chat, "advanced" = show home screen

	// Theme settings (persisted)
	ThemeName      string `json:"theme_name"`      // Named colour palette (e.g. "SwarmCode", "Nord", "Dracula")
	BackgroundMode string `json:"background_mode"` // "solid" or "none" (transparent / terminal default)

	// Cache settings
	CachingEnabled bool   `json:"caching_enabled"` // Enable prompt caching
	CacheTTL       string `json:"cache_ttl"`       // "5m" or "1h"

	// Tool-specific overrides
	ToolConfigs map[string]*ToolRenderConfig `json:"tool_configs"`

	// Color scheme
	Colors struct {
		ToolCall   string `json:"tool_call"`   // Color for tool names
		ToolOutput string `json:"tool_output"` // Color for tool results
		ToolError  string `json:"tool_error"`  // Color for errors
		Connector  string `json:"connector"`   // Color for tree connectors
	} `json:"colors"`
}

// ModelCacheStats tracks cache statistics for a specific model
type ModelCacheStats struct {
	ModelName      string    `json:"model_name"`
	Provider       string    `json:"provider"`
	TotalRequests  int64     `json:"total_requests"`
	CreationTokens int64     `json:"creation_tokens"`
	ReadTokens     int64     `json:"read_tokens"`
	HitRate        float64   `json:"hit_rate"`
	LastUsed       time.Time `json:"last_used"`
	TokensSaved    int64     `json:"tokens_saved"`       // Estimate of tokens saved via caching
	CostSaved      float64   `json:"cost_saved"`         // Estimate of cost saved (USD)
	AverageLatency float64   `json:"average_latency_ms"` // Average response time
	CacheHits      int64     `json:"cache_hits"`         // Number of cache hits
	CacheMisses    int64     `json:"cache_misses"`       // Number of cache misses
	InputTokens    int64     `json:"input_tokens"`       // Total input tokens
	OutputTokens   int64     `json:"output_tokens"`      // Total output tokens
}

// CacheStats tracks cache statistics (persistent across sessions)
type CacheStats struct {
	// Session stats (current session only)
	SessionCreationTokens int       `json:"session_creation_tokens"`
	SessionReadTokens     int       `json:"session_read_tokens"`
	SessionHitRate        float64   `json:"session_hit_rate"`
	SessionStartTime      time.Time `json:"session_start_time"`

	// Global stats (all-time)
	TotalRequests       int64   `json:"total_requests"`
	TotalCreationTokens int64   `json:"total_creation_tokens"`
	TotalReadTokens     int64   `json:"total_read_tokens"`
	TotalHitRate        float64 `json:"total_hit_rate"`
	TotalInputTokens    int64   `json:"total_input_tokens"`
	TotalOutputTokens   int64   `json:"total_output_tokens"`

	// Invalidation stats
	SystemPromptInvalidations int64 `json:"system_prompt_invalidations"`
	ToolInvalidations         int64 `json:"tool_invalidations"`
	MCPInvalidations          int64 `json:"mcp_invalidations"`

	// Per-model statistics
	ModelStats map[string]*ModelCacheStats `json:"model_stats"`

	// Cache efficiency metrics
	AverageHitRate   float64 `json:"average_hit_rate"`
	TotalTokensSaved int64   `json:"total_tokens_saved"`
	TotalCostSaved   float64 `json:"total_cost_saved_usd"`

	// Time-based metrics
	LastUpdated   time.Time `json:"last_updated"`
	FirstRecorded time.Time `json:"first_recorded"`
}

// GetToolCall returns the tool call color
func (c *RenderSettings) GetToolCall() string {
	return c.Colors.ToolCall
}

// GetToolOutput returns the tool output color
func (c *RenderSettings) GetToolOutput() string {
	return c.Colors.ToolOutput
}

// GetToolError returns the tool error color
func (c *RenderSettings) GetToolError() string {
	return c.Colors.ToolError
}

// GetConnector returns the connector color
func (c *RenderSettings) GetConnector() string {
	return c.Colors.Connector
}

// GetStartupView returns the startup view preference ("simple" or "advanced")
func (c *RenderSettings) GetStartupView() string {
	if c.StartupView == "" {
		return "simple"
	}
	return c.StartupView
}

// SetStartupView sets the startup view preference
func (c *RenderSettings) SetStartupView(view string) {
	c.StartupView = view
}

// GetColors returns the color scheme
func (rs *RenderSettings) GetColors() *RenderSettings {
	return rs
}

// NewDefaultRenderSettings creates default rendering settings
func NewDefaultRenderSettings() *RenderSettings {
	settings := &RenderSettings{
		DefaultMode:              DisplayCompact,
		DefaultMaxLines:          10,
		ShowThinking:             true,
		ThinkingBudget:           2048, // Default 2048 tokens
		ShowFullToolOutput:       false,
		ShowHooks:                true, // Default: show hook executions
		RichAnimations:           false,
		SpinnerType:              "Braille", // Default spinner type (no random letters)
		CopySelectionShortcut:    false,
		AutoCopySelectionOnMouse: false,
		ThemeName:                "SwarmCode", // Default theme
		BackgroundMode:           "solid",     // Default: solid background
		CachingEnabled:           true,        // Enabled by default
		CacheTTL:                 "1h",        // 1-hour default
		ToolConfigs:              make(map[string]*ToolRenderConfig),
		StartupView:              "simple", // Default: skip home screen, go straight to chat
	}

	// Default colors - model picker palette
	settings.Colors.ToolCall = palette.Accent
	settings.Colors.ToolOutput = palette.TextDim
	settings.Colors.ToolError = palette.Error
	settings.Colors.Connector = palette.TextMuted

	// ============================================================================
	// TOOL CATEGORY DEFAULTS
	// ============================================================================

	// Category A: Read-Only Tools - Apply line limits for readability
	// These tools show data but don't modify anything

	// Shell/System Tools
	settings.ToolConfigs["Bash"] = &ToolRenderConfig{
		Name:           "Bash",
		DisplayMode:    DisplayCompact,
		MaxLines:       15,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: true, // Terminal box has borders — truncation would clip ╰────╯
	}
	settings.ToolConfigs["bash"] = &ToolRenderConfig{
		Name:           "bash",
		DisplayMode:    DisplayCompact,
		MaxLines:       15,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: true, // Terminal box has borders — truncation would clip ╰────╯
	}

	// File Read Tools
	settings.ToolConfigs["Read"] = &ToolRenderConfig{
		Name:           "Read",
		DisplayMode:    DisplayCompact,
		MaxLines:       20,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: false,
	}
	settings.ToolConfigs["ReadLegacy"] = &ToolRenderConfig{
		Name:           "ReadLegacy",
		DisplayMode:    DisplayCompact,
		MaxLines:       20,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: false,
	}
	settings.ToolConfigs["file_read"] = &ToolRenderConfig{
		Name:           "file_read",
		DisplayMode:    DisplayCompact,
		MaxLines:       20,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: false,
	}

	// Search Tools
	settings.ToolConfigs["Grep"] = &ToolRenderConfig{
		Name:           "Grep",
		DisplayMode:    DisplayCompact,
		MaxLines:       60,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: false,
	}
	settings.ToolConfigs["grep"] = &ToolRenderConfig{
		Name:           "grep",
		DisplayMode:    DisplayCompact,
		MaxLines:       60,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: false,
	}
	settings.ToolConfigs["semantic_grep"] = &ToolRenderConfig{
		Name:           "semantic_grep",
		DisplayMode:    DisplayCompact,
		MaxLines:       80,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: false,
	}

	settings.ToolConfigs["Glob"] = &ToolRenderConfig{
		Name:           "Glob",
		DisplayMode:    DisplayCompact,
		MaxLines:       30,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: false,
	}

	settings.ToolConfigs["LS"] = &ToolRenderConfig{
		Name:           "LS",
		DisplayMode:    DisplayCompact,
		MaxLines:       30,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: false,
	}

	// Web Tools
	settings.ToolConfigs["WebFetch"] = &ToolRenderConfig{
		Name:           "WebFetch",
		DisplayMode:    DisplayCompact,
		MaxLines:       20,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: false,
	}
	settings.ToolConfigs["WebSearch"] = &ToolRenderConfig{
		Name:           "WebSearch",
		DisplayMode:    DisplayCompact,
		MaxLines:       20,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: false,
	}

	// Todo/Notebook Read
	settings.ToolConfigs["TodoRead"] = &ToolRenderConfig{
		Name:           "TodoRead",
		DisplayMode:    DisplayCompact,
		MaxLines:       15,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: false,
	}
	settings.ToolConfigs["NotebookRead"] = &ToolRenderConfig{
		Name:           "NotebookRead",
		DisplayMode:    DisplayCompact,
		MaxLines:       20,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: false,
	}

	// Category B: Write/Modify Tools - ALWAYS show full output
	// These tools modify files/state, users need to see exactly what changed

	settings.ToolConfigs["Write"] = &ToolRenderConfig{
		Name:           "Write",
		DisplayMode:    DisplayCompact,
		MaxLines:       0, // Ignored due to AlwaysShowFull
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: true, // ← ALWAYS show complete output
	}
	settings.ToolConfigs["file_write"] = &ToolRenderConfig{
		Name:           "file_write",
		DisplayMode:    DisplayCompact,
		MaxLines:       0,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: true,
	}

	settings.ToolConfigs["Edit"] = &ToolRenderConfig{
		Name:           "Edit",
		DisplayMode:    DisplayCompact,
		MaxLines:       0,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: true,
	}
	settings.ToolConfigs["ApplyPatch"] = &ToolRenderConfig{
		Name:           "ApplyPatch",
		DisplayMode:    DisplayCompact,
		MaxLines:       0,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: true,
	}
	settings.ToolConfigs["apply_patch"] = &ToolRenderConfig{
		Name:           "apply_patch",
		DisplayMode:    DisplayCompact,
		MaxLines:       0,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: true,
	}
	settings.ToolConfigs["str_replace"] = &ToolRenderConfig{
		Name:           "str_replace",
		DisplayMode:    DisplayCompact,
		MaxLines:       0,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: true,
	}
	settings.ToolConfigs["file_undo"] = &ToolRenderConfig{
		Name:           "file_undo",
		DisplayMode:    DisplayCompact,
		MaxLines:       0,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: true,
	}

	settings.ToolConfigs["MultiEdit"] = &ToolRenderConfig{
		Name:           "MultiEdit",
		DisplayMode:    DisplayCompact,
		MaxLines:       0,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: true,
	}

	settings.ToolConfigs["TodoWrite"] = &ToolRenderConfig{
		Name:           "TodoWrite",
		DisplayMode:    DisplayCompact,
		MaxLines:       0,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: true,
	}
	settings.ToolConfigs["TaskManage"] = &ToolRenderConfig{
		Name:        "TaskManage",
		DisplayMode: DisplayCompact,
		MaxLines:    0,
		// TaskManage inputs contain internal operation keys and orchestration
		// details. The task renderer presents the resulting task state instead.
		ShowParams:     false,
		Color:          "",
		AlwaysShowFull: true,
	}
	settings.ToolConfigs["NotebookEdit"] = &ToolRenderConfig{
		Name:           "NotebookEdit",
		DisplayMode:    DisplayCompact,
		MaxLines:       0,
		ShowParams:     true,
		Color:          "",
		AlwaysShowFull: true,
	}

	return settings
}

// GetDefaultCacheStats returns default cache statistics
func GetDefaultCacheStats() *CacheStats {
	now := time.Now()
	return &CacheStats{
		SessionCreationTokens:     0,
		SessionReadTokens:         0,
		SessionHitRate:            0.0,
		SessionStartTime:          now,
		TotalRequests:             0,
		TotalCreationTokens:       0,
		TotalReadTokens:           0,
		TotalHitRate:              0.0,
		TotalInputTokens:          0,
		TotalOutputTokens:         0,
		SystemPromptInvalidations: 0,
		ToolInvalidations:         0,
		MCPInvalidations:          0,
		ModelStats:                make(map[string]*ModelCacheStats),
		AverageHitRate:            0.0,
		TotalTokensSaved:          0,
		TotalCostSaved:            0.0,
		LastUpdated:               now,
		FirstRecorded:             now,
	}
}

// LoadCacheStats loads persistent cache statistics
func LoadCacheStats() (*CacheStats, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return GetDefaultCacheStats(), err
	}

	filePath := filepath.Join(home, ".swarmos", "cache_stats.json")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return GetDefaultCacheStats(), nil
		}
		return GetDefaultCacheStats(), err
	}

	var stats CacheStats
	if err := json.Unmarshal(data, &stats); err != nil {
		return GetDefaultCacheStats(), err
	}

	// Initialize session stats
	stats.SessionCreationTokens = 0
	stats.SessionReadTokens = 0
	stats.SessionHitRate = 0.0
	stats.SessionStartTime = time.Now()

	// Ensure model stats map is initialized
	if stats.ModelStats == nil {
		stats.ModelStats = make(map[string]*ModelCacheStats)
	}

	return &stats, nil
}

// SaveCacheStats saves persistent cache statistics
func SaveCacheStats(stats *CacheStats) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	configDir := filepath.Join(home, ".swarmos")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	stats.LastUpdated = time.Now()

	filePath := filepath.Join(configDir, "cache_stats.json")
	data, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filePath, data, 0644)
}

// UpdateModelStats updates statistics for a specific model
func (cs *CacheStats) UpdateModelStats(provider, modelName string, creationTokens, readTokens, inputTokens, outputTokens int64, latencyMs float64) {
	key := provider + "/" + modelName

	if cs.ModelStats == nil {
		cs.ModelStats = make(map[string]*ModelCacheStats)
	}

	stats, exists := cs.ModelStats[key]
	if !exists {
		stats = &ModelCacheStats{
			ModelName: modelName,
			Provider:  provider,
		}
		cs.ModelStats[key] = stats
	}

	stats.TotalRequests++
	stats.CreationTokens += creationTokens
	stats.ReadTokens += readTokens
	stats.InputTokens += inputTokens
	stats.OutputTokens += outputTokens
	stats.LastUsed = time.Now()

	// Update cache hits/misses
	if readTokens > 0 {
		stats.CacheHits++
	} else {
		stats.CacheMisses++
	}

	// Calculate hit rate
	totalCacheAttempts := stats.CacheHits + stats.CacheMisses
	if totalCacheAttempts > 0 {
		stats.HitRate = float64(stats.CacheHits) / float64(totalCacheAttempts)
	}

	// Update average latency (exponential moving average)
	if stats.AverageLatency == 0 {
		stats.AverageLatency = latencyMs
	} else {
		stats.AverageLatency = 0.9*stats.AverageLatency + 0.1*latencyMs
	}

	// Calculate tokens saved (read tokens are cached, so they're "saved")
	stats.TokensSaved = stats.ReadTokens

	// Estimate cost saved (rough estimate: $3/1M input tokens for Claude)
	stats.CostSaved = float64(stats.TokensSaved) / 1000000.0 * 3.0
}

// GetTopModels returns the top N models by usage
func (cs *CacheStats) GetTopModels(n int) []*ModelCacheStats {
	models := make([]*ModelCacheStats, 0, len(cs.ModelStats))
	for _, stats := range cs.ModelStats {
		models = append(models, stats)
	}

	// Sort by total requests (descending)
	sort.Slice(models, func(i, j int) bool {
		return models[i].TotalRequests > models[j].TotalRequests
	})

	if len(models) > n {
		return models[:n]
	}
	return models
}

// ResetSessionStats resets only the session statistics
func (cs *CacheStats) ResetSessionStats() {
	cs.SessionCreationTokens = 0
	cs.SessionReadTokens = 0
	cs.SessionHitRate = 0.0
	cs.SessionStartTime = time.Now()
}

// ResetAllStats resets all statistics (including per-model)
func (cs *CacheStats) ResetAllStats() {
	*cs = *GetDefaultCacheStats()
}

// getToolConfigInternal returns the rendering config for a tool (or default) - internal use only
func (rs *RenderSettings) getToolConfigInternal(toolName string) *ToolRenderConfig {
	if config, ok := rs.ToolConfigs[toolName]; ok {
		return config
	}

	// Return default config
	return &ToolRenderConfig{
		Name:        toolName,
		DisplayMode: rs.DefaultMode,
		MaxLines:    rs.DefaultMaxLines,
		ShowParams:  true,
		Color:       "",
	}
}

// GetToolConfig returns the rendering config for a tool (satisfies commands.RenderSettings interface)
// Returns as interface to satisfy the commands.RenderSettings contract
func (rs *RenderSettings) GetToolConfig(toolName string) interface {
	GetName() string
	GetDisplayMode() string
	GetMaxLines() int
	GetShowParams() bool
} {
	return rs.getToolConfigInternal(toolName)
}

// GetDisplayMode returns the display mode for a tool as a string
func (rs *RenderSettings) GetDisplayMode(toolName string) string {
	return string(rs.getToolConfigInternal(toolName).DisplayMode)
}

// GetDisplayModeEnum returns the display mode for a tool as ToolDisplayMode enum
func (rs *RenderSettings) GetDisplayModeEnum(toolName string) ToolDisplayMode {
	return rs.getToolConfigInternal(toolName).DisplayMode
}

// GetMaxLines returns the max lines for a tool in compact mode
func (rs *RenderSettings) GetMaxLines(toolName string) int {
	maxLines := rs.getToolConfigInternal(toolName).MaxLines
	if maxLines <= 0 {
		return rs.DefaultMaxLines
	}
	return maxLines
}

// ShouldShowParams returns whether to show parameters for a tool
func (rs *RenderSettings) ShouldShowParams(toolName string) bool {
	return rs.getToolConfigInternal(toolName).ShowParams
}

// GetToolColor returns the color for a tool's output
func (rs *RenderSettings) GetToolColor(toolName string) string {
	color := rs.getToolConfigInternal(toolName).Color
	if color == "" {
		return rs.Colors.ToolOutput
	}
	return color
}

// ShouldTruncate determines if a tool's output should be truncated based on:
// 1. Global verbose mode (ShowFullToolOutput) - if true, never truncate
// 2. Tool-specific AlwaysShowFull flag - if true, never truncate
// 3. Display mode - only truncate in compact mode
func (rs *RenderSettings) ShouldTruncate(toolName string) bool {
	// Global verbose mode: never truncate
	if rs.ShowFullToolOutput {
		return false
	}

	config := rs.getToolConfigInternal(toolName)

	// Tool marked as always full: never truncate
	// This is for write tools (Write, Edit, ApplyPatch) that must show complete output
	if config.AlwaysShowFull {
		return false
	}

	// Only truncate in compact mode
	return config.DisplayMode == DisplayCompact
}

// SetToolMode sets the display mode for a tool
func (rs *RenderSettings) SetToolMode(toolName, mode string) {
	config := rs.getToolConfigInternal(toolName)
	if _, ok := rs.ToolConfigs[toolName]; !ok {
		// Create new config
		rs.ToolConfigs[toolName] = &ToolRenderConfig{
			Name:           toolName,
			DisplayMode:    ToolDisplayMode(mode),
			MaxLines:       config.MaxLines,
			ShowParams:     config.ShowParams,
			Color:          config.Color,
			AlwaysShowFull: config.AlwaysShowFull,
		}
	} else {
		rs.ToolConfigs[toolName].DisplayMode = ToolDisplayMode(mode)
	}
}

// SetToolMaxLines sets the max lines for a tool
func (rs *RenderSettings) SetToolMaxLines(toolName string, maxLines int) {
	config := rs.getToolConfigInternal(toolName)
	if _, ok := rs.ToolConfigs[toolName]; !ok {
		// Create new config
		rs.ToolConfigs[toolName] = &ToolRenderConfig{
			Name:           toolName,
			DisplayMode:    config.DisplayMode,
			MaxLines:       maxLines,
			ShowParams:     config.ShowParams,
			Color:          config.Color,
			AlwaysShowFull: config.AlwaysShowFull,
		}
	} else {
		rs.ToolConfigs[toolName].MaxLines = maxLines
	}
}

// SetThinkingDisplay sets whether to show thinking blocks
func (rs *RenderSettings) SetThinkingDisplay(show bool) {
	rs.ShowThinking = show
}

// SetColorScheme sets the color scheme
func (rs *RenderSettings) SetColorScheme(toolCall, toolOutput, toolError, connector string) {
	rs.Colors.ToolCall = toolCall
	rs.Colors.ToolOutput = toolOutput
	rs.Colors.ToolError = toolError
	rs.Colors.Connector = connector
}

// GetShowThinking returns whether to show thinking blocks
func (rs *RenderSettings) GetShowThinking() bool {
	return rs.ShowThinking
}

// GetShowHooks returns whether to show hook executions
func (rs *RenderSettings) GetShowHooks() bool {
	return rs.ShowHooks
}

// SetShowHooks sets whether to show hook executions
func (rs *RenderSettings) SetShowHooks(show bool) {
	rs.ShowHooks = show
}

// GetAllTools returns a sorted list of all configured tools
func (rs *RenderSettings) GetAllTools() []string {
	tools := make([]string, 0, len(rs.ToolConfigs))
	for tool := range rs.ToolConfigs {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	return tools
}

// Reset resets settings to defaults
func (rs *RenderSettings) Reset() {
	defaults := NewDefaultRenderSettings()
	*rs = *defaults
}

// Save persists settings to disk
func (rs *RenderSettings) Save() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}

	swarmDir := filepath.Join(configDir, "swarmos")
	if err := os.MkdirAll(swarmDir, 0755); err != nil {
		return err
	}

	path := filepath.Join(swarmDir, "render_settings.json")
	data, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// Load reads settings from disk
func LoadRenderSettings() (*RenderSettings, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return NewDefaultRenderSettings(), nil
	}

	path := filepath.Join(configDir, "swarmos", "render_settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		// File doesn't exist, return defaults
		return NewDefaultRenderSettings(), nil
	}

	// Start from defaults so new fields (like ShowHooks) get their default
	// values even when loading an older config that lacks them.
	settings := NewDefaultRenderSettings()
	if err := json.Unmarshal(data, settings); err != nil {
		return NewDefaultRenderSettings(), err
	}

	return settings, nil
}

// SaveRenderSettings persists settings to disk (standalone function)
func SaveRenderSettings(settings *RenderSettings) error {
	return settings.Save()
}
