package autogenskills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

const (
	historyFormatVersion   = 1
	maxHistoryManifestSize = 1 << 20
	maxHistoryFileSize     = 8 << 20
	maxHistoryPackageSize  = 64 << 20
	maxHistoryPackageFiles = 1024
)

var (
	historyRename    = os.Rename
	historyRemoveAll = os.RemoveAll
)

type revisionFile struct {
	Path string      `json:"path"`
	Mode fs.FileMode `json:"mode"`
	Blob string      `json:"blob"`
	Size int64       `json:"size"`
}

type revisionManifest struct {
	Format      int            `json:"format"`
	ID          string         `json:"id,omitempty"`
	Skill       string         `json:"skill"`
	Parent      string         `json:"parent,omitempty"`
	Action      string         `json:"action"`
	RevertOf    string         `json:"revert_of,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	Placement   string         `json:"placement"`
	Files       []revisionFile `json:"files,omitempty"`
	CuratorSet  bool           `json:"curator_set,omitempty"`
	CuratorMeta SkillMeta      `json:"curator_meta,omitempty"`
}

// SkillRevision is the read-only public history summary returned by Service.
type SkillRevision struct {
	ID        string    `json:"id"`
	Parent    string    `json:"parent,omitempty"`
	Action    string    `json:"action"`
	RevertOf  string    `json:"revert_of,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Placement string    `json:"placement"`
}

type historyStore struct {
	root       string
	autogenDir string
}

func newHistoryStore(autogenDir string) *historyStore {
	return &historyStore{root: filepath.Join(autogenDir, ".history"), autogenDir: autogenDir}
}

