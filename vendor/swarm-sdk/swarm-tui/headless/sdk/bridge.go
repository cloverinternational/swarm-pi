// Package sdk provides the bridge between the headless core and the SwarmOS SDK.
// This bridge converts SDK types and events to headless core types.
package sdk

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mode"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vision"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/headless/profile"
)

// Bridge implements core.SDKBridge using the SwarmOS SDK.
type Bridge struct {
	mu sync.RWMutex

	// SDK components
	agentFactory          func(ctx context.Context, model string) (*agent.Agent, error)
	agentFactoryWithTools func(ctx context.Context, model string, toolRegistry tools.Registry) (*agent.Agent, error)
	convManager           *manager.Manager
	convStorage           storage.Storage
	toolRegistry          tools.Registry
	providers             map[string]provider.Provider
	profileStore          *profile.ProfileStore

	// Micro-compaction
	microCompactor *compaction.MicroCompactor
	config         core.ConfigManager

	// Current state
	currentProvider string
	currentModel    string
	currentAgent    *agent.Agent

	// Cancellation for ongoing execution
	cancelFn context.CancelFunc
}

// Option is a function that configures a Bridge.
// This functional options pattern allows for flexible, extensible Bridge creation.
type Option func(*Bridge) error

// BridgeConfig holds configuration for creating a Bridge.
// Deprecated: Use functional options (NewBridge with Option funcs) instead.
type BridgeConfig struct {
	// AgentFactory creates agents with the specified model.
	AgentFactory func(ctx context.Context, model string) (*agent.Agent, error)

	// AgentFactoryWithTools creates agents with access to the shared tool registry.
	AgentFactoryWithTools func(ctx context.Context, model string, toolRegistry tools.Registry) (*agent.Agent, error)

	// ConversationManager manages conversation persistence.
	ConversationManager *manager.Manager

	// ConversationStorage provides direct access to conversation storage for listing.
	ConversationStorage storage.Storage

	// ToolRegistry provides access to available tools.
	ToolRegistry tools.Registry

	// Providers maps provider names to provider instances.
	Providers map[string]provider.Provider

	// ProfileStore provides access to agent profiles.
	ProfileStore *profile.ProfileStore

	// ConfigProvider provides access to configuration.
	ConfigProvider core.ConfigManager

	// DefaultProvider is the initial provider to use.
	DefaultProvider string

	// DefaultModel is the initial model to use.
	DefaultModel string
}

// NewBridge creates a new SDK bridge with functional options.
// Example:
//
//	bridge, err := sdk.NewBridge(
//		sdk.WithAgentFactory(agentFactory),
//		sdk.WithConversationManager(manager),
//		sdk.WithDefaultModel("gpt-4"),
//	)
func NewBridge(opts ...Option) (*Bridge, error) {
	b := &Bridge{
		providers:      make(map[string]provider.Provider),
		microCompactor: compaction.NewMicroCompactor(),
	}

	// Apply all options
	for _, opt := range opts {
		if err := opt(b); err != nil {
			return nil, err
		}
	}

	return b, nil
}

// NewBridgeFromConfig creates a Bridge from a BridgeConfig (for backward compatibility).
func NewBridgeFromConfig(config BridgeConfig) (*Bridge, error) {
	var opts []Option
	if config.AgentFactory != nil {
		opts = append(opts, WithAgentFactory(config.AgentFactory))
	}
	if config.AgentFactoryWithTools != nil {
		opts = append(opts, WithAgentFactoryWithTools(config.AgentFactoryWithTools))
	}
	if config.ConversationManager != nil {
		opts = append(opts, WithConversationManager(config.ConversationManager))
	}
	if config.ConversationStorage != nil {
		opts = append(opts, WithConversationStorage(config.ConversationStorage))
	}
	if config.ToolRegistry != nil {
		opts = append(opts, WithToolRegistry(config.ToolRegistry))
	}
	if config.Providers != nil {
		opts = append(opts, WithProviders(config.Providers))
	}
	if config.ProfileStore != nil {
		opts = append(opts, WithProfileStore(config.ProfileStore))
	}
	if config.ConfigProvider != nil {
		opts = append(opts, WithConfigManager(config.ConfigProvider))
	}
	if config.DefaultProvider != "" {
		opts = append(opts, WithDefaultProvider(config.DefaultProvider))
	}
	if config.DefaultModel != "" {
		opts = append(opts, WithDefaultModel(config.DefaultModel))
	}
	return NewBridge(opts...)
}

// ─── Functional Options ────────────────────────────────────────────────────────

// WithAgentFactory sets the agent factory function (basic version).
func WithAgentFactory(fn func(ctx context.Context, model string) (*agent.Agent, error)) Option {
	return func(b *Bridge) error {
		if fn == nil {
			return errors.New("agent factory cannot be nil")
		}
		b.agentFactory = fn
		return nil
	}
}

// WithAgentFactoryWithTools sets the agent factory function with tool registry access.
func WithAgentFactoryWithTools(fn func(ctx context.Context, model string, toolRegistry tools.Registry) (*agent.Agent, error)) Option {
	return func(b *Bridge) error {
		if fn == nil {
			return errors.New("agent factory with tools cannot be nil")
		}
		b.agentFactoryWithTools = fn
		return nil
	}
}

// WithConversationManager sets the conversation manager.
func WithConversationManager(m *manager.Manager) Option {
	return func(b *Bridge) error {
		if m == nil {
			return errors.New("conversation manager cannot be nil")
		}
		b.convManager = m
		return nil
	}
}

// WithConversationStorage sets the conversation storage backend.
func WithConversationStorage(s storage.Storage) Option {
	return func(b *Bridge) error {
		if s == nil {
			return errors.New("conversation storage cannot be nil")
		}
		b.convStorage = s
		return nil
	}
}

// WithToolRegistry sets the tool registry.
func WithToolRegistry(r tools.Registry) Option {
	return func(b *Bridge) error {
		if r == nil {
			return errors.New("tool registry cannot be nil")
		}
		b.toolRegistry = r
		return nil
	}
}

