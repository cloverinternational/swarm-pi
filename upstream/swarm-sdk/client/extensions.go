package client

// extensions.go — supplementary Client methods.

import (
	"context"
	"errors"
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
)

// Update is an alias for agent.IntermediateUpdate so callers of Stream()
// do not need to import swarm-sdk/agent directly.
type Update = agent.IntermediateUpdate

// ProviderInfo carries read-only information about the active provider and
// model that the Client was configured with.
type ProviderInfo struct {
	// Name is the provider identifier ("anthropic", "openai", "gemini", etc.)
	Name string
	// Model is the model ID as passed to WithProvider().
	Model string
	// ContextWindow is the effective maximum context window in tokens.
	// Returns 0 when the provider is not yet initialised.
	ContextWindow int
}

// IsLegacyEngineEnabled reports whether the SWARM_USE_LEGACY_ENGINE=1
// environment variable is set.  When true, callers should route execution
// through the legacy headless engine/bridge rather than sdk/client.Client.
func IsLegacyEngineEnabled() bool {
	return os.Getenv("SWARM_USE_LEGACY_ENGINE") == "1"
}

// Stream returns a channel that receives every IntermediateUpdate emitted
// during subsequent Chat/Execute calls.  The channel is closed when ctx is
// cancelled or when the returned cancel func is called.
//
// Typical usage:
//
//	ch, err := c.Stream(ctx)
//	go func() {
//	    for ev := range ch {
//	        // render ev
//	    }
//	}()
//	_, err = c.Chat("hello")
func (c *Client) Stream(ctx context.Context) (<-chan Update, error) {
	ch := make(chan Update, 64)

	unsub := c.SubscribeUpdates(func(_ context.Context, ev agent.IntermediateUpdate) error {
		select {
		case ch <- ev:
		case <-ctx.Done():
		}
		return nil
	})

	// Close channel and deregister subscriber when context is cancelled.
	go func() {
		<-ctx.Done()
		unsub()
		close(ch)
	}()

	return ch, nil
}

// Cancel stops the currently executing agent turn.  It is safe to call from
// any goroutine and returns nil on success.  If no turn is running the call
// is a no-op.
//
// Cancel honours the SendMessage cancel hook (so subscribers see a
// stream-end event) before stopping the underlying agent loop.
func (c *Client) Cancel(_ context.Context) error {
	c.sessExecMu.Lock()
	cancel := c.sessExecCancel
	c.sessExecCancel = nil
	c.sessExecMu.Unlock()
	// Cancel the in-flight turn's context. The agent's executeLoop selects on
	// this ctx (agent_execute.go) and unwinds promptly, so this — not a
	// lifecycle Stop — is what actually halts the turn.
	if cancel != nil {
		cancel()
	}
	// Return the agent to a clean idle baseline WITHOUT tearing it down.
	// Previously Cancel() called StopAgent() → agent.Stop(), which cancelled
	// the agent's ROOT context (a.ctx) permanently and parked the state machine
	// in StateStopped. Both leaked into the next turn: every subsequent Execute
	// ran on a dead root context (forcing retries/timeouts) and could stall on
	// the non-idle state machine — the observed "next turn after cancel is very
	// slow until daemon restart" bug. InterruptTurn fixes that by resetting only
	// the per-turn execution state.
	if c.agent != nil {
		c.agent.InterruptTurn()
	}
	return nil
}

// ErrNoConfigManager is returned by methods that require a configbundle
// Manager when none was supplied at construction time via
// WithConfigManager.
var ErrNoConfigManager = errors.New("client: no ConfigManager configured")

// CompactConversation summarises a conversation's message history, replacing
// it with a compact summary message that preserves semantic context while
// reducing token count.
//
// The conversation is loaded from storage, compacted using a default
// compaction service, and saved back.  If the conversation does not exist,
// or compaction produces no result, an error is returned.
//
// Callers that need the structured outcome (before/after token counts,
// new conversation ID, etc.) should use CompactConversationDetail.
func (c *Client) CompactConversation(ctx context.Context, convID string) error {
	_, err := c.CompactConversationDetail(ctx, convID)
	return err
}

// CompactConversationDetail performs the same operation as CompactConversation
// but returns the compaction.CompactionResult so callers can inspect token
// counts, recovered files, and the resulting conversation ID.  Returns
// (nil, error) on any failure path.
func (c *Client) CompactConversationDetail(ctx context.Context, convID string) (*compaction.CompactionResult, error) {
	conv, err := c.convManager.Resume(ctx, convID)
	if err != nil {
		return nil, err
	}

	svc := compaction.NewService(compaction.CompactionConfig{})

	// Wire a summarisation function using the client's provider if available.
	if c.provider != nil {
		svc.SetSummarizeFunc(
			compaction.ProviderSummarizeFunc(c.provider, c.opts.model),
		)
	}

	result, err := svc.Compact(ctx, conv, true /* manual */)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("CompactConversation: compaction returned no result")
	}

	if err := c.convManager.Save(ctx, conv); err != nil {
		return result, err
	}
	return result, nil
}