func (h *historyStore) withLock(ctx context.Context, name string, fn func() error) error {
	if !isSafeSkillDirName(name) {
		return fmt.Errorf("autogenskills: unsafe skill name %q", name)
	}
	lockDir := filepath.Join(h.root, "locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(lockDir, name+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		ok, lockErr := tryLockHistoryFile(f)
		if lockErr != nil {
			return fmt.Errorf("autogenskills: lock history for %q: %w", name, lockErr)
		}
		if ok {
			defer unlockHistoryFile(f)
			return fn()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("autogenskills: wait for history lock for %q: %w", name, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (h *historyStore) headPath(name string) string {
	return filepath.Join(h.root, "heads", name)
}

func (h *historyStore) readHead(name string) (string, error) {
	if !isSafeSkillDirName(name) {
		return "", fmt.Errorf("autogenskills: invalid skill name %q", name)
	}
	data, err := os.ReadFile(h.headPath(name))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(data))
	if !isHistoryID(id) {
		return "", fmt.Errorf("autogenskills: corrupt history HEAD for %q", name)
	}
	return id, nil
}

func isHistoryID(id string) bool {
	if len(id) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func atomicWrite(path string, data []byte, mode fs.FileMode) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(parent, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Chmod(mode)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = replaceHistoryFile(tmpName, path)
	}
	if err == nil {
		err = syncHistoryDir(parent)
	}
	return err
}

func readRegularFileBounded(path string, maxSize int64, description string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("autogenskills: %s is not a regular file", description)
	}
	if info.Size() > maxSize {
		return nil, fmt.Errorf("autogenskills: %s exceeds %d-byte limit", description, maxSize)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !openedInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("autogenskills: %s is not a regular file", description)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("autogenskills: %s grew beyond %d-byte limit while being read", description, maxSize)
	}
	return data, nil
}

func (h *historyStore) publishHead(name, id string) error {
	return atomicWrite(h.headPath(name), []byte(id+"\n"), 0o644)
}

func (h *historyStore) revisionPath(name, id string) string {
	return filepath.Join(h.root, "revisions", name, id+".json")
}

func (h *historyStore) loadRevision(name, id string) (*revisionManifest, error) {
	if !isSafeSkillDirName(name) || !isHistoryID(id) {
		return nil, fmt.Errorf("autogenskills: invalid revision identifier %q", id)
	}
	data, err := readRegularFileBounded(
		h.revisionPath(name, id), maxHistoryManifestSize, fmt.Sprintf("revision manifest %s", id),
	)
	if err != nil {
		return nil, err
	}
	var manifest revisionManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("autogenskills: decode revision %s: %w", id, err)
	}
	if manifest.ID != id || manifest.Skill != name {
		return nil, fmt.Errorf("autogenskills: corrupt revision %s", id)
	}
	if err := validateManifest(&manifest); err != nil {
		return nil, fmt.Errorf("autogenskills: corrupt revision %s: %w", id, err)
	}
	canonical := manifest
	canonical.ID = ""
	canonicalData, err := json.Marshal(&canonical)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(canonicalData)
	if hex.EncodeToString(sum[:]) != id {
		return nil, fmt.Errorf("autogenskills: corrupt revision %s: non-canonical revision hash", id)
	}
	return &manifest, nil
}

func validateManifest(manifest *revisionManifest) error {
	if manifest.Format != historyFormatVersion {
		return fmt.Errorf("unsupported format %d", manifest.Format)
	}
	if !isSafeSkillDirName(manifest.Skill) {
		return fmt.Errorf("unsafe skill name %q", manifest.Skill)
	}
	if manifest.Action == "" || (manifest.Parent != "" && !isHistoryID(manifest.Parent)) ||
		(manifest.RevertOf != "" && !isHistoryID(manifest.RevertOf)) {
		return errors.New("invalid revision provenance")
	}
	switch manifest.Placement {
	case "active", "archived":
		if len(manifest.Files) == 0 {
			return errors.New("placed revision has no files")
		}
	case "absent":
		if len(manifest.Files) != 0 {
			return errors.New("absent revision contains files")
		}
	default:
		return fmt.Errorf("unsupported placement %q", manifest.Placement)
	}
	if len(manifest.Files) > maxHistoryPackageFiles {
		return fmt.Errorf("revision exceeds %d-file limit", maxHistoryPackageFiles)
	}
	seen := make(map[string]struct{}, len(manifest.Files))
	var packageBytes int64
	for _, file := range manifest.Files {
		if filepath.ToSlash(filepath.Clean(filepath.FromSlash(file.Path))) != file.Path ||
			file.Path == "." || filepath.IsAbs(filepath.FromSlash(file.Path)) ||
			strings.HasPrefix(file.Path, "../") {
			return fmt.Errorf("unsafe file path %q", file.Path)
		}
		if file.Mode != file.Mode.Perm() || !isHistoryID(file.Blob) {
			return fmt.Errorf("invalid file metadata for %q", file.Path)
		}
		if file.Size < 0 || file.Size > maxHistoryFileSize {
			return fmt.Errorf("file %q size %d exceeds %d-byte limit", file.Path, file.Size, maxHistoryFileSize)
		}
		if packageBytes > maxHistoryPackageSize-file.Size {
			return fmt.Errorf("revision exceeds %d-byte package limit", maxHistoryPackageSize)
		}
		packageBytes += file.Size
		parts := strings.Split(file.Path, "/")
		for i := 1; i < len(parts); i++ {
			if _, ok := seen[strings.Join(parts[:i], "/")]; ok {
				return fmt.Errorf("path prefix conflict at %q", file.Path)
			}
		}
		if _, ok := seen[file.Path]; ok {
			return fmt.Errorf("duplicate file path %q", file.Path)
		}
		for existing := range seen {
			if strings.HasPrefix(existing, file.Path+"/") {
				return fmt.Errorf("path prefix conflict at %q", file.Path)
			}
		}
		seen[file.Path] = struct{}{}
	}
	return nil
}

func (h *historyStore) writeRevision(manifest *revisionManifest) (string, error) {
	manifest.Format = historyFormatVersion
	manifest.ID = ""
	if err := validateManifest(manifest); err != nil {
		return "", err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	id := hex.EncodeToString(sum[:])
	manifest.ID = id
	data, err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	path := h.revisionPath(manifest.Skill, id)
	if _, err := os.Stat(path); err == nil {
		if _, loadErr := h.loadRevision(manifest.Skill, id); loadErr != nil {
			return "", fmt.Errorf("autogenskills: existing revision %s failed integrity validation: %w", id, loadErr)
		}
		return id, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := atomicWrite(path, append(data, '\n'), 0o444); err != nil {
		return "", err
	}
	return id, nil
}

func (h *historyStore) placementPath(name, placement string) string {
	if placement == "archived" {
		return filepath.Join(h.autogenDir, "archive", name)
	}
	return filepath.Join(h.autogenDir, name)
}

type placementOperation uint8

const (
	placementOperationMutation placementOperation = iota
	placementOperationRead
)

func inspectPlacement(autogenDir, name string, operation placementOperation) (string, string, error) {
	active := filepath.Join(autogenDir, name)
	archived := filepath.Join(autogenDir, "archive", name)
	activeInfo, activeErr := os.Lstat(active)
	archiveInfo, archiveErr := os.Lstat(archived)
	if activeErr != nil && !os.IsNotExist(activeErr) {
		return "", "", activeErr
	}
	if archiveErr != nil && !os.IsNotExist(archiveErr) {
		return "", "", archiveErr
	}
	if activeErr == nil {
		if activeInfo.Mode()&os.ModeSymlink != 0 || !activeInfo.IsDir() {
			if archiveErr == nil {
				return "", "", fmt.Errorf(
					"autogenskills: active placement %q for skill %q must be a real directory; archive placement is %q; remove or replace the unsafe active path %q before retrying, or remove archive path %q only if that archived copy is intentionally discarded",
					active, name, archived, active, archived,
				)
			}
			return "", "", fmt.Errorf("autogenskills: skill %q must be a real directory", name)
		}
	}
	if archiveErr == nil {
		if archiveInfo.Mode()&os.ModeSymlink != 0 || !archiveInfo.IsDir() {
			if activeErr == nil {
				return "", "", fmt.Errorf(
					"autogenskills: archive placement %q for skill %q must be a real directory; active placement is %q; remove or replace the unsafe archive path %q before retrying, or remove active path %q only if that active copy is intentionally discarded",
					archived, name, active, archived, active,
				)
			}
			return "", "", fmt.Errorf("autogenskills: archived skill %q must be a real directory", name)
		}
	}
	if activeErr == nil && archiveErr == nil {
		if operation == placementOperationRead {
			return "active", active, nil
		}
		return "", "", fmt.Errorf(
			"autogenskills: skill %q exists in both active placement %q and archive placement %q; remove archive path %q to keep the active copy, or remove active path %q to keep the archived copy",
			name, active, archived, archived, active,
		)
	}
	if activeErr == nil {
		return "active", active, nil
	}
	if archiveErr == nil {
		return "archived", archived, nil
	}
	return "absent", "", nil
}

func revisionMeta(meta SkillMeta) SkillMeta {
	// Operational curator data does not affect package identity. Placement and
	// archive disposition are sufficient to restore lifecycle state.
	meta.State = ""
	meta.Pinned = false
	meta.Version = ""
	meta.LastUsedAt = time.Time{}
	meta.CreatedAt = time.Time{}
	return meta
}

func (h *historyStore) capture(name, parent, action, revertOf string, curator *Curator, persistBlobs bool) (*revisionManifest, error) {
	return h.captureForOperation(name, parent, action, revertOf, curator, persistBlobs, placementOperationMutation)
}

func (h *historyStore) captureRead(name, parent, action, revertOf string, curator *Curator) (*revisionManifest, error) {
	return h.captureForOperation(name, parent, action, revertOf, curator, false, placementOperationRead)
}

func (h *historyStore) captureForOperation(name, parent, action, revertOf string, curator *Curator, persistBlobs bool, operation placementOperation) (*revisionManifest, error) {
	if !isSafeSkillDirName(name) {
		return nil, fmt.Errorf("autogenskills: invalid skill name %q", name)
	}
	placement, root, err := inspectPlacement(h.autogenDir, name, operation)
	if err != nil {
		return nil, err
	}
	manifest := &revisionManifest{
		Skill: name, Parent: parent, Action: action, RevertOf: revertOf,
		CreatedAt: time.Now().UTC(), Placement: placement,
	}
	if curator != nil {
		curator.mu.Lock()
		manifest.CuratorMeta, manifest.CuratorSet = curator.state.SkillStates[name]
		curator.mu.Unlock()
		manifest.CuratorMeta = revisionMeta(manifest.CuratorMeta)
		if manifest.CuratorSet && manifest.CuratorMeta.ArchivedAt == nil && manifest.CuratorMeta.AbsorbedInto == "" &&
			manifest.CuratorMeta.ArchiveReason == "" {
			manifest.CuratorSet = false
			manifest.CuratorMeta = SkillMeta{}
		}
	}
	if placement == "absent" {
		return manifest, nil
	}
	var packageBytes int64
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("autogenskills: invalid package path %q", path)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("autogenskills: symlink rejected in skill package: %s", filepath.ToSlash(relative))
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("autogenskills: unsupported filesystem entry in skill package: %s", filepath.ToSlash(relative))
		}
		if info.Size() > maxHistoryFileSize {
			return fmt.Errorf("autogenskills: history capture rejected %s: file size %d exceeds %d-byte limit", filepath.ToSlash(relative), info.Size(), maxHistoryFileSize)
		}
		if len(manifest.Files) >= maxHistoryPackageFiles {
			return fmt.Errorf("autogenskills: history capture rejected package %q: exceeds %d-file limit", name, maxHistoryPackageFiles)
		}
		data, err := readRegularFileBounded(path, maxHistoryFileSize, "history capture file "+filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		packageBytes += int64(len(data))
		if packageBytes > maxHistoryPackageSize {
			return fmt.Errorf("autogenskills: history capture rejected package %q: exceeds %d-byte limit", name, maxHistoryPackageSize)
		}
		sum := sha256.Sum256(data)
		blob := hex.EncodeToString(sum[:])
		if persistBlobs {
			blobPath := filepath.Join(h.root, "blobs", blob)
			if _, err := os.Lstat(blobPath); os.IsNotExist(err) {
				if err := atomicWrite(blobPath, data, 0o444); err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else {
				existing, readErr := readRegularFileBounded(
					blobPath, maxHistoryFileSize, "existing blob "+blob,
				)
				if readErr != nil {
					return readErr
				}
				existingSum := sha256.Sum256(existing)
				if hex.EncodeToString(existingSum[:]) != blob {
					return fmt.Errorf("autogenskills: existing blob %s failed integrity validation", blob)
				}
			}
		}
		manifest.Files = append(manifest.Files, revisionFile{
			Path: filepath.ToSlash(relative), Mode: info.Mode().Perm(), Blob: blob, Size: int64(len(data)),
		})
		return nil
	})
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	if err == nil && placement != "absent" {
		data, readErr := readRegularFileBounded(
			filepath.Join(root, "SKILL.md"), maxHistoryFileSize, "SKILL.md for "+name,
		)
		if readErr != nil {
			return nil, fmt.Errorf("autogenskills: read SKILL.md for %q: %w", name, readErr)
		}
		meta, _, _, parseErr := skills.ParseSkillMDContent(data)
		if parseErr != nil {
			return nil, fmt.Errorf("autogenskills: parse SKILL.md for %q: %w", name, parseErr)
		}
		if meta.Name != name {
			return nil, fmt.Errorf("autogenskills: SKILL.md name %q does not match package name %q", meta.Name, name)
		}
	}
	return manifest, err
}

func (h *historyStore) ensureBaseline(name string, curator *Curator) (string, error) {
	head, err := h.readHead(name)
	if err != nil || head != "" {
		return head, err
	}
	baseline, err := h.capture(name, "", "baseline", "", curator, true)
	if err != nil {
		return "", err
	}
	baseline.CreatedAt = time.Time{}
	id, err := h.writeRevision(baseline)
	if err != nil {
		return "", err
	}
	if err := h.publishHead(name, id); err != nil {
		return "", err
	}
	return id, nil
}

func (h *historyStore) apply(manifest *revisionManifest, curator *Curator) error {
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if err := h.validateRestoreBlobs(manifest); err != nil {
		return err
	}
	active := filepath.Join(h.autogenDir, manifest.Skill)
	archived := filepath.Join(h.autogenDir, "archive", manifest.Skill)
	var stage string
	retainRecovery := false
	if manifest.Placement != "absent" {
		var err error
		stage, err = os.MkdirTemp(h.autogenDir, ".skill-restore-"+manifest.Skill+"-*")
		if err != nil {
			return err
		}
		defer func() {
			if stage != "" && !retainRecovery {
				_ = historyRemoveAll(stage)
			}
		}()
		for _, file := range manifest.Files {
			clean := filepath.Clean(filepath.FromSlash(file.Path))
			if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return fmt.Errorf("autogenskills: corrupt revision path %q", file.Path)
			}
			if !isHistoryID(file.Blob) {
				return fmt.Errorf("autogenskills: corrupt blob identifier %q", file.Blob)
			}
			data, err := readRegularFileBounded(
				filepath.Join(h.root, "blobs", file.Blob), maxHistoryFileSize, "blob "+file.Blob,
			)
			if err != nil {
				return err
			}
			if int64(len(data)) != file.Size || len(data) > maxHistoryFileSize {
				return fmt.Errorf("autogenskills: blob %s size changed while restoring %s", file.Blob, file.Path)
			}
			sum := sha256.Sum256(data)
			if hex.EncodeToString(sum[:]) != file.Blob {
				return fmt.Errorf("autogenskills: corrupt blob %s for %s", file.Blob, file.Path)
			}
			target := filepath.Join(stage, clean)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(target, data, file.Mode.Perm()); err != nil {
				return err
			}
		}
	}
	backupRoot, err := os.MkdirTemp(h.autogenDir, ".skill-transaction-"+manifest.Skill+"-*")
	if err != nil {
		return err
	}
	defer func() {
		if !retainRecovery {
			_ = historyRemoveAll(backupRoot)
		}
	}()
	type movedPath struct{ original, backup string }
	var backups []movedPath
	rollbackPlacements := func() error {
		var rollbackErr error
		for _, path := range []string{active, archived} {
			if _, statErr := os.Lstat(path); statErr == nil {
				trash := filepath.Join(backupRoot, "failed-"+filepath.Base(filepath.Dir(path))+"-"+filepath.Base(path))
				if renameErr := historyRename(path, trash); renameErr != nil {
					rollbackErr = errors.Join(rollbackErr, renameErr)
				}
			} else if !os.IsNotExist(statErr) {
				rollbackErr = errors.Join(rollbackErr, statErr)
			}
		}
		for index := len(backups) - 1; index >= 0; index-- {
			if renameErr := historyRename(backups[index].backup, backups[index].original); renameErr != nil {
				rollbackErr = errors.Join(rollbackErr, renameErr)
			}
		}
		return rollbackErr
	}
	failTransaction := func(primary error) error {
		rollbackErr := rollbackPlacements()
		if rollbackErr == nil {
			return primary
		}
		retainRecovery = true
		return errors.Join(primary, fmt.Errorf("autogenskills: rollback incomplete; recovery data retained at %s: %w", backupRoot, rollbackErr))
	}
	for index, path := range []string{active, archived} {
		info, statErr := os.Lstat(path)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return failTransaction(statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return failTransaction(fmt.Errorf("autogenskills: refusing to replace unsafe package path %s", path))
		}
		backup := filepath.Join(backupRoot, fmt.Sprintf("placement-%d", index))
		if renameErr := historyRename(path, backup); renameErr != nil {
			return failTransaction(renameErr)
		}
		backups = append(backups, movedPath{path, backup})
	}
	if manifest.Placement != "absent" {
		destination := active
		if manifest.Placement == "archived" {
			destination = archived
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return failTransaction(err)
		}
		if err := historyRename(stage, destination); err != nil {
			return failTransaction(err)
		}
		stage = ""
	}
	if curator != nil {
		curator.mu.Lock()
		previous, hadPrevious := curator.state.SkillStates[manifest.Skill]
		if manifest.CuratorSet {
			restored := manifest.CuratorMeta
			if manifest.Placement == "archived" {
				restored.State = SkillStateArchived
			} else {
				restored.State = SkillStateActive
			}
			if hadPrevious {
				restored.CreatedAt = previous.CreatedAt
				restored.LastUsedAt = previous.LastUsedAt
				restored.Pinned = previous.Pinned
			}
			curator.state.SkillStates[manifest.Skill] = restored
		} else if manifest.Placement == "active" {
			curator.state.SkillStates[manifest.Skill] = SkillMeta{
				State:      SkillStateActive,
				CreatedAt:  time.Now(),
				LastUsedAt: time.Now(),
			}
		} else {
			delete(curator.state.SkillStates, manifest.Skill)
		}
		err := curator.saveStateLocked()
		if err != nil {
			if hadPrevious {
				curator.state.SkillStates[manifest.Skill] = previous
			} else {
				delete(curator.state.SkillStates, manifest.Skill)
			}
		}
		curator.mu.Unlock()
		if err != nil {
			return failTransaction(err)
		}
	}
	return nil
}

// validateRestoreBlobs validates every referenced blob before apply creates a
// staging directory or writes package data. Stat checks reject obviously large
// blobs before ReadFile, while the read verifies the actual bytes and digest.
func (h *historyStore) validateRestoreBlobs(manifest *revisionManifest) error {
	var packageBytes int64
	for _, file := range manifest.Files {
		path := filepath.Join(h.root, "blobs", file.Blob)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("autogenskills: corrupt blob %s is not a regular file", file.Blob)
		}
		if info.Size() != file.Size || info.Size() > maxHistoryFileSize {
			return fmt.Errorf("autogenskills: corrupt blob %s has invalid size %d for %s", file.Blob, info.Size(), file.Path)
		}
		data, err := readRegularFileBounded(path, maxHistoryFileSize, "blob "+file.Blob)
		if err != nil {
			return err
		}
		if int64(len(data)) != file.Size || len(data) > maxHistoryFileSize {
			return fmt.Errorf("autogenskills: corrupt blob %s has invalid actual size %d for %s", file.Blob, len(data), file.Path)
		}
		if packageBytes > maxHistoryPackageSize-int64(len(data)) {
			return fmt.Errorf("autogenskills: restore exceeds %d-byte package limit", maxHistoryPackageSize)
		}
		packageBytes += int64(len(data))
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != file.Blob {
			return fmt.Errorf("autogenskills: corrupt blob %s for %s", file.Blob, file.Path)
		}
	}
	return nil
}