// WithProviders sets the provider map.
func WithProviders(p map[string]provider.Provider) Option {
	return func(b *Bridge) error {
		if p == nil {
			return errors.New("providers map cannot be nil")
		}
		b.providers = p
		return nil
	}
}

// WithProfileStore sets the profile store.
func WithProfileStore(p *profile.ProfileStore) Option {
	return func(b *Bridge) error {
		if p == nil {
			return errors.New("profile store cannot be nil")
		}
		b.profileStore = p
		return nil
	}
}

// WithConfigManager sets the configuration manager.
func WithConfigManager(c core.ConfigManager) Option {
	return func(b *Bridge) error {
		if c == nil {
			return errors.New("config manager cannot be nil")
		}
		b.config = c
		return nil
	}
}

// WithDefaultProvider sets the default provider name.
func WithDefaultProvider(p string) Option {
	return func(b *Bridge) error {
		b.currentProvider = strings.TrimSpace(p)
		return nil
	}
}

// WithDefaultModel sets the default model.
func WithDefaultModel(m string) Option {
	return func(b *Bridge) error {
		b.currentModel = strings.TrimSpace(m)
		return nil
	}
}

// WithMicroCompactor sets a custom micro-compactor (advanced option).
func WithMicroCompactor(c *compaction.MicroCompactor) Option {
	return func(b *Bridge) error {
		if c == nil {
			return errors.New("micro compactor cannot be nil")
		}
		b.microCompactor = c
		return nil
	}
}

// ExecuteMessage implements core.SDKBridge.ExecuteMessage.
// It sends a message to the agent and returns a channel of state updates.
func (b *Bridge) ExecuteMessage(ctx context.Context, convID, message, model string) (<-chan core.StateUpdate, error) {
	return b.executeMessage(ctx, convID, message, model, "", nil)
}

// ExecuteMessageWithMode sends a message with operating mode context.
func (b *Bridge) ExecuteMessageWithMode(ctx context.Context, convID, message, model, modeID string) (<-chan core.StateUpdate, error) {
	return b.executeMessage(ctx, convID, message, model, modeID, nil)
}

// ExecuteMessageWithModeAndMetadata sends a message with both mode and metadata
func (b *Bridge) ExecuteMessageWithModeAndMetadata(ctx context.Context, convID, message, model, modeID string, metadata map[string]any) (<-chan core.StateUpdate, error) {
	return b.executeMessage(ctx, convID, message, model, modeID, metadata)
}

