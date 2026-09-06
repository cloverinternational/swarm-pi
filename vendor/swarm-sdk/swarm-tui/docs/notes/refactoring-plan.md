
● 🏗️  COMPREHENSIVE REFACTORING PLAN & ARCHITECTURAL LAYOUT

  PART 1: TARGET ARCHITECTURE

  1.1 Proper Directory Structure

  SwarmCode/
  ├── agent-sdk/                          # Pure SDK - ZERO TUI dependencies
  │   ├── go.mod                          # Remove swarmos-tui dependency
  │   ├── provider/                       # LLM provider interfaces & implementations
  │   │   ├── provider.go                 # Core interfaces
  │   │   ├── anthropic/
  │   │   ├── openai/
  │   │   ├── gemini/
  │   │   └── codex/
  │   ├── conversation/                   # Conversation management
  │   │   ├── conversation.go             # Core types
  │   │   ├── manager/                    # Conversation manager
  │   │   └── storage/                    # Storage backends
  │   ├── tools/                          # Tool system
  │   │   ├── registry.go                 # Tool registry interface
  │   │   ├── builtin/                    # Built-in tools
  │   │   ├── mcp/                        # MCP tools
  │   │   └── permissions.go              # Permission system
  │   ├── agent/                          # Agent runtime (REFACTORED)
  │   │   ├── agent.go                    # Core Agent interface (~200 LOC)
  │   │   ├── executor.go                 # Execution engine (~300 LOC)
  │   │   ├── tool_execution.go           # Tool execution strategies (~400 LOC)
  │   │   ├── provider_bridge.go          # Provider interaction (~300 LOC)
  │   │   ├── state.go                    # State management (~200 LOC)
  │   │   └── factory.go                  # Agent factory (~150 LOC)
  │   ├── hooks/                          # Hook system
  │   ├── skills/                         # Skills system
  │   ├── plugins/                        # Plugin system
  │   ├── compaction/                     # Compaction (REFACTORED)
  │   │   ├── service.go                  # Main service (~150 LOC)
  │   │   ├── tokenizer.go                # Token estimation (~200 LOC)
  │   │   ├── summarizer.go               # Summarization (~250 LOC)
  │   │   └── recovery.go                 # File recovery (~200 LOC)
  │   ├── mode/                           # Operating modes
  │   └── observability/                  # Logging, tracing
  │
  ├── swarmos-core/                       # NEW: Platform-agnostic core (extracted from headless)
  │   ├── go.mod                          # Depends on agent-sdk ONLY
  │   ├── interfaces/                     # Core interfaces
  │   │   ├── sdk_bridge.go               # SDKBridge interface
  │   │   ├── conversation_manager.go     # ConversationManager interface
  │   │   ├── renderer.go                 # Renderer interface
  │   │   └── state_store.go              # StateStore interface
  │   ├── engine/                         # State machine engine
  │   │   ├── engine.go                   # Main engine (~400 LOC)
  │   │   ├── states.go                   # State definitions (~200 LOC)
  │   │   └── transitions.go              # State transitions (~200 LOC)
  │   ├── config/                         # Configuration management
  │   │   ├── manager.go                  # ConfigManager interface
  │   │   └── types.go                    # Config types
  │   └── bridge/                         # SDK bridge implementation
  │       ├── sdk_adapter.go              # Implements SDKBridge (~500 LOC)
  │       ├── provider_adapter.go         # Provider management (~300 LOC)
  │       ├── conversation_adapter.go     # Conversation management (~400 LOC)
  │       └── tool_adapter.go             # Tool management (~300 LOC)
  │
  ├── swarmos-tui/                        # TUI - Depends on swarmos-core + agent-sdk
  │   ├── go.mod                          # Depends on swarmos-core, agent-sdk
  │   ├── cmd/
  │   │   └── swarmos/                    # Main TUI executable
  │   ├── internal/
  │   │   └── ui/                         # REFACTORED UI layer
  │   │       ├── app/                    # Application shell
  │   │       │   ├── app.go              # Main app (~200 LOC, composing components)
  │   │       │   ├── state.go            # App state (~150 LOC)
  │   │       │   └── lifecycle.go        # Init/Update/View (~200 LOC)
  │   │       ├── components/             # Reusable UI components
  │   │       │   ├── chat/               # Chat view component
  │   │       │   │   ├── chat.go         # Chat component (~300 LOC)
  │   │       │   │   ├── state.go        # Chat state (~150 LOC)
  │   │       │   │   ├── input.go        # Input handling (~200 LOC)
  │   │       │   │   └── render.go       # Rendering (~250 LOC)
  │   │       │   ├── sidebar/            # Sidebar component
  │   │       │   │   ├── sidebar.go      # (~200 LOC)
  │   │       │   │   ├── state.go        # (~100 LOC)
  │   │       │   │   └── render.go       # (~150 LOC)
  │   │       │   ├── messagelist/        # Message list component
  │   │       │   │   ├── messagelist.go  # (~250 LOC)
  │   │       │   │   └── viewport.go     # (~150 LOC)
  │   │       │   ├── modals/             # Modal system
  │   │       │   │   ├── manager.go      # Modal manager (~200 LOC)
  │   │       │   │   ├── approval.go     # Approval modal
  │   │       │   │   ├── question.go     # Question modal
  │   │       │   │   └── newchat.go      # New chat modal
  │   │       │   └── statusbar/          # Status bar component
  │   │       ├── services/               # Business logic layer
  │   │       │   ├── conversation_service.go  # Conversation operations (~300 LOC)
  │   │       │   ├── message_service.go       # Message operations (~250 LOC)
  │   │       │   ├── provider_service.go      # Provider management (~200 LOC)
  │   │       │   ├── permission_service.go    # Permissions (~250 LOC)
  │   │       │   └── settings_service.go      # Settings management (~200 LOC)
  │   │       ├── adapters/               # Adapters to swarmos-core
  │   │       │   ├── core_adapter.go     # Main adapter (~300 LOC)
  │   │       │   └── event_mapper.go     # Event mapping (~200 LOC)
  │   │       └── settings/               # Settings UI (REFACTORED)
  │   │           ├── manager.go          # Settings manager (~200 LOC)
  │   │           ├── model/              # Model settings
  │   │           │   ├── picker.go       # (~400 LOC)
  │   │           │   ├── config.go       # (~300 LOC)
  │   │           │   └── ui.go           # (~400 LOC)
  │   │           ├── mcp/                # MCP settings
  │   │           │   ├── manager.go      # (~300 LOC)
  │   │           │   ├── config.go       # (~250 LOC)
  │   │           │   └── ui.go           # (~400 LOC)
  │   │           ├── hooks/              # Hooks settings
  │   │           ├── permissions/        # Permission settings
  │   │           └── profiles/           # Profile settings
  │   └── headless/                       # Headless servers (moved from root)
  │       ├── cmd/
  │       │   ├── ipc-server/             # IPC server executable
  │       │   └── ws-server/              # WebSocket server executable
  │       ├── ipc/                        # IPC protocol
  │       ├── transport/                  # Transport layer
  │       ├── approval/                   # Approval broker
  │       └── cloudsync/                  # Cloud sync
  │
  └── sdk-cli/                            # NEW: Separate CLI tool (not in SDK)
      ├── go.mod                          # Depends on agent-sdk, swarmos-core
      ├── cmd/
      │   └── headless/                   # Headless CLI
      │       ├── main.go                 # Main CLI (~200 LOC)
      │       ├── commands.go             # Command definitions (~300 LOC)
      │       └── config.go               # Config management (~200 LOC)
      └── internal/                       # CLI-specific logic

  ---
  PART 2: DEPENDENCY FLOW

  2.1 Clean Dependency Graph

  ┌─────────────────────────────────────────────────────────────┐
  │                         USER LAYER                          │
  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
  │  │ swarmos-tui  │  │   sdk-cli    │  │  IDE/Web UI  │      │
  │  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘      │
  │         │                 │                  │              │
  └─────────┼─────────────────┼──────────────────┼──────────────┘
            │                 │                  │
            └─────────────────┼──────────────────┘
                              ↓
  ┌─────────────────────────────────────────────────────────────┐
  │                    PLATFORM-AGNOSTIC CORE                   │
  │                    ┌──────────────────┐                     │
  │                    │  swarmos-core    │                     │
  │                    │  ┌────────────┐  │                     │
  │                    │  │ Interfaces │  │                     │
  │                    │  ├────────────┤  │                     │
  │                    │  │   Engine   │  │                     │
  │                    │  ├────────────┤  │                     │
  │                    │  │   Bridge   │  │                     │
  │                    │  └────────────┘  │                     │
  │                    └─────────┬────────┘                     │
  └──────────────────────────────┼──────────────────────────────┘
                                 ↓
  ┌─────────────────────────────────────────────────────────────┐
  │                        SDK LAYER                            │
  │                    ┌──────────────────┐                     │
  │                    │   agent-sdk      │                     │
  │                    │  ┌────────────┐  │                     │
  │                    │  │  Provider  │  │                     │
  │                    │  ├────────────┤  │                     │
  │                    │  │Conversation│  │                     │
  │                    │  ├────────────┤  │                     │
  │                    │  │   Tools    │  │                     │
  │                    │  ├────────────┤  │                     │
  │                    │  │   Agent    │  │                     │
  │                    │  └────────────┘  │                     │
  │                    └──────────────────┘                     │
  └─────────────────────────────────────────────────────────────┘

  KEY PRINCIPLES:
  ✅ All arrows point DOWN (dependencies flow downward)
  ✅ No upward dependencies
  ✅ SDK has ZERO knowledge of consumers
  ✅ swarmos-core provides platform-agnostic abstractions
  ✅ TUI/CLI/IDE all use swarmos-core, never SDK directly

  2.2 Module Dependencies (go.mod)

  agent-sdk/go.mod:
  module github.com/Swarm-Code/mono/swarm-sdk

  // NO replace directives
  // NO dependencies on swarmos-tui or swarmos-core
  // Pure library

  swarmos-core/go.mod:
  module github.com/Swarm-Code/mono/swarmos-core

  require (
      github.com/Swarm-Code/mono/swarm-sdk v0.6.2
      // Other pure dependencies
  )

  // NO dependencies on swarmos-tui

  swarmos-tui/go.mod:
  module github.com/Swarm-Code/mono/swarmos-tui

  require (
      github.com/Swarm-Code/mono/swarmos-core v0.1.0
      github.com/Swarm-Code/mono/swarm-sdk v0.6.2  // Direct for specific needs
      // UI dependencies
  )

  sdk-cli/go.mod:
  module github.com/Swarm-Code/mono/sdk-cli

  require (
      github.com/Swarm-Code/mono/swarmos-core v0.1.0
      github.com/Swarm-Code/mono/swarm-sdk v0.6.2
  )

  ---
  PART 3: INTERFACE SEGREGATION

  3.1 Core Interfaces (swarmos-core/interfaces/)

  sdk_bridge.go:
  package interfaces

  import (
      "context"
      "github.com/Swarm-Code/mono/swarm-sdk/conversation"
      "github.com/Swarm-Code/mono/swarm-sdk/tools"
  )

  // ProviderManager handles provider selection and configuration
  type ProviderManager interface {
      GetCurrentProvider() (name string, model string)
      SwitchProvider(ctx context.Context, name string, model string) error
      ListProviders() []ProviderInfo
      GetModelCapabilities(model string) ModelCapabilities
  }

  // ConversationManager handles conversation lifecycle
  type ConversationManager interface {
      Create(ctx context.Context, opts CreateOptions) (string, error)
      Load(ctx context.Context, id string) (*ConversationState, error)
      List(ctx context.Context, filters ListFilters) ([]ConversationSummary, error)
      Delete(ctx context.Context, id string) error
      Fork(ctx context.Context, id string, atMessage int) (string, error)
  }

  // MessageExecutor handles message execution
  type MessageExecutor interface {
      Execute(ctx context.Context, req ExecuteRequest) (<-chan StateUpdate, error)
      Cancel(ctx context.Context, conversationID string) error
      GetStatus(conversationID string) ExecutionStatus
  }

  // ToolManager handles tool registry and execution
  type ToolManager interface {
      ListTools() []ToolInfo
      GetTool(name string) (ToolInfo, error)
      ExecuteTool(ctx context.Context, name string, params map[string]any) (ToolResult, error)
      RegisterTool(tool tools.Tool) error
  }

  // PermissionManager handles permission requests
  type PermissionManager interface {
      RequestPermission(ctx context.Context, req PermissionRequest) (bool, error)
      GetPermissionPolicy(permission string) PermissionPolicy
      SetPermissionPolicy(permission string, policy PermissionPolicy) error
  }

  // CompactionManager handles conversation compaction
  type CompactionManager interface {
      ShouldCompact(ctx context.Context, convID string) (bool, CompactionReason)
      Compact(ctx context.Context, convID string, opts CompactionOptions) error
      GetCompactionHistory(convID string) []CompactionEvent
  }

  // SDKBridge is the main interface to SDK functionality
  // Aggregates all manager interfaces
  type SDKBridge interface {
      ProviderManager
      ConversationManager
      MessageExecutor
      ToolManager
      PermissionManager
      CompactionManager

      // Lifecycle
      Initialize(ctx context.Context, config Config) error
      Shutdown(ctx context.Context) error
  }

  Why this works:
  - Interface Segregation Principle: Clients depend only on methods they use
  - Single Responsibility: Each interface has one concern
  - Testability: Can mock individual interfaces
  - Composability: SDKBridge aggregates them all

  3.2 TUI Services Layer (swarmos-tui/internal/ui/services/)

  conversation_service.go:
  package services

  import (
      "context"
      "github.com/Swarm-Code/mono/swarmos-core/interfaces"
  )

  // ConversationService handles conversation business logic for TUI
  type ConversationService struct {
      core       interfaces.ConversationManager
      cache      *ConversationCache
      workspace  string
  }

  func NewConversationService(
      core interfaces.ConversationManager,
      workspace string,
  ) *ConversationService {
      return &ConversationService{
          core:      core,
          cache:     NewConversationCache(),
          workspace: workspace,
      }
  }

  func (s *ConversationService) CreateNew(ctx context.Context, prompt string) (string, error) {
      // TUI-specific logic (workspace detection, UI state, etc.)
      opts := interfaces.CreateOptions{
          Workspace: s.workspace,
          InitialMessage: prompt,
      }

      convID, err := s.core.Create(ctx, opts)
      if err != nil {
          return "", err
      }

      s.cache.Add(convID, &CachedConversation{
          ID: convID,
          Workspace: s.workspace,
      })

      return convID, nil
  }

  func (s *ConversationService) LoadConversation(ctx context.Context, id string) (*UIConversationState, error) {
      // Check cache
      if cached := s.cache.Get(id); cached != nil {
          return cached.ToUIState(), nil
      }

      // Load from core
      state, err := s.core.Load(ctx, id)
      if err != nil {
          return nil, err
      }

      // Transform to UI state
      uiState := s.transformToUIState(state)

      // Cache it
      s.cache.Add(id, uiState)

      return uiState, nil
  }

  func (s *ConversationService) transformToUIState(core *interfaces.ConversationState) *UIConversationState {
      // Transform core state to UI-specific state
      // This is where TUI-specific fields live
      return &UIConversationState{
          ID:       core.ID,
          Messages: s.transformMessages(core.Messages),
          // UI-specific fields:
          IsExpanded: true,
          ScrollPosition: 0,
          InputBuffer: "",
      }
  }

  Why this works:
  - Separation: Business logic separate from UI rendering
  - Caching: TUI-specific caching without polluting core
  - Transformation: Clean mapping between core and UI states
  - Testable: Can mock interfaces.ConversationManager

  ---
  PART 4: REFACTORING STRATEGY

  4.1 Phase 1: Extract swarmos-core (Week 1-2)

  Step 1: Create new module
  mkdir -p ../swarmos-core/{interfaces,engine,bridge,config}
  cd ../swarmos-core
  go mod init github.com/Swarm-Code/mono/swarmos-core

  Step 2: Move headless/core to swarmos-core
  # Copy files
  cp -r /home/swarm/SwarmCode/TUI/headless/core/* ./interfaces/

  # Update package names
  find ./interfaces -name "*.go" -exec sed -i 's/package core/package interfaces/g' {} \;

  # Update imports
  find ./interfaces -name "*.go" -exec sed -i 's|"github.com/Swarm-Code/mono/swarmos-tui/headless/core"|"scm.swar
  mcode.ai/SwarmCode/swarmos-core/interfaces"|g' {} \;

  Step 3: Implement bridge in swarmos-core
  # Move headless/sdk/bridge.go to swarmos-core/bridge/
  cp /home/swarm/SwarmCode/TUI/headless/sdk/bridge.go ./bridge/sdk_adapter.go

  Step 4: Update go.mod
  // swarmos-core/go.mod
  module github.com/Swarm-Code/mono/swarmos-core

  require (
      github.com/Swarm-Code/mono/swarm-sdk v0.6.2
  )

  // Use local path during development
  replace github.com/Swarm-Code/mono/swarm-sdk => ../TUI/sdk

  Step 5: Verify it builds
  cd swarmos-core
  go build ./...
  go test ./...

  4.2 Phase 2: Fix SDK Dependency (Week 2)

  Step 1: Remove TUI dependency from SDK
  cd /home/swarm/SwarmCode/TUI/sdk

  # Edit go.mod - REMOVE this line:
  # replace github.com/Swarm-Code/mono/swarmos-tui => ../

  # Verify SDK builds without TUI
  go build ./...

  Step 2: Move sdk/cmd/headless to new sdk-cli repo
  mkdir -p ../sdk-cli/{cmd/headless,internal}
  mv /home/swarm/SwarmCode/TUI/sdk/cmd/headless ../sdk-cli/cmd/

  cd ../sdk-cli
  go mod init github.com/Swarm-Code/mono/sdk-cli

  Step 3: Update sdk-cli to use swarmos-core
  // sdk-cli/go.mod
  module github.com/Swarm-Code/mono/sdk-cli

  require (
      github.com/Swarm-Code/mono/swarmos-core v0.1.0
      github.com/Swarm-Code/mono/swarm-sdk v0.6.2
  )

  replace github.com/Swarm-Code/mono/swarmos-core => ../swarmos-core
  replace github.com/Swarm-Code/mono/swarm-sdk => ../TUI/sdk

  Step 4: Update imports in sdk-cli
  cd sdk-cli/cmd/headless
  find . -name "*.go" -exec sed -i 's|"github.com/Swarm-Code/mono/swarmos-tui/headless/core"|"scm.swarmcode.ai/Sw
  armCode/swarmos-core/interfaces"|g' {} \;

  Step 5: Verify
  # SDK should build without TUI
  cd /home/swarm/SwarmCode/TUI/sdk
  go build ./...  # Should succeed with NO tui imports

  # CLI should build with swarmos-core
  cd ../sdk-cli
  go build ./cmd/headless  # Should succeed

  4.3 Phase 3: Break Up sdk_integration.go (Week 3-4)

  Current: internal/chat/sdk_integration.go (6,796 lines, 121 methods)

  Target Structure:
  internal/ui/
  ├── services/
  │   ├── provider_service.go          # Provider management (15 methods)
  │   ├── conversation_service.go      # Conversation operations (20 methods)
  │   ├── message_service.go           # Message execution (12 methods)
  │   ├── tool_service.go              # Tool management (18 methods)
  │   ├── permission_service.go        # Permissions (10 methods)
  │   ├── compaction_service.go        # Compaction (8 methods)
  │   ├── profile_service.go           # Profiles (12 methods)
  │   ├── cache_service.go             # Caching (10 methods)
  │   └── observability_service.go     # Debug/logging (8 methods)
  └── adapters/
      └── core_adapter.go               # Adapter to swarmos-core (~300 LOC)

  Step-by-Step Extraction:

  1. Create provider_service.go (extract provider methods):
  // internal/ui/services/provider_service.go
  package services

  import (
      "context"
      "github.com/Swarm-Code/mono/swarmos-core/interfaces"
  )

  type ProviderService struct {
      core interfaces.ProviderManager
      // UI-specific state
      currentModel string
      modelCache   map[string]ModelInfo
  }

  func NewProviderService(core interfaces.ProviderManager) *ProviderService {
      return &ProviderService{
          core:       core,
          modelCache: make(map[string]ModelInfo),
      }
  }

  // Extract these methods from SDKIntegration:
  // - GetProvider()
  // - GetProviderName()
  // - SwitchProvider()
  // - GetCurrentModel()
  // - SetCurrentModel()
  // - GetModelCapabilities()
  // - GetModelContextWindow()
  // - GetModelForRole()
  // - ListProviders()
  // - ReloadProvider()
  // - GetProviderConfig()
  // - SetProviderConfig()
  // - GetAvailableModels()
  // - IsOAuth()
  // - GetAuthToken()

  2. Create conversation_service.go (extract conversation methods):
  // internal/ui/services/conversation_service.go
  package services

  type ConversationService struct {
      core      interfaces.ConversationManager
      workspace string
      cache     *ConversationCache
  }

  // Extract these methods from SDKIntegration:
  // - CreateConversation()
  // - CreateConversationWithBranch()
  // - GetConversation()
  // - ListConversations()
  // - DeleteConversation()
  // - ForkConversation()
  // - CompleteConversation()
  // - GetMessages()
  // - AddMessage()
  // - UpdateMessage()
  // - GetConversationTokens()
  // - LoadConversations()
  // - SaveConversation()
  // - DetectWorkspace()
  // - GetWorkspaceRoot()
  // - SetWorkspaceRoot()

  3. Create message_service.go (extract execution methods):
  // internal/ui/services/message_service.go
  package services

  type MessageService struct {
      executor interfaces.MessageExecutor
      // Streaming state
      activeExecutions map[string]*ExecutionState
  }

  // Extract these methods:
  // - ExecuteMessage()
  // - ExecuteMessageWithDetails()
  // - ExecuteAgentsMessage()
  // - ExecuteHooksMessage()
  // - ExecuteWorkflowAssistantMessage()
  // - CancelExecution()
  // - GetExecutionStatus()
  // - StreamMessage()
  // - HandleStreamingUpdate()

  4. Repeat for remaining services

  5. Update App struct to use services:
  // internal/ui/app/app.go (BEFORE)
  type App struct {
      sdk *SDKIntegration  // 6,796 line god object
      // ... 267 other fields
  }

  // internal/ui/app/app.go (AFTER)
  type App struct {
      services *Services   // Aggregated services
      // ... other UI fields
  }

  type Services struct {
      Provider     *services.ProviderService
      Conversation *services.ConversationService
      Message      *services.MessageService
      Tool         *services.ToolService
      Permission   *services.PermissionService
      Compaction   *services.CompactionService
      Profile      *services.ProfileService
      Cache        *services.CacheService
      Observability *services.ObservabilityService
  }

  func NewServices(core interfaces.SDKBridge) *Services {
      return &Services{
          Provider:     services.NewProviderService(core),
          Conversation: services.NewConversationService(core),
          Message:      services.NewMessageService(core),
          // ... etc
      }
  }

  6. Update callers:
  // BEFORE
  a.sdk.ExecuteMessage(ctx, msg)

  // AFTER
  a.services.Message.Execute(ctx, msg)

  4.4 Phase 4: Break Up App Struct (Week 5-6)

  Current: 267 fields in one struct

  Target: Composition of focused structs

  app.go:
  package app

  type App struct {
      // Core services (from swarmos-core)
      services *Services

      // UI state (grouped by concern)
      chat      *ChatState
      sidebar   *SidebarState
      modals    *ModalState
      streaming *StreamingState
      workspace *WorkspaceState

      // UI components
      components *Components

      // App-level state
      theme       Theme
      screen      string
      initialized bool
  }

  type ChatState struct {
      messages           []Message
      focusedMessageIdx  int
      editMode           EditMode
      inputBuffer        string
      viewport           *MessageViewport
      scrollPosition     int
      userScrolledAway   bool
  }

  type SidebarState struct {
      conversations     []ConversationSummary
      selectedIdx       int
      filterMode        string
      cacheValid        bool
      lastUpdate        time.Time
  }

  type ModalState struct {
      stack    []Modal
      activeID string
  }

  type StreamingState struct {
      active          bool
      conversationID  string
      message         *StreamingMessage
      buffer          strings.Builder
      inputTokens     int
      outputChars     int
  }

  type WorkspaceState struct {
      root       string
      panes      []*WorkspacePane
      activeIdx  int
      layout     LayoutMode
  }

  type Components struct {
      messageList  *MessageList
      sidebar      *Sidebar
      input        *Input
      statusBar    *StatusBar
      modalManager *ModalManager
  }

  Why this works:
  - Grouped concerns: Related fields together
  - Clear boundaries: Each state struct has one responsibility
  - Testable: Can test ChatState independently
  - Composable: Easy to add new state groups

  4.5 Phase 5: Break Up Agent.go (Week 7-8)

  Current: sdk/agent/agent.go (2,627 lines)

  Target Structure:
  sdk/agent/
  ├── agent.go              # Core Agent interface + factory (~200 LOC)
  ├── executor.go           # Main execution engine (~350 LOC)
  ├── loop.go               # Execute loop (~300 LOC)
  ├── tool_execution.go     # Tool execution strategies (~450 LOC)
  ├── provider_bridge.go    # Provider interaction (~300 LOC)
  ├── state.go              # State management (~200 LOC)
  ├── token_accounting.go   # Token tracking (~200 LOC)
  ├── hooks.go              # Hook integration (~150 LOC)
  ├── memory.go             # Memory management (~200 LOC)
  └── background.go         # Background agent support (~200 LOC)

  Refactoring Steps:

  1. Create interfaces (agent.go):
  // sdk/agent/agent.go
  package agent

  // Agent is the core interface
  type Agent interface {
      // Configuration
      ID() string
      SetProvider(provider provider.Provider)
      SetToolRegistry(registry tools.Registry)

      // Execution
      Execute(ctx context.Context, req ExecuteRequest) (*ExecuteResponse, error)

      // Lifecycle
      Close() error
  }

  // AgentConfig holds agent configuration
  type AgentConfig struct {
      ID              string
      Provider        provider.Provider
      ToolRegistry    tools.Registry
      ConversationMgr conversation.Manager
      Logger          observability.Logger
      Tracer          observability.Tracer
      Callbacks       Callbacks
  }

  // Callbacks holds optional callback functions
  type Callbacks struct {
      OnMessage      MessageCallback
      OnIntermediate IntermediateCallback
      OnToolCall     ToolCallCallback
  }

  // Factory creates agents
  type Factory interface {
      CreateAgent(config AgentConfig) (Agent, error)
      CreateSubAgent(parent Agent, role string) (Agent, error)
  }

  2. Create executor (executor.go):
  // sdk/agent/executor.go
  package agent

  type executor struct {
      config AgentConfig
      state  *executionState
      loop   *executeLoop
      tools  *toolExecutor

      mu sync.RWMutex
  }

  func newExecutor(config AgentConfig) *executor {
      return &executor{
          config: config,
          state:  newExecutionState(),
          loop:   newExecuteLoop(config),
          tools:  newToolExecutor(config.ToolRegistry),
      }
  }

  func (e *executor) Execute(ctx context.Context, req ExecuteRequest) (*ExecuteResponse, error) {
      // Main execution logic (150 lines instead of 2627)
      e.mu.Lock()
      if err := e.state.startExecution(req); err != nil {
          e.mu.Unlock()
          return nil, err
      }
      e.mu.Unlock()

      // Delegate to loop
      return e.loop.run(ctx, req)
  }

  3. Create tool execution (tool_execution.go):
  // sdk/agent/tool_execution.go
  package agent

  type toolExecutor struct {
      registry     tools.Registry
      parallelism  int
      strategies   map[string]ExecutionStrategy
  }

  // ExecutionStrategy defines how tools are executed
  type ExecutionStrategy interface {
      Execute(ctx context.Context, calls []conversation.ToolCall) ([]*conversation.Message, error)
  }

  type sequentialStrategy struct {
      registry tools.Registry
  }

  type parallelStrategy struct {
      registry    tools.Registry
      parallelism int
  }

  type streamingStrategy struct {
      registry tools.Registry
  }

  // Extract all tool execution logic here
  func (te *toolExecutor) execute(ctx context.Context, calls []conversation.ToolCall) ([]*conversation.Message,
  error) {
      // Pick strategy
      strategy := te.selectStrategy(calls)
      return strategy.Execute(ctx, calls)
  }

  4. Create state management (state.go):
  // sdk/agent/state.go
  package agent

  type executionState struct {
      status         Status
      conversationID string
      turnCount      int
      requestContext map[string]interface{}
      mu             sync.RWMutex
  }

  func (s *executionState) startExecution(req ExecuteRequest) error {
      s.mu.Lock()
      defer s.mu.Unlock()

      if s.status != StatusIdle {
          return fmt.Errorf("agent busy")
      }

      s.status = StatusExecuting
      s.conversationID = req.ConversationID
      s.turnCount = 0
      s.requestContext = req.Context

      return nil
  }

  func (s *executionState) incrementTurn() {
      s.mu.Lock()
      defer s.mu.Unlock()
      s.turnCount++
  }

  func (s *executionState) getStatus() Status {
      s.mu.RLock()
      defer s.mu.RUnlock()
      return s.status
  }

  5. Create provider bridge (provider_bridge.go):
  // sdk/agent/provider_bridge.go
  package agent

  type providerBridge struct {
      provider provider.Provider
      logger   observability.Logger
  }

  func (pb *providerBridge) chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
      // Handle provider interaction
      // Token accounting
      // Error handling
      // Logging/tracing
  }

  func (pb *providerBridge) chatStream(ctx context.Context, req provider.ChatRequest) (<-chan
  provider.StreamEvent, error) {
      // Handle streaming
  }

  func (pb *providerBridge) buildRequest(messages []*conversation.Message, tools []tools.Tool)
  provider.ChatRequest {
      // Build provider request
  }

  ---
  PART 5: MIGRATION TIMELINE

  Week-by-Week Breakdown
  ┌──────┬────────────────────┬────────────────────────────────────────────────┬────────────────────────────┐
  │ Week │       Phase        │                     Tasks                      │        Deliverable         │
  ├──────┼────────────────────┼────────────────────────────────────────────────┼────────────────────────────┤
  │ 1    │ Setup              │ Create swarmos-core module, move headless/core │ swarmos-core compiles      │
  ├──────┼────────────────────┼────────────────────────────────────────────────┼────────────────────────────┤
  │ 2    │ SDK Fix            │ Remove TUI dep from SDK, create sdk-cli        │ SDK independent            │
  ├──────┼────────────────────┼────────────────────────────────────────────────┼────────────────────────────┤
  │ 3    │ Extract Services 1 │ Create provider_service, conversation_service  │ 40 methods extracted       │
  ├──────┼────────────────────┼────────────────────────────────────────────────┼────────────────────────────┤
  │ 4    │ Extract Services 2 │ Create remaining services, update callers      │ sdk_integration.go deleted │
  ├──────┼────────────────────┼────────────────────────────────────────────────┼────────────────────────────┤
  │ 5    │ App Refactor 1     │ Create ChatState, SidebarState, ModalState     │ App struct reduced         │
  ├──────┼────────────────────┼────────────────────────────────────────────────┼────────────────────────────┤
  │ 6    │ App Refactor 2     │ Create Components, wire everything             │ App has <30 fields         │
  ├──────┼────────────────────┼────────────────────────────────────────────────┼────────────────────────────┤
  │ 7    │ Agent Refactor 1   │ Extract executor, loop, state                  │ agent.go <500 LOC          │
  ├──────┼────────────────────┼────────────────────────────────────────────────┼────────────────────────────┤
  │ 8    │ Agent Refactor 2   │ Extract tool execution, provider bridge        │ agent.go deleted           │
  ├──────┼────────────────────┼────────────────────────────────────────────────┼────────────────────────────┤
  │ 9    │ Testing            │ Write tests for all new services/components    │ 70% coverage               │
  ├──────┼────────────────────┼────────────────────────────────────────────────┼────────────────────────────┤
  │ 10   │ Polish             │ Documentation, examples, migration guide       │ v1.0 release               │
  └──────┴────────────────────┴────────────────────────────────────────────────┴────────────────────────────┘
  ---
  PART 6: RISK MITIGATION

  6.1 How to Refactor Without Breaking Everything

  Strategy: Parallel Implementation

  1. Keep old code running while building new
  2. Feature flag to switch between old/new
  3. Gradual migration one service at a time
  4. Comprehensive testing before deleting old code

  Example:
  // app.go
  type App struct {
      // Old (to be deleted)
      sdk *SDKIntegration  // Feature flagged

      // New
      services *Services    // Feature flagged

      // Feature flag
      useNewServices bool
  }

  func (a *App) ExecuteMessage(ctx context.Context, msg string) error {
      if a.useNewServices {
          // New implementation
          return a.services.Message.Execute(ctx, msg)
      } else {
          // Old implementation (keep until tested)
          return a.sdk.ExecuteMessage(ctx, msg)
      }
  }

  Once tested:
  1. Set useNewServices = true as default
  2. Run for 1 week in production
  3. Delete old code if no issues

  6.2 Testing Strategy

  For each extracted service:

  // services/provider_service_test.go
  func TestProviderService_SwitchProvider(t *testing.T) {
      // Mock the core interface
      mockCore := &mockProviderManager{
          currentProvider: "anthropic",
          currentModel:    "claude-sonnet-4.5",
      }

      svc := NewProviderService(mockCore)

      err := svc.SwitchProvider(context.Background(), "openai", "gpt-4")
      require.NoError(t, err)

      assert.Equal(t, "openai", mockCore.currentProvider)
      assert.Equal(t, "gpt-4", mockCore.currentModel)
  }

  type mockProviderManager struct {
      currentProvider string
      currentModel    string
  }

  func (m *mockProviderManager) SwitchProvider(ctx context.Context, name, model string) error {
      m.currentProvider = name
      m.currentModel = model
      return nil
  }

  Integration tests:
  // integration_test.go
  func TestFullMessageExecution(t *testing.T) {
      // Use real swarmos-core with test provider
      core := setupTestCore(t)
      services := NewServices(core)

      convID, err := services.Conversation.CreateNew(context.Background(), "test")
      require.NoError(t, err)

      resp, err := services.Message.Execute(context.Background(), ExecuteRequest{
          ConversationID: convID,
          Message:        "Hello",
      })
      require.NoError(t, err)
      assert.NotEmpty(t, resp.Content)
  }

  ---
  PART 7: SUCCESS METRICS

  Before Refactoring

  ✗ SDK depends on TUI (8 files)
  ✗ sdk_integration.go: 6,796 lines, 121 methods
  ✗ agent.go: 2,627 lines
  ✗ App struct: 267 fields
  ✗ interface{} usage: 1,160 times
  ✗ Test coverage: ~5%
  ✗ God objects: 3
  ✗ Monolithic files: 8+ over 700 LOC

  After Refactoring

  ✓ SDK is independent (0 TUI imports)
  ✓ Largest service file: <500 LOC
  ✓ Largest agent file: <450 LOC
  ✓ App struct: <30 fields
  ✓ interface{} usage: <50 (all with contracts)
  ✓ Test coverage: >70%
  ✓ God objects: 0
  ✓ Monolithic files: 0 over 500 LOC

  ---
  PART 8: QUICK START GUIDE

  For Developers Starting Refactoring

  Day 1: Setup
  # Clone repo
  git clone <repo>
  cd SwarmCode

  # Create feature branch
  git checkout -b refactor/extract-swarmos-core

  # Create new module
  mkdir ../swarmos-core
  cd ../swarmos-core
  go mod init github.com/Swarm-Code/mono/swarmos-core

  Day 2-3: Extract interfaces
  # Copy headless/core to swarmos-core/interfaces
  cp -r ../SwarmCode/TUI/headless/core/* ./interfaces/

  # Update package names
  find ./interfaces -name "*.go" -exec sed -i 's/package core/package interfaces/g' {} \;

  # Build
  go build ./...

  Day 4-5: Move bridge implementation
  # Copy bridge
  mkdir bridge
  cp ../SwarmCode/TUI/headless/sdk/bridge.go ./bridge/sdk_adapter.go

  # Update imports
  sed -i 's|headless/core|swarmos-core/interfaces|g' bridge/sdk_adapter.go

  # Build
  go build ./...
  go test ./...

  Week 2: Extract first service
  cd ../SwarmCode/TUI

  # Create service structure
  mkdir -p internal/ui/services

  # Extract provider service (start small)
  # Copy relevant methods from sdk_integration.go to provider_service.go
  # Update imports
  # Test
  go test ./internal/ui/services/...

  ---
  Would you like me to:
  1. Generate the actual code for any of these refactored files?
  2. Create detailed migration scripts for automated refactoring?
  3. Design the CI/CD pipeline to support gradual migration?
  4. Write comprehensive tests for the new architecture?

✻ Cooked for 2m 42s
