// Package client — crud.go
//
// CRUD-style methods that mutate the active configbundle.ConfigBundle and
// emit themed Events.  Lifted from the legacy session.ClientSession surface
// so *client.Client can drive IPC/ACP servers directly without an
// intermediate session wrapper.
//
// Each method:
//  1. Mutates the active configbundle.ConfigBundle via Manager.UpdateActive
//     (when a Manager was supplied at construction).
//  2. Persists the change (Manager.Save) where the legacy engine's handler
//     did.
//  3. Emits a session-level Event so subscribers see the result.
//
// When ConfigManager is nil (i.e. no WithConfigManager option was passed)
// the method returns ErrNoConfigManager.  This keeps the surface usable for
// unit tests that only exercise event fan-out without an on-disk config
// dependency.
package client

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
)

// configKeyActiveSystemPrompt is the SystemConfig.Custom key used to pin
// the active named system prompt.  PromptsConfig has no active-pointer
// field of its own, so SetSystemPrompt stores the selection here.
const configKeyActiveSystemPrompt = "activeSystemPrompt"

// ─── Agent CRUD ───────────────────────────────────────────────────────────────

// SetAgent updates the currently active agent and emits EventAgentChanged.
// When a ConfigManager is set, the change is also persisted to the active
// SystemConfig.DefaultAgent (mirroring the legacy engine's behaviour of
// remembering the last selection across restarts).
func (c *Client) SetAgent(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("SetAgent: name is required")
	}
	c.stateMu.Lock()
	c.sessState.ActiveAgent = name
	c.sessState.UpdatedAt = time.Now()
	c.stateMu.Unlock()

	if c.cfgMgr != nil {
		_ = c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
			cb.System.DefaultAgent = name
			cb.Agents.DefaultAgent = name
		})
		_ = c.cfgMgr.Save(ctx)
	}

	c.dispatchEvent(Event{Kind: EventAgentChanged, Payload: AgentPayload{
		Name:   name,
		Action: "set",
	}, At: time.Now()})
	return nil
}

// CreateAgent appends a new agent definition to the active config bundle
// and persists.  Returns ErrNoConfigManager when no ConfigManager was
// supplied at construction.
func (c *Client) CreateAgent(ctx context.Context, spec AgentSpec) error {
	return c.upsertAgent(ctx, spec, "CreateAgent", "created")
}

// UpdateAgent replaces an existing agent definition in the active config
// bundle.  If no agent with the same Name exists it is appended.
func (c *Client) UpdateAgent(ctx context.Context, spec AgentSpec) error {
	return c.upsertAgent(ctx, spec, "UpdateAgent", "updated")
}

func (c *Client) upsertAgent(ctx context.Context, spec AgentSpec, op, action string) error {
	if spec.Name == "" {
		return fmt.Errorf("%s: name is required", op)
	}
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}

	def := agentDefFromSpec(spec)
	if err := c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
		cb.Agents.Definitions = appendOrReplaceAgent(cb.Agents.Definitions, def)
		if spec.IsDefault {
			cb.Agents.DefaultAgent = spec.Name
		}
	}); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if err := c.cfgMgr.Save(ctx); err != nil {
		return fmt.Errorf("%s save: %w", op, err)
	}

	c.dispatchEvent(Event{Kind: EventAgentChanged, Payload: agentPayloadFromSpec(spec, action), At: time.Now()})
	return nil
}

// DeleteAgent removes an agent definition from the active config bundle.
func (c *Client) DeleteAgent(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("DeleteAgent: name is required")
	}
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}

	if err := c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
		filtered := cb.Agents.Definitions[:0]
		for _, d := range cb.Agents.Definitions {
			if d.Name == name || d.ID == name {
				continue
			}
			filtered = append(filtered, d)
		}
		cb.Agents.Definitions = filtered
	}); err != nil {
		return fmt.Errorf("DeleteAgent: %w", err)
	}
	if err := c.cfgMgr.Save(ctx); err != nil {
		return fmt.Errorf("DeleteAgent save: %w", err)
	}

	c.dispatchEvent(Event{Kind: EventAgentChanged, Payload: AgentPayload{
		Name:   name,
		Action: "deleted",
	}, At: time.Now()})
	return nil
}

