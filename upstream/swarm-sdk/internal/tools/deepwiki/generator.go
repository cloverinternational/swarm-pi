package deepwiki

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Generator produces wiki pages and deep research from a CodeGraph + LLM.
type Generator struct {
	llm         LLMClient
	fallbackLLM LLMClient // optional; used when primary exhausts all compaction levels
	embedder    *Embedder
	logger      *RequestLogger // optional; logs requests for analysis
}

// ---------------------------------------------------------------------------
// Compaction strategy
// ---------------------------------------------------------------------------

// pageLLMTimeout is the per-attempt LLM call timeout.
// Each attempt gets its own independent deadline — it does NOT inherit the
// parent context's deadline so a tight caller deadline doesn't bleed through.
//
// Default: 10 minutes.  Override with DEEPWIKI_CALL_TIMEOUT_MINUTES env var.
// Set DEEPWIKI_CALL_TIMEOUT_MINUTES=0 to disable the per-call timeout entirely
// (useful when the provider can take arbitrarily long for large contexts).
var pageLLMTimeout = func() time.Duration {
	if s := os.Getenv("DEEPWIKI_CALL_TIMEOUT_MINUTES"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			if n <= 0 {
				return 0 // disabled — callers will use context.Background() deadline
			}
			return time.Duration(n) * time.Minute
		}
	}
	return 10 * time.Minute
}()

// withCallDeadline returns a context with pageLLMTimeout applied on top of
// context.WithoutCancel(ctx).  If pageLLMTimeout is 0 (disabled via env) the
// parent cancellation is still stripped but no deadline is added — the call
// runs until the provider responds or the HTTP client timeout fires.
func withCallDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(ctx)
	if pageLLMTimeout <= 0 {
		return context.WithCancel(base)
	}
	return context.WithTimeout(base, pageLLMTimeout)
}

// llmsWithFallback returns the LLM clients to try, primary first.
func (gen *Generator) llmsWithFallback() []LLMClient {
	if gen.fallbackLLM != nil {
		return []LLMClient{gen.llm, gen.fallbackLLM}
	}
	return []LLMClient{gen.llm}
}

// SetLogger sets the request logger for this generator.
func (gen *Generator) SetLogger(logger *RequestLogger) {
	gen.logger = logger
}

// NewGenerator creates a wiki generator.
func NewGenerator(llm LLMClient, embedder *Embedder) *Generator {
	return &Generator{llm: llm, embedder: embedder}
}

// NewGeneratorWithFallback creates a generator that falls back to fallbackLLM
// when the primary LLM times out or exhausts its context after compaction.
func NewGeneratorWithFallback(llm, fallbackLLM LLMClient, embedder *Embedder) *Generator {
	return &Generator{llm: llm, fallbackLLM: fallbackLLM, embedder: embedder}
}

// --- Wiki Generation ---

// generateWithFallback tries the primary LLM, then the fallback if set.
// Each attempt gets its own independent timeout so the parent deadline
// does not bleed through.  Explicit cancellation (context.Canceled) halts
// the retry chain immediately.
func (gen *Generator) generateWithFallback(ctx context.Context, system, user string) (string, error) {
	clients := gen.llmsWithFallback()
	var lastErr error
	for i, client := range clients {
		if ctx.Err() == context.Canceled {
			return "", context.Canceled
		}
		log.Printf("[deepwiki/gen] generateWithFallback: client=%d prompt=%d chars", i, len(system)+len(user))
		t0 := time.Now()
		attemptCtx, cancel := withCallDeadline(ctx)
		out, err := client.Generate(attemptCtx, system, user)
		duration := time.Since(t0)
		cancel()

		// Log the request if logger is set
		if gen.logger != nil {
			errMsg := ""
			if err != nil {
				errMsg = err.Error()
			}
			gen.logger.Log(&RequestLog{
				Timestamp: t0,
				Phase:     "generate",
				Stage:     "fallback",
				Provider:  client.Provider(),
				Model:     client.ModelName(),
				Request: RequestPayload{
					System: system,
					User:   user,
				},
				Response: ResponsePayload{
					Raw: out,
				},
				Timing: TimingInfo{
					Start:    t0,
					End:      time.Now(),
					Duration: duration,
				},
				Metadata: map[string]any{
					"attempt":      i,
					"error":        errMsg,
					"response_len": len(out),
				},
			})
		}

		if err == nil {
			log.Printf("[deepwiki/gen] generateWithFallback: client=%d done in %s response=%d chars", i, duration.Round(time.Millisecond), len(out))
			return out, nil
		}
		log.Printf("[deepwiki/gen] generateWithFallback: client=%d FAILED in %s: %v", i, duration.Round(time.Millisecond), err)
		lastErr = err
		if ctx.Err() == context.Canceled {
			return "", context.Canceled
		}
	}
	return "", lastErr
}

