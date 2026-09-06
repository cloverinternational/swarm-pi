package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/configformat"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	sdkbuiltin "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/atotto/clipboard"
)

// CustomAgentEntry represents a custom agent configuration
type CustomAgentEntry struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Description  string             `json:"description,omitempty"`
	Provider     string             `json:"provider"`
	Model        string             `json:"model"`
	SystemPrompt string             `json:"system_prompt,omitempty"`
	Tools        []string           `json:"tools,omitempty"`
	Hooks        []string           `json:"hooks,omitempty"`
	Capabilities *AgentCapabilities `json:"capabilities,omitempty"`
	Builtin      bool               `json:"builtin"`
	Color        string             `json:"color,omitempty"`
	Icon         string             `json:"icon,omitempty"`

	// ProfileID optionally pins this agent to a specific model profile.
	// If empty, the globally active profile is used.
	// Example: "quality", "performance", "balanced"
	ProfileID string `json:"profile_id,omitempty"`

	// RoleAlias optionally designates which role alias this agent fills
	// within the profile system (e.g. "main", "sub_agent", "steering").
	// Used when this agent is selected as the active agent for a session.
	RoleAlias string `json:"role_alias,omitempty"`

	// Disabled marks this agent as inactive — it will not appear in agent
	// selection dropdowns but remains in the config for easy re-enabling.
	// Built-in agents can also be disabled.
	Disabled bool `json:"disabled,omitempty"`
}

// CustomAgentConfig stores all custom agents
type CustomAgentConfig struct {
	DefaultAgent string             `json:"default_agent"`
	Agents       []CustomAgentEntry `json:"agents"`
}

// AgentToolInfo represents information about an available tool for agent configuration
type AgentToolInfo struct {
	Name        string
	Description string
	Category    string // "builtin", "mcp", "custom"
}

// AgentHookInfo represents information about an available hook for agent configuration
type AgentHookInfo struct {
	Name        string
	Description string
	Type        string // "event", "shell", "builtin"
	Source      string // Where the hook comes from
}

// AgentsSettings manages custom agent configurations
type AgentsSettings struct {
	config          CustomAgentConfig
	configPath      string
	availableTools  []AgentToolInfo
	availableHooks  []AgentHookInfo
	onAgentChange   func(string) error  // Callback when default agent changes
	providers       []commands.Provider // Available providers from config
	providerNames   []string            // List of provider names for dropdown
	availableModels map[string][]string // Models per provider (provider_name -> []model_id)

	// profileManager is used to list available profiles for the ProfileID dropdown.
	// Set via SetProfileManager(). May be nil if profiles are not configured.
	profileManager *ProfileManager

	// AI assistant chat
	onChatSend         func(message string) // Legacy callback (simple string response)
	onChatSendDetailed AgentChatCallback    // Detailed callback with tool calls
	chatMessages       []AgentChatMessage   // Chat history
}

// AgentChatCallback is the callback type for detailed agent responses
type AgentChatCallback func(message string) (*AgentChatResponse, error)

// AgentChatResponse contains the full response from the agents assistant
type AgentChatResponse struct {
	Content    string
	ToolCalls  []ToolCallDisplay
	TotalTurns int
	Error      string
}

// AgentChatMessage represents a message in the agents chat
type AgentChatMessage struct {
	Role      string            // "user", "assistant", "system", "tool"
	Content   string            // Main text content
	ToolCalls []ToolCallDisplay // Tool calls (for assistant messages)
	Turns     int               // Number of LLM turns (for assistant messages)
}

// ToolCallDisplay represents a tool call for UI display (reused from hooks)
// Already defined in hooks.go, but we reference it here for clarity

// Built-in agent definitions
var builtinAgents = []CustomAgentEntry{
	{
		ID:           "general-assistant",
		Name:         "General Assistant",
		Description:  "Multipurpose AI assistant for general tasks",
		SystemPrompt: "You are a helpful AI assistant. You help users with a variety of tasks including answering questions, writing code, analyzing data, and solving problems.",
		Tools:        []string{"*"},
		Hooks:        []string{},
		Capabilities: &AgentCapabilities{
			MaxTokens:   0, // Use default/auto-compact naturally
			Temperature: 0.7,
			MaxTurns:    0, // Unlimited
			Timeout:     900,
		},
		Builtin:   true,
		RoleAlias: "main", // Primary agent role
		Color:     "#6366F1",
		Icon:      "🤖",
	},
	{
		ID:           "code-reviewer",
		Name:         "Code Reviewer",
		Description:  "Specialized agent for code review and analysis",
		SystemPrompt: "You are an expert code reviewer. Analyze code for bugs, security issues, performance problems, and style violations. Provide constructive feedback with specific suggestions for improvement.",
		Tools:        []string{"file_read", "grep", "list_dir"},
		Hooks:        []string{},
		Capabilities: &AgentCapabilities{
			MaxTokens:   0, // Use default/auto-compact naturally
			Temperature: 0.3,
			MaxTurns:    0, // Unlimited
			Timeout:     180,
		},
		Builtin:   true,
		RoleAlias: "inference", // Code analysis requires strong inference
		Color:     "#10B981",
		Icon:      "🔍",
	},
	{
		ID:           "research-agent",
		Name:         "Research Agent",
		Description:  "Focused on exploration, search, and information gathering",
		SystemPrompt: "You are a research agent. Your job is to explore codebases, search for information, and gather context. Be thorough in your exploration and provide comprehensive summaries of what you find.",
		Tools:        []string{"file_read", "grep", "list_dir", "bash"},
		Hooks:        []string{},
		Capabilities: &AgentCapabilities{
			MaxTokens:   0, // Use default/auto-compact naturally
			Temperature: 0.5,
			MaxTurns:    0, // Unlimited
			Timeout:     600,
		},
		Builtin:   true,
		RoleAlias: "longcontext", // Research benefits from extended context
		Color:     "#F59E0B",
		Icon:      "🔬",
	},
	{
		ID:           "background-worker",
		Name:         "Background Worker",
		Description:  "Long-running task executor for async operations",
		SystemPrompt: "You are a background worker agent. Execute long-running tasks efficiently. Report progress periodically and handle errors gracefully.",
		Tools:        []string{"*"},
		Hooks:        []string{},
		Capabilities: &AgentCapabilities{
			MaxTokens:   0, // Use default/auto-compact naturally
			Temperature: 0.5,
			MaxTurns:    0, // Unlimited
			Timeout:     1800,
		},
		Builtin:   true,
		RoleAlias: "background", // Designed for background execution
		Color:     "#8B5CF6",
		Icon:      "⚙️",
	},
}

// NewAgentsSettings creates a new agents settings manager
func NewAgentsSettings() *AgentsSettings {
	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, ".swarmos", "custom_agents.json")

	s := &AgentsSettings{
		configPath:      configPath,
		availableTools:  []AgentToolInfo{},
		availableHooks:  []AgentHookInfo{},
		availableModels: make(map[string][]string),
		chatMessages:    []AgentChatMessage{},
	}

	// Load providers from config
	s.loadProvidersAndModels()

	// Load config or create with defaults
	if err := s.load(); err != nil {
		// Initialize with built-in agents
		s.config = CustomAgentConfig{
			DefaultAgent: "general-assistant",
			Agents:       builtinAgents,
		}
		if err := s.save(); err != nil {
			logDebug("Failed to save default agents config: %v", err)
		}
	} else {
		// Sync built-in agents
		s.syncBuiltinAgents()
	}

	return s
}

