package deepwiki

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Engine is the main deepwiki library entry point.
// It is NOT a tool — it's a Go API that anything can call:
// TUI, Electron app, IPC server, agent tools, CLI, tests.
//
// Usage:
//
//	eng, err := deepwiki.NewEngine("/path/to/repo",
//	    deepwiki.WithProvider("ollama", "qwen3:1.7b"))
//	graph, err := eng.Analyze(ctx)
//	wiki, err := eng.GenerateWiki(ctx, deepwiki.GenerateRequest{...})
//	results, err := eng.Search(ctx, "how does auth work", 10)
type Engine struct {
	mu sync.RWMutex

	repoPath string
	repoName string

	// Analysis results
	graph *CodeGraph

	// Sub-systems
	analyzer    *Analyzer
	chunker     *Chunker
	embedder    *Embedder
	llm         LLMClient
	fallbackLLM LLMClient // optional; populated from env if creds available
	cache       *Cache

	// Config
	excludeDirs []string
	focusDirs   []string
}

// Option configures the engine.
type Option func(*Engine)

// WithProvider sets the LLM provider and model for wiki generation.
// If not called, the engine can still analyze and embed but not generate.
func WithProvider(provider, model string) Option {
	return func(e *Engine) {
		llm, err := NewLLMClient(provider, model)
		if err == nil {
			e.llm = llm
		}
	}
}

// WithLLM sets a pre-built LLM client directly.
func WithLLM(llm LLMClient) Option {
	return func(e *Engine) {
		e.llm = llm
	}
}

// WithFallbackLLM sets a fallback LLM used when the primary exhausts all
// compaction levels.  If not called, NewEngine auto-detects from the
// environment (GEMINI_API_KEY / GOOGLE_API_KEY → Gemini 2.5 Flash;
// OPENAI_API_KEY → gpt-4o-mini).
// Pass WithFallbackLLM(nil) to disable the automatic fallback.
func WithFallbackLLM(llm LLMClient) Option {
	return func(e *Engine) {
		e.fallbackLLM = llm
	}
}

// WithExcludeDirs sets directories to skip during analysis.
func WithExcludeDirs(dirs ...string) Option {
	return func(e *Engine) {
		e.excludeDirs = dirs
	}
}

// WithFocusDirs limits analysis to specific directories.
func WithFocusDirs(dirs ...string) Option {
	return func(e *Engine) {
		e.focusDirs = dirs
	}
}

// NewEngine creates a new deepwiki engine for a repository.
func NewEngine(repoPath string, opts ...Option) (*Engine, error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolve repo path: %w", err)
	}

	e := &Engine{
		repoPath:    absPath,
		repoName:    filepath.Base(absPath),
		excludeDirs: defaultExcludeDirs,
		fallbackLLM: NewDefaultFallbackLLM(), // auto-detected from env; nil if no creds
	}

	for _, opt := range opts {
		opt(e)
	}

	e.analyzer = NewAnalyzer()
	e.chunker = NewChunker()
	e.embedder = NewEmbedder()
	e.cache = NewCache(absPath)

	return e, nil
}

// Analyze runs both code-aware parsing AND document chunking on the repo.
// Returns the unified CodeGraph containing entities, edges, and chunks.
func (e *Engine) Analyze(ctx context.Context) (*CodeGraph, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	graph := NewCodeGraph(e.repoPath, e.repoName)

	// Walk the repo and collect files
	files, err := e.analyzer.WalkRepo(e.repoPath, e.excludeDirs, e.focusDirs)
	if err != nil {
		return nil, fmt.Errorf("walk repo: %w", err)
	}

	graph.TotalFiles = len(files)

	for _, f := range files {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Layer 1: Code-aware analysis (AST parsing where supported)
		entities, edges, err := e.analyzer.AnalyzeFile(f)
		if err == nil {
			for _, ent := range entities {
				graph.AddEntity(ent)
			}
			for _, edge := range edges {
				graph.AddEdge(edge.From, edge.To, edge.Kind, edge.FilePath)
			}
		}

		// Layer 2: Document chunking (every file, regardless of language)
		chunks, err := e.chunker.ChunkFile(f)
		if err == nil {
			for _, chunk := range chunks {
				graph.AddChunk(chunk)
			}
		}
	}

	// Detect primary language
	graph.Language = e.analyzer.DetectPrimaryLanguage(files)
	graph.AnalyzedAt = time.Now()

	e.graph = graph
	return graph, nil
}

