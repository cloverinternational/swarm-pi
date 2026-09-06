package skilltools

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// Budget constants for skill listing in system prompts.
// Mirrors src/tools/SkillTool/prompt.ts in Claude Code.
const (
	// SkillBudgetContextPercent is the fraction of the context window reserved
	// for the skill listing. Claude Code uses 1% (0.01).
	SkillBudgetContextPercent = 0.01

	// CharsPerToken is the approximate characters per token for budget estimation.
	CharsPerToken = 4

	// DefaultCharBudget is the fallback budget when context window size is unknown.
	// 1% of 200k tokens × 4 chars/token = 8000 chars.
	DefaultCharBudget = 8000

	// MaxListingDescChars is the maximum description length for non-bundled skills
	// in the listing. Descriptions exceeding this are truncated with "…".
	MaxListingDescChars = 250
)

// FormatSkillsWithinBudget generates a compact skill listing that fits within
// a token budget. Only frontmatter (name + description + whenToUse) is emitted;
// full skill content is not included.
//
// The listing format:
//
//	Available skills:
//	- skill-name: Description. When to use: whenToUse
//	- another-skill: Description
//
// Mirrors src/tools/SkillTool/prompt.ts:70-171 in Claude Code.
func FormatSkillsWithinBudget(allSkills []*skills.Skill, contextWindowTokens int) string {
	if len(allSkills) == 0 {
		return ""
	}

	// Filter out skills with DisableModelInvocation
	var eligible []*skills.Skill
	for _, s := range allSkills {
		if !s.Metadata.DisableModelInvocation {
			eligible = append(eligible, s)
		}
	}
	if len(eligible) == 0 {
		return ""
	}

	// Sort by name for consistent output
	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].Metadata.Name < eligible[j].Metadata.Name
	})

	// Calculate character budget
	charBudget := DefaultCharBudget
	if contextWindowTokens > 0 {
		charBudget = int(float64(contextWindowTokens) * SkillBudgetContextPercent * CharsPerToken)
	}
	if charBudget < 500 {
		charBudget = 500 // Minimum viable budget
	}

	// Reserve space for the header
	header := "Available skills:\n"
	charBudget -= len(header)
	if charBudget <= 0 {
		return header
	}

	var builder strings.Builder
	builder.WriteString(header)

	for _, skill := range eligible {
		entry := formatSkillEntry(skill)
		entryLen := len(entry) + 1 // +1 for newline

		if entryLen <= charBudget {
			builder.WriteString(entry)
			builder.WriteString("\n")
			charBudget -= entryLen
		} else {
			// Try a truncated version
			truncEntry := formatSkillEntryTruncated(skill, charBudget)
			if truncEntry != "" && len(truncEntry)+1 <= charBudget {
				builder.WriteString(truncEntry)
				builder.WriteString("\n")
				charBudget -= len(truncEntry) + 1
			}
			// If even truncated doesn't fit, skip this skill
		}
	}

	result := builder.String()
	// Remove trailing newline
	return strings.TrimRight(result, "\n")
}

// formatSkillEntry creates a single-line entry for a skill in the listing.
func formatSkillEntry(skill *skills.Skill) string {
	name := skill.Metadata.Name
	desc := skill.Metadata.Description
	whenToUse := skill.Metadata.WhenToUse

	// Build the description part
	entry := fmt.Sprintf("- %s: %s", name, desc)
	if whenToUse != "" {
		entry += fmt.Sprintf(". When to use: %s", whenToUse)
	}

	return entry
}

// formatSkillEntryTruncated creates a truncated entry that fits within maxChars.
func formatSkillEntryTruncated(skill *skills.Skill, maxChars int) string {
	if maxChars <= 10 {
		return "" // Not enough space for a meaningful entry
	}

	name := skill.Metadata.Name
	prefix := fmt.Sprintf("- %s: ", name)

	// Calculate remaining chars for description
	remaining := maxChars - len(prefix)
	if remaining <= 0 {
		return prefix + "…"
	}

	desc := skill.Metadata.Description
	if len(desc) > remaining {
		if remaining > 1 {
			desc = desc[:remaining-1] + "…"
		} else {
			desc = "…"
		}
	}

	return prefix + desc
}