// ─── Profile CRUD ─────────────────────────────────────────────────────────────

// CreateProfile appends a new profile definition to the active config
// bundle's inline profiles list and persists.
func (c *Client) CreateProfile(ctx context.Context, spec ProfileSpec) error {
	return c.upsertProfile(ctx, spec, "CreateProfile", "created")
}

// UpdateProfile replaces an existing profile definition in the inline
// list (or appends if absent) and persists.
func (c *Client) UpdateProfile(ctx context.Context, spec ProfileSpec) error {
	return c.upsertProfile(ctx, spec, "UpdateProfile", "updated")
}

func (c *Client) upsertProfile(ctx context.Context, spec ProfileSpec, op, action string) error {
	if spec.Name == "" {
		return fmt.Errorf("%s: name is required", op)
	}
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}

	def := profileDefFromSpec(spec)
	if err := c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
		cb.Profiles.Inline = appendOrReplaceProfile(cb.Profiles.Inline, def)
	}); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if err := c.cfgMgr.Save(ctx); err != nil {
		return fmt.Errorf("%s save: %w", op, err)
	}

	c.dispatchEvent(Event{Kind: EventProfileChanged, Payload: ProfilePayload{
		ProfileID:   def.ID,
		Name:        spec.Name,
		Description: spec.Description,
		Action:      action,
	}, At: time.Now()})
	return nil
}

// DeleteProfile removes a profile definition by name (or ID) from the
// inline list and persists.
func (c *Client) DeleteProfile(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("DeleteProfile: name is required")
	}
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}

	if err := c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
		filtered := cb.Profiles.Inline[:0]
		for _, p := range cb.Profiles.Inline {
			if p.Name == name || p.ID == name {
				continue
			}
			filtered = append(filtered, p)
		}
		cb.Profiles.Inline = filtered
	}); err != nil {
		return fmt.Errorf("DeleteProfile: %w", err)
	}
	if err := c.cfgMgr.Save(ctx); err != nil {
		return fmt.Errorf("DeleteProfile save: %w", err)
	}

	c.dispatchEvent(Event{Kind: EventProfileChanged, Payload: ProfilePayload{
		ProfileID: name,
		Name:      name,
		Action:    "deleted",
	}, At: time.Now()})
	return nil
}

// ─── Hook CRUD ────────────────────────────────────────────────────────────────

// CreateHook appends a new hook to the active config bundle and persists.
func (c *Client) CreateHook(ctx context.Context, spec HookSpec) error {
	if spec.Name == "" {
		return errors.New("CreateHook: name is required")
	}
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}

	def := hookDefFromSpec(spec)
	if err := c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
		cb.Hooks.Definitions = append(cb.Hooks.Definitions, def)
	}); err != nil {
		return fmt.Errorf("CreateHook: %w", err)
	}
	if err := c.cfgMgr.Save(ctx); err != nil {
		return fmt.Errorf("CreateHook save: %w", err)
	}

	c.dispatchEvent(Event{Kind: EventHookChanged, Payload: hookPayloadFromSpec(spec, "created"), At: time.Now()})
	return nil
}