// loadProvidersAndModels loads available providers and models from config
func (s *AgentsSettings) loadProvidersAndModels() {
	cm, err := commands.NewConfigManager()
	if err != nil {
		// Fallback to default providers
		s.providerNames = []string{"anthropic", "openai"}
		s.availableModels["anthropic"] = []string{"claude-sonnet-4-20250514", "claude-opus-4-20241113"}
		s.availableModels["openai"] = []string{"gpt-4", "gpt-4-turbo", "gpt-3.5-turbo"}
		return
	}

	providerConfigs, err := cm.LoadProviders()
	if err != nil {
		// Fallback to default providers
		s.providerNames = []string{"anthropic", "openai"}
		s.availableModels["anthropic"] = []string{"claude-sonnet-4-20250514", "claude-opus-4-20241113"}
		s.availableModels["openai"] = []string{"gpt-4", "gpt-4-turbo", "gpt-3.5-turbo"}
		return
	}

	// Convert to providers list
	s.providers = make([]commands.Provider, 0, len(providerConfigs))
	s.providerNames = make([]string, 0, len(providerConfigs))

	for _, pc := range providerConfigs {
		models := make([]commands.ModelInfo, len(pc.Models))
		modelIDs := make([]string, len(pc.Models))
		for j, mc := range pc.Models {
			models[j] = commands.ModelInfo{
				ID:            mc.ID,
				DisplayName:   mc.DisplayName,
				Context:       mc.Context,
				ContextWindow: mc.ContextWindow,
			}
			modelIDs[j] = mc.ID
		}

		provider := commands.Provider{
			Name:        pc.Name,
			DisplayName: pc.DisplayName,
			Color:       pc.Color,
			Type:        pc.Type,
			APIType:     pc.APIType,
			BaseURL:     pc.BaseURL,
			Source:      pc.Source,
			Models:      models,
			Available:   pc.Available,
		}

		s.providers = append(s.providers, provider)
		s.providerNames = append(s.providerNames, pc.Name)
		s.availableModels[pc.Name] = modelIDs
	}

	// Fallback if no providers loaded
	if len(s.providerNames) == 0 {
		s.providerNames = []string{"anthropic", "openai"}
		s.availableModels["anthropic"] = []string{"claude-sonnet-4-20250514", "claude-opus-4-20241113"}
		s.availableModels["openai"] = []string{"gpt-4", "gpt-4-turbo", "gpt-3.5-turbo"}
	}
}

// syncBuiltinAgents ensures all built-in agents exist in the config
func (s *AgentsSettings) syncBuiltinAgents() {
	changed := false
	for _, builtin := range builtinAgents {
		found := false
		for i, existing := range s.config.Agents {
			if existing.ID == builtin.ID {
				found = true
				// Update built-in agents to latest version, but preserve disabled status
				if existing.Builtin {
					disabled := existing.Disabled
					s.config.Agents[i] = builtin
					s.config.Agents[i].Disabled = disabled
					changed = true
				}
				break
			}
		}
		if !found {
			s.config.Agents = append(s.config.Agents, builtin)
			changed = true
		}
	}
	if changed {
		if err := s.save(); err != nil {
			logDebug("Failed to sync built-in agents: %v", err)
		}
	}
}

// load loads the config from disk. Resolves custom_agents.yaml -> .yml ->
// .json (YAML default; legacy JSON still read transparently).
func (s *AgentsSettings) load() error {
	dir := filepath.Dir(s.configPath)
	_, err := configformat.Load(dir, "custom_agents", &s.config)
	return err
}

// save saves the config to disk. Writes custom_agents.yaml (canonical);
// any pre-existing custom_agents.json is left shadowed for rollback.
func (s *AgentsSettings) save() error {
	dir := filepath.Dir(s.configPath)
	_, err := configformat.Save(dir, "custom_agents", s.config, 0644)
	return err
}

// GetAgents returns all agents
func (s *AgentsSettings) GetAgents() []CustomAgentEntry {
	return s.config.Agents
}

// GetAgent returns an agent by ID
func (s *AgentsSettings) GetAgent(id string) *CustomAgentEntry {
	for i := range s.config.Agents {
		if s.config.Agents[i].ID == id {
			return &s.config.Agents[i]
		}
	}
	return nil
}

// GetDefaultAgentID returns the default agent ID
func (s *AgentsSettings) GetDefaultAgentID() string {
	return s.config.DefaultAgent
}

