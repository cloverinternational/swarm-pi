package autogenskills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

func filesystemTree(t *testing.T, root string) string {
	t.Helper()
	var tree strings.Builder
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(&tree, "%s %s\n", filepath.ToSlash(relative), info.Mode())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(&tree, "%q\n", data)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return tree.String()
}

func newHistoryTestService(t *testing.T) (*Service, *skills.Registry, string) {
	t.Helper()
	dir := t.TempDir()
	registry := skills.NewRegistry()
	service, err := NewService(Config{Mode: ModeManual, AutogenDir: dir}, registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	return service, registry, dir
}

func historyCreate(t *testing.T, service *Service, name string) string {
	t.Helper()
	result, revision := service.CreateSkillRevisioned(context.Background(), CreateOptions{
		Name:          name,
		Description:   "original description",
		Instructions:  strings.Repeat("Original package instructions. ", 10),
		Tags:          []string{"original"},
		Category:      "testing",
		TriggerReason: TriggerManual,
	})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	if revision == "" {
		t.Fatal("create returned an empty revision")
	}
	return revision
}

func TestSkillHistoryViewStableRevisionAndExpectedRevision(t *testing.T) {
	service, _, _ := newHistoryTestService(t)
	initial := historyCreate(t, service, "stable-history")
	tool, _ := NewSkillManageTool(service)

	first, _ := tool.Execute(context.Background(), map[string]any{"action": "view", "name": "stable-history"})
	second, _ := tool.Execute(context.Background(), map[string]any{"action": "view", "name": "stable-history"})
	if !strings.Contains(first.Output, "Revision: "+initial) || first.Output != second.Output {
		t.Fatalf("view did not return a stable revision:\nfirst=%s\nsecond=%s", first.Output, second.Output)
	}

	missing, _ := tool.Execute(context.Background(), map[string]any{
		"action": "patch", "name": "stable-history", "description": "loser",
	})
	if !strings.Contains(missing.Output, "expected_revision is required") {
		t.Fatalf("missing expected_revision was not rejected: %s", missing.Output)
	}

	winner, winnerRevision := service.PatchSkillRevisioned(context.Background(), PatchOptions{
		Name: "stable-history", Description: "winner",
	}, initial)
	if winner.Error != nil {
		t.Fatal(winner.Error)
	}
	before, _ := os.ReadFile(winner.Path)
	loser, _ := service.PatchSkillRevisioned(context.Background(), PatchOptions{
		Name: "stable-history", Description: "stale loser",
	}, initial)
	if loser.Error == nil || !strings.Contains(loser.Error.Error(), "revision conflict") {
		t.Fatalf("stale caller was not rejected: %v", loser.Error)
	}
	after, _ := os.ReadFile(winner.Path)
	if string(after) != string(before) {
		t.Fatal("stale caller changed the winning package")
	}
	current, _ := service.CurrentRevision(context.Background(), "stable-history")
	if current != winnerRevision {
		t.Fatalf("stale caller changed HEAD: got %s want %s", current, winnerRevision)
	}
}

func TestSkillHistoryPatchAndUndoRestoresExactPackageAndRegistry(t *testing.T) {
	service, registry, dir := newHistoryTestService(t)
	initial := historyCreate(t, service, "patch-undo")
	skillPath := filepath.Join(dir, "patch-undo", "SKILL.md")
	before, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}

	patched, patchedRevision := service.PatchSkillRevisioned(context.Background(), PatchOptions{
		Name:         "patch-undo",
		Description:  "changed description",
		Instructions: "changed instructions",
		Tags:         []string{"changed"},
	}, initial)
	if patched.Error != nil {
		t.Fatal(patched.Error)
	}
	revertRevision, err := service.UndoSkill(context.Background(), "patch-undo", patchedRevision, "")
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(skillPath)
	if string(after) != string(before) {
		t.Fatalf("undo did not exactly restore SKILL.md\nbefore=%q\nafter=%q", before, after)
	}
	loaded, ok := registry.Get("patch-undo")
	if !ok || loaded.Metadata.Version != "1.0.0" ||
		loaded.Metadata.Description != "original description" ||
		!strings.Contains(loaded.Instructions, "Original package instructions") {
		t.Fatalf("registry was not refreshed after undo: %#v", loaded)
	}
	history, err := service.SkillHistory(context.Background(), "patch-undo")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) < 4 || history[0].ID != revertRevision || history[0].Action != "undo" ||
		history[0].RevertOf != initial || history[0].Parent != patchedRevision {
		t.Fatalf("undo did not append a provenance-bearing revision: %#v", history)
	}
}

