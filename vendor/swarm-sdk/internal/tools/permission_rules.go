package tools

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// PermissionRule defines a conditional policy override.
type PermissionRule struct {
	ID          string               `json:"id,omitempty"`
	Description string               `json:"description,omitempty"`
	Priority    int                  `json:"priority,omitempty"`
	When        PermissionRuleMatch  `json:"when"`
	Then        PermissionRuleAction `json:"then"`
}

// PermissionRuleMatch defines the conditions for a rule.
type PermissionRuleMatch struct {
	Permissions []Permission            `json:"permissions,omitempty"`
	Tools       []string                `json:"tools,omitempty"`
	Operations  []string                `json:"operations,omitempty"`
	Paths       *PermissionPatternMatch `json:"paths,omitempty"`
	Commands    *PermissionPatternMatch `json:"commands,omitempty"`
	URLs        *PermissionPatternMatch `json:"urls,omitempty"`
	SizeBytes   *PermissionNumericMatch `json:"sizeBytes,omitempty"`
	Dangerous   *bool                   `json:"dangerous,omitempty"`
	AgentIDs    []string                `json:"agentIds,omitempty"`
	Modes       []string                `json:"modes,omitempty"`
}

// PermissionRuleAction defines the policy applied when a rule matches.
type PermissionRuleAction struct {
	Policy PermissionPolicy `json:"policy"`
	Reason string           `json:"reason,omitempty"`
}

// PermissionPatternMatch defines glob/regex matches with optional negation.
type PermissionPatternMatch struct {
	Glob  []string                `json:"glob,omitempty"`
	Regex []string                `json:"regex,omitempty"`
	Not   *PermissionPatternMatch `json:"not,omitempty"`
}

// PermissionNumericMatch defines numeric comparisons.
type PermissionNumericMatch struct {
	Eq      *int64                  `json:"eq,omitempty"`
	Gt      *int64                  `json:"gt,omitempty"`
	Gte     *int64                  `json:"gte,omitempty"`
	Lt      *int64                  `json:"lt,omitempty"`
	Lte     *int64                  `json:"lte,omitempty"`
	Between *PermissionNumericRange `json:"between,omitempty"`
}

// PermissionNumericRange defines inclusive numeric ranges.
type PermissionNumericRange struct {
	Min int64 `json:"min"`
	Max int64 `json:"max"`
}

// Matches returns true when the rule matches the request.
func (m PermissionRuleMatch) Matches(request PermissionEvaluationRequest) bool {
	if !matchesPermissions(m.Permissions, request.Permissions) {
		return false
	}
	if !matchesStringList(m.Tools, request.Tool) {
		return false
	}
	if !matchesAnyString(m.Operations, request.Operations) {
		return false
	}
	if !matchesPatternList(m.Paths, request.Paths) {
		return false
	}
	if !matchesPatternList(m.Commands, request.Commands) {
		return false
	}
	if !matchesPatternList(m.URLs, request.URLs) {
		return false
	}
	if !matchesNumeric(m.SizeBytes, request) {
		return false
	}
	if !matchesDangerous(m.Dangerous, request.Dangerous) {
		return false
	}
	if !matchesAnyString(m.AgentIDs, []string{request.AgentID}) {
		return false
	}
	if !matchesAnyString(m.Modes, []string{request.Mode}) {
		return false
	}
	return true
}

func matchesPermissions(rulePermissions []Permission, requestPermissions []Permission) bool {
	if len(rulePermissions) == 0 {
		return true
	}
	var index int
	for index = range rulePermissions {
		var rulePerm Permission = rulePermissions[index]
		var requestIndex int
		for requestIndex = range requestPermissions {
			var requestPerm Permission = requestPermissions[requestIndex]
			if requestPerm == rulePerm {
				return true
			}
		}
	}
	return false
}

func matchesStringList(values []string, target string) bool {
	if len(values) == 0 {
		return true
	}
	var index int
	for index = range values {
		if values[index] == target {
			return true
		}
	}
	return false
}