// PageCallback is called each time a page is generated.
// Returning an error aborts the generation.
type PageCallback func(page *WikiPage, current, total int) error

// ---------------------------------------------------------------------------
// V2 pipeline — multi-planner council + 4-pass chunked page generation
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// V2 pipeline — multi-planner council + 4-pass chunked page generation
// ---------------------------------------------------------------------------

// GenerateV2 produces a wiki using the new pipeline:
//   - Stage 3: PlannerCouncil (Planner A + B in parallel, then Synthesis)
//   - Stage 4: ChunkedPageBuilder (4 structured passes per page)
//
// Model routing follows "right model per step":
//
//	fastLLM   = gen.fallbackLLM (cheaper) — skeleton, reflector, Planners A+B
//	mediumLLM = gen.llm (primary)         — sections, synthesis, Synthesis Planner
func (gen *Generator) GenerateV2(ctx context.Context, graph *CodeGraph, req GenerateRequest) (*WikiCache, error) {
	return gen.generateV2Internal(ctx, graph, req, nil)
}

// GenerateWithCallbackV2 is like GenerateV2 but streams each page to onPage as it completes.
func (gen *Generator) GenerateWithCallbackV2(ctx context.Context, graph *CodeGraph, req GenerateRequest, onPage PageCallback) (*WikiCache, error) {
	return gen.generateV2Internal(ctx, graph, req, onPage)
}

