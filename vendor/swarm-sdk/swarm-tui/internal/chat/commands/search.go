package commands

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// ModelTag classifies models for filtering and presets.
type ModelTag string

const (
	TagFast        ModelTag = "fast"
	TagLongContext ModelTag = "long_context"
	TagCoding      ModelTag = "coding"
	TagVision      ModelTag = "vision"
	TagTools       ModelTag = "tools"
)

// SearchFilters captures parsed filter tokens from search input.
type SearchFilters struct {
	Provider   string
	ContextMin int
	ContextMax int
	Tags       map[ModelTag]bool
}

// ParsedSearch contains free-text query and filters.
type ParsedSearch struct {
	Text    string
	Filters SearchFilters
}

// MatchSearch returns true when query tokens are found in the combined fields.
func MatchSearch(query string, fields ...string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	var haystackBuilder strings.Builder
	for _, field := range fields {
		if field == "" {
			continue
		}
		if haystackBuilder.Len() > 0 {
			haystackBuilder.WriteString(" ")
		}
		haystackBuilder.WriteString(strings.ToLower(field))
	}
	haystack := haystackBuilder.String()
	for token := range strings.FieldsSeq(query) {
		if token == "" {
			continue
		}
		if !strings.Contains(haystack, token) {
			return false
		}
	}
	return true
}

// ParseSearchQuery extracts filter tokens and returns remaining text.
func ParseSearchQuery(query string) ParsedSearch {
	result := ParsedSearch{Filters: SearchFilters{Tags: make(map[ModelTag]bool)}}
	var textParts []string
	for token := range strings.FieldsSeq(query) {
		lower := strings.ToLower(token)
		switch {
		case strings.HasPrefix(lower, "@"):
			result.Filters.Provider = strings.TrimPrefix(lower, "@")
			continue
		case strings.HasPrefix(lower, "provider:"):
			result.Filters.Provider = strings.TrimPrefix(lower, "provider:")
			continue
		case strings.HasPrefix(lower, "ctx:"):
			value := strings.TrimPrefix(lower, "ctx:")
			if min, max, ok := parseContextFilter(value); ok {
				result.Filters.ContextMin = min
				result.Filters.ContextMax = max
				continue
			}
		case strings.HasPrefix(lower, "tag:"):
			value := strings.TrimPrefix(lower, "tag:")
			if addTagFilter(&result.Filters, value) {
				continue
			}
		case strings.HasPrefix(lower, "#"):
			value := strings.TrimPrefix(lower, "#")
			if addTagFilter(&result.Filters, value) {
				continue
			}
		}
		textParts = append(textParts, token)
	}
	result.Text = strings.Join(textParts, " ")
	return result
}

func addTagFilter(filters *SearchFilters, value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "fast", "cheap", "speed":
		filters.Tags[TagFast] = true
		return true
	case "long", "context", "longcontext":
		filters.Tags[TagLongContext] = true
		return true
	case "coding", "code", "coder":
		filters.Tags[TagCoding] = true
		return true
	case "vision", "image", "multimodal":
		filters.Tags[TagVision] = true
		return true
	case "tools", "tool", "function":
		filters.Tags[TagTools] = true
		return true
	default:
		return false
	}
}

func parseContextFilter(value string) (int, int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, 0, false
	}
	if strings.Contains(value, "-") {
		parts := strings.SplitN(value, "-", 2)
		min := ParseContextValue(parts[0])
		max := ParseContextValue(parts[1])
		if min > 0 && max > 0 {
			return min, max, true
		}
	}

	op := ""
	switch {
	case strings.HasPrefix(value, ">="):
		op = ">="
		value = strings.TrimPrefix(value, ">=")
	case strings.HasPrefix(value, ">"):
		op = ">"
		value = strings.TrimPrefix(value, ">")
	case strings.HasPrefix(value, "<="):
		op = "<="
		value = strings.TrimPrefix(value, "<=")
	case strings.HasPrefix(value, "<"):
		op = "<"
		value = strings.TrimPrefix(value, "<")
	case strings.HasPrefix(value, "="):
		op = "="
		value = strings.TrimPrefix(value, "=")
	}
	amount := ParseContextValue(value)
	if amount == 0 {
		return 0, 0, false
	}
	switch op {
	case ">", ">=":
		return amount, 0, true
	case "<", "<=":
		return 0, amount, true
	case "=":
		return amount, amount, true
	default:
		return amount, 0, true
	}
}

// ParseContextValue parses numeric context strings like 200k or 1m.
func ParseContextValue(value string) int {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return 0
	}
	multiplier := 1
	if strings.HasSuffix(value, "k") {
		multiplier = 1000
		value = strings.TrimSuffix(value, "k")
	} else if strings.HasSuffix(value, "m") {
		multiplier = 1000000
		value = strings.TrimSuffix(value, "m")
	}
	value = strings.TrimSpace(value)

	var digits strings.Builder
	for _, r := range value {
		if unicode.IsDigit(r) {
			digits.WriteRune(r)
		}
	}
	if digits.Len() == 0 {
		return 0
	}
	num, err := strconv.Atoi(digits.String())
	if err != nil {
		return 0
	}
	return num * multiplier
}

// FuzzyMatchToken matches a single token against a candidate string.
func FuzzyMatchToken(query, candidate string) (int, []int, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return 0, nil, true
	}
	q := []rune(strings.ToLower(query))
	c := []rune(strings.ToLower(candidate))
	indices := make([]int, 0, len(q))
	score := 0
	last := -1
	qi := 0
	for i, r := range c {
		if qi >= len(q) {
			break
		}
		if r == q[qi] {
			indices = append(indices, i)
			score += 10
			if last >= 0 {
				gap := i - last - 1
				if gap == 0 {
					score += 5
				} else {
					score -= gap
				}
			}
			if i == 0 || isDelimiter(c[i-1]) {
				score += 6
			}
			last = i
			qi++
		}
	}
	if qi != len(q) {
		return 0, nil, false
	}
	score -= len(c) / 2
	return score, indices, true
}

// FuzzyMatchTokens matches all tokens and merges highlight indices.
func FuzzyMatchTokens(query, candidate string) (int, []int, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return 0, nil, true
	}
	parts := strings.Fields(query)
	if len(parts) == 0 {
		return 0, nil, true
	}
	merged := make([]int, 0)
	total := 0
	for _, part := range parts {
		score, indices, ok := FuzzyMatchToken(part, candidate)
		if !ok {
			return 0, nil, false
		}
		total += score
		merged = append(merged, indices...)
	}
	if len(merged) == 0 {
		return total, nil, true
	}
	sort.Ints(merged)
	out := merged[:0]
	prev := -1
	for _, idx := range merged {
		if idx == prev {
			continue
		}
		out = append(out, idx)
		prev = idx
	}
	return total, out, true
}

func isDelimiter(r rune) bool {
	return r == ' ' || r == '-' || r == '_' || r == '/' || r == '.'
}
