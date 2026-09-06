package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const indexMdHeader = "The following repository indexes describe the structure " +
	"of the codebase. Use them to navigate without filesystem exploration."

// indexMdStaleAfter is the age beyond which an INDEX.md is considered stale.
// Past this threshold the index risks describing a codebase that has since
// drifted, turning a navigation aid into misleading bloat. When any injected
// INDEX.md exceeds this age, a freshness notice is appended to the context
// nudging the agent to re-index. Two weeks mirrors the SWA-9 requirement.
const indexMdStaleAfter = 14 * 24 * time.Hour

// indexMdStaleNotice builds the freshness notice appended when one or more
// injected INDEX.md files are stale. Kept separate from the walk so tests can
// assert on its output without duplicating the wording.
func indexMdStaleNotice(stale []string) string {
	var b strings.Builder
	b.WriteString("INDEX FRESHNESS NOTICE: the following repository index(es) " +
		"have not been updated in over 14 days and may no longer reflect the " +
		"current code. Treat their navigation hints with caution and, if you " +
		"observe drift between an index and the actual files, regenerate the " +
		"affected INDEX.md before relying on it:\n")
	for _, p := range stale {
		b.WriteString("  - ")
		b.WriteString(p)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// loadIndexMdWalk walks up from workspaceDir to the filesystem root,
// collecting INDEX.md files at each level. Files closer to workspaceDir
// are appended last (higher priority, the same ordering Claude Code uses
// for CLAUDE.md — closer to CWD = higher priority = read last by the model).
// Returns an empty string if no INDEX.md files are found.
func loadIndexMdWalk(workspaceDir string) string {
	if workspaceDir == "" {
		return ""
	}

	// Collect dirs walking upward from workspaceDir to root.
	var dirs []string
	current := workspaceDir
	for {
		dirs = append(dirs, current)
		parent := filepath.Dir(current)
		if parent == current {
			break // reached filesystem root
		}
		current = parent
	}

	// Reverse so root comes first; workspaceDir is last (highest priority).
	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}

	var parts []string
	var stale []string
	now := time.Now()
	for _, dir := range dirs {
		indexPath := filepath.Join(dir, "INDEX.md")
		content, err := os.ReadFile(indexPath)
		if err != nil {
			continue // silently skip missing or unreadable files
		}
		trimmed := strings.TrimSpace(string(content))
		if trimmed == "" {
			continue
		}
		// Flag indexes older than the staleness threshold so the agent is
		// warned not to trust drifted navigation hints.
		if info, statErr := os.Stat(indexPath); statErr == nil {
			if now.Sub(info.ModTime()) > indexMdStaleAfter {
				stale = append(stale, indexPath)
			}
		}
		parts = append(parts, fmt.Sprintf(
			"Contents of %s (repository navigation index):\n\n%s",
			indexPath, trimmed,
		))
	}

	if len(parts) == 0 {
		return ""
	}
	out := indexMdHeader + "\n\n" + strings.Join(parts, "\n\n---\n\n")
	if len(stale) > 0 {
		out += "\n\n---\n\n" + indexMdStaleNotice(stale)
	}
	return out
}