// SetDefaultAgent sets the default agent by ID
func (s *AgentsSettings) SetDefaultAgent(id string) error {
	// Verify agent exists
	found := false
	for _, a := range s.config.Agents {
		if a.ID == id {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("agent '%s' not found", id)
	}

	s.config.DefaultAgent = id
	if err := s.save(); err != nil {
		return err
	}

	if s.onAgentChange != nil {
		return s.onAgentChange(id)
	}
	return nil
}

// CreateAgent creates a new custom agent
func (s *AgentsSettings) CreateAgent(agent CustomAgentEntry) error {
	// Validate ID
	if agent.ID == "" {
		return fmt.Errorf("agent ID is required")
	}
	if !isValidAgentID(agent.ID) {
		return fmt.Errorf("invalid agent ID: must be lowercase alphanumeric with hyphens")
	}

	// Check if ID already exists
	for _, a := range s.config.Agents {
		if a.ID == agent.ID {
			return fmt.Errorf("agent '%s' already exists", agent.ID)
		}
	}

	// Set defaults
	agent.Builtin = false
	if agent.Capabilities == nil {
		agent.Capabilities = &AgentCapabilities{
			MaxTokens:   0, // Use default/auto-compact naturally
			Temperature: 0.7,
			MaxTurns:    0, // Unlimited
			Timeout:     900,
		}
	}

	s.config.Agents = append(s.config.Agents, agent)
	return s.save()
}

// UpdateAgent updates an existing custom or built-in agent
func (s *AgentsSettings) UpdateAgent(id string, agent CustomAgentEntry) error {
	for i, a := range s.config.Agents {
		if a.ID == id {
			// Preserve builtin status
			agent.Builtin = a.Builtin
			s.config.Agents[i] = agent

			// Update default agent ID if it was renamed
			if s.config.DefaultAgent == id && agent.ID != id {
				s.config.DefaultAgent = agent.ID
			}

			return s.save()
		}
	}
	return fmt.Errorf("agent '%s' not found", id)
}

// DeleteAgent deletes a custom or built-in agent
func (s *AgentsSettings) DeleteAgent(id string) error {
	for i, a := range s.config.Agents {
		if a.ID == id {
			s.config.Agents = append(s.config.Agents[:i], s.config.Agents[i+1:]...)

			// If this was default, switch to first agent
			if s.config.DefaultAgent == id {
				if len(s.config.Agents) > 0 {
					s.config.DefaultAgent = s.config.Agents[0].ID
				}
			}

			return s.save()
		}
	}
	return fmt.Errorf("agent '%s' not found", id)
}

// CloneAgent creates a copy of an existing agent
func (s *AgentsSettings) CloneAgent(id string, newID string) error {
	original := s.GetAgent(id)
	if original == nil {
		return fmt.Errorf("agent '%s' not found", id)
	}

	clone := *original
	clone.ID = newID
	clone.Name = original.Name + " (Copy)"
	clone.Builtin = false

	// Deep copy capabilities
	if original.Capabilities != nil {
		clone.Capabilities = &AgentCapabilities{
			MaxTokens:   original.Capabilities.MaxTokens,
			Temperature: original.Capabilities.Temperature,
			MaxTurns:    original.Capabilities.MaxTurns,
			Timeout:     original.Capabilities.Timeout,
		}
	}

	// Deep copy slices
	if len(original.Tools) > 0 {
		clone.Tools = make([]string, len(original.Tools))
		copy(clone.Tools, original.Tools)
	}
	if len(original.Hooks) > 0 {
		clone.Hooks = make([]string, len(original.Hooks))
		copy(clone.Hooks, original.Hooks)
	}

	return s.CreateAgent(clone)
}

// ToggleAgentDisabled flips the Disabled flag on any agent (including built-ins).
// Disabled agents are hidden from selection dropdowns but kept in the config.
func (s *AgentsSettings) ToggleAgentDisabled(id string) error {
	for i := range s.config.Agents {
		if s.config.Agents[i].ID == id {
			s.config.Agents[i].Disabled = !s.config.Agents[i].Disabled
			return s.save()
		}
	}
	return fmt.Errorf("agent '%s' not found", id)
}

// UpsertCustomAgent creates-or-updates a custom agent definition from a constructor
// entry emitted by the agent_constructor subagent. This is the persistence
// callback wired via SubagentTool.SetAgentWriteCallback at startup.
//
// Rules:
//   - If an agent with the same ID already exists and is NOT builtin, it is updated.
//   - If no agent with that ID exists, a new custom agent is appended.
//   - Built-in agents cannot be overwritten via this path (use UpdateAgent instead).
func (s *AgentsSettings) UpsertCustomAgent(entry sdkbuiltin.AgentConstructorEntry) error {
	if entry.ID == "" {
		return fmt.Errorf("agent constructor: entry.id is required")
	}
	if entry.Name == "" {
		return fmt.Errorf("agent constructor: entry.name is required")
	}
	if entry.SystemPrompt == "" {
		return fmt.Errorf("agent constructor: entry.system_prompt is required")
	}

	for i, a := range s.config.Agents {
		if a.ID == entry.ID {
			if a.Builtin {
				return fmt.Errorf("agent constructor: cannot overwrite builtin agent '%s' via constructor", entry.ID)
			}
			// Update mutable fields only; preserve provider/model/color/icon/capabilities
			s.config.Agents[i].Name = entry.Name
			s.config.Agents[i].SystemPrompt = entry.SystemPrompt
			if entry.Description != "" {
				s.config.Agents[i].Description = entry.Description
			}
			if entry.ProfileID != "" {
				s.config.Agents[i].ProfileID = entry.ProfileID
			}
			if entry.RoleAlias != "" {
				s.config.Agents[i].RoleAlias = entry.RoleAlias
			}
			if len(entry.Tools) > 0 {
				s.config.Agents[i].Tools = entry.Tools
			}
			return s.save()
		}
	}

	// Not found — create new custom agent with sensible defaults
	newEntry := CustomAgentEntry{
		ID:           entry.ID,
		Name:         entry.Name,
		SystemPrompt: entry.SystemPrompt,
		Description:  entry.Description,
		ProfileID:    entry.ProfileID,
		RoleAlias:    entry.RoleAlias,
		Tools:        entry.Tools,
		Builtin:      false,
		Color:        "#7c4dff",
		Icon:         "★",
		Capabilities: &AgentCapabilities{
			MaxTokens:   0, // Use default/auto-compact naturally
			Temperature: 0.7,
			MaxTurns:    0,
			Timeout:     900,
		},
	}
	if len(newEntry.Tools) == 0 {
		newEntry.Tools = []string{"*"}
	}
	s.config.Agents = append(s.config.Agents, newEntry)
	return s.save()
}

// DeleteCustomAgent removes a custom agent by ID. Built-in agents are protected.
// This is the delete callback wired via SubagentTool.SetAgentWriteCallback.
func (s *AgentsSettings) DeleteCustomAgent(id string) error {
	for i, a := range s.config.Agents {
		if a.ID == id {
			if a.Builtin {
				return fmt.Errorf("agent constructor: cannot delete builtin agent '%s'", id)
			}
			s.config.Agents = append(s.config.Agents[:i], s.config.Agents[i+1:]...)
			if s.config.DefaultAgent == id && len(s.config.Agents) > 0 {
				s.config.DefaultAgent = s.config.Agents[0].ID
			}
			return s.save()
		}
	}
	return fmt.Errorf("agent '%s' not found", id)
}

// GetConstructorContext returns structured context for the agent_constructor:
//   - agentsJSON: JSON-marshalled slice of all non-builtin agent entries
//   - profileIDs:  available profile IDs from the profile manager
//   - roleAliases: valid role alias strings from the fallback system
//
// This is the context provider wired via SubagentTool.SetAgentContextProvider.
func (s *AgentsSettings) GetConstructorContext() (agentsJSON []byte, profileIDs []string, roleAliases []string) {
	// Only expose custom agents to the constructor — builtin agents are read-only
	// and the constructor doesn't need to know about them to make minimal diffs.
	custom := make([]CustomAgentEntry, 0)
	for _, a := range s.config.Agents {
		if !a.Builtin {
			custom = append(custom, a)
		}
	}
	agentsJSON, _ = json.Marshal(custom)

	profileIDs = s.GetAvailableProfileIDs()

	// Standard role aliases in the fallback system
	roleAliases = []string{
		"main", "sub_agent", "steering", "inference",
		"longcontext", "background", "vision", "code",
	}
	return agentsJSON, profileIDs, roleAliases
}

// SetAvailableTools sets the list of available tools
func (s *AgentsSettings) SetAvailableTools(tools []AgentToolInfo) {
	s.availableTools = tools
}

// GetAvailableTools returns the list of available tools
func (s *AgentsSettings) GetAvailableTools() []AgentToolInfo {
	return s.availableTools
}

// SetAvailableHooks sets the list of available hooks
func (s *AgentsSettings) SetAvailableHooks(hooks []AgentHookInfo) {
	s.availableHooks = hooks
}

// GetAvailableHooks returns the list of available hooks
func (s *AgentsSettings) GetAvailableHooks() []AgentHookInfo {
	return s.availableHooks
}

// SetOnAgentChange sets the callback for when default agent changes
func (s *AgentsSettings) SetOnAgentChange(callback func(string) error) {
	s.onAgentChange = callback
}

// SetProfileManager wires in the profile manager so the agent form
// can display available profiles in the ProfileID dropdown.
func (s *AgentsSettings) SetProfileManager(pm *ProfileManager) {
	s.profileManager = pm
}

// GetAvailableProfileIDs returns ["(use active profile)", "balanced", "quality", ...]
// for use in the ProfileID dropdown in the agent editor.
func (s *AgentsSettings) GetAvailableProfileIDs() []string {
	ids := []string{"(use active profile)"}
	if s.profileManager != nil {
		for _, p := range s.profileManager.ListProfiles() {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

// ToSDKDefinition converts a CustomAgentEntry to an SDK agent.Definition
func (e *CustomAgentEntry) ToSDKDefinition() *agent.Definition {
	def := &agent.Definition{
		ID:           e.ID,
		Name:         e.Name,
		Description:  e.Description,
		Provider:     e.Provider,
		Model:        e.Model,
		SystemPrompt: e.SystemPrompt,
		ToolHints:    e.Tools,
	}

	if e.Capabilities != nil {
		def.Capabilities = &agent.Capabilities{
			MaxTokens:         e.Capabilities.MaxTokens,
			Temperature:       e.Capabilities.Temperature,
			MaxTurns:          e.Capabilities.MaxTurns,
			Timeout:           time.Duration(e.Capabilities.Timeout) * time.Second,
			SupportsTools:     true,
			SupportsStreaming: true,
		}
	}

	// Store hooks in metadata for later processing
	if len(e.Hooks) > 0 {
		if def.Metadata == nil {
			def.Metadata = make(map[string]any)
		}
		def.Metadata["hooks"] = e.Hooks
	}

	// Store color and icon in metadata
	if e.Color != "" || e.Icon != "" {
		if def.Metadata == nil {
			def.Metadata = make(map[string]any)
		}
		if e.Color != "" {
			def.Metadata["color"] = e.Color
		}
		if e.Icon != "" {
			def.Metadata["icon"] = e.Icon
		}
	}

	return def
}

// GetAllSDKDefinitions returns all agent definitions converted to SDK format
func (s *AgentsSettings) GetAllSDKDefinitions() []*agent.Definition {
	defs := make([]*agent.Definition, 0, len(s.config.Agents))
	for i := range s.config.Agents {
		defs = append(defs, s.config.Agents[i].ToSDKDefinition())
	}
	return defs
}

// GetSDKDefinition returns a specific agent definition in SDK format
func (s *AgentsSettings) GetSDKDefinition(id string) *agent.Definition {
	agent := s.GetAgent(id)
	if agent == nil {
		return nil
	}
	return agent.ToSDKDefinition()
}

// GetDefaultSDKDefinition returns the default agent definition in SDK format
func (s *AgentsSettings) GetDefaultSDKDefinition() *agent.Definition {
	return s.GetSDKDefinition(s.config.DefaultAgent)
}

// isValidAgentID checks if an agent ID is valid
func isValidAgentID(id string) bool {
	matched, _ := regexp.MatchString("^[a-z0-9][a-z0-9-]*[a-z0-9]$|^[a-z0-9]$", id)
	return matched
}

// Render renders the agents settings UI
func (s *AgentsSettings) HandleKey(key string, state *State) bool {
	switch state.AgentsState {
	case "list":
		return s.handleListKey(key, state)
	case "action_menu":
		return s.handleActionMenuKey(key, state)
	case "create", "edit":
		return s.handleFormKey(key, state)
	case "preview":
		return s.handlePreviewKey(key, state)
	case "chat":
		return s.handleChatKey(key, state)
	}
	return false
}

func (s *AgentsSettings) getVisualIndices() []int {
	var builtins []int
	var custom []int
	for i, a := range s.config.Agents {
		if a.Builtin {
			builtins = append(builtins, i)
		} else {
			custom = append(custom, i)
		}
	}
	// -1 is "New Agent" button at the top
	res := []int{-1}
	res = append(res, builtins...)
	res = append(res, custom...)
	return res
}

// handleListKey handles keyboard input in list view
func (s *AgentsSettings) handleListKey(key string, state *State) bool {
	visualOrder := s.getVisualIndices()
	currPos := -1
	for i, v := range visualOrder {
		if v == state.AgentsSelected {
			currPos = i
			break
		}
	}

	switch key {
	case "up", "k":
		if currPos > 0 {
			state.AgentsSelected = visualOrder[currPos-1]
		}
		return true

	case "down", "j":
		if currPos < len(visualOrder)-1 {
			state.AgentsSelected = visualOrder[currPos+1]
		} else if currPos == -1 && len(visualOrder) > 0 {
			// Fallback if somehow selection is lost
			state.AgentsSelected = visualOrder[0]
		}
		return true

	case "a":
		// Open AI assistant chat
		state.AgentsState = "chat"
		state.AgentsChatInput = ""
		state.AgentsChatScrollOffset = 0
		return true

	case "n":
		// Create new agent
		state.AgentsState = "create"
		s.resetFormState(state)
		return true

	case "enter", " ":
		if state.AgentsSelected == -1 {
			// "New Agent" selected
			state.AgentsState = "create"
			s.resetFormState(state)
		} else if state.AgentsSelected >= 0 && state.AgentsSelected < len(s.config.Agents) {
			// Agent selected - show action menu
			state.AgentsState = "action_menu"
			state.AgentsActionChoice = 0
		}
		return true

	case "d":
		// Quick set as default
		if state.AgentsSelected >= 0 && state.AgentsSelected < len(s.config.Agents) {
			agent := s.config.Agents[state.AgentsSelected]
			if err := s.SetDefaultAgent(agent.ID); err != nil {
				logDebug("Failed to set default agent: %v", err)
			}
		}
		return true

	case "e":
		// Quick edit — skip action menu, go straight to form
		if state.AgentsSelected >= 0 && state.AgentsSelected < len(s.config.Agents) {
			agent := s.config.Agents[state.AgentsSelected]
			state.AgentsState = "edit"
			s.loadAgentToForm(agent, state)
		}
		return true

	case "x":
		// Quick disable/enable toggle
		if state.AgentsSelected >= 0 && state.AgentsSelected < len(s.config.Agents) {
			_ = s.ToggleAgentDisabled(s.config.Agents[state.AgentsSelected].ID)
		}
		return true
	}
	return false
}

// handleActionMenuKey handles keyboard input in action menu
func (s *AgentsSettings) handleActionMenuKey(key string, state *State) bool {
	if state.AgentsSelected < 0 || state.AgentsSelected >= len(s.config.Agents) {
		return false
	}

	agent := s.config.Agents[state.AgentsSelected]

	// Determine available actions
	maxActions := 6 // Edit, Set Default, Clone, Delete, Preview, Enable/Disable

	switch key {
	case "up", "k":
		if state.AgentsActionChoice > 0 {
			state.AgentsActionChoice--
		}
		return true

	case "down", "j":
		if state.AgentsActionChoice < maxActions-1 {
			state.AgentsActionChoice++
		}
		return true

	case "enter", " ":
		// Actions available for both built-in and custom agents
		switch state.AgentsActionChoice {
		case 0: // Edit
			state.AgentsState = "edit"
			s.loadAgentToForm(agent, state)
		case 1: // Set as Default
			if err := s.SetDefaultAgent(agent.ID); err != nil {
				logDebug("Failed to set default agent: %v", err)
			}
			state.AgentsState = "list"
		case 2: // Clone
			newID := agent.ID + "-copy"
			if err := s.CloneAgent(agent.ID, newID); err != nil {
				logDebug("Failed to clone agent: %v", err)
			}
			state.AgentsState = "list"
		case 3: // Delete
			if err := s.DeleteAgent(agent.ID); err != nil {
				logDebug("Failed to delete agent: %v", err)
			}
			if state.AgentsSelected >= len(s.config.Agents) {
				state.AgentsSelected = len(s.config.Agents) - 1
			}
			state.AgentsState = "list"
		case 4: // Preview
			state.AgentsState = "preview"
		case 5: // Enable / Disable
			_ = s.ToggleAgentDisabled(agent.ID)
			state.AgentsState = "list"
		}
		return true

	case "esc", "backspace":
		state.AgentsState = "list"
		return true
	}
	return false
}

// handleFormKey handles keyboard input in form view
func (s *AgentsSettings) handleFormKey(key string, state *State) bool {
	// Check if we're in a text input field
	isTextInput := s.isTextInputField(state)
	isToggle := s.isToggleField(state)

	// Handle enter/space on the UseOverride toggle (field 6)
	if isToggle && (key == "enter" || key == "space") {
		state.AgentsFormUseOverride = !state.AgentsFormUseOverride
		return true
	}

	// Handle enter key to toggle editing mode for text fields
	if key == "enter" && isTextInput {
		state.AgentsFormEditing = !state.AgentsFormEditing
		return true
	}

	// Auto-enter editing mode when user starts typing in a text field
	if isTextInput && !state.AgentsFormEditing {
		// Check if this is a printable character or space
		isPrintable := (len(key) == 1 && key[0] >= 32 && key[0] <= 126) || key == "space"
		if isPrintable && key != "enter" && key != "esc" {
			// Auto-enter editing mode at the end of the field
			state.AgentsFormEditing = true
			state.AgentsCursorPos = s.getCurrentFieldLength(state)
		}
	}

	// If currently editing a text field, handle text input
	if state.AgentsFormEditing {
		switch key {
		case "esc":
			// Exit editing mode
			state.AgentsFormEditing = false
			return true
		case "enter":
			// Exit editing mode and stay in form
			state.AgentsFormEditing = false
			return true
		case "backspace":
			s.handleBackspace(state)
			return true
		case "ctrl+v":
			// Paste from clipboard
			content, err := clipboard.ReadAll()
			if err == nil && content != "" {
				content = strings.TrimSpace(content)
				s.handleTextInputForCurrentField(state, content)
			}
			return true
		default:
			// Regular character input (including space)
			s.handleTextInputForCurrentField(state, key)
			return true
		}
	}

	// Navigation mode (not editing)
	switch key {
	case "up", "k":
		if state.AgentsFormTab == 1 {
			// Tools tab navigation
			if state.AgentsToolsSelected > 0 {
				state.AgentsToolsSelected--
			}
		} else if state.AgentsFormTab == 2 {
			// Hooks tab navigation
			if state.AgentsHooksSelected > 0 {
				state.AgentsHooksSelected--
			}
		} else {
			// Other tabs - field navigation
			if state.AgentsFormField > 0 {
				state.AgentsFormField--
				s.updateCursorPosForField(state)
			}
		}
		return true

	case "down", "j":
		if state.AgentsFormTab == 1 {
			// Tools tab
			orderedTools := s.getOrderedToolList()
			maxItems := 1 + len(orderedTools) // Enable All + tools
			if state.AgentsToolsSelected < maxItems-1 {
				state.AgentsToolsSelected++
			}
		} else if state.AgentsFormTab == 2 {
			// Hooks tab
			orderedHooks := s.getOrderedHookList()
			if state.AgentsHooksSelected < len(orderedHooks)-1 {
				state.AgentsHooksSelected++
			}
		} else {
			// Other tabs - field navigation
			maxFields := s.getMaxFieldsForTab(state.AgentsFormTab)
			if state.AgentsFormField < maxFields-1 {
				state.AgentsFormField++
				s.updateCursorPosForField(state)
			}
		}
		return true

	case "left", "h":
		// Adjust sliders in capabilities tab, or dropdown navigation
		if state.AgentsFormTab == 3 {
			s.adjustCapability(state, -1)
		} else if state.AgentsFormTab == 0 && s.isDropdownField(state) {
			s.adjustDropdown(state, -1)
		} else if state.AgentsFormTab == 0 && state.AgentsCursorPos > 0 {
			state.AgentsCursorPos--
		}
		return true

	case "right", "l":
		if state.AgentsFormTab == 3 {
			s.adjustCapability(state, 1)
		} else if state.AgentsFormTab == 0 && s.isDropdownField(state) {
			s.adjustDropdown(state, 1)
		} else if state.AgentsFormTab == 0 {
			fieldLen := s.getCurrentFieldLength(state)
			if state.AgentsCursorPos < fieldLen {
				state.AgentsCursorPos++
			}
		}
		return true

	case "space":
		// Toggle for tools/hooks tabs, or override toggle in basic tab
		if state.AgentsFormTab == 1 {
			s.toggleTool(state)
		} else if state.AgentsFormTab == 2 {
			s.toggleHook(state)
		} else if state.AgentsFormTab == 0 && state.AgentsFormField == 6 {
			state.AgentsFormUseOverride = !state.AgentsFormUseOverride
		}
		return true

	case "ctrl+tab", "shift+tab":
		// Previous tab
		if state.AgentsFormTab > 0 {
			state.AgentsFormTab--
		} else {
			state.AgentsFormTab = 3
		}
		state.AgentsFormField = 0
		state.AgentsCursorPos = 0
		return true

	case "tab":
		// Next tab
		if state.AgentsFormTab < 3 {
			state.AgentsFormTab++
		} else {
			state.AgentsFormTab = 0
		}
		state.AgentsFormField = 0
		state.AgentsCursorPos = 0
		return true

	case "ctrl+s":
		// Save agent
		if s.validateForm(state) {
			agent := s.buildAgentFromForm(state)
			if state.AgentsState == "create" {
				if err := s.CreateAgent(agent); err != nil {
					logDebug("Failed to create agent: %v", err)
				}
			} else {
				// Edit mode - update existing agent
				if state.AgentsSelected >= 0 && state.AgentsSelected < len(s.config.Agents) {
					oldID := s.config.Agents[state.AgentsSelected].ID
					if err := s.UpdateAgent(oldID, agent); err != nil {
						logDebug("Failed to update agent: %v", err)
					}
				}
			}
			state.AgentsState = "list"
		}
		return true

	case "esc":
		// Cancel and go back to list
		state.AgentsState = "list"
		return true

	default:
		return false
	}
}

// handleBackspace handles backspace in text input mode
func (s *AgentsSettings) handleBackspace(state *State) {
	switch state.AgentsFormField {
	case 0: // ID
		if len(state.AgentsFormID) > 0 && state.AgentsCursorPos > 0 {
			state.AgentsFormID = state.AgentsFormID[:state.AgentsCursorPos-1] + state.AgentsFormID[state.AgentsCursorPos:]
			state.AgentsCursorPos--
		}
	case 1: // Name
		if len(state.AgentsFormName) > 0 && state.AgentsCursorPos > 0 {
			state.AgentsFormName = state.AgentsFormName[:state.AgentsCursorPos-1] + state.AgentsFormName[state.AgentsCursorPos:]
			state.AgentsCursorPos--
		}
	case 2: // Description
		if len(state.AgentsFormDescription) > 0 && state.AgentsCursorPos > 0 {
			state.AgentsFormDescription = state.AgentsFormDescription[:state.AgentsCursorPos-1] + state.AgentsFormDescription[state.AgentsCursorPos:]
			state.AgentsCursorPos--
		}
	case 3: // Icon
		if len(state.AgentsFormIcon) > 0 && state.AgentsCursorPos > 0 {
			state.AgentsFormIcon = state.AgentsFormIcon[:state.AgentsCursorPos-1] + state.AgentsFormIcon[state.AgentsCursorPos:]
			state.AgentsCursorPos--
		}
	case 6: // System Prompt
		if len(state.AgentsFormSystemPrompt) > 0 && state.AgentsCursorPos > 0 {
			state.AgentsFormSystemPrompt = state.AgentsFormSystemPrompt[:state.AgentsCursorPos-1] + state.AgentsFormSystemPrompt[state.AgentsCursorPos:]
			state.AgentsCursorPos--
		}
	}
}

// handlePreviewKey handles keyboard input in preview view
func (s *AgentsSettings) handlePreviewKey(key string, state *State) bool {
	if key == "esc" || key == "backspace" {
		state.AgentsState = "action_menu"
		return true
	}
	return false
}

// Helper functions

func (s *AgentsSettings) resetFormState(state *State) {
	state.AgentsFormTab = 0
	state.AgentsFormField = 0
	state.AgentsCursorPos = 0
	state.AgentsFormID = ""
	state.AgentsFormName = ""
	state.AgentsFormDescription = ""
	state.AgentsFormProvider = "anthropic"
	state.AgentsFormModel = "claude-sonnet-4-20250514"
	state.AgentsFormSystemPrompt = ""
	state.AgentsFormTools = []string{"*"}
	state.AgentsFormHooks = []string{}
	state.AgentsFormTemperature = 0.7
	state.AgentsFormMaxTurns = 20
	state.AgentsFormTimeout = 900
	state.AgentsFormProfileID = ""
	state.AgentsFormRoleAlias = ""
	state.AgentsFormIcon = "●"
	state.AgentsFormColor = "#6366F1"
	state.AgentsToolsSelected = 0
	state.AgentsHooksSelected = 0
	state.AgentsToolsFilter = ""
	state.AgentsToolsFiltering = false
	state.AgentsFormError = ""
}

func (s *AgentsSettings) loadAgentToForm(agent CustomAgentEntry, state *State) {
	state.AgentsFormTab = 0
	state.AgentsFormField = 0
	state.AgentsCursorPos = 0
	state.AgentsFormID = agent.ID
	state.AgentsFormName = agent.Name
	state.AgentsFormDescription = agent.Description
	state.AgentsFormProvider = agent.Provider
	state.AgentsFormModel = agent.Model
	state.AgentsFormSystemPrompt = agent.SystemPrompt
	state.AgentsFormProfileID = agent.ProfileID
	state.AgentsFormRoleAlias = agent.RoleAlias
	state.AgentsFormIcon = agent.Icon
	if state.AgentsFormIcon == "" {
		state.AgentsFormIcon = "●"
	}
	state.AgentsFormColor = agent.Color
	if state.AgentsFormColor == "" {
		state.AgentsFormColor = "#6366F1"
	}
	state.AgentsFormTools = make([]string, len(agent.Tools))
	copy(state.AgentsFormTools, agent.Tools)
	state.AgentsFormHooks = make([]string, len(agent.Hooks))
	copy(state.AgentsFormHooks, agent.Hooks)
	if agent.Capabilities != nil {
		state.AgentsFormTemperature = agent.Capabilities.Temperature
		state.AgentsFormMaxTurns = agent.Capabilities.MaxTurns
		state.AgentsFormTimeout = agent.Capabilities.Timeout
	}
	state.AgentsToolsFilter = ""
	state.AgentsToolsFiltering = false
	state.AgentsFormError = ""
	// UseOverride: show explicit provider/model only if the agent has them set
	// and they differ from empty (i.e. user previously configured a specific model)
	state.AgentsFormUseOverride = agent.Provider != "" && agent.Model != ""
}

func (s *AgentsSettings) validateForm(state *State) bool {
	if state.AgentsFormID == "" {
		state.AgentsFormError = "ID is required"
		return false
	}
	if !isValidAgentID(state.AgentsFormID) {
		state.AgentsFormError = "ID must be lowercase letters, numbers, hyphens only"
		return false
	}
	if state.AgentsFormName == "" {
		state.AgentsFormError = "Name is required"
		return false
	}
	// Only require provider+model when the user has explicitly enabled override
	if state.AgentsFormUseOverride {
		if state.AgentsFormProvider == "" {
			state.AgentsFormError = "Provider is required (or disable model pin)"
			return false
		}
		if state.AgentsFormModel == "" {
			state.AgentsFormError = "Model is required (or disable model pin)"
			return false
		}
	}
	state.AgentsFormError = ""
	return true
}

func (s *AgentsSettings) buildAgentFromForm(state *State) CustomAgentEntry {
	profileID := state.AgentsFormProfileID
	if profileID == "(use active profile)" {
		profileID = ""
	}
	roleAlias := state.AgentsFormRoleAlias
	if roleAlias == "(none)" {
		roleAlias = ""
	}
	icon := state.AgentsFormIcon
	if icon == "" {
		icon = "●"
	}
	color := state.AgentsFormColor
	if color == "" {
		color = "#6366F1"
	}
	// When not using override, clear provider+model so the profile system is used
	provider := ""
	model := ""
	if state.AgentsFormUseOverride {
		provider = state.AgentsFormProvider
		model = state.AgentsFormModel
	}
	return CustomAgentEntry{
		ID:           state.AgentsFormID,
		Name:         state.AgentsFormName,
		Description:  state.AgentsFormDescription,
		Provider:     provider,
		Model:        model,
		SystemPrompt: state.AgentsFormSystemPrompt,
		Tools:        state.AgentsFormTools,
		Hooks:        state.AgentsFormHooks,
		ProfileID:    profileID,
		RoleAlias:    roleAlias,
		Icon:         icon,
		Color:        color,
		Capabilities: &AgentCapabilities{
			MaxTokens:   0, // Let system use default/auto-compact naturally
			Temperature: state.AgentsFormTemperature,
			MaxTurns:    state.AgentsFormMaxTurns,
			Timeout:     max(state.AgentsFormTimeout, 900), // Enforce minimum 900s
		},
		Builtin: false,
	}
}

func (s *AgentsSettings) getMaxFieldsForTab(tab int) int {
	switch tab {
	case 0:
		// 0:ID 1:Name 2:Desc 3:Icon 4:Profile 5:RoleAlias 6:Override toggle 7:Provider 8:Model 9:SystemPrompt
		return 10
	case 3: // Capabilities: Temperature, MaxTurns, Timeout
		return 3
	default:
		return 0
	}
}

func (s *AgentsSettings) updateCursorPosForField(state *State) {
	state.AgentsCursorPos = s.getCurrentFieldLength(state)
}

func (s *AgentsSettings) getCurrentFieldLength(state *State) int {
	if state.AgentsFormTab != 0 {
		return 0
	}
	switch state.AgentsFormField {
	case 0:
		return len(state.AgentsFormID)
	case 1:
		return len(state.AgentsFormName)
	case 2:
		return len(state.AgentsFormDescription)
	case 3:
		return len(state.AgentsFormIcon)
	case 9:
		return len(state.AgentsFormSystemPrompt)
	}
	return 0
}

func (s *AgentsSettings) handleTextInputForCurrentField(state *State, key string) {
	if state.AgentsFormTab != 0 {
		return
	}

	var current string
	switch state.AgentsFormField {
	case 0:
		current = state.AgentsFormID
	case 1:
		current = state.AgentsFormName
	case 2:
		current = state.AgentsFormDescription
	case 3:
		current = state.AgentsFormIcon
	case 9:
		current = state.AgentsFormSystemPrompt
	default:
		return
	}

	newValue := handleTextInput(current, &state.AgentsCursorPos, key)

	switch state.AgentsFormField {
	case 0:
		state.AgentsFormID = strings.ToLower(newValue)
	case 1:
		state.AgentsFormName = newValue
	case 2:
		state.AgentsFormDescription = newValue
	case 3:
		state.AgentsFormIcon = newValue
	case 9:
		state.AgentsFormSystemPrompt = newValue
	}
}

// Field indices for Basic Info tab (tab 0):
//
//	0 = ID            (text)
//	1 = Name          (text)
//	2 = Description   (text)
//	3 = Icon          (text, short)
//	4 = Profile       (dropdown ←/→)
//	5 = Role Alias    (dropdown ←/→)
//	6 = System Prompt (text field)
//	9 = System Prompt (text, multiline)

// isTextInputField returns true if the current field accepts free-text input.
func (s *AgentsSettings) isTextInputField(state *State) bool {
	if state.AgentsFormTab != 0 {
		return false
	}
	switch state.AgentsFormField {
	case 0, 1, 2, 3, 9: // ID, Name, Description, Icon, SystemPrompt
		return true
	}
	return false
}

// isDropdownField returns true if the current field is a ←/→ cycle dropdown.
func (s *AgentsSettings) isDropdownField(state *State) bool {
	if state.AgentsFormTab != 0 {
		return false
	}
	switch state.AgentsFormField {
	case 4, 5: // Profile, RoleAlias
		return true
	case 6: // System Prompt (text field - no cycle)
		return false
	}
	return false
}

// isToggleField returns true if the current field is a Space-to-toggle boolean.
func (s *AgentsSettings) isToggleField(state *State) bool {
	return state.AgentsFormTab == 0 && state.AgentsFormField == 6
}

// adjustDropdown adjusts provider/model/profile/role dropdown selection
func (s *AgentsSettings) adjustDropdown(state *State, delta int) {
	cycleString := func(list []string, current string, delta int) string {
		if len(list) == 0 {
			return current
		}
		idx := 0
		for i, v := range list {
			if v == current {
				idx = i
				break
			}
		}
		idx = (idx + delta + len(list)) % len(list)
		return list[idx]
	}

	switch state.AgentsFormField {
	case 4: // Profile
		profiles := s.GetAvailableProfileIDs()
		state.AgentsFormProfileID = cycleString(profiles, state.AgentsFormProfileID, delta)
	case 5: // Role Alias
		roles := GetAvailableRoleAliases()
		state.AgentsFormRoleAlias = cycleString(roles, state.AgentsFormRoleAlias, delta)
	case 7: // Provider (only when UseOverride)
		state.AgentsFormProvider = cycleString(s.providerNames, state.AgentsFormProvider, delta)
		// Auto-select first model when provider changes
		models := s.availableModels[state.AgentsFormProvider]
		if len(models) > 0 {
			state.AgentsFormModel = models[0]
		}
	case 8: // Model (only when UseOverride)
		models := s.availableModels[state.AgentsFormProvider]
		state.AgentsFormModel = cycleString(models, state.AgentsFormModel, delta)
	}
}

func (s *AgentsSettings) toggleTool(state *State) {
	if state.AgentsToolsSelected == 0 {
		// Toggle "Enable All"
		if len(state.AgentsFormTools) == 1 && state.AgentsFormTools[0] == "*" {
			state.AgentsFormTools = []string{}
		} else {
			state.AgentsFormTools = []string{"*"}
		}
		return
	}

	// Find the tool at the selected index using the ordered list
	orderedTools := s.getOrderedToolList()
	idx := state.AgentsToolsSelected - 1 // Account for "Enable All"
	if idx >= 0 && idx < len(orderedTools) {
		toolName := orderedTools[idx].Name
		if contains(state.AgentsFormTools, toolName) {
			state.AgentsFormTools = remove(state.AgentsFormTools, toolName)
		} else {
			// If "*" is set, clear it first
			if len(state.AgentsFormTools) == 1 && state.AgentsFormTools[0] == "*" {
				state.AgentsFormTools = []string{}
			}
			state.AgentsFormTools = append(state.AgentsFormTools, toolName)
		}
	}
}

func (s *AgentsSettings) toggleHook(state *State) {
	// Find the hook at the selected index using the ordered list
	orderedHooks := s.getOrderedHookList()
	if state.AgentsHooksSelected >= 0 && state.AgentsHooksSelected < len(orderedHooks) {
		hookName := orderedHooks[state.AgentsHooksSelected].Name
		if contains(state.AgentsFormHooks, hookName) {
			state.AgentsFormHooks = remove(state.AgentsFormHooks, hookName)
		} else {
			state.AgentsFormHooks = append(state.AgentsFormHooks, hookName)
		}
	}
}

func (s *AgentsSettings) adjustCapability(state *State, direction int) {
	switch state.AgentsFormField {
	case 0: // Temperature
		step := 0.1 * float64(direction)
		state.AgentsFormTemperature += step
		if state.AgentsFormTemperature < 0 {
			state.AgentsFormTemperature = 0
		}
		if state.AgentsFormTemperature > 1.0 {
			state.AgentsFormTemperature = 1.0
		}
	case 1: // Max Turns
		step := 1 * direction
		state.AgentsFormMaxTurns += step
		if state.AgentsFormMaxTurns < 0 {
			state.AgentsFormMaxTurns = 0
		}
		if state.AgentsFormMaxTurns > 1000 {
			state.AgentsFormMaxTurns = 1000
		}
	case 2: // Timeout
		step := 30 * direction
		state.AgentsFormTimeout += step
		if state.AgentsFormTimeout < 900 {
			state.AgentsFormTimeout = 900
		}
		if state.AgentsFormTimeout > 3600 {
			state.AgentsFormTimeout = 3600
		}
	}
}

// handleChatKey handles keyboard input in chat view
func (s *AgentsSettings) handleChatKey(key string, state *State) bool {
	// Don't handle input if waiting for response
	if state.AgentsChatWaiting {
		if key == "esc" {
			// Allow escape even when waiting
			state.AgentsState = "list"
			state.AgentsChatWaiting = false
			return true
		}
		return true // Consume all other keys while waiting
	}

	switch key {
	case "esc":
		// Return to list
		state.AgentsState = "list"
		return true

	case "ctrl+c":
		// Clear chat history
		s.ClearChatHistory()
		state.AgentsChatInput = ""
		state.AgentsCursorPos = 0
		state.AgentsChatScrollOffset = 0
		return true

	case "enter":
		// Send message
		if strings.TrimSpace(state.AgentsChatInput) != "" {
			// Trigger chat send via callback
			if s.onChatSend != nil {
				s.onChatSend(state.AgentsChatInput)
			}
			// Clear input and set waiting state
			state.AgentsChatInput = ""
			state.AgentsCursorPos = 0
			state.AgentsChatWaiting = true
		}
		return true

	case "backspace":
		// Delete character before cursor
		if state.AgentsCursorPos > 0 && state.AgentsCursorPos <= len(state.AgentsChatInput) {
			state.AgentsChatInput = state.AgentsChatInput[:state.AgentsCursorPos-1] + state.AgentsChatInput[state.AgentsCursorPos:]
			state.AgentsCursorPos--
		} else if state.AgentsCursorPos > len(state.AgentsChatInput) {
			// Fix cursor position if out of bounds
			state.AgentsCursorPos = len(state.AgentsChatInput)
		}
		return true

	case "delete":
		// Delete character at cursor
		if state.AgentsCursorPos < len(state.AgentsChatInput) {
			state.AgentsChatInput = state.AgentsChatInput[:state.AgentsCursorPos] + state.AgentsChatInput[state.AgentsCursorPos+1:]
		} else if state.AgentsCursorPos > len(state.AgentsChatInput) {
			// Fix cursor position if out of bounds
			state.AgentsCursorPos = len(state.AgentsChatInput)
		}
		return true

	case "left":
		// Move cursor left
		if state.AgentsCursorPos > 0 {
			state.AgentsCursorPos--
		}
		return true

	case "right":
		// Move cursor right
		if state.AgentsCursorPos < len(state.AgentsChatInput) {
			state.AgentsCursorPos++
		}
		return true

	case "home", "ctrl+a":
		// Move cursor to beginning
		state.AgentsCursorPos = 0
		return true

	case "end", "ctrl+e":
		// Move cursor to end
		state.AgentsCursorPos = len(state.AgentsChatInput)
		return true

	case "up":
		// Scroll up in chat history
		if state.AgentsChatScrollOffset > 0 {
			state.AgentsChatScrollOffset--
		}
		return true

	case "down":
		// Scroll down in chat history
		state.AgentsChatScrollOffset++
		return true

	case "pageup", "ctrl+u":
		// Scroll up faster
		if state.AgentsChatScrollOffset > 0 {
			state.AgentsChatScrollOffset -= 5
			if state.AgentsChatScrollOffset < 0 {
				state.AgentsChatScrollOffset = 0
			}
		}
		return true

	case "pagedown", "ctrl+d":
		// Scroll down faster
		state.AgentsChatScrollOffset += 5
		return true

	case "space":
		// Insert space at cursor
		if state.AgentsCursorPos > len(state.AgentsChatInput) {
			state.AgentsCursorPos = len(state.AgentsChatInput)
		}
		state.AgentsChatInput = state.AgentsChatInput[:state.AgentsCursorPos] + " " + state.AgentsChatInput[state.AgentsCursorPos:]
		state.AgentsCursorPos++
		return true

	case "ctrl+v":
		// Paste from clipboard
		content, err := clipboard.ReadAll()
		if err == nil && content != "" {
			if state.AgentsCursorPos > len(state.AgentsChatInput) {
				state.AgentsCursorPos = len(state.AgentsChatInput)
			}
			state.AgentsChatInput = state.AgentsChatInput[:state.AgentsCursorPos] + content + state.AgentsChatInput[state.AgentsCursorPos:]
			state.AgentsCursorPos += len(content)
		}
		return true

	case "ctrl+k":
		// Delete from cursor to end of line
		if state.AgentsCursorPos > len(state.AgentsChatInput) {
			state.AgentsCursorPos = len(state.AgentsChatInput)
		}
		state.AgentsChatInput = state.AgentsChatInput[:state.AgentsCursorPos]
		return true

	case "ctrl+w":
		// Delete word before cursor
		if state.AgentsCursorPos > len(state.AgentsChatInput) {
			state.AgentsCursorPos = len(state.AgentsChatInput)
		}
		if state.AgentsCursorPos > 0 {
			// Find start of current word
			i := state.AgentsCursorPos - 1
			// Skip whitespace
			for i >= 0 && state.AgentsChatInput[i] == ' ' {
				i--
			}
			// Find word boundary
			for i >= 0 && state.AgentsChatInput[i] != ' ' {
				i--
			}
			state.AgentsChatInput = state.AgentsChatInput[:i+1] + state.AgentsChatInput[state.AgentsCursorPos:]
			state.AgentsCursorPos = i + 1
		}
		return true

	default:
		// Regular text input - handle all printable characters
		if len(key) == 1 {
			// Insert character at cursor position
			if state.AgentsCursorPos > len(state.AgentsChatInput) {
				state.AgentsCursorPos = len(state.AgentsChatInput)
			}
			state.AgentsChatInput = state.AgentsChatInput[:state.AgentsCursorPos] + key + state.AgentsChatInput[state.AgentsCursorPos:]
			state.AgentsCursorPos++
			return true
		} else if len(key) > 1 && !strings.HasPrefix(key, "ctrl+") && !strings.HasPrefix(key, "alt+") && !strings.HasPrefix(key, "shift+") {
			// Handle multi-byte characters (e.g., emojis, Unicode)
			if state.AgentsCursorPos > len(state.AgentsChatInput) {
				state.AgentsCursorPos = len(state.AgentsChatInput)
			}
			state.AgentsChatInput = state.AgentsChatInput[:state.AgentsCursorPos] + key + state.AgentsChatInput[state.AgentsCursorPos:]
			state.AgentsCursorPos += len(key)
			return true
		}
	}
	return false
}

// Utility functions

// getOrderedToolList returns tools grouped and ordered consistently for UI rendering.
func (s *AgentsSettings) getOrderedToolList() []AgentToolInfo {
	if len(s.availableTools) == 0 {
		return []AgentToolInfo{
			{"Bash", "Execute shell commands", "builtin"},
			{"Read", "Read file contents (hash-verified)", "builtin"},
			{"Edit", "Edit files (hash-verified)", "builtin"},
			{"ReadLegacy", "Read file contents", "builtin"},
			{"EditLegacy", "Edit specific lines", "builtin"},
			{"WriteLegacy", "Write/create files", "builtin"},
			{"Grep", "Search file contents", "builtin"},
			{"Task", "Delegate to sub-agents", "builtin"},
			{"BackgroundTask", "Run background tasks", "builtin"},
			{"TaskOutput", "Check background task status", "builtin"},
		}
	}

	categories := map[string][]AgentToolInfo{}
	for _, t := range s.availableTools {
		cat := t.Category
		if cat == "" {
			cat = "other"
		}
		categories[cat] = append(categories[cat], t)
	}

	categoryOrder := []string{"builtin", "mcp", "custom", "other"}
	var out []AgentToolInfo
	for _, cat := range categoryOrder {
		if tools, ok := categories[cat]; ok {
			out = append(out, tools...)
		}
	}
	return out
}

// getOrderedHookList returns hooks grouped and ordered consistently for UI rendering.
func (s *AgentsSettings) getOrderedHookList() []AgentHookInfo {
	if len(s.availableHooks) == 0 {
		return []AgentHookInfo{
			{"logging", "Log all events", "builtin", "sdk"},
			{"metrics", "Collect metrics", "builtin", "sdk"},
			{"audit", "Audit sensitive events", "builtin", "sdk"},
			{"tool.before_execute", "Pre-tool validation", "event", "system"},
			{"tool.after_execute", "Post-tool processing", "event", "system"},
			{"message.before_send", "Pre-message hook", "event", "system"},
		}
	}

	types := map[string][]AgentHookInfo{}
	for _, h := range s.availableHooks {
		t := h.Type
		if t == "" {
			t = "other"
		}
		types[t] = append(types[t], h)
	}

	typeOrder := []string{"builtin", "event", "shell", "other"}
	var out []AgentHookInfo
	for _, t := range typeOrder {
		if hooks, ok := types[t]; ok {
			out = append(out, hooks...)
		}
	}
	return out
}

func renderSlider(value, min, max, width int) string {
	if max <= min {
		return strings.Repeat("─", width)
	}
	pos := (value - min) * (width - 1) / (max - min)
	if pos < 0 {
		pos = 0
	}
	if pos >= width {
		pos = width - 1
	}
	return "◀" + strings.Repeat("─", pos) + "●" + strings.Repeat("─", width-1-pos) + "▶"
}

func contains(slice []string, item string) bool {
	return slices.Contains(slice, item)
}

func remove(slice []string, item string) []string {
	result := make([]string, 0, len(slice))
	for _, s := range slice {
		if s != item {
			result = append(result, s)
		}
	}
	return result
}

// ============================================================================
// AI ASSISTANT CHAT
// ============================================================================

// SetChatCallback sets the callback for simple chat messages
func (s *AgentsSettings) SetChatCallback(callback func(message string)) {
	s.onChatSend = callback
}

// SetDetailedChatCallback sets the callback for detailed chat messages with tool calls
func (s *AgentsSettings) SetDetailedChatCallback(callback AgentChatCallback) {
	s.onChatSendDetailed = callback
}

// AddChatMessage adds a message to the chat history
func (s *AgentsSettings) AddChatMessage(role, content string, toolCalls []ToolCallDisplay, turns int) {
	s.chatMessages = append(s.chatMessages, AgentChatMessage{
		Role:      role,
		Content:   content,
		ToolCalls: toolCalls,
		Turns:     turns,
	})
}

// ClearChatHistory clears the chat history
func (s *AgentsSettings) ClearChatHistory() {
	s.chatMessages = []AgentChatMessage{}
}

// renderAgentChat renders the AI chat interface for agent management
func (s *AgentsSettings) renderAgentChat(width, height int, state *State, th Theme) string {
	var lines []string

	// Title bar
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Text)).
		Background(lipgloss.Color(th.Primary)).
		Bold(true).
		Width(width-4).
		Padding(0, 2)
	lines = append(lines, titleStyle.Render("AI Agent Assistant"))
	lines = append(lines, "")

	// Instructions
	instructStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Padding(0, 2)
	lines = append(lines, instructStyle.Render("Create and manage agents with AI assistance."))
	lines = append(lines, instructStyle.Render("Examples: 'Create a security auditor agent'"))
	lines = append(lines, instructStyle.Render("          'Set temperature to 0.3 for code-reviewer'"))
	lines = append(lines, "")

	// Chat history area
	chatAreaHeight := height - 14
	if chatAreaHeight < 5 {
		chatAreaHeight = 5
	}

	chatBgStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.BG)).
		Width(width-6).
		Height(chatAreaHeight).
		Padding(1).
		Margin(0, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border))

	var chatLines []string
	if len(s.chatMessages) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)
		chatLines = append(chatLines, emptyStyle.Render("No messages yet. Start chatting to create and modify agents."))
	} else {
		// Styles for different elements
		userStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Primary)).
			Bold(true)
		assistantHeaderStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Success)).
			Bold(true)
		assistantTextStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Text))
		toolNameStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#C792EA")). // Purple for tool names
			Bold(true)
		toolSymbolStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#89DDFF")) // Cyan for symbols
		toolInputStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#82AAFF")) // Blue for input JSON
		toolResultStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#C3E88D")) // Green for results
		toolErrorStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.Error))
		turnsStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Italic(true)

		for _, msg := range s.chatMessages {
			switch msg.Role {
			case "user":
				chatLines = append(chatLines, userStyle.Render("◆ You"))
				wrapped := wordWrapText(msg.Content, width-16)
				for line := range strings.SplitSeq(wrapped, "\n") {
					chatLines = append(chatLines, "  "+line)
				}
				chatLines = append(chatLines, "")

			case "assistant":
				// Show turn count if > 1 (indicates multi-turn agent conversation)
				if msg.Turns > 1 {
					chatLines = append(chatLines, assistantHeaderStyle.Render("◆ Agent")+turnsStyle.Render(fmt.Sprintf(" (%d turns)", msg.Turns)))
				} else {
					chatLines = append(chatLines, assistantHeaderStyle.Render("◆ Agent"))
				}

				// Show tool calls (if any)
				if len(msg.ToolCalls) > 0 {
					for _, tc := range msg.ToolCalls {
						// Tool call header
						chatLines = append(chatLines, "  "+toolSymbolStyle.Render("⎿ ")+toolNameStyle.Render(tc.Name))

						// Tool input (pretty-printed JSON, truncated if long)
						if tc.InputJSON != "" {
							inputLines := strings.Split(tc.InputJSON, "\n")
							maxInputLines := 6
							if len(inputLines) > maxInputLines {
								for i := 0; i < maxInputLines-1; i++ {
									line := inputLines[i]
									if len(line) > width-20 {
										line = line[:width-23] + "..."
									}
									chatLines = append(chatLines, "    "+toolInputStyle.Render(line))
								}
								chatLines = append(chatLines, "    "+toolInputStyle.Render("    ..."))
							} else {
								for _, line := range inputLines {
									if len(line) > width-20 {
										line = line[:width-23] + "..."
									}
									chatLines = append(chatLines, "    "+toolInputStyle.Render(line))
								}
							}
						}

						// Tool result
						if tc.Result != "" {
							resultStyle := toolResultStyle
							resultPrefix := "✓ "
							if !tc.Success {
								resultStyle = toolErrorStyle
								resultPrefix = "✗ "
							}
							resultLines := strings.Split(tc.Result, "\n")
							maxResultLines := 8
							if len(resultLines) > maxResultLines {
								for i := 0; i < maxResultLines-1; i++ {
									line := resultPrefix + resultLines[i]
									if len(line) > width-20 {
										line = line[:width-23] + "..."
									}
									chatLines = append(chatLines, "    "+resultStyle.Render(line))
								}
								chatLines = append(chatLines, "    "+resultStyle.Render("    ..."))
							} else {
								for _, line := range resultLines {
									displayLine := resultPrefix + line
									resultPrefix = "  " // Only show checkmark on first line
									if len(displayLine) > width-20 {
										displayLine = displayLine[:width-23] + "..."
									}
									chatLines = append(chatLines, "    "+resultStyle.Render(displayLine))
								}
							}
						}
						chatLines = append(chatLines, "")
					}
				}

				// Show assistant's text response
				if msg.Content != "" {
					wrapped := wordWrapText(msg.Content, width-16)
					for line := range strings.SplitSeq(wrapped, "\n") {
						chatLines = append(chatLines, "  "+assistantTextStyle.Render(line))
					}
				}
				chatLines = append(chatLines, "")
			}
		}
	}

	// Scroll handling
	maxScroll := len(chatLines) - chatAreaHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if state.AgentsChatScrollOffset > maxScroll {
		state.AgentsChatScrollOffset = maxScroll
	}
	if state.AgentsChatScrollOffset < 0 {
		state.AgentsChatScrollOffset = 0
	}

	// Display visible lines
	visibleLines := make([]string, 0, chatAreaHeight)
	for i := state.AgentsChatScrollOffset; i < state.AgentsChatScrollOffset+chatAreaHeight && i < len(chatLines); i++ {
		visibleLines = append(visibleLines, chatLines[i])
	}

	// Pad with empty lines if needed
	for len(visibleLines) < chatAreaHeight {
		visibleLines = append(visibleLines, "")
	}

	chatContent := chatBgStyle.Render(strings.Join(visibleLines, "\n"))
	lines = append(lines, chatContent)
	lines = append(lines, "")

	// Input area
	inputLabelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Bold(true).
		Padding(0, 2)
	lines = append(lines, inputLabelStyle.Render("Your message:"))

	inputBoxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Primary)).
		Padding(0, 1).
		Width(width-8).
		Margin(0, 2)

	inputText := state.AgentsChatInput
	if state.AgentsChatWaiting {
		inputText = "Agent is thinking..."
	} else if inputText == "" {
		inputText = "Type your message here..."
	} else {
		// Show cursor in input
		cursorPos := state.AgentsCursorPos
		if cursorPos < 0 {
			cursorPos = 0
		}
		if cursorPos > len(inputText) {
			cursorPos = len(inputText)
		}
		// Insert cursor marker
		if cursorPos < len(inputText) {
			// Cursor in middle or start
			inputText = inputText[:cursorPos] + "│" + inputText[cursorPos:]
		} else {
			// Cursor at end
			inputText = inputText + "│"
		}
	}

	lines = append(lines, inputBoxStyle.Render(inputText))
	lines = append(lines, "")

	// Hints
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true).
		Padding(0, 2)
	lines = append(lines, hintStyle.Render("Enter: Send • Ctrl+V: Paste • Ctrl+C: Clear • Esc: Back"))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	containerStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Padding(1, 2)

	return containerStyle.Render(content)
}
