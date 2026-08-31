// Package deepwiki — CompilerAgent
//
// CompilerAgent orchestrates the full wiki generation pipeline:
//
//  1. Fans out EmbedderAgent and PipelineAgent concurrently.
//  2. Waits for both to complete and merges their results into a unified
//     CodeGraph.
//  3. Runs the LLM generation loop (plan → per-page generation), emitting
//     CompilerProgress events on a channel that the caller (e.g. HTTP bridge)
//     can consume for real-time SSE streaming.
//
// CompilerAgent does not persist anything. Persistence is the caller's job.
package deepwiki

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Configurable settings from environment
// ---------------------------------------------------------------------------

// maxRetries controls the number of retry attempts for LLM calls.
// Override with DEEPWIKI_MAX_RETRIES env var (default: 20 for aggressive retry).
var maxRetries = func() int {
	if s := os.Getenv("DEEPWIKI_MAX_RETRIES"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return 20 // Default: 20 aggressive retries
}()

// retryBaseWait is the initial back-off duration for retries.
// Override with DEEPWIKI_RETRY_BASE_WAIT_MS env var (default: 500ms).
var retryBaseWait = func() time.Duration {
	if s := os.Getenv("DEEPWIKI_RETRY_BASE_WAIT_MS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Millisecond
		}
	}
	return 500 * time.Millisecond // Default: 500ms base wait
}()

// ---------------------------------------------------------------------------
// Progress events
// ---------------------------------------------------------------------------

// CompilerPhase enumerates the phases a CompilerAgent goes through.
type CompilerPhase string

const (
	PhaseInit       CompilerPhase = "init"
	PhaseGraphCache CompilerPhase = "graph_cache" // loading/saving the disk-persisted CodeGraph
	PhaseEmbedding  CompilerPhase = "embedding"
	PhasePipeline   CompilerPhase = "pipeline"
	PhaseMerging    CompilerPhase = "merging"
	PhasePlanning   CompilerPhase = "planning"
	PhaseGenerating CompilerPhase = "generating"
	PhaseDone       CompilerPhase = "done"
	PhaseError      CompilerPhase = "error"
)

// CompilerProgress is a progress event emitted by CompilerAgent on its channel.
type CompilerProgress struct {
	Phase   CompilerPhase `json:"phase"`
	Step    string        `json:"step"`
	Current int           `json:"current,omitempty"` // page index (during generating)
	Total   int           `json:"total,omitempty"`   // total pages (during generating)
	Pct     int           `json:"pct"`               // 0–100

	// Set only on PhaseGenerating — the page that was just completed.
	Page *WikiPage `json:"page,omitempty"`

	// Set only on PhaseError — human-readable error message.
	Error string `json:"error,omitempty"`
	// IsDetailLog marks low-level per-file events (embedding/AST per file).
	// The UI routes these to a separate log drawer instead of the main progress bar.
	IsDetailLog bool `json:"is_detail_log,omitempty"`
	// Plan is set on the PhasePlanning event that fires once the wiki structure
	// is finalised. The server uses it to send a wiki_structure SSE event so
	// the UI sidebar can show all planned pages before content is generated.
	Plan *FinalPlan `json:"plan,omitempty"`
}

// ---------------------------------------------------------------------------
// CompilerAgent
// ---------------------------------------------------------------------------

// CompilerAgent orchestrates EmbedderAgent + PipelineAgent and drives the LLM
// wiki generation. It is the single entry point for both full-wiki generation
// and single-page regeneration.
type CompilerAgent struct {
	llm         LLMClient
	fallbackLLM LLMClient // optional; activated when primary exhausts compaction levels
	embedder    *EmbedderAgent
	pipeline    *PipelineAgent
	repair      *RepairAgent // post-generation markdown repair; uses fastLLM
	wikiID      string       // for request logging
}

// CompilerAgentOption configures a CompilerAgent.
type CompilerAgentOption func(*CompilerAgent)

// WithCompilerExcludeDirs adds directories to skip in both sub-agents.
// The call is additive: it appends to the sub-agents' existing exclude lists
// (which start with defaultExcludeDirs) rather than replacing them, so the
// universal defaults (node_modules, vendor, dist, …) are always honoured.
func WithCompilerExcludeDirs(dirs ...string) CompilerAgentOption {
	return func(a *CompilerAgent) {
		a.embedder.excludeDirs = append(a.embedder.excludeDirs, dirs...)
		a.pipeline.excludeDirs = append(a.pipeline.excludeDirs, dirs...)
	}
}

