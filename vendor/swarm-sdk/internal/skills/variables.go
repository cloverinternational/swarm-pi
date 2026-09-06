package skills

import (
	"path/filepath"
	"strings"
)

// SubstituteVariables replaces built-in variables in skill content at invocation
// time. Two variables are supported:
//
//   - ${SWARM_SKILL_DIR} → the skill's own directory path (so scripts can be
//     referenced relative to the skill root)
//   - ${SWARM_SESSION_ID} → the current session identifier
//
// CONTRACT:
//   - skillDir must be an absolute path; if empty, ${SWARM_SKILL_DIR} is replaced with ""
//   - sessionID may be empty; ${SWARM_SESSION_ID} is replaced with "" in that case
//   - On Windows, backslashes in skillDir are converted to forward slashes
//   - Mirrors src/skills/loadSkillsDir.ts:359-369 in Claude Code
//     (which uses ${CLAUDE_SKILL_DIR} and ${CLAUDE_SESSION_ID})
//
// Example:
//
//	SubstituteVariables("Run ${SWARM_SKILL_DIR}/scripts/deploy.sh", "/home/user/.swarm/skills/deploy", "sess_abc123")
//	→ "Run /home/user/.swarm/skills/deploy/scripts/deploy.sh"
func SubstituteVariables(content, skillDir, sessionID string) string {
	if content == "" {
		return content
	}

	// Normalize skillDir to forward slashes (consistent with Claude Code behavior on Windows)
	dir := filepath.ToSlash(skillDir)

	c := strings.ReplaceAll(content, "${SWARM_SKILL_DIR}", dir)
	c = strings.ReplaceAll(c, "${SWARM_SESSION_ID}", sessionID)

	return c
}
