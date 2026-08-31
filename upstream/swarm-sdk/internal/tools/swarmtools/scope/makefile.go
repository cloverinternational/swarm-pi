package scope

import (
	"regexp"
	"strings"
)

// MakefileDetector detects scope boundaries in Makefiles.
// Uses targets as scope containers: lines indented with tab under a target
// are in that target's scope.
type MakefileDetector struct{}

func init() {
	Register(&MakefileDetector{}, ".mk", ".makefile")
	// Note: "Makefile" with no extension is handled via special case in ForFile
}

func (d *MakefileDetector) Name() string { return "makefile" }

var makeTargetRe = regexp.MustCompile(`^([a-zA-Z_][\w.-]*)\s*:`)

func (d *MakefileDetector) DetectScopes(lines []string) []ScopeChain {
	result := make([]ScopeChain, len(lines))
	if len(lines) == 0 {
		return result
	}

	var currentTarget string

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			if currentTarget != "" {
				result[i] = ScopeChain{{Label: currentTarget}}
			} else {
				result[i] = ScopeChain{}
			}
			continue
		}

		// Target definition (not indented, ends with :)
		if m := makeTargetRe.FindStringSubmatch(line); m != nil && !strings.HasPrefix(line, "\t") {
			// Target line itself is at top level
			result[i] = ScopeChain{}
			currentTarget = m[1]
			continue
		}

		// Recipe line (starts with tab) — belongs to current target
		if strings.HasPrefix(line, "\t") && currentTarget != "" {
			result[i] = ScopeChain{{Label: currentTarget}}
			continue
		}

		// Variable assignment or other top-level content
		result[i] = ScopeChain{}
		currentTarget = "" // Non-recipe, non-target line resets context
	}

	return result
}