// Graph returns the current code graph, or nil if Analyze hasn't been called.
func (e *Engine) Graph() *CodeGraph {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.graph
}

// Embed computes enriched embeddings for all entities and chunks in the graph.
// Requires Analyze to have been called first.
func (e *Engine) Embed(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.graph == nil {
		return fmt.Errorf("no graph — call Analyze first")
	}

	return e.embedder.EmbedGraph(ctx, e.graph)
}

// Search queries both the entity graph and document chunks.
// Returns ranked results from both layers, fused together.
func (e *Engine) Search(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.graph == nil {
		return nil, fmt.Errorf("no graph — call Analyze first")
	}

	if topK <= 0 {
		topK = 10
	}

	return e.embedder.Search(ctx, e.graph, query, topK)
}

// GenerateWiki produces a full wiki from the analyzed repository.
// Requires Analyze and Embed to have been called first.
// Requires an LLM to be configured.
func (e *Engine) GenerateWiki(ctx context.Context, req GenerateRequest) (*WikiCache, error) {
	e.mu.RLock()
	graph := e.graph
	llm := e.llm
	e.mu.RUnlock()

	if graph == nil {
		return nil, fmt.Errorf("no graph — call Analyze first")
	}
	if llm == nil {
		return nil, fmt.Errorf("no LLM configured — use WithProvider or WithLLM")
	}

	gen := NewGeneratorWithFallback(llm, e.fallbackLLM, e.embedder)
	wiki, err := gen.GenerateV2(ctx, graph, req)
	if err != nil {
		return nil, err
	}

	// Persist to cache
	if err := e.cache.Save(wiki); err != nil {
		return wiki, fmt.Errorf("generated but failed to cache: %w", err)
	}

	return wiki, nil
}

// Research runs a multi-turn deep research session on a topic.
// Requires Analyze and Embed to have been called first.
func (e *Engine) Research(ctx context.Context, req ResearchRequest) (*ResearchResult, error) {
	e.mu.RLock()
	graph := e.graph
	llm := e.llm
	e.mu.RUnlock()

	if graph == nil {
		return nil, fmt.Errorf("no graph — call Analyze first")
	}
	if llm == nil {
		return nil, fmt.Errorf("no LLM configured — use WithProvider or WithLLM")
	}

	gen := NewGeneratorWithFallback(llm, e.fallbackLLM, e.embedder)
	return gen.Research(ctx, graph, req)
}

// LoadCachedWiki loads a previously generated wiki from the cache.
func (e *Engine) LoadCachedWiki() (*WikiCache, error) {
	return e.cache.Load()
}

// SaveWiki persists a wiki to the cache. Used to re-save after updating
// the smart cache state.
func (e *Engine) SaveWiki(wiki *WikiCache) error {
	return e.cache.Save(wiki)
}

// Stats returns summary statistics about the current analysis.
func (e *Engine) Stats() *CodeGraphStats {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.graph == nil {
		return nil
	}

	counts := make(map[string]int)
	for _, ent := range e.graph.Entities {
		counts[string(ent.Kind)]++
	}

	return &CodeGraphStats{
		TotalFiles:      e.graph.TotalFiles,
		TotalEntities:   e.graph.TotalEntities,
		TotalEdges:      e.graph.TotalEdges,
		TotalChunks:     e.graph.TotalChunks,
		PrimaryLanguage: e.graph.Language,
		EntityCounts:    counts,
	}
}

// SearchResult is a ranked result from a Search query.
type SearchResult struct {
	// One of these will be set
	Entity *CodeEntity    `json:"entity,omitempty"`
	Chunk  *DocumentChunk `json:"chunk,omitempty"`

	Score  float64 `json:"score"`
	Source string  `json:"source"` // "entity", "chunk", "keyword"
}

