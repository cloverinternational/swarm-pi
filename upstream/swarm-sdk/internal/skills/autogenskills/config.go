// Package autogenskills implements the Hermes-style closed learning loop for
// the Swarm SDK skill system.
//
// CONTRACT:
//   - All types in this package are zero-value-safe where possible.
//   - Config.Validate() must be called before passing Config to New().
//   - CreationMode is strongly typed (not a string) to prevent misuse.
//   - The default CreationMode is ModeNever (fully disabled).
package autogenskills

import (
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// CreationMode controls when skills are created.
// CONTRACT: ModeManual never creates skills without explicit user/API action.
// ModeAuto allows the system to create skills autonomously.
// ModeNever is the safe default.
type CreationMode string

const (
	// ModeManual only creates skills via explicit CreateSkill API calls.
	// The LLM is never nudged to create skills automatically.
	ModeManual CreationMode = "manual"

	// ModeAuto allows both programmatic and LLM-driven skill creation.
	// The system injects nudges into the system prompt when thresholds are met.
	ModeAuto CreationMode = "auto"

	// ModeNever is the default. The autogenskills system is fully disabled.
	// No hooks are registered, no nudges are generated, no skills are created.
	ModeNever CreationMode = "never"
)

// IsEnabled returns true if the mode allows any skill creation.
func (m CreationMode) IsEnabled() bool {
	return m == ModeManual || m == ModeAuto
}

// TriggerConfig defines when skill creation is triggered.
// All fields have zero-value-safe defaults.
type TriggerConfig struct {
	// ToolCallThreshold triggers creation after N tool calls.
	// CONTRACT: Must be >= 3, <= 90 (matches Hermes's 90-turn budget).
	// Zero means "disabled".
	ToolCallThreshold uint `json:"tool_call_threshold"`

	// ErrorResolutionThreshold triggers creation after N error-resolution turns.
	// Zero means "disabled".
	ErrorResolutionThreshold uint `json:"error_resolution_threshold"`

	// MinInstructionsLength prevents creation of trivial skills.
	// CONTRACT: Must be >= 200 chars to avoid low-value skills.
	// Zero means "no minimum".
	MinInstructionsLength int `json:"min_instructions_length"`

	// ToolCallBudget is the initial (onboarding) budget.
	// After this many non-exempt tool calls without creating a skill, ALL
	// further non-exempt tools are HARD-BLOCKED.
	// CONTRACT: Must be >= 3, <= 90. Zero means "no enforcement".
	// Default: 15. The review can conclude that there is nothing to save.
	ToolCallBudget uint `json:"tool_call_budget"`

	// WorkingBudget is the budget after the agent has created or used a skill.
	// When exceeded, the agent receives a soft nudge (not a hard block).
	// After MaxNudgeIgnores soft nudges are ignored, it escalates to hard block.
	// CONTRACT: Must be >= ToolCallBudget, <= 200. Zero means "use ToolCallBudget".
	// Default: 90.
	WorkingBudget uint `json:"working_budget"`

	// MaxNudgeIgnores is the number of soft nudges the agent can ignore before
	// the working budget escalates to a hard block.
	// CONTRACT: Must be >= 1. Default: 3.
	MaxNudgeIgnores int `json:"max_nudge_ignores"`

	// NudgeInterval controls how often to nudge the LLM (in turns).
	// CONTRACT: Must be >= 1. Default is 15.
	NudgeInterval uint `json:"nudge_interval"`
}

// Validate checks that the trigger configuration is valid.
// CONTRACT: Returns a descriptive error for each violation.
// Callers must handle the error before using the config.
func (c TriggerConfig) Validate() error {
	if c.ToolCallThreshold > 90 {
		return fmt.Errorf("autogenskills: tool_call_threshold %d exceeds maximum 90", c.ToolCallThreshold)
	}
	if c.ToolCallBudget > 90 {
		return fmt.Errorf("autogenskills: tool_call_budget %d exceeds maximum 90", c.ToolCallBudget)
	}
	if c.WorkingBudget > 200 {
		return fmt.Errorf("autogenskills: working_budget %d exceeds maximum 200", c.WorkingBudget)
	}
	if c.MaxNudgeIgnores < 0 {
		return fmt.Errorf("autogenskills: max_nudge_ignores must be >= 0")
	}
	if c.MinInstructionsLength > 0 && c.MinInstructionsLength < 200 {
		return fmt.Errorf("autogenskills: min_instructions_length %d must be >= 200", c.MinInstructionsLength)
	}
	return nil
}

// WithDefaults returns a copy of the trigger config with zero-value-safe defaults applied.
func (c TriggerConfig) WithDefaults() TriggerConfig {
	out := c
	if out.NudgeInterval == 0 {
		out.NudgeInterval = 15
	}
	if out.ToolCallBudget == 0 {
		out.ToolCallBudget = 15
	}
	if out.WorkingBudget == 0 {
		out.WorkingBudget = 90
	}
	if out.MaxNudgeIgnores == 0 {
		out.MaxNudgeIgnores = 3
	}
	if out.ErrorResolutionThreshold == 0 {
		out.ErrorResolutionThreshold = 1
	}
	return out
}

// CuratorConfig defines background curation behavior.
type CuratorConfig struct {
	// Interval is the idle duration before the curator runs.
	// Default: 1 hour.
	Interval string `json:"interval"`

	// StaleAfterDays marks an unused skill stale after N days. Stale is an
	// intermediate state before archive (mirrors Hermes DEFAULT_STALE_AFTER_DAYS).
	// A stale skill is still rendered but flagged; it is archived once it
	// crosses ArchiveAfterDays. Default: 30.
	StaleAfterDays int `json:"stale_after_days"`

	// ArchiveAfterDays moves unused skills to archive after N days.
	// Default: 90 (mirrors Hermes DEFAULT_ARCHIVE_AFTER_DAYS).
	ArchiveAfterDays int `json:"archive_after_days"`

	// PatchAfterDays suggests patching after N days without a version change.
	// Default: 60.
	PatchAfterDays int `json:"patch_after_days"`

	// ConsolidateTagOverlap is the Jaccard similarity threshold for consolidation.
	// Skills sharing > this fraction of tags are consolidation candidates.
	// Default: 0.5.
	ConsolidateTagOverlap float64 `json:"consolidate_tag_overlap"`

	// Consolidate enables the near-duplicate consolidation pass (rule-based
	// tag-overlap detection + LLM merge instructions). OFF by default, mirroring
	// Hermes' DEFAULT_CONSOLIDATE=False: merging skills is destructive-ish
	// (archives originals) and benefits from explicit opt-in.
	Consolidate bool `json:"consolidate"`

	// MinRunGap is the minimum duration between curator runs (daily cadence).
	// The curator skips running if less than this has elapsed since LastRunAt.
	// Default: "24h".
	MinRunGap string `json:"min_run_gap"`

	// MaxTurns is the curator sub-agent's turn ceiling. The high default matches
	// Hermes' large-library review budget; consolidation remains opt-in.
	// Default: 9999.
	MaxTurns int `json:"max_turns"`

	// Timeout is the curator sub-agent's wall-clock execution timeout.
	// Hermes has no short corpus-sweep deadline; Swarm keeps a configurable
	// one-hour safety bound because consolidation remains explicit opt-in.
	// Default: "1h".
	Timeout string `json:"timeout"`
}

// WithDefaults returns a copy with defaults applied.
func (c CuratorConfig) WithDefaults() CuratorConfig {
	out := c
	if out.Interval == "" {
		out.Interval = "1h"
	}
	if out.StaleAfterDays == 0 {
		out.StaleAfterDays = 30
	}
	if out.ArchiveAfterDays == 0 {
		out.ArchiveAfterDays = 90
	}
	if out.PatchAfterDays == 0 {
		out.PatchAfterDays = 60
	}
	if out.ConsolidateTagOverlap == 0 {
		out.ConsolidateTagOverlap = 0.5
	}
	if out.MinRunGap == "" {
		out.MinRunGap = "24h"
	}
	if out.MaxTurns == 0 {
		out.MaxTurns = 9999
	}
	if out.Timeout == "" {
		out.Timeout = "1h"
	}
	return out
}

// Validate checks curator sub-agent execution limits.
func (c CuratorConfig) Validate() error {
	if c.MaxTurns < 0 {
		return fmt.Errorf("autogenskills: curator max_turns must be >= 0")
	}
	if c.Timeout != "" {
		timeout, err := time.ParseDuration(c.Timeout)
		if err != nil || timeout <= 0 {
			return fmt.Errorf("autogenskills: curator timeout %q must be a positive duration", c.Timeout)
		}
	}
	return nil
}

// Config is the top-level configuration for the autogenskills lifecycle.
//
// CONSTRUCTOR: Use DefaultConfig() for environment-aware defaults, then
// override specific fields. Always call Validate() before passing to New().
type Config struct {
	// Mode controls the overall behavior. Default: ModeNever.
	Mode CreationMode

	// Trigger defines when skill creation is triggered.
	Trigger TriggerConfig

	// Curator defines background curation behavior.
	Curator CuratorConfig

	// AutogenDir is the directory where autogenerated skills are written.
	// Default: ~/.swarm/skills/autogen/
	AutogenDir string
}

// DefaultConfig returns a safe default configuration.
// CONTRACT: The returned config has Mode=ModeNever (disabled by default).
// Downstream consumers must explicitly change Mode to enable the feature.
func DefaultConfig() Config {
	return Config{
		Mode:       ModeNever,
		Trigger:    TriggerConfig{NudgeInterval: 15, ToolCallBudget: 15, WorkingBudget: 90, MaxNudgeIgnores: 3, ErrorResolutionThreshold: 1},
		Curator:    CuratorConfig{Interval: "1h", StaleAfterDays: 30, ArchiveAfterDays: 90, PatchAfterDays: 60, ConsolidateTagOverlap: 0.5, MaxTurns: 9999, Timeout: "1h"},
		AutogenDir: paths.AutogenSkillsDir(),
	}
}

// Validate checks that the configuration is valid.
// CONTRACT: Returns nil for valid configs, descriptive errors for invalid ones.
// Must be called before passing Config to New().
func (c Config) Validate() error {
	if !c.Mode.IsEnabled() {
		// ModeNever and ModeManual are always valid
		return nil
	}

	if c.Mode == ModeAuto {
		if err := c.Trigger.Validate(); err != nil {
			return err
		}
	}
	if err := c.Curator.Validate(); err != nil {
		return err
	}

	if c.AutogenDir == "" {
		return fmt.Errorf("autogenskills: autogen_dir must be set when mode is %q", c.Mode)
	}

	return nil
}

// IsEnabled returns true if the configuration enables any skill creation.
func (c Config) IsEnabled() bool {
	return c.Mode.IsEnabled()
}