func (b *Bridge) executeMessage(ctx context.Context, convID, message, model, modeID string, metadata map[string]any) (<-chan core.StateUpdate, error) {
	fmt.Fprintf(os.Stderr, "[SDK-BRIDGE-EXECUTE] Message received: len=%d convID=%q model=%q\n", len(message), convID, model)
	b.mu.Lock()

	// Create cancellable context
	execCtx, cancel := context.WithCancel(ctx)
	b.cancelFn = cancel

	// Update model if specified
	if model != "" && model != b.currentModel {
		b.currentModel = model
	}

	b.mu.Unlock()

	// Create update channel
	updateChan := make(chan core.StateUpdate, 100)

	go func() {
		defer close(updateChan)
		defer func() {
			b.mu.Lock()
			b.cancelFn = nil
			b.mu.Unlock()
		}()

		// Create agent for this execution
		var ag *agent.Agent
		var err error
		if b.agentFactoryWithTools != nil {
			ag, err = b.agentFactoryWithTools(execCtx, b.currentModel, b.toolRegistry)
		} else if b.agentFactory != nil {
			ag, err = b.agentFactory(execCtx, b.currentModel)
		} else {
			updateChan <- core.NewStateUpdate(core.UpdateError, core.ErrorPayload{
				Message: "agent factory not configured",
				Code:    "agent_factory_not_configured",
			})
			return
		}
		if err != nil {
			updateChan <- core.NewStateUpdate(core.UpdateError, core.ErrorPayload{
				Error:   err,
				Message: err.Error(),
				Code:    "agent_creation_failed",
			})
			return
		}

		b.mu.Lock()
		b.currentAgent = ag
		b.mu.Unlock()

		// Get conversation history
		var history []*conversation.Message
		if b.convManager != nil && convID != "" {
			conv, err := b.convManager.Resume(execCtx, convID)
			if err == nil {
				history = conv.Messages
			}
		}

		// Apply tool filtering for ALL conversations (new and resumed) as a session-wide safety constraint
		// This ensures that if the user switches environments, tools are properly restricted immediately
		if b.config != nil {
			envCfg := b.config.GetEnvironments()
			if envCfg != nil {
				activeEnv := envCfg.GetForEnv(envCfg.Active)
				if len(activeEnv.DisallowedTools) > 0 {
					ag.SetToolFilter(func(allTools []provider.Tool) []provider.Tool {
						filtered := make([]provider.Tool, 0, len(allTools))
						for _, tool := range allTools {
							allowed := !slices.Contains(activeEnv.DisallowedTools, tool.Name)
							if allowed {
								filtered = append(filtered, tool)
							}
						}
						return filtered
					})
				}

				// Apply environment system-prompt suffix.
				// Get the base prompt already set on the agent (from config / profile),
				// then append the per-environment suffix so behaviour changes when the
				// user switches between Chat / Work / Code tabs.
				if activeEnv.SystemPromptSuffix != "" {
					base := ag.SystemPrompt()
					combined := strings.TrimRight(base, "\n")
					if combined != "" {
						combined += "\n\n" + activeEnv.SystemPromptSuffix
					} else {
						combined = activeEnv.SystemPromptSuffix
					}
					ag.SetSystemPrompt(combined)
					fmt.Fprintf(os.Stderr, "[SDK-BRIDGE] Applied systemPromptSuffix for env=%q (suffix len=%d)\n", envCfg.Active, len(activeEnv.SystemPromptSuffix))
				}
			}
		}

		// Apply micro-compaction if enabled
		var tokensSaved int
		if b.config != nil && b.microCompactor != nil {
			cfg := b.config.GetConfig()
			if cfg != nil && cfg.EnableMicroCompaction {
				// Configure retention count from config
				retentionCount := cfg.MicroRetentionCount
				if retentionCount <= 0 {
					retentionCount = 3 // Default from compaction package
				}
				b.microCompactor.SetRetentionCount(retentionCount)

				// Process messages
				history, tokensSaved = b.microCompactor.Process(history)

				// Micro-compaction stats available via b.microCompactor.GetStats() if needed
				_ = tokensSaved // Suppress unused warning for now
			}
		}

		// Set up intermediate callback to stream updates
		intermediateCallback := func(ctx context.Context, update agent.IntermediateUpdate) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			stateUpdate := convertIntermediateUpdate(update)
			if stateUpdate != nil {
				select {
				case updateChan <- *stateUpdate:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}

		// Set up message callback to save messages
		messageCallback := func(ctx context.Context, msg *conversation.Message) error {
			if b.convManager != nil && convID != "" {
				return b.convManager.AddMessage(ctx, convID, msg)
			}
			return nil
		}

		// Send stream start
		updateChan <- core.NewStateUpdate(core.UpdateStreamStart, nil)

		// Set the intermediate callback on the agent
		ag.SetIntermediateCallback(intermediateCallback)

		// Set the message callback if the agent supports it
		if msgCallbackSetter, ok := any(ag).(interface {
			SetMessageCallback(func(context.Context, *conversation.Message) error)
		}); ok {
			msgCallbackSetter.SetMessageCallback(messageCallback)
		}
		_ = messageCallback // Use if agent doesn't support SetMessageCallback directly

		// REMOVED: Initial token estimate
		// Reason: Estimates can be wildly inaccurate (200x+ inflation) when tool results
		// contain large files or images. All providers (Anthropic, OpenAI, Gemini) return
		// accurate token counts in their API responses within milliseconds.
		// We wait for real counts instead of showing misleading estimates.
		// See: TOKEN_COUNT_FIX_PLAN.md for details.

		// Send stream start
		updateChan <- core.NewStateUpdate(core.UpdateStreamStart, nil)

		// Record start time for duration calculation.
		startTime := time.Now()

		// Execute the agent
		// Extract and attach image metadata if present
		if metadata != nil && len(metadata) > 0 {
			fmt.Fprintf(os.Stderr, "[SDK-BRIDGE] executeMessage with metadata keys: %v\n", func() []string {
				keys := make([]string, 0)
				for k := range metadata {
					keys = append(keys, k)
				}
				return keys
			}())
		}

		req := agent.ExecuteRequest{
			Message:             message,
			ConversationID:      convID,
			ConversationHistory: history,
		}

		// Attach metadata to context for SDK to access
		if metadata != nil {
			if req.Context == nil {
				req.Context = make(map[string]any)
			}
			req.Context["metadata"] = metadata
			if images, ok := metadata["images"]; ok {
				fmt.Fprintf(os.Stderr, "[SDK-BRIDGE] Passing %v images in context\n", len(images.([]map[string]string)))
			}
		}

		if modeFilter := buildModeFilterContext(modeID); modeFilter != nil {
			req.Context = map[string]any{
				"mode_filter": modeFilter,
			}
		}

		resp, err := ag.Execute(execCtx, req)

		// Send completion or error
		if err != nil {
			if execCtx.Err() != nil {
				// Cancelled
				updateChan <- core.NewStateUpdate(core.UpdateStreamEnd, core.StreamEndPayload{
					Subtype:           "error_during_execution",
					IsError:           true,
					DurationMs:        time.Since(startTime).Milliseconds(),
					DurationApiMs:     time.Since(startTime).Milliseconds(),
					Result:            "",
					PermissionDenials: []any{},
					FastModeState:     "off",
					ModelUsage:        map[string]core.StreamEndModelUsage{},
					Usage: core.StreamEndUsage{
						ServerToolUse: core.StreamEndServerToolUse{},
						Iterations:    []any{},
						Speed:         "standard",
					},
				})
				return
			}
			updateChan <- core.NewStateUpdate(core.UpdateError, core.ErrorPayload{
				Error:   err,
				Message: err.Error(),
				Code:    "execution_failed",
			})
			updateChan <- core.NewStateUpdate(core.UpdateStreamEnd, core.StreamEndPayload{
				Subtype:           "error_during_execution",
				IsError:           true,
				DurationMs:        time.Since(startTime).Milliseconds(),
				DurationApiMs:     time.Since(startTime).Milliseconds(),
				Result:            "",
				PermissionDenials: []any{},
				FastModeState:     "off",
				ModelUsage:        map[string]core.StreamEndModelUsage{},
				Usage: core.StreamEndUsage{
					ServerToolUse: core.StreamEndServerToolUse{},
					Iterations:    []any{},
					Speed:         "standard",
				},
			})
			return
		}

		// Extract cache metrics from response metadata.
		cacheCreationTokens := extractMetaInt(resp.Metadata, "cache_metrics", "cache_creation_tokens")
		cacheReadTokens := extractMetaInt(resp.Metadata, "cache_metrics", "cache_read_tokens")
		cache1hTokens := extractMetaInt(resp.Metadata, "cache_metrics", "cache_creation_1h_tokens")
		cache5mTokens := extractMetaInt(resp.Metadata, "cache_metrics", "cache_creation_5m_tokens")

		// Extract provider message ID (e.g. "msg_01XYZ…") threaded via stream chunk metadata.
		providerMsgID := ""
		if resp.Metadata != nil {
			if v, ok := resp.Metadata["message_id"].(string); ok {
				providerMsgID = v
			}
		}

		// Send enriched token count (keeps the RPC path working as before).
		updateChan <- core.NewStateUpdate(core.UpdateTokenCount, core.TokenCountPayload{
			InputTokens:              resp.InputTokens,
			OutputTokens:             resp.OutputTokens,
			TotalTokens:              resp.TokensUsed,
			CacheRead:                cacheReadTokens,
			CacheCreate:              cacheCreationTokens,
			CacheCreationInputTokens: cacheCreationTokens,
			CacheReadInputTokens:     cacheReadTokens,
			Ephemeral1hInputTokens:   cache1hTokens,
			Ephemeral5mInputTokens:   cache5mTokens,
			Model:                    b.currentModel,
			MessageID:                providerMsgID,
		})

		// Send message complete
		updateChan <- core.NewStateUpdate(core.UpdateMessageComplete, nil)

		// Build per-model usage for the result event.
		modelUsage := map[string]core.StreamEndModelUsage{}
		if b.currentModel != "" {
			modelUsage[b.currentModel] = core.StreamEndModelUsage{
				InputTokens:              resp.InputTokens,
				OutputTokens:             resp.OutputTokens,
				CacheReadInputTokens:     cacheReadTokens,
				CacheCreationInputTokens: cacheCreationTokens,
				CostUSD:                  resp.CostUSD,
			}
		}

		// Send stream end with full result payload.
		updateChan <- core.NewStateUpdate(core.UpdateStreamEnd, core.StreamEndPayload{
			Subtype:       "success",
			IsError:       false,
			DurationMs:    resp.Duration.Milliseconds(),
			DurationApiMs: resp.Duration.Milliseconds(),
			NumTurns:      resp.TurnCount,
			Result:        resp.Message,
			StopReason:    finishReasonToStopReason(resp.FinishReason),
			TotalCostUsd:  resp.CostUSD,
			Usage: core.StreamEndUsage{
				InputTokens:              resp.InputTokens,
				CacheCreationInputTokens: cacheCreationTokens,
				CacheReadInputTokens:     cacheReadTokens,
				OutputTokens:             resp.OutputTokens,
				ServerToolUse:            core.StreamEndServerToolUse{},
				ServiceTier:              "standard",
				CacheCreation: core.StreamEndCacheCreation{
					Ephemeral1hInputTokens: cache1hTokens,
					Ephemeral5mInputTokens: cache5mTokens,
				},
				InferenceGeo: "",
				Iterations:   []any{},
				Speed:        "standard",
			},
			ModelUsage:        modelUsage,
			PermissionDenials: []any{},
			FastModeState:     "off",
		})

		b.mu.Lock()
		b.currentAgent = nil
		b.mu.Unlock()
	}()

	return updateChan, nil
}

func buildModeFilterContext(modeID string) map[string]any {
	normalized := strings.ToLower(strings.TrimSpace(modeID))
	if normalized == "" {
		normalized = mode.ModeOff
	}

	modeDef := mode.GetBuiltinMode(normalized)
	if modeDef == nil {
		normalized = mode.ModeOff
		modeDef = mode.GetBuiltinMode(normalized)
	}
	if modeDef == nil {
		return nil
	}

	if normalized == mode.ModeOff || normalized == mode.ModeAct {
		return nil
	}

	return map[string]any{
		"mode_name":          modeDef.Name,
		"allowed_tools":      modeDef.AllowedTools,
		"blocked_tools":      modeDef.BlockedTools,
		"hide_blocked_tools": modeDef.HideBlockedTools,
	}
}

// CancelExecution implements core.SDKBridge.CancelExecution.
func (b *Bridge) CancelExecution() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.cancelFn != nil {
		b.cancelFn()
		b.cancelFn = nil
	}

	return nil
}