// UpdateHook replaces an existing hook (matched by Name) and persists.
func (c *Client) UpdateHook(ctx context.Context, spec HookSpec) error {
	if spec.Name == "" {
		return errors.New("UpdateHook: name is required")
	}
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}

	def := hookDefFromSpec(spec)
	if err := c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
		for i, h := range cb.Hooks.Definitions {
			if h.Name == spec.Name || h.ID == spec.Name {
				cb.Hooks.Definitions[i] = def
				return
			}
		}
		cb.Hooks.Definitions = append(cb.Hooks.Definitions, def)
	}); err != nil {
		return fmt.Errorf("UpdateHook: %w", err)
	}
	if err := c.cfgMgr.Save(ctx); err != nil {
		return fmt.Errorf("UpdateHook save: %w", err)
	}

	c.dispatchEvent(Event{Kind: EventHookChanged, Payload: hookPayloadFromSpec(spec, "updated"), At: time.Now()})
	return nil
}

// DeleteHook removes a hook by name and persists.
func (c *Client) DeleteHook(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("DeleteHook: name is required")
	}
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}

	if err := c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
		filtered := cb.Hooks.Definitions[:0]
		for _, h := range cb.Hooks.Definitions {
			if h.Name == name || h.ID == name {
				continue
			}
			filtered = append(filtered, h)
		}
		cb.Hooks.Definitions = filtered
	}); err != nil {
		return fmt.Errorf("DeleteHook: %w", err)
	}
	if err := c.cfgMgr.Save(ctx); err != nil {
		return fmt.Errorf("DeleteHook save: %w", err)
	}

	c.dispatchEvent(Event{Kind: EventHookChanged, Payload: HookPayload{
		Name:   name,
		Action: "deleted",
	}, At: time.Now()})
	return nil
}

// ToggleHook flips the Enabled flag on the named hook and persists.
// Toggling reflects the on-disk Disabled list rather than the inline
// Enabled pointer (mirrors HooksConfig conventions).
func (c *Client) ToggleHook(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("ToggleHook: name is required")
	}
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}

	var nowEnabled bool
	if err := c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
		// Source of truth for "is this enabled right now":
		//   - definition.Enabled (when non-nil) AND
		//   - !contains(Hooks.Disabled, name)
		// Toggling flips the disabled list and the inline pointer in step.
		idx := -1
		for i, h := range cb.Hooks.Definitions {
			if h.Name == name || h.ID == name {
				idx = i
				break
			}
		}

		disabled := slices.Contains(cb.Hooks.Disabled, name)
		if idx >= 0 && cb.Hooks.Definitions[idx].Enabled != nil && !*cb.Hooks.Definitions[idx].Enabled {
			disabled = true
		}

		if disabled {
			cb.Hooks.Disabled = slices.DeleteFunc(cb.Hooks.Disabled, func(s string) bool { return s == name })
			if idx >= 0 {
				t := true
				cb.Hooks.Definitions[idx].Enabled = &t
			}
			nowEnabled = true
		} else {
			cb.Hooks.Disabled = append(cb.Hooks.Disabled, name)
			if idx >= 0 {
				f := false
				cb.Hooks.Definitions[idx].Enabled = &f
			}
			nowEnabled = false
		}
	}); err != nil {
		return fmt.Errorf("ToggleHook: %w", err)
	}
	if err := c.cfgMgr.Save(ctx); err != nil {
		return fmt.Errorf("ToggleHook save: %w", err)
	}

	c.dispatchEvent(Event{Kind: EventHookChanged, Payload: HookPayload{
		Name:    name,
		Enabled: nowEnabled,
		Action:  "toggled",
	}, At: time.Now()})
	return nil
}

