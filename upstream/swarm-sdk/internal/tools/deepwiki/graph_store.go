// Package deepwiki — GraphStore
//
// GraphStore persists the merged CodeGraph (entities + embeddings) to
// ~/.swarm/deepwiki/graphs/{repoID}/ so that subsequent generation runs
// against the same unmodified repository can skip the expensive analysis and
// embedding phases entirely.
//
// Layout:
//
//	~/.swarm/deepwiki/graphs/
//	  {repoID}/           ← first 16 hex chars of SHA256(abs repo path)
//	    meta.json         ← fingerprint, timestamps, stats, backend
//	    graph.json.gz     ← gzip-compressed JSON CodeGraph (entities+chunks+vectors)
//
// Freshness is determined by a "repo fingerprint": a SHA256 over the sorted
// list of {relpath}:{size}:{mtime} tuples for every file in the repo.
// This is a pure stat-walk — no file contents are read.
// If any file is added, removed, or modified the fingerprint changes and the
// cache is considered stale; both agents re-run and overwrite the cache.
package deepwiki

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// ---------------------------------------------------------------------------
// Metadata
// ---------------------------------------------------------------------------

// graphMeta is the small metadata file stored alongside the compressed graph.
// It is checked first on every run — if the fingerprint matches we load the
// full graph; otherwise we skip straight to re-analysis.
type graphMeta struct {
	RepoPath      string    `json:"repo_path"`
	RepoID        string    `json:"repo_id"`
	Fingerprint   string    `json:"fingerprint"` // SHA256 of file-tree stat walk
	AnalyzedAt    time.Time `json:"analyzed_at"`
	TotalFiles    int       `json:"total_files"`
	TotalEntities int       `json:"total_entities"`
	TotalChunks   int       `json:"total_chunks"`
	Backend       string    `json:"backend"` // embedding backend used
}

// ---------------------------------------------------------------------------
// GraphStore
// ---------------------------------------------------------------------------

// GraphStore persists and loads a merged CodeGraph from
// ~/.swarm/deepwiki/graphs/{repoID}/.
type GraphStore struct {
	dir    string // absolute path to the per-repo cache dir
	repoID string // the directory basename (first 16 hex chars of SHA256)
}

// NewGraphStore creates a GraphStore for the given repository path.
// The cache directory is derived from a hash of the canonical absolute
// repo path and always lives under ~/.swarm/deepwiki/graphs/.
func NewGraphStore(repoPath string) (*GraphStore, error) {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("graph store: resolve repo path: %w", err)
	}
	// Use the first 16 hex chars of SHA256(abs path) as the directory name.
	// Matches the pattern the bridge uses for wiki IDs.
	h := sha256.Sum256([]byte(abs))
	repoID := fmt.Sprintf("%x", h[:8]) // 16 hex chars
	dir := paths.In("deepwiki", "graphs", repoID)
	return &GraphStore{dir: dir, repoID: repoID}, nil
}

// Dir returns the on-disk path of the cache directory for this repo.
func (s *GraphStore) Dir() string { return s.dir }

// RepoID returns the repo's cache identifier (short hash of the repo path).
func (s *GraphStore) RepoID() string { return s.repoID }

// ---------------------------------------------------------------------------
// Freshness check
// ---------------------------------------------------------------------------

// IsFresh returns true if a valid cached graph exists and the repo's file
// tree has not changed since the graph was built.
//
// Uses a pure stat-walk (no file reads) so this is fast even on large repos.
func (s *GraphStore) IsFresh(repoPath string, excludeDirs []string) bool {
	meta, err := s.loadMeta()
	if err != nil || meta == nil {
		return false
	}
	// Also verify the graph file itself exists
	if _, err := os.Stat(filepath.Join(s.dir, "graph.json.gz")); err != nil {
		return false
	}
	current, err := repoFingerprint(repoPath, excludeDirs)
	if err != nil {
		return false
	}
	return meta.Fingerprint == current
}

// ---------------------------------------------------------------------------
// Save
// ---------------------------------------------------------------------------

