// Package profiles provides the canonical agent profile system for the Swarm SDK.
//
// An agent profile is a named set of per-role model pools (fallback chains).
// Every agent role (Main, Steering, Background, SubAgent, Inference,
// LongContext, Compaction) is configured with an ordered list of models.
// If the primary model fails (rate-limit, billing, auth error) the runtime
// automatically tries the next model in the chain — no downtime, no manual
// intervention.
//
// # Architecture
//
//	ProfilesConfig          — root object, persisted to agent_profiles.json
//	  └─ []AgentProfile     — ordered list of named profiles
//	       └─ map[ModelAlias]RoleConfig  — role-name → chain config
//	              └─ *fallback.Chain     — ordered pool of provider/model pairs
//
// # Persistence
//
//	~/.swarm/config/agent_profiles.json   (TUI default)
//	<configDir>/agent_profiles.json  (headless / SDK apps)
//
// # Usage
//
//	mgr := profiles.NewManager("")        // defaults to ~/.swarm/config/
//	chain, err := mgr.ResolveChain(profiles.AliasMain)
//	// pass chain to fallback.Execute(ctx, chain, req)
package profiles

import (
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
)

// ============================================================================
// ALIASES — ROLE IDENTIFIERS
// ============================================================================

// ModelAlias is a symbolic role name stored as a JSON key.
type ModelAlias string

const (
	// AliasMain — primary conversation agent.
	AliasMain ModelAlias = "main"

	// AliasSteering — supervisor / quality-control agent.
	AliasSteering ModelAlias = "steering"

	// AliasBackground — long-running async task agent.
	AliasBackground ModelAlias = "background"

	// AliasSubAgent — fast, cheap subtask agent (Task tool).
	AliasSubAgent ModelAlias = "sub_agent"

	// AliasInference — fast low-latency inference.
	AliasInference ModelAlias = "inference"

	// AliasLongContext — large-context specialist (100k+ tokens).
	AliasLongContext ModelAlias = "long_context"

	// AliasCompaction — context summarisation.
	AliasCompaction ModelAlias = "compaction"

	// AliasVision — vision-capable model for processing images.
	// Used when the primary agent model lacks vision support but needs
	// to process image files (Read tool), screenshots, or other visual content.
	// The runtime automatically routes vision-requiring tool calls through
	// this model when the main model reports no vision capability.
	AliasVision ModelAlias = "vision"

	// AliasThinking — deprecated alias, kept for backward-compat JSON migration.
	// Old files may have "thinking" keys; MigratePointersToRoles maps them to AliasInference.
	AliasThinking ModelAlias = "thinking"
)

// AllAliases returns the canonical set of aliases for new profiles.
// AliasThinking is excluded — it is legacy-only.
func AllAliases() []ModelAlias {
	return []ModelAlias{
		AliasMain,
		AliasSteering,
		AliasBackground,
		AliasSubAgent,
		AliasInference,
		AliasLongContext,
		AliasCompaction,
		AliasVision,
	}
}

// AliasDisplayName returns a human-readable label.
func AliasDisplayName(a ModelAlias) string {
	switch a {
	case AliasMain:
		return "Main"
	case AliasSteering:
		return "Steering"
	case AliasBackground:
		return "Background"
	case AliasSubAgent:
		return "Sub Agent"
	case AliasInference:
		return "Fast Inference"
	case AliasLongContext:
		return "Long Context"
	case AliasCompaction:
		return "Compaction"
	case AliasVision:
		return "Vision"
	case AliasThinking:
		return "Thinking (legacy)"
	default:
		return string(a)
	}
}

// AliasDescription returns a short description of the role.
func AliasDescription(a ModelAlias) string {
	switch a {
	case AliasMain:
		return "Primary conversation agent"
	case AliasSteering:
		return "Supervisor — quality control and orchestration"
	case AliasBackground:
		return "Long-running async tasks"
	case AliasSubAgent:
		return "Fast specialised subtasks"
	case AliasInference:
		return "Quick reasoning, low latency"
	case AliasLongContext:
		return "Large-context specialist (100k+ tokens)"
	case AliasCompaction:
		return "Context summarisation"
	case AliasVision:
		return "Vision-capable model for image processing"
	default:
		return ""
	}
}

func (a ModelAlias) String() string { return string(a) }

// IsValid returns true for all current + legacy aliases.
func (a ModelAlias) IsValid() bool {
	switch a {
	case AliasMain, AliasSteering, AliasBackground, AliasSubAgent,
		AliasInference, AliasLongContext, AliasCompaction, AliasVision, AliasThinking:
		return true
	}
	return false
}

// ============================================================================
// MODEL POINTER — LEGACY SINGLE-MODEL CONFIG
// ============================================================================

