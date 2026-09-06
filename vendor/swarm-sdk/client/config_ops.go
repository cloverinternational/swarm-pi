// Package client — config_ops.go
//
// Per-call configuration operations (mode, model, provider, profile)
// and compaction lifted from session.ClientSession.  These methods
// reconfigure the client and emit themed events on success.
package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
)

// SetMode updates the operating mode ("plan", "act", "auto").  The
// next SendMessage uses the new mode.
func (c *Client) SetMode(_ context.Context, mode string) error {
	if mode == "" {
		return errors.New("SetMode: mode is required")
	}
	if err := c.rejectHarnessGovernedMutation("SetMode"); err != nil {
		return err
	}
	c.stateMu.Lock()
	c.sessState.OperatingMode = mode
	c.sessState.UpdatedAt = time.Now()
	c.stateMu.Unlock()
	c.dispatchEvent(Event{Kind: EventModeChanged, Payload: ModePayload{Mode: mode}, At: time.Now()})
	return nil
}

// SetMaxTokens updates the maximum output tokens per request.  The execution
// path reads c.opts.maxTokens live under c.mu, so the new limit takes effect on
// the next Chat / Execute turn without an agent rebuild.  Lets an embedding
// program drive request limits through the client rather than the UI layer.
func (c *Client) SetMaxTokens(_ context.Context, n int) error {
	if n <= 0 {
		return errors.New("SetMaxTokens: n must be positive")
	}
	if err := c.rejectHarnessGovernedMutation("SetMaxTokens"); err != nil {
		return err
	}
	c.mu.Lock()
	old := c.opts.maxTokens
	c.opts.maxTokens = n
	c.mu.Unlock()
	// Notify subscribers so a thin UI re-renders the token/limit display.
	c.dispatchEvent(Event{Kind: EventConfigChanged, Payload: ConfigPayload{
		Key:      "maxTokens",
		OldValue: old,
		NewValue: n,
	}, At: time.Now()})
	return nil
}

// SetNoFallback toggles the no-fallback policy at runtime. When enabled, the
// agent only attempts its chain primary; on failure the real primary error
// surfaces instead of cascading into fallback providers (which may be
// unregistered). The setting is persisted into c.opts so it survives any later
// Reconfigure (provider/model switch) and is applied immediately to the live
// agent. Mirrors SetMaxTokens: no agent rebuild required.
func (c *Client) SetNoFallback(_ context.Context, noFallback bool) error {
	if err := c.rejectHarnessGovernedMutation("SetNoFallback"); err != nil {
		return err
	}
	c.mu.Lock()
	c.opts.noFallback = noFallback
	a := c.agent
	c.mu.Unlock()
	if a != nil {
		a.SetNoFallback(noFallback)
	}
	return nil
}

// SetModel updates the active model on the underlying client.  Calls
// Reconfigure(WithProvider(currentProvider, model)) so the new model
// takes effect on the next Chat / Execute turn.  On success the
// session state and EventModelChanged subscribers are updated.
func (c *Client) SetModel(_ context.Context, model string) error {
	if model == "" {
		return errors.New("SetModel: model is required")
	}
	info := c.ProviderInfo()
	if info.Name == "" {
		return errors.New("SetModel: client has no active provider — use SetProvider")
	}
	if err := c.Reconfigure(WithProviderString(info.Name, model)); err != nil {
		return err
	}
	c.applyProviderInfo()
	return nil
}

// SetProvider updates the active provider (and optionally model) by
// rebuilding the underlying client via Reconfigure.  When model is
// empty, the current model is retained.  On success the session state
// and EventModelChanged subscribers are updated.
func (c *Client) SetProvider(_ context.Context, providerName, model string) error {
	if providerName == "" {
		return errors.New("SetProvider: provider is required")
	}
	if model == "" {
		model = c.ProviderInfo().Model
	}
	if err := c.Reconfigure(WithProviderString(providerName, model)); err != nil {
		return err
	}
	c.applyProviderInfo()
	return nil
}

