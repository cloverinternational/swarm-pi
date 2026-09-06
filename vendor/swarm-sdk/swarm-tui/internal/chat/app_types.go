package chat

import (
	"context"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/interaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/bgprocess"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/appshell"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/prerender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/components/gitpanel"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/metrics"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/termimage"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/update"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/visual"
)

// ============================================================================
// LAYOUT CONSTANTS
// ============================================================================

const (
	// UI component sizing
	ButtonWidth = 20
	ButtonGap   = 2

	// Content width is now calculated dynamically using breakpoints
	// See contentWidth() function for responsive width logic
)

// Two-pane layout constants
const (
	SidebarPane  = 0
	MessagesPane = 1
)

// Usage tab sub-tabs
const (
	UsageSubTabSubscription = 0 // Subscription usage (rate limits, credits)
	UsageSubTabConsumption  = 1 // Token consumption (totals, cache split, trend)
	UsageSubTabDiagnostics  = 2 // Input spikes and cache breaks
)

// ============================================================================
// DATA TYPES
// ============================================================================

// ClickableRegion represents a clickable area on screen
type ClickableRegion struct {
	ElementType string // "conversation", "button", "input", etc.
	ElementID   string // "conv_0", "btn_send", etc.
	StartY      int    // First line of this region
	EndY        int    // Last line of this region
	StartX      int    // Column start
	EndX        int    // Column end
}

// Message represents a chat message
type Message struct {
	Role      string
	Content   string
	Timestamp time.Time

	// Extended fields for tool use and metadata
	ToolCalls   []ToolCallDisplay
	ToolResults []ToolResultDisplay
	Thinking    string // Extended thinking content
	RawPayload  []byte // Principle 3: State Preservation - original provider JSON
	Metadata    map[string]any
	A2A         *conversation.A2AMetadata

	// System message sections - decomposed system prompt architecture
	// Each section is independently collapsible with its own styling
	Sections []SystemMessageSection

	// Attachments (images, files, etc.)
	Attachments []Attachment

	// Images are user-pasted images attached to this message.
	// These are persisted in metadata["images"] and restored when loading conversations.
	// This is separate from Attachments which is used for tool output attachments.
	Images []ImageAttachment

	// Ordered blocks for proper interleaved rendering
	// This tracks the sequence: text → tool call → result → text → etc.
	OrderedBlocks []MessageBlock

	// Token usage per message (from SDK)
	InputTokens  int // Input tokens for this message
	OutputTokens int // Output tokens for this message

	// Completion info (set when agent finishes)
	IsComplete  bool          // True when response is complete
	ElapsedTime time.Duration // How long the agent worked
	Model       string        // Model used for this response

	// Bash command fields
	IsBashCommand bool        // Indicates if this is a bash command
	BashResult    *BashResult // Stores bash execution results
	IsLoading     bool        // Indicates if message is loading (for bash commands)

	// Unified tool render cache for pre-processed results (syntax highlighting, diffs, etc).
	// Populated by toolRegistry.PreProcess(), consumed by toolRegistry.Render().
	CachedToolResults map[string]toolrender.CachedResult // Key: tool call ID

	// Pre-rendered workflow content: skip wrapText/renderMarkdown in message renderer
	IsPreRendered bool
	// Per-message dirty tracking for incremental rendering
	dirty           bool                // True if this message needs re-rendering
	preRenderMutex  *sync.RWMutex       // Protects pre-rendered content (pointer to allow copying)
	preRenderLines  []string            // Pre-rendered lines for fast consumption
	preRenderImages []nativeImageAnchor // Native image anchors relative to preRenderLines
	preRenderWidth  int                 // Viewport width preRenderLines were wrapped at (0 = unknown)

	// Alternate width slot. Toggling the side panel flips the viewport between
	// exactly two widths, and wrapping is a pure function of (content, width).
	// Keeping the previous width's wrap makes toggling back a pointer swap
	// instead of a full markdown re-render of the entire transcript.
	altPreRenderWidth  int
	altPreRenderLines  []string
	altPreRenderImages []nativeImageAnchor

	// Error lineage metadata for failed assistant responses.
	ErrorLineage *errorLineagePanel

	// Builders for efficient streaming (avoid repeated allocations)
	contentBuilder  *strings.Builder
	thinkingBuilder *strings.Builder
}

// nativeImageAnchor describes an image rectangle in a rendered message. Line
// is relative to Message.preRenderLines; updateViewportIncremental translates
// it into the MessageList's raw transcript coordinate space.
type nativeImageAnchor struct {
	OccurrenceKey string
	SourceKey     string
	FileName      string
	MimeType      string
	Line          int
	Column        int
	Columns       int
	Rows          int
	PixelWidth    int
	PixelHeight   int
	Placement     termimage.Placement
}

// ============================================================================
// SYSTEM MESSAGE SECTIONS - Decomposed system prompt architecture
// ============================================================================

// SectionType identifies the type of content in a system message section
type SectionType string

const (
	SectionBasePrompt SectionType = "base_prompt" // Core system prompt/instructions
	SectionGitContext SectionType = "git_context" // Git branch, status, workflow
	SectionTools      SectionType = "tools"       // Available tools for current mode
	SectionMemories   SectionType = "memories"    // Persistent project memories
	SectionSkills     SectionType = "skills"      // Available/active skills (<available_skills>)
	SectionMode       SectionType = "mode"        // Operating mode configuration
	SectionContext    SectionType = "context"     // An injected <context name="…"> block
)

// SystemMessageSection represents a collapsible section within the system message
// Each section has its own header, styling, and collapse state
type SystemMessageSection struct {
	ID        string      // Unique identifier (e.g., "base-prompt", "git-context")
	Type      SectionType // Type of section for styling and behavior
	Title     string      // Display title (e.g., "System Instructions")
	Icon      string      // Emoji or nerd font icon (e.g., "◆", "󰊢")
	Content   string      // Raw content, shown verbatim (not markdown-parsed)
	LineCount int         // Number of content lines
	CharCount int         // Number of content characters (for "[N chars]" display)
	Collapsed bool        // Per-section collapse state
	IsEmpty   bool        // Skip rendering if true
	Priority  int         // Render order (lower = first)
}

