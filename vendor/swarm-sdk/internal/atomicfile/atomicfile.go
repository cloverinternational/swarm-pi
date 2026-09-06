// Package atomicfile provides robust, crash-safe file writes for SwarmOS
// config and secret files.
//
// Write performs an atomic replace: it creates a temp file in the same
// directory as the target, writes the payload, fsyncs the file, renames it
// over the target, and finally fsyncs the parent directory so the rename is
// durable. WithLock serializes concurrent writers across processes using an
// advisory flock. Backup keeps a bounded number of timestamped copies and
// prunes stale/corrupt ones.
package atomicfile

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// ErrEmpty is returned by Write when data is empty and AllowEmpty was not set.
var ErrEmpty = errors.New("atomicfile: refusing to write empty data (use AllowEmpty)")

// cfg holds resolved options for a Write call.
type cfg struct {
	perm       fs.FileMode
	allowEmpty bool
}

// Option configures a Write call.
type Option func(*cfg)

// AllowEmpty permits writing empty data. By default Write refuses empty
// payloads to guard against truncating a config file to nothing.
func AllowEmpty() Option {
	return func(c *cfg) { c.allowEmpty = true }
}

// WithPerm overrides the file mode of the written file (default 0600).
func WithPerm(p fs.FileMode) Option {
	return func(c *cfg) { c.perm = p }
}

// Write atomically writes data to path.
//
// Sequence: CreateTemp in the target's directory -> write -> f.Sync -> close
// -> os.Rename -> open parent dir + fsync. Empty data is refused unless
// AllowEmpty() is passed. Default permission is 0600; override with WithPerm.
func Write(path string, data []byte, opts ...Option) error {
	c := cfg{perm: 0o600}
	for _, o := range opts {
		o(&c)
	}
	if len(data) == 0 && !c.allowEmpty {
		return ErrEmpty
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("atomicfile: mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("atomicfile: create temp: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail before a successful rename.
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(c.perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("atomicfile: chmod temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("atomicfile: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("atomicfile: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("atomicfile: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("atomicfile: rename: %w", err)
	}
	// fsync the parent directory so the rename survives a crash.
	if d, derr := os.Open(dir); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// lockPath returns the advisory lock file path for target, derived from a
// stable hash of the absolute target path, under paths.LocksDir().
func lockPath(target string) string {
	abs, err := filepath.Abs(target)
	if err != nil {
		abs = target
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(paths.LocksDir(), hex.EncodeToString(sum[:])+".lock")
}

// WithLock runs fn while holding an exclusive advisory lock associated with
// path. The lock is released when fn returns (or on any error acquiring it).
// On platforms without flock, this degrades to a no-op guard (fn still runs).
func WithLock(path string, fn func() error) error {
	lp := lockPath(path)
	f, err := os.OpenFile(lp, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("atomicfile: open lock %s: %w", lp, err)
	}
	defer f.Close()

	if err := flockExclusive(f); err != nil {
		return fmt.Errorf("atomicfile: flock %s: %w", lp, err)
	}
	defer func() { _ = flockUnlock(f) }()

	return fn()
}

// Backup copies path into paths.BackupsDir() at 0600, named
// <base>.<timestamp>.bak, then keeps the newest `keep` backups for that base
// and deletes older ones plus any 0-byte/corrupt copies. If keep <= 0 a
// default of 5 is used. It never widens permissions.
func Backup(path string, keep int) error {
	if keep <= 0 {
		keep = 5
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("atomicfile: stat source %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("atomicfile: cannot backup directory %s", path)
	}

	base := filepath.Base(path)
	dir := paths.BackupsDir()

	// Timestamp: RFC3339-ish but filesystem-safe (no colons).
	ts := time.Now().UTC().Format("2006-01-02T15-04-05.000000000Z")
	dst := filepath.Join(dir, fmt.Sprintf("%s.%s.bak", base, ts))

	if err := copyFile(path, dst, 0o600); err != nil {
		return err
	}

	return pruneBackups(dir, base, keep)
}

// copyFile copies src to dst with the given perm, fsyncing the result.
func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("atomicfile: open src: %w", err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return fmt.Errorf("atomicfile: create backup: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("atomicfile: copy backup: %w", err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return fmt.Errorf("atomicfile: sync backup: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("atomicfile: close backup: %w", err)
	}
	// Ensure perms were not widened by umask interplay.
	_ = os.Chmod(dst, perm)
	return nil
}

// backupEntry pairs a backup filename with its modtime for sorting.
type backupEntry struct {
	name    string
	path    string
	modTime time.Time
}

// pruneBackups removes 0-byte copies and keeps only the newest `keep` backups
// for the given base prefix.
func pruneBackups(dir, base string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("atomicfile: read backups dir: %w", err)
	}

	prefix := base + "."
	var valid []backupEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".bak") {
			continue
		}
		full := filepath.Join(dir, name)
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		// Prune zero-byte / corrupt copies immediately.
		if info.Size() == 0 {
			_ = os.Remove(full)
			continue
		}
		valid = append(valid, backupEntry{name: name, path: full, modTime: info.ModTime()})
	}

	// Newest first.
	sort.Slice(valid, func(i, j int) bool {
		if valid[i].modTime.Equal(valid[j].modTime) {
			return valid[i].name > valid[j].name
		}
		return valid[i].modTime.After(valid[j].modTime)
	})

	for i, e := range valid {
		if i >= keep {
			_ = os.Remove(e.path)
		}
	}
	return nil
}