// ToggleSkill enables or disables an installed skill by ID/name and persists.
// Additive read/write pair with ActiveConfigBundle's skills listing.
func (c *Client) ToggleSkill(ctx context.Context, name string, enabled bool) error {
	if name == "" {
		return errors.New("ToggleSkill: name is required")
	}
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}
	found := false
	if err := c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
		for i := range cb.Skills.Installed {
			s := &cb.Skills.Installed[i]
			if s.ID == name || s.Name == name {
				v := enabled
				s.Enabled = &v
				found = true
				return
			}
		}
	}); err != nil {
		return fmt.Errorf("ToggleSkill: %w", err)
	}
	if !found {
		// The skill may be a built-in / auto-loaded one (present in the runtime
		// skill registry, which LoadedSkills lists) rather than an installed
		// plugin in the config bundle. Those are always available in the
		// Claude-style skill model and can't be individually toggled — return an
		// honest, specific error instead of a misleading "not found" so the UI
		// can message it clearly rather than looking broken.
		if c.skillRegistry != nil {
			if err := c.skillRegistry.Activate(name); err == nil {
				return fmt.Errorf("ToggleSkill: %q is a built-in skill and is always available (only installed plugins can be toggled)", name)
			}
		}
		return fmt.Errorf("ToggleSkill: skill %q not found", name)
	}
	if err := c.cfgMgr.Save(ctx); err != nil {
		return fmt.Errorf("ToggleSkill save: %w", err)
	}
	c.dispatchEvent(Event{Kind: EventConfigChanged, Payload: ConfigPayload{
		Key:      "skill:" + name,
		NewValue: enabled,
	}, At: time.Now()})
	return nil
}

// ─── System prompt ────────────────────────────────────────────────────────────

// SetSystemPrompt installs (or replaces) a named system prompt and marks
// it active.  When content is empty, the existing prompt with matching
// Name is left intact and only the active selection changes.
func (c *Client) SetSystemPrompt(ctx context.Context, name, content string) error {
	if name == "" {
		return errors.New("SetSystemPrompt: name is required")
	}
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}

	if err := c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
		if cb.Prompts.Custom == nil {
			cb.Prompts.Custom = make(map[string]string)
		}
		if content != "" {
			cb.Prompts.Custom[name] = content
		}
		// Pin "active" via SystemConfig.Custom (PromptsConfig itself has
		// no active-pointer field).  This matches what the engine's
		// SystemPromptsConfig.ActivePrompt did pre-migration.
		if cb.System.Custom == nil {
			cb.System.Custom = make(map[string]any)
		}
		cb.System.Custom[configKeyActiveSystemPrompt] = name
	}); err != nil {
		return fmt.Errorf("SetSystemPrompt: %w", err)
	}
	if err := c.cfgMgr.Save(ctx); err != nil {
		return fmt.Errorf("SetSystemPrompt save: %w", err)
	}

	c.dispatchEvent(Event{Kind: EventSystemPromptChanged, Payload: SystemPromptPayload{
		Name:    name,
		Content: content,
	}, At: time.Now()})
	return nil
}

// ─── Context source ──────────────────────────────────────────────────────────

// AddContextSource appends a context source to the active config bundle
// and persists.
func (c *Client) AddContextSource(ctx context.Context, spec ContextSourceSpec) error {
	if spec.Name == "" {
		return errors.New("AddContextSource: name is required")
	}
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}

	def := contextSourceDefFromSpec(spec)
	if err := c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
		cb.ContextSources.Sources = append(cb.ContextSources.Sources, def)
	}); err != nil {
		return fmt.Errorf("AddContextSource: %w", err)
	}
	if err := c.cfgMgr.Save(ctx); err != nil {
		return fmt.Errorf("AddContextSource save: %w", err)
	}

	c.dispatchEvent(Event{Kind: EventContextSourceChanged, Payload: ContextSourcePayload{
		Name:    spec.Name,
		Type:    spec.Type,
		Enabled: spec.Enabled,
		Action:  "added",
	}, At: time.Now()})
	return nil
}

// RemoveContextSource removes a context source by name and persists.
func (c *Client) RemoveContextSource(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("RemoveContextSource: name is required")
	}
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}

	if err := c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
		filtered := cb.ContextSources.Sources[:0]
		for _, src := range cb.ContextSources.Sources {
			if src.Name == name || src.ID == name {
				continue
			}
			filtered = append(filtered, src)
		}
		cb.ContextSources.Sources = filtered
	}); err != nil {
		return fmt.Errorf("RemoveContextSource: %w", err)
	}
	if err := c.cfgMgr.Save(ctx); err != nil {
		return fmt.Errorf("RemoveContextSource save: %w", err)
	}

	c.dispatchEvent(Event{Kind: EventContextSourceChanged, Payload: ContextSourcePayload{
		Name:   name,
		Action: "removed",
	}, At: time.Now()})
	return nil
}

