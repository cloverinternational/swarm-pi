package plugins

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestOfficialClaudePluginsSource(t *testing.T) {
	// Test that we can fetch the official Claude plugins marketplace
	url := "https://raw.githubusercontent.com/anthropics/claude-plugins-official/main/.claude-plugin/marketplace.json"

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("Failed to fetch official Claude plugins: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	var index ClaudeMarketplaceIndex
	if err := json.NewDecoder(resp.Body).Decode(&index); err != nil {
		t.Fatalf("Failed to decode marketplace JSON: %v", err)
	}

	if len(index.Plugins) == 0 {
		t.Fatal("Expected plugins in the marketplace, got 0")
	}

	t.Logf("Found %d plugins in official Claude marketplace", len(index.Plugins))

	// Check for specific known plugins
	foundPlugins := map[string]bool{
		"feature-dev":    false,
		"code-review":    false,
		"plugin-dev":     false,
		"typescript-lsp": false,
	}

	for _, plugin := range index.Plugins {
		if _, ok := foundPlugins[plugin.Name]; ok {
			foundPlugins[plugin.Name] = true
			t.Logf("  Found expected plugin: %s - %s", plugin.Name, plugin.Description)
		}
	}

	// Verify all expected plugins were found
	for name, found := range foundPlugins {
		if !found {
			t.Errorf("Expected plugin %q not found in marketplace", name)
		}
	}
}

func TestMarketplaceDatabaseSources(t *testing.T) {
	db := NewMarketplaceDatabase("")

	sources := db.GetSources()
	if len(sources) == 0 {
		t.Fatal("Expected default sources, got 0")
	}

	// Check that claude-official source exists
	foundOfficial := false
	for _, src := range sources {
		t.Logf("Source: %s (type=%s, enabled=%v, priority=%d)", src.Name, src.Type, src.Enabled, src.Priority)
		if src.Name == "claude-official" {
			foundOfficial = true
			if !src.Enabled {
				t.Error("claude-official source should be enabled by default")
			}
			if src.Priority != 200 {
				t.Errorf("claude-official priority should be 200, got %d", src.Priority)
			}
		}
	}

	if !foundOfficial {
		t.Error("claude-official source not found in default sources")
	}
}

func TestMarketplaceDatabaseUpdate(t *testing.T) {
	db := NewMarketplaceDatabase("")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := db.Update(ctx); err != nil {
		t.Fatalf("Failed to update marketplace: %v", err)
	}

	all := db.GetAll()
	t.Logf("Total plugins from all sources: %d", len(all))

	if len(all) == 0 {
		t.Error("Expected plugins after update, got 0")
	}

	// Check for feature-dev specifically
	featureDev := db.Get("feature-dev")
	if featureDev == nil {
		t.Error("feature-dev plugin not found after marketplace update")
	} else {
		t.Logf("Found feature-dev: %s (source: %s)", featureDev.Description, featureDev.Marketplace)
	}

	// Search for "feature"
	results := db.Search("feature")
	t.Logf("Search for 'feature' returned %d results", len(results))
	for i, r := range results {
		if i < 5 { // Show first 5
			t.Logf("  %d. %s - %s", i+1, r.Name, r.Description)
		}
	}
}

func TestMarketplaceSearchOfficialPlugins(t *testing.T) {
	db := NewMarketplaceDatabase("")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := db.Update(ctx); err != nil {
		t.Fatalf("Failed to update marketplace: %v", err)
	}

	// Test searches for various Claude official plugins
	searches := []struct {
		query    string
		expected string // Expected plugin name to be in results
	}{
		{"feature", "feature-dev"},
		{"code review", "code-review"},
		{"typescript", "typescript-lsp"},
		{"gopls", "gopls-lsp"},
		{"rust", "rust-analyzer-lsp"},
	}

	for _, s := range searches {
		results := db.Search(s.query)
		found := false
		for _, r := range results {
			if r.Name == s.expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Search for %q should find %q, but it wasn't in the %d results", s.query, s.expected, len(results))
		} else {
			t.Logf("Search for %q found %q (total %d results)", s.query, s.expected, len(results))
		}
	}
}
