// Package fallback provides a model fallback chain abstraction for resilient LLM calls.
// It allows configuring a primary model with fallbacks that are tried in order if the primary fails.
package fallback

import (
	"fmt"
	"strings"
)

// nonChatModelExactNames lists legacy/utility OpenAI model IDs that are not
// chat-completion models at all (legacy text-completion models). Matched
// case-insensitively against the whole model ID.
var nonChatModelExactNames = map[string]struct{}{
	"babbage-002": {},
	"davinci-002": {},
}

// nonChatModelPrefixes lists ID prefixes for OpenAI (and OpenAI-compatible)
// models that are image, audio, embedding, moderation, or legacy
// completion-only models. None of these accept a chat-completions request,
// so they must never be auto-selected — or silently retained — as a chat
// fallback-chain entry. Matched case-insensitively as a prefix of the model ID.
//
// Background: Swarm-Code/mono#66 — a provider's raw /v1/models listing (used
// to populate provider catalogs and fallback-chain pickers) is not filtered
// for chat-completion capability, so alphabetically-early legacy models like
// "babbage-002" could end up selected as a UI default and persisted into
// chat_fallback_chain. Once persisted, every execution that fell through to
// that fallback entry hit a hard 400 from the backend and aborted the whole
// run because there was no other fallback left to try.
var nonChatModelPrefixes = []string{
	"dall-e-",
	"gpt-image-",
	"whisper-",
	"tts-",
	"text-embedding-",
	"text-moderation-",
	"omni-moderation-",
	"computer-use-preview",
	"text-davinci-",
	"text-curie-",
	"text-babbage-",
	"text-ada-",
	"code-davinci-",
	"code-cushman-",
}

// IsKnownNonChatModel reports whether model is a known non-chat-completion
// model (legacy text-completion, image, audio, embedding, or moderation
// model). Such models must never be auto-selected — or silently kept — as a
// chat/fallback-chain primary or fallback entry: sending them a chat request
// always fails, and for a fallback chain that failure can abort an entire
// run when it's the last entry left. See Swarm-Code/mono#66.
func IsKnownNonChatModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return false
	}
	if _, ok := nonChatModelExactNames[m]; ok {
		return true
	}
	for _, prefix := range nonChatModelPrefixes {
		if strings.HasPrefix(m, prefix) {
			return true
		}
	}
	return false
}

// SanitizeChatChain returns a copy of chain with any entry that references a
// known non-chat-completion model (see IsKnownNonChatModel) removed. If the
// primary entry itself is a known non-chat model, the first surviving
// fallback (if any) is promoted to primary. Returns nil if every entry in
// the chain is a known non-chat model or the chain itself is nil/empty.
// This defends chat/fallback execution paths against a persisted config
// that was poisoned by a bad UI default or manual edit (Swarm-Code/mono#66).
func SanitizeChatChain(chain *Chain) *Chain {
	if chain == nil || chain.IsEmpty() {
		return chain
	}

	var kept []ModelRef
	for _, ref := range chain.All() {
		if ref.IsEmpty() || IsKnownNonChatModel(ref.Model) {
			continue
		}
		kept = append(kept, ref)
	}

	if len(kept) == 0 {
		return nil
	}

	sanitized := &Chain{Primary: kept[0]}
	if len(kept) > 1 {
		sanitized.Fallbacks = append([]ModelRef{}, kept[1:]...)
	}
	return sanitized
}

// ModelRef represents a provider/model pair in a fallback chain.
// This is compatible with commands.ModelRef but defined here to avoid circular imports.
type ModelRef struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// Key returns a normalized key for the model reference.
func (m ModelRef) Key() string {
	return strings.ToLower(strings.TrimSpace(m.Provider)) + "::" + strings.ToLower(strings.TrimSpace(m.Model))
}

// String returns a human-readable representation.
func (m ModelRef) String() string {
	return fmt.Sprintf("%s/%s", m.Provider, m.Model)
}

// IsEmpty returns true if the model reference has no provider or model set.
func (m ModelRef) IsEmpty() bool {
	return m.Provider == "" || m.Model == ""
}