// GetProvider implements core.SDKBridge.GetProvider.
func (b *Bridge) GetProvider() (name string, model string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.currentProvider, b.currentModel
}

// SetProvider sets the current provider and model.
func (b *Bridge) SetProvider(providerName, model string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.providers[providerName]; !ok {
		return errors.New("provider not found: " + providerName)
	}

	b.currentProvider = providerName
	b.currentModel = model
	return nil
}

// SwitchProfile changes the active profile and updates current model from the "main" alias.
func (b *Bridge) SwitchProfile(profileID string) error {
	if b.profileStore == nil {
		return errors.New("profile store not configured")
	}

	// Get the profile
	p, err := b.profileStore.GetProfile(profileID)
	if err != nil {
		return fmt.Errorf("failed to get profile: %w", err)
	}

	// Resolve the "main" alias to get provider/model
	pointer, err := p.GetPointer(profile.AliasMain)
	if err != nil {
		return fmt.Errorf("profile %s has no main alias: %w", profileID, err)
	}

	// Update current provider and model
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentProvider = pointer.Provider
	b.currentModel = pointer.Model

	return nil
}

// GetToolRegistry implements core.SDKBridge.GetToolRegistry.
func (b *Bridge) GetToolRegistry() core.ToolRegistryInfo {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.toolRegistry == nil {
		return core.ToolRegistryInfo{}
	}

	// Get tool list from registry - List() returns []string
	toolNames := b.toolRegistry.List()

	info := core.ToolRegistryInfo{
		ToolCount: len(toolNames),
		ToolNames: toolNames,
	}

	type registrationLookup interface {
		ListAll() []string
		GetRegistration(name string) (*tools.ToolRegistration, bool)
	}

	if lookup, ok := b.toolRegistry.(registrationLookup); ok {
		allNames := lookup.ListAll()
		toolsInfo := make([]core.ToolInfo, 0, len(allNames))
		for _, name := range allNames {
			reg, ok := lookup.GetRegistration(name)
			if !ok || reg == nil || reg.Tool == nil {
				continue
			}
			infoItem := core.ToolInfo{
				Name:        name,
				Description: reg.Tool.Description(),
				Enabled:     reg.Enabled,
			}
			if reg.Metadata != nil {
				infoItem.Source = string(reg.Metadata.Source)
				infoItem.MCPServer = reg.Metadata.ServerName
			}
			toolsInfo = append(toolsInfo, infoItem)
		}
		info.Tools = toolsInfo
	} else {
		toolsInfo := make([]core.ToolInfo, 0, len(toolNames))
		for _, name := range toolNames {
			toolsInfo = append(toolsInfo, core.ToolInfo{
				Name:    name,
				Enabled: true,
			})
		}
		info.Tools = toolsInfo
	}

	return info
}

