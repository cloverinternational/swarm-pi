// Package autogenskills implements the Hermes-style closed learning loop for
// the Swarm SDK skill system.
package autogenskills

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// Service is the top-level orchestrator for the autogenskills lifecycle.
//
// CONTRACT:
//   - Construct via NewService; zero value is NOT usable.
//   - All methods are thread-safe.
//   - When ModeNever, all operations are no-ops (no errors, no side effects).
//   - When ModeManual, nudges are never generated but CreateSkill works.
//   - When ModeAuto, full nudge generation and skill creation is active.
//   - LastNudgeTurn is tracked per-service (not persisted between sessions).
type Service struct {
	cfg        Config
	factory    *SkillFactory
	metrics    *Metrics
	builder    *NudgeBuilder
	budgetHook *BudgetEnforcementHook
	curator    *Curator

	mu            sync.Mutex
	lastNudgeTurn uint
}

// NewService creates a Service from a validated Config.
//
// CONTRACT:
//   - cfg must be validated before passing (call cfg.Validate()).
//   - registry is required (used by factory for skill registration).
//   - metrics may be nil (metrics will be disabled).
func NewService(cfg Config, registry *skills.Registry, metrics *Metrics) (*Service, error) {
	if !cfg.IsEnabled() {
		// Return a no-op service for disabled mode
		return &Service{cfg: cfg}, nil
	}

	factory, err := NewSkillFactory(cfg, registry, metrics)
	if err != nil {
		return nil, err
	}

	return &Service{
		cfg:     cfg,
		factory: factory,
		metrics: metrics,
		builder: NewNudgeBuilder(),
	}, nil
}

// RunTurn increments the turn counter and checks if a nudge should be generated.
// Call this after each completed agent turn.
//
// CONTRACT:
//   - Thread-safe; may be called from the agent execution loop.
//   - Returns a zero NudgeFragment when no nudge is warranted.
func (s *Service) RunTurn() NudgeFragment {
	if !s.cfg.IsEnabled() {
		return NudgeFragment("")
	}

	if s.metrics != nil {
		s.metrics.IncrementTurn()
	}

	return s.checkNudge()
}

// HandleToolCall increments the tool call counter.
// Call this after each successful tool execution.
func (s *Service) HandleToolCall() {
	if !s.cfg.IsEnabled() || s.metrics == nil {
		return
	}
	s.metrics.IncrementToolCall()
}

// HandleError increments the error counter.
// Call this after each tool or execution error.
func (s *Service) HandleError() {
	if !s.cfg.IsEnabled() || s.metrics == nil {
		return
	}
	s.metrics.IncrementError()
}

// HandleErrorResolved increments the error-resolved counter.
// Call this when an error was successfully fixed.
func (s *Service) HandleErrorResolved() {
	if !s.cfg.IsEnabled() || s.metrics == nil {
		return
	}
	s.metrics.IncrementErrorResolved()
}

// GetNudgeFragment returns the current nudge fragment for prompt injection.
// This is useful for callers that want to check the nudge without incrementing turns.
//
// CONTRACT:
//   - Does NOT increment any counters.
//   - Returns zero fragment if mode is not ModeAuto.
func (s *Service) GetNudgeFragment(existingSkills []string) NudgeFragment {
	if s.cfg.Mode != ModeAuto {
		return NudgeFragment("")
	}
	return s.checkNudgeWithSkills(existingSkills)
}

// CreateSkill creates a skill programmatically.
// Available in both ModeManual and ModeAuto.
//
// CONTRACT:
//   - Delegates to SkillFactory.Create which validates options and writes to disk.
//   - Returns CreationResult with Error set on failure.
func (s *Service) CreateSkill(opts CreateOptions) CreationResult {
	if !s.cfg.IsEnabled() {
		return CreationResult{Error: ErrDisabled}
	}
	return s.factory.Create(opts)
}

// Snapshot returns the current metrics snapshot.
// Returns zero MetricsSnapshot when metrics are disabled.
func (s *Service) Snapshot() MetricsSnapshot {
	if s.metrics == nil {
		return MetricsSnapshot{}
	}
	return s.metrics.Snapshot()
}

// GetConfig returns the service's configuration.
func (s *Service) GetConfig() Config {
	return s.cfg
}

// checkNudge builds a nudge if thresholds are met, updating lastNudgeTurn.
func (s *Service) checkNudge() NudgeFragment {
	if s.cfg.Mode != ModeAuto || s.builder == nil {
		return NudgeFragment("")
	}

	ctx := s.buildNudgeContext()
	frag := s.builder.BuildNudge(ctx, s.cfg)
	if !frag.IsZero() {
		s.mu.Lock()
		s.lastNudgeTurn = uint(s.Snapshot().TurnCount)
		s.mu.Unlock()
		if s.metrics != nil {
			s.metrics.IncrementNudge()
		}
	}
	return frag
}

