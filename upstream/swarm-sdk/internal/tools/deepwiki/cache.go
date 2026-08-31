package deepwiki

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Cache handles persistence of generated wikis.
// Stores as JSON files in .swarm/deepwiki/ alongside the repo.
type Cache struct {
	dir string
}

// NewCache creates a cache for a repository.
func NewCache(repoPath string) *Cache {
	return &Cache{
		dir: filepath.Join(repoPath, ".swarm", "deepwiki"),
	}
}

// Save persists a generated wiki to disk.
func (c *Cache) Save(wiki *WikiCache) error {
	if err := os.MkdirAll(c.dir, 0755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	path := filepath.Join(c.dir, c.filename(wiki.Language))

	data, err := json.MarshalIndent(wiki, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal wiki: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// Load reads a cached wiki from disk.
func (c *Cache) Load() (*WikiCache, error) {
	return c.LoadLang("en")
}

// LoadLang reads a cached wiki for a specific language.
func (c *Cache) LoadLang(lang string) (*WikiCache, error) {
	path := filepath.Join(c.dir, c.filename(lang))

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no cache, not an error
		}
		return nil, fmt.Errorf("read cache: %w", err)
	}

	var wiki WikiCache
	if err := json.Unmarshal(data, &wiki); err != nil {
		return nil, fmt.Errorf("parse cache: %w", err)
	}

	return &wiki, nil
}

// Delete removes a cached wiki.
func (c *Cache) Delete(lang string) error {
	path := filepath.Join(c.dir, c.filename(lang))
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SaveGraph persists the code graph for inspection/debugging.
func (c *Cache) SaveGraph(graph *CodeGraph) error {
	if err := os.MkdirAll(c.dir, 0755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	path := filepath.Join(c.dir, "code_graph.json")

	data, err := json.MarshalIndent(graph, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal graph: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// LoadGraph reads a cached code graph.
func (c *Cache) LoadGraph() (*CodeGraph, error) {
	path := filepath.Join(c.dir, "code_graph.json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read graph: %w", err)
	}

	var graph CodeGraph
	if err := json.Unmarshal(data, &graph); err != nil {
		return nil, fmt.Errorf("parse graph: %w", err)
	}

	return &graph, nil
}

func (c *Cache) filename(lang string) string {
	if lang == "" {
		lang = "en"
	}
	return fmt.Sprintf("wiki_%s.json", lang)
}