// HasSections returns true if this message has decomposed sections
func (m *Message) HasSections() bool {
	return len(m.Sections) > 0
}

// GetSection retrieves a section by ID, or nil if not found
func (m *Message) GetSection(id string) *SystemMessageSection {
	for i := range m.Sections {
		if m.Sections[i].ID == id {
			return &m.Sections[i]
		}
	}
	return nil
}

// GetSectionByType retrieves the first section of a given type
func (m *Message) GetSectionByType(t SectionType) *SystemMessageSection {
	for i := range m.Sections {
		if m.Sections[i].Type == t {
			return &m.Sections[i]
		}
	}
	return nil
}

// BuildContentFromSections concatenates all section content into Message.Content
// Used for backward compatibility and LLM API calls
func (m *Message) BuildContentFromSections() string {
	if !m.HasSections() {
		return m.Content
	}

	var parts []string
	for _, section := range m.Sections {
		if !section.IsEmpty && section.Content != "" {
			if section.Type == SectionGitContext {
				parts = append(parts, "## Git Context\n"+section.Content)
			} else {
				parts = append(parts, section.Content)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// AppendContent appends content efficiently using a builder during streaming
func (m *Message) AppendContent(content string) {
	if m.contentBuilder == nil {
		m.contentBuilder = &strings.Builder{}
		m.contentBuilder.WriteString(m.Content)
	}
	m.contentBuilder.WriteString(content)
}

// GetContent returns the accumulated content
func (m *Message) GetContent() string {
	if m.contentBuilder != nil {
		return m.contentBuilder.String()
	}
	return m.Content
}

// FinalizeContent converts builder to string
func (m *Message) FinalizeContent() {
	if m.contentBuilder != nil {
		m.Content = m.contentBuilder.String()
		m.contentBuilder = nil
	}
}

// AppendThinking appends thinking content efficiently
func (m *Message) AppendThinking(content string) {
	if m.thinkingBuilder == nil {
		m.thinkingBuilder = &strings.Builder{}
		m.thinkingBuilder.WriteString(m.Thinking)
	}
	m.thinkingBuilder.WriteString(content)
}

// GetThinking returns the accumulated thinking content
func (m *Message) GetThinking() string {
	if m.thinkingBuilder != nil {
		return m.thinkingBuilder.String()
	}
	return m.Thinking
}

// FinalizeThinking converts builder to string
func (m *Message) FinalizeThinking() {
	if m.thinkingBuilder != nil {
		m.Thinking = m.thinkingBuilder.String()
		m.thinkingBuilder = nil
	}
}

// MessageLinePosition tracks the line range for each message in the viewport
type MessageLinePosition struct {
	MessageIdx int
	StartLine  int
	EndLine    int
}

// Conversation represents a chat conversation
type Conversation struct {
	ID              string
	Title           string
	Preview         string
	FirstUserPrompt string // Cached first user prompt from ConversationSummary
	Recap           string // LLM-generated free-text summary (from ConversationSummary.Recap; may be empty)
	Status          string // "idle", "thinking", "waiting", "streaming"
	LastMessage     time.Time
	MessageCount    int
	TotalTokens     int // Cached token count - calculated on load, not on render
	InputTokens     int // Total input tokens across all messages
	OutputTokens    int // Total output tokens across all messages
	AgentThought    string
	WaitingFor      string
	IsActive        bool   // Is the AI currently processing?
	InputBuffer     string // Save input when switching away
	ContinueInBg    bool   // Should continue running in background
	Branch          string // Git branch associated with this conversation (empty = no branch)

	// Model tracking
	Model      string   // Most recent assistant model
	ModelsUsed []string // All unique models used (ordered by first appearance)
	CostUSD    float64  // Total cost in USD (from SDK)
	WallTime   time.Duration // Cumulative agent walltime, persisted across compaction

	// Lineage: forking and compaction tracking
	ForkedFrom    string // Parent conversation ID (if forked via edit)
	ForkPoint     int    // Message index where fork occurred
	CompactedFrom string // Original conversation ID (if compacted)

	// Compaction details
	CompactionCount int // Number of times this conversation was compacted
	ToolCallCount   int // Total tool calls across all messages
}

// BranchFilter represents filtering mode for conversations
type BranchFilter int

const (
	FilterAll BranchFilter = iota
	FilterCurrentBranch
	FilterNoBranch
)

// PaneType represents the type of content in a pane
type PaneType int

const (
	PaneChat PaneType = iota
	PaneEmpty
)

// Pane represents a screen area with specific content (ONLY chat panes, sidebar is fixed)
type Pane struct {
	Type          PaneType
	X, Y          int
	Width, Height int
	ConvID        string // Which conversation is displayed in this pane
	Focused       bool   // Is this pane focused?

	// Per-pane conversation state (for parallel execution support)
	// These store state when switching between panes
	Messages         []Message    // Messages for this pane's conversation
	Viewport         *MessageList // Each pane has its own optimized viewport (replaces ScrollOffset)
	TokenCount       int          // Token count for this pane
	TokenCountIsEst  bool         // Is the token count an estimate
	LastRealTokens   int          // Last real token count
	StreamingMessage bool         // Is this pane streaming
	InputBuffer      string       // Saved input for this pane
}

// SplitDirection represents how a container is split
type SplitDirection int

const (
	SplitHorizontal SplitDirection = iota // Left/Right
	SplitVertical                         // Top/Bottom
)

// Split represents a container divided into two parts
type Split struct {
	Direction SplitDirection
	Ratio     float64 // 0.0-1.0, size of first pane
	First     *PaneContainer
	Second    *PaneContainer
}

// PaneContainer is either a Pane or a Split
type PaneContainer struct {
	Pane  *Pane
	Split *Split
}

// Workspace represents a layout configuration
type Workspace struct {
	ID        int
	Name      string
	Root      *PaneContainer
	FocusPath []int // Path to focused pane
}

// ============================================================================
// APP STATE
// ============================================================================

// Screen represents the current screen
type Screen int

const (
	ScreenHome Screen = iota
	ScreenChats
	ScreenChat
	ScreenSettings // Deprecated: settings now renders inline in ScreenHome via ButtonSettings
	ScreenViewer
	ScreenNewChatUI // Experimental: new modular chat UI
	ScreenGit       // Git TUI panel (OctoGit)
)

// HomeButton represents a button on the home screen
type HomeButton int

const (
	ButtonNewChat HomeButton = iota
	ButtonConversations
	ButtonInbox
	ButtonUsage // Usage statistics screen
	ButtonViewer
	ButtonGit // Git TUI button
	ButtonSettings
	ButtonNewChatUI // Debug: test new modular chat UI
)

// rawEventCallbackWrapper implements provider.RawEventCallback
type rawEventCallbackWrapper struct {
	debugScreen *DebugScreen
}

func (r *rawEventCallbackWrapper) OnRawEvent(eventType string, rawData string) {
	if r.debugScreen != nil {
		r.debugScreen.AddRawEvent(eventType, rawData)
	}
}

// ScrollAnchor saves information needed to restore viewport position after content changes.
// NEW APPROACH: Instead of tracking line counts, we track WHICH MESSAGE and WHERE within that message
// the viewport anchor was, then find it again after re-render. This handles width changes, word wrapping,
// tool output expansion/contraction, etc. properly.
type ScrollAnchor struct {
	// Original viewport state
	YOffset     int  // Current viewport scroll position
	WasAtBottom bool // Was user at bottom?

	// Content-based anchor for re-finding position after render
	AnchorMessageIndex  int // Which message was at the anchor point (0 = system prompt)
	AnchorLineInMessage int // Which line within that message (0-indexed)
	AnchorVisualLine    int // What visual line in viewport was this (to maintain same visual position)
}

// App is the main chat application state
type App struct {
	// Viewport
	width  int
	height int

	// Bootstrap shell. The shell is created synchronously and remains interactive
	// while the complete SDK/provider runtime is built by a Bubble Tea command.
	bootstrapPending       bool
	bootstrapGeneration    uint64
	bootstrapStartedAt     time.Time
	bootstrapNotifications []Notification

	// Feature flags
	useNewUI            bool               // Experimental: use new modular chat UI rendering
	debugLineageFixture bool               // Deterministic lineage fixture mode for visual testing
	chatPanel           *chatui.Panel      // New modular chat panel (used when useNewUI is true)
	chatScreen          *chatui.ChatScreen // New modular chat screen (full UI)

	// Easter egg mode: 1/10 chance of AutoVac mode (Asimov Multivac tribute)
	// When enabled, replaces Swarm branding with Multivac/AutoVac theme
	autovacMode       bool   // True when running in AutoVac Easter egg mode
	autovacLogoText   string // The ASCII logo text (SWARM or MULTIVAC)
	autovacBrandName  string // Brand name shown in UI ("Swarm" or "Multivac")
	currentSplashText string // The selected splash text (chosen once at startup)

	// Git TUI panel (OctoGit integration)
	gitPanel *gitpanel.Model // Git operations panel

	// Navigation
	screen              Screen
	selectedIdx         int
	scrollOffset        int // Chat message viewport scroll offset
	previewScrollOffset int // Conversation preview scroll offset
	homeButton          HomeButton
	tabBar              *appshell.TabBar // Shared top navigation bar

	// Theme & Data
	hasDarkTerminal bool // defaults dark, then updates from Bubble Tea's async background-color response
	theme           Theme
	conversations   []Conversation
	activeConv      *Conversation
	messages        []Message

	// Per-message render cache for high-performance scrolling (Crush technique)
	messageCache *MessageRenderCache

	// Streaming state — controls debouncing and active message detection
	streamingInProgress bool // True when actively streaming a message

	// Task completion tracking for terminal title notifications
	lastStreamingEndTime time.Time // When streaming last ended (task completed)
	completionNotified   bool      // Whether we've sent a bell for this completion

	// Input protection during streaming (prevents cursor/state disruption)
	inputProtection struct {
		lastUpdateTime time.Time     // Last time viewport was updated
		debounceDelay  time.Duration // Minimum time between viewport updates
		pendingUpdate  bool          // True if update is debounced and pending
	}

	// Input (custom component compatible with bubbletea v2)
	textInput          *SimpleInput
	msgViewport        *MessageList
	currentAttachments []Attachment  // Current attachments being composed
	inputHistory       *InputHistory // Command history for up/down navigation

	// Message navigation mode
	messageNavMode              bool                  // Tab to enable line-by-line scroll mode with message focus tracking
	focusedMessageIdx           int                   // Which message is currently focused (updated based on scroll position)
	messageLinePositions        []MessageLinePosition // Cache of line positions for each message
	lastRenderedImageAnchors    []nativeImageAnchor   // Image metadata from the latest single-message render
	imageCapabilityQueryPending bool                  // Set only after this runtime sends Kitty query + DA
	imageCellPixelWidth         int                   // Terminal cell width in px from CSI 16t (0 = unknown, use default)
	imageCellPixelHeight        int                   // Terminal cell height in px from CSI 16t (0 = unknown, use default)
	imageCacheMu                sync.Mutex            // Protects prepared image bytes shared by render paths
	imageCache                  map[string]imageCacheEntry
	imageCacheBytes             int
	imageCacheClock             uint64

	// Word playback is transient UI state for replaying the latest agent message.
	wordPlayback wordPlaybackState

	// Edit message mode (conversation branching)
	editMessageMode     bool  // True when user is selecting a message to edit
	editMessageIdx      int   // Index in a.messages of the currently highlighted user message
	editMessageUserIdxs []int // Cached list of indices of user messages (for fast navigation)

	// Slash commands (deprecated - keeping for compatibility)
	cmdRegistry     *commands.Registry
	cmdAutocomplete *commands.Autocomplete
	activeCommand   commands.Command

	// Daemon attach (tmux-like SSE streaming)
	sseState            *sseAttachmentState
	attachScreenFactory func() *AttachScreen
	attachScreen        *AttachScreen

	// Mention autocomplete (triggered by @) - unified agents, tmux, files
	mentionAutocomplete *MentionAutocomplete
	inputOverlayY       int // Rendered input start line for overlay positioning

	// Input-box mouse selection: multi-click tracking for word/line select.
	inputLastClickTime time.Time
	inputLastClickX    int
	inputLastClickY    int
	inputClickStreak   int // 1=single, 2=double, 3=triple

	// Modal dialog
	activeModal       *Modal
	approvalModal     *ApprovalModal
	approvalQueue     []tools.PermissionApprovalRequest
	autoModeVerdicts  map[string]autoModeVerdict // auto-mode classification by requestID
	taskActivityModal *AgentTaskModal            // Ctrl+T agent task viewer
	questionModal     *QuestionModal
	questionQueue     []interaction.QuestionRequest

	// Command palette (Ctrl+P)
	commandPalette *CommandPalette

	// Quick-access modal switchers
	modelSwitcher   ModelSwitcher
	agentSwitcher   AgentSwitcher
	promptSwitcher  PromptSwitcher
	profileSwitcher ProfileSwitcher
	skillPicker     SkillPicker

	// pendingExhaustionResponse is non-nil when the agent's fallback chain
	// has been fully exhausted and the profile switcher is open waiting for
	// the user to pick an alternative profile. Sending a FallbackDecision
	// unblocks the agent's onExhausted goroutine so execution can resume.
	pendingExhaustionResponse chan agent.FallbackDecision

	// pendingSubAgentExhaustion tracks sub-agents that have exhausted their
	// fallback chain and are waiting for the user to pick a new profile.
	// Key is the sub-agent ID (AgentID from SubAgentUpdate).
	// Value is the response channel to send FallbackDecision back to the sub-agent.
	pendingSubAgentExhaustion map[string]chan agent.FallbackDecision

	// Home screen input (OpenCode-style)
	homeInput            *SimpleInput
	homeInputFocused     bool
	homeInputOverlayY    int // Absolute Y position of input box for autocomplete overlay
	homeInputOverlayX    int // X offset for autocomplete (accounts for sidebar)
	homeSidebarCollapsed bool

	// Home sidebar data cache — prevents shelling out to git on every render frame
	homeSidebarCache struct {
		valid         bool
		lastRefresh   time.Time
		commits       []string
		currentBranch string
		headFiles     string // e.g. "20"
		headIns       string // e.g. "+1714"
		headDel       string // e.g. "-725"
		modifiedFiles []modifiedFileEntry
		branchParents map[string]string // branch → parent (just current)
	}

	// Side panel
	showSidePanel bool

	// Live-terminal change tracking. bgTerminalSigs holds the last seen output
	// signature per background task ID so the bg tick only redraws blocks whose
	// output actually moved; bgTerminalHeartbeatAt bounds how long a quiet
	// block can go without a redraw so elapsed timers keep advancing.
	bgTerminalSigs        map[string]BackgroundProcessSignature
	bgTerminalHeartbeatAt time.Time

	// agentTickPending guards the agent drain-tick loop so at most one chain
	// of agentTickMsg is ever in flight. See scheduleAgentTick in
	// agent_tick.go for why the CAS lives inside the returned closure.
	agentTickPending int32

	// SidePanel render cache for performance optimization
	// Cache is invalidated when any of the tracked state changes
	sidePanelCache struct {
		rendered               string    // Cached rendered output
		valid                  bool      // Is cache valid?
		tokenCount             int       // Last token count
		mode                   string    // Last operating mode
		permissionLevel        string    // Last permission level
		width                  int       // Last panel width
		height                 int       // Last panel height
		toolCount              int       // Last tool count
		bgAgentCount           int       // Last background agent count
		bgAgentDigest          string    // Last background agent task/status/progress digest
		bgProcessCount         int       // Last background bash process count
		bashDigest             string    // Digest of BASH rows+tails (forces re-render on change)
		showThinking           bool      // Last thinking toggle state
		showVerbose            bool      // Last verbose toggle state
		hooksEnabled           bool      // Last hooks enabled state
		cachingOn              bool      // Last caching enabled state
		microCompactionOn      bool      // Last micro-compaction enabled state
		autoModeOn             bool      // Last auto-mode enabled state
		currentModel           string    // Last current model (for cache invalidation on model change)
		currentProvider        string    // Last current provider (for cache invalidation on provider change)
		currentModelDisplay    string    // Last model display name
		currentProviderDisplay string    // Last provider display name
		tpsCurrentValue        float64   // Last TPS current value (rounded)
		tpsPeakValue           float64   // Last TPS peak value (rounded)
		tpsAvgValue            float64   // Last TPS average value (rounded)
		tpsStreaming           bool      // Last TPS streaming state
		goalState              string    // Last /goal state
		goalIterations         int       // Last /goal iteration count
		goalReason             string    // Last /goal evaluation reason
		cacheHitRate           float64   // Last cache hit rate
		cacheHits              int64     // Last cache hits count
		cacheFailures          int64     // Last cache failures count
		todoCount              int       // Last todo item count
		usageFetchedAt         time.Time // Last time usage was fetched
		budgetToolCalls        int       // Last budget tool call count
		budgetSkilled          bool      // Last budget skilled state
		budgetNudgeIgnores     int       // Last budget nudge ignore count
		budgetEnabled          bool      // Last budget enabled state
		vaultUnlocked          bool      // Last agent-visible vault state
	}
	vaultChipWidth int // Rendered width of the right-aligned vault header chip.

	// Cached separator string to avoid strings.Repeat every frame
	cachedSeparator struct {
		str      string // The separator string "───────..."
		rendered string // Fully rendered separator (with FG, BG, Width)
		width    int    // Width it was created for
	}

	// Cached context-usage gauge line (bottom-separator variant). Keyed on
	// "width|pct|thresholdPct" so it only re-renders when those change.
	cachedCtxGauge struct {
		key      string
		rendered string
	}

	// Cached header rendering (invalidated when title/workflow/width change)
	cachedHeaderRendered string
	cachedHeaderKey      string // fmt.Sprintf("%s|%s|%d", title, workflowText, width)

	// Cached divider rendering
	cachedDividerRendered string
	cachedDividerWidth    int

	// Cached viewport rendering
	cachedViewportRendered   string
	cachedViewportContent    string
	cachedViewportWidth      int
	cachedViewportHeight     int
	cachedViewportLastMissAt time.Time // time of last viewport cache miss (for rapid-invalidation detection)

	// SimCity render model: only recompute frame when viewNeedsRefresh is true.
	// Streaming tokens update state but do NOT set this flag — they batch to the
	// next animation tick. User input and animation ticks set it immediately.
	viewNeedsRefresh bool   // set in Update for anim tick / input events
	lastRenderOutput string // cached final frame (post applyBG)
	lastWindowTitle  string // cached window title to prevent flicker

	// Frame cap. Bubble Tea calls View() once per message (tea.go:880) while
	// the terminal flush is already ticker-capped at 60fps (tea.go:1394), so
	// building frames faster than the flush rate is provably discarded work.
	// During scrolling the wheel event rate drove 267-350 View()/s.
	lastFrameBuiltAt  time.Time   // when the last full frame was composed
	frameDeferred     bool        // a frame was skipped; one is still owed
	frameFlushPending int32       // atomic: guards a single trailing flush tick
	screenLayer       *smartLayer // 60fps zero-cost layer: O(1) on unchanged frames

	// Pre-wrap: track the newest background wrap target. A history load can change
	// both content and geometry while an older wrap is still running, so a single
	// boolean is not sufficient: stale work must not block the current target.
	prewrapInFlight      bool
	prewrapInFlightWidth int
	prewrapInFlightHash  uint64
	prewrapRequestID     uint64

	// Status bar
	currentModel           string
	currentProvider        string
	currentModelDisplay    string // Friendly display name for model
	currentProviderDisplay string // Friendly display name for provider
	modelContextWindow     int    // Context window size for current model
	tokenCount             int
	tokenCountIsEstimate   bool              // True if tokenCount is estimated, false if real
	lastRealTokenCount     int               // Last known real token count from API
	showThinking           bool              // Toggle for showing thinking blocks
	showFullToolOutput     bool              // Toggle for showing full tool output (Ctrl+O)
	skipScrollRestoreOnce  bool              // Skip scroll restoration in updateViewportIncremental() once (for anchor restoration)
	renderSettings         *RenderSettings   // Tool output rendering customization
	settingsManager        *settings.Manager // New modular settings manager

	// Config Bundle Integration (unified global/project config management)
	configBundle *commands.ConfigBundleIntegration // Config bundle manager with project detection

	// Tool collapse management (for collapsible tool outputs)
	collapseManager  *CollapseManager     // Manages per-tool-call collapse states
	toolNameResolver *MCPToolNameResolver // Resolves MCP tool names to friendly display names
	collapseWidget   *CollapseWidget      // Renders collapse UI elements
	toolResultParser *ToolResultParser    // Extracts images from tool outputs

	// Error lineage UI state, persisted per conversation for this session.
	errorLineageExpandedByConv map[string]bool

	// SDK Integration
	appOptions            AppOptions
	initialPromptConsumed bool
	workspaceLease        *gitops.WorkspaceLease
	sdk                   *SDKIntegration
	permissionBroker      *PermissionsBroker
	permissionConfig      *PermissionConfig
	questionBroker        *QuestionBroker
	vaultUnlockBroker     *VaultUnlockBroker
	interactionBrkr       *TUIInteractionBroker
	visualReg             *visual.Registry
	a2aEnabled            bool
	a2aHandle             string
	a2aListenAddress      string
	// hub is the always-on workspace WebSocket hub for local peer discovery.
	// nil when WorkspaceRoot is empty or hub failed to start (falls back to SQLite).
	hub                  *a2a.WorkspaceHub
	currentConvID        string                // Current conversation ID in SDK
	rootSessionID        string                // Stable session ID (set once on first convID, never changed by compaction)
	metrics              *metrics.MetricsStore // Metrics tracking (TPS, cache stats)
	usageResult          *usageDataResult      // Last fetched usage data across providers
	usageLoading         bool                  // True while provider subscription usage is loading
	usageLoadGeneration  uint64                // Rejects stale async results from older refreshes
	usageLoadCancel      context.CancelFunc    // Cancels live lookups from an obsolete refresh generation
	usageSubTab          int                   // 0 = Subscription, 1 = Consumption
	streamingMessage     bool                  // Is currently streaming a response
	streamBuffer         string                // Buffer for streaming content
	activeToolActivities []toolActivity        // Currently-running tool descriptions for activity display
	activityState        *ActivityStateManager // Centralized visible turn phase + activity label state
	bgProcessTickActive  bool                  // Whether the bgProcessTickMsg chain is running
	pendingUserMessages  []string              // Messages queued by user while agent is running (FIFO)
	pendingMsgMu         sync.Mutex            // Protects pendingUserMessages AND pendingSystemMessages
	pendingNavIdx        int                   // Index into pendingUserMessages for Up-arrow editing (0 = not browsing)
	// pendingSystemMessages queues NON-USER notifications (background task done, background agent done,
	//	other async system events). They are flushed via a RichMessageInjector as conversation.RoleSystem
	//	messages so the model does NOT attribute them to the user. Survey 2026-04-27 identified this as a
	//	correctness bug: "[Background Task Notification]" blocks were being tagged [USER] in the transcript.
	pendingSystemMessages []string
	userScrolledAway      bool              // User has scrolled away from bottom during streaming
	viewportContentDirty  bool              // Content changed, viewport needs re-render
	preRenderBuffer       *prerender.Buffer // Background pre-rendering for heavy content
	agentStartTime        time.Time         // When agent started working (for elapsed time)
	messageSequence       int               // Current sequence number for message blocks during streaming

	// Real-time token estimation during streaming (Claude Code approach)
	streamingInputTokens int // Input tokens from message_start
	streamingOutputChars int // Cumulative output character count during streaming
	// Animation system - SINGLE clock for all animations
	animationClock *AnimationClock // Shared clock for all animation timing
	pendingAnimCmd tea.Cmd         // Pending animation cmd to return from next Update (set by async workflow launch)

	// Diffusion reveal animation for text-diffusion models (see diffusion_reveal.go)
	diffusionReveal diffusionRevealState

	// lastGoalEvalKey dedupes /goal evaluation notifications (state|iter|reason)
	lastGoalEvalKey string

	// Loading indicators for visual feedback
	loadingIndicator         *LoadingIndicator // Visual indicator when agent is working
	spinner                  *Spinner          // Main animated spinner
	taskPanel                TaskPanelModel    // Task panel above chat input (displays active tasks)
	debugScreen              *DebugScreen      // Debug screen for API inspection
	hooksDashboard           *HooksDashboard   // Hooks dashboard for event monitoring
	hooksAssistant           *HooksAssistant   // AI assistant for managing hooks
	agentsAssistant          *AgentsAssistant  // AI assistant for managing agents
	mcpAssistant             *MCPAssistant     // AI assistant for managing MCP servers
	hooksConfig              *HooksConfig      // Hooks configuration
	hooksRegistrationPending bool              // Defer hook registration until UI is running
	sdkInitError             string            // Captures SDK init failures for user-facing notifications
	notifications            []Notification

	// Compaction
	compactionService       *compaction.Service  // Context compaction for long conversations
	compactionMu            *sync.Mutex          // Shared pointer survives App value replacement without copying lock state
	isCompacting            bool                 // True when compaction is in progress
	preCompactIndicatorType LoadingIndicatorType // Saved loadingIndicator style to restore after compaction's progress bar finishes
	compactionBlocked       bool                 // Terminal compaction failure blocks provider calls until compaction succeeds
	compactionFailCount     int                  // Consecutive compaction failures (circuit breaker)
	// autoCompactThresholdTokens is the live auto-compaction threshold reported
	// by the SDK (tokenUpdateMsg.autoCompactThreshold). Used by the context
	// usage gauge in the chat footer.
	autoCompactThresholdTokens int
	lastCompactionAttempt   time.Time            // Time of last compaction attempt (cooldown)

	// Dream background memory consolidation
	dreamRunning bool // True when a Dream consolidation goroutine is active

	// AUTO mode permission management
	// When the user enters AUTO mode, we save their current permission level here
	// so we can restore it when they leave AUTO mode.
	preAutoPermissionLevel tools.PermissionLevel // permission level before AUTO mode was entered

	// Plan Mode (PLAN 	 ACT workflow)
	inPlanMode        bool           // True when the agent is in plan mode (exploring/designing)
	planBroker        *PlanBroker    // Bridges SDK plan tools ↔ TUI
	planContent       string         // Current plan submitted by the agent for approval
	planQuestionModal *QuestionModal // Approval/rejection bar shown after plan submitted
	planViewer        *PlanViewer    // Dedicated viewport for plan review (takes over chat area)

	// detailViewer is a generic takeover pane (Enter-to-view) for a background
	// bash command or sub-agent. When non-nil it replaces the chat area until
	// dismissed with Esc.
	detailViewer *DetailViewer

	// Background Process Management
	bgProcessManager       *bgprocess.BackgroundProcessManager // Manages background bash commands
	suppressNextUserBubble bool                                // When true, handleSendMessage omits the visible user bubble (used for system-triggered agent wakes)

	// Bash dock: a compact list of background bash commands docked under the
	// chatbox. Down at the bottom of the input focuses it; Up/Down scroll the
	// commands; Esc / Up-past-top returns focus to the input. Selection is
	// anchored by stable entry ID (not index) so it does not jump when the
	// list refreshes.
	bashDockFocused    bool
	bashDockSelectedID string

	// lastBashDockHeight / lastTaskPanelHeight cache the previous frame's
	// rendered height for these two bars so renderChatContent can trace
	// height-transition events (BASH_DOCK_HEIGHT_CHANGE / TASK_PANEL_HEIGHT_CHANGE)
	// only when the height actually changes, instead of every frame.
	lastBashDockHeight  int
	lastTaskPanelHeight int

	// Operating Mode (PLAN/ACT/AUTO)
	operatingMode     string // Current operating mode ID (default: "act")
	lastCommittedMode string // Mode of last sent message (for append-only mode tracking)

	// Animation
	bootFrame    int
	bootComplete bool
	menuFrame    int
	menuReady    bool
	// TTE Intro animation (SwarmWizard logo reveal)
	introComplete      bool
	introFrame         string          // latest rendered TTE frame
	introDoneHold      int             // hold-phase tick counter
	introEffectIdx     int             // which effect in the cycle we're currently running
	introAnim          *introAnimState // TTE effect + terminal (defined in app_intro.go)
	introGlowing       bool            // true after the reveal; shows slow breathing logo until first send
	introGlowPhase     float64         // 0..2π oscillation phase, incremented each glow tick
	introFromBootstrap bool            // current effect began in the lightweight startup shell

	// Home-screen title burn animation ("swarm / terminal intelligence")
	homeTitleAnim  *introAnimState // TTE effect + terminal for title burn
	homeTitleFrame string          // latest rendered frame from title burn
	homeTitleDone  bool            // true once the burn effect has finished
	homeBurnW      int             // canvas width the burn was built for
	homeBurnH      int             // canvas height the burn was built for

	// Workspace/Panes
	workspaceMode    bool // Toggle workspace tiling mode (default: false)
	workspaces       []*Workspace
	currentWorkspace int
	focusedPane      *Pane

	// Agent update queue for real-time UI updates during execution
	updateQueue           chan tea.Msg
	program               *tea.Program
	modelPromotedCallback func(*App)
	runtimeReadyCallback  func()
	quitting              bool // true when the app is shutting down, prevents wakeRuntimeAsync goroutine leaks
	pendingQuit           bool // set when user confirms quit via modal; checked after modal callback returns

	// wakeInProgress guards the single wake goroutine: only one runs at a time
	// (atomic int32: 0 = no goroutine, 1 = goroutine running). Combined with
	// wakePending it forms a lock-free coalescing signal — see wakeRuntimeAsync.
	wakeInProgress int32

	// wakePending is the re-arm flag for the wake signal (atomic int32). Every
	// wake request sets it to 1; the running wake goroutine consumes it and, if
	// it was set again after the last drainQueueMsg was sent, sends another.
	// This guarantees no lost wakeup: a message enqueued while a wake is
	// in-flight always causes at least one more drainQueueMsg after it, so it
	// can never be stranded until the next terminal event (a focus click).
	wakePending int32

	// wakeNotify, when non-nil, is invoked by wakeRuntimeAsync INSTEAD of nudging
	// the bubbletea Program via Send. It is a test-only seam: it lets tests observe
	// whether a runtime wake was requested without spinning up a real *tea.Program.
	// Production leaves this nil, so the real program.Send path is used.
	wakeNotify func()

	// Global sequence counter for agent updates (atomic, incremented when updates arrive)
	globalUpdateSequence int
	updateSequenceMu     sync.Mutex
	a2aInboundConvID     string

	// Background agent manager for concurrent conversations
	bgManager *BackgroundAgentManager

	// Debug mode - enables DebugLogs tool
	debugMode bool

	// Project Viewer
	viewerComponent *ViewerComponent

	// Pre-allocated styles for sub-agent rendering (avoids 90+ allocs/sec during streaming)
	subAgentStyles   *SubAgentRenderStyles
	subAgentRenderer *SubAgentRenderer    // Boxed sub-agent renderer
	subAgentTable    *SubAgentTable       // Compact table for multiple sub-agents
	toolRegistry     *toolrender.Registry // Unified tool render registry for all tool types

	// Branch-based conversation management
	gitHelper               *GitHelper     // Git operations helper
	newChatModal            *NewChatModal  // New chat input modal
	checkoutModal           *CheckoutModal // Branch checkout confirmation modal
	gitInitModal            *GitInitModal  // Git init prompt modal
	branchFilter            BranchFilter   // Current conversation filter mode
	compactConversationView bool           // Use compact 1-line view for conversations (default: false)
	currentBranchCtx        string         // Cached current branch for filtering
	pendingAutoSubmit       bool           // Auto-submit flag for new chat modal
	branchPromptAsked       bool           // True if we've already asked about branch creation in current conversation

	// Two-pane layout for conversations view
	// BASH side-panel interaction state
	bashPanelFocused    bool   // side panel BASH section has keyboard focus
	bashSelectedIdx     int    // selected absolute index in the filtered BASH rows
	bashSelectedID      string // stable process identity across filtering and reordering
	twoPaneMode         bool   // Enable two-pane layout (sidebar + messages)
	sidebarVisible      bool   // Toggle sidebar visibility
	sidebarWidth        int    // Width of sidebar (percentage or fixed)
	activePane          int    // 0 = sidebar, 1 = messages pane
	messageScrollOffset int    // Scroll offset for message view in right pane

	// Conversation loading state
	//
	// With the metadata-only storage path we no longer hold every raw
	// conversation in memory. Instead we paginate from the store directly:
	//   storageOffset:             next byte offset to read from the store
	//   conversationsHasMore:      true while more pages are available
	//   conversationsPageLoading:  guards against concurrent next-page fetches
	conversationsLoading     bool      // True while loading the initial page
	conversationsLoaded      bool      // True after first page returns
	conversationsPageLoading bool      // True while a background next-page fetch is in flight
	conversationsHasMore     bool      // True when the last fetch returned a full chunk
	storageOffset            int       // Offset into storage for the next page request
	messagesLoading          bool      // True while loading messages for selected conversation
	cachedPreviewID          string    // ID of conversation whose messages are cached
	cachedPreviewMsgs        []Message // Cached messages for preview pane
	historyPreviewLoading    bool      // True while selected History preview detail is loading
	historyPreview           historyPreviewDetail

	// Fork/compaction grouping in sidebar
	collapsedParents map[string]bool // Parent conv IDs whose forks are collapsed

	// Provider configuration cache (loaded from providers.json)
	providerConfigs []ProviderConfig // Cached provider configs for icons/colors

	// Voice input support
	voiceManager      *VoiceManager // Voice input manager (nil if not initialized)
	voiceEnabled      bool          // Whether voice input is available
	voiceRecording    bool          // True when recording
	voiceTranscribing bool          // True while waiting for final transcript
	voicePulseTick    bool          // Toggles for pulsing red dot animation
	voicePulseActive  bool          // Whether the pulse tick loop is running
	voiceAutoRecord   bool          // Auto-record mode: restart recording after completion
	voiceInterimText  string        // Interim transcript text (live preview)
	voiceFinalText    string        // Accumulated final transcript
	voiceEventCh      chan any      // Channel for voice events from goroutine
	lastVoiceError    string        // Last voice error notification text (dedupe spam)
	lastVoiceErrorAt  time.Time     // When last voice error notification was shown

	// Token count change profiling - tracks when/why tokenCount changes
	tokenChanges      []TokenChangeEntry // Recent token count changes (last 10)
	lastTokenSource   string             // Source of last token count update
	lastTokenChangeAt time.Time          // When last token change occurred
	// Auto-update support
	updater                *update.Updater        // Auto-update manager (nil if disabled)
	updateCheckPending     bool                   // True when a background update check is running
	updateAvailable        *update.CheckResult    // Non-nil when an update is available
	updateMandatoryBlocked bool                   // True when mandatory update is blocking UI
	updateDownloading      bool                   // True when downloading an update
	updateDownloadResult   *update.DownloadResult // Download result when ready to apply

	// Swarm terminal chat — peer-to-peer text chat via WorkspaceHub
	swarmChatPeers []string // Cached peer list (refreshed on join/leave)

	// Memory threshold watcher — non-blocking heap profiler
	memoryWatcher *MemoryWatcher
}

// TokenChangeEntry tracks a single token count change event
type TokenChangeEntry struct {
	Timestamp time.Time // When the change occurred
	PrevCount int       // Previous token count
	NewCount  int       // New token count
	Delta     int       // Change amount (positive = increase, negative = decrease)
	Source    string    // What caused the change (e.g., "api_response", "conversation_reload", "compaction")
	Context   string    // Additional context about the change
	IsBounce  bool      // True if this is an unexpected decrease after an increase
}

// SwarmChatEntry is a single message in the swarm terminal chat.
type SwarmChatEntry struct {
	From      string    // Handle of the sender
	Content   string    // Message text
	Timestamp time.Time // When it was received/sent
	IsOwn     bool      // True when sent by this terminal (optimistic append)
}

// modifiedFileEntry represents a single modified file for the sidebar display
type modifiedFileEntry struct {
	path   string
	status string // "M" modified, "A" added, "D" deleted, "?" untracked, "R" renamed
	staged bool
	ins    int // lines added
	del    int // lines deleted
}

// newSubAgentRenderStyles creates pre-allocated styles for a given theme.
// Call this once at init and when theme changes.
func newSubAgentRenderStyles(th Theme) *SubAgentRenderStyles {
	return &SubAgentRenderStyles{
		Agent:     lipgloss.NewStyle().Foreground(lipgloss.Color(th.Warning)).Bold(true),
		Meta:      lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)),
		Tool:      lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)),
		Content:   lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text)),
		Error:     lipgloss.NewStyle().Foreground(lipgloss.Color(th.Error)),
		Connector: lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)),
	}
}

