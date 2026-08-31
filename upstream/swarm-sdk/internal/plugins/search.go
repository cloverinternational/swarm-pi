package plugins

import (
	"context"
	"sort"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// UnifiedSearcher provides unified search across skills and plugins.
type UnifiedSearcher struct {
	// Skills loader for skill search
	skillsLoader *skills.Loader

	// Plugins loader for plugin search
	pluginsLoader *Loader

	// Marketplace database for remote search
	marketplace *MarketplaceDatabase
}

// NewUnifiedSearcher creates a new unified searcher.
func NewUnifiedSearcher(skillsLoader *skills.Loader, pluginsLoader *Loader, marketplace *MarketplaceDatabase) *UnifiedSearcher {
	return &UnifiedSearcher{
		skillsLoader:  skillsLoader,
		pluginsLoader: pluginsLoader,
		marketplace:   marketplace,
	}
}

// Search performs a unified search across skills and plugins.
func (s *UnifiedSearcher) Search(ctx context.Context, filter SearchFilter) ([]UnifiedSearchResult, error) {
	var results []UnifiedSearchResult

	// Determine which types to search
	searchSkills := len(filter.Types) == 0 || containsType(filter.Types, "skill")
	searchPlugins := len(filter.Types) == 0 || containsType(filter.Types, "plugin")

	// Search local skills
	if searchSkills && s.skillsLoader != nil {
		skillResults := s.searchSkills(ctx, filter)
		results = append(results, skillResults...)
	}

	// Search local plugins
	if searchPlugins && s.pluginsLoader != nil {
		pluginResults := s.searchLocalPlugins(ctx, filter)
		results = append(results, pluginResults...)
	}

	// Search marketplace for both skills and plugins
	if !filter.InstalledOnly {
		if searchPlugins && s.marketplace != nil {
			marketplaceResults := s.searchMarketplace(ctx, filter)
			// Merge, avoiding duplicates with local results
			results = mergeResults(results, marketplaceResults)
		}
	}

	// Apply sorting
	results = s.sortResults(results, filter)

	// Apply limit
	if filter.Limit > 0 && len(results) > filter.Limit {
		results = results[:filter.Limit]
	}

	return results, nil
}

// searchSkills searches local skills.
func (s *UnifiedSearcher) searchSkills(ctx context.Context, filter SearchFilter) []UnifiedSearchResult {
	if s.skillsLoader == nil {
		return nil
	}

	// Get skill search results
	skillResults, err := s.skillsLoader.Search(ctx, filter.Query)
	if err != nil {
		return nil
	}

	var results []UnifiedSearchResult
	for _, sr := range skillResults {
		// Apply filters
		if !s.matchesFilter(sr, filter) {
			continue
		}

		result := UnifiedSearchResult{
			ResultType:     "skill",
			Name:           sr.Name,
			Description:    sr.Description,
			Version:        sr.Version,
			Author:         sr.Author,
			Category:       sr.Category,
			Tags:           sr.Tags,
			Source:         sr.Source,
			Installed:      sr.Installed,
			Enabled:        sr.Installed, // Skills are enabled if installed
			Downloads:      sr.Downloads,
			Rating:         sr.Rating,
			ScriptCount:    0, // Would need to load skill to get these
			ReferenceCount: 0,
		}

		// Get additional skill details
		if skill, ok := s.skillsLoader.Registry.Get(sr.Name); ok {
			result.ScriptCount = len(skill.Scripts)
			result.ReferenceCount = len(skill.References)
			result.Enabled = s.skillsLoader.Registry.IsActive(sr.Name)
		}

		results = append(results, result)
	}

	return results
}

// searchLocalPlugins searches locally installed plugins.
func (s *UnifiedSearcher) searchLocalPlugins(ctx context.Context, filter SearchFilter) []UnifiedSearchResult {
	if s.pluginsLoader == nil {
		return nil
	}

	pluginResults := s.pluginsLoader.Search(filter.Query)

	var results []UnifiedSearchResult
	for _, pr := range pluginResults {
		// Apply filters
		if !s.matchesPluginFilter(pr, filter) {
			continue
		}

		results = append(results, UnifiedSearchResult{
			ResultType:   "plugin",
			Name:         pr.Name,
			Description:  pr.Description,
			Version:      pr.Version,
			Author:       pr.Author,
			Category:     pr.Category,
			Tags:         pr.Keywords,
			Source:       pr.Source,
			Installed:    pr.Installed,
			Enabled:      pr.Enabled,
			CommandCount: pr.CommandCount,
			AgentCount:   pr.AgentCount,
			SkillCount:   pr.SkillCount,
			HasMCP:       pr.HasMCP,
			HasLSP:       pr.HasLSP,
			Downloads:    pr.Downloads,
			Rating:       pr.Rating,
			Verified:     pr.Verified,
			Featured:     pr.Featured,
		})
	}

	return results
}

// searchMarketplace searches the marketplace database.
func (s *UnifiedSearcher) searchMarketplace(ctx context.Context, filter SearchFilter) []UnifiedSearchResult {
	if s.marketplace == nil {
		return nil
	}

	marketplaceResults := s.marketplace.Search(filter.Query)

	var results []UnifiedSearchResult
	for _, mr := range marketplaceResults {
		// Apply filters
		if !s.matchesMarketplaceFilter(mr, filter) {
			continue
		}

		// Check if installed
		installed := false
		enabled := false
		if s.pluginsLoader != nil {
			if p := s.pluginsLoader.Get(mr.Name); p != nil {
				installed = true
				enabled = p.Enabled
			}
		}

		results = append(results, UnifiedSearchResult{
			ResultType:  "plugin",
			Name:        mr.Name,
			Description: mr.Description,
			Version:     mr.Version,
			Author:      mr.Author,
			Category:    mr.Category,
			Tags:        mr.Tags,
			Source:      mr.Marketplace,
			Installed:   installed,
			Enabled:     enabled,
			Downloads:   mr.Downloads,
			Rating:      mr.Rating,
			Verified:    mr.Verified,
			Featured:    mr.Featured,
		})
	}

	return results
}

// matchesFilter checks if a skill result matches the filter.
func (s *UnifiedSearcher) matchesFilter(sr skills.SkillSearchResult, filter SearchFilter) bool {
	// Category filter
	if len(filter.Categories) > 0 && !containsIgnoreCase(filter.Categories, sr.Category) {
		return false
	}

	// Tags filter
	if len(filter.Tags) > 0 {
		hasTag := false
		for _, tag := range sr.Tags {
			if containsIgnoreCase(filter.Tags, tag) {
				hasTag = true
				break
			}
		}
		if !hasTag {
			return false
		}
	}

	// Installed only filter
	if filter.InstalledOnly && !sr.Installed {
		return false
	}

	return true
}

// matchesPluginFilter checks if a plugin result matches the filter.
func (s *UnifiedSearcher) matchesPluginFilter(pr PluginSearchResult, filter SearchFilter) bool {
	// Category filter
	if len(filter.Categories) > 0 && !containsIgnoreCase(filter.Categories, pr.Category) {
		return false
	}

	// Tags filter
	if len(filter.Tags) > 0 {
		hasTag := false
		for _, tag := range pr.Keywords {
			if containsIgnoreCase(filter.Tags, tag) {
				hasTag = true
				break
			}
		}
		if !hasTag {
			return false
		}
	}

	// Installed only filter
	if filter.InstalledOnly && !pr.Installed {
		return false
	}

	// Enabled only filter
	if filter.EnabledOnly && !pr.Enabled {
		return false
	}

	// Featured only filter
	if filter.FeaturedOnly && !pr.Featured {
		return false
	}

	// Verified only filter
	if filter.VerifiedOnly && !pr.Verified {
		return false
	}

	return true
}

// matchesMarketplaceFilter checks if a marketplace entry matches the filter.
func (s *UnifiedSearcher) matchesMarketplaceFilter(mr PluginMarketplaceEntry, filter SearchFilter) bool {
	// Category filter
	if len(filter.Categories) > 0 && !containsIgnoreCase(filter.Categories, mr.Category) {
		return false
	}

	// Tags filter
	if len(filter.Tags) > 0 {
		hasTag := false
		for _, tag := range mr.Tags {
			if containsIgnoreCase(filter.Tags, tag) {
				hasTag = true
				break
			}
		}
		if !hasTag {
			return false
		}
	}

	// Featured only filter
	if filter.FeaturedOnly && !mr.Featured {
		return false
	}

	// Verified only filter
	if filter.VerifiedOnly && !mr.Verified {
		return false
	}

	return true
}

// sortResults sorts results based on filter criteria.
func (s *UnifiedSearcher) sortResults(results []UnifiedSearchResult, filter SearchFilter) []UnifiedSearchResult {
	query := strings.ToLower(filter.Query)

	sort.Slice(results, func(i, j int) bool {
		// Primary: exact name match
		iExact := strings.EqualFold(results[i].Name, query)
		jExact := strings.EqualFold(results[j].Name, query)
		if iExact != jExact {
			if filter.SortDesc {
				return jExact
			}
			return iExact
		}

		// Secondary: sort by specified field
		switch filter.SortBy {
		case "name":
			if filter.SortDesc {
				return results[i].Name > results[j].Name
			}
			return results[i].Name < results[j].Name

		case "downloads":
			if filter.SortDesc {
				return results[i].Downloads > results[j].Downloads
			}
			return results[i].Downloads < results[j].Downloads

		case "rating":
			if filter.SortDesc {
				return results[i].Rating > results[j].Rating
			}
			return results[i].Rating < results[j].Rating

		default: // "relevance" or empty
			// Featured first
			if results[i].Featured != results[j].Featured {
				if filter.SortDesc {
					return results[j].Featured
				}
				return results[i].Featured
			}
			// Verified next
			if results[i].Verified != results[j].Verified {
				if filter.SortDesc {
					return results[j].Verified
				}
				return results[i].Verified
			}
			// Installed next
			if results[i].Installed != results[j].Installed {
				if filter.SortDesc {
					return results[j].Installed
				}
				return results[i].Installed
			}
			// Then by name
			if filter.SortDesc {
				return results[i].Name > results[j].Name
			}
			return results[i].Name < results[j].Name
		}
	})

	return results
}

// mergeResults merges marketplace results with local results, avoiding duplicates.
func mergeResults(local, marketplace []UnifiedSearchResult) []UnifiedSearchResult {
	seen := make(map[string]bool)
	for _, r := range local {
		seen[r.Name] = true
	}

	for _, r := range marketplace {
		if !seen[r.Name] {
			local = append(local, r)
		}
	}

	return local
}

// containsType checks if a type is in the list.
func containsType(types []string, t string) bool {
	for _, typ := range types {
		if strings.EqualFold(typ, t) {
			return true
		}
	}
	return false
}

// containsIgnoreCase checks if a string is in the list (case-insensitive).
func containsIgnoreCase(list []string, item string) bool {
	for _, s := range list {
		if strings.EqualFold(s, item) {
			return true
		}
	}
	return false
}

// GetCategories returns all categories from skills and plugins.
func (s *UnifiedSearcher) GetCategories() []string {
	categories := make(map[string]bool)

	// Get skill categories
	if s.skillsLoader != nil {
		for _, cat := range s.skillsLoader.ListCategories() {
			categories[cat] = true
		}
	}

	// Get plugin categories
	if s.pluginsLoader != nil {
		for _, cat := range s.pluginsLoader.Registry.GetCategories() {
			categories[cat] = true
		}
	}

	// Get marketplace categories
	if s.marketplace != nil {
		for _, cat := range s.marketplace.GetCategories() {
			categories[cat] = true
		}
	}

	result := make([]string, 0, len(categories))
	for cat := range categories {
		result = append(result, cat)
	}
	sort.Strings(result)

	return result
}

// GetFeatured returns featured items from both skills and plugins.
func (s *UnifiedSearcher) GetFeatured() []UnifiedSearchResult {
	var results []UnifiedSearchResult

	// Get featured skills (would need to add featured field to skills)
	// Currently skills don't have a featured field

	// Get featured plugins
	if s.pluginsLoader != nil {
		for _, p := range s.pluginsLoader.List() {
			// Would need a featured field on plugins
			_ = p
		}
	}

	// Get featured from marketplace
	if s.marketplace != nil {
		for _, mr := range s.marketplace.GetFeatured() {
			installed := false
			enabled := false
			if s.pluginsLoader != nil {
				if p := s.pluginsLoader.Get(mr.Name); p != nil {
					installed = true
					enabled = p.Enabled
				}
			}

			results = append(results, mr.ToUnifiedSearchResult(installed))
			results[len(results)-1].Enabled = enabled
		}
	}

	return results
}

// QuickSearch performs a quick search with default filters.
func (s *UnifiedSearcher) QuickSearch(ctx context.Context, query string) ([]UnifiedSearchResult, error) {
	return s.Search(ctx, SearchFilter{
		Query: query,
		Limit: 20,
	})
}

// SearchSkillsOnly searches only skills.
func (s *UnifiedSearcher) SearchSkillsOnly(ctx context.Context, query string) ([]UnifiedSearchResult, error) {
	return s.Search(ctx, SearchFilter{
		Query: query,
		Types: []string{"skill"},
	})
}

// SearchPluginsOnly searches only plugins.
func (s *UnifiedSearcher) SearchPluginsOnly(ctx context.Context, query string) ([]UnifiedSearchResult, error) {
	return s.Search(ctx, SearchFilter{
		Query: query,
		Types: []string{"plugin"},
	})
}

// SearchInstalled searches only installed items.
func (s *UnifiedSearcher) SearchInstalled(ctx context.Context, query string) ([]UnifiedSearchResult, error) {
	return s.Search(ctx, SearchFilter{
		Query:         query,
		InstalledOnly: true,
	})
}

// SearchByCategory searches items in a specific category.
func (s *UnifiedSearcher) SearchByCategory(ctx context.Context, category string) ([]UnifiedSearchResult, error) {
	return s.Search(ctx, SearchFilter{
		Query:      "",
		Categories: []string{category},
	})
}

// Count returns the total count of skills and plugins.
func (s *UnifiedSearcher) Count() (skills, plugins, marketplacePlugins int) {
	if s.skillsLoader != nil {
		skills = len(s.skillsLoader.List())
	}
	if s.pluginsLoader != nil {
		plugins = s.pluginsLoader.Registry.Count()
	}
	if s.marketplace != nil {
		marketplacePlugins = len(s.marketplace.GetAll())
	}
	return skills, plugins, marketplacePlugins
}
