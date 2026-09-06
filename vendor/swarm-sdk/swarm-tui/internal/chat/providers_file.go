package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// providers.json durability helpers.
//
// ~/.swarmos/providers.json has been truncated to 0 bytes more than once in
// this codebase's history (see the providers.json.corrupt_* graveyard in any
// long-lived ~/.swarmos). The mechanism: every writer used plain os.WriteFile
// — truncate-then-write — while BOTH the TUI and the always-on background
// daemon run this same code against the same file. A crash mid-write, or two
// writers interleaving truncate/write, leaves an empty or torn file, and with
// it every configured provider (and the settings / Manage Providers / Auth
// screens) silently vanishes.
//
// Rules enforced here:
//   1. Writes are ATOMIC: temp file in the same directory + rename. Readers
//      see the old content or the new content, never a torn/empty file.
//   2. Writes REFUSE invalid or empty JSON — no code path may persist an
//      empty provider set by accident.
//   3. Every successful write first rolls the current (valid) content to
//      "<path>.bak", so there is always a fresh restore point.
//   4. Reads detect an empty/corrupt file, QUARANTINE it, and auto-restore
//      the newest parseable backup — self-healing instead of "0 providers
//      configured".

// atomicWriteProvidersJSON atomically replaces path with data, keeping a
// rolling "<path>.bak" of the previous valid content. It refuses to write
// empty or syntactically invalid JSON.
func atomicWriteProvidersJSON(path string, data []byte, perm os.FileMode) error {
	if len(bytes.TrimSpace(data)) == 0 || !json.Valid(data) {
		return fmt.Errorf("refusing to write empty/invalid JSON to %s", path)
	}

	// Roll a backup of the current content while it is still known-good.
	if old, err := os.ReadFile(path); err == nil &&
		len(bytes.TrimSpace(old)) > 0 && json.Valid(old) && !bytes.Equal(old, data) {
		_ = os.WriteFile(path+".bak", old, perm) // best-effort
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".providers-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp for %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp for %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("chmod temp for %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename into %s: %w", path, err)
	}
	return nil
}

// readProvidersJSONWithRecovery reads path. If the file exists but is empty
// or not valid JSON, it quarantines the bad file (renaming it to
// "<path>.corrupt_<timestamp>") and restores the newest parseable backup
// ("<path>.bak", "<path>.bak*", "<path>.backup*") in its place, returning the
// restored content. Returns the underlying error (e.g. fs.ErrNotExist) when
// the file does not exist and no backup can stand in.
func readProvidersJSONWithRecovery(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil && len(bytes.TrimSpace(data)) > 0 && json.Valid(data) {
		return data, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// File is missing, empty, or corrupt — look for the newest good backup.
	backup, bdata := newestParseableBackup(path)
	if backup == "" {
		if err != nil {
			return nil, err // did not exist; nothing to restore
		}
		return nil, fmt.Errorf("%s is empty or corrupt and no parseable backup exists", path)
	}

	// Quarantine the bad file (if present) so the evidence is preserved.
	if err == nil {
		quarantine := fmt.Sprintf("%s.corrupt_%s", path, time.Now().UTC().Format("20060102T150405Z"))
		_ = os.Rename(path, quarantine)
		logDebug("[Providers] %s was empty/corrupt — quarantined to %s, restoring %s", path, quarantine, backup)
	} else {
		logDebug("[Providers] %s missing — restoring from %s", path, backup)
	}

	perm := os.FileMode(0600)
	if fi, serr := os.Stat(backup); serr == nil {
		perm = fi.Mode().Perm()
	}
	if werr := atomicWriteProvidersJSON(path, bdata, perm); werr != nil {
		// Restore-in-place failed; still hand the caller the good content.
		logDebug("[Providers] restore write failed: %v", werr)
	}
	return bdata, nil
}

// newestParseableBackup returns the newest-mtime sibling backup of path whose
// content is non-empty valid JSON, along with that content.
func newestParseableBackup(path string) (string, []byte) {
	var candidates []string
	for _, pattern := range []string{path + ".bak", path + ".bak*", path + ".backup*"} {
		if matches, err := filepath.Glob(pattern); err == nil {
			candidates = append(candidates, matches...)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		fi, ierr := os.Stat(candidates[i])
		fj, jerr := os.Stat(candidates[j])
		if ierr != nil || jerr != nil {
			return ierr == nil
		}
		return fi.ModTime().After(fj.ModTime())
	})
	seen := make(map[string]bool)
	for _, c := range candidates {
		if seen[c] {
			continue
		}
		seen[c] = true
		data, err := os.ReadFile(c)
		if err == nil && len(bytes.TrimSpace(data)) > 0 && json.Valid(data) {
			return c, data
		}
	}
	return "", nil
}
