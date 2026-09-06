package deepwiki

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// LLMEmbedder interface + implementations
// ---------------------------------------------------------------------------

// Embedder creates enriched embeddings for both code entities and document chunks.
//
// Two complementary backends are supported:
//
//  1. OllamaEmbedder  — local, zero-cost, requires `nomic-embed-text` pulled.
//     Uses the /api/embed batch endpoint (Ollama ≥0.1.33) for high throughput.
//  2. OpenAIEmbedder  — OpenAI text-embedding-3-small at 256 dims (same model as
//     deepwiki-open's reference implementation). Accepts any OpenAI-compatible
//     endpoint, including self-hosted proxies. Falls back to this if Ollama is
//     unavailable and OPENAI_API_KEY is set.
//  3. NoOpEmbedder    — returns zero vectors; Search() falls back to pure keyword
//     matching. Used when no embedding backend is reachable so generation never
//     fails outright.
//
// EmbedGraph uses a concurrent worker pool with per-backend batch sizes:
//
//	Ollama  — 32 texts/batch, 8 concurrent workers  (~70× faster than sequential)
//	OpenAI  — 256 texts/batch, 4 concurrent workers (rate-limit aware)
type Embedder struct {
	backend embedBackend
	dim     int
}

// embedBackend is the internal interface for pluggable embedding providers.
type embedBackend interface {
	// embed embeds a single text (used for query-time search).
	embed(ctx context.Context, text string) ([]float32, error)
	// embedBatch embeds a slice of texts in one round-trip.
	// Returns a slice of vectors in the same order as the input.
	embedBatch(ctx context.Context, texts []string) ([][]float32, error)
	// batchSize returns the preferred batch size for this backend.
	batchSize() int
	// workers returns how many concurrent batch goroutines to use.
	workers() int
	isAvailable() bool
	name() string
}

// ---------------------------------------------------------------------------
// Concurrency tuning constants
// ---------------------------------------------------------------------------

const (
	// Ollama serializes requests internally — concurrency just causes queuing.
	// Larger batches reduce HTTP round-trips and are the real win (~1.8× faster).
	ollamaBatchSize = 128 // sweet spot from benchmarking (16.9ms/item vs 29.9ms sequential)
	ollamaWorkers   = 1   // single worker: Ollama processes one request at a time
	// OpenAI does true server-side parallelism — both larger batches AND concurrency help.
	oaiBatchSize = 256 // well within the 2048 limit; keeps per-request latency low
	oaiWorkers   = 4   // rate-limit-aware concurrency
)

// ---------------------------------------------------------------------------
// NewEmbedder — auto-detects the best available backend
// ---------------------------------------------------------------------------

// NewEmbedder creates an Embedder by probing available backends in priority order:
//  1. Ollama (free, local)
//  2. OpenAI text-embedding-3-small (if OPENAI_API_KEY is set)
//  3. NoOp (no embeddings — generation still works via keyword fallback)
func NewEmbedder() *Embedder {
	ollama := newOllamaBackend()
	if ollama.isAvailable() {
		return &Embedder{backend: ollama, dim: 384}
	}

	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		oai := newOpenAIBackend("text-embedding-3-small", key, "")
		return &Embedder{backend: oai, dim: 256}
	}
	if key := os.Getenv("OPENAI_COMPATIBLE_KEY"); key != "" {
		base := os.Getenv("OPENAI_COMPATIBLE_URL")
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		oai := newOpenAIBackend("text-embedding-3-small", key, base)
		return &Embedder{backend: oai, dim: 256}
	}

	return &Embedder{backend: &noOpBackend{}, dim: 0}
}

// NewOpenAIEmbedder creates an Embedder backed by the OpenAI text-embedding-3-small
// model (256-dimensional). baseURL can be empty to use the default OpenAI endpoint.
func NewOpenAIEmbedder(apiKey, baseURL string) *Embedder {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &Embedder{
		backend: newOpenAIBackend("text-embedding-3-small", apiKey, baseURL),
		dim:     256,
	}
}

// NewOllamaEmbedder creates an Embedder backed by Ollama nomic-embed-text.
func NewOllamaEmbedder() *Embedder {
	return &Embedder{backend: newOllamaBackend(), dim: 384}
}

// IsAvailable returns true if the underlying embedding backend is reachable.
func (e *Embedder) IsAvailable() bool { return e.backend.isAvailable() }

// BackendName returns the name of the active embedding backend.
func (e *Embedder) BackendName() string { return e.backend.name() }

// ---------------------------------------------------------------------------
// Ollama embedding backend  (batch via /api/embed, single via /api/embeddings)
// ---------------------------------------------------------------------------