// ToggleContextSource flips the Enabled flag on the named context source
// and persists.
func (c *Client) ToggleContextSource(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("ToggleContextSource: name is required")
	}
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}

	var nowEnabled bool
	var srcType string
	if err := c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
		for i, src := range cb.ContextSources.Sources {
			if src.Name == name || src.ID == name {
				cur := true
				if src.Enabled != nil {
					cur = *src.Enabled
				}
				flipped := !cur
				cb.ContextSources.Sources[i].Enabled = &flipped
				nowEnabled = flipped
				srcType = src.Type
				return
			}
		}
	}); err != nil {
		return fmt.Errorf("ToggleContextSource: %w", err)
	}
	if err := c.cfgMgr.Save(ctx); err != nil {
		return fmt.Errorf("ToggleContextSource save: %w", err)
	}

	c.dispatchEvent(Event{Kind: EventContextSourceChanged, Payload: ContextSourcePayload{
		Name:    name,
		Type:    srcType,
		Enabled: nowEnabled,
		Action:  "toggled",
	}, At: time.Now()})
	return nil
}

// ─── Tool toggle ─────────────────────────────────────────────────────────────

// ToggleTool toggles a tool's enabled state in the active config.  The
// underlying SDK does not yet accept runtime tool toggles on a live agent
// — this method records the new state in ToolsConfig.Disabled (additively
// on disable, removed on enable) and emits EventToolChanged so listeners
// (UIs, IPC clients) can refresh their view.
func (c *Client) ToggleTool(ctx context.Context, name string, enabled bool) error {
	if name == "" {
		return errors.New("ToggleTool: name is required")
	}
	if c.cfgMgr != nil {
		if err := c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
			if enabled {
				cb.Tools.Disabled = slices.DeleteFunc(cb.Tools.Disabled, func(s string) bool { return s == name })
			} else if !slices.Contains(cb.Tools.Disabled, name) {
				cb.Tools.Disabled = append(cb.Tools.Disabled, name)
			}
		}); err != nil {
			return fmt.Errorf("ToggleTool: %w", err)
		}
		if err := c.cfgMgr.Save(ctx); err != nil {
			return fmt.Errorf("ToggleTool save: %w", err)
		}
	}

	c.dispatchEvent(Event{Kind: EventToolChanged, Payload: ToolPayload{
		Name:    name,
		Enabled: enabled,
	}, At: time.Now()})
	return nil
}

// ─── Config ──────────────────────────────────────────────────────────────────

// SetConfig updates a single field on the active SystemConfig.  Supported
// keys (matching the legacy InputSetConfig metadata vocabulary) include
// "theme", "compactMode", "showThinking", "showTokenCount",
// "showToolOutput", "syntaxHighlighting", "logLevel", "editor",
// "defaultMode", "defaultAgent", and "memoryBackend".  Unknown keys are
// stored under SystemConfig.Custom and an EventConfigChanged is still
// dispatched.
func (c *Client) SetConfig(ctx context.Context, key string, value any) error {
	if key == "" {
		return errors.New("SetConfig: key is required")
	}
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}

	var oldVal any
	if err := c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
		oldVal = applyConfigValue(&cb.System, key, value)
	}); err != nil {
		return fmt.Errorf("SetConfig: %w", err)
	}
	if err := c.cfgMgr.Save(ctx); err != nil {
		return fmt.Errorf("SetConfig save: %w", err)
	}

	c.dispatchEvent(Event{Kind: EventConfigChanged, Payload: ConfigPayload{
		Key:      key,
		OldValue: oldVal,
		NewValue: value,
	}, At: time.Now()})
	return nil
}