// GetConversationManager implements core.SDKBridge.GetConversationManager.
func (b *Bridge) GetConversationManager() core.ConversationManager {
	return &conversationManagerAdapter{
		manager: b.convManager,
		storage: b.convStorage,
	}
}

// convertIntermediateUpdate converts an SDK IntermediateUpdate to a core StateUpdate.
func convertIntermediateUpdate(update agent.IntermediateUpdate) *core.StateUpdate {
	switch u := update.(type) {
	case agent.ContentUpdate:
		result := core.NewStateUpdate(core.UpdateContentDelta, core.ContentDeltaPayload{
			Content: u.Content,
			Append:  u.Append,
		})
		return &result

	case agent.TokenCountUpdate:
		// Real per-turn token count from the SDK execute loop.
		result := core.NewStateUpdate(core.UpdateTokenCount, core.TokenCountPayload{
			InputTokens:          u.InputTokens,
			OutputTokens:         u.OutputTokens,
			ContextWindow:        u.ContextWindow,
			EffectiveWindow:      u.EffectiveWindow,
			AutoCompactThreshold: u.AutoCompactThreshold,
			PctUsed:              u.PctUsed,
		})
		return &result

	case agent.ThinkingUpdate:
		result := core.NewStateUpdate(core.UpdateThinking, core.ThinkingPayload{
			Content: u.Content,
			Append:  u.Append,
		})
		return &result

	case agent.ToolCallUpdate:
		result := core.NewStateUpdate(core.UpdateToolCall, core.ToolCallPayload{
			ID:         u.ID,
			Name:       u.Name,
			Parameters: u.Parameters,
			Caller:     &core.ToolCaller{Type: "direct"},
		})
		return &result

	case agent.ToolResultUpdate:
		errStr := ""
		if u.Error != nil {
			errStr = u.Error.Error()
		}
		var contentBlocks []core.ContentBlockPayload
		for _, raw := range u.ContentBlocks {
			if block, ok := raw.(tools.ContentBlock); ok {
				cb := core.ContentBlockPayload{
					Type:     string(block.Type),
					Text:     block.Text,
					MimeType: block.MimeType,
					URI:      block.URI,
					Name:     block.Name,
				}
				if len(block.Data) > 0 {
					cb.Data = base64.StdEncoding.EncodeToString(block.Data)
				}
				contentBlocks = append(contentBlocks, cb)
			}
		}
		isError := errStr != ""
		// For shell tools, stdout/stderr are encoded in the output field.
		// Populate both for print-mode consumers; non-shell tools just get Output.
		result := core.NewStateUpdate(core.UpdateToolResult, core.ToolResultPayload{
			CallID:        u.ID,
			Output:        u.Output,
			Error:         errStr,
			ContentBlocks: contentBlocks,
			Hosted:        u.Hosted,
			TaskClass:     u.TaskClass,
			IsError:       isError,
			Stdout:        u.Output, // set to combined output; shell executor overrides below
			Stderr:        "",
		})
		return &result

	case agent.ToolOutputChunk:
		result := core.NewStateUpdate(core.UpdateToolChunk, core.ToolChunkPayload{
			CallID: u.ID,
			Chunk:  u.Chunk,
			Stream: u.Stream,
		})
		return &result

	case agent.HookExecutionUpdate:
		result := core.NewStateUpdate(core.UpdateHookExecution, core.HookExecutionPayload{
			HookName:          u.HookName,
			ToolName:          u.ToolName,
			Phase:             u.Phase,
			Success:           u.Success,
			Output:            u.Output,
			Blocked:           u.Blocked,
			Error:             u.Error,
			Duration:          int(u.Duration.Milliseconds()),
			ExitCode:          u.ExitCode,
			MatchedPattern:    u.MatchedPattern,
			TimeoutConfigured: int(u.TimeoutConfigured.Seconds()),
			WorkingDir:        u.WorkingDir,
			// Real-time fields (v1.2) - Spec: 020-hook-execution-realtime
			HookID:    u.HookID,
			Status:    u.Status,
			StartedAt: u.StartedAt,
		})
		return &result

	case agent.HookOutputChunk:
		// Spec: 020-hook-execution-realtime
		result := core.NewStateUpdate(core.UpdateHookOutputChunk, core.HookOutputChunkPayload{
			HookID:    u.HookID,
			HookName:  u.HookName,
			Chunk:     u.Chunk,
			IsStderr:  u.IsStderr,
			Timestamp: u.Timestamp,
		})
		return &result

	case agent.CompactionNeededUpdate:
		// Proactive compaction needed during multi-turn tool use
		result := core.NewStateUpdate(core.UpdateCompactionWarning, core.CompactionPayload{
			Mode:            "auto",
			Status:          "warning",
			CurrentTokens:   u.CurrentTokens,
			RemainingTokens: u.ContextLimit - u.CurrentTokens,
			ThresholdTokens: u.Threshold,
			Message:         fmt.Sprintf("Approaching context limit: %d tokens (%.1f%% used)", u.CurrentTokens, u.PercentUsed*100),
		})
		return &result

	case agent.SubAgentUpdate:
		// Recursively convert the inner update
		innerUpdate := convertIntermediateUpdate(u.Update)
		if innerUpdate == nil {
			return nil
		}
		result := core.NewStateUpdate(core.UpdateSubAgent, core.SubAgentPayload{
			AgentName: u.AgentName,
			Status:    string(innerUpdate.Type),
			Message:   "", // Could extract from inner payload if needed
		})
		return &result

	default:
		return nil
	}
}

