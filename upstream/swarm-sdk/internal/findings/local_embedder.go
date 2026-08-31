package findings

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LocalEmbedder uses a local GGUF model (llama.cpp) for embeddings
type LocalEmbedder struct {
	modelPath    string
	llamaCppPath string
	dimension    int
	batchSize    int

	// Cache
	cache   map[string][]float32
	cacheMu sync.RWMutex

	// Stats
	totalCalls int
	totalTime  time.Duration
}

// LocalEmbedderConfig holds configuration for local embedding
type LocalEmbedderConfig struct {
	ModelPath    string // Path to .gguf model
	LlamaCppPath string // Path to llama.cpp embedding executable
	Dimension    int    // Embedding dimension (depends on model)
	BatchSize    int    // Batch size for embedding generation
	CacheSize    int    // Max cache entries
}

// DefaultLocalEmbedderConfig returns defaults for nomic-embed setup
func DefaultLocalEmbedderConfig() LocalEmbedderConfig {
	// Common paths to check for nomic embed model
	modelPaths := []string{
		"/opt/models/nomic-embed-text-v1.Q4_K_M.gguf",
		"/opt/models/nomic-embed-matryoshka-Q4_0.gguf",
		"/home/swarm/models/nomic-embed-text-v1.Q4_K_M.gguf",
		"/home/swarm/models/nomic-embed-matryoshka-Q4_0.gguf",
	}

	// Find first existing model
	modelPath := ""
	for _, path := range modelPaths {
		if _, err := os.Stat(path); err == nil {
			modelPath = path
			break
		}
	}

	home, _ := os.UserHomeDir()

	return LocalEmbedderConfig{
		ModelPath:    modelPath,
		LlamaCppPath: filepath.Join(home, "llama.cpp", "embedding"),
		Dimension:    768, // Nomic-embed dimension
		BatchSize:    1,
		CacheSize:    10000,
	}
}

// NewLocalEmbedder creates a new local embedder
func NewLocalEmbedder(config LocalEmbedderConfig) (*LocalEmbedder, error) {
	// Validate model exists
	if _, err := os.Stat(config.ModelPath); err != nil {
		return nil, fmt.Errorf("model not found at %s: %w", config.ModelPath, err)
	}

	// llama.cpp embedding binary is optional - we'll use fallback if not found
	if _, err := os.Stat(config.LlamaCppPath); err != nil {
		fmt.Printf("[embedder] llama.cpp not found at %s, will use fallback\n", config.LlamaCppPath)
	}

	return &LocalEmbedder{
		modelPath:    config.ModelPath,
		llamaCppPath: config.LlamaCppPath,
		dimension:    config.Dimension,
		batchSize:    config.BatchSize,
		cache:        make(map[string][]float32),
	}, nil
}

// Embed generates embeddings for text
func (le *LocalEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	start := time.Now()
	defer func() {
		le.totalTime += time.Since(start)
		le.totalCalls++
	}()

	// Check cache
	le.cacheMu.RLock()
	if cached, ok := le.cache[text]; ok {
		le.cacheMu.RUnlock()
		return cached, nil
	}
	le.cacheMu.RUnlock()

	// Try llama.cpp if available
	if _, err := os.Stat(le.llamaCppPath); err == nil {
		embedding, err := le.embedWithLlamaCpp(ctx, text)
		if err == nil {
			le.cacheResult(text, embedding)
			return embedding, nil
		}
		// Fall back to hash-based on error
		fmt.Printf("[embedder] llama.cpp failed: %v, using fallback\n", err)
	}

	// Fallback: use hash-based deterministic embedding
	embedding := le.embedWithHash(text)
	le.cacheResult(text, embedding)
	return embedding, nil
}

// embedWithLlamaCpp uses llama.cpp for actual embeddings
func (le *LocalEmbedder) embedWithLlamaCpp(ctx context.Context, text string) ([]float32, error) {
	// Create temp file for input
	tmpFile, err := os.CreateTemp("", "embed-input-*.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write text to file
	if _, err := tmpFile.WriteString(text); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("failed to write to temp file: %w", err)
	}
	tmpFile.Close()

	// Run llama.cpp embedding
	// Command: ./embedding -m model.gguf -f input.txt --pooling mean
	cmd := exec.CommandContext(ctx, le.llamaCppPath,
		"-m", le.modelPath,
		"-f", tmpFile.Name(),
		"--pooling", "mean",
		"-ngl", "99", // GPU layers
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("llama.cpp failed: %w", err)
	}

	// Parse output - llama.cpp outputs space-separated floats
	return parseEmbeddingOutput(string(output))
}