// LoadConfig reloads the active config bundle from disk via the
// underlying ConfigManager and emits EventConfigLoaded.
func (c *Client) LoadConfig(ctx context.Context) error {
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}
	if err := c.cfgMgr.Refresh(ctx); err != nil {
		return fmt.Errorf("LoadConfig: %w", err)
	}
	c.dispatchEvent(Event{Kind: EventConfigLoaded, At: time.Now()})
	return nil
}

// SaveConfig persists the active config bundle via the underlying
// ConfigManager and emits EventConfigSaved.
func (c *Client) SaveConfig(ctx context.Context) error {
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}
	if err := c.cfgMgr.Save(ctx); err != nil {
		return fmt.Errorf("SaveConfig: %w", err)
	}
	c.dispatchEvent(Event{Kind: EventConfigSaved, At: time.Now()})
	return nil
}

// ─── Display ─────────────────────────────────────────────────────────────────

// SetTheme sets the theme on the active SystemConfig and persists.
func (c *Client) SetTheme(ctx context.Context, theme string) error {
	if theme == "" {
		return errors.New("SetTheme: theme is required")
	}
	if c.cfgMgr != nil {
		if err := c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
			cb.System.Theme = theme
		}); err != nil {
			return fmt.Errorf("SetTheme: %w", err)
		}
		if err := c.cfgMgr.Save(ctx); err != nil {
			return fmt.Errorf("SetTheme save: %w", err)
		}
	}
	c.dispatchEvent(Event{Kind: EventThemeChanged, Payload: ThemePayload{Theme: theme}, At: time.Now()})
	return nil
}

// ToggleCompactMode flips System.CompactMode and persists.  Emits
// EventConfigChanged with key "compactMode".
func (c *Client) ToggleCompactMode(ctx context.Context) error {
	if c.cfgMgr == nil {
		return ErrNoConfigManager
	}
	var newVal bool
	if err := c.cfgMgr.UpdateActive(func(cb *configbundle.ConfigBundle) {
		cur := false
		if cb.System.CompactMode != nil {
			cur = *cb.System.CompactMode
		}
		flipped := !cur
		cb.System.CompactMode = &flipped
		newVal = flipped
	}); err != nil {
		return fmt.Errorf("ToggleCompactMode: %w", err)
	}
	if err := c.cfgMgr.Save(ctx); err != nil {
		return fmt.Errorf("ToggleCompactMode save: %w", err)
	}
	c.dispatchEvent(Event{Kind: EventConfigChanged, Payload: ConfigPayload{
		Key:      "compactMode",
		NewValue: newVal,
	}, At: time.Now()})
	return nil
}

// ─── History ─────────────────────────────────────────────────────────────────

// ClearHistory deletes every conversation accessible via the underlying
// client.  Mirrors the legacy engine's handleClearHistory.  Emits
// EventHistoryCleared on success.
func (c *Client) ClearHistory(ctx context.Context) error {
	convs, err := c.ListConversationsMeta(ctx, "")
	if err != nil {
		return fmt.Errorf("ClearHistory: list: %w", err)
	}
	for _, cv := range convs {
		if cv == nil {
			continue
		}
		_ = c.DeleteConversation(ctx, cv.ID)
	}
	c.stateMu.Lock()
	c.sessState.Conversations = nil
	c.sessState.ActiveConvID = ""
	c.sessState.UpdatedAt = time.Now()
	c.stateMu.Unlock()
	c.dispatchEvent(Event{Kind: EventHistoryCleared, At: time.Now()})
	return nil
}

