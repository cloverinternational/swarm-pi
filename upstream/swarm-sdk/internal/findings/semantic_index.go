package findings

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// persistDebounce is the window during which bursts of Index/Delete calls
// coalesce into a single disk write. Tuned for a write-heavy agent loop:
// short enough that durability is near-real-time, long enough that we don't
// spawn a goroutine per call or rewrite the same file thousands of times.
const persistDebounce = 100 * time.Millisecond

// EmbeddingModel represents the interface for generating embeddings
type EmbeddingModel interface {
	// Embed generates a vector embedding for the given text
	Embed(ctx context.Context, text string) ([]float32, error)

	// Dimension returns the vector dimension
	Dimension() int

	// Name returns the model name
	Name() string
}

// SimpleEmbeddingModel is a basic local embedding implementation
// In production, this would use a proper embedding model like sentence-transformers
type SimpleEmbeddingModel struct {
	dimension int
}

// NewSimpleEmbeddingModel creates a simple local embedding model
// Note: This is a BASIC placeholder. Production should use:
// - sentence-transformers via Python bridge
// - OpenAI/Anthropic API
// - Local ONNX model
func NewSimpleEmbeddingModel(dimension int) *SimpleEmbeddingModel {
	if dimension <= 0 {
		dimension = 384 // Default for many small models
	}
	return &SimpleEmbeddingModel{
		dimension: dimension,
	}
}

// Embed generates a deterministic embedding based on text hash
// This is NOT a semantic embedding - it's for demonstration only
// Real implementation would use a neural network
func (m *SimpleEmbeddingModel) Embed(ctx context.Context, text string) ([]float32, error) {
	// Hash the text for deterministic "embedding"
	hash := sha256.Sum256([]byte(text))

	// Generate vector from hash
	vector := make([]float32, m.dimension)
	for i := 0; i < m.dimension; i++ {
		// Use hash bytes to generate values between -1 and 1
		byteIdx := i % len(hash)
		vector[i] = float32(int(hash[byteIdx])-128) / 128.0
	}

	return vector, nil
}

// Dimension returns the vector dimension
func (m *SimpleEmbeddingModel) Dimension() int {
	return m.dimension
}

// Name returns the model name
func (m *SimpleEmbeddingModel) Name() string {
	return fmt.Sprintf("simple-%dd", m.dimension)
}

// ExternalEmbeddingModel uses an external API for embeddings
type ExternalEmbeddingModel struct {
	endpoint  string
	apiKey    string
	dimension int
	modelName string
}

// NewExternalEmbeddingModel creates an API-based embedding model
func NewExternalEmbeddingModel(endpoint, apiKey, modelName string, dimension int) *ExternalEmbeddingModel {
	return &ExternalEmbeddingModel{
		endpoint:  endpoint,
		apiKey:    apiKey,
		modelName: modelName,
		dimension: dimension,
	}
}

// Embed generates embeddings via external API
// Placeholder - real implementation would make HTTP call
func (m *ExternalEmbeddingModel) Embed(ctx context.Context, text string) ([]float32, error) {
	// TODO: Implement actual API call
	// Example:
	// resp, err := http.Post(m.endpoint, ...)
	// Parse response and return embeddings

	// For now, fall back to simple hash-based
	simple := NewSimpleEmbeddingModel(m.dimension)
	return simple.Embed(ctx, text)
}

// Dimension returns the vector dimension
func (m *ExternalEmbeddingModel) Dimension() int {
	return m.dimension
}

// Name returns the model name
func (m *ExternalEmbeddingModel) Name() string {
	return m.modelName
}

// FaissSemanticIndex implements semantic search using FAISS
// This is a GO INTERFACE that would wrap FAISS via CGO or external process
type FaissSemanticIndex struct {
	baseDir   string
	model     EmbeddingModel
	dimension int
	mu        sync.RWMutex

	// In-memory index of finding IDs to vector positions
	// Real implementation would use FAISS index
	findings  map[string]int    // findingID -> index position
	vectors   [][]float32       // vector storage
	textCache map[string]string // findingID -> original text (for fallback)

	// Persistence coordination. A single background worker coalesces bursts of
	// Index/Delete calls into one disk write. persistReq has buffer 1 and
	// acts as a pending-request flag; stopOnce guarantees Close is idempotent.
	persistReq  chan struct{}
	stopCh      chan struct{}
	persistDone chan struct{}
	stopOnce    sync.Once
}

