package commands

import "strings"

// ContextFromModel returns the context window for a model, falling back to the label.
func ContextFromModel(model ModelInfo) int {
	if model.ContextWindow > 0 {
		return model.ContextWindow
	}
	return ParseContextValue(model.Context)
}

// InferModelTags assigns heuristic tags based on model identifiers and context.
func InferModelTags(providerName string, model ModelInfo) map[ModelTag]bool {
	tags := make(map[ModelTag]bool)
	text := strings.ToLower(strings.TrimSpace(model.ID + " " + model.DisplayName))
	provider := strings.ToLower(strings.TrimSpace(providerName))

	if hasAny(text, "mini", "small", "flash", "fast", "haiku", "lite", "turbo", "instant", "quick", "nano") {
		tags[TagFast] = true
	}
	if hasAny(text, "code", "coder", "coding", "dev") {
		tags[TagCoding] = true
	}
	if hasAny(text, "vision", "multimodal", "image", "gpt-4o", "gpt-4.1", "gpt-4.5", "claude-3", "claude-4", "gemini") {
		tags[TagVision] = true
	}
	if hasAny(text, "tool", "tools", "function") {
		tags[TagTools] = true
	}
	if provider == "openai" || provider == "anthropic" || provider == "claudecode" || provider == "openrouter" || provider == "gemini" {
		tags[TagTools] = true
	}

	context := ContextFromModel(model)
	if context >= 128000 {
		tags[TagLongContext] = true
	}

	return tags
}

func hasAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if needle == "" {
			continue
		}
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