// ConversationManager returns the underlying conversation manager for
// callers that need lower-level access (e.g. session.ClientSession's
// EditMessage implementation, which mutates a loaded conversation in
// place and saves it back).  May be nil when the client was constructed
// without conversation persistence.
func (c *Client) ConversationManager() *manager.Manager {
	return c.convManager
}

// ProviderInfo returns read-only information about the active provider and
// model.
func (c *Client) ProviderInfo() ProviderInfo {
	info := ProviderInfo{
		Name:  c.opts.providerName,
		Model: c.opts.model,
	}
	if c.provider != nil {
		caps := c.provider.Capabilities()
		info.ContextWindow = caps.MaxContextWindow
	}
	return info
}

// CurrentModel returns the active model ID (c.opts.model).  Convenience
// accessor for callers that only need the model string and want a clear
// single-purpose entry point distinct from ProviderInfo().
func (c *Client) CurrentModel() string {
	if c == nil {
		return ""
	}
	return c.opts.model
}

// ProviderName returns the active provider name (c.opts.providerName).
// Convenience accessor mirroring CurrentModel().
func (c *Client) ProviderName() string {
	if c == nil {
		return ""
	}
	return c.opts.providerName
}

// MaxTokens returns the configured maximum output tokens per request
// (c.opts.maxTokens).  The execution path reads this same value live, so a
// value updated via SetMaxTokens takes effect on the next turn.  Convenience
// accessor mirroring CurrentModel()/ProviderName() so embedding callers can
// treat the client as the single source of truth for request limits.
func (c *Client) MaxTokens() int {
	if c == nil {
		return 0
	}
	return c.opts.maxTokens
}

// ApplyPromptCaching enables Anthropic prefix-based prompt caching for a single
// request by writing ephemeral cache-control markers into req.Context. The
// provider translate layer reads these to add cache_control to the system
// prompt, tools, and messages. ttl is typically "5m" or "1h". Keeping the
// provider-specific cache-control keys inside the SDK lets embedding programs
// (and the TUI) toggle caching without encoding translate-layer details — the
// "how" of caching belongs to the client, not the UI.
func (c *Client) ApplyPromptCaching(req *agent.ExecuteRequest, ttl string) {
	if req == nil {
		return
	}
	if req.Context == nil {
		req.Context = make(map[string]any)
	}
	req.Context["system_cache_control"] = map[string]string{"type": "ephemeral", "ttl": ttl}
	req.Context["tool_cache_control"] = map[string]string{"type": "ephemeral", "ttl": ttl}
	req.Context["message_cache_control"] = map[string]string{"type": "ephemeral", "ttl": ttl}
}

// ApplyThinking writes extended-thinking configuration into req.Context so the
// provider translate layer can build the right output_config. When enabled it
// sets thinking_enabled/thinking_budget (and thinking_effort when non-empty).
// When disabled it sets thinking_enabled=false EXPLICITLY — required so that
// providers such as Gemini don't default thinking on for 2.5/3 models. Keeps the
// thinking context-key contract inside the SDK rather than the UI layer.
func (c *Client) ApplyThinking(req *agent.ExecuteRequest, enabled bool, budget int, effort string) {
	if req == nil {
		return
	}
	if req.Context == nil {
		req.Context = make(map[string]any)
	}
	if !enabled {
		req.Context["thinking_enabled"] = false
		return
	}
	req.Context["thinking_enabled"] = true
	req.Context["thinking_budget"] = budget
	if effort != "" {
		req.Context["thinking_effort"] = effort
	}
}

// ApplyModeFilter writes the operating-mode tool-filtering contract into
// req.Context under "mode_filter". The map shape (mode_name/allowed_tools/
// blocked_tools/hide_blocked_tools) is the bridge the agent reads to apply
// per-mode tool restrictions — keeping these contract keys in the SDK means the
// UI doesn't hard-code them. The caller decides WHEN filtering applies (only
// restrictive modes; off/act allow all tools).
func (c *Client) ApplyModeFilter(req *agent.ExecuteRequest, modeName string, allowed, blocked []string, hideBlocked bool) {
	if req == nil {
		return
	}
	if req.Context == nil {
		req.Context = make(map[string]any)
	}
	req.Context["mode_filter"] = map[string]any{
		"mode_name":          modeName,
		"allowed_tools":      allowed,
		"blocked_tools":      blocked,
		"hide_blocked_tools": hideBlocked,
	}
}

// OperatingModeID returns the current operating mode ID
// ("act"|"plan"|"auto"|"off"|...).  This is a thin alias over ActiveMode()
// kept distinct so TUI/IPC callers can query mode by a name that matches
// the setter (SetMode) symmetrically.  Returns "" when no mode has been
// initialised.
func (c *Client) OperatingModeID() string {
	if c == nil {
		return ""
	}
	return c.ActiveMode()
}