// conversationManagerAdapter adapts manager.Manager to core.ConversationManager.
type conversationManagerAdapter struct {
	manager *manager.Manager
	storage storage.Storage
}

func (a *conversationManagerAdapter) Create(ctx context.Context, opts core.CreateConversationOptions) (string, error) {
	if a.manager == nil {
		return "", nil
	}
	createOpts := manager.CreateOptions{
		Mode:          "act", // Default mode
		WorkspacePath: opts.WorkspacePath,
	}
	// Apply feature-first options (005-agent-mode-feature-first)
	if opts.ProjectID != "" || len(opts.Tags) > 0 {
		createOpts.Metadata = &conversation.ConversationMetadata{
			ProjectID: opts.ProjectID,
			Tags:      opts.Tags,
		}
	}
	conv, err := a.manager.Create(ctx, createOpts)
	if err != nil {
		return "", err
	}
	return conv.ID, nil
}

func (a *conversationManagerAdapter) Load(ctx context.Context, id string) (*core.ConversationState, error) {
	if a.manager == nil {
		return nil, nil
	}

	conv, err := a.manager.Resume(ctx, id)
	if err != nil {
		return nil, err
	}

	// Convert messages
	messages := make([]core.Message, len(conv.Messages))
	for i, msg := range conv.Messages {
		messages[i] = convertSDKMessage(msg)
	}

	var status core.ConversationStatus
	switch conv.Status {
	case conversation.StatusActive:
		status = core.StatusIdle
	case conversation.StatusArchived:
		status = core.StatusComplete
	default:
		status = core.StatusIdle
	}

	return &core.ConversationState{
		ID:       conv.ID,
		Messages: messages,
		Status:   status,
	}, nil
}

func (a *conversationManagerAdapter) Save(ctx context.Context, state *core.ConversationState) error {
	// The SDK manager handles saving internally via AddMessage
	// This is a no-op for the adapter since saves happen during message processing
	return nil
}

func (a *conversationManagerAdapter) AddMessage(ctx context.Context, convID string, msg core.Message) error {
	if a.manager == nil {
		return nil
	}

	// Convert core.Message to conversation.Message
	sdkMsg := convertCoreMessage(msg)
	return a.manager.AddMessage(ctx, convID, sdkMsg)
}

func (a *conversationManagerAdapter) List(ctx context.Context, opts core.ListConversationsOptions) ([]core.ConversationSummary, error) {
	if a.storage == nil {
		fmt.Fprintf(os.Stderr, "[bridge.List] ERROR: storage is nil\n")
		return nil, nil
	}

	fmt.Fprintf(os.Stderr, "[bridge.List] START: workspacePath=%q, projectID=%q, limit=%d, offset=%d\n",
		opts.WorkspacePath, opts.ProjectID, opts.Limit, opts.Offset)

	// Fast path: if filtering by workspace, use WorkspaceStorage.ListByWorkspace to
	// read only that subdirectory instead of scanning every conversation on disk.
	var convs []*conversation.Conversation
	var err error
	if opts.WorkspacePath != "" {
		if ws, ok := a.storage.(storage.WorkspaceStorage); ok {
			convs, err = ws.ListByWorkspace(ctx, opts.WorkspacePath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[bridge.List] ERROR: ListByWorkspace failed: %v\n", err)
				return nil, err
			}
			fmt.Fprintf(os.Stderr, "[bridge.List] ListByWorkspace returned %d conversations\n", len(convs))
		} else {
			// Fallback: full scan + in-memory filter (old behaviour).
			convs, err = a.storage.List(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[bridge.List] ERROR: storage.List failed: %v\n", err)
				return nil, err
			}
			fmt.Fprintf(os.Stderr, "[bridge.List] storage.List returned %d conversations, filtering by workspace\n", len(convs))
			filtered := convs[:0:0]
			for _, conv := range convs {
				matched := conv.WorkspacePath == opts.WorkspacePath
				if !matched && conv.Metadata.Custom != nil {
					if wp, ok := conv.Metadata.Custom["workspace_path"].(string); ok && wp == opts.WorkspacePath {
						matched = true
					}
				}
				if !matched && conv.Metadata.ProjectID == opts.WorkspacePath {
					matched = true
				}
				if matched {
					filtered = append(filtered, conv)
				}
			}
			convs = filtered
			fmt.Fprintf(os.Stderr, "[bridge.List] Filtered to %d conversations\n", len(convs))
		}
	} else {
		convs, err = a.storage.List(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[bridge.List] ERROR: storage.List failed: %v\n", err)
			return nil, err
		}
		fmt.Fprintf(os.Stderr, "[bridge.List] storage.List returned %d conversations (no workspace filter)\n", len(convs))
	}

	// Filter by projectId if specified
	if opts.ProjectID != "" {
		filtered := convs[:0:0]
		for _, conv := range convs {
			if conv.Metadata.ProjectID == opts.ProjectID {
				filtered = append(filtered, conv)
			}
		}
		convs = filtered
		fmt.Fprintf(os.Stderr, "[bridge.List] Filtered to %d conversations by projectID\n", len(convs))
	}

	// Sort newest-first by UpdatedAt
	sort.Slice(convs, func(i, j int) bool {
		return convs[i].UpdatedAt.After(convs[j].UpdatedAt)
	})

	// Apply offset for pagination
	if opts.Offset > 0 {
		if opts.Offset >= len(convs) {
			fmt.Fprintf(os.Stderr, "[bridge.List] Offset %d >= length %d, returning empty\n", opts.Offset, len(convs))
			return []core.ConversationSummary{}, nil
		}
		convs = convs[opts.Offset:]
	}

	// Apply limit (0 = no limit)
	if opts.Limit > 0 && opts.Limit < len(convs) {
		convs = convs[:opts.Limit]
		fmt.Fprintf(os.Stderr, "[bridge.List] Limited to %d conversations\n", len(convs))
	}

	summaries := make([]core.ConversationSummary, len(convs))
	for i, conv := range convs {
		title := conv.ID
		preview := ""

		// Use stored Title if available (generated by naming agent)
		if conv.Title != "" {
			title = conv.Title
			if len(title) > 60 {
				title = title[:60] + "..."
			}
		} else if len(conv.Messages) > 0 {
			// Fall back to first user message if no stored title
			for _, msg := range conv.Messages {
				if msg.Role == conversation.RoleUser {
					title = msg.Content
					if len(title) > 60 {
						title = title[:60] + "..."
					}
					break
				}
			}
		}

		// Generate preview from the last meaningful message (not system/task messages)
		preview = GenerateConversationPreview(conv.Messages)

		workspacePath := conv.WorkspacePath
		if workspacePath == "" && conv.Metadata.Custom != nil {
			if wp, ok := conv.Metadata.Custom["workspace_path"].(string); ok {
				workspacePath = wp
			}
		}

		summaries[i] = core.ConversationSummary{
			ID:            conv.ID,
			Title:         title,
			Preview:       preview,
			MessageCount:  len(conv.Messages),
			TotalTokens:   conv.TotalTokens,
			UpdatedAt:     conv.UpdatedAt,
			Status:        string(conv.Status),
			ProjectID:     conv.Metadata.ProjectID,
			WorkspacePath: workspacePath,
			Tags:          conv.Metadata.Tags,
		}
		fmt.Fprintf(os.Stderr, "[bridge.List]   [%d] ID=%q, Title=%q\n", i, summaries[i].ID, summaries[i].Title)
	}

	fmt.Fprintf(os.Stderr, "[bridge.List] SUCCESS: Returning %d conversation summaries\n", len(summaries))
	return summaries, nil
}

