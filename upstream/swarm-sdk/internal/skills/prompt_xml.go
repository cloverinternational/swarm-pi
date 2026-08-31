// Package skills implements the Claude Code-style skills system.
// This file contains XML generation for system prompts per the agentskills.io specification.
package skills

import (
	"encoding/xml"
	"fmt"
	"html"
	"path/filepath"
	"sort"
	"strings"
)

// XMLSkill represents a skill in XML format for system prompts.
type XMLSkill struct {
	XMLName     xml.Name `xml:"skill"`
	Name        string   `xml:"name"`
	Description string   `xml:"description"`
	Location    string   `xml:"location"`
}

// XMLAvailableSkills represents the <available_skills> XML block.
type XMLAvailableSkills struct {
	XMLName xml.Name   `xml:"available_skills"`
	Skills  []XMLSkill `xml:"skill"`
}

// GenerateAvailableSkillsXML generates the <available_skills> XML block
// for injection into system prompts. Skills are sorted by name for consistency.
func GenerateAvailableSkillsXML(skills []*Skill) string {
	return GenerateAvailableSkillsXMLCapped(skills, MaxAvailableSkillsDefault)
}

// GenerateAvailableSkillsXMLCapped renders at most maxSkills skills (<=0 means
// no cap). Skills are sorted by name for determinism; when the list is
// truncated a comment marker records how many were omitted so the model knows
// the listing is partial.
func GenerateAvailableSkillsXMLCapped(skills []*Skill, maxSkills int) string {
	if len(skills) == 0 {
		return ""
	}

	// Sort skills by name for consistent output
	sortedSkills := make([]*Skill, len(skills))
	copy(sortedSkills, skills)
	sort.Slice(sortedSkills, func(i, j int) bool {
		return sortedSkills[i].Metadata.Name < sortedSkills[j].Metadata.Name
	})

	omitted := 0
	if maxSkills > 0 && len(sortedSkills) > maxSkills {
		omitted = len(sortedSkills) - maxSkills
		sortedSkills = sortedSkills[:maxSkills]
	}

	var builder strings.Builder
	builder.WriteString("<available_skills>\n")

	for _, skill := range sortedSkills {
		builder.WriteString(GenerateSkillXML(skill))
	}

	if omitted > 0 {
		builder.WriteString(fmt.Sprintf("  <!-- %d additional skill(s) omitted to bound prompt size; run the curator to consolidate/archive -->\n", omitted))
	}

	builder.WriteString("</available_skills>")
	return builder.String()
}

// GenerateSkillXML generates XML for a single skill.
// Output format per agentskills.io specification:
//
//	<skill>
//	  <name>skill-name</name>
//	  <description>What this skill does</description>
//	  <location>/path/to/skill/SKILL.md</location>
//	</skill>
func GenerateSkillXML(skill *Skill) string {
	return generateSkillXMLWithDescription(skill, "")
}

// generateSkillXMLWithDescription renders a skill entry, optionally replacing
// the metadata description. The override keeps prompt-only truncation from
// mutating the registry's canonical Skill object.
func generateSkillXMLWithDescription(skill *Skill, descriptionOverride string) string {
	if skill == nil {
		return ""
	}

	description := skill.Metadata.Description
	if descriptionOverride != "" {
		description = descriptionOverride
	}

	var builder strings.Builder
	builder.WriteString("  <skill>\n")
	builder.WriteString(fmt.Sprintf("    <name>%s</name>\n", escapeXML(skill.Metadata.Name)))
	builder.WriteString(fmt.Sprintf("    <description>%s</description>\n", escapeXML(description)))

	// Include when_to_use if present (mirrors Claude Code's SkillTool prompt)
	if skill.Metadata.WhenToUse != "" {
		builder.WriteString(fmt.Sprintf("    <when_to_use>%s</when_to_use>\n", escapeXML(skill.Metadata.WhenToUse)))
	}

	// Generate location path (path to SKILL.md)
	location := filepath.Join(skill.Path, "SKILL.md")
	builder.WriteString(fmt.Sprintf("    <location>%s</location>\n", escapeXML(location)))

	builder.WriteString("  </skill>\n")
	return builder.String()
}