func (s *Service) historyStore() (*historyStore, error) {
	if !s.cfg.IsEnabled() {
		return nil, ErrDisabled
	}
	if s.cfg.AutogenDir == "" {
		return nil, errors.New("autogenskills: history requires AutogenDir")
	}
	return newHistoryStore(s.cfg.AutogenDir), nil
}

func (s *Service) refreshSkill(name string) error {
	if s.factory == nil || s.factory.registry == nil {
		return nil
	}
	placement, root, err := inspectPlacement(s.cfg.AutogenDir, name, placementOperationRead)
	if err != nil {
		return err
	}
	if placement != "active" {
		s.factory.registry.Unload(name)
		return nil
	}
	skill, err := skills.LoadSkill(root)
	if err != nil {
		return err
	}
	if skill.Metadata.Name != name {
		return fmt.Errorf("autogenskills: SKILL.md name %q does not match package name %q", skill.Metadata.Name, name)
	}
	skill.Source = "autogen"
	skill.LoadedFrom = "autogen"
	s.factory.registry.Unload(name)
	s.factory.registry.RegisterSkill(skill, true)
	return nil
}

func revisionConflict(name, expected, current string) error {
	if expected == "" {
		return fmt.Errorf("autogenskills: expected_revision is required for existing skill %q; view it and retry with revision %s", name, current)
	}
	return fmt.Errorf("autogenskills: revision conflict for skill %q: expected %s, current %s; view the skill and retry", name, expected, current)
}

