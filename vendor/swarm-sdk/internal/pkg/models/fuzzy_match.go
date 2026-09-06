// Package models provides utilities for model name handling and fuzzy matching.
package models

import (
	"fmt"
	"slices"
	"strings"
)

// ModelMatchResult contains the result of a fuzzy model match attempt
type ModelMatchResult struct {
	Model       string   // The matched canonical model name
	Confidence  string   // "exact", "alias", "pattern", or "none"
	Suggestions []string // Alternative suggestions if no match
}

// FuzzyModelMatch attempts to match a user-friendly model name to a canonical model identifier.
// It now returns more detailed information to help prevent wrong matches.
func FuzzyModelMatch(input string) (string, bool) {
	result := FuzzyModelMatchWithDetails(input)
	return result.Model, result.Model != ""
}

// FuzzyModelMatchWithDetails provides detailed matching information
func FuzzyModelMatchWithDetails(input string) ModelMatchResult {
	if input == "" {
		return ModelMatchResult{
			Confidence:  "none",
			Suggestions: []string{"sonnet", "haiku", "opus", "gpt-4", "gpt-3.5-turbo"},
		}
	}

	// Normalize input: lowercase and trim spaces
	normalized := strings.ToLower(strings.TrimSpace(input))

	// Direct matches first (exact canonical names)
	if canonical, ok := directMatches[normalized]; ok {
		return ModelMatchResult{
			Model:      canonical,
			Confidence: "exact",
		}
	}

	// Check aliases - these are high confidence matches
	if canonical, ok := modelAliases[normalized]; ok {
		return ModelMatchResult{
			Model:      canonical,
			Confidence: "alias",
		}
	}

	// Pattern-based matching - be more careful here
	// Only match if the pattern is a significant part of the input
	var patternMatch string
	var matchedPattern string
	for pattern, canonical := range patternMatches {
		if normalized == pattern || strings.HasPrefix(normalized, pattern+" ") || strings.HasSuffix(normalized, " "+pattern) {
			// Exact pattern match or pattern with clear word boundaries
			if matchedPattern == "" || len(pattern) > len(matchedPattern) {
				patternMatch = canonical
				matchedPattern = pattern
			}
		}
	}

	if patternMatch != "" {
		return ModelMatchResult{
			Model:      patternMatch,
			Confidence: "pattern",
		}
	}

	// No match found - provide helpful suggestions
	suggestions := findSimilarModels(normalized)
	return ModelMatchResult{
		Confidence:  "none",
		Suggestions: suggestions,
	}
}

// findSimilarModels returns models that might be what the user intended
func findSimilarModels(input string) []string {
	var suggestions []string

	// Check if input contains key model family terms
	lowerInput := strings.ToLower(input)

	if strings.Contains(lowerInput, "claude") || strings.Contains(lowerInput, "anthropic") {
		suggestions = append(suggestions, "sonnet", "haiku", "opus")
	} else if strings.Contains(lowerInput, "gpt") || strings.Contains(lowerInput, "openai") {
		suggestions = append(suggestions, "gpt-4", "gpt-3.5-turbo")
	} else if strings.Contains(lowerInput, "gemini") || strings.Contains(lowerInput, "google") {
		suggestions = append(suggestions, "gemini-pro")
	} else if strings.Contains(lowerInput, "llama") || strings.Contains(lowerInput, "meta") {
		suggestions = append(suggestions, "llama-3.1-70b", "llama-3.1-8b")
	} else {
		// Generic suggestions
		suggestions = []string{"sonnet", "gpt-4", "gemini-pro"}
	}

	return suggestions
}

// ValidateModelMatch checks if a fuzzy match is safe to use
func ValidateModelMatch(input string, matched string) error {
	result := FuzzyModelMatchWithDetails(input)

	if result.Model == "" {
		return fmt.Errorf("no model found matching '%s'. Try: %s",
			input, strings.Join(result.Suggestions, ", "))
	}

	if result.Confidence == "pattern" {
		// For pattern matches, warn about potential ambiguity
		normalized := strings.ToLower(strings.TrimSpace(input))
		if !strings.Contains(normalized, "claude") && strings.HasPrefix(result.Model, "claude") {
			return fmt.Errorf("ambiguous model '%s' matched to '%s'. Please be more specific", input, result.Model)
		}
		if !strings.Contains(normalized, "gpt") && strings.HasPrefix(result.Model, "gpt") {
			return fmt.Errorf("ambiguous model '%s' matched to '%s'. Please be more specific", input, result.Model)
		}
	}

	return nil
}

// GetAvailableModels returns a list of all available model identifiers
func GetAvailableModels() []string {
	models := make([]string, 0, len(canonicalModels))
	for _, model := range canonicalModels {
		models = append(models, model)
	}
	return models
}

// GetModelAliases returns all known aliases for a given canonical model
func GetModelAliases(canonical string) []string {
	var aliases []string
	for alias, model := range modelAliases {
		if model == canonical {
			aliases = append(aliases, alias)
		}
	}
	return aliases
}