// Default directories to skip during analysis.
// These are universal — they apply to ANY repository being analyzed.
// Do NOT add monorepo-specific names here; those belong in the caller's
// WithExcludeDirs option.
var defaultExcludeDirs = []string{
	// VCS
	".git", ".svn", ".hg",
	// Package managers / vendor / dependency trees
	"node_modules", "vendor", "third_party", "bower_components",
	".venv", "venv", "__pycache__", ".pnpm", ".yarn",
	// Build output & generated artefacts
	"dist", "build", "out", "target", "bin", "obj", "generated",
	"coverage", "storybook-static",
	// Caches & tooling state
	".idea", ".vscode", ".swarm", ".cache", ".worktrees",
	".turbo", ".gradle", ".mvn", ".parcel-cache", ".rollup.cache",
	"__mocks__",
	// Frontend framework output
	".next", ".nuxt", ".svelte-kit", ".output",
	// Test fixtures / generated data
	"testdata", "fixtures",
}

// ---------------------------------------------------------------------------
// PageCallback-based bulk generation
// ---------------------------------------------------------------------------

// GenerateWikiWithCallback produces a wiki using the V2 pipeline, calling onPage after each page is generated.
// This lets the bridge stream pages to the UI as they arrive.
func (e *Engine) GenerateWikiWithCallback(ctx context.Context, req GenerateRequest, onPage PageCallback) (*WikiCache, error) {
	e.mu.RLock()
	graph := e.graph
	llm := e.llm
	e.mu.RUnlock()

	if graph == nil {
		return nil, fmt.Errorf("no graph — call Analyze first")
	}
	if llm == nil {
		return nil, fmt.Errorf("no LLM configured — use WithProvider or WithLLM")
	}

	gen := NewGeneratorWithFallback(llm, e.fallbackLLM, e.embedder)
	wiki, err := gen.GenerateWithCallbackV2(ctx, graph, req, onPage)
	if err != nil {
		return wiki, err
	}

	// Persist to engine's built-in cache alongside the repo
	if err := e.cache.Save(wiki); err != nil {
		return wiki, fmt.Errorf("generated but failed to cache: %w", err)
	}

	return wiki, nil
}

// ---------------------------------------------------------------------------
// Single-page regeneration
// ---------------------------------------------------------------------------

// RegeneratePageRequest describes a single-page regeneration task.
type RegeneratePageRequest struct {
	PageTitle       string   // The title of the page to regenerate
	ExistingContent string   // Current page content (may be empty for new pages)
	Instructions    string   // User instructions (e.g. "Add a section about Docker")
	FocusEntities   []string // Optional: entity names to focus on
	FocusFiles      []string // Optional: file paths to focus on
	Language        string   // Language for the output (default: "en")
}