func sameRevisionState(left, right *revisionManifest) bool {
	if left.Placement != right.Placement || left.CuratorSet != right.CuratorSet ||
		!sameRevisionMeta(left.CuratorMeta, right.CuratorMeta) || len(left.Files) != len(right.Files) {
		return false
	}
	for index := range left.Files {
		if left.Files[index] != right.Files[index] {
			return false
		}
	}
	return true
}

func sameRevisionMeta(left, right SkillMeta) bool {
	if left.State != right.State || left.Pinned != right.Pinned || left.Version != right.Version ||
		left.AbsorbedInto != right.AbsorbedInto || left.ArchiveReason != right.ArchiveReason ||
		(left.ArchivedAt == nil) != (right.ArchivedAt == nil) {
		return false
	}
	return left.ArchivedAt == nil || left.ArchivedAt.Equal(*right.ArchivedAt)
}

func driftConflict(name, head string) error {
	return fmt.Errorf("autogenskills: live package drift conflict for skill %q: filesystem state does not match HEAD %s; re-view and reconcile the out-of-band change before retrying", name, head)
}

func normalizeLiveRevision(manifest *revisionManifest, parent, action string) (string, error) {
	manifest.Parent = parent
	manifest.Action = action
	manifest.RevertOf = ""
	manifest.CreatedAt = time.Time{}
	return canonicalRevisionID(manifest)
}