// applyProviderInfo re-reads the live provider info, mirrors it into
// session state, and dispatches EventModelChanged.  Callers should hold
// no locks.
func (c *Client) applyProviderInfo() {
	info := c.ProviderInfo()
	c.stateMu.Lock()
	c.sessState.Provider = info.Name
	c.sessState.Model = info.Model
	c.sessState.ModelContextWindow = info.ContextWindow
	c.sessState.UpdatedAt = time.Now()
	c.stateMu.Unlock()
	c.dispatchEvent(Event{Kind: EventModelChanged, Payload: ModelPayload{
		Provider:      info.Name,
		Model:         info.Model,
		ContextWindow: info.ContextWindow,
	}, At: time.Now()})
}

// SwitchProfile activates the named profile from the configbundle.
// Looks up the profile by ID/Name in the active bundle's inline
// profiles, then rebuilds the underlying client via Reconfigure with
// the profile's Provider, Model, SystemPrompt, and MaxTokens.  Records
// the switch in session state and emits EventProfileChanged.
//
// Returns ErrNoConfigManager when the client was constructed without
// a configbundle.Manager.  Returns an error if the profile is not
// found or if Reconfigure fails.
func (c *Client) SwitchProfile(_ context.Context, profileID string) error {
	if profileID == "" {
		return errors.New("SwitchProfile: profileID is required")
	}
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}

	bundle := c.cfgMgr.Active()
	if bundle == nil {
		return errors.New("SwitchProfile: no active config bundle")
	}

	var prof *configbundle.ProfileDefinition
	for i := range bundle.Profiles.Inline {
		if bundle.Profiles.Inline[i].ID == profileID || bundle.Profiles.Inline[i].Name == profileID {
			prof = &bundle.Profiles.Inline[i]
			break
		}
	}
	if prof == nil {
		return fmt.Errorf("SwitchProfile: profile %q not found", profileID)
	}

	var opts []Option
	currentInfo := c.ProviderInfo()
	provider := prof.Provider
	if provider == "" {
		provider = currentInfo.Name
	}
	model := prof.Model
	if model == "" {
		model = currentInfo.Model
	}
	if provider != "" {
		opts = append(opts, WithProviderString(provider, model))
	}
	if prof.SystemPrompt != "" {
		opts = append(opts, WithSystemPrompt(prof.SystemPrompt))
	}
	if prof.MaxTokens > 0 {
		opts = append(opts, WithMaxTokens(prof.MaxTokens))
	}
	if err := c.Reconfigure(opts...); err != nil {
		return fmt.Errorf("SwitchProfile: %w", err)
	}

	c.stateMu.Lock()
	c.sessState.ActiveProfile = prof.ID
	c.stateMu.Unlock()

	c.dispatchEvent(Event{Kind: EventProfileChanged, Payload: ProfilePayload{
		ProfileID: prof.ID,
		Action:    "set",
	}, At: time.Now()})
	c.applyProviderInfo()
	return nil
}

// Compact runs manual compaction on convID via CompactConversation.  When
// convID is empty the active conversation is used.
func (c *Client) Compact(ctx context.Context, convID string) error {
	_, err := c.CompactDetail(ctx, convID)
	return err
}

// CompactDetail runs manual compaction and returns the underlying
// compaction.CompactionResult (so callers populating a
// ManualCompactionOutcome can echo before/after token counts and the
// new conversation ID).  Returns (nil, error) on any failure path.  On
// success the State.CompactionCount is incremented.
func (c *Client) CompactDetail(ctx context.Context, convID string) (*compaction.CompactionResult, error) {
	if convID == "" {
		c.stateMu.RLock()
		convID = c.sessState.ActiveConvID
		c.stateMu.RUnlock()
	}
	if convID == "" {
		return nil, errors.New("Compact: no active conversation")
	}
	result, err := c.CompactConversationDetail(ctx, convID)
	if err != nil {
		return nil, err
	}
	c.stateMu.Lock()
	c.sessState.CompactionCount++
	if result != nil && result.CompactedTokens > 0 {
		c.sessState.CurrentContext = result.CompactedTokens
	}
	c.sessState.UpdatedAt = time.Now()
	c.stateMu.Unlock()

	// Notify subscribers so UIs can observe compaction.
	if result != nil {
		c.dispatchEvent(Event{Kind: EventCompaction, Payload: CompactionPayload{
			ConvID:       convID,
			BeforeTokens: result.OriginalTokens,
			AfterTokens:  result.CompactedTokens,
			NewConvID:    result.NewConvID,
		}, At: time.Now()})
	}
	return result, nil
}
