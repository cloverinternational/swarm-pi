package autogenskills

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

const absorbTestInstructions = "This is a comprehensive set of instructions that definitely exceeds the two hundred character minimum length requirement so that validation passes without any issues whatsoever in this particular test case."

// binaryFixture is deliberately not valid UTF-8. A NUL byte, a lone 0xFF and an
// unpaired surrogate-ish sequence cannot survive being carried as a JSON string
// through a model's output, which is exactly the failure this primitive exists
// to remove. Any implementation that round-trips content through text will
// corrupt these bytes and fail the digest comparison below.
var binaryFixture = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0xFF, 0xFE, 0xC0, 0x80, 0x7F, 0x00, 0x01}

func newAbsorbService(t *testing.T) (*Service, *SkillManageTool, string) {
	t.Helper()
	dir := t.TempDir()
	svc, err := NewService(Config{Mode: ModeManual, AutogenDir: dir}, skills.NewRegistry(), nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	tool, err := NewSkillManageTool(svc)
	if err != nil {
		t.Fatalf("NewSkillManageTool: %v", err)
	}
	return svc, tool, dir
}

func mustCreateSkill(t *testing.T, tool *SkillManageTool, name string) {
	t.Helper()
	result, err := tool.Execute(context.Background(), map[string]any{
		"action":       "create",
		"name":         name,
		"description":  "Fixture skill for absorb tests",
		"instructions": absorbTestInstructions,
	})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	if !strings.Contains(result.Output, "Created skill") {
		t.Fatalf("create %s did not succeed: %q", name, result.Output)
	}
}

// writeSupport places a file directly on disk, bypassing the tool, so the test
// controls the exact bytes.
func writeSupport(t *testing.T, autogenDir, skill, relPath string, data []byte) {
	t.Helper()
	full := filepath.Join(autogenDir, skill, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", relPath, err)
	}
	if err := os.WriteFile(full, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

// currentRevision reads the revision a mutation must chain from. The package
// uses optimistic concurrency, so every write against an existing skill has to
// present the revision it observed.
func currentRevision(t *testing.T, svc *Service, name string) string {
	t.Helper()
	_, revision, err := svc.ViewSkillRevision(context.Background(), name, true)
	if err != nil {
		t.Fatalf("view %s for revision: %v", name, err)
	}
	return revision
}

// TestAbsorbFiles_PreservesBinaryBytesExactly is the core regression test for
// the curator's protected-file corruption. It asserts byte equality, not
// similarity: the destination must be identical to the source.
func TestAbsorbFiles_PreservesBinaryBytesExactly(t *testing.T) {
	svc, tool, dir := newAbsorbService(t)
	mustCreateSkill(t, tool, "source-skill")
	mustCreateSkill(t, tool, "umbrella-skill")
	writeSupport(t, dir, "source-skill", "assets/logo.png", binaryFixture)

	result, err := tool.Execute(context.Background(), map[string]any{
		"action":            "absorb_files",
		"from_skill":        "source-skill",
		"name":              "umbrella-skill",
		"expected_revision": currentRevision(t, svc, "umbrella-skill"),
	})
	if err != nil {
		t.Fatalf("absorb_files: %v", err)
	}
	if !strings.Contains(result.Output, "Absorbed 1 support file") {
		t.Fatalf("expected one absorbed file, got: %q", result.Output)
	}

	got, err := os.ReadFile(filepath.Join(dir, "umbrella-skill", "assets", "logo.png"))
	if err != nil {
		t.Fatalf("read absorbed asset: %v", err)
	}
	if !bytes.Equal(got, binaryFixture) {
		t.Fatalf("binary content was not preserved byte-for-byte:\n source %v\n dest   %v", binaryFixture, got)
	}

	// The report must describe the transfer without embedding the payload,
	// otherwise the bytes are back in the model's context.
	if strings.Contains(result.Output, string(binaryFixture)) {
		t.Error("absorb_files leaked raw file content into the tool result")
	}
}

// TestAbsorbFiles_CarriesEveryDirectoryAndNestedPaths proves the default is
// "carry everything", including nested paths, across all four support dirs.
func TestAbsorbFiles_CarriesEveryDirectoryAndNestedPaths(t *testing.T) {
	svc, tool, dir := newAbsorbService(t)
	mustCreateSkill(t, tool, "source-skill")
	mustCreateSkill(t, tool, "umbrella-skill")

	want := map[string][]byte{
		"references/notes.md":    []byte("# notes\nsome detail\n"),
		"scripts/collector.sh":   []byte("#!/usr/bin/env bash\nset -euo pipefail\necho hi\n"),
		"templates/report.tmpl":  []byte("{{ .Title }}\n"),
		"assets/nested/blob.bin": binaryFixture,
	}
	for rel, data := range want {
		writeSupport(t, dir, "source-skill", rel, data)
	}

	if _, err := tool.Execute(context.Background(), map[string]any{
		"action": "absorb_files", "from_skill": "source-skill", "name": "umbrella-skill",
		"expected_revision": currentRevision(t, svc, "umbrella-skill"),
	}); err != nil {
		t.Fatalf("absorb_files: %v", err)
	}

	for rel, data := range want {
		got, err := os.ReadFile(filepath.Join(dir, "umbrella-skill", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("missing absorbed file %s: %v", rel, err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("%s content differs after absorb", rel)
		}
	}
}

// TestAbsorbFiles_SelectsNamedSubset checks the model can carry a subset.
func TestAbsorbFiles_SelectsNamedSubset(t *testing.T) {
	svc, tool, dir := newAbsorbService(t)
	mustCreateSkill(t, tool, "source-skill")
	mustCreateSkill(t, tool, "umbrella-skill")
	writeSupport(t, dir, "source-skill", "scripts/keep.sh", []byte("keep\n"))
	writeSupport(t, dir, "source-skill", "scripts/drop.sh", []byte("drop\n"))

	if _, err := tool.Execute(context.Background(), map[string]any{
		"action": "absorb_files", "from_skill": "source-skill", "name": "umbrella-skill",
		"file_paths":        "scripts/keep.sh",
		"expected_revision": currentRevision(t, svc, "umbrella-skill"),
	}); err != nil {
		t.Fatalf("absorb_files: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "umbrella-skill", "scripts", "keep.sh")); err != nil {
		t.Errorf("selected file was not carried: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "umbrella-skill", "scripts", "drop.sh")); !os.IsNotExist(err) {
		t.Error("unselected file should not have been carried")
	}
}

// TestAbsorbFiles_RejectsUnknownPath keeps a typo from silently carrying nothing.
func TestAbsorbFiles_RejectsUnknownPath(t *testing.T) {
	svc, tool, dir := newAbsorbService(t)
	mustCreateSkill(t, tool, "source-skill")
	mustCreateSkill(t, tool, "umbrella-skill")
	writeSupport(t, dir, "source-skill", "scripts/real.sh", []byte("real\n"))

	result, err := tool.Execute(context.Background(), map[string]any{
		"action": "absorb_files", "from_skill": "source-skill", "name": "umbrella-skill",
		"file_paths":        "scripts/typo.sh",
		"expected_revision": currentRevision(t, svc, "umbrella-skill"),
	})
	if err != nil {
		t.Fatalf("absorb_files: %v", err)
	}
	if !strings.Contains(result.Output, "no such support file") {
		t.Errorf("expected explicit rejection of unknown path, got: %q", result.Output)
	}
}

// TestArchive_RefusedWhenSupportFilesWouldBeLost is the guard that turns silent
// data loss into a hard error.
func TestArchive_RefusedWhenSupportFilesWouldBeLost(t *testing.T) {
	svc, tool, dir := newAbsorbService(t)
	svc.curator = NewCurator(CuratorConfig{}, svc.factory, filepath.Join(dir, ".curator_state"))
	mustCreateSkill(t, tool, "source-skill")
	mustCreateSkill(t, tool, "umbrella-skill")
	writeSupport(t, dir, "source-skill", "scripts/collector.sh", []byte("#!/bin/sh\ncollect\n"))

	result, err := tool.Execute(context.Background(), map[string]any{
		"action": "archive", "name": "source-skill", "absorbed_into": "umbrella-skill",
		"expected_revision": currentRevision(t, svc, "source-skill"),
	})
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !strings.Contains(result.Output, "Archive refused") {
		t.Fatalf("archive should refuse to drop support files, got: %q", result.Output)
	}
	if !strings.Contains(result.Output, "scripts/collector.sh") {
		t.Errorf("refusal must name the file at risk, got: %q", result.Output)
	}
	if !strings.Contains(result.Output, "absorb_files") {
		t.Errorf("refusal must point at the remedy, got: %q", result.Output)
	}
	if _, err := os.Stat(filepath.Join(dir, "source-skill")); err != nil {
		t.Errorf("refused archive must leave the source in place: %v", err)
	}
}

// TestArchive_AllowedAfterAbsorb closes the loop: absorb, then archive succeeds.
func TestArchive_AllowedAfterAbsorb(t *testing.T) {
	svc, tool, dir := newAbsorbService(t)
	svc.curator = NewCurator(CuratorConfig{}, svc.factory, filepath.Join(dir, ".curator_state"))
	mustCreateSkill(t, tool, "source-skill")
	mustCreateSkill(t, tool, "umbrella-skill")
	writeSupport(t, dir, "source-skill", "scripts/collector.sh", []byte("#!/bin/sh\ncollect\n"))
	writeSupport(t, dir, "source-skill", "assets/icon.bin", binaryFixture)

	if _, err := tool.Execute(context.Background(), map[string]any{
		"action": "absorb_files", "from_skill": "source-skill", "name": "umbrella-skill",
		"expected_revision": currentRevision(t, svc, "umbrella-skill"),
	}); err != nil {
		t.Fatalf("absorb_files: %v", err)
	}
	result, err := tool.Execute(context.Background(), map[string]any{
		"action": "archive", "name": "source-skill", "absorbed_into": "umbrella-skill",
		"expected_revision": currentRevision(t, svc, "source-skill"),
	})
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if strings.Contains(result.Output, "Archive refused") {
		t.Fatalf("archive should succeed once files are carried, got: %q", result.Output)
	}
	if !strings.Contains(result.Output, "Archived skill") {
		t.Fatalf("expected archive success, got: %q", result.Output)
	}
}

// TestArchive_DroppedFilesRequireJustification allows deliberate loss, but only
// when it is named and explained.
func TestArchive_DroppedFilesRequireJustification(t *testing.T) {
	svc, tool, dir := newAbsorbService(t)
	svc.curator = NewCurator(CuratorConfig{}, svc.factory, filepath.Join(dir, ".curator_state"))
	mustCreateSkill(t, tool, "source-skill")
	mustCreateSkill(t, tool, "umbrella-skill")
	writeSupport(t, dir, "source-skill", "scripts/obsolete.sh", []byte("old\n"))

	// Named but unjustified: refused.
	result, err := tool.Execute(context.Background(), map[string]any{
		"action": "archive", "name": "source-skill", "absorbed_into": "umbrella-skill",
		"dropped_files":     "scripts/obsolete.sh",
		"expected_revision": currentRevision(t, svc, "source-skill"),
	})
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !strings.Contains(result.Output, "requires 'pruning_reason'") {
		t.Fatalf("dropping a file without justification must be refused, got: %q", result.Output)
	}

	// Named and justified: allowed.
	result, err = tool.Execute(context.Background(), map[string]any{
		"action": "archive", "name": "source-skill", "absorbed_into": "umbrella-skill",
		"dropped_files":     "scripts/obsolete.sh",
		"pruning_reason":    "collector was replaced by the umbrella's own runner and no longer executes",
		"expected_revision": currentRevision(t, svc, "source-skill"),
	})
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !strings.Contains(result.Output, "Archived skill") {
		t.Fatalf("justified drop should archive, got: %q", result.Output)
	}
}

// TestArchive_UnaffectedWhenSourceHasNoSupportFiles keeps the common case cheap.
func TestArchive_UnaffectedWhenSourceHasNoSupportFiles(t *testing.T) {
	svc, tool, dir := newAbsorbService(t)
	svc.curator = NewCurator(CuratorConfig{}, svc.factory, filepath.Join(dir, ".curator_state"))
	mustCreateSkill(t, tool, "plain-skill")
	mustCreateSkill(t, tool, "umbrella-skill")

	result, err := tool.Execute(context.Background(), map[string]any{
		"action": "archive", "name": "plain-skill", "absorbed_into": "umbrella-skill",
		"expected_revision": currentRevision(t, svc, "plain-skill"),
	})
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !strings.Contains(result.Output, "Archived skill") {
		t.Fatalf("a skill with no support files should archive unchanged, got: %q", result.Output)
	}
}

// TestListSupportFiles_RejectsSymlink ensures a symlinked artifact is reported
// rather than quietly skipped, since a skip would defeat the archive guard.
func TestListSupportFiles_RejectsSymlink(t *testing.T) {
	_, tool, dir := newAbsorbService(t)
	mustCreateSkill(t, tool, "source-skill")
	writeSupport(t, dir, "source-skill", "scripts/real.sh", []byte("real\n"))

	link := filepath.Join(dir, "source-skill", "scripts", "link.sh")
	if err := os.Symlink(filepath.Join(dir, "source-skill", "scripts", "real.sh"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := listSupportFiles(dir, "source-skill"); err == nil {
		t.Error("expected symlink in support tree to be rejected")
	}
}