func (s *Service) currentRevisionSnapshot(store *historyStore, name string) (string, error) {
	head, err := store.readHead(name)
	if err != nil {
		return "", err
	}
	live, err := store.capture(name, head, "current", "", s.curator, false)
	if err != nil {
		return "", err
	}
	if head == "" {
		return normalizeLiveRevision(live, "", "baseline")
	}
	recorded, err := store.loadRevision(name, head)
	if err != nil {
		return "", err
	}
	if sameRevisionState(live, recorded) {
		return head, nil
	}
	return normalizeLiveRevision(live, head, "external")
}

// prepareMutationBase validates expected against the serialized live state. An
// out-of-band state is adopted only when the caller explicitly presents the
// synthetic revision returned by a subsequent view.
func (s *Service) prepareMutationBase(store *historyStore, name, expected string, requireExpected bool) (string, *revisionManifest, error) {
	head, err := store.readHead(name)
	if err != nil {
		return "", nil, err
	}
	live, err := store.capture(name, head, "transaction-baseline", "", s.curator, false)
	if err != nil {
		return "", nil, err
	}
	if head == "" {
		synthetic, idErr := normalizeLiveRevision(live, "", "baseline")
		if idErr != nil {
			return "", nil, idErr
		}
		if requireExpected && expected != synthetic {
			return "", nil, revisionConflict(name, expected, synthetic)
		}
		if !requireExpected && live.Placement != "absent" {
			return "", nil, revisionConflict(name, expected, synthetic)
		}
		persisted, captureErr := store.capture(name, "", "baseline", "", s.curator, true)
		if captureErr != nil {
			return "", nil, captureErr
		}
		persisted.CreatedAt = time.Time{}
		id, writeErr := store.writeRevision(persisted)
		if writeErr != nil {
			return "", nil, writeErr
		}
		if id != synthetic {
			return "", nil, fmt.Errorf("autogenskills: live package changed while initializing history for %q", name)
		}
		if requireExpected {
			if publishErr := store.publishHead(name, id); publishErr != nil {
				return "", nil, publishErr
			}
		}
		return id, persisted, nil
	}

	recorded, err := store.loadRevision(name, head)
	if err != nil {
		return "", nil, err
	}
	if sameRevisionState(live, recorded) {
		if requireExpected && expected != head {
			return "", nil, revisionConflict(name, expected, head)
		}
		persisted, captureErr := store.capture(name, head, "transaction-baseline", "", s.curator, true)
		return head, persisted, captureErr
	}

	synthetic, err := normalizeLiveRevision(live, head, "external")
	if err != nil {
		return "", nil, err
	}
	if !requireExpected || expected != synthetic {
		return "", nil, revisionConflict(name, expected, synthetic)
	}
	external, err := store.capture(name, head, "external", "", s.curator, true)
	if err != nil {
		return "", nil, err
	}
	external.CreatedAt = time.Time{}
	externalID, err := store.writeRevision(external)
	if err != nil {
		return "", nil, err
	}
	if externalID != synthetic {
		return "", nil, fmt.Errorf("autogenskills: live package changed while reconciling %q", name)
	}
	if err := store.publishHead(name, externalID); err != nil {
		return "", nil, err
	}
	return externalID, external, nil
}

func (s *Service) runRevisionMutation(ctx context.Context, name, expected, action string, requireExpected bool, mutate func() error) (string, error) {
	store, err := s.historyStore()
	if err != nil {
		return "", err
	}
	var newID string
	err = store.withLock(ctx, name, func() error {
		current, _, err := s.prepareMutationBase(store, name, expected, requireExpected)
		if err != nil {
			return err
		}
		baseline, err := store.capture(name, current, "transaction-baseline", "", s.curator, true)
		if err != nil {
			return err
		}
		if err := mutate(); err != nil {
			return errors.Join(err, store.apply(baseline, s.curator), s.refreshSkill(name))
		}
		next, err := store.capture(name, current, action, "", s.curator, true)
		if err != nil {
			return errors.Join(err, store.apply(baseline, s.curator), s.refreshSkill(name))
		}
		newID, err = store.writeRevision(next)
		if err == nil {
			err = s.refreshSkill(name)
		}
		if err == nil {
			err = store.publishHead(name, newID)
		}
		if err != nil {
			return errors.Join(err, store.apply(baseline, s.curator), s.refreshSkill(name))
		}
		return nil
	})
	return newID, err
}