func (gen *Generator) generateV2Internal(ctx context.Context, graph *CodeGraph, req GenerateRequest, onPage PageCallback) (*WikiCache, error) {
	lang := req.Language
	if lang == "" {
		lang = "English"
	}
	maxPages := req.MaxPages
	if maxPages <= 0 {
		maxPages = max(graph.TotalFiles/5, 20)
	}

	fastLLM := gen.fallbackLLM
	if fastLLM == nil {
		fastLLM = gen.llm
	}
	mediumLLM := gen.llm

	council := NewPlannerCouncil(fastLLM, mediumLLM, gen.embedder, lang, maxPages)
	plan, err := council.Run(ctx, graph)
	if err != nil {
		return nil, fmt.Errorf("planner council: %w", err)
	}
	// Notify caller that the plan is ready so the UI can populate the sidebar
	// before any page content is generated.
	if req.OnPlanReady != nil {
		req.OnPlanReady(plan)
	}

	// Flatten all pages from all sections, preserving section association.
	type pageItem struct {
		plan      *FinalPage
		sectionID string
	}
	var items []pageItem
	for si := range plan.Sections {
		for pi := range plan.Sections[si].Pages {
			items = append(items, pageItem{
				plan:      &plan.Sections[si].Pages[pi],
				sectionID: plan.Sections[si].ID,
			})
		}
	}
	total := len(items)

	builder := NewChunkedPageBuilder(fastLLM, mediumLLM, gen.embedder, graph, lang)
	builder.SetLogger(gen.logger)

	// Build a synthetic ACE delta from the plan before any page is generated.
	// Every page's Pass 1 (Skeleton) sees the full intended wiki coverage
	// upfront — better than the old sequential approach where page N only
	// saw summaries from pages 1..N-1.
	syntheticACE := make([]PageSummary, 0, total)
	for _, item := range items {
		syntheticACE = append(syntheticACE, PageSummary{
			PageID:  item.plan.ID,
			Title:   item.plan.Title,
			Bullets: item.plan.RequiredSections,
			Files:   item.plan.FocusFiles,
		})
	}
	builder.pageSummaries = syntheticACE

	// Pre-compute inDegree once — read-only, shared safely across all goroutines.
	inDegree := make(map[string]int, len(graph.Entities))
	for _, edge := range graph.Edges {
		if edge.Kind == EdgeCalls || edge.Kind == EdgeReferences {
			inDegree[edge.To]++
		}
	}
	relRef := func(filePath string, start, end int) string {
		rel := graphRelPath(graph.RepoPath, filePath)
		if rel == "" {
			rel = filePath
		}
		if start > 0 && end > 0 {
			return fmt.Sprintf("%s:L%d-%d", rel, start, end)
		}
		return rel
	}

	// Bounded worker pool — hard limit of 5 concurrent LLM calls across all stages.
	const workerLimit = 5
	sem := make(chan struct{}, workerLimit)

	// runParallel fires fn(i) for every i in [0,n) with at most workerLimit
	// goroutines active at once. Respects explicit cancellation.
	runParallel := func(n int, fn func(i int)) {
		var wg sync.WaitGroup
		for i := range n {
			if ctx.Err() == context.Canceled {
				break
			}
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				fn(idx)
			}(i)
		}
		wg.Wait()
	}

	// Per-stage result slices — index i corresponds to items[i].
	skeletons := make([][]SectionPlan, total)
	rawContents := make([]string, total)
	rawErrors := make([]error, total)
	synthContents := make([]string, total)
	finalContents := make([]string, total)

	// ── Stage A: Pass 1 — Skeleton (all pages in parallel) ──────────────────
	log.Printf("[deepwiki/v2] stage A: skeleton — %d pages, concurrency=%d", total, workerLimit)
	runParallel(total, func(i int) {
		item := items[i]
		sigCtx := builder.buildSignatureContext(item.plan, relRef)
		skel, skelErr := builder.passOneSkeleton(ctx, item.plan, sigCtx)
		if skelErr != nil {
			log.Printf("[deepwiki/v2] stage A: page[%q] skeleton fallback: %v", item.plan.Title, skelErr)
			skel = builder.fallbackSectionPlan(item.plan)
		}
		skeletons[i] = builder.verifySectionPlan(skel, item.plan)
	})
	if ctx.Err() == context.Canceled {
		return &WikiCache{GeneratedPages: make(map[string]WikiPage)}, context.Canceled
	}

	// ── Stage B: Pass 2 — Section writing (all pages in parallel) ───────────
	// Sections within each page are still sequential — the growing-page ACE
	// pattern is preserved. Only the outer page loop is parallelised.
	log.Printf("[deepwiki/v2] stage B: sections — %d pages, concurrency=%d", total, workerLimit)
	runParallel(total, func(i int) {
		item := items[i]
		content, sectErr := builder.passTwoSections(ctx, item.plan, skeletons[i], inDegree, relRef)
		rawContents[i] = content
		rawErrors[i] = sectErr
	})
	if ctx.Err() == context.Canceled {
		return &WikiCache{GeneratedPages: make(map[string]WikiPage)}, context.Canceled
	}

	// Replace synthetic ACE with real summaries extracted from all Stage B
	// outputs. Stage C (Synthesis) now sees every page's actual content when
	// building cross-references — richer than the old sequential approach.
	realSummaries := make([]PageSummary, total)
	for i, item := range items {
		realSummaries[i] = builderExtractSummary(item.plan, rawContents[i])
	}
	builder.pageSummaries = realSummaries

	// ── Stage C: Pass 3 — Synthesis (all pages in parallel) ─────────────────
	log.Printf("[deepwiki/v2] stage C: synthesis — %d pages, concurrency=%d", total, workerLimit)
	runParallel(total, func(i int) {
		if rawErrors[i] != nil {
			synthContents[i] = rawContents[i]
			return
		}
		item := items[i]
		draft, synthErr := builder.passThreeSynthesis(ctx, item.plan, rawContents[i])
		if synthErr != nil {
			log.Printf("[deepwiki/v2] stage C: page[%q] synthesis fallback: %v", item.plan.Title, synthErr)
			draft = rawContents[i]
		}
		synthContents[i] = draft
	})
	if ctx.Err() == context.Canceled {
		return &WikiCache{GeneratedPages: make(map[string]WikiPage)}, context.Canceled
	}

	// ── Stage D: Pass 4 — Reflector (all pages in parallel) ─────────────────
	log.Printf("[deepwiki/v2] stage D: reflector — %d pages, concurrency=%d", total, workerLimit)
	// Stage D fires onPage as each page completes — don't wait for all pages.
	// finalPages mirrors items[] and is populated inside runParallel so the
	// assembly loop below can read them without rebuilding the struct.
	finalPages := make([]WikiPage, total)
	var onPageMu sync.Mutex
	completedCount := 0
	runParallel(total, func(i int) {
		item := items[i]
		content := synthContents[i]
		if rawErrors[i] == nil {
			report, _ := builder.passFourReflector(ctx, item.plan, synthContents[i])
			if report != nil && !report.Valid && len(report.Issues) > 0 {
				content = builder.appendVerificationNotes(content, report)
			}
		}
		finalContents[i] = content

		var page WikiPage
		if rawErrors[i] != nil {
			page = WikiPage{
				ID:          item.plan.ID,
				Title:       item.plan.Title,
				Content:     fmt.Sprintf("*Error generating page: %v*", rawErrors[i]),
				Importance:  item.plan.Importance,
				SectionID:   item.sectionID,
				GeneratedAt: time.Now().Format(time.RFC3339),
			}
		} else {
			page = WikiPage{
				ID:           item.plan.ID,
				Title:        item.plan.Title,
				Content:      content,
				FilePaths:    item.plan.FocusFiles,
				Entities:     item.plan.FocusEntities,
				RelatedPages: item.plan.RelatedPages,
				Importance:   item.plan.Importance,
				SectionID:    item.sectionID,
				GeneratedAt:  time.Now().Format(time.RFC3339),
			}
		}
		finalPages[i] = page

		// Fire the callback as soon as this page is done — the UI sees it
		// immediately instead of waiting for every other page to finish.
		if onPage != nil {
			onPageMu.Lock()
			completedCount++
			cp := completedCount
			onPageMu.Unlock()
			_ = onPage(&page, cp, total)
		}
	})

	// ── Assemble wiki in original section/page order ──────────────────────────
	// onPage was already fired inside Stage D; this loop only populates the
	// returned WikiCache (needed for the final wiki structure saved to disk).
	now := time.Now()
	wiki := &WikiCache{
		Structure: WikiStructure{
			ID:          fmt.Sprintf("wiki-%s-%d", graph.RepoName, now.Unix()),
			Title:       plan.Title,
			Description: plan.Description,
			CreatedAt:   now,
			UpdatedAt:   now,
			Provider:    req.Provider,
			Model:       req.Model,
		},
		GeneratedPages: make(map[string]WikiPage),
		RepoPath:       graph.RepoPath,
		Provider:       req.Provider,
		Model:          req.Model,
		Language:       lang,
	}
	for idx := range items {
		wiki.GeneratedPages[finalPages[idx].ID] = finalPages[idx]
	}

	// Build section structure from plan order.
	pageIdx := 0
	for _, sec := range plan.Sections {
		wikiSec := WikiSection{
			ID:          sec.ID,
			Title:       sec.Title,
			Description: sec.Description,
		}
		for range sec.Pages {
			wikiSec.PageIDs = append(wikiSec.PageIDs, items[pageIdx].plan.ID)
			pageIdx++
		}
		wiki.Structure.Sections = append(wiki.Structure.Sections, wikiSec)
	}

	return wiki, nil
}