// checkNudgeWithSkills builds a nudge using caller-provided skill names.
func (s *Service) checkNudgeWithSkills(existingSkills []string) NudgeFragment {
	if s.cfg.Mode != ModeAuto || s.builder == nil {
		return NudgeFragment("")
	}

	ctx := s.buildNudgeContextWithSkills(existingSkills)
	return s.builder.BuildNudge(ctx, s.cfg)
}

// buildNudgeContext constructs the runtime nudge context from metrics.
func (s *Service) buildNudgeContext() NudgeContext {
	return s.buildNudgeContextWithSkills(nil)
}

// buildNudgeContextWithSkills constructs context with given skill names.
func (s *Service) buildNudgeContextWithSkills(existingSkills []string) NudgeContext {
	var snap MetricsSnapshot
	if s.metrics != nil {
		snap = s.metrics.Snapshot()
	}

	s.mu.Lock()
	lastNudge := s.lastNudgeTurn
	s.mu.Unlock()

	return NudgeContext{
		TurnCount:          uint(snap.TurnCount),
		ToolCallCount:      uint(snap.ToolCallCount),
		ErrorCount:         uint(snap.ErrorCount),
		ErrorResolvedCount: uint(snap.ErrorResolvedCount),
		ExistingSkillNames: existingSkills,
		LastNudgeTurn:      lastNudge,
	}
}

// GetHooks returns the hooks that should be registered with the agent's
// hook system. This includes:
//  1. LifecycleHook — observes tool events and delivers nudges (priority 20)
//  2. BudgetEnforcementHook — hard-blocks tools after budget exceeded (priority 90)
//
// CONTRACT:
//   - Returns nil, nil when service is disabled (no hooks needed).
//   - Callers must register returned hooks with their hooks.Manager.
//   - Order of hooks in the returned slice is not significant (Manager sorts by Priority).
func (s *Service) GetHooks() ([]hooks.Hook, error) {
	if !s.cfg.IsEnabled() {
		return nil, nil
	}

	lifecycle, err := NewLifecycleHook(s)
	if err != nil {
		return nil, err
	}

	budget := NewBudgetEnforcementHook(s, s.cfg)
	if budget == nil {
		// Budget enforcement disabled (nil service or budget=0)
		return []hooks.Hook{lifecycle}, nil
	}

	s.budgetHook = budget

	return []hooks.Hook{lifecycle, budget}, nil
}

// RegisterHooksWithManager registers the autogenskills hooks (LifecycleHook + BudgetEnforcementHook)
// with the given hooks.Manager under the global scope.
//
// CONTRACT:
//   - Returns nil when service is disabled (no hooks to register).
//   - Propagates any registration errors from hooks.Manager.Register.
//   - This is a convenience method; callers may call GetHooks() and register manually if they
//     need a different scope or error handling strategy.
func (s *Service) RegisterHooksWithManager(mgr *hooks.Manager) error {
	hookList, err := s.GetHooks()
	if err != nil {
		return err
	}
	if hookList == nil {
		return nil // disabled
	}
	for _, h := range hookList {
		if err := mgr.Register(h, hooks.ScopeGlobal, ""); err != nil {
			return fmt.Errorf("autogenskills: register hook %q: %w", h.Name(), err)
		}
	}
	return nil
}

// GetBudgetEnforcementHook returns the budget enforcement hook for reading state.
// Returns nil when budget enforcement is disabled (ModeNever or ToolCallBudget=0).
func (s *Service) GetBudgetEnforcementHook() *BudgetEnforcementHook {
	return s.budgetHook
}

// PatchSkill modifies an existing skill and bumps its version.
// CONTRACT: Returns PatchResult with Error set on failure.
// Delegates to SkillFactory.Patch.
func (s *Service) PatchSkill(opts PatchOptions) PatchResult {
	if !s.cfg.IsEnabled() {
		return PatchResult{Error: ErrDisabled}
	}
	return s.factory.Patch(opts)
}

