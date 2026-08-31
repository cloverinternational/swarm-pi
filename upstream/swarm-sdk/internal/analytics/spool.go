package analytics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

type spooledEvent struct {
	Path  string
	Event EventEnvelope
}

type Spool struct {
	dir         string
	maxBytes    int64
	maxFiles    int
	mu          sync.Mutex
	cachedSize  int64    // running total of .json bytes on disk; -1 = uninitialized
	cachedNames []string // sorted .json filenames (base names only); nil = uninitialized
}

func NewSpool(dir string, maxBytes int64, maxFiles int) (*Spool, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create spool dir: %w", err)
	}
	return &Spool{dir: dir, maxBytes: maxBytes, maxFiles: maxFiles, cachedSize: -1}, nil
}

func (s *Spool) Enqueue(event EventEnvelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filename := fmt.Sprintf("%s_%s.json", time.Now().UTC().Format("20060102T150405.000000000"), event.EventID)
	path := filepath.Join(s.dir, filename)
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, body, 0o644); err != nil {
		return fmt.Errorf("write spool temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename spool temp: %w", err)
	}
	s.cachedSize += int64(len(body))
	if s.cachedNames != nil {
		i, _ := slices.BinarySearch(s.cachedNames, filename)
		s.cachedNames = slices.Insert(s.cachedNames, i, filename)
	}
	return s.pruneIfNeeded()
}

func (s *Spool) LoadBatch(limit int) ([]spooledEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	names, err := s.namesSorted()
	if err != nil {
		return nil, fmt.Errorf("read spool dir: %w", err)
	}
	if limit > 0 && len(names) > limit {
		names = names[:limit]
	}
	batch := make([]spooledEvent, 0, len(names))
	for _, name := range names {
		path := filepath.Join(s.dir, name)
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read spooled event: %w", err)
		}
		var event EventEnvelope
		if err := json.Unmarshal(body, &event); err != nil {
			return nil, fmt.Errorf("decode spooled event: %w", err)
		}
		batch = append(batch, spooledEvent{Path: path, Event: event})
	}
	return batch, nil
}

func (s *Spool) Delete(paths []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, path := range paths {
		if info, err := os.Stat(path); err == nil {
			s.cachedSize -= info.Size()
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove spooled event: %w", err)
		}
		if s.cachedNames != nil {
			name := filepath.Base(path)
			if i, ok := slices.BinarySearch(s.cachedNames, name); ok {
				s.cachedNames = slices.Delete(s.cachedNames, i, i+1)
			}
		}
	}
	if s.cachedSize < 0 {
		s.cachedSize = 0
	}
	return nil
}

// namesSorted returns the sorted list of .json filenames, initializing the
// cache from disk on the first call. Caller must hold s.mu.
func (s *Spool) namesSorted() ([]string, error) {
	if s.cachedNames != nil {
		return s.cachedNames, nil
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		names = append(names, entry.Name())
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
	}
	sort.Strings(names)
	s.cachedNames = names
	if s.cachedSize < 0 {
		s.cachedSize = total
	}
	return s.cachedNames, nil
}

// pruneIfNeeded enforces maxBytes and maxFiles limits. Called after each Enqueue.
// Caller must hold s.mu.
func (s *Spool) pruneIfNeeded() error {
	if s.cachedSize < 0 {
		if _, err := s.namesSorted(); err != nil {
			return err
		}
	}
	overBytes := s.maxBytes > 0 && s.cachedSize > s.maxBytes
	overFiles := s.maxFiles > 0 && len(s.cachedNames) > s.maxFiles
	if !overBytes && !overFiles {
		return nil
	}
	return s.pruneToLimit()
}

// pruneToLimit deletes oldest files until under maxBytes and maxFiles limits.
// Caller must hold s.mu.
func (s *Spool) pruneToLimit() error {
	names, err := s.namesSorted()
	if err != nil {
		return err
	}
	// Re-scan sizes since we need accurate per-file sizes to track removal.
	var total int64
	type entry struct {
		name string
		size int64
	}
	files := make([]entry, 0, len(names))
	for _, name := range names {
		info, err := os.Stat(filepath.Join(s.dir, name))
		if err != nil {
			continue
		}
		total += info.Size()
		files = append(files, entry{name: name, size: info.Size()})
	}
	overBytes := func() bool { return s.maxBytes > 0 && total > s.maxBytes }
	overFiles := func() bool { return s.maxFiles > 0 && len(files) > s.maxFiles }
	for (overBytes() || overFiles()) && len(files) > 0 {
		oldest := files[0]
		files = files[1:]
		path := filepath.Join(s.dir, oldest.name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		total -= oldest.size
		if i, ok := slices.BinarySearch(s.cachedNames, oldest.name); ok {
			s.cachedNames = slices.Delete(s.cachedNames, i, i+1)
		}
	}
	s.cachedSize = total
	return nil
}

// PruneToLimits enforces maxBytes and maxFiles limits. Safe to call from outside
// the enqueue hot-path (e.g. startup cleanup after age-based pruning).
func (s *Spool) PrunToLimits() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pruneIfNeeded()
}

// PruneOlderThan deletes spooled events whose filename timestamp predates cutoff.
// Events that can't be delivered (backend unreachable) would otherwise accumulate
// forever; this caps retention to a reasonable window.
func (s *Spool) PruneOlderThan(maxAge time.Duration) error {
	if maxAge <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	names, err := s.namesSorted()
	if err != nil {
		return err
	}
	cutoff := time.Now().UTC().Add(-maxAge)
	for _, name := range names {
		t, err := spoolFileTime(name)
		if err != nil || !t.Before(cutoff) {
			continue
		}
		path := filepath.Join(s.dir, name)
		info, _ := os.Stat(path)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if info != nil {
			s.cachedSize -= info.Size()
		}
		if i, ok := slices.BinarySearch(s.cachedNames, name); ok {
			s.cachedNames = slices.Delete(s.cachedNames, i, i+1)
		}
	}
	if s.cachedSize < 0 {
		s.cachedSize = 0
	}
	return nil
}

// spoolFileTime parses the UTC timestamp embedded in a spool filename.
// Filenames are formatted as "20060102T150405.000000000_<rest>.json".
func spoolFileTime(filename string) (time.Time, error) {
	// Strip directory prefix if present.
	base := filepath.Base(filename)
	// Find the underscore that separates timestamp from the rest.
	before, _, ok := strings.Cut(base, "_")
	if !ok {
		return time.Time{}, fmt.Errorf("no underscore in spool filename: %s", base)
	}
	return time.Parse("20060102T150405.000000000", before)
}