// ---------------------------------------------------------------------------
// JSON repair helpers
// ---------------------------------------------------------------------------

// repairJSON applies heuristic fixes to LLM-generated JSON that has common
// errors: trailing commas before ] or }, and mismatched closing brackets.
// Always call this before json.Unmarshal on LLM text.
func repairJSON(s string) string {
	s = strings.TrimSpace(s)
	s = removeTrailingCommas(s)
	s = balanceBrackets(s)
	return s
}

func removeTrailingCommas(s string) string {
	bs := []byte(s)
	inStr := false
	result := make([]byte, 0, len(bs))
	i := 0
	for i < len(bs) {
		ch := bs[i]
		if ch == '\\' && inStr && i+1 < len(bs) {
			result = append(result, ch, bs[i+1])
			i += 2
			continue
		}
		if ch == '"' {
			inStr = !inStr
		}
		if !inStr && ch == ',' {
			j := i + 1
			for j < len(bs) && (bs[j] == ' ' || bs[j] == '\t' || bs[j] == '\n' || bs[j] == '\r') {
				j++
			}
			if j < len(bs) && (bs[j] == ']' || bs[j] == '}') {
				i++ // skip the trailing comma
				continue
			}
		}
		result = append(result, ch)
		i++
	}
	return string(result)
}