func canonicalRevisionID(manifest *revisionManifest) (string, error) {
	copy := *manifest
	copy.Format = historyFormatVersion
	copy.ID = ""
	if err := validateManifest(&copy); err != nil {
		return "", err
	}
	data, err := json.Marshal(&copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// CurrentRevision returns (and, for a legacy package, initializes) its immutable revision ID.
func (s *Service) CurrentRevision(ctx context.Context, name string) (string, error) {
	store, err := s.historyStore()
	if err != nil {
		return "", err
	}
	var id string
	err = store.withLock(ctx, name, func() error {
		if _, inner := store.ensureBaseline(name, s.curator); inner != nil {
			return inner
		}
		var inner error
		id, inner = s.currentRevisionSnapshot(store, name)
		return inner
	})
	return id, err
}

// CurrentRevisionReadOnly computes an untracked legacy revision without creating history files.
func (s *Service) CurrentRevisionReadOnly(name string) (string, error) {
	if !isSafeSkillDirName(name) {
		return "", fmt.Errorf("autogenskills: invalid skill name %q", name)
	}
	store, err := s.historyStore()
	if err != nil {
		return "", err
	}
	return s.currentRevisionSnapshot(store, name)
}

// ViewSkillRevision returns content and revision validated from one serialized disk snapshot.
func (s *Service) ViewSkillRevision(ctx context.Context, name string, readOnly bool) (*skills.Skill, string, error) {
	if !isSafeSkillDirName(name) {
		return nil, "", fmt.Errorf("autogenskills: invalid skill name %q", name)
	}
	skill, revision, _, _, err := s.viewSkillRevisionSnapshot(ctx, name, readOnly)
	return skill, revision, err
}

// ViewSkillRevisionSnapshot also returns support paths captured with the same revision.
func (s *Service) ViewSkillRevisionSnapshot(ctx context.Context, name string, readOnly bool) (*skills.Skill, string, []string, error) {
	if !isSafeSkillDirName(name) {
		return nil, "", nil, fmt.Errorf("autogenskills: invalid skill name %q", name)
	}
	skill, revision, supportFiles, _, err := s.viewSkillRevisionSnapshot(ctx, name, readOnly)
	return skill, revision, supportFiles, err
}

func (s *Service) viewSkillRevisionSnapshot(ctx context.Context, name string, readOnly bool) (*skills.Skill, string, []string, bool, error) {
	if !isSafeSkillDirName(name) {
		return nil, "", nil, false, fmt.Errorf("autogenskills: invalid skill name %q", name)
	}
	store, err := s.historyStore()
	if err != nil {
		return nil, "", nil, false, err
	}
	var result *skills.Skill
	var revision string
	var supportFiles []string
	var external bool
	read := func() error {
		head, headErr := store.readHead(name)
		err = headErr
		if err != nil {
			return err
		}
		before, captureErr := store.captureRead(name, head, "view", "", s.curator)
		if captureErr != nil {
			return captureErr
		}
		if head != "" {
			recorded, loadErr := store.loadRevision(name, head)
			if loadErr != nil {
				return loadErr
			}
			if sameRevisionState(before, recorded) {
				revision = head
			} else {
				external = true
				revision, err = normalizeLiveRevision(before, head, "external")
				if err != nil {
					return err
				}
			}
		} else {
			external = true
			revision, err = normalizeLiveRevision(before, "", "baseline")
			if err != nil {
				return err
			}
		}
		if before.Placement == "absent" {
			return fmt.Errorf("autogenskills: skill %q not found in active or archived packages", name)
		}
		for _, file := range before.Files {
			first, _, _ := strings.Cut(file.Path, "/")
			if first == "references" || first == "templates" || first == "scripts" || first == "assets" {
				supportFiles = append(supportFiles, file.Path)
			}
		}
		result, err = skills.LoadSkill(store.placementPath(name, before.Placement))
		if err != nil {
			return err
		}
		if result.Metadata.Name != name {
			return fmt.Errorf("autogenskills: SKILL.md name %q does not match package name %q", result.Metadata.Name, name)
		}
		after, captureErr := store.captureRead(name, head, "view", "", s.curator)
		if captureErr != nil {
			return captureErr
		}
		if !sameRevisionState(before, after) {
			return fmt.Errorf("autogenskills: skill %q changed while being viewed; re-view and retry", name)
		}
		result.Source = "autogen"
		result.LoadedFrom = "autogen"
		return nil
	}
	if readOnly {
		err = read()
	} else {
		err = store.withLock(ctx, name, read)
	}
	return result, revision, supportFiles, external, err
}

// readSupportFileRevisionSnapshot reads bytes verified against a single package
// capture. Preview mode performs the same checks without creating lock/history
// files.
func (s *Service) readSupportFileRevisionSnapshot(ctx context.Context, name, relativePath string, readOnly bool) ([]byte, string, bool, error) {
	if !isSafeSkillDirName(name) {
		return nil, "", false, fmt.Errorf("autogenskills: invalid skill name %q", name)
	}
	target, err := resolveSupportPath(s.cfg.AutogenDir, name, relativePath)
	if err != nil {
		return nil, "", false, err
	}
	store, err := s.historyStore()
	if err != nil {
		return nil, "", false, err
	}
	cleanPath := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relativePath)))
	var data []byte
	var revision string
	var external bool
	read := func() error {
		head, readErr := store.readHead(name)
		if readErr != nil {
			return readErr
		}
		before, captureErr := store.captureRead(name, head, "read_file", "", s.curator)
		if captureErr != nil {
			return captureErr
		}
		if before.Placement != "active" {
			return fmt.Errorf("autogenskills: skill %q not found in active packages", name)
		}
		var captured *revisionFile
		for index := range before.Files {
			if before.Files[index].Path == cleanPath {
				captured = &before.Files[index]
				break
			}
		}
		if captured == nil {
			return fmt.Errorf("autogenskills: support file %q not found in captured package", cleanPath)
		}
		info, statErr := os.Lstat(target)
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("autogenskills: support file %q is not a regular file", cleanPath)
		}
		if info.Size() > 1<<20 {
			return fmt.Errorf("autogenskills: support file exceeds 1 MiB")
		}
		data, readErr = readRegularFileBounded(target, 1<<20, "support file "+cleanPath)
		if readErr != nil {
			return readErr
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != captured.Blob {
			return fmt.Errorf("autogenskills: skill %q changed while support file was read; retry", name)
		}
		after, captureErr := store.captureRead(name, head, "read_file", "", s.curator)
		if captureErr != nil {
			return captureErr
		}
		if !sameRevisionState(before, after) {
			return fmt.Errorf("autogenskills: skill %q changed while support file was read; retry", name)
		}
		if head == "" {
			external = true
			revision, readErr = normalizeLiveRevision(before, "", "baseline")
			return readErr
		}
		recorded, loadErr := store.loadRevision(name, head)
		if loadErr != nil {
			return loadErr
		}
		if sameRevisionState(before, recorded) {
			revision = head
			return nil
		}
		external = true
		revision, readErr = normalizeLiveRevision(before, head, "external")
		return readErr
	}
	if readOnly {
		err = read()
	} else {
		err = store.withLock(ctx, name, read)
	}
	return data, revision, external, err
}