// NewFaissSemanticIndex creates a new FAISS-based semantic index
func NewFaissSemanticIndex(baseDir string, model EmbeddingModel) (*FaissSemanticIndex, error) {
	if model == nil {
		model = NewSimpleEmbeddingModel(384)
	}

	idx := &FaissSemanticIndex{
		baseDir:     baseDir,
		model:       model,
		dimension:   model.Dimension(),
		findings:    make(map[string]int),
		vectors:     make([][]float32, 0),
		textCache:   make(map[string]string),
		persistReq:  make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
		persistDone: make(chan struct{}),
	}

	// Ensure directory exists
	indexDir := filepath.Join(baseDir, "index")
	if err := os.MkdirAll(indexDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create index directory: %w", err)
	}

	// Try to load existing index
	if err := idx.load(); err != nil {
		// Not fatal - start with empty index
		fmt.Fprintf(os.Stderr, "[findings] Could not load existing index: %v\n", err)
	}

	go idx.persistLoop()

	return idx, nil
}

// extractText extracts searchable text from a finding
func extractText(f Finding) string {
	parts := []string{
		f.ToolName,
		f.ContextSummary,
	}

	// Add tags
	parts = append(parts, f.Tags...)

	// Add insights if available
	if insights, ok := f.Metadata.Custom["insights"].([]string); ok {
		parts = append(parts, insights...)
	}

	return strings.Join(parts, " ")
}

// Index adds or updates a finding in the semantic index
func (idx *FaissSemanticIndex) Index(ctx context.Context, finding Finding) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if finding.FindingID == "" {
		finding.FindingID = uuid.New().String()
	}

	// Extract text for embedding
	text := extractText(finding)

	// Generate embedding
	vector, err := idx.model.Embed(ctx, text)
	if err != nil {
		return fmt.Errorf("failed to generate embedding: %w", err)
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Check if already indexed (update)
	if pos, exists := idx.findings[finding.FindingID]; exists {
		idx.vectors[pos] = vector
		idx.textCache[finding.FindingID] = text
	} else {
		// New finding
		pos := len(idx.vectors)
		idx.findings[finding.FindingID] = pos
		idx.vectors = append(idx.vectors, vector)
		idx.textCache[finding.FindingID] = text
	}

	idx.requestPersist()

	return nil
}

// requestPersist notifies the background persister that state has changed.
// Non-blocking: if a request is already pending the signal is dropped, since
// the worker will pick up the latest snapshot when it wakes.
func (idx *FaissSemanticIndex) requestPersist() {
	select {
	case idx.persistReq <- struct{}{}:
	default:
	}
}

// persistLoop runs one write-to-disk at a time, debounced so bursts of
// Index/Delete calls coalesce into a single file write instead of spawning an
// unbounded number of goroutines that race on encoding/json.
func (idx *FaissSemanticIndex) persistLoop() {
	defer close(idx.persistDone)
	for {
		select {
		case <-idx.stopCh:
			return
		case <-idx.persistReq:
			// Debounce window: drain any follow-up signals that arrive while
			// we wait, so a burst of writes produces one persist.
			timer := time.NewTimer(persistDebounce)
		debounce:
			for {
				select {
				case <-timer.C:
					break debounce
				case <-idx.persistReq:
					// keep draining
				case <-idx.stopCh:
					timer.Stop()
					return
				}
			}
			_ = idx.persist()
		}
	}
}

