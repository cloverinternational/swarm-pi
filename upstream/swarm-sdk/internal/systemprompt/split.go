package systemprompt

import "strings"

const (
	CachedContextStartTag    = "<swarmos_cached_context>"
	CachedContextEndTag      = "</swarmos_cached_context>"
	EphemeralContextStartTag = "<swarmos_context>"
	EphemeralContextEndTag   = "</swarmos_context>"
)

// SplitSystemPrompt extracts base, cached, and ephemeral context sections from a system prompt.
// Any tagged sections are removed from the returned base prompt.
func SplitSystemPrompt(systemPrompt string) (string, string, string) {
	base, cachedSections := extractTaggedSections(systemPrompt, CachedContextStartTag, CachedContextEndTag)
	base, ephemeralSections := extractTaggedSections(base, EphemeralContextStartTag, EphemeralContextEndTag)

	base = strings.TrimSpace(base)
	cached := strings.TrimSpace(joinSections(cachedSections))
	ephemeral := strings.TrimSpace(joinSections(ephemeralSections))

	return base, cached, ephemeral
}

func extractTaggedSections(input, startTag, endTag string) (string, []string) {
	updated := input
	sections := []string{}
	for {
		start := strings.Index(updated, startTag)
		if start == -1 {
			break
		}
		endOffset := strings.Index(updated[start+len(startTag):], endTag)
		if endOffset == -1 {
			break
		}
		section := updated[start+len(startTag) : start+len(startTag)+endOffset]
		sections = append(sections, section)
		end := start + len(startTag) + endOffset + len(endTag)
		updated = updated[:start] + updated[end:]
	}
	return updated, sections
}

func joinSections(sections []string) string {
	cleaned := make([]string, 0, len(sections))
	for _, section := range sections {
		trimmed := strings.TrimSpace(section)
		if trimmed == "" {
			continue
		}
		cleaned = append(cleaned, trimmed)
	}
	return strings.Join(cleaned, "\n\n")
}
