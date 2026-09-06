package settings

import (
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// SecurityLevel is an integer index into the ordered list of permission levels
// (0=AlwaysAsk, 1=Balanced, 2=Permissive, 3=YOLO).  The named constants and
// String method make log/debug output self-documenting.
type SecurityLevel int

const (
	SecurityLevelAlwaysAsk  SecurityLevel = 0
	SecurityLevelBalanced   SecurityLevel = 1
	SecurityLevelPermissive SecurityLevel = 2
	SecurityLevelYOLO       SecurityLevel = 3
)

// String returns a human-readable name for the security level.
func (l SecurityLevel) String() string {
	switch l {
	case SecurityLevelAlwaysAsk:
		return "AlwaysAsk"
	case SecurityLevelBalanced:
		return "Balanced"
	case SecurityLevelPermissive:
		return "Permissive"
	case SecurityLevelYOLO:
		return "YOLO"
	default:
		return fmt.Sprintf("SecurityLevel(%d)", int(l))
	}
}

// Section represents a settings category
type Section int

const (
	SectionGeneral Section = iota
	SectionSecurity
	SectionModel
	SectionModels  // Unified: manage profiles, providers, and model config
	SectionProxies // New: Proxy configuration
	SectionAgents
	SectionAgentProfiles // New: Agent profile configuration
	SectionConfigBundles // New: Configuration bundles for sharing/switching configs
	SectionConfigSource  // New: Global/project config source switching
	SectionMCP
	SectionHooks
	SectionSkills  // Skills management
	SectionPlugins // Claude Code-compatible plugins
	SectionSystemPrompt
	SectionContext
	SectionDisplay
	SectionTheme // Theme & Background picker
	SectionAuth
	SectionCompaction
	SectionPlan // Plan-before-act workflow (enter_plan_mode / exit_plan_mode)
	SectionReliability
	SectionAdvanced
	SectionCache
	SectionVoice       // Voice input settings
	SectionUpdates     // Auto-update settings
	SectionSteering    // Steering configuration
	SectionWebSearch   // Web search backend and options
	SectionVault       // Credential vault management
	SectionComputerUse // Screen control and input automation
)

// SectionInfo holds metadata about a settings section
type SectionInfo struct {
	ID          Section
	Name        string
	Description string
	Icon        string
	Group       string // Parent group name (for sidebar grouping)
}

// SectionGroups defines the ordered list of sidebar groups
var SectionGroups = []string{
	"AI Config",
	"Tools & Integrations",
	"Appearance",
	"Security & Auth",
	"Performance",
	"Advanced",
}

var Sections = []SectionInfo{
	// AI Config
	{SectionModels, "Models", "Configure model profiles and providers", "◆", "AI Config"},
	{SectionAgents, "Agents", "Create and manage custom agents", "◆", "AI Config"},
	{SectionConfigSource, "Config Source", "Switch between global and project config", "◆", "AI Config"},
	{SectionSystemPrompt, "System Prompts", "Manage AI system prompts", "◆", "AI Config"},
	{SectionCompaction, "Compaction", "Configure automatic compaction thresholds", "◆", "AI Config"},
	{SectionPlan, "Plan Mode", "Configure plan-before-act workflow tools", "◆", "AI Config"},

	// Tools & Integrations
	{SectionMCP, "Tools and MCP", "Manage tools and MCP servers", "◆", "Tools & Integrations"},
	{SectionVoice, "Transcription", "Configure providers, local models, and microphone input", "◆", "Tools & Integrations"},
	{SectionHooks, "Hooks", "Configure event hooks and automation", "◆", "Tools & Integrations"},
	{SectionSteering, "Steering", "Configure meta-cognitive layer and tool interception", "◆", "Tools & Integrations"},
	{SectionSkills, "Skills", "Manage agent skills and capabilities", "◆", "Tools & Integrations"},
	{SectionPlugins, "Plugins", "Claude Code-compatible plugins", "◆", "Tools & Integrations"},
	{SectionContext, "Context Sources", "Configure project context loading", "◆", "Tools & Integrations"},
	{SectionWebSearch, "Web Search", "Choose search backend and configure Exa options", "◆", "Tools & Integrations"},
	{SectionComputerUse, "Computer Use", "Enable screen control and input automation tools", "\u25c6", "Tools & Integrations"},

	// Appearance
	{SectionDisplay, "Display", "Customize rendering and output", "◆", "Appearance"},
	{SectionTheme, "Theme & Background", "Choose colour palette and background style", "◆", "Appearance"},

	// Security & Auth
	{SectionSecurity, "Security", "Permissions and safety controls", "◆", "Security & Auth"},
	{SectionAuth, "Authentication", "Login and API keys", "◆", "Security & Auth"},
	{SectionVault, "Vault", "Secure credential storage and execution", "◆", "Security & Auth"},
	{SectionProxies, "Proxies", "Configure API proxy endpoints", "◆", "Security & Auth"},

	// Performance
	{SectionCache, "Cache Statistics", "View cache performance metrics", "◆", "Performance"},
	{SectionReliability, "Reliability", "Retry, fallback, and rate-limit", "◆", "Performance"},

	// Advanced
	{SectionUpdates, "Updates", "Auto-update settings and version info", "◆", "Advanced"},
	{SectionGeneral, "General", "General application settings", "◆", "Advanced"},
	{SectionConfigBundles, "Config Bundles", "Export/import configurations", "◆", "Advanced"},
	{SectionAdvanced, "Advanced", "Advanced configuration", "◆", "Advanced"},
}

// AdvancedSubsection represents a subsection within Advanced settings (kept for backward compatibility)
type AdvancedSubsection int

const (
	AdvancedMain AdvancedSubsection = iota
	AdvancedCache
)

// InputType defines the type of input control
type InputType int

const (
	InputTypeText InputType = iota
	InputTypeDropdown
	InputTypeToggle
	InputTypeNumber
	InputTypeSlider
	InputTypeButton
)

// Item represents a single configurable setting
type Item struct {
	Key         string
	Label       string
	Description string
	Type        InputType
	Value       any
	Options     []string // For dropdowns
	Min         int      // For sliders/numbers
	Max         int      // For sliders/numbers
	Step        int      // For sliders/numbers
	Category    string   // Parent category within section
	OnChange    func(any) error
	OnClick     func() error // For buttons
}

// Focus represents which panel has focus
type Focus int

const (
	FocusSidebar Focus = iota
	FocusContent
)

// State tracks the settings screen state
type State struct {
	SelectedSection Section
	SelectedItem    int
	ScrollOffset    int
	MaxVisible      int
	EditingItem     bool
	EditBuffer      string
	Focus           Focus // Which panel has focus
	Dirty           bool  // true when any setting has been changed since last reset

	// Sidebar grouping & search
	CollapsedGroups map[string]bool // Which groups are collapsed
	SearchQuery     string          // Sidebar search filter
	SearchActive    bool            // Whether search input is active

	// Exit confirmation modal
	ShowExitModal   bool
	ExitModalChoice int // 0 = Save & Exit, 1 = Exit Without Saving, 2 = Cancel

	// Security settings state
	SecuritySelected      int
	SecurityScrollOffset  int
	SecurityLevelSelected SecurityLevel

	// Security rules management
	SecurityTab              int    // 0=Allow, 1=Ask, 2=Deny, 3=Workspace
	SecuritySearchQuery      string // Search filter text
	SecuritySearchActive     bool   // Whether the search box has focus (user pressed /)
	SecurityRulesState       string // "list", "action_menu", "add_form"
	SecurityRuleActionChoice int    // 0=Delete
	SecurityAddFormField     int    // 0=tool, 1=pattern, 2=policy
	SecurityAddFormTool      string // Tool name for new rule
	SecurityAddFormPattern   string // Glob pattern for new rule
	SecurityAddFormPolicy    string // "allow", "ask", "deny" — chosen inside the form
	SecurityAddFormWorkspace bool   // Whether to save to workspace (project) config
	SecurityAddCursorPos     int    // Cursor position in pattern input

	// Advanced subsection state (kept for backward compatibility)
	AdvancedSubsection AdvancedSubsection

	// MCP-specific state
	MCPState                string // "main", "configure_tools", "mcp_config", "tool_tester", "error_detail"
	MCPSelectedButton       int    // 0=Configure Tools, 1=MCP Config, 2=Tool Tester, 3=AI Assistant
	MCPFocusedPanel         string // "buttons", "enabled", "disabled"
	MCPScrollOffset         int
	MCPScrollOffsetEnabled  int
	MCPScrollOffsetDisabled int
	MCPCurrentServer        *commands.MCPServerState
	MCPCurrentTool          *mcp.MCPTool
	MCPErrorDetailScroll    int // Scroll offset for error detail view

	// Configure Tools screen state
	MCPConfigureToolsSelected           int             // Selected tool index
	MCPConfigureToolsScrollOffset       int             // Scroll offset for tool list
	MCPConfigureToolsExpandedCategories map[string]bool // Track which categories are expanded
	MCPConfigureToolsExpandedServers    map[string]bool // Track which MCP servers are expanded in by_server view
	MCPConfigureToolsViewMode           string          // "flat" or "categorized"

	// MCP Servers screen state
	MCPServersSelected     int    // Selected server index
	MCPServersScrollOffset int    // Scroll offset for server list
	MCPServersAddMode      bool   // Whether in add server mode
	MCPServersEditMode     bool   // Whether in edit server mode
	MCPServersDeleteMode   bool   // Whether in delete confirmation mode
	MCPServersDeleteName   string // Name of server being deleted (for confirmation)

	// Add server form state
	AddFormField    int
	AddFormName     string
	AddFormType     string // "stdio", "sse", "http", "oauth"
	AddFormCommand  string
	AddFormArgs     string
	AddFormURL      string
	AddFormHeaders  string
	AddFormClientID string
	AddFormScopes   string
	AddFormEnv      string
	AddFormWorkDir  string
	AddFormTimeout  string

	// System Prompt specific state
	SystemPromptState        string // "list", "action_menu", "create", "edit", "preview"
	SystemPromptSelected     int    // Selected prompt index in list (-1 = "New Prompt" option)
	SystemPromptActionChoice int    // Which action in the menu (0=Edit, 1=Activate, 2=Delete, 3=Preview)
	SystemPromptEditingField int    // 0=name, 1=content
	SystemPromptFormName     string
	SystemPromptFormContent  string
	SystemPromptCursorPos    int
	SystemPromptScrollOffset int
	SystemPromptContentLines []string // For multi-line editing

	// Hooks specific state
	HooksState        string // "main", "chat", "action_menu", "edit", "templates"
	HooksSelected     int    // Selected hook index in current panel
	HooksActionChoice int    // Which action in the menu
	HooksEditingField int    // 0=name, 1=event, 2=command, 3=tool_matcher, 4=path_allowlist, 5=path_denylist, 6=action, 7=timeout
	HooksEditingMode  string // "create" or "edit"
	HooksEditingScope string // "global" or "project"
	HooksFormName     string
	HooksFormEvent    string
	HooksFormCommand  string
	HooksCursorPos    int

	// Hooks panel navigation (MCP-style split view)
	HooksFocusedPanel        string // "buttons", "global", "project"
	HooksSelectedButton      int    // 0=Templates, 1=AI Assistant, 2=Reload, 3=New Hook
	HooksScrollOffsetGlobal  int    // Scroll offset for global hooks panel
	HooksScrollOffsetProject int    // Scroll offset for project hooks panel
	HooksSelectedGlobal      int    // Selected hook in global panel
	HooksSelectedProject     int    // Selected hook in project panel

	// Hooks template picker
	HooksTemplateIdx int // Selected template index in template picker

	// Hooks form fields for editing
	HooksFormToolMatcher   string
	HooksFormPathAllowlist string
	HooksFormPathDenylist  string
	HooksFormAction        string
	HooksFormTimeout       string
	HooksFormDescription   string
	HooksFormEnabled       bool

	// Hooks chat state
	HooksChatInput        string   // Current chat input
	HooksChatHistory      []string // Chat history for display (alternating user/assistant)
	HooksChatScrollOffset int      // Scroll offset for chat history
	HooksChatWaiting      bool     // Whether waiting for AI response

	// Context settings state
	ContextSelectedItem   int    // Which source is selected
	ContextState          string // "list", "detail", "mcp_servers", "mcp_picker", "mcp_prompt_args"
	ContextEditTarget     string // "global" or "project"
	ContextDetailSourceID string

	// Context MCP picker state
	ContextMCPServerIndex    int
	ContextMCPSelectedServer string
	ContextMCPTab            int // 0=resources, 1=prompts
	ContextMCPResourceIndex  int
	ContextMCPPromptIndex    int
	ContextMCPFilter         string
	ContextMCPFilterMode     bool
	ContextMCPArgsServer     string
	ContextMCPArgsPromptName string
	ContextMCPArgsPromptArgs []mcp.PromptArgument
	ContextMCPArgsValues     map[string]string
	ContextMCPArgsSelected   int

	// Agents settings state
	AgentsState        string // "list", "action_menu", "create", "edit", "preview"
	AgentsSelected     int    // Selected agent index in list (-1 = "New Agent" option)
	AgentsActionChoice int    // Which action in the menu (0=Edit, 1=SetDefault, 2=Clone, 3=Delete, 4=Preview)
	AgentsFormTab      int    // Current form tab (0=Basic, 1=Tools, 2=Hooks, 3=Capabilities) - DEPRECATED, keeping for compat
	AgentsFormField    int    // Current field in form (0-8: ID, Name, Desc, Provider, Model, SysPrompt, Tools, Hooks, Save)
	AgentsCursorPos    int    // Cursor position in text fields
	AgentsScrollOffset int    // Scroll offset for lists
	AgentsFormEditing  bool   // Whether currently editing a text field

	// Agents form data
	AgentsFormID           string
	AgentsFormName         string
	AgentsFormDescription  string
	AgentsFormProvider     string
	AgentsFormModel        string
	AgentsFormSystemPrompt string
	AgentsFormTools        []string // Selected tool names
	AgentsFormHooks        []string // Selected hook names
	AgentsFormMaxTokens    int
	AgentsFormTemperature  float64
	AgentsFormMaxTurns     int
	AgentsFormTimeout      int
	AgentsFormProfileID    string // Profile to use (empty = active profile)
	AgentsFormRoleAlias    string // Role alias designation (empty = none)
	AgentsFormIcon         string // Agent icon emoji
	AgentsFormColor        string // Agent color hex
	AgentsFormUseOverride  bool   // When false: agent uses profile model. When true: explicit provider+model

	// Agents tool/hook selection state
	AgentsToolsScrollOffset int
	AgentsToolsSelected     int
	AgentsHooksScrollOffset int
	AgentsHooksSelected     int
	AgentsToolsFilter       string // Search filter for tools
	AgentsHooksFilter       string // Search filter for hooks
	AgentsToolsFiltering    bool   // Whether in filter input mode
	AgentsFormError         string // Inline validation error message

	// Agents chat state (AI assistant)
	AgentsChatInput        string   // Current chat input
	AgentsChatHistory      []string // Chat history for display (alternating user/assistant)
	AgentsChatScrollOffset int      // Scroll offset for chat history
	AgentsChatWaiting      bool     // Whether waiting for AI response

	// MCP Chat state
	MCPChatInput        string   // Current chat input
	MCPChatHistory      []string // Chat history for display (alternating user/assistant)
	MCPChatScrollOffset int      // Scroll offset for chat history
	MCPChatWaiting      bool     // Whether waiting for AI response

	// Agent Profiles settings state
	ProfilesState        string // "list", "action_menu", "edit", "edit_role", "preview"
	ProfilesSelected     int    // Selected profile index in list (-1 = "New Profile" option)
	ProfilesActionChoice int    // Which action in the menu (0=Edit, 1=SetDefault, 2=Clone, 3=Delete, 4=Preview)
	ProfilesFormField    int    // Current field in edit form (0=ID, 1=Name, 2=Desc, 3=Icon, 4=Color, 5=ConfigureRoles, 6=Save)
	ProfilesRoleEditing  string // Which role is being edited (ModelAlias string value)
	ProfilesRoleField    int    // Current field in role edit (0=Provider, 1=Model, 2=SystemPrompt, 3=MaxTokens, 4=Temperature, 5=MaxTurns, 6=Timeout, 7=Save)
	ProfilesCursorPos    int    // Cursor position in text fields
	ProfilesScrollOffset int    // Scroll offset for lists
	ProfilesFormEditing  bool   // Whether currently editing a text field

	// Profile form data (metadata)
	ProfilesFormID          string
	ProfilesFormName        string
	ProfilesFormDescription string
	ProfilesFormIcon        string
	ProfilesFormColor       string

	// Profile role configuration data
	ProfilesRoleProvider     string
	ProfilesRoleModel        string
	ProfilesRoleSystemPrompt string
	ProfilesRoleMaxTokens    int
	ProfilesRoleTemperature  float64
	ProfilesRoleMaxTurns     int
	ProfilesRoleTimeout      int

	// Profile feedback messages
	ProfilesErrorMessage   string // Error message to show user
	ProfilesSuccessMessage string // Success message to show user
	ProfilesMessageTimeout int    // Frames to show message (decrements each render)

	// Skills settings state
	SkillsState          string // "list", "detail", "search", "install"
	SkillsSelected       int    // Selected skill index
	SkillsScrollOffset   int    // Scroll offset for list
	SkillsSearchQuery    string // Current search query
	SkillsSearchSelected int    // Selected search result index
	SkillsSearchOffset   int    // Scroll offset for search results
	SkillsDetailTab      int    // Detail view tab (0=Info, 1=Scripts, 2=Refs)
	SkillsCursorPos      int    // Cursor position in search input

	// Plugins settings state
	PluginsState               string // "list", "detail", "search", "marketplace"
	PluginsSelected            int    // Selected plugin index
	PluginsScrollOffset        int    // Scroll offset for list
	PluginsSearchQuery         string // Current search query
	PluginsSearchSelected      int    // Selected search result index
	PluginsSearchOffset        int    // Scroll offset for search results
	PluginsDetailTab           int    // Detail view tab (0=Info, 1=Commands, 2=Agents, 3=MCP/LSP)
	PluginsCursorPos           int    // Cursor position in search input
	PluginsMarketplaceSelected int    // Selected marketplace source
	// Dream memory browser state
	DreamState           string // "" = settings, "memories" = memory browser
	DreamMemorySelected  int    // Selected memory entry index in browser
	DreamMemoryScrollOff int    // Scroll offset for memory list
	// Updates settings state
	UpdatesModeSelected     int    // Selected mode in dropdown (0=prompt, 1=automatic, 2=manual, 3=disabled)
	UpdatesChannelSelected  int    // Selected channel (0=stable, 1=beta, 2=nightly)
	UpdatesIntervalSelected int    // Selected check interval (0=1h, 1=6h, 2=24h, 3=never)
	UpdatesChecking         bool   // Currently checking for updates
	UpdatesLastCheck        string // Formatted last check time
	UpdatesCurrentVersion   string // Current TUI version
	UpdatesLatestVersion    string // Latest available version
	UpdatesAvailable        bool   // Whether an update is available

	// Vault settings state
	VaultScope          string // "global" or "project"
	VaultState          string // "locked", "unlocking", "unlocked", "error"
	VaultCursorPos      int    // Cursor position in passphrase input
	VaultPassphrase     string // Passphrase buffer (cleared after unlock)
	VaultSelectedCred   int    // Selected credential index
	VaultScrollOffset   int    // Scroll offset for credential list
	VaultUnlockingScope string // Which scope is being unlocked ("global" or "project")
}

// NewState creates initial settings state
func NewState() *State {
	return &State{
		SelectedSection:                     SectionModels, // Start on Models (unified: profiles + providers)
		SelectedItem:                        0,
		ScrollOffset:                        0,
		MaxVisible:                          10,
		Focus:                               FocusSidebar,
		CollapsedGroups:                     make(map[string]bool),
		SearchQuery:                         "",
		SearchActive:                        false,
		SecuritySelected:                    -2, // Start on the level row
		SecurityScrollOffset:                0,
		SecurityLevelSelected:               0,
		SecurityTab:                         0,
		SecuritySearchQuery:                 "",
		SecuritySearchActive:                false,
		SecurityRulesState:                  "list",
		SecurityRuleActionChoice:            0,
		SecurityAddFormField:                0,
		SecurityAddFormTool:                 "Bash",
		SecurityAddFormPattern:              "",
		SecurityAddFormPolicy:               "allow",
		SecurityAddFormWorkspace:            false,
		SecurityAddCursorPos:                0,
		AdvancedSubsection:                  AdvancedMain,
		MCPState:                            "main",
		MCPSelectedButton:                   0,
		MCPFocusedPanel:                     "buttons",
		MCPScrollOffset:                     0,
		MCPScrollOffsetEnabled:              0,
		MCPScrollOffsetDisabled:             0,
		MCPConfigureToolsSelected:           0,
		MCPConfigureToolsScrollOffset:       0,
		MCPConfigureToolsExpandedCategories: make(map[string]bool),
		MCPConfigureToolsExpandedServers:    make(map[string]bool),
		MCPConfigureToolsViewMode:           "categorized", // Default to categorized view
		MCPServersSelected:                  0,
		MCPServersScrollOffset:              0,
		MCPServersAddMode:                   false,
		MCPServersEditMode:                  false,
		AddFormField:                        0,
		AddFormType:                         "stdio", // Default to stdio type
		SystemPromptState:                   "list",
		SystemPromptSelected:                -1, // Start on "New Prompt"
		SystemPromptActionChoice:            0,
		SystemPromptEditingField:            0,
		SystemPromptCursorPos:               0,
		SystemPromptScrollOffset:            0,
		SystemPromptContentLines:            []string{},
		HooksState:                          "main",
		HooksSelected:                       0,
		HooksActionChoice:                   0,
		HooksEditingField:                   0,
		HooksEditingMode:                    "edit",
		HooksEditingScope:                   "",
		HooksCursorPos:                      0,
		HooksFocusedPanel:                   "global",
		HooksSelectedButton:                 0,
		HooksScrollOffsetGlobal:             0,
		HooksScrollOffsetProject:            0,
		HooksSelectedGlobal:                 0,
		HooksSelectedProject:                0,
		HooksFormToolMatcher:                "",
		HooksFormPathAllowlist:              "",
		HooksFormPathDenylist:               "",
		HooksFormAction:                     "block_exit2",
		HooksFormTimeout:                    "60s",
		HooksFormDescription:                "",
		HooksFormEnabled:                    true,
		HooksChatInput:                      "",
		HooksChatHistory:                    []string{},
		HooksChatScrollOffset:               0,
		HooksChatWaiting:                    false,
		ContextSelectedItem:                 0,
		ContextState:                        "list",
		ContextEditTarget:                   "global",
		ContextDetailSourceID:               "",
		ContextMCPServerIndex:               0,
		ContextMCPSelectedServer:            "",
		ContextMCPTab:                       0,
		ContextMCPResourceIndex:             0,
		ContextMCPPromptIndex:               0,
		ContextMCPFilter:                    "",
		ContextMCPFilterMode:                false,
		ContextMCPArgsServer:                "",
		ContextMCPArgsPromptName:            "",
		ContextMCPArgsPromptArgs:            nil,
		ContextMCPArgsValues:                map[string]string{},
		ContextMCPArgsSelected:              0,

		// Agents settings initialization
		AgentsState:             "list",
		AgentsSelected:          -1, // Start on "New Agent"
		AgentsActionChoice:      0,
		AgentsFormTab:           0,
		AgentsFormField:         0,
		AgentsCursorPos:         0,
		AgentsScrollOffset:      0,
		AgentsFormEditing:       false,
		AgentsFormID:            "",
		AgentsFormName:          "",
		AgentsFormDescription:   "",
		AgentsFormProvider:      "anthropic",
		AgentsFormModel:         "",
		AgentsFormSystemPrompt:  "",
		AgentsFormTools:         []string{},
		AgentsFormHooks:         []string{},
		AgentsFormMaxTokens:     8192,
		AgentsFormTemperature:   0.7,
		AgentsFormMaxTurns:      20,
		AgentsFormTimeout:       900,
		AgentsToolsScrollOffset: 0,
		AgentsToolsSelected:     0,
		AgentsHooksScrollOffset: 0,
		AgentsHooksSelected:     0,
		AgentsToolsFilter:       "",
		AgentsHooksFilter:       "",
		AgentsChatInput:         "",
		AgentsChatHistory:       []string{},
		AgentsChatScrollOffset:  0,
		AgentsChatWaiting:       false,

		// MCP Chat initialization
		MCPChatInput:        "",
		MCPChatHistory:      []string{},
		MCPChatScrollOffset: 0,
		MCPChatWaiting:      false,

		// Agent Profiles settings initialization
		ProfilesState:    "list",
		ProfilesSelected: 0, // Start on first profile

		// Skills settings initialization
		SkillsState:          "list",
		SkillsSelected:       0,
		SkillsScrollOffset:   0,
		SkillsSearchQuery:    "",
		SkillsSearchSelected: 0,
		SkillsSearchOffset:   0,
		SkillsDetailTab:      0,
		SkillsCursorPos:      0,

		// Plugins settings initialization
		PluginsState:               "list",
		PluginsSelected:            0,
		PluginsScrollOffset:        0,
		PluginsSearchQuery:         "",
		PluginsSearchSelected:      0,
		PluginsSearchOffset:        0,
		PluginsDetailTab:           0,
		PluginsCursorPos:           0,
		PluginsMarketplaceSelected: 0,
		// Updates settings initialization
		UpdatesModeSelected:     0, // prompt
		UpdatesChannelSelected:  0, // stable
		UpdatesIntervalSelected: 1, // 6h
		UpdatesChecking:         false,
		UpdatesLastCheck:        "Never",
		UpdatesCurrentVersion:   "",
		UpdatesLatestVersion:    "",
		UpdatesAvailable:        false,
		// Vault settings initialization
		VaultScope:          "global",
		VaultState:          "locked",
		VaultCursorPos:      0,
		VaultPassphrase:     "",
		VaultSelectedCred:   0,
		VaultScrollOffset:   0,
		VaultUnlockingScope: "",
	}
}