// WithCompilerFocusDirs limits both sub-agents to specific directories.
func WithCompilerFocusDirs(dirs ...string) CompilerAgentOption {
	return func(a *CompilerAgent) {
		a.embedder.focusDirs = dirs
		a.pipeline.focusDirs = dirs
	}
}

// WithCompilerFallbackLLM sets the LLM client used when the primary fails all
// compaction levels.  Pass nil to disable the fallback explicitly.
func WithCompilerFallbackLLM(llm LLMClient) CompilerAgentOption {
	return func(a *CompilerAgent) {
		a.fallbackLLM = llm
	}
}

// WithCompilerWikiID sets the wiki ID for request logging.
func WithCompilerWikiID(wikiID string) CompilerAgentOption {
	return func(a *CompilerAgent) {
		a.wikiID = wikiID
	}
}

// NewCompilerAgent creates a CompilerAgent with the given LLM client.
// EmbedderAgent and PipelineAgent are constructed with sensible defaults and
// share the same exclude/focus directories.
//
// A fallback LLM is auto-detected from the environment (GEMINI_API_KEY /
// GOOGLE_API_KEY → Gemini 2.5 Flash; OPENAI_API_KEY → gpt-4o-mini).
// Use WithFallbackLLM(nil) to disable auto-detection.
func NewCompilerAgent(llm LLMClient, opts ...CompilerAgentOption) *CompilerAgent {
	a := &CompilerAgent{
		llm:         llm,
		fallbackLLM: NewDefaultFallbackLLM(), // auto-detect; nil if no creds found
		embedder:    NewEmbedderAgent(),
		pipeline:    NewPipelineAgent(),
	}
	for _, o := range opts {
		o(a)
	}
	// Repair agent uses the fast/fallback model; falls back to primary if unavailable.
	repairLLM := a.fallbackLLM
	if repairLLM == nil {
		repairLLM = a.llm
	}
	a.repair = NewRepairAgent(repairLLM)
	return a
}

// ---------------------------------------------------------------------------
// GenerateWiki — full wiki from scratch
// ---------------------------------------------------------------------------

// GenerateWikiRequest configures a full-wiki generation run via CompilerAgent.
type GenerateWikiRequest struct {
	RepoPath    string
	Provider    string
	Model       string
	Language    string
	MaxPages    int
	SkipPageIDs []string // pages already generated — skip during resume

	// PipelineVersion is ignored — V2 is the only supported pipeline.
	// Kept for backward compatibility with existing configs.
	PipelineVersion int

	// FastLLM overrides the "fast" model used for cheap, latency-sensitive passes
	// (skeleton, reflector, merge, planner council).  If nil, the compiler's
	// auto-detected fallbackLLM is used.
	FastLLM LLMClient

	// RateLimitLLM is used as the immediate fallback when the primary LLM returns
	// a rate-limit error (HTTP 429).  If nil, FastLLM (or fallbackLLM) is used.
	RateLimitLLM LLMClient

	// Workers overrides the global DEEPWIKI_WORKERS concurrency limit for this
	// run only.  0 means "use the global default".
	Workers int
}

