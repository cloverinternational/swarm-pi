// Package autogenskills implements the Hermes-style closed learning loop for
// the Swarm SDK skill system.
package autogenskills

import (
	"errors"
	"log"
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/configformat"
)

// userConfigBaseName is the basename of the per-user override file that lives
// inside the autogen skills directory (config.yaml or config.json, resolved
// by configformat). Keeping it next to the skills it governs avoids the
// shared-config dual-writer problem entirely: this file has exactly one
// owner — the autogenskills subsystem.
const userConfigBaseName = "config"

// userConfigOverrides mirrors Hermes' curator.* config block
// (~/.hermes/config.yaml). Every field is a pointer so "absent" is
// distinguishable from "explicit zero" — only keys present in the file
// override the built-in/product defaults.
type userConfigOverrides struct {
	Mode    *CreationMode     `json:"mode,omitempty"`
	Trigger *triggerOverrides `json:"trigger,omitempty"`
	Curator *curatorOverrides `json:"curator,omitempty"`
}

type triggerOverrides struct {
	ToolCallThreshold        *uint `json:"tool_call_threshold,omitempty"`
	ErrorResolutionThreshold *uint `json:"error_resolution_threshold,omitempty"`
	MinInstructionsLength    *int  `json:"min_instructions_length,omitempty"`
	ToolCallBudget           *uint `json:"tool_call_budget,omitempty"`
	WorkingBudget            *uint `json:"working_budget,omitempty"`
	MaxNudgeIgnores          *int  `json:"max_nudge_ignores,omitempty"`
	NudgeInterval            *uint `json:"nudge_interval,omitempty"`
}

type curatorOverrides struct {
	Interval              *string  `json:"interval,omitempty"`
	StaleAfterDays        *int     `json:"stale_after_days,omitempty"`
	ArchiveAfterDays      *int     `json:"archive_after_days,omitempty"`
	PatchAfterDays        *int     `json:"patch_after_days,omitempty"`
	ConsolidateTagOverlap *float64 `json:"consolidate_tag_overlap,omitempty"`
	Consolidate           *bool    `json:"consolidate,omitempty"`
	MinRunGap             *string  `json:"min_run_gap,omitempty"`
	MaxTurns              *int     `json:"max_turns,omitempty"`
	Timeout               *string  `json:"timeout,omitempty"`
}

// ApplyUserConfig overlays user overrides from <cfg.AutogenDir>/config.yaml
// (or config.json) onto cfg and returns the result plus the path of the file
// that was applied ("" when no valid override file exists).
//
// CONTRACT:
//   - Missing file: cfg returned unchanged, path "".
//   - Corrupt file: cfg returned unchanged, path "", warning logged. A broken
//     override must never disable the whole skills lifecycle.
//   - Only keys present in the file override cfg; everything else survives.
func ApplyUserConfig(cfg Config) (Config, string) {
	if cfg.AutogenDir == "" {
		return cfg, ""
	}

	var ov userConfigOverrides
	path, err := configformat.Load(cfg.AutogenDir, userConfigBaseName, &ov)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("autogenskills: ignoring corrupt user config %s: %v", path, err)
		}
		return cfg, ""
	}
	if path == "" {
		return cfg, ""
	}

	if ov.Mode != nil {
		cfg.Mode = *ov.Mode
	}
	if t := ov.Trigger; t != nil {
		if t.ToolCallThreshold != nil {
			cfg.Trigger.ToolCallThreshold = *t.ToolCallThreshold
		}
		if t.ErrorResolutionThreshold != nil {
			cfg.Trigger.ErrorResolutionThreshold = *t.ErrorResolutionThreshold
		}
		if t.MinInstructionsLength != nil {
			cfg.Trigger.MinInstructionsLength = *t.MinInstructionsLength
		}
		if t.ToolCallBudget != nil {
			cfg.Trigger.ToolCallBudget = *t.ToolCallBudget
		}
		if t.WorkingBudget != nil {
			cfg.Trigger.WorkingBudget = *t.WorkingBudget
		}
		if t.MaxNudgeIgnores != nil {
			cfg.Trigger.MaxNudgeIgnores = *t.MaxNudgeIgnores
		}
		if t.NudgeInterval != nil {
			cfg.Trigger.NudgeInterval = *t.NudgeInterval
		}
	}
	if c := ov.Curator; c != nil {
		if c.Interval != nil {
			cfg.Curator.Interval = *c.Interval
		}
		if c.StaleAfterDays != nil {
			cfg.Curator.StaleAfterDays = *c.StaleAfterDays
		}
		if c.ArchiveAfterDays != nil {
			cfg.Curator.ArchiveAfterDays = *c.ArchiveAfterDays
		}
		if c.PatchAfterDays != nil {
			cfg.Curator.PatchAfterDays = *c.PatchAfterDays
		}
		if c.ConsolidateTagOverlap != nil {
			cfg.Curator.ConsolidateTagOverlap = *c.ConsolidateTagOverlap
		}
		if c.Consolidate != nil {
			cfg.Curator.Consolidate = *c.Consolidate
		}
		if c.MinRunGap != nil {
			cfg.Curator.MinRunGap = *c.MinRunGap
		}
		if c.MaxTurns != nil {
			cfg.Curator.MaxTurns = *c.MaxTurns
		}
		if c.Timeout != nil {
			cfg.Curator.Timeout = *c.Timeout
		}
	}

	return cfg, path
}