func TestSkillHistorySupportFilesUndoModesAbsenceAndBlobDedupe(t *testing.T) {
	service, _, dir := newHistoryTestService(t)
	revision := historyCreate(t, service, "support-undo")
	path, createdRevision, err := service.WriteSupportFileRevisioned(
		context.Background(), "support-undo", "scripts/tool.sh", []byte("same bytes\n"), revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.WriteSupportFileRevisioned(
		context.Background(), "support-undo", "references/rejected.txt", []byte("bad\n"), createdRevision,
	); err == nil || !strings.Contains(err.Error(), "revision conflict") {
		t.Fatalf("out-of-band mode drift was not rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "support-undo", "references", "rejected.txt")); !os.IsNotExist(err) {
		t.Fatalf("drift conflict changed package: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	_, modeRevision, err := service.WriteSupportFileRevisioned(
		context.Background(), "support-undo", "references/copy.txt", []byte("same bytes\n"), createdRevision,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, overwriteRevision, err := service.WriteSupportFileRevisioned(
		context.Background(), "support-undo", "scripts/tool.sh", []byte("new bytes\n"), modeRevision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UndoSkill(context.Background(), "support-undo", overwriteRevision, modeRevision); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(data) != "same bytes\n" || info.Mode().Perm() != 0o644 {
		t.Fatalf("overwrite undo did not restore bytes/mode: %q %o", data, info.Mode().Perm())
	}

	current, _ := service.CurrentRevision(context.Background(), "support-undo")
	_, createOnlyRevision, err := service.WriteSupportFileRevisioned(
		context.Background(), "support-undo", "assets/new.txt", []byte("temporary"), current,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UndoSkill(context.Background(), "support-undo", createOnlyRevision, current); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "support-undo", "assets", "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("undo of support-file creation did not restore absence: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, ".history", "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if seen[entry.Name()] {
			t.Fatalf("duplicate content-addressed blob %s", entry.Name())
		}
		seen[entry.Name()] = true
	}
	// SKILL.md, "same bytes", "new bytes", and "temporary" are the only payloads.
	if len(entries) != 4 {
		t.Fatalf("blob store did not deduplicate identical support bytes: got %d blobs", len(entries))
	}
}

func TestSkillHistoryCreateArchiveUndoAndCuratorState(t *testing.T) {
	service, registry, dir := newHistoryTestService(t)
	curator := NewCurator(CuratorConfig{}, service.factory, filepath.Join(dir, ".curator_state"))
	service.SetCurator(curator)

	created := historyCreate(t, service, "placement-undo")
	if _, err := service.UndoSkill(context.Background(), "placement-undo", created, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "placement-undo")); !os.IsNotExist(err) {
		t.Fatalf("create undo did not remove package: %v", err)
	}
	if _, ok := registry.Get("placement-undo"); ok {
		t.Fatal("create undo did not unload registry")
	}
	if _, err := os.Stat(filepath.Join(dir, ".history")); err != nil {
		t.Fatalf("create undo removed history: %v", err)
	}

	recreated := historyCreate(t, service, "archive-undo")
	curator.MarkUsed("archive-undo")
	before := curator.GetState().SkillStates["archive-undo"]
	_, archived, err := service.ArchiveSkillRevisioned(
		context.Background(), "archive-undo", "", "obsolete", recreated,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UndoSkill(context.Background(), "archive-undo", archived, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "archive-undo", "SKILL.md")); err != nil {
		t.Fatalf("archive undo did not restore active package: %v", err)
	}
	if _, ok := registry.Get("archive-undo"); !ok {
		t.Fatal("archive undo did not restore registry")
	}
	after := curator.GetState().SkillStates["archive-undo"]
	if after.State != before.State || after.AbsorbedInto != before.AbsorbedInto ||
		after.ArchiveReason != before.ArchiveReason || after.ArchivedAt != nil {
		t.Fatalf("archive undo did not restore curator disposition: before=%#v after=%#v", before, after)
	}
}

func TestSkillHistoryRejectsUnsafePackagesAndPreviewPolicy(t *testing.T) {
	service, _, dir := newHistoryTestService(t)
	revision := historyCreate(t, service, "safe-history")
	before, _ := os.ReadFile(filepath.Join(dir, "safe-history", "SKILL.md"))
	if _, _, err := service.WriteSupportFileRevisioned(
		context.Background(), "safe-history", "../escape", []byte("bad"), revision,
	); err == nil {
		t.Fatal("path traversal was accepted")
	}
	if err := os.Symlink(filepath.Join(dir, "safe-history", "SKILL.md"), filepath.Join(dir, "safe-history", "references")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.WriteSupportFileRevisioned(
		context.Background(), "safe-history", "references/escape", []byte("bad"), revision,
	); err == nil || !strings.Contains(err.Error(), "symlink rejected") {
		t.Fatalf("symlink package entry was not rejected: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "safe-history", "SKILL.md"))
	head, _ := newHistoryStore(dir).readHead("safe-history")
	if string(after) != string(before) || head != revision {
		t.Fatal("failed transaction changed package or HEAD")
	}

	_ = os.Remove(filepath.Join(dir, "safe-history", "references"))
	preview, _ := NewPreviewSkillManageTool(service)
	historyResult, _ := preview.Execute(context.Background(), map[string]any{
		"action": "history", "name": "safe-history",
	})
	if !strings.Contains(historyResult.Output, revision) {
		t.Fatalf("preview denied or failed history: %s", historyResult.Output)
	}
	undoResult, _ := preview.Execute(context.Background(), map[string]any{
		"action": "undo", "name": "safe-history", "expected_revision": revision,
	})
	if !strings.Contains(undoResult.Output, "preview is read-only") {
		t.Fatalf("preview allowed undo: %s", undoResult.Output)
	}
}

func TestSkillHistoryPreviewLegacyIsFilesystemReadOnly(t *testing.T) {
	service, _, dir := newHistoryTestService(t)
	root := filepath.Join(dir, "legacy-preview")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: legacy-preview\ndescription: preview fixture\n---\nlegacy body\n"
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	before := filesystemTree(t, dir)
	preview, _ := NewPreviewSkillManageTool(service)
	for _, action := range []string{"view", "history"} {
		result, _ := preview.Execute(context.Background(), map[string]any{"action": action, "name": "legacy-preview"})
		if !strings.Contains(result.Output, "Revision") && !strings.Contains(result.Output, "untracked") {
			t.Fatalf("preview %s did not return computed revision: %s", action, result.Output)
		}
	}
	after := filesystemTree(t, dir)
	if before != after {
		t.Fatalf("preview changed filesystem\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := os.Stat(filepath.Join(dir, ".history")); !os.IsNotExist(err) {
		t.Fatalf("preview initialized history: %v", err)
	}
}

func TestSkillHistoryViewReconcilesDriftAndRejectsFrontmatterMismatch(t *testing.T) {
	service, _, dir := newHistoryTestService(t)
	revision := historyCreate(t, service, "consistent-view")
	path := filepath.Join(dir, "consistent-view", "SKILL.md")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(data, []byte("\nout-of-band\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	viewed, external, err := service.ViewSkillRevision(context.Background(), "consistent-view", false)
	if err != nil || external == revision || !strings.Contains(viewed.Instructions, "out-of-band") {
		t.Fatalf("view did not expose live drift with a new revision: old=%s new=%s err=%v", revision, external, err)
	}
	head, _ := newHistoryStore(dir).readHead("consistent-view")
	if head != revision {
		t.Fatalf("view wrote history: HEAD=%s want %s", head, revision)
	}
	stale, _ := service.PatchSkillRevisioned(context.Background(), PatchOptions{
		Name: "consistent-view", Description: "stale",
	}, revision)
	if stale.Error == nil || !strings.Contains(stale.Error.Error(), "revision conflict") {
		t.Fatalf("stale HEAD holder overwrote drift: %v", stale.Error)
	}
	reconciled, patchedRevision := service.PatchSkillRevisioned(context.Background(), PatchOptions{
		Name: "consistent-view", Description: "reconciled",
	}, external)
	if reconciled.Error != nil {
		t.Fatalf("explicit reconciliation failed: %v", reconciled.Error)
	}
	history, err := service.SkillHistory(context.Background(), "consistent-view")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) < 3 || history[0].ID != patchedRevision || history[0].Parent != external ||
		history[1].ID != external || history[1].Action != "external" || history[1].Parent != revision {
		t.Fatalf("external reconciliation chain is incorrect: %#v", history)
	}
	updated, _ := os.ReadFile(path)
	mismatch := strings.Replace(string(updated), "name: consistent-view", "name: different-name", 1)
	if err := os.WriteFile(path, []byte(mismatch), 0o644); err != nil {
		t.Fatal(err)
	}
	result, _ := service.PatchSkillRevisioned(context.Background(), PatchOptions{
		Name: "consistent-view", Description: "must fail",
	}, patchedRevision)
	if result.Error == nil || !strings.Contains(result.Error.Error(), "does not match package name") {
		t.Fatalf("frontmatter/package mismatch was accepted: %v", result.Error)
	}
}

func TestSkillHistoryRejectsOrphanUndoAndCorruption(t *testing.T) {
	service, _, dir := newHistoryTestService(t)
	initial := historyCreate(t, service, "validated-history")
	patched, current := service.PatchSkillRevisioned(context.Background(), PatchOptions{
		Name: "validated-history", Description: "current",
	}, initial)
	if patched.Error != nil {
		t.Fatal(patched.Error)
	}
	store := newHistoryStore(dir)
	orphan, err := store.capture("validated-history", "", "orphan", "", service.curator, true)
	if err != nil {
		t.Fatal(err)
	}
	orphanID, err := store.writeRevision(orphan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UndoSkill(context.Background(), "validated-history", current, orphanID); err == nil ||
		!strings.Contains(err.Error(), "not an ancestor") {
		t.Fatalf("orphan undo was accepted: %v", err)
	}

	target, err := store.loadRevision("validated-history", initial)
	if err != nil {
		t.Fatal(err)
	}
	blobPath := filepath.Join(dir, ".history", "blobs", target.Files[0].Blob)
	originalBlob, _ := os.ReadFile(blobPath)
	if err := os.Chmod(blobPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobPath, []byte("corrupt"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := store.apply(target, service.curator); err == nil || !strings.Contains(err.Error(), "corrupt blob") {
		t.Fatalf("corrupt blob was restored: %v", err)
	}
	if err := os.WriteFile(blobPath, originalBlob, 0o444); err != nil {
		t.Fatal(err)
	}

	manifestPath := store.revisionPath("validated-history", initial)
	manifestData, _ := os.ReadFile(manifestPath)
	if err := os.Chmod(manifestPath, 0o644); err != nil {
		t.Fatal(err)
	}
	corruptManifest := strings.Replace(string(manifestData), `"action": "create"`, `"action": "tampered"`, 1)
	if corruptManifest == string(manifestData) {
		t.Fatal("test could not locate manifest action")
	}
	if err := os.WriteFile(manifestPath, []byte(corruptManifest), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := store.loadRevision("validated-history", initial); err == nil ||
		!strings.Contains(err.Error(), "non-canonical") {
		t.Fatalf("non-canonical manifest was accepted: %v", err)
	}
}

func TestApplyRetainsRecoveryBackupWhenRollbackFails(t *testing.T) {
	service, _, dir := newHistoryTestService(t)
	revision := historyCreate(t, service, "rollback-retained")
	store := newHistoryStore(dir)
	manifest, err := store.loadRevision("rollback-retained", revision)
	if err != nil {
		t.Fatal(err)
	}
	originalRename := historyRename
	t.Cleanup(func() { historyRename = originalRename })
	calls := 0
	historyRename = func(oldPath, newPath string) error {
		calls++
		if calls == 2 {
			return errors.New("injected install failure")
		}
		if calls == 3 {
			return errors.New("injected rollback failure")
		}
		return os.Rename(oldPath, newPath)
	}
	err = store.apply(manifest, service.curator)
	if err == nil || !strings.Contains(err.Error(), "rollback incomplete") ||
		!strings.Contains(err.Error(), "recovery data retained at") {
		t.Fatalf("rollback failure did not report retained recovery path: %v", err)
	}
	backups, globErr := filepath.Glob(filepath.Join(dir, ".skill-transaction-rollback-retained-*", "placement-0"))
	if globErr != nil || len(backups) != 1 {
		t.Fatalf("original backup was deleted after incomplete rollback: paths=%v err=%v", backups, globErr)
	}
	if _, statErr := os.Stat(filepath.Join(backups[0], "SKILL.md")); statErr != nil {
		t.Fatalf("retained backup is not recoverable: %v", statErr)
	}
}

func TestStagedSupportWriteRetainsBackupWhenRollbackFails(t *testing.T) {
	service, _, dir := newHistoryTestService(t)
	historyCreate(t, service, "write-rollback")
	originalRename := historyRename
	t.Cleanup(func() { historyRename = originalRename })
	calls := 0
	historyRename = func(oldPath, newPath string) error {
		calls++
		if calls == 2 {
			return errors.New("injected install failure")
		}
		if calls == 3 {
			return errors.New("injected rollback failure")
		}
		return os.Rename(oldPath, newPath)
	}
	err := stagedSupportWrite(dir, "write-rollback", "references/new.md", []byte("new"))
	if err == nil || !strings.Contains(err.Error(), "rollback incomplete") {
		t.Fatalf("staged write did not report rollback failure: %v", err)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, ".skill-write-backup-write-rollback-*", "original"))
	if len(backups) != 1 {
		t.Fatalf("staged write deleted retained backup: %v", backups)
	}
	if _, statErr := os.Stat(filepath.Join(backups[0], "SKILL.md")); statErr != nil {
		t.Fatalf("staged write backup is not recoverable: %v", statErr)
	}
}

func TestSkillNamesRejectReservedAndInvalidIdentities(t *testing.T) {
	service, _, dir := newHistoryTestService(t)
	invalid := []string{"archive", ".history", "Uppercase", "with/slash", `with\slash`, "double--hyphen", "-leading", "trailing-", ".skill-transaction-x", "con", "com1", "lpt9"}
	for _, name := range invalid {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			opts := CreateOptions{
				Name: name, Description: "invalid", Instructions: "body", TriggerReason: TriggerManual,
			}
			if err := opts.Validate(); err == nil {
				t.Fatalf("CreateOptions accepted invalid name %q", name)
			}
			result, _ := service.CreateSkillRevisioned(context.Background(), opts)
			if result.Error == nil {
				t.Fatalf("revision service accepted invalid name %q", name)
			}
		})
	}
	if _, err := service.UndoSkill(context.Background(), "archive", strings.Repeat("0", 64), ""); err == nil {
		t.Fatal("undo targeted the archive container")
	}
	if _, err := os.Stat(filepath.Join(dir, "archive", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("invalid identity wrote into archive container: %v", err)
	}
}

func TestPinAndUnpinDoNotCauseHistoryDrift(t *testing.T) {
	service, _, dir := newHistoryTestService(t)
	curator := NewCurator(CuratorConfig{}, service.factory, filepath.Join(dir, ".curator_state"))
	service.SetCurator(curator)
	revision := historyCreate(t, service, "pin-stable")
	if err := curator.Pin("pin-stable"); err != nil {
		t.Fatal(err)
	}
	_, viewed, err := service.ViewSkillRevision(context.Background(), "pin-stable", false)
	if err != nil || viewed != revision {
		t.Fatalf("pin changed revision identity: got=%s want=%s err=%v", viewed, revision, err)
	}
	if err := curator.Unpin("pin-stable"); err != nil {
		t.Fatal(err)
	}
	patched, _ := service.PatchSkillRevisioned(context.Background(), PatchOptions{
		Name: "pin-stable", Description: "pin state did not drift",
	}, revision)
	if patched.Error != nil {
		t.Fatalf("unpin caused a drift conflict: %v", patched.Error)
	}
}

func TestCuratorArchiveIsAdoptedOnlyAfterReview(t *testing.T) {
	service, _, dir := newHistoryTestService(t)
	curator := NewCurator(CuratorConfig{}, service.factory, filepath.Join(dir, ".curator_state"))
	service.SetCurator(curator)
	revision := historyCreate(t, service, "curator-external")
	if _, err := curator.ArchiveWithReason(dir, "curator-external", "", "automatic"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UndoSkill(context.Background(), "curator-external", revision, ""); err == nil ||
		!strings.Contains(err.Error(), "revision conflict") {
		t.Fatalf("stale HEAD holder adopted curator archive: %v", err)
	}
	viewed, external, err := service.ViewSkillRevision(context.Background(), "curator-external", false)
	if err != nil || viewed.Metadata.Name != "curator-external" || external == revision {
		t.Fatalf("archived live state was not reviewable: rev=%s err=%v", external, err)
	}
	undoRevision, err := service.UndoSkill(context.Background(), "curator-external", external, "")
	if err != nil {
		t.Fatalf("reviewed curator archive could not be reconciled and undone: %v", err)
	}
	history, err := service.SkillHistory(context.Background(), "curator-external")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) < 3 || history[0].ID != undoRevision || history[0].Parent != external ||
		history[1].ID != external || history[1].Action != "external" || history[1].Parent != revision {
		t.Fatalf("curator archive reconciliation chain is incorrect: %#v", history)
	}
	if _, err := os.Stat(filepath.Join(dir, "curator-external", "SKILL.md")); err != nil {
		t.Fatalf("undo did not restore curator-archived package: %v", err)
	}
}

func TestExistingBlobAndRevisionAreValidatedBeforeDedupe(t *testing.T) {
	service, _, dir := newHistoryTestService(t)
	revision := historyCreate(t, service, "dedupe-integrity")
	store := newHistoryStore(dir)
	manifest, err := store.loadRevision("dedupe-integrity", revision)
	if err != nil {
		t.Fatal(err)
	}
	revisionPath := store.revisionPath("dedupe-integrity", revision)
	revisionBytes, _ := os.ReadFile(revisionPath)
	if err := os.Chmod(revisionPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(revisionPath, append(revisionBytes, []byte("tampered")...), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := store.writeRevision(manifest); err == nil || !strings.Contains(err.Error(), "integrity validation") {
		t.Fatalf("corrupt existing revision was trusted: %v", err)
	}
	if err := os.WriteFile(revisionPath, revisionBytes, 0o444); err != nil {
		t.Fatal(err)
	}
	blobPath := filepath.Join(dir, ".history", "blobs", manifest.Files[0].Blob)
	if err := os.Chmod(blobPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobPath, []byte("corrupt"), 0o444); err != nil {
		t.Fatal(err)
	}
	headBefore, _ := store.readHead("dedupe-integrity")
	result, _ := service.PatchSkillRevisioned(context.Background(), PatchOptions{
		Name: "dedupe-integrity", Description: "must not publish",
	}, revision)
	if result.Error == nil || !strings.Contains(result.Error.Error(), "existing blob") {
		t.Fatalf("corrupt existing blob was trusted: %v", result.Error)
	}
	headAfter, _ := store.readHead("dedupe-integrity")
	if headAfter != headBefore {
		t.Fatalf("HEAD changed despite corrupt dedupe target: before=%s after=%s", headBefore, headAfter)
	}
}

func TestPreviewReadFileUsesImmutableLiveSnapshot(t *testing.T) {
	service, _, dir := newHistoryTestService(t)
	revision := historyCreate(t, service, "snapshot-read")
	if _, _, err := service.WriteSupportFileRevisioned(context.Background(), "snapshot-read", "references/live.md", []byte("initial"), revision); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "snapshot-read", "references", "live.md"), []byte("manual"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := filesystemTree(t, dir)
	preview, _ := NewPreviewSkillManageTool(service)
	result, _ := preview.Execute(context.Background(), map[string]any{
		"action": "read_file", "name": "snapshot-read", "file_path": "references/live.md",
	})
	if !strings.Contains(result.Output, "manual") || !strings.Contains(result.Output, "live external/untracked state") {
		t.Fatalf("read_file did not return the verified live snapshot and revision note: %s", result.Output)
	}
	after := filesystemTree(t, dir)
	if before != after {
		t.Fatalf("preview read_file mutated the filesystem\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestHistoryCaptureRejectsOversizedFile(t *testing.T) {
	service, _, dir := newHistoryTestService(t)
	revision := historyCreate(t, service, "capture-limit")
	path := filepath.Join(dir, "capture-limit", "assets", "large.bin")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, maxHistoryFileSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	result, _ := service.PatchSkillRevisioned(context.Background(), PatchOptions{
		Name: "capture-limit", Description: "must fail",
	}, revision)
	if result.Error == nil || !strings.Contains(result.Error.Error(), "exceeds") {
		t.Fatalf("oversized package capture was not rejected: %v", result.Error)
	}
}

func TestReadOnlyHistoryEntriesRejectUnsafeNames(t *testing.T) {
	service, _, dir := newHistoryTestService(t)
	before := filesystemTree(t, dir)
	invalid := "../escape"

	if _, err := service.ViewSkill(invalid); err == nil {
		t.Fatal("ViewSkill accepted an unsafe name")
	}
	if _, err := service.CurrentRevisionReadOnly(invalid); err == nil {
		t.Fatal("CurrentRevisionReadOnly accepted an unsafe name")
	}
	if _, _, err := service.ViewSkillRevision(context.Background(), invalid, true); err == nil {
		t.Fatal("ViewSkillRevision accepted an unsafe name")
	}
	if _, _, _, err := service.ViewSkillRevisionSnapshot(context.Background(), invalid, true); err == nil {
		t.Fatal("ViewSkillRevisionSnapshot accepted an unsafe name")
	}
	if _, err := service.SkillHistoryReadOnly(invalid); err == nil {
		t.Fatal("SkillHistoryReadOnly accepted an unsafe name")
	}
	if _, err := service.SkillHistory(context.Background(), invalid); err == nil {
		t.Fatal("SkillHistory accepted an unsafe name")
	}
	if _, _, _, err := service.readSupportFileRevisionSnapshot(
		context.Background(), invalid, "references/data.txt", true,
	); err == nil {
		t.Fatal("readSupportFileRevisionSnapshot accepted an unsafe name")
	}
	preview, err := NewPreviewSkillManageTool(service)
	if err != nil {
		t.Fatal(err)
	}
	for _, params := range []map[string]any{
		{"action": "view", "name": invalid},
		{"action": "history", "name": invalid},
		{"action": "read_file", "name": invalid, "file_path": "references/data.txt"},
	} {
		result, executeErr := preview.Execute(context.Background(), params)
		if executeErr != nil {
			t.Fatal(executeErr)
		}
		if !strings.Contains(strings.ToLower(result.Output), "invalid skill name") {
			t.Fatalf("preview action %q did not reject unsafe name: %s", params["action"], result.Output)
		}
	}
	if after := filesystemTree(t, dir); after != before {
		t.Fatalf("unsafe read-only calls changed the filesystem\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestSkillHistoryReportsLiveDriftWithoutPersisting(t *testing.T) {
	service, _, dir := newHistoryTestService(t)
	head := historyCreate(t, service, "history-drift")
	path := filepath.Join(dir, "history-drift", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "original description", "external description", 1))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	before := filesystemTree(t, filepath.Join(dir, ".history"))

	locked, err := service.SkillHistory(context.Background(), "history-drift")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.SkillHistoryReadOnly("history-drift")
	if err != nil {
		t.Fatal(err)
	}
	for label, revisions := range map[string][]SkillRevision{"locked": locked, "preview": preview} {
		if len(revisions) < 2 || revisions[0].Action != "external" ||
			revisions[0].Parent != head || revisions[1].ID != head {
			t.Fatalf("%s history did not expose live drift: %#v", label, revisions)
		}
	}
	if locked[0].ID != preview[0].ID {
		t.Fatalf("drift revision was not deterministic: locked=%s preview=%s", locked[0].ID, preview[0].ID)
	}
	current, err := newHistoryStore(dir).readHead("history-drift")
	if err != nil || current != head {
		t.Fatalf("history read persisted drift: HEAD=%s err=%v", current, err)
	}
	if after := filesystemTree(t, filepath.Join(dir, ".history")); after != before {
		t.Fatalf("history read persisted files\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestCurrentRevisionReportsDeterministicLiveDrift(t *testing.T) {
	service, _, dir := newHistoryTestService(t)
	head := historyCreate(t, service, "current-drift")
	path := filepath.Join(dir, "current-drift", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("\nexternal edit\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	before := filesystemTree(t, filepath.Join(dir, ".history"))

	locked, err := service.CurrentRevision(context.Background(), "current-drift")
	if err != nil {
		t.Fatal(err)
	}
	readOnly, err := service.CurrentRevisionReadOnly("current-drift")
	if err != nil {
		t.Fatal(err)
	}
	_, viewed, err := service.ViewSkillRevision(context.Background(), "current-drift", true)
	if err != nil {
		t.Fatal(err)
	}
	history, err := service.SkillHistoryReadOnly("current-drift")
	if err != nil {
		t.Fatal(err)
	}
	if locked == head || locked != readOnly || locked != viewed || len(history) < 1 || locked != history[0].ID {
		t.Fatalf("current revision did not report deterministic drift: head=%s locked=%s readonly=%s view=%s history=%#v",
			head, locked, readOnly, viewed, history)
	}
	current, err := newHistoryStore(dir).readHead("current-drift")
	if err != nil || current != head {
		t.Fatalf("current revision persisted drift: HEAD=%s err=%v", current, err)
	}
	if after := filesystemTree(t, filepath.Join(dir, ".history")); after != before {
		t.Fatalf("current revision persisted files\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestHistoryReadsRejectOversizedAndUnsafeObjects(t *testing.T) {
	service, _, dir := newHistoryTestService(t)
	revision := historyCreate(t, service, "bounded-objects")
	store := newHistoryStore(dir)
	manifest, err := store.loadRevision("bounded-objects", revision)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("symlink manifest", func(t *testing.T) {
		symlinkRevision := historyCreate(t, service, "unsafe-manifest")
		path := store.revisionPath("unsafe-manifest", symlinkRevision)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, "manifest-target")
		if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := store.loadRevision("unsafe-manifest", symlinkRevision); err == nil ||
			!strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("symlink manifest was accepted: %v", err)
		}
	})

	t.Run("oversized manifest", func(t *testing.T) {
		path := store.revisionPath("bounded-objects", revision)
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(maxHistoryManifestSize + 1); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := store.loadRevision("bounded-objects", revision); err == nil ||
			!strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("oversized manifest was accepted: %v", err)
		}
	})

	t.Run("unsafe dedupe blob", func(t *testing.T) {
		blobPath := filepath.Join(dir, ".history", "blobs", manifest.Files[0].Blob)
		if err := os.Remove(blobPath); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, "symlink-target")
		if err := os.WriteFile(target, []byte("target"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, blobPath); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := store.capture("bounded-objects", revision, "test", "", service.curator, true); err == nil ||
			!strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("symlink dedupe blob was accepted: %v", err)
		}
		if err := os.Remove(blobPath); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(blobPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(maxHistoryFileSize + 1); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := store.capture("bounded-objects", revision, "test", "", service.curator, true); err == nil ||
			!strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("oversized dedupe blob was accepted: %v", err)
		}
	})
}

func TestHistoryManifestAndBlobRestoreLimits(t *testing.T) {
	blob := strings.Repeat("a", 64)
	base := revisionManifest{
		Format: historyFormatVersion, Skill: "restore-limits", Action: "test", Placement: "active",
	}

	tooMany := base
	for index := 0; index <= maxHistoryPackageFiles; index++ {
		tooMany.Files = append(tooMany.Files, revisionFile{
			Path: fmt.Sprintf("references/%04d", index), Mode: 0o644, Blob: blob, Size: 1,
		})
	}
	if err := validateManifest(&tooMany); err == nil || !strings.Contains(err.Error(), "file limit") {
		t.Fatalf("oversized manifest file count was accepted: %v", err)
	}

	tooLarge := base
	tooLarge.Files = []revisionFile{{Path: "SKILL.md", Mode: 0o644, Blob: blob, Size: maxHistoryFileSize + 1}}
	if err := validateManifest(&tooLarge); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("oversized manifest file was accepted: %v", err)
	}

	aggregate := base
	for index := 0; index < maxHistoryPackageSize/maxHistoryFileSize+1; index++ {
		aggregate.Files = append(aggregate.Files, revisionFile{
			Path: fmt.Sprintf("assets/%02d", index), Mode: 0o644, Blob: blob, Size: maxHistoryFileSize,
		})
	}
	if err := validateManifest(&aggregate); err == nil || !strings.Contains(err.Error(), "package limit") {
		t.Fatalf("oversized manifest package was accepted: %v", err)
	}

	dir := t.TempDir()
	store := newHistoryStore(dir)
	if err := os.MkdirAll(filepath.Join(dir, ".history", "blobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	blobPath := filepath.Join(dir, ".history", "blobs", blob)
	file, err := os.Create(blobPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxHistoryFileSize + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	restore := base
	restore.Files = []revisionFile{{Path: "SKILL.md", Mode: 0o644, Blob: blob, Size: 1}}
	if err := store.apply(&restore, nil); err == nil || !strings.Contains(err.Error(), "corrupt blob") {
		t.Fatalf("oversized restore blob was accepted: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".skill-restore-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("restore allocated staging before blob validation: matches=%v err=%v", matches, err)
	}
}

func TestStagedSupportWriteEnforcesPackageLimits(t *testing.T) {
	t.Run("file count", func(t *testing.T) {
		dir := t.TempDir()
		root := filepath.Join(dir, "staging-count")
		if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
			t.Fatal(err)
		}
		for index := 0; index < maxHistoryPackageFiles; index++ {
			if err := os.WriteFile(filepath.Join(root, "assets", fmt.Sprintf("%04d", index)), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		err := stagedSupportWrite(dir, "staging-count", "references/new.txt", []byte("new"))
		if err == nil || !strings.Contains(err.Error(), "file limit") {
			t.Fatalf("staged file-count limit was not enforced: %v", err)
		}
	})

	t.Run("per file", func(t *testing.T) {
		dir := t.TempDir()
		root := filepath.Join(dir, "staging-file", "assets")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(root, "large.bin"))
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(maxHistoryFileSize + 1); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		err = stagedSupportWrite(dir, "staging-file", "references/new.txt", []byte("new"))
		if err == nil || !strings.Contains(err.Error(), "file") || !strings.Contains(err.Error(), "limit") {
			t.Fatalf("staged per-file limit was not enforced: %v", err)
		}
	})

	t.Run("aggregate", func(t *testing.T) {
		dir := t.TempDir()
		root := filepath.Join(dir, "staging-total", "assets")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		for index := 0; index < maxHistoryPackageSize/maxHistoryFileSize+1; index++ {
			file, err := os.Create(filepath.Join(root, fmt.Sprintf("%02d.bin", index)))
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Truncate(maxHistoryFileSize); err != nil {
				file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		}
		err := stagedSupportWrite(dir, "staging-total", "references/new.txt", []byte("new"))
		if err == nil || !strings.Contains(err.Error(), "byte limit") {
			t.Fatalf("staged aggregate limit was not enforced: %v", err)
		}
	})
}
