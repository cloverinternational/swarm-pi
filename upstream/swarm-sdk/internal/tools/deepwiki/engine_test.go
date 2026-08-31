package deepwiki

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// TestAnalyzeSelf runs the analyzer against the deepwiki package itself.
func TestAnalyzeSelf(t *testing.T) {
	eng, err := NewEngine(".")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	graph, err := eng.Analyze(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if graph.TotalEntities == 0 {
		t.Fatal("expected entities, got 0")
	}
	if graph.TotalFiles == 0 {
		t.Fatal("expected files, got 0")
	}
	if graph.TotalChunks == 0 {
		t.Fatal("expected chunks, got 0")
	}
	if graph.TotalEdges == 0 {
		t.Fatal("expected edges, got 0")
	}

	t.Logf("Files: %d, Entities: %d, Edges: %d, Chunks: %d, Language: %s",
		graph.TotalFiles, graph.TotalEntities, graph.TotalEdges, graph.TotalChunks, graph.Language)

	// Verify key types from this package
	keyTypes := []string{"deepwiki.Engine", "deepwiki.CodeGraph", "deepwiki.CodeEntity", "deepwiki.Analyzer"}
	for _, name := range keyTypes {
		if _, ok := graph.Entities[name]; !ok {
			t.Errorf("expected to find entity %q", name)
		}
	}

	// Verify key functions
	keyFuncs := []string{"deepwiki.NewEngine", "deepwiki.NewAnalyzer", "deepwiki.NewChunker"}
	for _, name := range keyFuncs {
		if _, ok := graph.Entities[name]; !ok {
			t.Errorf("expected to find function %q", name)
		}
	}

	// Verify contains edges
	containsCount := 0
	for _, edge := range graph.Edges {
		if edge.Kind == EdgeContains {
			containsCount++
		}
	}
	if containsCount == 0 {
		t.Error("expected 'contains' edges")
	}

	// Verify chunks have content
	for id, chunk := range graph.Chunks {
		if chunk.Content == "" {
			t.Errorf("chunk %s has empty content", id)
		}
	}

	stats := eng.Stats()
	t.Logf("Entity counts: %v", stats.EntityCounts)

	count := 0
	for qname, ent := range graph.Entities {
		if count >= 10 {
			break
		}
		sig := ent.Signature
		if len(sig) > 80 {
			sig = sig[:80] + "..."
		}
		fmt.Printf("  %s %s: %s\n", ent.Kind, qname, sig)
		count++
	}
}

// TestAnalyzeParentSDK runs the analyzer against the full swarm-sdk.
func TestAnalyzeParentSDK(t *testing.T) {
	eng, err := NewEngine("../..")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	graph, err := eng.Analyze(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("SDK Analysis — Files: %d, Entities: %d, Edges: %d, Chunks: %d, Language: %s",
		graph.TotalFiles, graph.TotalEntities, graph.TotalEdges, graph.TotalChunks, graph.Language)

	stats := eng.Stats()
	t.Logf("Entity breakdown: %v", stats.EntityCounts)

	if graph.Language != "go" {
		t.Errorf("expected primary language 'go', got %q", graph.Language)
	}
	if graph.TotalEntities < 50 {
		t.Errorf("expected at least 50 entities in SDK, got %d", graph.TotalEntities)
	}
}

// TestAnalyzeDeepWiki runs against the cloned deepwiki-open (Python+TS).
func TestAnalyzeDeepWiki(t *testing.T) {
	repoPath := "../../../deepwiki-open"
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		t.Skip("deepwiki-open not cloned")
	}

	eng, err := NewEngine(repoPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	graph, err := eng.Analyze(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("DeepWiki Analysis — Files: %d, Entities: %d, Edges: %d, Chunks: %d, Language: %s",
		graph.TotalFiles, graph.TotalEntities, graph.TotalEdges, graph.TotalChunks, graph.Language)

	stats := eng.Stats()
	t.Logf("Entity breakdown: %v", stats.EntityCounts)

	if stats.EntityCounts["function"] == 0 && stats.EntityCounts["class"] == 0 {
		t.Error("expected to find functions or classes in deepwiki-open")
	}
}
