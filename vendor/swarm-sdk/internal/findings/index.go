package findings

import (
	"context"
	"fmt"
)

// InMemoryIndex is a simple in-memory semantic index for development.
// In production, this would use FAISS or a similar vector database.
type InMemoryIndex struct {
	findings map[string]Finding
	// In a real implementation, this would store vectors
	// and use approximate nearest neighbor search
}

// NewInMemoryIndex creates a new in-memory semantic index.
func NewInMemoryIndex() *InMemoryIndex {
	return &InMemoryIndex{
		findings: make(map[string]Finding),
	}
}

// Index adds or updates a finding in the semantic index.
// In this simple implementation, we just store the finding.
// A real implementation would:
// 1. Generate an embedding vector from the finding content
// 2. Add it to a vector index (FAISS, Annoy, etc.)
// 3. Store the mapping from vector ID to finding ID
func (idx *InMemoryIndex) Index(ctx context.Context, finding Finding) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if finding.FindingID == "" {
		return fmt.Errorf("finding must have an ID to be indexed")
	}

	idx.findings[finding.FindingID] = finding
	return nil
}

// SemanticSearch finds similar findings by semantic query.
// In this simple implementation, we just do text matching.
// A real implementation would:
// 1. Generate an embedding from the query
// 2. Search the vector index for nearest neighbors
// 3. Return the closest findings
func (idx *InMemoryIndex) SemanticSearch(ctx context.Context, query string, limit int) ([]FindingResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Simple text-based search for now
	// In production, this would use vector similarity
	var results []FindingResult

	for _, finding := range idx.findings {
		score := calculateSemanticScore(finding, query)
		if score > 0 {
			results = append(results, FindingResult{
				Finding:   finding,
				Score:     score,
				MatchType: "semantic",
			})
		}
	}

	// Sort by score
	for i := range results {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// calculateSemanticScore computes a simple relevance score.
// In production, this would use cosine similarity of embeddings.
func calculateSemanticScore(f Finding, query string) float64 {
	score := 0.0

	// Match tool name
	if f.ToolName == query {
		score += 1.0
	}

	// Match context summary keywords
	// This is a very naive implementation
	for _, tag := range f.Tags {
		if tag == query {
			score += 0.5
		}
	}

	// Recency bonus
	// (would be calculated properly in real implementation)

	return score
}

// Delete removes a finding from the index.
func (idx *InMemoryIndex) Delete(ctx context.Context, findingID string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	delete(idx.findings, findingID)
	return nil
}

// Close releases resources.
func (idx *InMemoryIndex) Close() error {
	idx.findings = nil
	return nil
}

// NoOpIndex is a semantic index that does nothing.
// Useful when semantic indexing is disabled.
type NoOpIndex struct{}

// NewNoOpIndex creates a no-op semantic index.
func NewNoOpIndex() *NoOpIndex {
	return &NoOpIndex{}
}

func (idx *NoOpIndex) Index(ctx context.Context, finding Finding) error {
	return nil
}

func (idx *NoOpIndex) SemanticSearch(ctx context.Context, query string, limit int) ([]FindingResult, error) {
	return []FindingResult{}, nil
}

func (idx *NoOpIndex) Delete(ctx context.Context, findingID string) error {
	return nil
}

func (idx *NoOpIndex) Close() error {
	return nil
}