// GenerateWiki fans out the two independent agents concurrently, merges their
// results, then runs the LLM wiki-generation loop.
//
// progressCh receives CompilerProgress events in real time.
// The channel is closed when generation finishes (successfully or with error).
// Pass a buffered channel to avoid blocking the agent.
func (a *CompilerAgent) GenerateWiki(
	ctx context.Context,
	req GenerateWikiRequest,
	progressCh chan<- CompilerProgress,
) (*WikiCache, error) {
	defer close(progressCh)

	send := func(p CompilerProgress) {
		select {
		case progressCh <- p:
		default:
		}
	}

	send(CompilerProgress{Phase: PhaseInit, Step: "Starting...", Pct: 0})

	// -----------------------------------------------------------------------
	// Graph cache: try to load a previously built CodeGraph from disk.
	// If the repo fingerprint matches (no files added/removed/modified since
	// last run), we skip both agents and jump straight to generation.
	// -----------------------------------------------------------------------
	var graph *CodeGraph

	store, storeErr := NewGraphStore(req.RepoPath)
	if storeErr == nil && store.IsFresh(req.RepoPath, a.pipeline.excludeDirs) {
		send(CompilerProgress{
			Phase: PhaseGraphCache,
			Step:  "Loading cached graph from " + store.Dir() + "...",
			Pct:   5,
		})
		graph, storeErr = store.LoadGraph()
		if storeErr != nil || graph == nil {
			// Corrupt or missing — fall through to re-analysis
			graph = nil
		} else {
			send(CompilerProgress{
				Phase: PhaseGraphCache,
				Step: fmt.Sprintf("Cache hit: %d entities, %d chunks — skipping analysis",
					graph.TotalEntities, graph.TotalChunks),
				Pct: 25,
			})
		}
	}

	// -----------------------------------------------------------------------
	// Fan-out: run EmbedderAgent and PipelineAgent concurrently.
	// Only executed on cache miss or stale cache.
	// -----------------------------------------------------------------------
	if graph == nil {
		send(CompilerProgress{Phase: PhaseInit, Step: "Starting embedding and pipeline agents...", Pct: 0})

		var (
			embedResult    *EmbedderResult
			pipelineResult *PipelineResult
			embedErr       error
			pipelineErr    error
			wg             sync.WaitGroup
		)

		wg.Add(2)

		go func() {
			defer wg.Done()
			embedResult, embedErr = a.embedder.Run(ctx, req.RepoPath, func(done, total int, file string) {
				pct := int(float64(done) / float64(total) * 20)
				send(CompilerProgress{
					Phase:       PhaseEmbedding,
					Step:        fmt.Sprintf("Embedding [%d/%d] %s", done, total, shortPath(file, req.RepoPath)),
					Pct:         pct,
					IsDetailLog: true,
				})
			})
		}()

		go func() {
			defer wg.Done()
			pipelineResult, pipelineErr = a.pipeline.Run(ctx, req.RepoPath, func(done, total int, file string) {
				pct := int(float64(done) / float64(total) * 20)
				send(CompilerProgress{
					Phase:       PhasePipeline,
					Step:        fmt.Sprintf("Parsing AST [%d/%d] %s", done, total, shortPath(file, req.RepoPath)),
					Pct:         pct,
					IsDetailLog: true,
				})
			})
		}()

		wg.Wait()

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if embedErr != nil {
			send(CompilerProgress{Phase: PhaseError, Step: "Embedding agent failed", Error: embedErr.Error(), Pct: 20})
			return nil, fmt.Errorf("embedder agent: %w", embedErr)
		}
		if pipelineErr != nil {
			send(CompilerProgress{Phase: PhaseError, Step: "Pipeline agent failed", Error: pipelineErr.Error(), Pct: 20})
			return nil, fmt.Errorf("pipeline agent: %w", pipelineErr)
		}

		// -------------------------------------------------------------------
		// Merge: combine both results into one unified CodeGraph
		// -------------------------------------------------------------------
		send(CompilerProgress{Phase: PhaseMerging, Step: "Merging embedding and pipeline results...", Pct: 22})

		graph = a.merge(req.RepoPath, embedResult, pipelineResult)

		send(CompilerProgress{
			Phase: PhaseMerging,
			Step: fmt.Sprintf("Merged: %d entities, %d chunks (%s backend)",
				graph.TotalEntities, graph.TotalChunks, embedResult.BackendName),
			Pct: 25,
		})

		// -------------------------------------------------------------------
		// Persist the graph to disk so next run can skip re-analysis.
		// Then nil out the raw agent results — the merged graph is all we
		// need from here, and freeing these allows GC to reclaim the memory.
		// -------------------------------------------------------------------
		if store != nil {
			send(CompilerProgress{
				Phase: PhaseGraphCache,
				Step:  fmt.Sprintf("Saving graph to %s...", store.Dir()),
				Pct:   26,
			})
			if saveErr := store.SaveGraph(graph, embedResult.BackendName); saveErr != nil {
				// Non-fatal: log and continue
				send(CompilerProgress{
					Phase: PhaseGraphCache,
					Step:  "Warning: could not save graph cache: " + saveErr.Error(),
					Pct:   26,
				})
			}
		}

		// Free agent result memory now that the merged graph is the only artifact
		// we need. Reading mem-stats before and after lets us emit a diagnostic
		// progress event that confirms the GC actually reclaimed the heap.
		var memBefore runtime.MemStats
		runtime.ReadMemStats(&memBefore)

		embedResult = nil
		pipelineResult = nil
		runtime.GC() // force collection so the LLM loop starts with a clean heap

		var memAfter runtime.MemStats
		runtime.ReadMemStats(&memAfter)
		reclaimed := max(int64(memBefore.HeapInuse)-int64(memAfter.HeapInuse), 0)
		send(CompilerProgress{
			Phase: PhaseGraphCache,
			Step: fmt.Sprintf("GC: heap %.1f MB \u2192 %.1f MB (reclaimed %.1f MB)",
				float64(memBefore.HeapInuse)/1e6,
				float64(memAfter.HeapInuse)/1e6,
				float64(reclaimed)/1e6),
			Pct: 27,
		})
	}
	// -----------------------------------------------------------------------
	// Generate: plan + per-page LLM loop
	// -----------------------------------------------------------------------
	lang := req.Language
	if lang == "" {
		lang = "en"
	}

	send(CompilerProgress{Phase: PhasePlanning, Step: "Planning wiki structure...", Pct: 28})

	embedderForGen := NewEmbedder()
	// Reuse the already-embedded chunks from the merge — give the generator
	// the same graph (which already has Embedding vectors) and a new Embedder
	// instance just for query embedding during generation.

	// ── LLM selection ──────────────────────────────────────────────────────
	// fast LLM: skeleton, reflector, merge — prefer user override, then
	// auto-detected fallbackLLM, then primary.
	var fastLLM LLMClient = a.fallbackLLM
	if req.FastLLM != nil {
		fastLLM = req.FastLLM
	}
	if fastLLM == nil {
		fastLLM = a.llm
	}

	// rate-limit fallback: used by RetryLLM when primary returns 429.
	// Prefer explicit RateLimitLLM, then fall back to fastLLM.
	var rateLimitFallback LLMClient = fastLLM
	if req.RateLimitLLM != nil {
		rateLimitFallback = req.RateLimitLLM
	}

	// Wrap primary with rate-limit-aware retry (configurable attempts via env).
	primaryWithRetry := NewRetryLLMWithFallback(a.llm, rateLimitFallback, maxRetries, retryBaseWait)
	gen := NewGeneratorWithFallback(primaryWithRetry, fastLLM, embedderForGen)

	// Set up request logging if wikiID is provided
	if a.wikiID != "" {
		gen.SetLogger(NewRequestLogger(a.wikiID))
	}
	// V2 pipeline: multi-planner council + 4-pass chunked page builder
	// This is the only supported pipeline.
	// -----------------------------------------------------------------------
	send(CompilerProgress{Phase: PhasePlanning, Step: "Launching planner council (code-first + semantic)...", Pct: 28})

	sdkReq := GenerateRequest{
		RepoPath: req.RepoPath,
		Provider: req.Provider,
		Model:    req.Model,
		Language: lang,
		MaxPages: req.MaxPages,
		Workers:  req.Workers,
	}
	sdkReq.OnPlanReady = func(p *FinalPlan) {
		send(CompilerProgress{
			Phase: PhasePlanning,
			Step:  "Wiki structure ready — populating sidebar",
			Pct:   30,
			Plan:  p,
		})
	}

	totalV2 := 0
	currentV2 := 0

	wiki, err := gen.GenerateWithCallbackV2(ctx, graph, sdkReq, func(page *WikiPage, current, total int) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		totalV2 = total
		currentV2 = current
		pct := 30 + int(float64(currentV2)/float64(totalV2+1)*65)
		send(CompilerProgress{
			Phase:   PhaseGenerating,
			Step:    fmt.Sprintf("[%d/%d] %s", current, total, page.Title),
			Current: current,
			Total:   total,
			Pct:     pct,
			Page:    page,
		})
		return nil
	})

	if err != nil {
		send(CompilerProgress{Phase: PhaseError, Step: "Generation failed", Error: err.Error(), Pct: 95})
		return wiki, fmt.Errorf("generate v2: %w", err)
	}

	send(CompilerProgress{Phase: PhaseDone, Step: "Done!", Pct: 100})
	return wiki, nil
}