// ModelPointer is the old per-role config (single model, no pool).
//
// Present in JSON as "pointers": { ... } on old profiles.
// LoadProfiles calls MigratePointersToRoles to convert automatically.
// DO NOT REMOVE — needed for backward-compat JSON unmarshalling.
type ModelPointer struct {
	Provider         string             `json:"provider"`
	Model            string             `json:"model"`
	SystemPrompt     string             `json:"system_prompt,omitempty"`
	Capabilities     *AgentCapabilities `json:"capabilities,omitempty"`
	ReasoningLevel   string             `json:"reasoning_level,omitempty"`
	DisableReasoning bool               `json:"disable_reasoning,omitempty"`
}

// Validate checks required fields.
func (p *ModelPointer) Validate() error {
	if p.Provider == "" {
		return ErrEmptyProvider
	}
	if p.Model == "" {
		return ErrEmptyModel
	}
	if p.Capabilities != nil {
		return p.Capabilities.Validate()
	}
	return nil
}

// ============================================================================
// CAPABILITIES
// ============================================================================

// AgentCapabilities defines runtime constraints for an agent role.
type AgentCapabilities struct {
	MaxTokens   int     `json:"max_tokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	MaxTurns    int     `json:"max_turns,omitempty"`
	Timeout     int     `json:"timeout,omitempty"` // seconds
}

// Validate checks for sensible values.
func (c *AgentCapabilities) Validate() error {
	if c.MaxTokens < 0 {
		return ErrInvalidMaxTokens
	}
	if c.MaxTokens > 2000000 {
		return ErrMaxTokensTooLarge
	}
	if c.Temperature < 0.0 || c.Temperature > 2.0 {
		return ErrInvalidTemperature
	}
	if c.MaxTurns < 0 {
		return ErrInvalidMaxTurns
	}
	if c.Timeout < 0 {
		return ErrInvalidTimeout
	}
	return nil
}

// ============================================================================
// ROLE CONFIG — POOL-BASED MODEL CONFIG
// ============================================================================

// RoleConfig is the per-role configuration in the new profile format.
//
// Each role gets an ordered pool (fallback.Chain) instead of a single model.
// This directly fixes "all providers exhausted" errors where sub-agents hit
// 429/402 with zero fallback.
//
//	{
//	  "chain":        { "primary": {...}, "fallbacks": [...] },
//	  "enabled":      true,
//	  "system_prompt": "...",
//	  "capabilities": { "max_tokens": 16384, "temperature": 0.7 }
//	}
type RoleConfig struct {
	Chain        *fallback.Chain    `json:"chain"`
	Enabled      bool               `json:"enabled"`
	SystemPrompt string             `json:"system_prompt,omitempty"`
	Capabilities *AgentCapabilities `json:"capabilities,omitempty"`
}

// NewRoleConfig creates a single-model RoleConfig with no fallbacks.
func NewRoleConfig(provider, model string) RoleConfig {
	return RoleConfig{
		Chain:   fallback.NewChain(provider, model),
		Enabled: true,
	}
}

// NewRoleConfigChain creates a RoleConfig from alternating provider/model pairs.
//
//	NewRoleConfigChain("anthropic", "claude-opus-4-20250514", "google", "gemini-2.5-pro")
func NewRoleConfigChain(pairs ...string) RoleConfig {
	if len(pairs) < 2 {
		return RoleConfig{Enabled: true}
	}
	rc := RoleConfig{
		Chain:   fallback.NewChain(pairs[0], pairs[1]),
		Enabled: true,
	}
	for i := 2; i+1 < len(pairs); i += 2 {
		rc.Chain.AddFallback(pairs[i], pairs[i+1])
	}
	return rc
}

// PrimaryRef returns the primary model ref, or empty if chain is nil/empty.
func (r *RoleConfig) PrimaryRef() fallback.ModelRef {
	if r.Chain == nil || r.Chain.IsEmpty() {
		return fallback.ModelRef{}
	}
	return r.Chain.Primary
}

// AllRefs returns primary + all fallback refs in order.
func (r *RoleConfig) AllRefs() []fallback.ModelRef {
	if r.Chain == nil {
		return nil
	}
	return r.Chain.All()
}

// PoolSummary renders the chain as "modelA → modelB → modelC".
func (r *RoleConfig) PoolSummary() string {
	if r.Chain == nil || r.Chain.IsEmpty() {
		return "(none)"
	}
	var out strings.Builder
	for i, ref := range r.Chain.All() {
		if i > 0 {
			out.WriteString(" → ")
		}
		out.WriteString(ref.Model)
	}
	return out.String()
}

// ToModelPointer converts to legacy ModelPointer (primary model only).
func (r *RoleConfig) ToModelPointer() ModelPointer {
	ref := r.PrimaryRef()
	return ModelPointer{
		Provider:     ref.Provider,
		Model:        ref.Model,
		SystemPrompt: r.SystemPrompt,
		Capabilities: r.Capabilities,
	}
}

// ============================================================================
// RETRY POLICY
// ============================================================================

// RetryPolicy controls when the pool rotates to the next model.
// Per-profile override on top of global Reliability settings.
type RetryPolicy struct {
	RotateOnRateLimit bool `json:"rotate_on_rate_limit"`
	RotateOnPayment   bool `json:"rotate_on_payment"`
	RotateOnAuthError bool `json:"rotate_on_auth_error"`
	RotateOnAnyError  bool `json:"rotate_on_any_error"`
	CooldownSeconds   int  `json:"cooldown_seconds"`
}

// DefaultRetryPolicy returns sensible production defaults.
func DefaultRetryPolicy() *RetryPolicy {
	return &RetryPolicy{
		RotateOnRateLimit: true,
		RotateOnPayment:   true,
		RotateOnAuthError: false,
		RotateOnAnyError:  false,
		CooldownSeconds:   30,
	}
}

func cloneRetryPolicy(p *RetryPolicy) *RetryPolicy {
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

// ============================================================================
// AGENT PROFILE
// ============================================================================

// AgentProfile is a named set of per-role model pools.
//
// # JSON (new format, written by this package)
//
//	{
//	  "id":   "balanced",
//	  "name": "Balanced",
//	  "roles": {
//	    "main":       { "chain": { "primary": {...}, "fallbacks": [...] }, "enabled": true },
//	    "sub_agent":  { "chain": { "primary": {...} }, "enabled": true }
//	  },
//	  "retry_policy": { "rotate_on_rate_limit": true, "cooldown_seconds": 30 },
//	  "created_at": "2026-02-17T00:00:00Z",
//	  "updated_at": "2026-02-17T00:00:00Z"
//	}
//
// # JSON (old format, auto-migrated on load)
//
//	{ "pointers": { "main": { "provider": "anthropic", "model": "claude-..." } } }
type AgentProfile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Color       string `json:"color,omitempty"`
	IsDefault   bool   `json:"is_default"`

	// Roles — new primary field. maps role alias → pool config.
	Roles map[ModelAlias]RoleConfig `json:"roles,omitempty"`

	// Pointers — legacy field (read-only). MigratePointersToRoles converts to Roles.
	Pointers map[ModelAlias]ModelPointer `json:"pointers,omitempty"`

	RetryPolicy *RetryPolicy `json:"retry_policy,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MigratePointersToRoles converts legacy Pointers → Roles. Idempotent.
func (p *AgentProfile) MigratePointersToRoles() {
	if len(p.Roles) > 0 {
		return
	}
	if len(p.Pointers) == 0 {
		return
	}
	p.Roles = make(map[ModelAlias]RoleConfig, len(p.Pointers))
	for alias, ptr := range p.Pointers {
		targetAlias := alias
		if alias == AliasThinking {
			targetAlias = AliasInference
		}
		rc := NewRoleConfig(ptr.Provider, ptr.Model)
		rc.SystemPrompt = ptr.SystemPrompt
		rc.Capabilities = ptr.Capabilities
		p.Roles[targetAlias] = rc
	}
	p.Pointers = nil
}

// GetRole returns the RoleConfig for an alias.
func (p *AgentProfile) GetRole(alias ModelAlias) (RoleConfig, bool) {
	if rc, ok := p.Roles[alias]; ok {
		return rc, true
	}
	// Legacy fallback
	if ptr, ok := p.Pointers[alias]; ok {
		rc := NewRoleConfig(ptr.Provider, ptr.Model)
		rc.SystemPrompt = ptr.SystemPrompt
		rc.Capabilities = ptr.Capabilities
		return rc, true
	}
	return RoleConfig{}, false
}

// SetRole sets or replaces the RoleConfig for an alias.
// Caller must call Manager.Save() to persist.
func (p *AgentProfile) SetRole(alias ModelAlias, rc RoleConfig) {
	if p.Roles == nil {
		p.Roles = make(map[ModelAlias]RoleConfig)
	}
	p.Roles[alias] = rc
}

// HasRole returns true if the alias has a configured role.
func (p *AgentProfile) HasRole(alias ModelAlias) bool {
	_, ok := p.GetRole(alias)
	return ok
}

// HasAlias returns true if the alias has a configured role (legacy name for HasRole).
func (p *AgentProfile) HasAlias(alias ModelAlias) bool {
	return p.HasRole(alias)
}

// GetPointer returns the legacy ModelPointer for an alias.
// It extracts the primary model from the role's chain.
func (p *AgentProfile) GetPointer(alias ModelAlias) (ModelPointer, error) {
	rc, ok := p.GetRole(alias)
	if !ok {
		return ModelPointer{}, fmt.Errorf("%w: %s", ErrAliasNotConfigured, alias)
	}
	return rc.ToModelPointer(), nil
}

// Validate checks the profile is structurally valid for storage.
func (p *AgentProfile) Validate() error {
	if p.ID == "" {
		return ErrEmptyProfileID
	}
	if p.Name == "" {
		return ErrEmptyProfileName
	}
	for alias, rc := range p.Roles {
		if !alias.IsValid() {
			return ErrInvalidAlias
		}
		if rc.Chain == nil || rc.Chain.IsEmpty() {
			return &ValidationError{Field: string(alias), Message: "role chain is empty"}
		}
		if rc.Chain.Primary.Provider == "" {
			return &ValidationError{Field: string(alias), Message: "role chain primary has no provider"}
		}
		if rc.Chain.Primary.Model == "" {
			return &ValidationError{Field: string(alias), Message: "role chain primary has no model"}
		}
		if rc.Capabilities != nil {
			if err := rc.Capabilities.Validate(); err != nil {
				return err
			}
		}
	}
	for alias, ptr := range p.Pointers {
		if !alias.IsValid() {
			return ErrInvalidAlias
		}
		if err := ptr.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// ============================================================================
// PROFILES CONFIG — STORAGE ROOT
// ============================================================================

// ProfilesConfig is the root object persisted to agent_profiles.json.
//
//	{
//	  "default_profile": "balanced",
//	  "profiles": [ { ... }, { ... } ]
//	}
type ProfilesConfig struct {
	DefaultProfile string         `json:"default_profile"`
	Profiles       []AgentProfile `json:"profiles"`
}

// Validate checks internal consistency.
func (c *ProfilesConfig) Validate() error {
	if len(c.Profiles) == 0 {
		return ErrNoProfiles
	}
	for i := range c.Profiles {
		if err := c.Profiles[i].Validate(); err != nil {
			return &ValidationError{Field: "profiles", Index: i, Message: err.Error()}
		}
	}
	if c.DefaultProfile != "" {
		found := false
		for _, p := range c.Profiles {
			if p.ID == c.DefaultProfile {
				found = true
				break
			}
		}
		if !found {
			return ErrDefaultProfileNotFound
		}
	}
	return nil
}

// GetProfile returns a pointer to the profile with the given ID.
func (c *ProfilesConfig) GetProfile(id string) (*AgentProfile, error) {
	for i := range c.Profiles {
		if c.Profiles[i].ID == id {
			return &c.Profiles[i], nil
		}
	}
	return nil, ErrProfileNotFound
}

// GetDefaultProfile returns the active profile, falling back to the first.
func (c *ProfilesConfig) GetDefaultProfile() (*AgentProfile, error) {
	if c.DefaultProfile != "" {
		if p, err := c.GetProfile(c.DefaultProfile); err == nil {
			return p, nil
		}
	}
	if len(c.Profiles) > 0 {
		return &c.Profiles[0], nil
	}
	return nil, ErrNoProfiles
}

// ============================================================================
// VALIDATION ERRORS
// ============================================================================

var (
	ErrEmptyProvider          = fmt.Errorf("provider cannot be empty")
	ErrEmptyModel             = fmt.Errorf("model cannot be empty")
	ErrInvalidMaxTokens       = fmt.Errorf("max_tokens must be >= 0")
	ErrMaxTokensTooLarge      = fmt.Errorf("max_tokens exceeds limit (2000000)")
	ErrInvalidTemperature     = fmt.Errorf("temperature must be 0.0–2.0")
	ErrInvalidMaxTurns        = fmt.Errorf("max_turns must be >= 0")
	ErrInvalidTimeout         = fmt.Errorf("timeout must be >= 0")
	ErrEmptyProfileID         = fmt.Errorf("profile ID cannot be empty")
	ErrEmptyProfileName       = fmt.Errorf("profile name cannot be empty")
	ErrInvalidAlias           = fmt.Errorf("invalid model alias")
	ErrAliasNotConfigured     = fmt.Errorf("alias not configured in profile")
	ErrProfileNotFound        = fmt.Errorf("profile not found")
	ErrDefaultProfileNotFound = fmt.Errorf("default profile not found in profiles list")
	ErrNoProfiles             = fmt.Errorf("profiles list is empty")
)

// ValidationError is a structured error for validation failures.
type ValidationError struct {
	Field   string
	Index   int
	Message string
}

func (e *ValidationError) Error() string {
	if e.Index > 0 {
		return fmt.Sprintf("validation error at %s[%d]: %s", e.Field, e.Index, e.Message)
	}
	return fmt.Sprintf("validation error at %s: %s", e.Field, e.Message)
}

// ============================================================================
// WARNINGS
// ============================================================================

// ProfileWarning is a non-fatal issue detected during profile load or
// validation. Warnings do not prevent operation; they are surfaced to UIs
// so users can correct configuration drift.
type ProfileWarning struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}
