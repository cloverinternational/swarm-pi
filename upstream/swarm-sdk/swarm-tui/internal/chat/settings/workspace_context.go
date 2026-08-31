package settings

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	chatcontext "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/context"
)

const maxExtensions = 15

// workspaceEnvInjectionEnabled consults the "workspace_env" injection source
// in the context settings (global + project merge), so users can disable the
// <system_information> block from the Context settings screen.
func workspaceEnvInjectionEnabled() bool {
	loader := chatcontext.NewFileLoader()
	return chatcontext.IsInjectionEnabled(loader.GetConfig(), chatcontext.SourceIDWorkspaceEnv)
}

// RenderWorkspaceContext returns a <system_information> block mirroring
// Forge's forge-partial-system-info.md output. It is injected at runtime
// into system prompts that set WorkspaceContext: true.
//
// Example output:
//
//	<system_information>
//	<operating_system>linux</operating_system>
//	<current_working_directory>/home/user/project</current_working_directory>
//	<default_shell>/bin/bash</default_shell>
//	<home_directory>/home/user</home_directory>
//	<workspace_extensions command="git ls-files" files="822" extensions="19">
//	 - .go: 215 files (60%)
//	 - .md: 45 files (12%)
//	</workspace_extensions>
//	</system_information>
func RenderWorkspaceContext() string {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	var sb strings.Builder
	sb.WriteString("<system_information>\n")
	fmt.Fprintf(&sb, "<operating_system>%s</operating_system>\n", runtime.GOOS)
	fmt.Fprintf(&sb, "<current_working_directory>%s</current_working_directory>\n", cwd)
	fmt.Fprintf(&sb, "<default_shell>%s</default_shell>\n", shell)
	fmt.Fprintf(&sb, "<home_directory>%s</home_directory>\n", home)

	if ext := fetchExtensions(cwd); ext != "" {
		sb.WriteString(ext)
	}

	sb.WriteString("</system_information>")
	return sb.String()
}

// extStat holds per-extension file count and percentage.
type extStat struct {
	ext        string
	count      int
	percentage int
}

// fetchExtensions runs "git ls-files" and returns the formatted
// <workspace_extensions> block, or empty string if git is unavailable
// or the directory is not a git repository.
func fetchExtensions(cwd string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "ls-files")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return ""
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	totalFiles := 0
	counts := make(map[string]int)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		totalFiles++
		// Take only the filename part (after last / or \)
		fname := line
		if idx := strings.LastIndexAny(line, "/\\"); idx >= 0 {
			fname = line[idx+1:]
		}
		// Find extension: rsplit on '.', ignore leading dots (hidden files)
		ext := "(no ext)"
		if idx := strings.LastIndex(fname, "."); idx > 0 {
			ext = fname[idx+1:]
		}
		counts[ext]++
	}

	if totalFiles == 0 {
		return ""
	}

	// Build sorted stats
	stats := make([]extStat, 0, len(counts))
	for ext, count := range counts {
		pct := int((float64(count*100) / float64(totalFiles)) + 0.5) // round
		stats = append(stats, extStat{ext: ext, count: count, percentage: pct})
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].count != stats[j].count {
			return stats[i].count > stats[j].count
		}
		return stats[i].ext < stats[j].ext
	})

	totalExtensions := len(stats)
	if len(stats) > maxExtensions {
		stats = stats[:maxExtensions]
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "<workspace_extensions command=\"git ls-files\" files=\"%d\" extensions=\"%d\">\n",
		totalFiles, totalExtensions)
	for _, s := range stats {
		fmt.Fprintf(&sb, " - .%s: %d files (%d%%)\n", s.ext, s.count, s.percentage)
	}
	if totalExtensions > maxExtensions {
		fmt.Fprintf(&sb, "(showing top %d of %d extensions)\n", maxExtensions, totalExtensions)
	}
	sb.WriteString("</workspace_extensions>\n")
	return sb.String()
}
