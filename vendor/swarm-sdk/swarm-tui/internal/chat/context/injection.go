package context

import (
	"strings"
	"unicode"
)

const (
	ContextStartTag       = "<swarmos_context>"
	ContextEndTag         = "</swarmos_context>"
	CachedContextStartTag = "<swarmos_cached_context>"
	CachedContextEndTag   = "</swarmos_cached_context>"
)

// StripInjectedContext removes any previously injected context block.
// It is a best-effort operation and leaves the original prompt intact if tags are malformed.
func StripInjectedContext(systemPrompt string) string {
	if systemPrompt == "" {
		return ""
	}

	updated := stripTagBlock(systemPrompt, CachedContextStartTag, CachedContextEndTag)
	updated = stripTagBlock(updated, ContextStartTag, ContextEndTag)
	return strings.TrimRightFunc(updated, unicode.IsSpace)
}

// InjectContext appends a context block after removing any previous injections.
func InjectContext(systemPrompt, contextBlock string) string {
	cachedBlock, ephemeralBlock := SplitContextBlock(contextBlock)
	return InjectContextBlocks(systemPrompt, cachedBlock, ephemeralBlock)
}

// InjectContextBlocks injects cached and ephemeral blocks after stripping previous injections.
// Cached blocks are always injected before ephemeral blocks.
func InjectContextBlocks(systemPrompt, cachedBlock, ephemeralBlock string) string {
	base := strings.TrimSpace(StripInjectedContext(systemPrompt))
	cachedBlock = normalizeContextBlock(cachedBlock, CachedContextStartTag, CachedContextEndTag)
	ephemeralBlock = normalizeContextBlock(ephemeralBlock, ContextStartTag, ContextEndTag)

	parts := make([]string, 0, 3)
	if base != "" {
		parts = append(parts, base)
	}
	if cachedBlock != "" {
		parts = append(parts, cachedBlock)
	}
	if ephemeralBlock != "" {
		parts = append(parts, ephemeralBlock)
	}
	return strings.Join(parts, "\n\n")
}

// SplitContextBlock extracts cached and ephemeral context content from a context block.
// If no tags are present, the content is treated as ephemeral.
func SplitContextBlock(contextBlock string) (string, string) {
	trimmed := strings.TrimSpace(contextBlock)
	if trimmed == "" {
		return "", ""
	}

	base, cachedSections := extractTagBlocks(trimmed, CachedContextStartTag, CachedContextEndTag)
	base, ephemeralSections := extractTagBlocks(base, ContextStartTag, ContextEndTag)

	cached := joinSections(cachedSections)
	ephemeral := joinSections(ephemeralSections)
	base = strings.TrimSpace(base)

	if cached == "" && ephemeral == "" {
		return "", strings.TrimSpace(trimmed)
	}

	if base != "" {
		if ephemeral == "" {
			ephemeral = base
		} else {
			ephemeral = strings.TrimSpace(ephemeral + "\n\n" + base)
		}
	}

	return cached, ephemeral
}

func stripTagBlock(input, startTag, endTag string) string {
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

func normalizeContextBlock(block, startTag, endTag string) string {
	trimmed := strings.TrimSpace(block)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, startTag) && strings.HasSuffix(trimmed, endTag) {
		return trimmed
	}
	if strings.Contains(trimmed, startTag) && strings.Contains(trimmed, endTag) {
		return trimmed
	}
	return startTag + "\n" + trimmed + "\n" + endTag
}

func extractTagBlocks(input, startTag, endTag string) (string, []string) {
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
