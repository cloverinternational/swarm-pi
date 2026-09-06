package commands

import (
	"sort"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// loadProvidersFromConfig loads providers from JSON config
func loadProvidersFromConfig() []Provider {
	cm, err := NewConfigManager()
	if err != nil {
		// Fallback to empty list on error
		return []Provider{}
	}

	providerConfigs, err := cm.LoadProviders()
	if err != nil {
		return []Provider{}
	}

	providers := make([]Provider, len(providerConfigs))
	for i, pc := range providerConfigs {
		models := make([]ModelInfo, len(pc.Models))
		for j, mc := range pc.Models {
			models[j] = ModelInfo{
				ID:                      mc.ID,
				DisplayName:             mc.DisplayName,
				Context:                 mc.Context,
				ContextWindow:           mc.ContextWindow,
				Description:             mc.Description,
				SupportsReasoningEffort: cloneBoolPtr(mc.SupportsReasoningEffort),
				ReasoningEfforts:        cloneStringSlice(mc.ReasoningEfforts),
			}
		}
		providers[i] = Provider{
			Name:        pc.Name,
			DisplayName: pc.DisplayName,
			Color:       pc.Color,
			Type:        pc.Type,
			APIType:     pc.APIType,
			BaseURL:     pc.BaseURL,
			Source:      pc.Source,
			Models:      models,
			Available:   pc.Available,
		}
	}

	return providers
}

func matchesProviderFilter(filter string, providerName string, providerDisplay string) bool {
	var normalized string = strings.ToLower(strings.TrimSpace(filter))
	if normalized == "" {
		return true
	}
	var name string = strings.ToLower(strings.TrimSpace(providerName))
	var display string = strings.ToLower(strings.TrimSpace(providerDisplay))
	return strings.Contains(name, normalized) || strings.Contains(display, normalized)
}

func matchesContextRange(context int, min int, max int) bool {
	if min == 0 && max == 0 {
		return true
	}
	if context <= 0 {
		return false
	}
	if min > 0 && context < min {
		return false
	}
	if max > 0 && context > max {
		return false
	}
	return true
}

func matchesTags(tags map[ModelTag]bool, required map[ModelTag]bool) bool {
	if len(required) == 0 {
		return true
	}
	for tag := range required {
		if !tags[tag] {
			return false
		}
	}
	return true
}

func matchCandidate(query string, candidate string, label string) (int, []int, bool) {
	if strings.TrimSpace(query) == "" {
		return 0, nil, true
	}
	var score int
	var ok bool
	score, _, ok = FuzzyMatchTokens(query, candidate)
	if !ok {
		return 0, nil, false
	}
	if label == "" {
		return score, nil, true
	}
	var highlight []int
	_, highlight, _ = FuzzyMatchTokens(query, label)
	return score, highlight, true
}

func aliasEntryContext(variants []ModelAliasVariant) int {
	var maxContext int
	for _, variant := range variants {
		var context int = ContextFromModel(variant.Model)
		if context > maxContext {
			maxContext = context
		}
	}
	return maxContext
}

func aliasEntryTags(variants []ModelAliasVariant) map[ModelTag]bool {
	var tags map[ModelTag]bool = make(map[ModelTag]bool)
	for _, variant := range variants {
		var variantTags map[ModelTag]bool = InferModelTags(variant.ProviderName, variant.Model)
		for tag := range variantTags {
			tags[tag] = true
		}
	}
	return tags
}

func (m *ModelCommand) buildGroupedResults(groups []modelGroup, scores map[int]int, highlights map[int][]int) groupedResults {
	var flattened []int = make([]int, 0)
	for i := range groups {
		var collapsed bool = m.collapsedGroups[groups[i].Key]
		groups[i].Collapsed = collapsed
		if collapsed {
			continue
		}
		flattened = append(flattened, groups[i].Indices...)
	}
	return groupedResults{
		Groups:     groups,
		Indices:    flattened,
		Scores:     scores,
		Highlights: highlights,
	}
}

// getAllModels returns all models from all providers
func (m *ModelCommand) getAllModels() []AllModelEntry {
	var allModels []AllModelEntry
	for _, prov := range m.providers {
		for _, model := range prov.Models {
			allModels = append(allModels, AllModelEntry{
				ProviderName: prov.Name,
				Provider:     prov,
				Model:        model,
			})
		}
	}
	return allModels
}

func (m *ModelCommand) providerResults() listResults {
	var parsed ParsedSearch = ParseSearchQuery(m.searchQuery)
	var searchText string = parsed.Text
	var providerFilter string = parsed.Filters.Provider
	var indices []int = make([]int, 0, len(m.providers))
	var scores map[int]int = make(map[int]int)
	var highlights map[int][]int = make(map[int][]int)
	for i, prov := range m.providers {
		if !matchesProviderFilter(providerFilter, prov.Name, prov.DisplayName) {
			continue
		}
		var label string = prov.DisplayName
		if label == "" {
			label = prov.Name
		}
		var candidateParts []string = []string{
			prov.DisplayName,
			prov.Name,
			prov.APIType,
			prov.BaseURL,
		}
		var candidate string = strings.Join(candidateParts, " ")
		var score int
		var highlight []int
		var ok bool
		score, highlight, ok = matchCandidate(searchText, candidate, label)
		if !ok {
			continue
		}
		indices = append(indices, i)
		scores[i] = score
		if len(highlight) > 0 {
			highlights[i] = highlight
		}
	}
	if strings.TrimSpace(searchText) != "" {
		sort.Slice(indices, func(a int, b int) bool {
			var left int = indices[a]
			var right int = indices[b]
			if scores[left] != scores[right] {
				return scores[left] > scores[right]
			}
			var leftLabel string = strings.ToLower(strings.TrimSpace(m.providers[left].DisplayName))
			if leftLabel == "" {
				leftLabel = strings.ToLower(strings.TrimSpace(m.providers[left].Name))
			}
			var rightLabel string = strings.ToLower(strings.TrimSpace(m.providers[right].DisplayName))
			if rightLabel == "" {
				rightLabel = strings.ToLower(strings.TrimSpace(m.providers[right].Name))
			}
			return leftLabel < rightLabel
		})
	}
	return listResults{Indices: indices, Scores: scores, Highlights: highlights}
}

func (m *ModelCommand) aliasResults() listResults {
	var searchText string
	var filters SearchFilters
	searchText, filters = m.effectiveSearch()
	var indices []int = make([]int, 0, len(m.aliasEntries))
	var scores map[int]int = make(map[int]int)
	var highlights map[int][]int = make(map[int][]int)
	var recommendedOnly bool = m.recommendedOnly()

	for i, alias := range m.aliasEntries {
		var variants []ModelAliasVariant = alias.Variants
		if len(variants) == 0 {
			continue
		}
		if filters.Provider != "" {
			var filteredVariants []ModelAliasVariant = make([]ModelAliasVariant, 0, len(variants))
			for _, variant := range variants {
				if matchesProviderFilter(filters.Provider, variant.ProviderName, variant.ProviderDisplayName) {
					filteredVariants = append(filteredVariants, variant)
				}
			}
			variants = filteredVariants
			if len(variants) == 0 {
				continue
			}
		}

		if recommendedOnly {
			var hasRecommended bool
			for _, variant := range variants {
				if m.isFavorite(variant.ProviderName, variant.Model.ID) || m.isRecent(variant.ProviderName, variant.Model.ID) {
					hasRecommended = true
					break
				}
			}
			if !hasRecommended {
				continue
			}
		}

		var context int = aliasEntryContext(variants)
		if !matchesContextRange(context, filters.ContextMin, filters.ContextMax) {
			continue
		}
		var tags map[ModelTag]bool = aliasEntryTags(variants)
		if !matchesTags(tags, filters.Tags) {
			continue
		}

		var label string = alias.DisplayName
		if label == "" {
			label = alias.Name
		}
		var candidateParts []string = make([]string, 0, 2+len(variants)*4)
		candidateParts = append(candidateParts, alias.DisplayName, alias.Name)
		for _, variant := range variants {
			candidateParts = append(candidateParts, variant.ProviderDisplayName, variant.ProviderName)
			candidateParts = append(candidateParts, variant.Model.DisplayName, variant.Model.ID)
		}
		var candidate string = strings.Join(candidateParts, " ")
		var score int
		var highlight []int
		var ok bool
		score, highlight, ok = matchCandidate(searchText, candidate, label)
		if !ok {
			continue
		}
		indices = append(indices, i)
		scores[i] = score
		if len(highlight) > 0 {
			highlights[i] = highlight
		}
	}

	if strings.TrimSpace(searchText) != "" {
		sort.Slice(indices, func(a int, b int) bool {
			var left int = indices[a]
			var right int = indices[b]
			if scores[left] != scores[right] {
				return scores[left] > scores[right]
			}
			var leftLabel string = strings.ToLower(strings.TrimSpace(m.aliasEntries[left].DisplayName))
			if leftLabel == "" {
				leftLabel = strings.ToLower(strings.TrimSpace(m.aliasEntries[left].Name))
			}
			var rightLabel string = strings.ToLower(strings.TrimSpace(m.aliasEntries[right].DisplayName))
			if rightLabel == "" {
				rightLabel = strings.ToLower(strings.TrimSpace(m.aliasEntries[right].Name))
			}
			return leftLabel < rightLabel
		})
	}
	return listResults{Indices: indices, Scores: scores, Highlights: highlights}
}

func (m *ModelCommand) providerModelResults(providerIdx int) groupedResults {
	if providerIdx < 0 || providerIdx >= len(m.providers) {
		return groupedResults{}
	}
	var provider Provider = m.providers[providerIdx]
	var searchText string
	var filters SearchFilters
	searchText, filters = m.effectiveSearch()
	filters.Provider = ""

	var indices []int = make([]int, 0, len(provider.Models))
	var scores map[int]int = make(map[int]int)
	var highlights map[int][]int = make(map[int][]int)
	var recommendedOnly bool = m.recommendedOnly()

	for i, model := range provider.Models {
		var context int = ContextFromModel(model)
		if !matchesContextRange(context, filters.ContextMin, filters.ContextMax) {
			continue
		}
		var tags map[ModelTag]bool = InferModelTags(provider.Name, model)
		if !matchesTags(tags, filters.Tags) {
			continue
		}
		if recommendedOnly && !m.isFavorite(provider.Name, model.ID) && !m.isRecent(provider.Name, model.ID) {
			continue
		}

		var label string = model.DisplayName
		if label == "" {
			label = model.ID
		}
		var candidateParts []string = []string{
			model.DisplayName,
			model.ID,
			model.Context,
			provider.DisplayName,
			provider.Name,
		}
		if model.ContextWindow > 0 {
			candidateParts = append(candidateParts, formatContextWindow(model.ContextWindow))
		}
		var candidate string = strings.Join(candidateParts, " ")
		var score int
		var highlight []int
		var ok bool
		score, highlight, ok = matchCandidate(searchText, candidate, label)
		if !ok {
			continue
		}
		indices = append(indices, i)
		scores[i] = score
		if len(highlight) > 0 {
			highlights[i] = highlight
		}
	}

	if strings.TrimSpace(searchText) != "" {
		sort.Slice(indices, func(a int, b int) bool {
			var left int = indices[a]
			var right int = indices[b]
			if scores[left] != scores[right] {
				return scores[left] > scores[right]
			}
			var leftLabel string = strings.ToLower(strings.TrimSpace(provider.Models[left].DisplayName))
			if leftLabel == "" {
				leftLabel = strings.ToLower(strings.TrimSpace(provider.Models[left].ID))
			}
			var rightLabel string = strings.ToLower(strings.TrimSpace(provider.Models[right].DisplayName))
			if rightLabel == "" {
				rightLabel = strings.ToLower(strings.TrimSpace(provider.Models[right].ID))
			}
			return leftLabel < rightLabel
		})
	}

	var favorites []int = make([]int, 0)
	var recents []int = make([]int, 0)
	var used map[int]bool = make(map[int]bool)
	for _, idx := range indices {
		var model ModelInfo = provider.Models[idx]
		if m.isFavorite(provider.Name, model.ID) {
			favorites = append(favorites, idx)
			used[idx] = true
			continue
		}
		if m.isRecent(provider.Name, model.ID) {
			recents = append(recents, idx)
			used[idx] = true
		}
	}

	var groups []modelGroup = make([]modelGroup, 0)
	if len(favorites) > 0 {
		groups = append(groups, modelGroup{
			Key:     "favorites:" + strings.ToLower(provider.Name),
			Title:   i18n.T("commands_b.model.group_favorites"),
			Indices: favorites,
		})
	}
	if len(recents) > 0 {
		groups = append(groups, modelGroup{
			Key:     "recents:" + strings.ToLower(provider.Name),
			Title:   i18n.T("commands_b.model.group_recent"),
			Indices: recents,
		})
	}

	if !recommendedOnly {
		var aliasOrder []string = make([]string, 0)
		var aliasTitle map[string]string = make(map[string]string)
		var aliasByModelID map[string]string = make(map[string]string)
		for _, alias := range m.aliasEntries {
			var aliasKey string = "alias:" + strings.ToLower(provider.Name) + ":" + strings.ToLower(strings.TrimSpace(alias.Name))
			if aliasKey == "alias:"+strings.ToLower(provider.Name)+":" {
				aliasKey = "alias:" + strings.ToLower(provider.Name) + ":" + strings.ToLower(strings.TrimSpace(alias.DisplayName))
			}
			var hasProvider bool
			for _, variant := range alias.Variants {
				if strings.EqualFold(variant.ProviderName, provider.Name) {
					aliasByModelID[variant.Model.ID] = aliasKey
					hasProvider = true
				}
			}
			if hasProvider {
				var title string = alias.DisplayName
				if title == "" {
					title = alias.Name
				}
				aliasOrder = append(aliasOrder, aliasKey)
				aliasTitle[aliasKey] = title
			}
		}

		var aliasGroups map[string][]int = make(map[string][]int)
		var other []int = make([]int, 0)
		for _, idx := range indices {
			if used[idx] {
				continue
			}
			var model ModelInfo = provider.Models[idx]
			var aliasKey string = aliasByModelID[model.ID]
			if aliasKey != "" {
				aliasGroups[aliasKey] = append(aliasGroups[aliasKey], idx)
				continue
			}
			other = append(other, idx)
		}

		for _, aliasKey := range aliasOrder {
			var groupIndices []int = aliasGroups[aliasKey]
			if len(groupIndices) == 0 {
				continue
			}
			var title string = aliasTitle[aliasKey]
			if title == "" {
				title = i18n.T("commands_b.model.group_models")
			}
			groups = append(groups, modelGroup{
				Key:     aliasKey,
				Title:   title,
				Indices: groupIndices,
			})
		}

		if len(other) > 0 {
			groups = append(groups, modelGroup{
				Key:     "other:" + strings.ToLower(provider.Name),
				Title:   i18n.T("commands_b.model.group_other"),
				Indices: other,
			})
		}
	}

	if len(groups) == 0 {
		groups = append(groups, modelGroup{
			Key:     "models:" + strings.ToLower(provider.Name),
			Title:   i18n.T("commands_b.model.group_models"),
			Indices: indices,
		})
	}
	return m.buildGroupedResults(groups, scores, highlights)
}

func (m *ModelCommand) aliasVariantResults(aliasIdx int) groupedResults {
	if aliasIdx < 0 || aliasIdx >= len(m.aliasEntries) {
		return groupedResults{}
	}
	var alias ModelAliasEntry = m.aliasEntries[aliasIdx]
	var variants []ModelAliasVariant = alias.Variants

	var searchText string
	var filters SearchFilters
	searchText, filters = m.effectiveSearch()
	var indices []int = make([]int, 0, len(variants))
	var scores map[int]int = make(map[int]int)
	var highlights map[int][]int = make(map[int][]int)
	var recommendedOnly bool = m.recommendedOnly()

	for i, variant := range variants {
		if !matchesProviderFilter(filters.Provider, variant.ProviderName, variant.ProviderDisplayName) {
			continue
		}
		var context int = ContextFromModel(variant.Model)
		if !matchesContextRange(context, filters.ContextMin, filters.ContextMax) {
			continue
		}
		var tags map[ModelTag]bool = InferModelTags(variant.ProviderName, variant.Model)
		if !matchesTags(tags, filters.Tags) {
			continue
		}
		if recommendedOnly && !m.isFavorite(variant.ProviderName, variant.Model.ID) && !m.isRecent(variant.ProviderName, variant.Model.ID) {
			continue
		}

		var label string = variant.ProviderDisplayName
		if label == "" {
			label = variant.ProviderName
		}
		var candidateParts []string = []string{
			label,
			variant.ProviderName,
			variant.Model.DisplayName,
			variant.Model.ID,
			variant.Model.Context,
		}
		if variant.Model.ContextWindow > 0 {
			candidateParts = append(candidateParts, formatContextWindow(variant.Model.ContextWindow))
		}
		var candidate string = strings.Join(candidateParts, " ")
		var score int
		var highlight []int
		var ok bool
		score, highlight, ok = matchCandidate(searchText, candidate, label)
		if !ok {
			continue
		}
		indices = append(indices, i)
		scores[i] = score
		if len(highlight) > 0 {
			highlights[i] = highlight
		}
	}

	if strings.TrimSpace(searchText) != "" {
		sort.Slice(indices, func(a int, b int) bool {
			var left int = indices[a]
			var right int = indices[b]
			if scores[left] != scores[right] {
				return scores[left] > scores[right]
			}
			var leftLabel string = strings.ToLower(strings.TrimSpace(variants[left].ProviderDisplayName))
			if leftLabel == "" {
				leftLabel = strings.ToLower(strings.TrimSpace(variants[left].ProviderName))
			}
			var rightLabel string = strings.ToLower(strings.TrimSpace(variants[right].ProviderDisplayName))
			if rightLabel == "" {
				rightLabel = strings.ToLower(strings.TrimSpace(variants[right].ProviderName))
			}
			return leftLabel < rightLabel
		})
	} else {
		sort.Slice(indices, func(a int, b int) bool {
			var left int = indices[a]
			var right int = indices[b]
			var leftLabel string = strings.ToLower(strings.TrimSpace(variants[left].ProviderDisplayName))
			if leftLabel == "" {
				leftLabel = strings.ToLower(strings.TrimSpace(variants[left].ProviderName))
			}
			var rightLabel string = strings.ToLower(strings.TrimSpace(variants[right].ProviderDisplayName))
			if rightLabel == "" {
				rightLabel = strings.ToLower(strings.TrimSpace(variants[right].ProviderName))
			}
			return leftLabel < rightLabel
		})
	}

	var favorites []int = make([]int, 0)
	var recents []int = make([]int, 0)
	var used map[int]bool = make(map[int]bool)
	for _, idx := range indices {
		var variant ModelAliasVariant = variants[idx]
		if m.isFavorite(variant.ProviderName, variant.Model.ID) {
			favorites = append(favorites, idx)
			used[idx] = true
			continue
		}
		if m.isRecent(variant.ProviderName, variant.Model.ID) {
			recents = append(recents, idx)
			used[idx] = true
		}
	}

	var groups []modelGroup = make([]modelGroup, 0)
	if len(favorites) > 0 {
		groups = append(groups, modelGroup{
			Key:     "favorites:alias:" + strings.ToLower(alias.Name),
			Title:   i18n.T("commands_b.model.group_favorites"),
			Indices: favorites,
		})
	}
	if len(recents) > 0 {
		groups = append(groups, modelGroup{
			Key:     "recents:alias:" + strings.ToLower(alias.Name),
			Title:   i18n.T("commands_b.model.group_recent"),
			Indices: recents,
		})
	}

	if !recommendedOnly {
		var available []int = make([]int, 0)
		var unavailable []int = make([]int, 0)
		for _, idx := range indices {
			if used[idx] {
				continue
			}
			if variants[idx].ProviderAvailable {
				available = append(available, idx)
			} else {
				unavailable = append(unavailable, idx)
			}
		}
		if len(available) > 0 {
			groups = append(groups, modelGroup{
				Key:     "available:alias:" + strings.ToLower(alias.Name),
				Title:   i18n.T("commands_b.model.group_configured"),
				Indices: available,
			})
		}
		if len(unavailable) > 0 {
			groups = append(groups, modelGroup{
				Key:     "unavailable:alias:" + strings.ToLower(alias.Name),
				Title:   i18n.T("commands_b.model.group_not_configured"),
				Indices: unavailable,
			})
		}
	}

	if len(groups) == 0 {
		groups = append(groups, modelGroup{
			Key:     "providers:alias:" + strings.ToLower(alias.Name),
			Title:   i18n.T("commands_b.model.group_providers"),
			Indices: indices,
		})
	}
	return m.buildGroupedResults(groups, scores, highlights)
}

func (m *ModelCommand) allModelResults(allModels []AllModelEntry) groupedResults {
	var searchText string
	var filters SearchFilters
	searchText, filters = m.effectiveSearch()
	var indices []int = make([]int, 0, len(allModels))
	var scores map[int]int = make(map[int]int)
	var highlights map[int][]int = make(map[int][]int)
	var recommendedOnly bool = m.recommendedOnly()

	for i, entry := range allModels {
		if !matchesProviderFilter(filters.Provider, entry.ProviderName, entry.Provider.DisplayName) {
			continue
		}
		var context int = ContextFromModel(entry.Model)
		if !matchesContextRange(context, filters.ContextMin, filters.ContextMax) {
			continue
		}
		var tags map[ModelTag]bool = InferModelTags(entry.ProviderName, entry.Model)
		if !matchesTags(tags, filters.Tags) {
			continue
		}
		if recommendedOnly && !m.isFavorite(entry.ProviderName, entry.Model.ID) && !m.isRecent(entry.ProviderName, entry.Model.ID) {
			continue
		}

		var label string = entry.Model.DisplayName
		if label == "" {
			label = entry.Model.ID
		}
		var candidateParts []string = []string{
			entry.Model.DisplayName,
			entry.Model.ID,
			entry.Model.Context,
			entry.Provider.DisplayName,
			entry.ProviderName,
		}
		if entry.Model.ContextWindow > 0 {
			candidateParts = append(candidateParts, formatContextWindow(entry.Model.ContextWindow))
		}
		var candidate string = strings.Join(candidateParts, " ")
		var score int
		var highlight []int
		var ok bool
		score, highlight, ok = matchCandidate(searchText, candidate, label)
		if !ok {
			continue
		}
		indices = append(indices, i)
		scores[i] = score
		if len(highlight) > 0 {
			highlights[i] = highlight
		}
	}

	if strings.TrimSpace(searchText) != "" {
		sort.Slice(indices, func(a int, b int) bool {
			var left int = indices[a]
			var right int = indices[b]
			if scores[left] != scores[right] {
				return scores[left] > scores[right]
			}
			var leftLabel string = strings.ToLower(strings.TrimSpace(allModels[left].Model.DisplayName))
			if leftLabel == "" {
				leftLabel = strings.ToLower(strings.TrimSpace(allModels[left].Model.ID))
			}
			var rightLabel string = strings.ToLower(strings.TrimSpace(allModels[right].Model.DisplayName))
			if rightLabel == "" {
				rightLabel = strings.ToLower(strings.TrimSpace(allModels[right].Model.ID))
			}
			return leftLabel < rightLabel
		})
	}

	var favorites []int = make([]int, 0)
	var recents []int = make([]int, 0)
	var used map[int]bool = make(map[int]bool)
	for _, idx := range indices {
		var entry AllModelEntry = allModels[idx]
		if m.isFavorite(entry.ProviderName, entry.Model.ID) {
			favorites = append(favorites, idx)
			used[idx] = true
			continue
		}
		if m.isRecent(entry.ProviderName, entry.Model.ID) {
			recents = append(recents, idx)
			used[idx] = true
		}
	}

	var includeFavRecent bool = recommendedOnly || (strings.TrimSpace(searchText) == "" && (len(favorites) > 0 || len(recents) > 0))
	var groups []modelGroup = make([]modelGroup, 0)
	if includeFavRecent {
		if len(favorites) > 0 {
			groups = append(groups, modelGroup{
				Key:     "favorites:all",
				Title:   i18n.T("commands_b.model.group_favorites"),
				Indices: favorites,
			})
		}
		if len(recents) > 0 {
			groups = append(groups, modelGroup{
				Key:     "recents:all",
				Title:   i18n.T("commands_b.model.group_recent"),
				Indices: recents,
			})
		}
	}

	if !recommendedOnly {
		var providerGroups map[string][]int = make(map[string][]int)
		for _, idx := range indices {
			if includeFavRecent && used[idx] {
				continue
			}
			var entry AllModelEntry = allModels[idx]
			providerGroups[entry.ProviderName] = append(providerGroups[entry.ProviderName], idx)
		}

		for _, provider := range m.providers {
			var groupIndices []int = providerGroups[provider.Name]
			if len(groupIndices) == 0 {
				continue
			}
			var title string = provider.DisplayName
			if title == "" {
				title = provider.Name
			}
			groups = append(groups, modelGroup{
				Key:     "provider:" + strings.ToLower(provider.Name),
				Title:   title,
				Indices: groupIndices,
			})
		}
	}

	if len(groups) == 0 {
		groups = append(groups, modelGroup{
			Key:     "models:all",
			Title:   i18n.T("commands_b.model.group_models"),
			Indices: indices,
		})
	}
	return m.buildGroupedResults(groups, scores, highlights)
}

func (m *ModelCommand) tabsEnabled() bool {
	switch m.state {
	case "model", "provider_models", "alias_variants":
		return true
	default:
		return false
	}
}

func (m *ModelCommand) normalizedTabIndex() int {
	if m.tabIndex < 0 || m.tabIndex >= len(modelTabs) {
		return TabAll
	}
	return m.tabIndex
}

func (m *ModelCommand) currentTab() (label string, contextMin int, tags []ModelTag) {
	tab := modelTabs[m.normalizedTabIndex()]
	return tab.label, tab.contextMin, tab.tags
}

func (m *ModelCommand) recommendedOnly() bool {
	return false
}

func (m *ModelCommand) contextFilterRange() (int, int, string) {
	switch m.filterContext {
	case ContextFilter32k:
		return 32000, 0, "32K+"
	case ContextFilter128k:
		return 128000, 0, "128K+"
	case ContextFilter1M:
		return 1000000, 0, "1M+"
	default:
		return 0, 0, i18n.T("commands_b.model.context_any")
	}
}

func (m *ModelCommand) effectiveSearch() (string, SearchFilters) {
	parsed := ParseSearchQuery(m.searchQuery)
	filters := SearchFilters{Tags: make(map[ModelTag]bool)}
	filters.Provider = parsed.Filters.Provider
	if filters.Provider == "" {
		filters.Provider = m.filterProvider
	}
	for tag := range m.filterTags {
		filters.Tags[tag] = true
	}
	_, tabContext, tabTags := m.currentTab()
	if tabContext > 0 {
		filters.ContextMin = tabContext
	}
	for _, tag := range tabTags {
		filters.Tags[tag] = true
	}
	if parsed.Filters.ContextMin > 0 || parsed.Filters.ContextMax > 0 {
		filters.ContextMin = parsed.Filters.ContextMin
		filters.ContextMax = parsed.Filters.ContextMax
	} else if filters.ContextMin == 0 && filters.ContextMax == 0 {
		filters.ContextMin, filters.ContextMax, _ = m.contextFilterRange()
	}
	for tag := range parsed.Filters.Tags {
		filters.Tags[tag] = true
	}
	return parsed.Text, filters
}

func (m *ModelCommand) searchEnabled() bool {
	switch m.state {
	case "provider", "provider_models", "model", "alias_variants":
		return true
	default:
		return false
	}
}
