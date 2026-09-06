package client

// SkillInfo is a public, read-only descriptor of a loaded skill for UI listing.
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category,omitempty"`
	Active      bool   `json:"active"`
	Source      string `json:"source,omitempty"` // user | project | builtin | autogen | ...
}

// LoadedSkills returns the skills actually loaded into the agent's skill
// registry (autogen + discovered), matching the TUI's source — NOT the
// config-bundle's "installed" list. Returns nil when autogenskills is off.
func (c *Client) LoadedSkills() []SkillInfo {
	if c == nil || c.skillRegistry == nil {
		return nil
	}
	all := c.skillRegistry.List()
	out := make([]SkillInfo, 0, len(all))
	for _, s := range all {
		if s == nil {
			continue
		}
		out = append(out, SkillInfo{
			Name:        s.Metadata.Name,
			Description: s.Metadata.Description,
			Category:    s.Metadata.Category,
			Active:      c.skillRegistry.IsActive(s.Metadata.Name),
			Source:      s.LoadedFrom,
		})
	}
	return out
}

// SkillBudgetState is a public, read-only snapshot of the two-tier skill budget
// for UI display. Active is false when autogenskills/budget enforcement is not
// wired. The current tier's gauge is (Used / Limit).
type SkillBudgetState struct {
	Active           bool   `json:"active"`
	Tier             string `json:"tier"` // "onboarding" | "working"
	Used             int    `json:"used"`
	Limit            int    `json:"limit"`
	Skilled          bool   `json:"skilled"`
	NudgeIgnores     int    `json:"nudgeIgnores"`
	OnboardingBudget int    `json:"onboardingBudget"`
	WorkingBudget    int    `json:"workingBudget"`
	MaxNudgeIgnores  int    `json:"maxNudgeIgnores"`
}

// SkillBudget returns the live skill-budget state for display. When the budget
// hook is not wired (autogenskills disabled or ToolCallBudget == 0) it returns
// {Active: false}.
func (c *Client) SkillBudget() SkillBudgetState {
	if c == nil || c.skillBudgetHook == nil {
		return SkillBudgetState{Active: false}
	}
	s := c.skillBudgetHook.GetBudgetSnapshot()
	tier := "onboarding"
	limit := int(s.OnboardingBudget)
	if s.Skilled {
		tier = "working"
		limit = int(s.WorkingBudget)
	}
	return SkillBudgetState{
		Active:           true,
		Tier:             tier,
		Used:             s.ToolCalls,
		Limit:            limit,
		Skilled:          s.Skilled,
		NudgeIgnores:     s.NudgeIgnores,
		OnboardingBudget: int(s.OnboardingBudget),
		WorkingBudget:    int(s.WorkingBudget),
		MaxNudgeIgnores:  s.MaxNudgeIgnores,
	}
}