func (a *conversationManagerAdapter) Delete(ctx context.Context, id string) error {
	if a.manager == nil {
		return nil
	}
	return a.manager.Delete(ctx, id)
}

func (a *conversationManagerAdapter) EditMessage(ctx context.Context, convID string, messageID string, newContent string) (*core.EditMessageResult, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("storage not available")
	}
	// Load the conversation
	conv, err := a.storage.Load(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("conversation not found: %w", err)
	}
	// Find the message
	msgIndex := -1
	for i, msg := range conv.Messages {
		if msg.ID == messageID {
			msgIndex = i
			break
		}
	}
	if msgIndex == -1 {
		return nil, fmt.Errorf("message %s not found in conversation %s", messageID, convID)
	}
	msg := conv.Messages[msgIndex]
	oldContent := msg.Content
	role := string(msg.Role)
	truncatedCount := 0
	// Record the edit in history
	msg.AddEdit("user", "Message edited via UI")
	msg.Content = newContent
	conv.Messages[msgIndex] = msg
	// For user messages: truncate everything after
	if msg.Role == conversation.RoleUser {
		truncatedCount = len(conv.Messages) - msgIndex - 1
		conv.Messages = conv.Messages[:msgIndex+1]
		// Recalculate token count
		conv.TotalTokens = 0
		for _, m := range conv.Messages {
			if m.Tokens != nil {
				conv.TotalTokens += m.Tokens.Total
			}
		}
	}
	// Save back
	if err := a.storage.Save(ctx, conv); err != nil {
		return nil, fmt.Errorf("failed to save edited conversation: %w", err)
	}
	return &core.EditMessageResult{
		MessageID:      messageID,
		ConvID:         convID,
		OldContent:     oldContent,
		NewContent:     newContent,
		TruncatedCount: truncatedCount,
		Role:           role,
	}, nil
}

// convertSDKMessage converts an SDK Message to a core Message.
func convertSDKMessage(msg *conversation.Message) core.Message {
	var role core.MessageRole
	switch msg.Role {
	case conversation.RoleUser:
		role = core.RoleUser
	case conversation.RoleAssistant:
		role = core.RoleAssistant
	case conversation.RoleSystem:
		role = core.RoleSystem
	case conversation.RoleTool:
		role = core.RoleTool
	}

	coreMsg := core.Message{
		ID:        msg.ID,
		Role:      role,
		Content:   msg.Content,
		Timestamp: msg.Timestamp,
		Thinking:  msg.Thinking,
		Model:     msg.Model,
	}

	if len(msg.Metadata) > 0 {
		coreMsg.Metadata = make(map[string]any, len(msg.Metadata))
		maps.Copy(coreMsg.Metadata, msg.Metadata)
	}

	// Convert tool calls
	if len(msg.ToolCalls) > 0 {
		coreMsg.ToolCalls = make([]core.ToolCallDisplay, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			coreMsg.ToolCalls[i] = core.ToolCallDisplay{
				ID:         tc.ID,
				Name:       tc.Name,
				Parameters: tc.Parameters,
				Status:     core.ToolStatusComplete,
			}
		}
	}

	// Convert tool results
	if len(msg.ToolResults) > 0 {
		coreMsg.ToolResults = make([]core.ToolResultDisplay, len(msg.ToolResults))
		for i, tr := range msg.ToolResults {
			errStr := ""
			if tr.Error != nil {
				errStr = tr.Error.Message
			}
			coreMsg.ToolResults[i] = core.ToolResultDisplay{
				CallID: tr.CallID,
				Output: tr.Output,
				Error:  errStr,
			}
		}
	}

	// Convert metadata["images"] to attachments
	if msg.Metadata != nil && vision.HasImages(msg.Metadata) {
		images, err := vision.ExtractImagesFromMetadata(msg.Metadata)
		if err == nil && len(images) > 0 {
			coreMsg.Attachments = make([]core.Attachment, 0, len(images))
			coreMsg.Images = make([]core.ContentBlockPayload, 0, len(images))

			for i, img := range images {
				if img.Type == "base64" {
					// Decode base64 to bytes
					data, err := vision.DecodeBase64Image(img.Data)
					if err != nil {
						continue
					}

					fileName := fmt.Sprintf("image_%d", i)
					coreMsg.Attachments = append(coreMsg.Attachments, core.Attachment{
						FileName: fileName,
						MimeType: img.MediaType,
						Content:  data,
						Size:     int64(len(data)),
					})
					coreMsg.Images = append(coreMsg.Images, core.ContentBlockPayload{
						Type:     "image",
						MimeType: img.MediaType,
						Data:     img.Data,
						Name:     fileName,
					})
				}
				// Note: URL-type images are not converted to attachments
				// as they don't have local content
			}
		}
	}

	return coreMsg
}