// Notification represents a banner-style message to surface errors/warnings to the user.
type Notification struct {
	Kind      string // "error", "warning", "info", "success"
	Text      string
	CreatedAt time.Time
}

const notificationTTL = 3 * time.Second

// ensureMutex lazily initializes the preRenderMutex if needed.
func (m *Message) ensureMutex() {
	if m.preRenderMutex == nil {
		m.preRenderMutex = &sync.RWMutex{}
	}
}

// MarkDirty marks this message as needing re-rendering.
func (m *Message) MarkDirty() {
	m.ensureMutex()
	m.preRenderMutex.Lock()
	m.dirty = true
	// Content changed, so BOTH cached widths are stale. Dropping the alternate
	// slot here is what keeps the width swap safe: without this, toggling the
	// side panel could restore pre-edit content.
	m.altPreRenderWidth = 0
	m.altPreRenderLines = nil
	m.altPreRenderImages = nil
	m.preRenderMutex.Unlock()
}

// IsDirty returns whether this message needs re-rendering.
func (m *Message) IsDirty() bool {
	m.ensureMutex()
	m.preRenderMutex.RLock()
	defer m.preRenderMutex.RUnlock()
	return m.dirty
}

// ClearDirty clears the dirty flag after rendering.
func (m *Message) ClearDirty() {
	m.ensureMutex()
	m.preRenderMutex.Lock()
	m.dirty = false
	m.preRenderMutex.Unlock()
}