func matchesAnyString(values []string, candidates []string) bool {
	if len(values) == 0 {
		return true
	}
	var candidateIndex int
	for candidateIndex = range candidates {
		var candidate string = candidates[candidateIndex]
		if candidate == "" {
			continue
		}
		var valueIndex int
		for valueIndex = range values {
			if values[valueIndex] == candidate {
				return true
			}
		}
	}
	return false
}

func matchesPatternList(match *PermissionPatternMatch, values []string) bool {
	if match == nil {
		return true
	}
	if len(values) == 0 {
		return false
	}
	var valueIndex int
	for valueIndex = range values {
		var value string = values[valueIndex]
		if matchPattern(value, *match) {
			return true
		}
	}
	return false
}

func matchPattern(value string, match PermissionPatternMatch) bool {
	var positiveMatch bool = hasAnyPatterns(match)
	if !positiveMatch {
		positiveMatch = true
	} else {
		positiveMatch = matchPositivePattern(value, match)
	}
	if !positiveMatch {
		return false
	}
	if match.Not != nil && hasAnyPatterns(*match.Not) {
		if matchPattern(value, *match.Not) {
			return false
		}
	}
	return true
}

func hasAnyPatterns(match PermissionPatternMatch) bool {
	if len(match.Glob) > 0 {
		return true
	}
	if len(match.Regex) > 0 {
		return true
	}
	return false
}

func matchPositivePattern(value string, match PermissionPatternMatch) bool {
	var globIndex int
	for globIndex = 0; globIndex < len(match.Glob); globIndex++ {
		var pattern string = match.Glob[globIndex]
		if matchGlob(pattern, value) {
			return true
		}
	}
	var regexIndex int
	for regexIndex = 0; regexIndex < len(match.Regex); regexIndex++ {
		var pattern string = match.Regex[regexIndex]
		if matchRegex(pattern, value) {
			return true
		}
	}
	return false
}

func matchGlob(pattern string, value string) bool {
	var normalizedPattern string = filepath.ToSlash(pattern)
	var normalizedValue string = filepath.ToSlash(value)
	var matched bool
	var err error
	matched, err = doublestar.PathMatch(normalizedPattern, normalizedValue)
	if err != nil {
		return false
	}
	return matched
}

func matchRegex(pattern string, value string) bool {
	var compiled *regexp.Regexp
	var err error
	compiled, err = regexp.Compile(pattern)
	if err != nil {
		return false
	}
	return compiled.MatchString(value)
}

func matchesNumeric(match *PermissionNumericMatch, request PermissionEvaluationRequest) bool {
	if match == nil {
		return true
	}
	if !request.HasSize {
		return false
	}
	var value int64 = request.SizeBytes
	if match.Eq != nil && value != *match.Eq {
		return false
	}
	if match.Gt != nil && value <= *match.Gt {
		return false
	}
	if match.Gte != nil && value < *match.Gte {
		return false
	}
	if match.Lt != nil && value >= *match.Lt {
		return false
	}
	if match.Lte != nil && value > *match.Lte {
		return false
	}
	if match.Between != nil {
		if value < match.Between.Min || value > match.Between.Max {
			return false
		}
	}
	return true
}

func matchesDangerous(match *bool, isDangerous bool) bool {
	if match == nil {
		return true
	}
	return *match == isDangerous
}

func normalizeOperation(operation string) string {
	var trimmed string = strings.TrimSpace(operation)
	return strings.ToLower(trimmed)
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	var seen map[string]bool = make(map[string]bool, len(values))
	var result []string = make([]string, 0, len(values))
	var index int
	for index = range values {
		var value string = values[index]
		if value == "" {
			continue
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func normalizeOperations(values []string) []string {
	var normalized []string = make([]string, 0, len(values))
	var index int
	for index = range values {
		var op string = normalizeOperation(values[index])
		if op == "" {
			continue
		}
		normalized = append(normalized, op)
	}
	return uniqueStrings(normalized)
}
