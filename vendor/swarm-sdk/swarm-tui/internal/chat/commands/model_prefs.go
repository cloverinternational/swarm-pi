package commands

import "strings"

// MaxRecentModels caps the size of the recent model list.
const MaxRecentModels = 12

// ModelRef identifies a provider/model pair.
type ModelRef struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// ModelRefKey returns a normalized key for provider/model pairs.
func ModelRefKey(provider, model string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "::" + strings.ToLower(strings.TrimSpace(model))
}

// AddRecentModels prepends a model to recents, dedupes, and caps the list.
func AddRecentModels(list []ModelRef, provider, model string, max int) []ModelRef {
	if max <= 0 {
		max = MaxRecentModels
	}
	key := ModelRefKey(provider, model)
	updated := make([]ModelRef, 0, len(list)+1)
	updated = append(updated, ModelRef{Provider: provider, Model: model})
	for _, ref := range list {
		if ModelRefKey(ref.Provider, ref.Model) == key {
			continue
		}
		updated = append(updated, ref)
		if len(updated) >= max {
			break
		}
	}
	if len(updated) > max {
		updated = updated[:max]
	}
	return updated
}

// ToggleFavoriteModels adds or removes a model from favorites.
func ToggleFavoriteModels(list []ModelRef, provider, model string) ([]ModelRef, bool) {
	key := ModelRefKey(provider, model)
	updated := make([]ModelRef, 0, len(list))
	removed := false
	for _, ref := range list {
		if ModelRefKey(ref.Provider, ref.Model) == key {
			removed = true
			continue
		}
		updated = append(updated, ref)
	}
	if removed {
		return updated, false
	}
	updated = append([]ModelRef{{Provider: provider, Model: model}}, updated...)
	return updated, true
}

// IndexOfModelRef returns the index of a model ref in the list, or -1.
func IndexOfModelRef(list []ModelRef, provider, model string) int {
	key := ModelRefKey(provider, model)
	for i, ref := range list {
		if ModelRefKey(ref.Provider, ref.Model) == key {
			return i
		}
	}
	return -1
}
