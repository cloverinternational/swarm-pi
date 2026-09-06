package a2a

import (
	"maps"
	"strings"

	a2apb "github.com/Swarm-Code/mono/swarm-sdk/internal/a2a/pb"
)

func buildAgentCard(descriptor AgentDescriptor, overrides CardOverrides, rpcURL string) *a2apb.AgentCard {
	name := firstNonEmpty(overrides.Name, descriptor.Name, descriptor.ID, "swarm-agent")
	description := firstNonEmpty(overrides.Description, descriptor.Description, "Swarm top-level agent session")
	version := firstNonEmpty(overrides.Version, descriptor.Version, "0.1.0")
	docURL := firstNonEmpty(overrides.DocumentationURL, descriptor.DocumentationURL)
	iconURL := firstNonEmpty(overrides.IconURL, descriptor.IconURL)

	inputModes := firstNonEmptySlice(overrides.DefaultInputModes, descriptor.DefaultInputModes, []string{"text/plain", "application/json"})
	outputModes := firstNonEmptySlice(overrides.DefaultOutputModes, descriptor.DefaultOutputModes, []string{"text/plain", "application/json"})

	skills := overrides.Skills
	if len(skills) == 0 {
		skills = descriptor.Skills
	}
	if len(skills) == 0 {
		skills = []Skill{{
			ID:          sanitizeSkillID(descriptor.ID),
			Name:        name,
			Description: description,
			Tags:        curatedSkillTags(descriptor),
		}}
	}

	card := &a2apb.AgentCard{
		Name:        name,
		Description: description,
		SupportedInterfaces: []*a2apb.AgentInterface{{
			Url:             rpcURL,
			ProtocolBinding: JSONRPCBinding,
			ProtocolVersion: ProtocolVersion,
		}},
		Version: version,
		Capabilities: &a2apb.AgentCapabilities{
			Streaming:         new(true),
			PushNotifications: new(false),
			Extensions:        append([]*a2apb.AgentExtension(nil), overrides.Extensions...),
			ExtendedAgentCard: new(false),
		},
		DefaultInputModes:  append([]string(nil), inputModes...),
		DefaultOutputModes: append([]string(nil), outputModes...),
		Skills:             make([]*a2apb.AgentSkill, 0, len(skills)),
	}
	if providerName := strings.TrimSpace(descriptor.ProviderName); providerName != "" {
		card.Provider = &a2apb.AgentProvider{
			Organization: providerName,
			Url:          strings.TrimSpace(descriptor.ProviderURL),
		}
	}
	if docURL != "" {
		card.DocumentationUrl = &docURL
	}
	if iconURL != "" {
		card.IconUrl = &iconURL
	}
	if len(overrides.SecuritySchemes) > 0 {
		card.SecuritySchemes = make(map[string]*a2apb.SecurityScheme, len(overrides.SecuritySchemes))
		maps.Copy(card.SecuritySchemes, overrides.SecuritySchemes)
	}
	if len(overrides.SecurityRequirements) > 0 {
		card.SecurityRequirements = append([]*a2apb.SecurityRequirement(nil), overrides.SecurityRequirements...)
	}

	for _, skill := range skills {
		card.Skills = append(card.Skills, &a2apb.AgentSkill{
			Id:          firstNonEmpty(skill.ID, sanitizeSkillID(skill.Name), sanitizeSkillID(name)),
			Name:        firstNonEmpty(skill.Name, name),
			Description: firstNonEmpty(skill.Description, description),
			Tags:        append([]string(nil), skill.Tags...),
			Examples:    append([]string(nil), skill.Examples...),
			InputModes:  append([]string(nil), firstNonEmptySlice(skill.InputModes, inputModes)...),
			OutputModes: append([]string(nil), firstNonEmptySlice(skill.OutputModes, outputModes)...),
		})
	}
	return card
}

func curatedSkillTags(descriptor AgentDescriptor) []string {
	tags := make([]string, 0, 4)
	for _, tool := range descriptor.ToolHints {
		trimmed := strings.TrimSpace(tool)
		if trimmed != "" && trimmed != "*" {
			tags = append(tags, trimmed)
		}
		if len(tags) == 4 {
			break
		}
	}
	if len(tags) == 0 {
		tags = append(tags, "general")
	}
	if provider := strings.TrimSpace(descriptor.ProviderName); provider != "" {
		tags = append(tags, strings.ToLower(provider))
	}
	return dedupeStrings(tags)
}

func sanitizeSkillID(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "swarm-skill"
	}
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "_", "-")
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNonEmptySlice(candidates ...[]string) []string {
	for _, candidate := range candidates {
		if len(candidate) > 0 {
			return dedupeStrings(candidate)
		}
	}
	return nil
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