// Canonical model identifiers
var canonicalModels = []string{
	// Anthropic Claude 4.x models (current)
	"claude-fable-5",
	"claude-opus-4-7",
	"claude-opus-4-6",
	"claude-opus-4-5-20251101",
	"claude-sonnet-4-6",
	"claude-sonnet-4-5-20250929",
	"claude-haiku-4-5-20251001",
	// Legacy Anthropic models (kept for backward-compat with stored configs)
	"claude-3-5-sonnet-20241022",
	"claude-3-5-haiku-20241022",
	"claude-3-opus-20240229",
	"claude-3-sonnet-20240229",
	"claude-3-haiku-20240307",
	"claude-2.1",
	"claude-2.0",
	"claude-instant-1.2",

	// OpenAI GPT models
	"gpt-4-turbo-preview",
	"gpt-4-turbo",
	"gpt-4",
	"gpt-4-32k",
	"gpt-3.5-turbo",
	"gpt-3.5-turbo-16k",

	// Other providers
	"gemini-pro",
	"gemini-pro-vision",
	"llama-3.1-8b",
	"llama-3.1-70b",
	"mixtral-8x7b",
}

// Direct canonical name matches (lowercase)
var directMatches = map[string]string{
	// Current Claude 4.x
	"claude-fable-5":             "claude-fable-5",
	"claude-opus-4-7":            "claude-opus-4-7",
	"claude-opus-4-6":            "claude-opus-4-6",
	"claude-opus-4-5-20251101":   "claude-opus-4-5-20251101",
	"claude-sonnet-4-6":          "claude-sonnet-4-6",
	"claude-sonnet-4-5-20250929": "claude-sonnet-4-5-20250929",
	"claude-haiku-4-5-20251001":  "claude-haiku-4-5-20251001",
	// Legacy
	"claude-3-5-sonnet-20241022": "claude-3-5-sonnet-20241022",
	"claude-3-5-haiku-20241022":  "claude-3-5-haiku-20241022",
	"claude-3-opus-20240229":     "claude-3-opus-20240229",
	"claude-3-sonnet-20240229":   "claude-3-sonnet-20240229",
	"claude-3-haiku-20240307":    "claude-3-haiku-20240307",
	"claude-2.1":                 "claude-2.1",
	"claude-2.0":                 "claude-2.0",
	"claude-instant-1.2":         "claude-instant-1.2",
	"gpt-4-turbo-preview":        "gpt-4-turbo-preview",
	"gpt-4-turbo":                "gpt-4-turbo",
	"gpt-4":                      "gpt-4",
	"gpt-4-32k":                  "gpt-4-32k",
	"gpt-3.5-turbo":              "gpt-3.5-turbo",
	"gpt-3.5-turbo-16k":          "gpt-3.5-turbo-16k",
}

// Model aliases for user-friendly names
// Model aliases for user-friendly names - keep these specific to avoid confusion
var modelAliases = map[string]string{
	// Claude aliases - be specific about versions
	"sonnet":            "claude-3-5-sonnet-20241022", // Latest Sonnet
	"new sonnet":        "claude-3-5-sonnet-20241022",
	"3.5 sonnet":        "claude-3-5-sonnet-20241022",
	"claude 3.5 sonnet": "claude-3-5-sonnet-20241022",
	"claude-3.5-sonnet": "claude-3-5-sonnet-20241022",
	"claude 3.5":        "claude-3-5-sonnet-20241022", // Default to sonnet
	"claude-3.5":        "claude-3-5-sonnet-20241022", // Default to sonnet

	"haiku":            "claude-3-5-haiku-20241022", // Latest Haiku
	"new haiku":        "claude-3-5-haiku-20241022",
	"3.5 haiku":        "claude-3-5-haiku-20241022",
	"claude 3.5 haiku": "claude-3-5-haiku-20241022",
	"claude-3.5-haiku": "claude-3-5-haiku-20241022",

	"opus":          "claude-3-opus-20240229",
	"claude opus":   "claude-3-opus-20240229",
	"claude 3 opus": "claude-3-opus-20240229",

	"old sonnet":      "claude-3-sonnet-20240229", // Explicitly old version
	"claude 3 sonnet": "claude-3-sonnet-20240229",

	"claude instant": "claude-instant-1.2",
	"instant":        "claude-instant-1.2",

	// GPT aliases - default to turbo variants
	"gpt4":  "gpt-4-turbo-preview",
	"gpt-4": "gpt-4-turbo-preview",
	"gpt 4": "gpt-4-turbo-preview",
	"turbo": "gpt-4-turbo-preview",

	"gpt3":    "gpt-3.5-turbo",
	"gpt-3":   "gpt-3.5-turbo",
	"gpt-3.5": "gpt-3.5-turbo",
	"gpt 3.5": "gpt-3.5-turbo",
	"chatgpt": "gpt-3.5-turbo",

	// Other specific aliases
	"gemini":    "gemini-pro",
	"llama3":    "llama-3.1-70b",
	"llama-3":   "llama-3.1-70b",
	"llama 70b": "llama-3.1-70b",
	"llama 8b":  "llama-3.1-8b",
	"mixtral":   "mixtral-8x7b",
}

// Pattern-based matches - use only for very specific patterns to avoid false matches
var patternMatches = map[string]string{
	// Only include patterns that are very unlikely to cause false matches
	// Removed generic patterns to avoid ambiguity
}

// IsValidModel checks if a model identifier is a valid canonical model
func IsValidModel(model string) bool {
	return slices.Contains(canonicalModels, model)
}
