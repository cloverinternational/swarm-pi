package skills

import (
	"sort"
	"strings"
	"unicode"
)

type rankedSkill struct {
	skill  *Skill
	score  int
	active bool
}

// RankForContext orders skills for prompt exposure without mutating the input.
// Active skills always remain visible, followed by deterministic lexical
// relevance across names, descriptions, when-to-use text, categories, and tags.
//
// Scoring reads the same shortened description the prompt renders. Ranking on
// text the catalog never displays lets a skill win on vocabulary the model
// cannot read, which produces confident wrong selections rather than honest
// misses, and makes description length an unbounded ranking lever.
func RankForContext(available, active []*Skill, query string) []*Skill {
	activeNames := make(map[string]struct{}, len(active))
	for _, skill := range active {
		if skill != nil {
			activeNames[strings.ToLower(skill.Metadata.Name)] = struct{}{}
		}
	}
	query = strings.ToLower(strings.TrimSpace(query))
	terms := relevanceTerms(query)
	ranked := make([]rankedSkill, 0, len(available))
	for _, skill := range available {
		if skill == nil {
			continue
		}
		_, isActive := activeNames[strings.ToLower(skill.Metadata.Name)]
		ranked = append(ranked, rankedSkill{skill: skill, active: isActive, score: skillRelevanceScore(skill, query, terms)})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].active != ranked[j].active {
			return ranked[i].active
		}
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if ranked[i].skill.Metadata.Priority != ranked[j].skill.Metadata.Priority {
			return ranked[i].skill.Metadata.Priority > ranked[j].skill.Metadata.Priority
		}
		return strings.ToLower(ranked[i].skill.Metadata.Name) < strings.ToLower(ranked[j].skill.Metadata.Name)
	})
	result := make([]*Skill, len(ranked))
	for i := range ranked {
		result[i] = ranked[i].skill
	}
	return result
}

func skillRelevanceScore(skill *Skill, query string, terms []string) int {
	name := strings.ToLower(skill.Metadata.Name)
	description := strings.ToLower(promptVisibleDescription(skill.Metadata.Description))
	whenToUse := strings.ToLower(skill.Metadata.WhenToUse)
	metadata := strings.ToLower(strings.Join(append(append([]string{skill.Metadata.Category}, skill.Metadata.Tags...), skill.LoadedFrom, skill.Source), " "))
	score := 0
	if query != "" {
		if strings.Contains(name, query) {
			score += 1000
		}
		if strings.Contains(description+" "+whenToUse, query) {
			score += 600
		}
	}
	for _, term := range terms {
		if strings.Contains(name, term) {
			score += 80
		}
		if strings.Contains(description, term) {
			score += 30
		}
		if strings.Contains(whenToUse, term) {
			score += 40
		}
		if strings.Contains(metadata, term) {
			score += 10
		}
	}
	return score
}

func relevanceTerms(value string) []string {
	seen := make(map[string]struct{})
	terms := make([]string, 0)
	for _, field := range strings.FieldsFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(field)) < 3 {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		terms = append(terms, field)
	}
	return terms
}