// ParseModelRef parses a model reference string in the format "provider/model".
// Returns an empty ModelRef if the string is empty or malformed.
func ParseModelRef(s string) ModelRef {
	if s == "" {
		return ModelRef{}
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ModelRef{}
	}
	return ModelRef{Provider: parts[0], Model: parts[1]}
}

// Chain represents an ordered list of model choices with a primary and fallbacks.
// Models are tried in order until one succeeds.
type Chain struct {
	Primary   ModelRef   `json:"primary"`
	Fallbacks []ModelRef `json:"fallbacks,omitempty"`
}

// NewChain creates a new chain with the given primary model.
func NewChain(provider, model string) *Chain {
	return &Chain{
		Primary: ModelRef{Provider: provider, Model: model},
	}
}

// NewChainWithDefaults creates a chain with sensible defaults for compaction.
// Uses Claude Haiku 4.5 as primary with fallbacks from multiple providers.
// All providers configured in providers.json can be used for compaction.
func NewChainWithDefaults() *Chain {
	return &Chain{
		Primary: ModelRef{Provider: "Anthropic", Model: "claude-haiku-4-5-20251001"},
		Fallbacks: []ModelRef{
			{Provider: "Anthropic", Model: "claude-sonnet-4-6"},
			{Provider: "Cerebras", Model: "llama-3.3-70b"},
		},
	}
}

// AddFallback appends a fallback model to the chain.
func (c *Chain) AddFallback(provider, model string) {
	c.Fallbacks = append(c.Fallbacks, ModelRef{Provider: provider, Model: model})
}

// InsertFallback inserts a fallback at the specified index.
// If index is out of bounds, the fallback is appended.
func (c *Chain) InsertFallback(index int, provider, model string) {
	ref := ModelRef{Provider: provider, Model: model}
	if index < 0 || index >= len(c.Fallbacks) {
		c.Fallbacks = append(c.Fallbacks, ref)
		return
	}
	// Grow slice by one and shift elements after index
	c.Fallbacks = append(c.Fallbacks, ModelRef{})
	copy(c.Fallbacks[index+1:], c.Fallbacks[index:])
	c.Fallbacks[index] = ref
}

// RemoveFallback removes a fallback at the specified index.
// Returns error if index is out of bounds.
func (c *Chain) RemoveFallback(index int) error {
	if index < 0 || index >= len(c.Fallbacks) {
		return fmt.Errorf("invalid fallback index: %d (have %d fallbacks)", index, len(c.Fallbacks))
	}
	c.Fallbacks = append(c.Fallbacks[:index], c.Fallbacks[index+1:]...)
	return nil
}

// UpdateFallback updates the fallback at the specified index.
// Returns error if index is out of bounds.
func (c *Chain) UpdateFallback(index int, provider, model string) error {
	if index < 0 || index >= len(c.Fallbacks) {
		return fmt.Errorf("invalid fallback index: %d (have %d fallbacks)", index, len(c.Fallbacks))
	}
	c.Fallbacks[index] = ModelRef{Provider: provider, Model: model}
	return nil
}

// SetPrimary updates the primary model.
func (c *Chain) SetPrimary(provider, model string) {
	c.Primary = ModelRef{Provider: provider, Model: model}
}

// MoveFallback moves a fallback up or down in the chain.
// direction: -1 to move up, +1 to move down
func (c *Chain) MoveFallback(index, direction int) error {
	if index < 0 || index >= len(c.Fallbacks) {
		return fmt.Errorf("invalid fallback index: %d (have %d fallbacks)", index, len(c.Fallbacks))
	}
	newIndex := index + direction
	if newIndex < 0 || newIndex >= len(c.Fallbacks) {
		return fmt.Errorf("cannot move fallback to index %d", newIndex)
	}
	c.Fallbacks[index], c.Fallbacks[newIndex] = c.Fallbacks[newIndex], c.Fallbacks[index]
	return nil
}

// All returns all models in the chain (primary + fallbacks) in order.
func (c *Chain) All() []ModelRef {
	if c.IsEmpty() {
		return nil
	}
	all := make([]ModelRef, 0, 1+len(c.Fallbacks))
	all = append(all, c.Primary)
	all = append(all, c.Fallbacks...)
	return all
}

