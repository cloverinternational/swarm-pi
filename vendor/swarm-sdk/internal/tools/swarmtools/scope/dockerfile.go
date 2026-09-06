package scope

import (
	"regexp"
	"strings"
)

// DockerfileDetector detects scope boundaries in Dockerfiles.
// Uses multi-stage build stages (FROM) as top-level scope, and key
// instructions (RUN, COPY, ADD, ENV) as context hints.
type DockerfileDetector struct{}

func init() {
	Register(&DockerfileDetector{}, ".dockerfile")
	// Also register for Dockerfile (no extension) — handled in ForFile via special case
}

func (d *DockerfileDetector) Name() string { return "dockerfile" }

var (
	dockerFromRe = regexp.MustCompile(`(?i)^FROM\s+(\S+)(?:\s+AS\s+(\w+))?`)
)

func (d *DockerfileDetector) DetectScopes(lines []string) []ScopeChain {
	result := make([]ScopeChain, len(lines))
	if len(lines) == 0 {
		return result
	}

	var currentStage string

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			if currentStage != "" {
				result[i] = ScopeChain{{Label: currentStage}}
			} else {
				result[i] = ScopeChain{}
			}
			continue
		}

		// FROM starts a new stage
		if m := dockerFromRe.FindStringSubmatch(trimmed); m != nil {
			// Assign without current stage (FROM is the boundary)
			result[i] = ScopeChain{}

			if m[2] != "" {
				currentStage = "stage " + m[2]
			} else {
				currentStage = "FROM " + m[1]
				if len(currentStage) > 40 {
					currentStage = currentStage[:40]
				}
			}
			continue
		}

		if currentStage != "" {
			result[i] = ScopeChain{{Label: currentStage}}
		} else {
			result[i] = ScopeChain{}
		}
	}

	return result
}
