package deepwiki

// page_cache.go — incremental per-page cache for V3+.
//
// Each wiki page is cached individually, keyed by a hash of its generation
// inputs (page ID, required sections, focus-file stat data). On a re-run
// where only a subset of files changed, only pages whose focus files were
// modified pay the LLM cost; the rest are served instantly from disk.
//
// Cache layout:
//
//	{repoPath}/.swarm/deepwiki/pages/{safe-pageID}-{inputHash}.json
//
// The hash is a 16-char prefix of SHA-256 over:
//
//	page.ID + sorted(RequiredSections) + for each sorted FocusFile: path:size:mtime
//
// This is a pure stat-walk — no file contents are read — so the check adds
// only a few milliseconds even for large repos.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PageCache stores and retrieves individual wiki pages by their input hash.
type PageCache struct {
	dir string // absolute path to the pages cache directory
}

// NewPageCache creates a PageCache for a repository.
// The cache lives at {repoPath}/.swarm/deepwiki/pages/.
func NewPageCache(repoPath string) *PageCache {
	return &PageCache{
		dir: filepath.Join(repoPath, ".swarm", "deepwiki", "pages"),
	}
}

// pageCacheEntry is the on-disk JSON envelope around a cached page.
type pageCacheEntry struct {
	PageID    string   `json:"page_id"`
	InputHash string   `json:"input_hash"`
	Page      WikiPage `json:"page"`
}

// HashPageInputs computes a stable, short hash for a page's generation inputs.
//
// Inputs hashed:
//   - page.ID
//   - page.RequiredSections (sorted)
//   - page.FocusFiles (sorted): path + os.Stat size + mtime nanoseconds
//
// Returns a 16-character hex string. Missing files are recorded as "missing"
// so a file being deleted also invalidates the cache entry.
func (pc *PageCache) HashPageInputs(repoPath string, page *FinalPage) string {
	h := sha256.New()

	fmt.Fprintf(h, "id:%s\n", page.ID)

	sections := make([]string, len(page.RequiredSections))
	copy(sections, page.RequiredSections)
	sort.Strings(sections)
	for _, s := range sections {
		fmt.Fprintf(h, "sec:%s\n", s)
	}

	files := make([]string, len(page.FocusFiles))
	copy(files, page.FocusFiles)
	sort.Strings(files)
	for _, fp := range files {
		abs := fp
		if !filepath.IsAbs(fp) {
			abs = filepath.Join(repoPath, fp)
		}
		info, err := os.Stat(abs)
		if err == nil {
			fmt.Fprintf(h, "file:%s:size:%d:mtime:%d\n", fp, info.Size(), info.ModTime().UnixNano())
		} else {
			fmt.Fprintf(h, "file:%s:missing\n", fp)
		}
	}

	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Load returns a cached WikiPage if a valid entry exists for (pageID, inputHash).
// Returns (nil, nil) on a cache miss. Corrupt entries are treated as misses.
func (pc *PageCache) Load(pageID, inputHash string) (*WikiPage, error) {
	path := pc.entryPath(pageID, inputHash)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // clean miss
		}
		return nil, fmt.Errorf("read page cache: %w", err)
	}
	var entry pageCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		// Corrupt — treat as miss; the next save will overwrite.
		return nil, nil
	}
	if entry.InputHash != inputHash {
		// Hash collision or stale file — evict silently.
		return nil, nil
	}
	return &entry.Page, nil
}

// Save writes a page to the cache. Errors are non-fatal — the caller should
// log and continue rather than aborting generation.
func (pc *PageCache) Save(pageID, inputHash string, page WikiPage) error {
	if err := os.MkdirAll(pc.dir, 0755); err != nil {
		return fmt.Errorf("create page cache dir: %w", err)
	}
	entry := pageCacheEntry{
		PageID:    pageID,
		InputHash: inputHash,
		Page:      page,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal page cache entry: %w", err)
	}
	return os.WriteFile(pc.entryPath(pageID, inputHash), data, 0644)
}

// entryPath returns the file path for a given (pageID, inputHash) pair.
// pageID is sanitised to remove characters that are unsafe in file names.
func (pc *PageCache) entryPath(pageID, inputHash string) string {
	safe := sanitiseFilename(pageID)
	return filepath.Join(pc.dir, fmt.Sprintf("%s-%s.json", safe, inputHash))
}

// sanitiseFilename replaces any character that is not alphanumeric, dash, or
// underscore with an underscore, keeping filenames filesystem-safe.
func sanitiseFilename(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