// SearchHistory returns conversation summaries matching query against
// title or preview (case-insensitive substring match).  An empty query
// returns every conversation.  Also emits EventHistoryResult.
func (c *Client) SearchHistory(ctx context.Context, query string) ([]ConversationSummary, error) {
	convs, err := c.ListConversationsMeta(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("SearchHistory: %w", err)
	}
	q := strings.ToLower(query)
	results := make([]ConversationSummary, 0, len(convs))
	for _, cv := range convs {
		if cv == nil {
			continue
		}
		summary := summaryFromConv(cv)
		if q == "" ||
			strings.Contains(strings.ToLower(summary.Title), q) ||
			strings.Contains(strings.ToLower(summary.Preview), q) {
			results = append(results, summary)
		}
	}
	c.dispatchEvent(Event{Kind: EventHistoryResult, Payload: HistoryResultPayload{
		Query:   query,
		Results: results,
		Total:   len(results),
	}, At: time.Now()})
	return results, nil
}

// ─── Internal helpers ────────────────────────────────────────────────────────

func agentDefFromSpec(spec AgentSpec) configbundle.AgentDefinition {
	id := spec.Name
	def := configbundle.AgentDefinition{
		ID:           id,
		Name:         spec.Name,
		Description:  spec.Description,
		SystemPrompt: spec.SystemPrompt,
	}
	if spec.Model != "" {
		def.ModelAlias = spec.Model
	}
	// Carry temperature/maxTokens/provider via Metadata so the IPC server's
	// event-payload listing can echo them back. configbundle's
	// AgentDefinition does not hold those fields directly.
	if spec.Temperature != 0 || spec.MaxTokens != 0 || spec.Provider != "" || spec.Profile != "" {
		def.Metadata = map[string]string{}
		if spec.Provider != "" {
			def.Metadata["provider"] = spec.Provider
		}
		if spec.Profile != "" {
			def.Metadata["profile"] = spec.Profile
		}
		if spec.Temperature != 0 {
			def.Metadata["temperature"] = strconv.FormatFloat(spec.Temperature, 'g', -1, 64)
		}
		if spec.MaxTokens != 0 {
			def.Metadata["maxTokens"] = strconv.Itoa(spec.MaxTokens)
		}
	}
	return def
}

func appendOrReplaceAgent(defs []configbundle.AgentDefinition, def configbundle.AgentDefinition) []configbundle.AgentDefinition {
	for i, d := range defs {
		if d.Name == def.Name || d.ID == def.ID {
			defs[i] = def
			return defs
		}
	}
	return append(defs, def)
}

func agentPayloadFromSpec(spec AgentSpec, action string) AgentPayload {
	return AgentPayload{
		Name:        spec.Name,
		Description: spec.Description,
		Model:       spec.Model,
		Provider:    spec.Provider,
		Profile:     spec.Profile,
		IsDefault:   spec.IsDefault,
		Action:      action,
	}
}

func profileDefFromSpec(spec ProfileSpec) configbundle.ProfileDefinition {
	id := spec.Name
	def := configbundle.ProfileDefinition{
		ID:           id,
		Name:         spec.Name,
		Description:  spec.Description,
		SystemPrompt: spec.SystemPrompt,
		Provider:     spec.Provider,
		Model:        spec.Model,
	}
	if spec.Temperature != 0 {
		t := spec.Temperature
		def.Temperature = &t
	}
	if spec.MaxTokens != 0 {
		def.MaxTokens = spec.MaxTokens
	}
	return def
}

func appendOrReplaceProfile(defs []configbundle.ProfileDefinition, def configbundle.ProfileDefinition) []configbundle.ProfileDefinition {
	for i, d := range defs {
		if d.Name == def.Name || d.ID == def.ID {
			defs[i] = def
			return defs
		}
	}
	return append(defs, def)
}