// convertCoreMessage converts a core Message to an SDK Message.
func convertCoreMessage(msg core.Message) *conversation.Message {
	var role conversation.Role
	switch msg.Role {
	case core.RoleUser:
		role = conversation.RoleUser
	case core.RoleAssistant:
		role = conversation.RoleAssistant
	case core.RoleSystem:
		role = conversation.RoleSystem
	case core.RoleTool:
		role = conversation.RoleTool
	}

	sdkMsg := &conversation.Message{
		ID:        msg.ID,
		Role:      role,
		Content:   msg.Content,
		Timestamp: msg.Timestamp,
		Thinking:  msg.Thinking,
		Model:     msg.Model,
	}

	if len(msg.Metadata) > 0 {
		sdkMsg.Metadata = make(map[string]any, len(msg.Metadata))
		maps.Copy(sdkMsg.Metadata, msg.Metadata)
	}

	// Convert tool calls
	if len(msg.ToolCalls) > 0 {
		sdkMsg.ToolCalls = make([]conversation.ToolCall, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			sdkMsg.ToolCalls[i] = conversation.ToolCall{
				ID:         tc.ID,
				Name:       tc.Name,
				Parameters: tc.Parameters,
			}
		}
	}

	// Convert tool results
	if len(msg.ToolResults) > 0 {
		sdkMsg.ToolResults = make([]conversation.ToolResult, len(msg.ToolResults))
		for i, tr := range msg.ToolResults {
			sdkMsg.ToolResults[i] = conversation.ToolResult{
				CallID: tr.CallID,
				Output: tr.Output,
			}
			if tr.Error != "" {
				sdkMsg.ToolResults[i].Error = &conversation.ToolError{
					Message: tr.Error,
				}
			}
		}
	}

	// Convert attachments to metadata["images"]
	if len(msg.Attachments) > 0 {
		images := make([]*vision.ImageData, 0, len(msg.Attachments))

		for _, att := range msg.Attachments {
			// Convert attachment to ImageData
			img, err := vision.NewImageFromBytes(att.Content, att.MimeType)
			if err != nil {
				// Log error but continue with other attachments
				// Could add logging here if bridge has a logger
				continue
			}
			images = append(images, img)
		}

		// Add images to metadata
		if len(images) > 0 {
			if sdkMsg.Metadata == nil {
				sdkMsg.Metadata = make(map[string]any)
			}
			_ = vision.AddImagesToMetadata(sdkMsg.Metadata, images)
		}
	}

	return sdkMsg
}

// ─── StreamEnd helpers ────────────────────────────────────────────────────────

// finishReasonToStopReason converts a provider.FinishReason to the nullable
// stop_reason string used in Claude's result event.
// Normal completion maps to nil; abnormal exits get a string.
func finishReasonToStopReason(r provider.FinishReason) *string {
	switch r {
	case provider.FinishReasonLength:
		s := "max_tokens"
		return &s
	case provider.FinishReasonError:
		s := "error"
		return &s
	default:
		// FinishReasonStop, FinishReasonToolCalls, empty → null (same as Claude)
		return nil
	}
}

// extractMetaInt extracts an int from a nested metadata map.
// It handles the shape: metadata[outerKey].(map[string]int)[innerKey].
func extractMetaInt(metadata map[string]any, outerKey, innerKey string) int {
	if metadata == nil {
		return 0
	}
	outer, ok := metadata[outerKey]
	if !ok {
		return 0
	}
	switch m := outer.(type) {
	case map[string]int:
		return m[innerKey]
	case map[string]any:
		if v, ok := m[innerKey]; ok {
			switch n := v.(type) {
			case int:
				return n
			case float64:
				return int(n)
			}
		}
	}
	return 0
}

// generateConversationPreview creates a preview string from the last meaningful
// message in a conversation. It skips system messages, task nudges, task maintenance
// reminders, dream prompts, and other non-conversational content.
func GenerateConversationPreview(messages []*conversation.Message) string {
	if len(messages) == 0 {
		return ""
	}

	// System message prefixes that should be skipped
	systemPrefixes := []string{
		"[Task Nudge]",
		"[Task Maintenance Reminder]",
		"# Dream:",
		"# Dream: Memory Consolidation",
		"## Phase 1",
		"## Phase 2",
		"## Phase 3",
		"## Phase 4",
		"You've used",
		"Has the work expanded",
		"Consider:",
		"No pending tasks",
		"You've used",
		"Track your work with tasks:",
		"Use task_create",
	}

	// Walk backwards through messages to find a meaningful one
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg == nil {
			continue
		}

		// Skip system messages entirely
		if msg.Role == conversation.RoleSystem {
			continue
		}

		content := msg.Content
		if content == "" {
			continue
		}

		// Skip messages that start with system prefixes
		skip := false
		for _, prefix := range systemPrefixes {
			if strings.HasPrefix(content, prefix) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		// Skip task-related messages (task nudges and reminders have specific patterns)
		if strings.Contains(content, "[Task Nudge]") ||
			strings.Contains(content, "[Task Maintenance Reminder]") ||
			strings.Contains(content, "No pending tasks") ||
			strings.HasPrefix(content, "Track your work with tasks:") {
			continue
		}

		// Found a meaningful message - truncate and return
		preview := content
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		return preview
	}

	// Fallback: return empty string if no meaningful message found
	return ""
}
