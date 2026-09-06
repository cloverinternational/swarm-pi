package chat

import (
	"context"
	"strings"
	"unicode"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	sdkprovider "github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/prompts"
)

const (
	swarmRuntimeGuidanceStartTag = "<swarm_runtime_guidance>"
	swarmRuntimeGuidanceEndTag   = "</swarm_runtime_guidance>"
)

func isCodexModelID(model string) bool {
	var normalized string = strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(normalized, "codex")
}

func isCodexBackedRequest(providerName string, model string, prov sdkprovider.Provider) bool {
	if isCodexModelID(model) {
		return true
	}
	if prov != nil && strings.EqualFold(strings.TrimSpace(prov.Name()), "codex") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(providerName), "codex")
}

func resolveCodexCanonicalPrompt(ctx context.Context, model string, refresh bool) (string, bool) {
	if refresh {
		prompt, changed := prompts.RefreshCodexSystemPromptForModel(ctx, model)
		if strings.TrimSpace(prompt) != "" {
			return prompt, changed
		}
	}
	prompt := prompts.GetCodexSystemPromptForModel(ctx, model)
	return prompt, false
}

func upsertSwarmRuntimeGuidanceSection(content string, sectionKey string, sectionBody string) string {
	var sectionStartTag string
	var sectionEndTag string
	sectionStartTag, sectionEndTag = swarmRuntimeGuidanceSectionTags(sectionKey)

	var existingGuidance string
	var remainder string
	existingGuidance, remainder = splitLeadingSwarmRuntimeGuidance(content)

	existingGuidance = strings.TrimSpace(stripTagBlock(existingGuidance, sectionStartTag, sectionEndTag))
	var body string = strings.TrimSpace(sectionBody)

	parts := make([]string, 0, 2)
	if existingGuidance != "" {
		parts = append(parts, existingGuidance)
	}
	if body != "" {
		parts = append(parts, sectionStartTag+"\n"+body+"\n"+sectionEndTag)
	}

	var mergedGuidance string = strings.Join(parts, "\n\n")
	return assembleSwarmRuntimeGuidance(remainder, mergedGuidance)
}

func upsertSwarmRuntimeGuidanceSectionOnLatestUserMessage(messages []*conversation.Message, sectionKey string, sectionBody string) []*conversation.Message {
	if len(messages) == 0 {
		return messages
	}

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg == nil || msg.Role != conversation.RoleUser {
			continue
		}

		updatedContent := upsertSwarmRuntimeGuidanceSection(msg.Content, sectionKey, sectionBody)
		if updatedContent == msg.Content {
			return messages
		}

		updatedMessages := make([]*conversation.Message, len(messages))
		copy(updatedMessages, messages)
		cloned := msg.Clone()
		cloned.Content = updatedContent
		updatedMessages[i] = cloned
		return updatedMessages
	}

	return messages
}

func swarmRuntimeGuidanceSectionTags(sectionKey string) (string, string) {
	var normalizedKey string = normalizeRuntimeSectionKey(sectionKey)
	var startTag string = "<swarm_runtime_" + normalizedKey + ">"
	var endTag string = "</swarm_runtime_" + normalizedKey + ">"
	return startTag, endTag
}

func normalizeRuntimeSectionKey(key string) string {
	trimmed := strings.TrimSpace(strings.ToLower(key))
	if trimmed == "" {
		return "section"
	}

	var b strings.Builder
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune('_')
		}
	}

	var normalized string = strings.Trim(b.String(), "_-")
	if normalized == "" {
		return "section"
	}
	return normalized
}

func splitLeadingSwarmRuntimeGuidance(content string) (string, string) {
	trimmed := strings.TrimLeftFunc(content, unicode.IsSpace)
	if !strings.HasPrefix(trimmed, swarmRuntimeGuidanceStartTag) {
		return "", content
	}

	afterStart := strings.TrimPrefix(trimmed, swarmRuntimeGuidanceStartTag)
	before, after, ok := strings.Cut(afterStart, swarmRuntimeGuidanceEndTag)
	if !ok {
		return "", content
	}

	guidance := strings.TrimSpace(before)
	remainder := strings.TrimLeftFunc(after, unicode.IsSpace)
	return guidance, remainder
}

func assembleSwarmRuntimeGuidance(remainder string, guidance string) string {
	var trimmedGuidance string = strings.TrimSpace(guidance)
	var trimmedRemainder string = strings.TrimLeftFunc(remainder, unicode.IsSpace)

	if trimmedGuidance == "" {
		return trimmedRemainder
	}

	var block string = swarmRuntimeGuidanceStartTag + "\n" + trimmedGuidance + "\n" + swarmRuntimeGuidanceEndTag
	if strings.TrimSpace(trimmedRemainder) == "" {
		return block
	}
	return block + "\n\n" + trimmedRemainder
}

func stripTagBlock(input string, startTag string, endTag string) string {
	updated := input
	for {
		start := strings.Index(updated, startTag)
		if start == -1 {
			break
		}
		endOffset := strings.Index(updated[start+len(startTag):], endTag)
		if endOffset == -1 {
			break
		}
		end := start + len(startTag) + endOffset + len(endTag)
		updated = updated[:start] + updated[end:]
	}
	return updated
}