func hookDefFromSpec(spec HookSpec) configbundle.HookDefinition {
	def := configbundle.HookDefinition{
		ID:      spec.Name,
		Name:    spec.Name,
		Type:    "command",
		Event:   spec.Event,
		Command: spec.Command,
		Timeout: spec.Timeout,
	}
	// Always set Enabled so toggling works deterministically.
	e := spec.Enabled
	def.Enabled = &e
	if len(spec.Environment) > 0 {
		env := make(map[string]string, len(spec.Environment))
		for _, kv := range spec.Environment {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) == 2 {
				env[parts[0]] = parts[1]
			}
		}
		def.Environment = env
	}
	return def
}

func hookPayloadFromSpec(spec HookSpec, action string) HookPayload {
	return HookPayload{
		Name:        spec.Name,
		Description: spec.Description,
		Event:       spec.Event,
		ToolMatch:   spec.ToolMatch,
		Phase:       spec.Phase,
		Enabled:     spec.Enabled,
		CanBlock:    spec.CanBlock,
		Action:      action,
	}
}

func contextSourceDefFromSpec(spec ContextSourceSpec) configbundle.ContextSourceDefinition {
	id := spec.Name
	def := configbundle.ContextSourceDefinition{
		ID:   id,
		Name: spec.Name,
		Type: spec.Type,
		Path: spec.Path,
		URL:  spec.URL,
	}
	enabled := spec.Enabled
	def.Enabled = &enabled

	if spec.Command != "" || spec.MCPServer != "" || spec.RefreshSecs != 0 {
		def.Config = map[string]any{}
		if spec.Command != "" {
			def.Config["command"] = spec.Command
		}
		if spec.MCPServer != "" {
			def.Config["mcpServer"] = spec.MCPServer
		}
		if spec.RefreshSecs != 0 {
			def.Config["refreshSecs"] = spec.RefreshSecs
		}
	}
	return def
}

// applyConfigValue mutates a SystemConfig field by string key and returns
// the previous value (for emission as ConfigPayload.OldValue).
func applyConfigValue(sc *configbundle.SystemConfig, key string, value any) any {
	switch key {
	case "theme":
		old := sc.Theme
		if v, ok := value.(string); ok {
			sc.Theme = v
		}
		return old
	case "defaultMode":
		old := sc.DefaultMode
		if v, ok := value.(string); ok {
			sc.DefaultMode = v
		}
		return old
	case "defaultAgent":
		old := sc.DefaultAgent
		if v, ok := value.(string); ok {
			sc.DefaultAgent = v
		}
		return old
	case "compactMode":
		old := boolPtrValue(sc.CompactMode)
		if v, ok := value.(bool); ok {
			sc.CompactMode = &v
		}
		return old
	case "showThinking":
		old := boolPtrValue(sc.ShowThinking)
		if v, ok := value.(bool); ok {
			sc.ShowThinking = &v
		}
		return old
	case "showTokenCount":
		old := boolPtrValue(sc.ShowTokenCount)
		if v, ok := value.(bool); ok {
			sc.ShowTokenCount = &v
		}
		return old
	case "showToolOutput":
		old := boolPtrValue(sc.ShowToolOutput)
		if v, ok := value.(bool); ok {
			sc.ShowToolOutput = &v
		}
		return old
	case "syntaxHighlighting":
		old := boolPtrValue(sc.SyntaxHighlighting)
		if v, ok := value.(bool); ok {
			sc.SyntaxHighlighting = &v
		}
		return old
	case "logLevel":
		old := sc.LogLevel
		if v, ok := value.(string); ok {
			sc.LogLevel = v
		}
		return old
	case "editor":
		old := sc.Editor
		if v, ok := value.(string); ok {
			sc.Editor = v
		}
		return old
	default:
		// Unknown keys land in Custom for forward compatibility.
		if sc.Custom == nil {
			sc.Custom = make(map[string]any)
		}
		old := sc.Custom[key]
		sc.Custom[key] = value
		return old
	}
}

func boolPtrValue(p *bool) any {
	if p == nil {
		return nil
	}
	return *p
}