// SaveGraph writes the CodeGraph to disk as a gzip-compressed JSON file and
// updates the metadata. Overwrites any existing cache for this repo.
//
// backend is the name of the embedding backend that was used (e.g.
// "ollama/nomic-embed-text", "openai/text-embedding-3-small", "none").
func (s *GraphStore) SaveGraph(graph *CodeGraph, backend string) error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return fmt.Errorf("graph store: create dir: %w", err)
	}

	// Write graph.json.gz
	gzPath := filepath.Join(s.dir, "graph.json.gz")
	f, err := os.Create(gzPath)
	if err != nil {
		return fmt.Errorf("graph store: create graph file: %w", err)
	}
	gz := gzip.NewWriter(f)
	if encErr := json.NewEncoder(gz).Encode(graph); encErr != nil {
		f.Close()
		return fmt.Errorf("graph store: encode graph: %w", encErr)
	}
	if closeErr := gz.Close(); closeErr != nil {
		f.Close()
		return fmt.Errorf("graph store: close gzip writer: %w", closeErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		return fmt.Errorf("graph store: close graph file: %w", closeErr)
	}

	// Compute fingerprint of current repo state
	fp, err := repoFingerprint(graph.RepoPath, nil)
	if err != nil {
		fp = "unknown"
	}

	// Write meta.json
	meta := graphMeta{
		RepoPath:      graph.RepoPath,
		RepoID:        s.repoID,
		Fingerprint:   fp,
		AnalyzedAt:    graph.AnalyzedAt,
		TotalFiles:    graph.TotalFiles,
		TotalEntities: graph.TotalEntities,
		TotalChunks:   graph.TotalChunks,
		Backend:       backend,
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("graph store: marshal meta: %w", err)
	}
	return atomicfile.Write(filepath.Join(s.dir, "meta.json"), metaBytes, atomicfile.WithPerm(0o644))
}

// ---------------------------------------------------------------------------
// Load
// ---------------------------------------------------------------------------

// LoadGraph reads and decompresses the cached graph from disk.
// Returns (nil, nil) if no cache file exists.
func (s *GraphStore) LoadGraph() (*CodeGraph, error) {
	gzPath := filepath.Join(s.dir, "graph.json.gz")
	f, err := os.Open(gzPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("graph store: open: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("graph store: open gzip reader: %w", err)
	}
	defer gz.Close()

	var graph CodeGraph
	if err := json.NewDecoder(gz).Decode(&graph); err != nil {
		return nil, fmt.Errorf("graph store: decode graph: %w", err)
	}
	return &graph, nil
}

// Meta returns the stored metadata without loading the full graph.
// Returns (nil, nil) if no cache exists.
func (s *GraphStore) Meta() (*graphMeta, error) {
	return s.loadMeta()
}

// Invalidate deletes the cached graph for this repo.
func (s *GraphStore) Invalidate() error {
	return os.RemoveAll(s.dir)
}

func (s *GraphStore) loadMeta() (*graphMeta, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, "meta.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m graphMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ---------------------------------------------------------------------------
// Repo fingerprint — pure stat walk, no file reads
// ---------------------------------------------------------------------------

// repoFingerprint computes a deterministic hash of the repo's file tree
// based on relative file paths, file sizes, and modification times.
// No file content is read — this is intentionally a fast metadata-only walk.
//
// If any file is added, removed, renamed, or modified (size/mtime change),
// the fingerprint changes and the cached graph will be considered stale.
func repoFingerprint(repoPath string, excludeDirs []string) (string, error) {
	// Merge caller excludes with the universal defaults
	excludeSet := make(map[string]bool, len(defaultExcludeDirs)+len(excludeDirs))
	for _, d := range defaultExcludeDirs {
		excludeSet[d] = true
	}
	for _, d := range excludeDirs {
		excludeSet[d] = true
	}

	var entries []string
	err := filepath.WalkDir(repoPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			// Never skip the walk root itself
			if path == repoPath {
				return nil
			}
			if excludeSet[name] || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(repoPath, path)
		entries = append(entries, fmt.Sprintf("%s:%d:%d", rel, info.Size(), info.ModTime().Unix()))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("repo fingerprint: walk: %w", err)
	}

	sort.Strings(entries) // sort for determinism regardless of walk order
	h := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return fmt.Sprintf("%x", h), nil
}