// SetPreRenderLines stores pre-rendered lines for fast consumption.
func (m *Message) SetPreRenderLines(lines []string) {
	m.SetPreRender(lines, nil)
}

// SetPreRender atomically stores rendered text and its native-image anchors.
func (m *Message) SetPreRender(lines []string, images []nativeImageAnchor) {
	m.ensureMutex()
	m.preRenderMutex.Lock()
	m.preRenderLines = lines
	m.preRenderImages = images
	// Width unknown: mark it as such and drop the alternate slot so a later
	// width swap cannot resurrect content that no longer matches.
	m.preRenderWidth = 0
	m.altPreRenderWidth = 0
	m.altPreRenderLines = nil
	m.altPreRenderImages = nil
	m.dirty = false // Clear dirty since we have fresh pre-rendered content
	m.preRenderMutex.Unlock()
}

// SetPreRenderAt stores rendered content and records the viewport width it was
// wrapped at, demoting the previously cached width into the alternate slot.
func (m *Message) SetPreRenderAt(width int, lines []string, images []nativeImageAnchor) {
	m.ensureMutex()
	m.preRenderMutex.Lock()
	// Only a wrap that was still VALID may be demoted into the alternate slot.
	// If the message was dirty, the outgoing primary was rendered from content
	// that has since changed: demoting it would let a later width swap serve
	// pre-mutation text (MarkDirty clears the alt slot, but it cannot clear a
	// primary that has not been superseded yet). Drop it instead.
	if m.dirty {
		m.altPreRenderWidth = 0
		m.altPreRenderLines = nil
		m.altPreRenderImages = nil
	} else if m.preRenderWidth != 0 && m.preRenderWidth != width && len(m.preRenderLines) > 0 {
		m.altPreRenderWidth = m.preRenderWidth
		m.altPreRenderLines = m.preRenderLines
		m.altPreRenderImages = m.preRenderImages
	}
	m.preRenderLines = lines
	m.preRenderImages = images
	m.preRenderWidth = width
	m.dirty = false
	m.preRenderMutex.Unlock()
}

