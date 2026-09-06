package autogenskills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// supportDirs are the four package subdirectories a skill may carry alongside
// SKILL.md. resolveSupportPath enforces the same set.
var supportDirs = []string{"references", "templates", "scripts", "assets"}

// supportFile is one carried artifact. Digest is the SHA-256 of the bytes on
// disk; it is what proves a transfer was exact.
type supportFile struct {
	RelPath string
	Size    int64
	Digest  string
}

// listSupportFiles enumerates every regular file under the skill's support
// directories, in stable order.
//
// Symlinks are reported as an error rather than skipped. A skipped symlink
// would silently drop content that the archive precondition is supposed to
// account for, which is the exact class of quiet loss this package is fixing.
func listSupportFiles(autogenDir, name string) ([]supportFile, error) {
	if !isSafeSkillDirName(name) {
		return nil, fmt.Errorf("unsafe skill name %q", name)
	}
	root := filepath.Join(autogenDir, name)
	var out []supportFile
	for _, dir := range supportDirs {
		base := filepath.Join(root, dir)
		if info, err := os.Lstat(base); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		} else if !info.IsDir() {
			continue
		}
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			slashRel := filepath.ToSlash(rel)
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink rejected in skill package: %s", slashRel)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("unsupported filesystem entry: %s", slashRel)
			}
			digest, err := digestFile(path)
			if err != nil {
				return err
			}
			out = append(out, supportFile{RelPath: slashRel, Size: info.Size(), Digest: digest})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out, nil
}

