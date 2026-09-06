package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseSkillMDContentLenientFallback covers the ~57 real-world autogen
// skills written by the pre-fix factory: unquoted frontmatter scalars
// containing ": " are invalid YAML, but the loader must still load them
// (lenient line-based fallback) instead of silently dropping the skill and
// creating a disk-vs-registry split-brain with the curator.
func TestParseSkillMDContentLenientFallback(t *testing.T) {
	legacy := `---
name: tui-slash-command-as-prompt
description: Wire a new TUI slash command. CRITICAL: addNotification() is invisible — always use LoopInjectPromptMsg.
version: 1.0.1
author: swarm-autogen
category: acting
tags: [tui, slash-command, swarm, bubbletea, chat]
trigger_reason: LLM nudge response
---

# TUI Slash Command

Body content here.
`

	metadata, content, _, err := ParseSkillMDContent([]byte(legacy))
	if err != nil {
		t.Fatalf("lenient fallback did not rescue legacy frontmatter: %v", err)
	}
	if metadata.Name != "tui-slash-command-as-prompt" {
		t.Errorf("name: got %q", metadata.Name)
	}
	if want := "Wire a new TUI slash command. CRITICAL: addNotification() is invisible — always use LoopInjectPromptMsg."; metadata.Description != want {
		t.Errorf("description:\n got %q\nwant %q", metadata.Description, want)
	}
	if metadata.Version != "1.0.1" {
		t.Errorf("version: got %q", metadata.Version)
	}
	if len(metadata.Tags) != 5 || metadata.Tags[0] != "tui" {
		t.Errorf("tags: got %v", metadata.Tags)
	}
	if content == "" {
		t.Error("content empty")
	}
}

// TestLoadSkillLenientEndToEnd verifies the full LoadSkill path accepts a
// legacy-format SKILL.md on disk.
func TestLoadSkillLenientEndToEnd(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "legacy-colon-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	legacy := "---\nname: legacy-colon-skill\ndescription: Fix the bug: quote your YAML\nversion: 1.0.0\nauthor: swarm-autogen\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}

	skill, err := LoadSkill(skillDir)
	if err != nil {
		t.Fatalf("LoadSkill rejected legacy file: %v", err)
	}
	if skill.Metadata.Name != "legacy-colon-skill" {
		t.Errorf("name: got %q", skill.Metadata.Name)
	}
	if want := "Fix the bug: quote your YAML"; skill.Metadata.Description != want {
		t.Errorf("description: got %q want %q", skill.Metadata.Description, want)
	}
}

// TestParseSkillMDContentStillRejectsGarbage: the lenient fallback must not
// turn arbitrary non-frontmatter garbage into a "loaded" skill with empty
// metadata — files with no recognizable key: value lines still fail.
func TestParseSkillMDContentStillRejectsGarbage(t *testing.T) {
	garbage := "---\n<<<<<<< HEAD\n{{{ not yaml at all\n---\nbody"
	metadata, _, _, err := ParseSkillMDContent([]byte(garbage))
	if err == nil && metadata != nil && metadata.Name == "" && metadata.Description == "" {
		t.Errorf("garbage frontmatter produced empty-but-accepted metadata; want error or fields")
	}
}

// TestParseSkillMDContentMalformedPreservesHooks covers the latent data-loss
// bug: when frontmatter has an unquoted scalar containing ": " (invalid YAML),
// the old code recovered flat metadata via the line-by-line lenient parser but
// silently dropped any nested "hooks:" block because the separate hooks
// yaml.Unmarshal also failed. The structured sanitize path must re-quote the
// offending scalar so nested hooks/triggers parse and survive.
func TestParseSkillMDContentMalformedPreservesHooks(t *testing.T) {
	legacy := `---
name: hooked-legacy-skill
description: Do the thing. NOTE: this value has a colon-space so strict YAML rejects it.
version: 1.0.0
hooks:
  PreToolUse:
    - matcher: Bash
      command: echo guard
---

body
`
	metadata, _, hooks, err := ParseSkillMDContent([]byte(legacy))
	if err != nil {
		t.Fatalf("sanitize path did not rescue malformed frontmatter: %v", err)
	}
	if metadata.Name != "hooked-legacy-skill" {
		t.Errorf("name: got %q", metadata.Name)
	}
	if want := "Do the thing. NOTE: this value has a colon-space so strict YAML rejects it."; metadata.Description != want {
		t.Errorf("description:\n got %q\nwant %q", metadata.Description, want)
	}
	if hooks == nil {
		t.Fatal("hooks were silently dropped from malformed frontmatter; want them preserved")
	}
	if len(hooks.PreToolUse) == 0 {
		t.Fatalf("PreToolUse hook not recovered: %+v", hooks)
	}
}

// TestSanitizeFrontmatterYAML unit-tests the scalar-quoting helper directly:
// flat scalars with ": " get quoted; nested lines, openers, and already-quoted
// values are left alone; a clean document reports changed=false.
func TestSanitizeFrontmatterYAML(t *testing.T) {
	lines := []string{
		"name: fine",
		"description: has a: colon",
		"hooks:",
		"  PreToolUse:",
		"    - matcher: Bash",
		`author: "already quoted: ok"`,
		"tags: [a, b]",
	}
	out, changed := sanitizeFrontmatterYAML(lines)
	if !changed {
		t.Fatal("expected changed=true for a document with an unquoted colon scalar")
	}
	if !strings.Contains(out, `description: "has a: colon"`) {
		t.Errorf("description not quoted:\n%s", out)
	}
	// Nested and opener lines must be preserved verbatim.
	if !strings.Contains(out, "hooks:") || !strings.Contains(out, "  PreToolUse:") || !strings.Contains(out, "    - matcher: Bash") {
		t.Errorf("nested structure not preserved:\n%s", out)
	}
	// Already-quoted and flow values are left as-is.
	if !strings.Contains(out, `author: "already quoted: ok"`) {
		t.Errorf("already-quoted scalar altered:\n%s", out)
	}
	if !strings.Contains(out, "tags: [a, b]") {
		t.Errorf("flow sequence altered:\n%s", out)
	}

	if _, changed := sanitizeFrontmatterYAML([]string{"name: fine", "version: 1.0.0"}); changed {
		t.Error("clean document should report changed=false")
	}
}
