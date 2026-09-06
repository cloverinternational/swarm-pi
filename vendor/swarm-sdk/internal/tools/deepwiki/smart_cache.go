package deepwiki

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"sort"
	"time"
)

// SmartCacheManager handles incremental wiki updates by tracking file hashes
// and identifying which pages need regeneration when source files change.
type SmartCacheManager struct {
	state *SmartCacheState
}

// NewSmartCacheManager creates a new cache manager with initialized state.
func NewSmartCacheManager() *SmartCacheManager {
	return &SmartCacheManager{
		state: &SmartCacheState{
			FileHashes:         make(map[string]string),
			FileToPages:        make(map[string][]string),
			PageContentHash:    make(map[string]string),
			LastFullGeneration: time.Time{},
			IncrementalUpdates: 0,
		},
	}
}

// LoadSmartCacheManager restores a cache manager from existing state.
func LoadSmartCacheManager(state *SmartCacheState) *SmartCacheManager {
	if state == nil {
		return NewSmartCacheManager()
	}
	if state.FileHashes == nil {
		state.FileHashes = make(map[string]string)
	}
	if state.FileToPages == nil {
		state.FileToPages = make(map[string][]string)
	}
	if state.PageContentHash == nil {
		state.PageContentHash = make(map[string]string)
	}
	return &SmartCacheManager{state: state}
}

// ComputeFileHash calculates SHA256 hash of file content.
func ComputeFileHash(filePath string) (string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file %s: %w", filePath, err)
	}
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:]), nil
}

// ComputeContentHash calculates hash of arbitrary content.
func ComputeContentHash(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

// DetectStalePages identifies which wiki pages need regeneration.
func (m *SmartCacheManager) DetectStalePages(
	pages map[string]WikiPage,
	currentHashes map[string]string,
) map[string][]string {
	stalePages := make(map[string][]string)

	for pageID, page := range pages {
		for _, filePath := range page.FilePaths {
			oldHash, existed := m.state.FileHashes[filePath]
			newHash, existsNow := currentHashes[filePath]

			if existed && !existsNow {
				stalePages[pageID] = append(stalePages[pageID], filePath+" (deleted)")
				continue
			}

			if existsNow && (!existed || oldHash != newHash) {
				stalePages[pageID] = append(stalePages[pageID], filePath)
			}
		}
	}

	return stalePages
}

// UpdateFileHashes refreshes cached file hashes.
func (m *SmartCacheManager) UpdateFileHashes(currentHashes map[string]string) {
	maps.Copy(m.state.FileHashes, currentHashes)
	for path := range m.state.FileHashes {
		if _, exists := currentHashes[path]; !exists {
			delete(m.state.FileHashes, path)
		}
	}
}

// BuildReverseIndex creates file -> pages mapping.
func (m *SmartCacheManager) BuildReverseIndex(pages map[string]WikiPage) {
	m.state.FileToPages = make(map[string][]string)
	for pageID, page := range pages {
		for _, filePath := range page.FilePaths {
			m.state.FileToPages[filePath] = append(m.state.FileToPages[filePath], pageID)
		}
	}
	for filePath := range m.state.FileToPages {
		sort.Strings(m.state.FileToPages[filePath])
	}
}

// UpdatePageHash records content hash for a generated page.
func (m *SmartCacheManager) UpdatePageHash(pageID string, content string) {
	m.state.PageContentHash[pageID] = ComputeContentHash(content)
}

// GetState returns current cache state.
func (m *SmartCacheManager) GetState() *SmartCacheState {
	return m.state
}

// MarkFullGeneration records complete regeneration.
func (m *SmartCacheManager) MarkFullGeneration() {
	m.state.LastFullGeneration = time.Now()
	m.state.IncrementalUpdates = 0
}

// MarkIncrementalUpdate records incremental update.
func (m *SmartCacheManager) MarkIncrementalUpdate() {
	m.state.IncrementalUpdates++
}