func balanceBrackets(s string) string {
	bs := []byte(s)
	var stack []byte
	inStr := false
	for i := 0; i < len(bs); i++ {
		ch := bs[i]
		if ch == '\\' && inStr && i+1 < len(bs) {
			i++
			continue
		}
		if ch == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch ch {
		case '{', '[':
			stack = append(stack, ch)
		case '}':
			if len(stack) > 0 && stack[len(stack)-1] == '[' {
				bs[i] = ']'
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case ']':
			if len(stack) > 0 && stack[len(stack)-1] == '{' {
				bs[i] = '}'
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			bs = append(bs, '}')
		} else {
			bs = append(bs, ']')
		}
	}
	return string(bs)
}

// ---------------------------------------------------------------------------
// Research — deep multi-turn research on a codebase topic
// ---------------------------------------------------------------------------

// Research performs a multi-turn deep research session on a topic.
func (gen *Generator) Research(ctx context.Context, graph *CodeGraph, req ResearchRequest) (*ResearchResult, error) {
	maxIter := req.MaxIterations
	if maxIter <= 0 {
		maxIter = 4
	}
	lang := req.Language
	if lang == "" {
		lang = "en"
	}

	result := &ResearchResult{
		Query: req.Query,
	}
	var allFindings strings.Builder
	filesUsed := make(map[string]bool)
	entitiesUsed := make(map[string]bool)

	for iter := 1; iter <= maxIter; iter++ {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		// Get relevant context for this iteration
		searchQuery := req.Query
		if iter > 1 {
			lines := strings.Split(allFindings.String(), "\n")
			if len(lines) > 5 {
				searchQuery = req.Query + " " + strings.Join(lines[len(lines)-5:], " ")
			}
		}

		var contextParts []string
		if gen.embedder != nil {
			results, err := gen.embedder.Search(ctx, graph, searchQuery, 8)
			if err == nil {
				for _, r := range results {
					if r.Entity != nil {
						entitiesUsed[r.Entity.QualifiedName] = true
						filesUsed[r.Entity.FilePath] = true
						contextParts = append(contextParts, fmt.Sprintf("### %s %s\n%s\n```\n%s\n```",
							r.Entity.Kind, r.Entity.QualifiedName,
							r.Entity.DocComment, truncate(r.Entity.Body, 2000)))
					} else if r.Chunk != nil {
						filesUsed[r.Chunk.FilePath] = true
						contextParts = append(contextParts, fmt.Sprintf("// %s:%d\n%s",
							r.Chunk.FilePath, r.Chunk.StartLine, r.Chunk.Content))
					}
				}
			}
		}

		ragContext := strings.Join(contextParts, "\n---\n")
		if ragContext == "" {
			ragContext = "(no relevant context found)"
		}

		system, user := promptDeepResearch(graph.RepoName, req.Query, ragContext, allFindings.String(), lang, iter, maxIter)
		iterOutput, err := gen.generateWithFallback(ctx, system, user)
		if err != nil {
			return result, fmt.Errorf("research iteration %d: %w", iter, err)
		}

		result.Iterations = append(result.Iterations, iterOutput)
		allFindings.WriteString(iterOutput)
		allFindings.WriteString("\n\n")

		if iter == maxIter {
			result.Conclusion = iterOutput
		}
	}

	for f := range filesUsed {
		result.FilesUsed = append(result.FilesUsed, f)
	}
	for e := range entitiesUsed {
		result.Entities = append(result.Entities, e)
	}
	return result, nil
}

// --- helpers ---

func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		idx := strings.Index(s, "\n")
		if idx > 0 {
			s = s[idx+1:]
		}
		if strings.HasSuffix(s, "```") {
			s = s[:len(s)-3]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... (truncated)"
}