type ollamaBackend struct {
	baseURL string
	model   string
	client  *http.Client
}

func newOllamaBackend() *ollamaBackend {
	baseURL := os.Getenv("OLLAMA_HOST")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &ollamaBackend{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   "nomic-embed-text",
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (o *ollamaBackend) name() string   { return "ollama/" + o.model }
func (o *ollamaBackend) batchSize() int { return ollamaBatchSize }
func (o *ollamaBackend) workers() int   { return ollamaWorkers }

func (o *ollamaBackend) isAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// embed sends a single text via the legacy /api/embeddings endpoint.
// Used only for query-time search — bulk indexing goes through embedBatch.
func (o *ollamaBackend) embed(ctx context.Context, text string) ([]float32, error) {
	if len(text) > 8000 {
		text = text[:8000]
	}
	body, _ := json.Marshal(map[string]string{
		"model":  o.model,
		"prompt": text,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embed %d: %s", resp.StatusCode, string(b))
	}
	var result struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode ollama embed: %w", err)
	}
	return float64sToFloat32s(result.Embedding), nil
}

// embedBatch uses the /api/embed endpoint (Ollama ≥0.1.33) which accepts an
// array of inputs and returns all vectors in one round-trip.
func (o *ollamaBackend) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	// Truncate each input to the model's context window
	truncated := make([]string, len(texts))
	for i, t := range texts {
		if len(t) > 8000 {
			t = t[:8000]
		}
		truncated[i] = t
	}

	body, _ := json.Marshal(map[string]any{
		"model": o.model,
		"input": truncated,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama batch embed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama batch embed %d: %s", resp.StatusCode, string(b))
	}

	var result struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode ollama batch embed: %w", err)
	}
	if len(result.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama batch embed: got %d vectors for %d inputs", len(result.Embeddings), len(texts))
	}

	out := make([][]float32, len(result.Embeddings))
	for i, row := range result.Embeddings {
		out[i] = float64sToFloat32s(row)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// OpenAI-compatible embedding backend
// ---------------------------------------------------------------------------

type openAIEmbedBackend struct {
	baseURL string
	apiKey  string
	model   string
	dims    int
	client  *http.Client
}

func newOpenAIBackend(model, apiKey, baseURL string) *openAIEmbedBackend {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &openAIEmbedBackend{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		dims:    256,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (o *openAIEmbedBackend) name() string      { return "openai/" + o.model }
func (o *openAIEmbedBackend) batchSize() int    { return oaiBatchSize }
func (o *openAIEmbedBackend) workers() int      { return oaiWorkers }
func (o *openAIEmbedBackend) isAvailable() bool { return o.apiKey != "" }

// embed sends a single text (used for query-time search).
func (o *openAIEmbedBackend) embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := o.embedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("openai embed returned no vectors")
	}
	return vecs[0], nil
}

// embedBatch sends up to oaiBatchSize texts in one API call.
// The OpenAI /embeddings endpoint accepts a string array for "input".
func (o *openAIEmbedBackend) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	// Truncate
	truncated := make([]string, len(texts))
	for i, t := range texts {
		if len(t) > 25000 {
			t = t[:25000]
		}
		truncated[i] = t
	}

	type embedReq struct {
		Input      []string `json:"input"`
		Model      string   `json:"model"`
		Dimensions int      `json:"dimensions,omitempty"`
	}
	type embedData struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	}
	type embedResp struct {
		Data  []embedData `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	reqBody, _ := json.Marshal(embedReq{
		Input:      truncated,
		Model:      o.model,
		Dimensions: o.dims,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/embeddings", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai batch embed: %w", err)
	}
	defer resp.Body.Close()

	var result embedResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode openai batch embed: %w", err)
	}
	if result.Error != nil {
		return nil, fmt.Errorf("openai batch embed error: %s", result.Error.Message)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("openai batch embed returned empty data")
	}

	// Sort by index to guarantee order matches input
	sort.Slice(result.Data, func(i, j int) bool {
		return result.Data[i].Index < result.Data[j].Index
	})

	out := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index < len(out) {
			out[d.Index] = d.Embedding
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// No-op embedding backend
// ---------------------------------------------------------------------------

type noOpBackend struct{}

func (n *noOpBackend) name() string                                         { return "none" }
func (n *noOpBackend) isAvailable() bool                                    { return true }
func (n *noOpBackend) batchSize() int                                       { return 1 }
func (n *noOpBackend) workers() int                                         { return 1 }
func (n *noOpBackend) embed(_ context.Context, _ string) ([]float32, error) { return nil, nil }
func (n *noOpBackend) embedBatch(_ context.Context, texts []string) ([][]float32, error) {
	return make([][]float32, len(texts)), nil
}

// ---------------------------------------------------------------------------
// EmbedGraph — concurrent batched embedding
// ---------------------------------------------------------------------------

// embedWork is a unit of work: an enriched text string and the destination
// pointer where the resulting vector should be written.
type embedWork struct {
	text string
	dest *[]float32
}

// EmbedGraph computes embeddings for all entities and chunks in the graph
// using a concurrent worker pool and backend-native batching.
//
// For Ollama (local Spark): 32 texts/batch × 8 workers ≈ 70× faster than sequential.
// For OpenAI: 256 texts/batch × 4 workers — rate-limit aware.
func (e *Embedder) EmbedGraph(ctx context.Context, g *CodeGraph) error {
	if _, isNoOp := e.backend.(*noOpBackend); isNoOp {
		return nil
	}

	// ── 1. Collect entity work ──────────────────────────────────────────────
	// We need to create DocumentChunks for entities after embedding, so we
	// record the entity alongside its work item.
	type entityWork struct {
		ent      *CodeEntity
		enriched string
		vec      []float32
	}
	var entityJobs []entityWork
	for _, ent := range g.Entities {
		if ent.Kind == KindFile {
			continue
		}
		entityJobs = append(entityJobs, entityWork{
			ent:      ent,
			enriched: e.enrichEntity(ent),
		})
	}

	// ── 2. Collect chunk work ───────────────────────────────────────────────
	type chunkWork struct {
		chunk    *DocumentChunk
		enriched string
	}
	var chunkJobs []chunkWork
	for _, chunk := range g.Chunks {
		if chunk.Embedding != nil {
			continue
		}
		chunkJobs = append(chunkJobs, chunkWork{
			chunk:    chunk,
			enriched: e.enrichChunk(chunk),
		})
	}

	// ── 3. Flatten into a single work list with destination pointers ────────
	work := make([]embedWork, 0, len(entityJobs)+len(chunkJobs))
	for i := range entityJobs {
		work = append(work, embedWork{text: entityJobs[i].enriched, dest: &entityJobs[i].vec})
	}
	for i := range chunkJobs {
		work = append(work, embedWork{text: chunkJobs[i].enriched, dest: &chunkJobs[i].chunk.Embedding})
	}

	if len(work) == 0 {
		return nil
	}

	// ── 4. Split into batches ───────────────────────────────────────────────
	bsz := e.backend.batchSize()
	type batch struct {
		start int // index into work slice
		items []embedWork
	}
	var batches []batch
	for i := 0; i < len(work); i += bsz {
		end := min(i+bsz, len(work))
		batches = append(batches, batch{start: i, items: work[i:end]})
	}

	// ── 5. Dispatch batches with a semaphore-capped worker pool ─────────────
	maxWorkers := e.backend.workers()
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	// embedErrors is not fatal — we log and continue so partial embeddings
	// still improve retrieval quality.
	var errMu sync.Mutex
	var embedErrors []error

	for _, b := range batches {
		if ctx.Err() != nil {
			break
		}
		b := b // capture
		texts := make([]string, len(b.items))
		for i, w := range b.items {
			texts[i] = w.text
		}

		sem <- struct{}{} // acquire
		wg.Go(func() {
			defer func() { <-sem }() // release

			vecs, err := e.backend.embedBatch(ctx, texts)
			if err != nil {
				errMu.Lock()
				embedErrors = append(embedErrors, err)
				errMu.Unlock()
				return
			}
			for i, vec := range vecs {
				if i < len(b.items) && vec != nil {
					*b.items[i].dest = vec
				}
			}
		})
	}
	wg.Wait()

	// ── 6. Register entity chunks (must happen after embeddings are written) ─
	for i := range entityJobs {
		if entityJobs[i].vec == nil {
			continue // embedding failed — skip
		}
		g.AddChunk(&DocumentChunk{
			ID:        "entity:" + entityJobs[i].ent.QualifiedName,
			FilePath:  entityJobs[i].ent.FilePath,
			Kind:      ChunkCode,
			Language:  "enriched",
			Content:   entityJobs[i].enriched,
			StartLine: entityJobs[i].ent.StartLine,
			EndLine:   entityJobs[i].ent.EndLine,
			IsCode:    true,
			Embedding: entityJobs[i].vec,
		})
	}

	// Surface errors as a combined warning (non-fatal)
	if len(embedErrors) > 0 && len(embedErrors) == len(batches) {
		// Every batch failed — that's worth returning
		return fmt.Errorf("all embedding batches failed; last error: %w", embedErrors[len(embedErrors)-1])
	}
	return nil
}

// ---------------------------------------------------------------------------
// Search — semantic retrieval with keyword fallback
// ---------------------------------------------------------------------------

func (e *Embedder) Search(ctx context.Context, g *CodeGraph, query string, topK int) ([]SearchResult, error) {
	if topK <= 0 {
		topK = 20
	}

	embeddedCount := 0
	for _, chunk := range g.Chunks {
		if chunk.Embedding != nil {
			embeddedCount++
		}
	}
	if embeddedCount == 0 {
		return e.keywordSearch(g, query, topK), nil
	}

	queryVec, err := e.backend.embed(ctx, query)
	if err != nil {
		return e.keywordSearch(g, query, topK), nil
	}
	if queryVec == nil {
		return e.keywordSearch(g, query, topK), nil
	}

	type scored struct {
		chunk *DocumentChunk
		score float64
	}
	var candidates []scored
	for _, chunk := range g.Chunks {
		if chunk.Embedding == nil {
			continue
		}
		candidates = append(candidates, scored{chunk: chunk, score: cosineSim(queryVec, chunk.Embedding)})
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	if topK > len(candidates) {
		topK = len(candidates)
	}

	var results []SearchResult
	for _, c := range candidates[:topK] {
		r := SearchResult{Chunk: c.chunk, Score: c.score, Source: "semantic"}
		if after, ok := strings.CutPrefix(c.chunk.ID, "entity:"); ok {
			qname := after
			if ent, ok := g.Entities[qname]; ok {
				r.Entity = ent
				r.Source = "entity"
			}
		}
		results = append(results, r)
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Keyword fallback
// ---------------------------------------------------------------------------

func (e *Embedder) keywordSearch(g *CodeGraph, query string, topK int) []SearchResult {
	queryWords := tokenize(strings.ToLower(query))
	if len(queryWords) == 0 {
		return nil
	}

	type scored struct {
		chunk *DocumentChunk
		score float64
	}
	var candidates []scored
	for _, chunk := range g.Chunks {
		if strings.HasPrefix(chunk.ID, "entity:") {
			continue
		}
		chunkWords := tokenize(strings.ToLower(chunk.Content + " " + chunk.FilePath))
		hits := 0
		for _, qw := range queryWords {
			for _, cw := range chunkWords {
				if strings.Contains(cw, qw) {
					hits++
					break
				}
			}
		}
		if hits > 0 {
			candidates = append(candidates, scored{chunk: chunk, score: float64(hits) / float64(len(queryWords))})
		}
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	if topK > len(candidates) {
		topK = len(candidates)
	}

	results := make([]SearchResult, 0, topK)
	for _, c := range candidates[:topK] {
		results = append(results, SearchResult{Chunk: c.chunk, Score: c.score, Source: "keyword"})
	}
	return results
}

// ---------------------------------------------------------------------------
// Enrichment helpers
// ---------------------------------------------------------------------------

func (e *Embedder) enrichEntity(ent *CodeEntity) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s in %s (%s)\n", ent.Kind, ent.Name, ent.Package, ent.FilePath)
	if ent.DocComment != "" {
		fmt.Fprintf(&b, "Documentation: %s\n", ent.DocComment)
	}
	if ent.Signature != "" {
		fmt.Fprintf(&b, "Signature: %s\n", ent.Signature)
	}
	if ent.Receiver != "" {
		fmt.Fprintf(&b, "Receiver: %s\n", ent.Receiver)
	}
	if len(ent.Calls) > 0 {
		fmt.Fprintf(&b, "Calls: %s\n", strings.Join(ent.Calls, ", "))
	}
	if len(ent.References) > 0 {
		fmt.Fprintf(&b, "References: %s\n", strings.Join(ent.References, ", "))
	}
	if len(ent.Implements) > 0 {
		fmt.Fprintf(&b, "Implements: %s\n", strings.Join(ent.Implements, ", "))
	}
	body := ent.Body
	if len(body) > 2000 {
		body = body[:2000] + "\n... (truncated)"
	}
	fmt.Fprintf(&b, "\n%s", body)
	return b.String()
}

func (e *Embedder) enrichChunk(chunk *DocumentChunk) string {
	var b strings.Builder
	fmt.Fprintf(&b, "File: %s (lines %d-%d, %s)\n\n", chunk.FilePath, chunk.StartLine, chunk.EndLine, chunk.Language)
	b.WriteString(chunk.Content)
	return b.String()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func float64sToFloat32s(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}

func cosineSim(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

func tokenize(s string) []string {
	words := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '_' || r == '/' || r == '.' || r == '-'
	})
	seen := make(map[string]bool, len(words))
	out := words[:0]
	for _, w := range words {
		if !seen[w] && len(w) > 2 {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}