// embedWithHash creates deterministic embeddings from text hash
// This is a fallback when llama.cpp is not available
func (le *LocalEmbedder) embedWithHash(text string) []float32 {
	// Use multiple hash functions for better distribution
	vector := make([]float32, le.dimension)

	// Create several different hashes
	hashes := []uint64{
		fnv64a(text + "1"),
		fnv64a(text + "2"),
		fnv64a(text + "3"),
		fnv64a(text + "4"),
	}

	// Fill vector from hashes
	for i := 0; i < le.dimension; i++ {
		hashIdx := i % len(hashes)
		hash := hashes[hashIdx]

		// Extract byte at position
		bytePos := (i / len(hashes)) % 8
		byteVal := (hash >> (bytePos * 8)) & 0xFF

		// Normalize to [-1, 1]
		vector[i] = float32(int(byteVal)-128) / 128.0
	}

	return vector
}

// cacheResult stores embedding in cache
func (le *LocalEmbedder) cacheResult(text string, embedding []float32) {
	le.cacheMu.Lock()
	defer le.cacheMu.Unlock()

	// Simple LRU: just add, will be cleaned up periodically
	le.cache[text] = embedding

	// If cache too big, clear it
	if len(le.cache) > 10000 {
		le.cache = make(map[string][]float32)
		le.cache[text] = embedding // Keep this one
	}
}

// Dimension returns the embedding dimension
func (le *LocalEmbedder) Dimension() int {
	return le.dimension
}

// Name returns the model name
func (le *LocalEmbedder) Name() string {
	return filepath.Base(le.modelPath)
}

// Stats returns usage statistics
func (le *LocalEmbedder) Stats() map[string]any {
	le.cacheMu.RLock()
	defer le.cacheMu.RUnlock()

	avgTime := time.Duration(0)
	if le.totalCalls > 0 {
		avgTime = le.totalTime / time.Duration(le.totalCalls)
	}

	return map[string]any{
		"model":       le.modelPath,
		"dimension":   le.dimension,
		"cache_size":  len(le.cache),
		"total_calls": le.totalCalls,
		"total_time":  le.totalTime.String(),
		"avg_time":    avgTime.String(),
	}
}

// parseEmbeddingOutput parses llama.cpp output
func parseEmbeddingOutput(output string) ([]float32, error) {
	// llama.cpp outputs space-separated floats
	parts := strings.Fields(output)

	embedding := make([]float32, len(parts))
	for i, p := range parts {
		var val float32
		if _, err := fmt.Sscanf(p, "%f", &val); err != nil {
			continue // Skip invalid
		}
		embedding[i] = val
	}

	return embedding, nil
}

// fnv64a implements FNV-1a 64-bit hash
func fnv64a(s string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)

	hash := uint64(offset64)
	for i := 0; i < len(s); i++ {
		hash ^= uint64(s[i])
		hash *= prime64
	}

	return hash
}

// SimpleLocalEmbedder is a simplified version that always uses hash-based embeddings
// Useful for testing without GPU
func NewSimpleLocalEmbedder(dimension int) EmbeddingModel {
	return &localEmbedderSimple{
		dimension: dimension,
	}
}

type localEmbedderSimple struct {
	dimension int
}

func (l *localEmbedderSimple) Embed(ctx context.Context, text string) ([]float32, error) {
	vector := make([]float32, l.dimension)

	// Use FNV hash for deterministic embeddings
	hash := fnv64a(text)

	for i := 0; i < l.dimension; i++ {
		// Rotate hash for each position
		hash = (hash * 1099511628211) ^ uint64(i)

		// Use middle bits for value
		val := int((hash>>16)&0xFF) - 128
		vector[i] = float32(val) / 128.0
	}

	return vector, nil
}

func (l *localEmbedderSimple) Dimension() int {
	return l.dimension
}

func (l *localEmbedderSimple) Name() string {
	return fmt.Sprintf("simple-hash-%d", l.dimension)
}
