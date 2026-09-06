package commands

import (
	"sort"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/cloud"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ModelAliasVariant represents a provider/model option for an alias.
type ModelAliasVariant struct {
	ProviderName        string
	ProviderDisplayName string
	ProviderColor       string
	ProviderAvailable   bool
	Model               ModelInfo
}

// ModelAliasEntry represents a model alias with provider variants.
type ModelAliasEntry struct {
	Name        string
	DisplayName string
	Variants    []ModelAliasVariant
}

// LoadModelAliases loads catalog aliases and matches them to configured providers/models.
func LoadModelAliases(providers []Provider) []ModelAliasEntry {
	var document *cloud.CatalogDocument
	var err error
	document, _, err = cloud.LoadCatalogDocument()
	if err != nil || document == nil || len(document.Aliases) == 0 {
		return nil
	}

	var providerByName map[string]Provider = make(map[string]Provider, len(providers))
	for _, provider := range providers {
		var key string = strings.ToLower(strings.TrimSpace(provider.Name))
		if key == "" {
			continue
		}
		providerByName[key] = provider
	}

	var catalogProviderKeys map[string]string = make(map[string]string, len(document.Providers)*2)
	for _, provider := range document.Providers {
		var canonical string = strings.TrimSpace(catalogProviderName(provider))
		if canonical == "" {
			continue
		}
		var keys []string = []string{provider.ID, provider.Name, canonical}
		for _, key := range keys {
			var normalized string = strings.ToLower(strings.TrimSpace(key))
			if normalized == "" {
				continue
			}
			catalogProviderKeys[normalized] = canonical
		}
	}

	var aliases []ModelAliasEntry = make([]ModelAliasEntry, 0, len(document.Aliases))
	for _, alias := range document.Aliases {
		var aliasName string = strings.TrimSpace(alias.Name)
		if aliasName == "" {
			continue
		}
		var displayName string = strings.TrimSpace(alias.DisplayName)
		if displayName == "" {
			displayName = aliasName
		}

		var variants []ModelAliasVariant = make([]ModelAliasVariant, 0, len(alias.Variants))
		for _, variant := range alias.Variants {
			var providerKey string = strings.ToLower(strings.TrimSpace(variant.Provider))
			if providerKey == "" {
				continue
			}
			if canonical, ok := catalogProviderKeys[providerKey]; ok {
				providerKey = strings.ToLower(strings.TrimSpace(canonical))
			}

			var provider Provider
			var ok bool
			provider, ok = providerByName[providerKey]
			if !ok {
				continue
			}

			var model ModelInfo
			for _, candidate := range provider.Models {
				if candidate.ID == variant.Model {
					model = candidate
					break
				}
			}
			if model.ID == "" {
				if variant.Model == "" {
					continue
				}
				model = ModelInfo{
					ID:          variant.Model,
					DisplayName: variant.Model,
				}
			}

			variants = append(variants, ModelAliasVariant{
				ProviderName:        provider.Name,
				ProviderDisplayName: provider.DisplayName,
				ProviderColor:       provider.Color,
				ProviderAvailable:   provider.Available,
				Model:               model,
			})
		}

		if len(variants) == 0 {
			continue
		}

		aliases = append(aliases, ModelAliasEntry{
			Name:        aliasName,
			DisplayName: displayName,
			Variants:    variants,
		})
	}

	sort.Slice(aliases, func(i, j int) bool {
		return strings.ToLower(aliases[i].DisplayName) < strings.ToLower(aliases[j].DisplayName)
	})

	return aliases
}

// AliasRepresentativeModel picks a representative provider/model for a model alias.
// It prefers the current provider when available and favors variants with descriptions.
func AliasRepresentativeModel(alias ModelAliasEntry, preferredProvider string, providers []Provider) (Provider, ModelInfo, bool) {
	var variant ModelAliasVariant
	var ok bool
	variant, ok = aliasRepresentativeVariant(alias, preferredProvider)
	if !ok {
		return Provider{}, ModelInfo{}, false
	}
	return aliasVariantProvider(variant, providers), variant.Model, true
}

func aliasRepresentativeVariant(alias ModelAliasEntry, preferredProvider string) (ModelAliasVariant, bool) {
	if len(alias.Variants) == 0 {
		return ModelAliasVariant{}, false
	}

	var preferredIndex int = -1
	if strings.TrimSpace(preferredProvider) != "" {
		for idx := range alias.Variants {
			if strings.EqualFold(alias.Variants[idx].ProviderName, preferredProvider) {
				preferredIndex = idx
				break
			}
		}
	}

	if preferredIndex >= 0 {
		var preferred ModelAliasVariant = alias.Variants[preferredIndex]
		if strings.TrimSpace(preferred.Model.Description) != "" {
			return preferred, true
		}
	}

	for idx := range alias.Variants {
		var candidate ModelAliasVariant = alias.Variants[idx]
		if candidate.ProviderAvailable && strings.TrimSpace(candidate.Model.Description) != "" {
			return candidate, true
		}
	}

	for idx := range alias.Variants {
		var candidate ModelAliasVariant = alias.Variants[idx]
		if strings.TrimSpace(candidate.Model.Description) != "" {
			return candidate, true
		}
	}

	if preferredIndex >= 0 {
		return alias.Variants[preferredIndex], true
	}

	for idx := range alias.Variants {
		var candidate ModelAliasVariant = alias.Variants[idx]
		if candidate.ProviderAvailable {
			return candidate, true
		}
	}

	return alias.Variants[0], true
}

func aliasVariantProvider(variant ModelAliasVariant, providers []Provider) Provider {
	var provider Provider = Provider{
		Name:        variant.ProviderName,
		DisplayName: variant.ProviderDisplayName,
		Color:       variant.ProviderColor,
		Available:   variant.ProviderAvailable,
	}
	for idx := range providers {
		if strings.EqualFold(providers[idx].Name, variant.ProviderName) {
			if provider.DisplayName == "" {
				provider.DisplayName = providers[idx].DisplayName
			}
			provider.Color = providers[idx].Color
			provider.Type = providers[idx].Type
			provider.APIType = providers[idx].APIType
			provider.BaseURL = providers[idx].BaseURL
			provider.Available = providers[idx].Available
			provider.Source = providers[idx].Source
			break
		}
	}
	return provider
}

// AliasContextLabel returns a normalized context label for alias variants.
func AliasContextLabel(variants []ModelAliasVariant) string {
	var seen map[string]struct{} = make(map[string]struct{})
	var label string
	for _, variant := range variants {
		label = strings.TrimSpace(variant.Model.Context)
		if label == "" && variant.Model.ContextWindow > 0 {
			label = formatContextWindow(variant.Model.ContextWindow)
		}
		if label == "" {
			continue
		}
		seen[label] = struct{}{}
	}

	if len(seen) == 0 {
		return ""
	}
	if len(seen) == 1 {
		for key := range seen {
			return key
		}
	}
	return i18n.T("commands_b.model.context_varies")
}