// ViewSkill returns the full skill by name.
// Returns the skill and nil on success, or nil and an error if not found.
// CONTRACT: Returns ErrDisabled when service is disabled.
//
// The registry is consulted first; on a miss the skill is loaded directly
// from the autogen directory. The filesystem is the source of truth (mirrors
// Hermes' live disk walk): the curator's recommendations come from a disk
// scan, so a skill it flags must be viewable even when the registry refused
// to index it (e.g. legacy names that fail current validation).
func (s *Service) ViewSkill(name string) (*skills.Skill, error) {
	if !s.cfg.IsEnabled() {
		return nil, ErrDisabled
	}
	if !isSafeSkillDirName(name) {
		return nil, fmt.Errorf("autogenskills: invalid skill name %q", name)
	}
	if s.factory == nil || s.factory.registry == nil {
		return nil, fmt.Errorf("autogenskills: no registry available")
	}
	if skill, found := s.factory.registry.Get(name); found {
		return skill, nil
	}
	if s.cfg.AutogenDir != "" {
		skillDir := filepath.Join(s.cfg.AutogenDir, name)
		if skill, err := skills.LoadSkill(skillDir); err == nil {
			skill.Source = "autogen"
			skill.LoadedFrom = "autogen"
			return skill, nil
		}
	}
	return nil, fmt.Errorf("autogenskills: skill %q not found", name)
}

// isSafeSkillDirName enforces the public skill identifier grammar and excludes
// names used by the on-disk lifecycle implementation.
func isSafeSkillDirName(name string) bool {
	if name == "" || len(name) > 64 || isReservedSkillDirName(name) {
		return false
	}
	previousHyphen := false
	for index, character := range name {
		if character == '-' {
			if index == 0 || previousHyphen || index == len(name)-1 {
				return false
			}
			previousHyphen = true
			continue
		}
		if character < 'a' || character > 'z' {
			if character < '0' || character > '9' {
				return false
			}
		}
		previousHyphen = false
	}
	return true
}

func isReservedSkillDirName(name string) bool {
	switch name {
	case "archive", "con", "prn", "aux", "nul":
		return true
	}
	if len(name) == 4 && name[3] >= '1' && name[3] <= '9' {
		return name[:3] == "com" || name[:3] == "lpt"
	}
	return false
}

// ListSkills returns all autogen skills.
// CONTRACT: Returns empty slice (not nil) when no skills exist.
// Only returns skills with Source=="autogen".
func (s *Service) ListSkills() []*skills.Skill {
	if !s.cfg.IsEnabled() {
		return nil
	}
	if s.factory == nil || s.factory.registry == nil {
		return nil
	}
	var result []*skills.Skill
	for _, sk := range s.factory.registry.List() {
		if sk.Source == "autogen" {
			result = append(result, sk)
		}
	}
	return result
}

// ListSkillNames returns just the names of autogen skills.
// Useful for compact display and nudge building.
func (s *Service) ListSkillNames() []string {
	sks := s.ListSkills()
	if sks == nil {
		return nil
	}
	names := make([]string, 0, len(sks))
	for _, sk := range sks {
		names = append(names, sk.Metadata.Name)
	}
	return names
}

// GetSKILLSGuidance returns the skill system guidance text for system prompt injection.
// Returns empty string when the service is disabled.
// CONTRACT: Call once at session start. Does NOT change per-turn.
func (s *Service) GetSKILLSGuidance() string {
	if !s.cfg.IsEnabled() {
		return ""
	}
	var existingNames []string
	if s.factory != nil && s.factory.registry != nil {
		for _, sk := range s.factory.registry.List() {
			if sk.Source == "autogen" {
				existingNames = append(existingNames, sk.Metadata.Name)
			}
		}
	}
	return BuildSKILLSGuidance(s.cfg.Mode, existingNames)
}

// GetSkillIndex returns a compact listing of autogen skills for system prompt injection.
// Returns empty string when no autogen skills exist or service is disabled.
// CONTRACT: Call once at session start. Agent can call SkillManage(action="list") for a fresh view.
func (s *Service) GetSkillIndex() string {
	if !s.cfg.IsEnabled() {
		return ""
	}
	if s.factory == nil || s.factory.registry == nil {
		return ""
	}
	return BuildAutogenSkillIndex(s.factory.registry.List())
}

// SetCurator sets the curator instance for the service.
// The curator is optional — it's used for MarkSkillUsed and lifecycle tracking.
// CONTRACT: Call during setup, before any hooks fire. Not thread-safe for concurrent set.
func (s *Service) SetCurator(c *Curator) {
	s.curator = c
}

// GetCurator returns the curator instance, or nil if not set.
func (s *Service) GetCurator() *Curator {
	return s.curator
}

// MarkSkillUsed updates the last_used_at timestamp for a skill.
// This is called when a skill is viewed, invoked, or patched.
// Delegates to the Curator if available; otherwise a no-op.
// CONTRACT: Thread-safe. No-op when service is disabled or curator is nil.
func (s *Service) MarkSkillUsed(skillName string) {
	if !s.cfg.IsEnabled() || s.curator == nil {
		return
	}
	s.curator.MarkUsed(skillName)
}

// ErrDisabled is returned when calling CreateSkill on a disabled service.
var ErrDisabled = errors.New("autogenskills: service is disabled")
