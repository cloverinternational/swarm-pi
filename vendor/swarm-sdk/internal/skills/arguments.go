package skills

import (
	"regexp"
	"strings"
)

// SubstituteArguments replaces {{arg}} placeholders in skill content with
// provided argument values. It supports both named and positional arguments.
//
// Named arguments: If argNames is provided (e.g., ["repo", "branch"]),
// {{repo}} is replaced with the first arg and {{branch}} with the second.
//
// Positional arguments: {{1}} is replaced with the first arg, {{2}} with
// the second, etc. Positional substitution always runs, even with named args.
//
// CONTRACT:
//   - args is a whitespace-separated string of argument values
//   - Placeholders not matched by any provided arg are left as-is
//   - Empty args string → content returned unchanged
//   - Mirrors src/utils/argumentSubstitution.ts:94 in Claude Code
//
// Example:
//
//	SubstituteArguments("Clone {{repo}} on branch {{branch}}", "myrepo main", []string{"repo", "branch"})
//	→ "Clone myrepo on branch main"
func SubstituteArguments(content, args string, argNames []string) string {
	if content == "" || args == "" {
		return content
	}

	argValues := strings.Fields(args)

	// Named argument substitution: {{name}} → value
	for i, name := range argNames {
		if i < len(argValues) {
			placeholder := "{{" + name + "}}"
			content = strings.ReplaceAll(content, placeholder, argValues[i])
		}
	}

	// Positional argument substitution: {{1}} → first arg, {{2}} → second, etc.
	posRe := regexp.MustCompile(`\{\{(\d+)\}\}`)
	content = posRe.ReplaceAllStringFunc(content, func(match string) string {
		// Extract the number from {{N}}
		numStr := match[2 : len(match)-2]
		var idx int
		for _, c := range numStr {
			idx = idx*10 + int(c-'0')
		}
		if idx >= 1 && idx <= len(argValues) {
			return argValues[idx-1]
		}
		return match // Leave placeholder if no matching arg
	})

	return content
}