// ---------------------------------------------------------------------------
// RegeneratePage — single-page update
// ---------------------------------------------------------------------------

// RegeneratePageRequest configures a single-page regeneration via CompilerAgent.
type CompilerRegenerateRequest struct {
	RepoPath        string
	PageTitle       string
	ExistingContent string
	Instructions    string
	FocusEntities   []string
	FocusFiles      []string
	Language        string
}

// RegeneratePage runs both agents for the targeted files only (or the full repo
// if FocusFiles is empty), then asks the LLM to rewrite the page.
//
// progressCh receives CompilerProgress events. The channel is closed on return.
func (a *CompilerAgent) RegeneratePage(
	ctx context.Context,
	req CompilerRegenerateRequest,
	progressCh chan<- CompilerProgress,
) (string, error) {
	defer close(progressCh)

	send := func(p CompilerProgress) {
		select {
		case progressCh <- p:
		default:
		}
	}

	send(CompilerProgress{Phase: PhaseInit, Step: "Starting agents for page regeneration...", Pct: 0})

	// For single-page regeneration, scope both agents to FocusFiles when provided.
	embedAgent := a.embedder
	pipeAgent := a.pipeline

	if len(req.FocusFiles) > 0 {
		embedAgent = NewEmbedderAgent(
			WithEmbedderExcludeDirs(a.embedder.excludeDirs...),
			WithEmbedderFocusDirs(req.FocusFiles...),
		)
		pipeAgent = NewPipelineAgent(
			WithPipelineExcludeDirs(a.pipeline.excludeDirs...),
			WithPipelineFocusDirs(req.FocusFiles...),
		)
	}

	// Fan-out
	var (
		embedResult    *EmbedderResult
		pipelineResult *PipelineResult
		embedErr       error
		pipelineErr    error
		wg             sync.WaitGroup
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		embedResult, embedErr = embedAgent.Run(ctx, req.RepoPath, func(done, total int, _ string) {
			send(CompilerProgress{Phase: PhaseEmbedding, Step: fmt.Sprintf("Embedding [%d/%d]", done, total), Pct: done * 15 / max(total, 1)})
		})
	}()
	go func() {
		defer wg.Done()
		pipelineResult, pipelineErr = pipeAgent.Run(ctx, req.RepoPath, func(done, total int, _ string) {
			send(CompilerProgress{Phase: PhasePipeline, Step: fmt.Sprintf("Parsing [%d/%d]", done, total), Pct: done * 15 / max(total, 1)})
		})
	}()
	wg.Wait()

	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if embedErr != nil {
		return "", fmt.Errorf("embedder: %w", embedErr)
	}
	if pipelineErr != nil {
		return "", fmt.Errorf("pipeline: %w", pipelineErr)
	}

	send(CompilerProgress{Phase: PhaseMerging, Step: "Merging context...", Pct: 20})
	graph := a.merge(req.RepoPath, embedResult, pipelineResult)

	// Delegate to the engine's RegeneratePage logic by constructing an Engine
	// with the merged graph already set.
	send(CompilerProgress{Phase: PhaseGenerating, Step: fmt.Sprintf("Rewriting page: %s", req.PageTitle), Pct: 30})

	eng := &Engine{
		repoPath: req.RepoPath,
		repoName: graph.RepoName,
		graph:    graph,
		llm:      a.llm,
		embedder: NewEmbedder(),
	}

	content, err := eng.RegeneratePage(ctx, RegeneratePageRequest{
		PageTitle:       req.PageTitle,
		ExistingContent: req.ExistingContent,
		Instructions:    req.Instructions,
		FocusEntities:   req.FocusEntities,
		FocusFiles:      req.FocusFiles,
		Language:        req.Language,
	})
	if err != nil {
		send(CompilerProgress{Phase: PhaseError, Step: "Page rewrite failed", Error: err.Error(), Pct: 90})
		return "", err
	}

	send(CompilerProgress{Phase: PhaseDone, Step: "Page regenerated", Pct: 100})
	return content, nil
}