// GenerateRankedAvailableSkillsXML renders an already-ranked skill list without
// re-sorting it. Both an entry cap and a byte budget are enforced so a large
// skill library cannot dominate the system prompt. Descriptions are shortened
// only in the prompt representation; full metadata remains available through
// SkillManage.
func GenerateRankedAvailableSkillsXML(skills []*Skill, maxSkills, maxChars int) string {
	if len(skills) == 0 {
		return ""
	}
	if maxSkills <= 0 {
		maxSkills = len(skills)
	}
	if maxChars <= 0 {
		maxChars = MaxAvailableSkillsCharsDefault
	}

	const closing = "</available_skills>"
	var builder strings.Builder
	builder.WriteString("<available_skills>\n")
	rendered := 0
	for _, skill := range skills {
		if skill == nil || rendered >= maxSkills {
			continue
		}
		description := truncatePromptDescription(skill.Metadata.Description, MaxPromptDescriptionRunesDefault)
		entry := generateSkillXMLWithDescription(skill, description)
		if rendered > 0 && builder.Len()+len(entry)+len(closing)+OmissionMarkerReserve > maxChars {
			break
		}
		builder.WriteString(entry)
		rendered++
	}

	omitted := len(skills) - rendered
	if omitted > 0 {
		builder.WriteString(fmt.Sprintf("  <!-- %d additional skill(s) omitted to bound prompt size; use SkillManage(action=\"list\") for on-demand discovery -->\n", omitted))
	}
	builder.WriteString(closing)
	return builder.String()
}

func truncatePromptDescription(value string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(value))
	if maxRunes <= 0 || len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes-1]) + "…"
}

// promptVisibleDescription returns the portion of a description the ranked
// catalog actually renders. Relevance scoring uses it so ranking and display
// agree on the same text.
func promptVisibleDescription(value string) string {
	return truncatePromptDescription(value, MaxPromptDescriptionRunesDefault)
}