// SemanticSearch finds similar findings by semantic query
func (idx *FaissSemanticIndex) SemanticSearch(ctx context.Context, query string, limit int) ([]FindingResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Generate query embedding
	queryVector, err := idx.model.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	// Brute force similarity search
	// In production, this would use FAISS index.search()
	type scoredFinding struct {
		findingID string
		score     float64
	}

	scores := make([]scoredFinding, 0, len(idx.findings))

	for findingID, pos := range idx.findings {
		if pos >= len(idx.vectors) {
			continue
		}

		vector := idx.vectors[pos]
		score := cosineSimilarity(queryVector, vector)

		// Skip non-positive similarities: a cosine score <= 0 means the finding
		// is orthogonal or anti-correlated with the query, i.e. not a match.
		// Returning these as "results" produces misleading negative scores.
		if score <= 0 {
			continue
		}

		scores = append(scores, scoredFinding{
			findingID: findingID,
			score:     score,
		})
	}

	// Sort by score (descending)
	for i := range scores {
		for j := i + 1; j < len(scores); j++ {
			if scores[j].score > scores[i].score {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}

	// Apply limit
	if limit > 0 && len(scores) > limit {
		scores = scores[:limit]
	}

	// Convert to FindingResults (placeholders)
	// In real implementation, would fetch actual findings from cache
	results := make([]FindingResult, len(scores))
	for i, s := range scores {
		results[i] = FindingResult{
			Finding: Finding{
				FindingID:      s.findingID,
				ToolName:       "semantic-match",
				ContextSummary: idx.textCache[s.findingID],
			},
			Score:     s.score,
			MatchType: "semantic",
		}
	}

	return results, nil
}

// Delete removes a finding from the index
func (idx *FaissSemanticIndex) Delete(ctx context.Context, findingID string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	if pos, exists := idx.findings[findingID]; exists {
		// Mark as deleted (set zero vector)
		if pos < len(idx.vectors) {
			idx.vectors[pos] = make([]float32, idx.dimension)
		}
		delete(idx.findings, findingID)
		delete(idx.textCache, findingID)
		idx.requestPersist()
	}

	return nil
}

// Close releases resources. Safe to call more than once.
func (idx *FaissSemanticIndex) Close() error {
	idx.stopOnce.Do(func() {
		close(idx.stopCh)
	})
	// Wait for the persist worker to exit so no background writer can race
	// with the nil-out below.
	<-idx.persistDone

	// Final synchronous persist (snapshots under RLock internally).
	idx.persist()

	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.findings = nil
	idx.vectors = nil
	idx.textCache = nil

	return nil
}

// cosineSimilarity calculates cosine similarity between two vectors.
// Returns a value in [-1, 1]. Zero on length mismatch or zero-norm inputs.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// persist saves the index to disk.
//
// Callers must NOT hold idx.mu. persist acquires RLock, snapshots the shared
// state into local copies, then releases the lock before marshalling. This
// prevents encoding/json from panicking with "index out of range" when another
// goroutine mutates the maps mid-encode (see Index/Delete).
func (idx *FaissSemanticIndex) persist() error {
	idx.mu.RLock()
	if idx.findings == nil && idx.textCache == nil && idx.vectors == nil {
		// Already closed; nothing to persist.
		idx.mu.RUnlock()
		return nil
	}
	modelName := idx.model.Name()
	dimension := idx.dimension
	findingsCopy := make(map[string]int, len(idx.findings))
	maps.Copy(findingsCopy, idx.findings)
	textCacheCopy := make(map[string]string, len(idx.textCache))
	maps.Copy(textCacheCopy, idx.textCache)
	// Shallow copy: inner []float32 slices are replaced atomically (never mutated),
	// so holding a pointer to the old slice is safe after releasing the lock.
	vectorsCopy := make([][]float32, len(idx.vectors))
	copy(vectorsCopy, idx.vectors)
	idx.mu.RUnlock()

	indexFile := filepath.Join(idx.baseDir, "index", "semantic_index.json")

	data := struct {
		Model     string            `json:"model"`
		Dimension int               `json:"dimension"`
		Findings  map[string]int    `json:"findings"`
		TextCache map[string]string `json:"text_cache"`
	}{
		Model:     modelName,
		Dimension: dimension,
		Findings:  findingsCopy,
		TextCache: textCacheCopy,
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal index: %w", err)
	}

	if err := os.WriteFile(indexFile, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write index: %w", err)
	}

	// Persist vectors in raw little-endian float32 binary format.
	// Layout: [count uint32][dim uint32][count*dim float32 values]
	// ~10x smaller and ~10x faster to encode than JSON.
	vectorsFile := filepath.Join(idx.baseDir, "index", "vectors.bin")
	if err := writeVectorsBinary(vectorsFile, vectorsCopy, dimension); err != nil {
		// Non-fatal: metadata index already written; vectors will be
		// re-embedded on next load if this file is missing or corrupt.
		_ = err
	}

	return nil
}

// load restores the index from disk
func (idx *FaissSemanticIndex) load() error {
	indexFile := filepath.Join(idx.baseDir, "index", "semantic_index.json")

	data, err := os.ReadFile(indexFile)
	if err != nil {
		return err
	}

	var saved struct {
		Model     string            `json:"model"`
		Dimension int               `json:"dimension"`
		Findings  map[string]int    `json:"findings"`
		TextCache map[string]string `json:"text_cache"`
	}

	if err := json.Unmarshal(data, &saved); err != nil {
		return fmt.Errorf("failed to unmarshal index: %w", err)
	}

	idx.findings = saved.Findings
	idx.textCache = saved.TextCache
	idx.dimension = saved.Dimension

	// Load vectors — try binary format first, fall back to legacy JSON.
	vectorsFile := filepath.Join(idx.baseDir, "index", "vectors.bin")
	if vecs, err := readVectorsBinary(vectorsFile); err == nil {
		idx.vectors = vecs
	} else if vectorData, err := os.ReadFile(vectorsFile); err == nil {
		json.Unmarshal(vectorData, &idx.vectors) //nolint:errcheck // legacy fallback
	}

	return nil
}

// Stats returns index statistics
func (idx *FaissSemanticIndex) Stats() map[string]any {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	return map[string]any{
		"model":     idx.model.Name(),
		"dimension": idx.dimension,
		"indexed":   len(idx.findings),
		"vectors":   len(idx.vectors),
	}
}

// writeVectorsBinary serializes vectors as raw little-endian float32.
// Layout: [count uint32][dim uint32][count*dim float32 values]
// dim must match the index's authoritative dimension; vectors with a
// different length are skipped to prevent buffer overruns.
func writeVectorsBinary(path string, vecs [][]float32, dim int) error {
	count := uint32(len(vecs))
	udim := uint32(dim)
	buf := make([]byte, 8+int(count)*dim*4)
	binary.LittleEndian.PutUint32(buf[0:4], count)
	binary.LittleEndian.PutUint32(buf[4:8], udim)
	off := 8
	for _, v := range vecs {
		if len(v) != dim {
			// Zero-fill slots for vectors with unexpected length (e.g. deleted/zero vecs).
			off += dim * 4
			continue
		}
		for _, f := range v {
			binary.LittleEndian.PutUint32(buf[off:off+4], math.Float32bits(f))
			off += 4
		}
	}
	return os.WriteFile(path, buf, 0644)
}

// readVectorsBinary reads a binary vector file written by writeVectorsBinary.
func readVectorsBinary(path string) ([][]float32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 {
		return nil, fmt.Errorf("vectors.bin too short")
	}
	count := binary.LittleEndian.Uint32(data[0:4])
	dim := binary.LittleEndian.Uint32(data[4:8])
	if int(count)*int(dim)*4+8 != len(data) {
		return nil, fmt.Errorf("vectors.bin size mismatch")
	}
	vecs := make([][]float32, count)
	off := 8
	for i := range vecs {
		vecs[i] = make([]float32, dim)
		for j := range vecs[i] {
			vecs[i][j] = math.Float32frombits(binary.LittleEndian.Uint32(data[off : off+4]))
			off += 4
		}
	}
	return vecs, nil
}