// ---------------------------------------------------------------------------
// merge — combine EmbedderResult + PipelineResult into a unified CodeGraph
// ---------------------------------------------------------------------------

// merge produces a single CodeGraph from the two independent agent results.
// EmbedderResult provides the chunk/vector layer.
// PipelineResult provides the entity/edge/package layer.
func (a *CompilerAgent) merge(repoPath string, er *EmbedderResult, pr *PipelineResult) *CodeGraph {
	repoName := repoPath
	if idx := strings.LastIndex(repoPath, "/"); idx >= 0 {
		repoName = repoPath[idx+1:]
	}

	g := NewCodeGraph(repoPath, repoName)

	// Populate from PipelineResult
	for _, ent := range pr.Entities {
		g.AddEntity(ent)
	}
	for _, edge := range pr.Edges {
		g.AddEdge(edge.From, edge.To, edge.Kind, edge.FilePath)
	}

	// Populate from EmbedderResult
	for _, chunk := range er.Chunks {
		g.AddChunk(chunk)
	}

	g.Language = pr.Language
	g.TotalFiles = max(er.TotalFiles, pr.TotalFiles)
	g.AnalyzedAt = pr.AnalyzedAt

	return g
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// shortPath trims the repo root prefix from a file path for display.
func shortPath(filePath, repoPath string) string {
	rel := strings.TrimPrefix(filePath, repoPath)
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return filePath
	}
	return rel
}