// UsePreRenderForWidth makes cached content for width current, if it exists.
// Returns true when the message needs no re-render at that width.
func (m *Message) UsePreRenderForWidth(width int) bool {
	if width <= 0 {
		return false
	}
	m.ensureMutex()
	m.preRenderMutex.Lock()
	defer m.preRenderMutex.Unlock()
	if m.dirty {
		return false
	}
	if m.preRenderWidth == width && len(m.preRenderLines) > 0 {
		return true
	}
	if m.altPreRenderWidth == width && len(m.altPreRenderLines) > 0 {
		m.preRenderWidth, m.altPreRenderWidth = m.altPreRenderWidth, m.preRenderWidth
		m.preRenderLines, m.altPreRenderLines = m.altPreRenderLines, m.preRenderLines
		m.preRenderImages, m.altPreRenderImages = m.altPreRenderImages, m.preRenderImages
		return true
	}
	return false
}

// GetPreRenderLines retrieves pre-rendered lines (thread-safe).
func (m *Message) GetPreRenderLines() []string {
	m.ensureMutex()
	m.preRenderMutex.RLock()
	defer m.preRenderMutex.RUnlock()
	// Return a copy to avoid race conditions
	result := make([]string, len(m.preRenderLines))
	copy(result, m.preRenderLines)
	return result
}

// GetPreRenderImages retrieves a defensive copy of cached native-image anchors.
func (m *Message) GetPreRenderImages() []nativeImageAnchor {
	m.ensureMutex()
	m.preRenderMutex.RLock()
	defer m.preRenderMutex.RUnlock()
	result := make([]nativeImageAnchor, len(m.preRenderImages))
	copy(result, m.preRenderImages)
	return result
}

// HasPreRenderLines returns true if this message has pre-rendered content.
func (m *Message) HasPreRenderLines() bool {
	m.ensureMutex()
	m.preRenderMutex.RLock()
	defer m.preRenderMutex.RUnlock()
	return len(m.preRenderLines) > 0
}