// RegeneratePage rewrites a single wiki page given user instructions and existing content.
// Requires Analyze to have been called first.
func (e *Engine) RegeneratePage(ctx context.Context, req RegeneratePageRequest) (string, error) {
	e.mu.RLock()
	graph := e.graph
	llm := e.llm
	e.mu.RUnlock()

	if graph == nil {
		return "", fmt.Errorf("no graph — call Analyze first")
	}
	if llm == nil {
		return "", fmt.Errorf("no LLM configured — use WithProvider or WithLLM")
	}

	// Gather entity context from the graph
	var entityParts []string
	for _, qname := range req.FocusEntities {
		if ent, ok := graph.Entities[qname]; ok {
			var sb strings.Builder
			fmt.Fprintf(&sb, "### %s %s\n", ent.Kind, ent.QualifiedName)
			if ent.DocComment != "" {
				fmt.Fprintf(&sb, "Doc: %s\n", ent.DocComment)
			}
			if ent.Signature != "" {
				fmt.Fprintf(&sb, "```\n%s\n```\n", ent.Signature)
			}
			body := ent.Body
			if len(body) > 3000 {
				body = body[:3000] + "\n// ... truncated"
			}
			fmt.Fprintf(&sb, "\nSource:\n```\n%s\n```\n", body)
			entityParts = append(entityParts, sb.String())
		}
	}
	// Also gather entities from focus files
	for _, fp := range req.FocusFiles {
		if names, ok := graph.FileEntities[fp]; ok {
			for _, qname := range names {
				if ent, ok := graph.Entities[qname]; ok && ent.Kind != KindFile {
					var sb strings.Builder
					fmt.Fprintf(&sb, "### %s %s\n", ent.Kind, ent.Name)
					if ent.Signature != "" {
						fmt.Fprintf(&sb, "```\n%s\n```\n", ent.Signature)
					}
					entityParts = append(entityParts, sb.String())
				}
			}
		}
	}

	// Semantic search for relevant chunks using the page title + instructions
	gen := NewGeneratorWithFallback(llm, e.fallbackLLM, e.embedder)
	var chunkParts []string
	searchQuery := req.PageTitle
	if req.Instructions != "" {
		searchQuery = req.Instructions + " " + req.PageTitle
	}
	results, err := gen.embedder.Search(ctx, graph, searchQuery, 8)
	if err == nil {
		for _, r := range results {
			if r.Chunk != nil {
				chunkParts = append(chunkParts, fmt.Sprintf("// %s (lines %d-%d)\n%s",
					r.Chunk.FilePath, r.Chunk.StartLine, r.Chunk.EndLine, r.Chunk.Content))
			}
		}
	}

	entityContext := strings.Join(entityParts, "\n---\n")
	chunkContext := strings.Join(chunkParts, "\n---\n")
	if entityContext == "" && chunkContext == "" {
		entityContext = "(no matching entities found)"
	}

	system, user := promptRegeneratePage(
		graph.RepoName, req.PageTitle,
		req.ExistingContent, entityContext, chunkContext,
		req.Instructions, req.Language,
	)

	return gen.generateWithFallback(ctx, system, user)
}

// ---------------------------------------------------------------------------
// V2 pipeline entry points on Engine
// ---------------------------------------------------------------------------

// GenerateWikiV2 produces a wiki using the multi-planner council and 4-pass
// chunked page builder.
//
// Usage:
//
//	eng, _ := deepwiki.NewEngine("/path/to/repo", deepwiki.WithProvider("cerebras", "llama-3.3-70b"))
//	graph, _ := eng.Analyze(ctx)
//	wiki, _ := eng.GenerateWikiV2(ctx, deepwiki.GenerateRequest{MaxPages: 20})
//
// Requires Analyze and Embed to have been called first.
// Requires an LLM to be configured via WithProvider or WithLLM.
func (e *Engine) GenerateWikiV2(ctx context.Context, req GenerateRequest) (*WikiCache, error) {
	e.mu.RLock()
	graph := e.graph
	llm := e.llm
	e.mu.RUnlock()

	if graph == nil {
		return nil, fmt.Errorf("no graph — call Analyze first")
	}
	if llm == nil {
		return nil, fmt.Errorf("no LLM configured — use WithProvider or WithLLM")
	}

	gen := NewGeneratorWithFallback(llm, e.fallbackLLM, e.embedder)
	wiki, err := gen.GenerateV2(ctx, graph, req)
	if err != nil {
		return nil, err
	}

	if err := e.cache.Save(wiki); err != nil {
		return wiki, fmt.Errorf("generated but failed to cache: %w", err)
	}
	return wiki, nil
}

// GenerateWikiWithCallbackV2 is like GenerateWikiV2 but streams pages to the
// caller via onPage as they are generated. Use this for progressive UI updates.
func (e *Engine) GenerateWikiWithCallbackV2(ctx context.Context, req GenerateRequest, onPage PageCallback) (*WikiCache, error) {
	e.mu.RLock()
	graph := e.graph
	llm := e.llm
	e.mu.RUnlock()

	if graph == nil {
		return nil, fmt.Errorf("no graph — call Analyze first")
	}
	if llm == nil {
		return nil, fmt.Errorf("no LLM configured — use WithProvider or WithLLM")
	}

	gen := NewGeneratorWithFallback(llm, e.fallbackLLM, e.embedder)
	wiki, err := gen.GenerateWithCallbackV2(ctx, graph, req, onPage)
	if err != nil {
		return wiki, err
	}

	if err := e.cache.Save(wiki); err != nil {
		return wiki, fmt.Errorf("generated but failed to cache: %w", err)
	}
	return wiki, nil
}