// digestFile hashes a file by streaming it, so a large asset never has to be
// held in memory in full.
func digestFile(path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 -- path is produced by resolveSupportPath or a bounded walk under it.
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// absorbOutcome records one attempted transfer for the tool's report.
type absorbOutcome struct {
	RelPath  string
	DestPath string
	Size     int64
	Digest   string
	Skipped  string // non-empty when the destination already held identical bytes
}

// absorbSupportFiles copies support files from one skill package into another
// WITHOUT the bytes passing through the model.
//
// This is the whole point of the primitive. The previous path required the
// agent to read a file into its context and re-emit it through write_file, so
// every carried artifact was retyped by a language model: text drifted and
// binary content (images, compiled artifacts, archives) could not survive a
// JSON string round-trip at all. Here the model chooses WHICH files move; the
// filesystem moves them.
//
// Each write is verified by re-hashing the destination and comparing against
// the source digest, so a partial or mangled copy is reported as an error
// rather than being recorded as a successful merge.
func (s *Service) absorbSupportFiles(ctx context.Context, fromSkill, toSkill string, relPaths []string, expected string) ([]absorbOutcome, string, error) {
	autogenDir := s.cfg.AutogenDir
	if autogenDir == "" {
		return nil, "", fmt.Errorf("no autogen directory configured")
	}
	if fromSkill == toSkill {
		return nil, "", fmt.Errorf("source and destination must differ")
	}
	if !isSafeSkillDirName(fromSkill) || !isSafeSkillDirName(toSkill) {
		return nil, "", fmt.Errorf("unsafe skill name")
	}

	available, err := listSupportFiles(autogenDir, fromSkill)
	if err != nil {
		return nil, "", fmt.Errorf("read %s support files: %w", fromSkill, err)
	}
	if len(available) == 0 {
		return nil, "", fmt.Errorf("skill %q has no support files to absorb", fromSkill)
	}

	selected, err := selectSupportFiles(available, relPaths)
	if err != nil {
		return nil, "", err
	}

	var outcomes []absorbOutcome
	revision := ""
	for _, file := range selected {
		sourcePath, err := resolveSupportPath(autogenDir, fromSkill, file.RelPath)
		if err != nil {
			return nil, "", fmt.Errorf("source %s: %w", file.RelPath, err)
		}
		data, err := readRegularFileBounded(sourcePath, maxHistoryFileSize, "support file "+file.RelPath)
		if err != nil {
			return nil, "", err
		}
		// Re-hash what we actually read. If the file changed between the
		// enumeration and this read, we must not record the stale digest.
		actual := sha256.Sum256(data)
		if hex.EncodeToString(actual[:]) != file.Digest {
			return nil, "", fmt.Errorf("source %s changed while being absorbed", file.RelPath)
		}

		if destDigest, err := digestSupportFile(autogenDir, toSkill, file.RelPath); err == nil && destDigest == file.Digest {
			outcomes = append(outcomes, absorbOutcome{
				RelPath: file.RelPath, Size: file.Size, Digest: file.Digest,
				DestPath: filepath.Join(autogenDir, toSkill, filepath.FromSlash(file.RelPath)),
				Skipped:  "already identical",
			})
			continue
		}

		target, id, err := s.WriteSupportFileRevisioned(ctx, toSkill, file.RelPath, data, expected)
		if err != nil {
			return nil, "", fmt.Errorf("write %s into %s: %w", file.RelPath, toSkill, err)
		}
		// Each successful write advances the revision, so subsequent writes in
		// this batch must chain from the new one rather than the caller's.
		expected = id
		revision = id

		written, err := digestSupportFile(autogenDir, toSkill, file.RelPath)
		if err != nil {
			return nil, "", fmt.Errorf("verify %s in %s: %w", file.RelPath, toSkill, err)
		}
		if written != file.Digest {
			return nil, "", fmt.Errorf("verification failed for %s: destination digest %s does not match source %s", file.RelPath, short(written), short(file.Digest))
		}
		outcomes = append(outcomes, absorbOutcome{RelPath: file.RelPath, DestPath: target, Size: file.Size, Digest: file.Digest})
	}
	return outcomes, revision, nil
}

// selectSupportFiles resolves the caller's requested paths against what the
// source actually has. An empty request means "carry everything", which is the
// safe default for a merge.
func selectSupportFiles(available []supportFile, relPaths []string) ([]supportFile, error) {
	if len(relPaths) == 0 {
		return available, nil
	}
	index := make(map[string]supportFile, len(available))
	for _, file := range available {
		index[file.RelPath] = file
	}
	var selected []supportFile
	var missing []string
	for _, raw := range relPaths {
		key := filepath.ToSlash(filepath.Clean(strings.TrimSpace(raw)))
		file, ok := index[key]
		if !ok {
			missing = append(missing, key)
			continue
		}
		selected = append(selected, file)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("source has no such support file(s): %s", strings.Join(missing, ", "))
	}
	return selected, nil
}

// digestSupportFile hashes one support file inside a skill package.
func digestSupportFile(autogenDir, name, relPath string) (string, error) {
	path, err := resolveSupportPath(autogenDir, name, relPath)
	if err != nil {
		return "", err
	}
	return digestFile(path)
}

// missingSupportFiles reports which of the source's support files are absent
// from the destination, or present with different bytes.
//
// This is the archive precondition. Without it, a merge could archive a skill
// whose scripts and assets were never carried across, and the tool would report
// success: exactly the silent loss measured in the curator benchmark.
func missingSupportFiles(autogenDir, fromSkill, toSkill string) ([]string, error) {
	available, err := listSupportFiles(autogenDir, fromSkill)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, file := range available {
		destDigest, err := digestSupportFile(autogenDir, toSkill, file.RelPath)
		if err != nil {
			missing = append(missing, file.RelPath)
			continue
		}
		if destDigest != file.Digest {
			missing = append(missing, file.RelPath+" (differs)")
		}
	}
	return missing, nil
}

func short(digest string) string {
	if len(digest) <= 12 {
		return digest
	}
	return digest[:12]
}

// excludeDropped removes entries the caller explicitly declined to carry.
//
// Matching ignores the " (differs)" suffix that missingSupportFiles appends, so
// naming a path covers both "absent" and "present but modified".
func excludeDropped(missing, dropped []string) []string {
	if len(dropped) == 0 {
		return missing
	}
	skip := make(map[string]bool, len(dropped))
	for _, entry := range dropped {
		skip[filepath.ToSlash(filepath.Clean(strings.TrimSpace(entry)))] = true
	}
	var remaining []string
	for _, entry := range missing {
		path := strings.TrimSuffix(entry, " (differs)")
		if skip[path] {
			continue
		}
		remaining = append(remaining, entry)
	}
	return remaining
}
