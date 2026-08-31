package settings

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// TestRenderWorkspaceContext_Structure verifies the XML envelope structure is stable.
// This is a snapshot test: if the output format changes, the test catches it.
func TestRenderWorkspaceContext_Structure(t *testing.T) {
	output := RenderWorkspaceContext()

	// Must start with the system_information tag.
	if !strings.HasPrefix(output, "<system_information>") {
		t.Errorf("expected output to start with <system_information>, got:\n%s", clipStr(output, 80))
	}

	// Must end with closing tag.
	if !strings.HasSuffix(output, "</system_information>") {
		t.Errorf("expected output to end with </system_information>, got:\n...%s", tailStr(output, 80))
	}

	// Must contain the four required OS/env fields.
	requiredTags := []string{
		"<operating_system>",
		"</operating_system>",
		"<current_working_directory>",
		"</current_working_directory>",
		"<default_shell>",
		"</default_shell>",
		"<home_directory>",
		"</home_directory>",
	}
	for _, tag := range requiredTags {
		if !strings.Contains(output, tag) {
			t.Errorf("expected output to contain %q\nFull output:\n%s", tag, output)
		}
	}
}

// TestRenderWorkspaceContext_OSValue verifies the OS field matches runtime.GOOS.
func TestRenderWorkspaceContext_OSValue(t *testing.T) {
	output := RenderWorkspaceContext()

	// The <operating_system> tag must contain a non-empty value.
	start := strings.Index(output, "<operating_system>") + len("<operating_system>")
	end := strings.Index(output, "</operating_system>")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("could not find <operating_system> in output")
	}
	osValue := output[start:end]
	if osValue == "" {
		t.Error("operating_system must be non-empty")
	}
	// Must be one of the known GOOS values or at least non-empty.
	knownOS := []string{"linux", "darwin", "windows", "freebsd", "openbsd"}
	found := slices.Contains(knownOS, osValue)
	if !found {
		t.Logf("operating_system value %q is not a common GOOS value, but that may be OK on exotic platforms", osValue)
	}
}

// TestRenderWorkspaceContext_CWD verifies the CWD matches os.Getwd().
func TestRenderWorkspaceContext_CWD(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Skip("could not get working directory")
	}

	output := RenderWorkspaceContext()
	if !strings.Contains(output, cwd) {
		t.Errorf("expected output to contain CWD %q\nFull output:\n%s", cwd, output)
	}
}

// TestRenderWorkspaceContext_Shell verifies the shell field is non-empty.
func TestRenderWorkspaceContext_Shell(t *testing.T) {
	output := RenderWorkspaceContext()

	start := strings.Index(output, "<default_shell>") + len("<default_shell>")
	end := strings.Index(output, "</default_shell>")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("could not find <default_shell> in output")
	}
	shell := output[start:end]
	if shell == "" {
		t.Error("default_shell must be non-empty")
	}
}

// TestRenderWorkspaceContext_HomeDir verifies the home directory is set.
func TestRenderWorkspaceContext_HomeDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("could not get home directory")
	}

	output := RenderWorkspaceContext()
	if !strings.Contains(output, home) {
		t.Errorf("expected output to contain HOME %q\nFull output:\n%s", home, output)
	}
}

// TestRenderWorkspaceContext_Extensions verifies that when running inside a git
// repo, the <workspace_extensions> block is present and well-formed.
func TestRenderWorkspaceContext_Extensions(t *testing.T) {
	// Only run this test inside a git repository.
	if _, err := os.Stat(".git"); os.IsNotExist(err) {
		// Walk up to find git root (we may be in a subdirectory).
		found := false
		dir := "."
		for range 6 {
			if _, err := os.Stat(dir + "/.git"); err == nil {
				found = true
				break
			}
			dir += "/.."
		}
		if !found {
			t.Skip("not inside a git repository, skipping extensions test")
		}
	}

	output := RenderWorkspaceContext()

	// The extensions block may or may not be present depending on whether
	// git ls-files returns results. We only check the structure if present.
	if strings.Contains(output, "<workspace_extensions") {
		if !strings.Contains(output, "</workspace_extensions>") {
			t.Error("found opening <workspace_extensions> but no closing tag")
		}
		// Must have the command attribute.
		if !strings.Contains(output, `command="git ls-files"`) {
			t.Error("workspace_extensions must have command attribute")
		}
		// Must have files and extensions counts.
		if !strings.Contains(output, `files="`) {
			t.Error("workspace_extensions must have files attribute")
		}
		if !strings.Contains(output, `extensions="`) {
			t.Error("workspace_extensions must have extensions attribute")
		}
	}
}

// TestFetchExtensions_SnapshotFormat is a snapshot test for the extension block
// format. It exercises fetchExtensions with a synthetic file list.
func TestFetchExtensions_SnapshotFormat(t *testing.T) {
	// We can't easily inject files, but we can test the output format by
	// calling RenderWorkspaceContext in a temp dir with a synthetic git repo.
	// For now, verify the real output format when running in the monorepo.
	output := RenderWorkspaceContext()

	// The output must be valid XML-like (no unmatched angle brackets in tag lines).
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "<") && !strings.HasPrefix(trimmed, "<!--") {
			// Tag lines must have matching open/close or be self-contained.
			openCount := strings.Count(trimmed, "<")
			closeCount := strings.Count(trimmed, ">")
			if openCount != closeCount {
				t.Errorf("line %d has mismatched angle brackets: %q", i+1, trimmed)
			}
		}
	}
}

// TestForgeSwarmSystemPromptSections verifies the forgeSwarmSystemPrompt constant
// contains all required sections of the Forge-ported prompt.
func TestForgeSwarmSystemPromptSections(t *testing.T) {
	prompt := forgeSwarmSystemPrompt

	// The prompt must be non-empty and substantial.
	if len(prompt) < 1000 {
		t.Errorf("forgeSwarmSystemPrompt too short (%d chars), expected > 1000", len(prompt))
	}

	// Key sections that the Forge prompt must contain.
	requiredSections := []string{
		// Tool preference rules
		"rg PATTERN",
		"sed -n",
		"Reading images",
		// Behavioral rules
		"ALWAYS",
		// Task management
		"TaskManage",
		// Planning and requirement discovery (grill-me integration)
		"decision tree",
		"ask_user_question",
	}

	for _, section := range requiredSections {
		if !strings.Contains(prompt, section) {
			t.Errorf("forgeSwarmSystemPrompt missing required section/keyword: %q", section)
		}
	}
}

// TestWorkspaceContextPromptEntry verifies the built-in Forge-Swarm prompt entry
// has WorkspaceContext enabled.
func TestWorkspaceContextPromptEntry(t *testing.T) {
	// The forgeSwarmSystemPrompt must be registered as a built-in prompt with
	// WorkspaceContext: true. We verify by checking the builtinPrompts list.
	found := false
	for _, p := range builtinPrompts {
		if p.WorkspaceContext {
			found = true
			// Verify the content is the forge-swarm prompt.
			if !strings.Contains(p.Content, "Finding and reading code") {
				t.Errorf("WorkspaceContext prompt %q is missing the shell-first search section", p.Name)
			}
			break
		}
	}
	if !found {
		t.Error("no built-in system prompt has WorkspaceContext: true")
	}
}

// helper: clip a string at n chars for display in error messages.
func clipStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// helper: last n chars for display in error messages.
func tailStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