// historySnapshot returns the persisted chain plus a deterministic synthetic
// first entry when the live package differs from HEAD. It never writes history.
func (s *Service) historySnapshot(store *historyStore, name string) ([]SkillRevision, error) {
	head, err := store.readHead(name)
	if err != nil {
		return nil, err
	}
	live, err := store.captureRead(name, head, "history", "", s.curator)
	if err != nil {
		return nil, err
	}
	var result []SkillRevision
	if head == "" {
		id, idErr := normalizeLiveRevision(live, "", "untracked")
		if idErr != nil {
			return nil, idErr
		}
		result = append(result, SkillRevision{ID: id, Action: "untracked", Placement: live.Placement})
	} else {
		recorded, loadErr := store.loadRevision(name, head)
		if loadErr != nil {
			return nil, loadErr
		}
		if !sameRevisionState(live, recorded) {
			id, idErr := normalizeLiveRevision(live, head, "external")
			if idErr != nil {
				return nil, idErr
			}
			result = append(result, SkillRevision{
				ID: id, Parent: head, Action: "external", Placement: live.Placement,
			})
		}
	}
	after, err := store.captureRead(name, head, "history", "", s.curator)
	if err != nil {
		return nil, err
	}
	if !sameRevisionState(live, after) {
		return nil, fmt.Errorf("autogenskills: skill %q changed while history was read; retry", name)
	}
	seen := make(map[string]struct{})
	for id := head; id != ""; {
		if _, ok := seen[id]; ok {
			return nil, fmt.Errorf("autogenskills: corrupt history cycle at revision %s", id)
		}
		seen[id] = struct{}{}
		manifest, loadErr := store.loadRevision(name, id)
		if loadErr != nil {
			return nil, loadErr
		}
		result = append(result, SkillRevision{
			ID: manifest.ID, Parent: manifest.Parent, Action: manifest.Action,
			RevertOf: manifest.RevertOf, CreatedAt: manifest.CreatedAt, Placement: manifest.Placement,
		})
		id = manifest.Parent
	}
	return result, nil
}

// SkillHistory returns newest-first immutable revision summaries. Live drift is
// represented by a synthetic external revision and is not persisted.
func (s *Service) SkillHistory(ctx context.Context, name string) ([]SkillRevision, error) {
	if !isSafeSkillDirName(name) {
		return nil, fmt.Errorf("autogenskills: invalid skill name %q", name)
	}
	store, err := s.historyStore()
	if err != nil {
		return nil, err
	}
	var result []SkillRevision
	err = store.withLock(ctx, name, func() error {
		var inner error
		result, inner = s.historySnapshot(store, name)
		return inner
	})
	return result, err
}

// SkillHistoryReadOnly reports live drift without locks or filesystem writes.
func (s *Service) SkillHistoryReadOnly(name string) ([]SkillRevision, error) {
	if !isSafeSkillDirName(name) {
		return nil, fmt.Errorf("autogenskills: invalid skill name %q", name)
	}
	store, err := s.historyStore()
	if err != nil {
		return nil, err
	}
	return s.historySnapshot(store, name)
}

// UndoSkill appends a revert revision restoring the selected revision (the parent by default).
func (s *Service) UndoSkill(ctx context.Context, name, expected, target string) (string, error) {
	store, err := s.historyStore()
	if err != nil {
		return "", err
	}
	var newID string
	err = store.withLock(ctx, name, func() error {
		current, _, err := s.prepareMutationBase(store, name, expected, true)
		if err != nil {
			return err
		}
		currentManifest, err := store.loadRevision(name, current)
		if err != nil {
			return err
		}
		if target == "" {
			target = currentManifest.Parent
		}
		if target == "" {
			return fmt.Errorf("autogenskills: revision %s has no prior state to undo", current)
		}
		targetManifest, err := store.loadRevision(name, target)
		if err != nil {
			return fmt.Errorf("autogenskills: undo target %q is not a revision of skill %q: %w", target, name, err)
		}
		ancestor := currentManifest.Parent
		for ancestor != "" && ancestor != target {
			manifest, loadErr := store.loadRevision(name, ancestor)
			if loadErr != nil {
				return loadErr
			}
			ancestor = manifest.Parent
		}
		if ancestor != target {
			return fmt.Errorf("autogenskills: undo target %s is not an ancestor of current HEAD %s", target, current)
		}
		baseline, err := store.capture(name, current, "rollback-baseline", "", s.curator, true)
		if err != nil {
			return err
		}
		if err := store.apply(targetManifest, s.curator); err != nil {
			return errors.Join(err, store.apply(baseline, s.curator))
		}
		revert, err := store.capture(name, current, "undo", target, s.curator, true)
		if err == nil {
			newID, err = store.writeRevision(revert)
		}
		if err == nil {
			err = s.refreshSkill(name)
		}
		if err == nil {
			err = store.publishHead(name, newID)
		}
		if err != nil {
			return errors.Join(err, store.apply(baseline, s.curator), s.refreshSkill(name))
		}
		return nil
	})
	return newID, err
}