// GenerateSkillXMLWithDetails generates more detailed XML for a skill including
// optional fields when present. This is useful for skill discovery interfaces.
func GenerateSkillXMLWithDetails(skill *Skill) string {
	if skill == nil {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("  <skill>\n")
	builder.WriteString(fmt.Sprintf("    <name>%s</name>\n", escapeXML(skill.Metadata.Name)))
	builder.WriteString(fmt.Sprintf("    <description>%s</description>\n", escapeXML(skill.Metadata.Description)))

	// Generate location path
	location := filepath.Join(skill.Path, "SKILL.md")
	builder.WriteString(fmt.Sprintf("    <location>%s</location>\n", escapeXML(location)))

	// Include optional fields if present
	if skill.Metadata.Version != "" {
		builder.WriteString(fmt.Sprintf("    <version>%s</version>\n", escapeXML(skill.Metadata.Version)))
	}

	if skill.Metadata.Author != "" {
		builder.WriteString(fmt.Sprintf("    <author>%s</author>\n", escapeXML(skill.Metadata.Author)))
	}

	if skill.Metadata.Category != "" {
		builder.WriteString(fmt.Sprintf("    <category>%s</category>\n", escapeXML(skill.Metadata.Category)))
	}

	if len(skill.Metadata.Tags) > 0 {
		builder.WriteString(fmt.Sprintf("    <tags>%s</tags>\n", escapeXML(strings.Join(skill.Metadata.Tags, ", "))))
	}

	if skill.Metadata.License != "" {
		builder.WriteString(fmt.Sprintf("    <license>%s</license>\n", escapeXML(skill.Metadata.License)))
	}

	if skill.Metadata.Compatibility != "" {
		builder.WriteString(fmt.Sprintf("    <compatibility>%s</compatibility>\n", escapeXML(skill.Metadata.Compatibility)))
	}

	if skill.Metadata.AllowedTools != "" {
		builder.WriteString(fmt.Sprintf("    <allowed-tools>%s</allowed-tools>\n", escapeXML(skill.Metadata.AllowedTools)))
	}

	// Include when_to_use if present
	if skill.Metadata.WhenToUse != "" {
		builder.WriteString(fmt.Sprintf("    <when_to_use>%s</when_to_use>\n", escapeXML(skill.Metadata.WhenToUse)))
	}

	builder.WriteString("  </skill>\n")
	return builder.String()
}

// GenerateActiveSkillsXML always returns "" in the Claude-style skill model.
//
// Deprecated: Active skill tracking is not used in the on-demand invocation
// model. This function is preserved for backward compatibility.
func GenerateActiveSkillsXML(skills []*Skill) string {
	return ""
}

// GenerateSkillInstructionsXML generates XML containing skill instructions
// for injection into system prompts.
func GenerateSkillInstructionsXML(skill *Skill) string {
	if skill == nil || skill.Instructions == "" {
		return ""
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("<skill_instructions name=\"%s\">\n", escapeXMLAttr(skill.Metadata.Name)))

	// Include metadata context
	if skill.Metadata.Description != "" {
		builder.WriteString(fmt.Sprintf("<context>%s</context>\n", escapeXML(skill.Metadata.Description)))
	}

	// Include the actual instructions (CDATA for markdown content)
	builder.WriteString("<instructions><![CDATA[\n")
	builder.WriteString(skill.Instructions)
	builder.WriteString("\n]]></instructions>\n")

	builder.WriteString("</skill_instructions>")
	return builder.String()
}

// GenerateCombinedInstructionsXML always returns "" in the Claude-style skill model.
//
// Deprecated: Active skill instructions are not injected into system prompts
// in the on-demand invocation model. Preserved for backward compatibility.
func GenerateCombinedInstructionsXML(skills []*Skill) string {
	return ""
}

// escapeXML escapes special characters for XML text content.
func escapeXML(s string) string {
	return html.EscapeString(s)
}

// escapeXMLAttr escapes special characters for XML attribute values.
func escapeXMLAttr(s string) string {
	// Same as escapeXML but also handles quotes
	escaped := html.EscapeString(s)
	escaped = strings.ReplaceAll(escaped, "\"", "&quot;")
	return escaped
}

// SkillPromptContext contains all skill-related XML for system prompts.
type SkillPromptContext struct {
	// AvailableSkillsXML contains the <available_skills> block
	AvailableSkillsXML string

	// ActiveSkillsXML contains the <active_skills> block
	ActiveSkillsXML string

	// InstructionsXML contains the <loaded_skills> block with instructions
	InstructionsXML string
}

// GeneratePromptContext generates all skill-related XML blocks for system prompts.
func GeneratePromptContext(available, active []*Skill) *SkillPromptContext {
	return &SkillPromptContext{
		AvailableSkillsXML: GenerateRankedAvailableSkillsXML(
			available,
			MaxAvailableSkillsDefault,
			MaxAvailableSkillsCharsDefault,
		),
		ActiveSkillsXML: GenerateActiveSkillsXML(active),
		InstructionsXML: GenerateCombinedInstructionsXML(active),
	}
}

// String returns the combined XML for all skill context.
func (c *SkillPromptContext) String() string {
	if c == nil {
		return ""
	}

	var parts []string
	if c.AvailableSkillsXML != "" {
		parts = append(parts, c.AvailableSkillsXML)
	}
	if c.ActiveSkillsXML != "" {
		parts = append(parts, c.ActiveSkillsXML)
	}
	if c.InstructionsXML != "" {
		parts = append(parts, c.InstructionsXML)
	}

	return strings.Join(parts, "\n\n")
}

// GenerateSkillToolListing generates a compact text listing of skills for
// injection into the SkillTool's system prompt description. This is the
// listing the LLM sees when deciding which skill to invoke.
//
// Only frontmatter (name + description + when_to_use) is included — no full
// instructions. Skills with DisableModelInvocation=true are filtered out.
// Descriptions are truncated to maxDescChars to fit within a token budget.
//
// Mirrors src/tools/SkillTool/prompt.ts:70-171 in Claude Code.
func GenerateSkillToolListing(skills []*Skill, maxDescChars int) string {
	if len(skills) == 0 {
		return ""
	}

	if maxDescChars <= 0 {
		maxDescChars = MaxListingDescCharsDefault
	}

	var builder strings.Builder
	builder.WriteString("Available skills:\n")

	for _, skill := range skills {
		// Filter out skills hidden from model invocation
		if skill.Metadata.DisableModelInvocation {
			continue
		}

		name := skill.Metadata.Name
		desc := skill.Metadata.Description
		whenToUse := skill.Metadata.WhenToUse

		// Truncate description if needed
		if len(desc) > maxDescChars {
			if maxDescChars > 1 {
				desc = desc[:maxDescChars-1] + "…"
			} else {
				desc = "…"
			}
		}

		// Build entry
		builder.WriteString(fmt.Sprintf("- %s: %s", name, desc))
		if whenToUse != "" {
			// Truncate when_to_use to a reasonable length
			wtuMax := max(maxDescChars/2, 50)
			if len(whenToUse) > wtuMax {
				whenToUse = whenToUse[:wtuMax-1] + "…"
			}
			builder.WriteString(fmt.Sprintf(". When to use: %s", whenToUse))
		}
		builder.WriteString("\n")
	}

	return strings.TrimRight(builder.String(), "\n")
}

// MaxListingDescCharsDefault is the default maximum description length
// for skill listings when no explicit budget is provided.
const MaxListingDescCharsDefault = 250

// MaxAvailableSkillsDefault caps how many skills are rendered into the
// <available_skills> block to bound system-prompt size. 0 in the capped
// variant means "no cap".
const MaxAvailableSkillsDefault = 60

// MaxAvailableSkillsCharsDefault is the byte budget for the ranked skill
// catalog injected into a request. Omitted skills remain discoverable through
// SkillManage.
const MaxAvailableSkillsCharsDefault = 12_000

// MaxPromptDescriptionRunesDefault bounds each catalog description while
// preserving the complete description in the registry and on disk.
const MaxPromptDescriptionRunesDefault = 240

// OmissionMarkerReserve leaves room for a bounded omission marker when deciding
// whether another skill entry fits the catalog budget.
const OmissionMarkerReserve = 180