// Get returns the model at the specified index.
// Index 0 is the primary, 1+ are fallbacks.
func (c *Chain) Get(index int) (ModelRef, bool) {
	if index == 0 && !c.Primary.IsEmpty() {
		return c.Primary, true
	}
	fallbackIdx := index - 1
	if fallbackIdx >= 0 && fallbackIdx < len(c.Fallbacks) {
		return c.Fallbacks[fallbackIdx], true
	}
	return ModelRef{}, false
}

// Len returns the total number of models (primary + fallbacks).
func (c *Chain) Len() int {
	if c.IsEmpty() {
		return 0
	}
	return 1 + len(c.Fallbacks)
}

// IsEmpty returns true if the chain has no primary model configured.
func (c *Chain) IsEmpty() bool {
	return c.Primary.IsEmpty()
}

// Contains checks if the chain contains the specified provider/model.
func (c *Chain) Contains(provider, model string) bool {
	key := strings.ToLower(strings.TrimSpace(provider)) + "::" + strings.ToLower(strings.TrimSpace(model))
	for _, ref := range c.All() {
		if ref.Key() == key {
			return true
		}
	}
	return false
}

// Clone creates a deep copy of the chain.
func (c *Chain) Clone() *Chain {
	if c == nil {
		return nil
	}
	clone := &Chain{
		Primary:   c.Primary,
		Fallbacks: make([]ModelRef, len(c.Fallbacks)),
	}
	copy(clone.Fallbacks, c.Fallbacks)
	return clone
}

// ValidationResult contains validation info for a model in the chain.
type ValidationResult struct {
	Ref         ModelRef
	Index       int // 0 = primary, 1+ = fallback index
	IsPrimary   bool
	Valid       bool   // Model exists in provider config
	Available   bool   // Provider has credentials
	DisplayName string // Human-readable name for UI display
	Error       error  // Validation error if any
}

// ProviderInfo contains provider information for validation.
type ProviderInfo struct {
	Name        string
	DisplayName string
	Available   bool
	Models      []ModelInfo
}

// ModelInfo contains model information for validation.
type ModelInfo struct {
	ID          string
	DisplayName string
	Context     string
}

// ProviderValidator is a function that returns provider info for validation.
type ProviderValidator func(providerName string) *ProviderInfo

// Validate checks if all models in the chain exist and are available.
func (c *Chain) Validate(getProvider ProviderValidator) []ValidationResult {
	if c.IsEmpty() {
		return []ValidationResult{{
			Ref:       c.Primary,
			Index:     0,
			IsPrimary: true,
			Valid:     false,
			Error:     fmt.Errorf("chain is empty"),
		}}
	}

	var results []ValidationResult
	for i, ref := range c.All() {
		result := ValidationResult{
			Ref:       ref,
			Index:     i,
			IsPrimary: i == 0,
		}

		provider := getProvider(ref.Provider)
		if provider == nil {
			result.Valid = false
			result.Error = fmt.Errorf("provider not found: %s", ref.Provider)
			results = append(results, result)
			continue
		}

		// Find model in provider
		var model *ModelInfo
		for j := range provider.Models {
			if provider.Models[j].ID == ref.Model {
				model = &provider.Models[j]
				break
			}
		}

		if model == nil {
			result.Valid = false
			result.Error = fmt.Errorf("model not found: %s", ref.Model)
			results = append(results, result)
			continue
		}

		result.Valid = true
		result.Available = provider.Available
		result.DisplayName = fmt.Sprintf("%s / %s", provider.DisplayName, model.DisplayName)
		results = append(results, result)
	}

	return results
}

// FirstAvailable returns the first model in the chain that is both valid and available.
// Returns nil if no model is available.
func (c *Chain) FirstAvailable(getProvider ProviderValidator) *ModelRef {
	validations := c.Validate(getProvider)
	for _, v := range validations {
		if v.Valid && v.Available {
			ref := v.Ref
			return &ref
		}
	}
	return nil
}