// CreateSkillRevisioned creates a package and records it as an immutable revision.
func (s *Service) CreateSkillRevisioned(ctx context.Context, opts CreateOptions) (CreationResult, string) {
	var result CreationResult
	id, err := s.runRevisionMutation(ctx, opts.Name, "", "create", false, func() error {
		result = s.factory.Create(opts)
		return result.Error
	})
	if err != nil {
		result = CreationResult{Error: err}
	}
	return result, id
}

// PatchSkillRevisioned patches only if expected still names the package HEAD.
func (s *Service) PatchSkillRevisioned(ctx context.Context, opts PatchOptions, expected string) (PatchResult, string) {
	var result PatchResult
	id, err := s.runRevisionMutation(ctx, opts.Name, expected, "patch", true, func() error {
		result = s.factory.Patch(opts)
		return result.Error
	})
	if err != nil {
		result = PatchResult{Error: err}
	}
	return result, id
}

// WriteSupportFileRevisioned atomically writes a support file under revision control.
func (s *Service) WriteSupportFileRevisioned(ctx context.Context, name, relativePath string, content []byte, expected string) (string, string, error) {
	target, err := resolveSupportPath(s.cfg.AutogenDir, name, relativePath)
	if err != nil {
		return "", "", err
	}
	id, err := s.runRevisionMutation(ctx, name, expected, "write_file", true, func() error {
		return stagedSupportWrite(s.cfg.AutogenDir, name, relativePath, content)
	})
	return target, id, err
}

func stagedSupportWrite(autogenDir, name, relativePath string, content []byte) error {
	if int64(len(content)) > maxHistoryFileSize {
		return fmt.Errorf("autogenskills: support file size %d exceeds %d-byte history limit", len(content), maxHistoryFileSize)
	}
	source := filepath.Join(autogenDir, name)
	clean := filepath.Clean(filepath.FromSlash(relativePath))
	stage, err := os.MkdirTemp(autogenDir, ".skill-write-"+name+"-*")
	if err != nil {
		return err
	}
	retainStage := false
	defer func() {
		if !retainStage {
			_ = historyRemoveAll(stage)
		}
	}()
	var fileCount int
	var packageBytes int64
	targetFound := false
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, relErr := filepath.Rel(source, path)
		if relErr != nil {
			return relErr
		}
		if relative == "." {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("autogenskills: symlink rejected in skill package: %s", filepath.ToSlash(relative))
		}
		destination := filepath.Join(stage, relative)
		if info.IsDir() {
			return os.Mkdir(destination, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("autogenskills: unsupported filesystem entry: %s", filepath.ToSlash(relative))
		}
		fileCount++
		if fileCount > maxHistoryPackageFiles {
			return fmt.Errorf("autogenskills: staged package exceeds %d-file limit", maxHistoryPackageFiles)
		}
		if relative == clean {
			targetFound = true
			if packageBytes > maxHistoryPackageSize-int64(len(content)) {
				return fmt.Errorf("autogenskills: staged package exceeds %d-byte limit", maxHistoryPackageSize)
			}
			packageBytes += int64(len(content))
			return nil
		}
		if info.Size() > maxHistoryFileSize {
			return fmt.Errorf("autogenskills: staged package file %s exceeds %d-byte limit", filepath.ToSlash(relative), maxHistoryFileSize)
		}
		if packageBytes > maxHistoryPackageSize-info.Size() {
			return fmt.Errorf("autogenskills: staged package exceeds %d-byte limit", maxHistoryPackageSize)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if len(data) > maxHistoryFileSize {
			return fmt.Errorf("autogenskills: staged package file %s grew beyond %d-byte limit while being read", filepath.ToSlash(relative), maxHistoryFileSize)
		}
		actualSize := int64(len(data))
		if packageBytes > maxHistoryPackageSize-actualSize {
			return fmt.Errorf("autogenskills: staged package exceeds %d-byte limit", maxHistoryPackageSize)
		}
		packageBytes += actualSize
		return os.WriteFile(destination, data, info.Mode().Perm())
	})
	if err != nil {
		return err
	}
	if !targetFound {
		fileCount++
		if fileCount > maxHistoryPackageFiles {
			return fmt.Errorf("autogenskills: staged package exceeds %d-file limit", maxHistoryPackageFiles)
		}
		if packageBytes > maxHistoryPackageSize-int64(len(content)) {
			return fmt.Errorf("autogenskills: staged package exceeds %d-byte limit", maxHistoryPackageSize)
		}
	}
	destination := filepath.Join(stage, clean)
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if err := atomicWrite(destination, content, 0o644); err != nil {
		return err
	}
	loaded, err := skills.LoadSkill(stage)
	if err != nil {
		return err
	}
	if loaded.Metadata.Name != name {
		return fmt.Errorf("autogenskills: SKILL.md name %q does not match package name %q", loaded.Metadata.Name, name)
	}
	backupRoot, err := os.MkdirTemp(autogenDir, ".skill-write-backup-"+name+"-*")
	if err != nil {
		return err
	}
	retainBackup := false
	defer func() {
		if !retainBackup {
			_ = historyRemoveAll(backupRoot)
		}
	}()
	backup := filepath.Join(backupRoot, "original")
	if err := historyRename(source, backup); err != nil {
		return err
	}
	if err := historyRename(stage, source); err != nil {
		rollbackErr := historyRename(backup, source)
		if rollbackErr != nil {
			retainBackup = true
			retainStage = true
			return errors.Join(err, fmt.Errorf("autogenskills: rollback incomplete; recovery data retained at %s: %w", backupRoot, rollbackErr))
		}
		return err
	}
	stage = ""
	return nil
}

// ArchiveSkillRevisioned archives a package and curator disposition under revision control.
func (s *Service) ArchiveSkillRevisioned(ctx context.Context, name, absorbedInto, reason, expected string) (string, string, error) {
	if s.curator == nil {
		return "", "", errors.New("archiving not available: no curator configured")
	}
	var path string
	id, err := s.runRevisionMutation(ctx, name, expected, "archive", true, func() error {
		var archiveErr error
		path, archiveErr = s.curator.ArchiveWithReason(s.cfg.AutogenDir, name, absorbedInto, reason)
		return archiveErr
	})
	return path, id, err
}
