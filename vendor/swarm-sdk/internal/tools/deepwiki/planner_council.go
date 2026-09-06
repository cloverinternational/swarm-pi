package deepwiki

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
)

// synthesisMaxInputFraction is the fraction of the model context window that
// may be used by the synthesis prompt inputs (80/20 rule).  The remaining 20 %
// is reserved for the planner's JSON output.
const synthesisMaxInputFraction = 0.80

// PlannerCouncil runs the three-planner wiki planning system.
//
// Planner A (code-first) and Planner B (semantic) run in parallel.
// Their outputs are merged by the Synthesis Planner to produce a FinalPlan
// where every page has verified focus_files, required_sections, section_queries,
// and related_pages.
//
// Model assignment follows the "right model per step" principle:
//
//	fastLLM   → Planner A + Planner B  (cheap, JSON output, low reasoning need)
//	mediumLLM → Synthesis Planner       (merging two plans requires precision)
type PlannerCouncil struct {
	fastLLM   LLMClient
	mediumLLM LLMClient
	embedder  *Embedder
	language  string
	maxPages  int
}

// NewPlannerCouncil creates a planner council.
// If mediumLLM is nil, fastLLM is used for the Synthesis Planner too.
func NewPlannerCouncil(fastLLM, mediumLLM LLMClient, embedder *Embedder, language string, maxPages int) *PlannerCouncil {
	if language == "" {
		language = "English"
	}
	if maxPages <= 0 {
		maxPages = 20 // caller should pass auto-scaled value; this is a last-resort fallback
	}
	med := mediumLLM
	if med == nil {
		med = fastLLM
	}
	return &PlannerCouncil{
		fastLLM:   fastLLM,
		mediumLLM: med,
		embedder:  embedder,
		language:  language,
		maxPages:  maxPages,
	}
}

// Run executes all three planners and returns the verified FinalPlan.
//
// Execution order:
//  1. Build GroundingLayer deterministically from CodeGraph (no LLM)
//  2. Build TopicClusters from embeddings (no LLM)
//  3. Planner A and Planner B in parallel (fast LLM calls)
//  4. Synthesis Planner merges A+B (medium LLM call)
//  5. Integrator check: verify focus_files and related_pages (no LLM)
func (pc *PlannerCouncil) Run(ctx context.Context, graph *CodeGraph) (*FinalPlan, error) {
	// Stage 1: Deterministic grounding — no LLM
	gl := BuildGroundingLayer(graph, 5)

	// Stage 2: Topic clusters from embeddings — no LLM
	clusters := BuildTopicClusters(graph, 10)

	// Stage 3: Planner A + B in parallel
	var (
		structural *StructuralMap
		thematic   *ThematicMap
		errA, errB error
		wg         sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		structural, errA = pc.runCodeFirstPlanner(ctx, gl, graph.RepoName)
	}()
	go func() {
		defer wg.Done()
		thematic, errB = pc.runSemanticPlanner(ctx, clusters, graph.RepoName)
	}()
	wg.Wait()

	// Graceful fallbacks if either planner fails
	if errA != nil {
		structural = &StructuralMap{
			Layers:     []StructuralLayer{{Name: graph.RepoName}},
			Boundaries: []string{},
		}
	}
	if errB != nil {
		thematic = &ThematicMap{Themes: []Theme{}}
	}

	// Stage 4: Synthesis Planner merges A + B
	plan, err := pc.runSynthesisPlanner(ctx, structural, thematic, gl.FileTree, graph.RepoName)
	if err != nil {
		return nil, fmt.Errorf("synthesis planner: %w", err)
	}

	// Stage 5: Integrator check — no LLM
	plan = pc.verifyPlan(plan, graph)

	return plan, nil
}

func (pc *PlannerCouncil) runCodeFirstPlanner(ctx context.Context, gl *GroundingLayer, repoName string) (*StructuralMap, error) {
	system, user := promptCodeFirstPlanner(gl, repoName, pc.language)
	promptTok := compaction.EstimateTokens(system) + compaction.EstimateTokens(user)
	log.Printf("[deepwiki/council] planner-A prompt: %d tokens (%.1f%% of %d ctx)",
		promptTok, float64(promptTok)*100/float64(modelContextTokens), modelContextTokens)
	start := time.Now()
	resp, err := pc.callLLM(ctx, pc.fastLLM, system, user)
	if err != nil {
		return nil, fmt.Errorf("planner A: %w", err)
	}
	var result StructuralMap
	clean := repairJSON(stripJSONFences(resp))
	parseErr := json.Unmarshal([]byte(clean), &result)
	logLLMCall(LLMCallLog{
		CallType:    "planner_a_code_first",
		System:      system,
		User:        user,
		RawResponse: resp,
		Cleaned:     clean,
		ParseOK:     parseErr == nil,
		ParseError:  llmErrStr(parseErr),
		DurationMs:  time.Since(start).Milliseconds(),
	})
	if parseErr != nil {
		return nil, fmt.Errorf("planner A parse: %w\nraw: %s", parseErr, truncate(resp, 2000))
	}
	return &result, nil
}

func (pc *PlannerCouncil) runSemanticPlanner(ctx context.Context, clusters []TopicCluster, repoName string) (*ThematicMap, error) {
	system, user := promptSemanticPlanner(clusters, repoName, pc.language)
	promptTok := compaction.EstimateTokens(system) + compaction.EstimateTokens(user)
	log.Printf("[deepwiki/council] planner-B prompt: %d tokens (%.1f%% of %d ctx)",
		promptTok, float64(promptTok)*100/float64(modelContextTokens), modelContextTokens)
	start := time.Now()
	resp, err := pc.callLLM(ctx, pc.fastLLM, system, user)
	if err != nil {
		return nil, fmt.Errorf("planner B: %w", err)
	}
	var result ThematicMap
	clean := repairJSON(stripJSONFences(resp))
	parseErr := json.Unmarshal([]byte(clean), &result)
	logLLMCall(LLMCallLog{
		CallType:    "planner_b_semantic",
		System:      system,
		User:        user,
		RawResponse: resp,
		Cleaned:     clean,
		ParseOK:     parseErr == nil,
		ParseError:  llmErrStr(parseErr),
		DurationMs:  time.Since(start).Milliseconds(),
	})
	if parseErr != nil {
		return nil, fmt.Errorf("planner B parse: %w\nraw: %s", parseErr, truncate(resp, 2000))
	}
	return &result, nil
}

func (pc *PlannerCouncil) runSynthesisPlanner(ctx context.Context, structural *StructuralMap, thematic *ThematicMap, fileTree, repoName string) (*FinalPlan, error) {
	// Marshal planner outputs to JSON strings once — reused across retry levels.
	structuralJSON, err := json.MarshalIndent(structural, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal structural: %w", err)
	}
	thematicJSON, err := json.MarshalIndent(thematic, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal thematic: %w", err)
	}
	structStr := string(structuralJSON)
	thematicStr := string(thematicJSON)

	// Proactive compaction retry loop.
	// Each level hard-caps inputs (pure Go, no LLM) and retries the synthesis
	// planner.  On a timeout/context-exceeded error we escalate to the next level.
	for level := range synthesisBudgets {
		s, th, ft := compressSynthesisToLevel(structStr, thematicStr, fileTree, level)
		system, user := promptSynthesisPlanner(s, th, ft, repoName, pc.language, pc.maxPages)

		// ── Pre-flight: measure assembled prompt and skip this level if it
		// exceeds the 80 % input ceiling WITHOUT making an LLM call.
		// This avoids wasting a full 5-minute timeout per oversized attempt.
		promptTok := compaction.EstimateTokens(system) + compaction.EstimateTokens(user)
		maxInputTok := int(float64(modelContextTokens) * synthesisMaxInputFraction)
		if promptTok > maxInputTok {
			log.Printf("[deepwiki/council] synthesis level=%d: prompt %d tokens > 80%% ceiling %d — escalating without LLM call",
				level, promptTok, maxInputTok)
			continue
		}
		log.Printf("[deepwiki/council] synthesis level=%d: prompt %d tokens (%.1f%% of %d ctx) — sending",
			level, promptTok, float64(promptTok)*100/float64(modelContextTokens), modelContextTokens)

		start := time.Now()
		resp, callErr := pc.callLLM(ctx, pc.mediumLLM, system, user)
		if callErr != nil {
			if !synthesisRetryable(callErr) {
				return nil, fmt.Errorf("synthesis planner: %w", callErr)
			}
			log.Printf("[deepwiki/council] synthesis level=%d: medium LLM failed (%v) — trying fast LLM at same level", level, callErr)
			var fastErr error
			resp, fastErr = pc.callLLM(ctx, pc.fastLLM, system, user)
			if fastErr != nil {
				log.Printf("[deepwiki/council] synthesis level=%d: fast LLM also failed (%v) — escalating", level, fastErr)
				continue
			}
			log.Printf("[deepwiki/council] synthesis level=%d: fast LLM fallback succeeded", level)
			callErr = nil
		}

		clean := repairJSON(stripJSONFences(resp))
		var plan FinalPlan
		parseErr := json.Unmarshal([]byte(clean), &plan)
		logLLMCall(LLMCallLog{
			CallType:    fmt.Sprintf("synthesis_planner_l%d", level),
			System:      system,
			User:        user,
			RawResponse: resp,
			Cleaned:     clean,
			ParseOK:     parseErr == nil,
			ParseError:  llmErrStr(parseErr),
			DurationMs:  time.Since(start).Milliseconds(),
		})
		if parseErr != nil {
			log.Printf("[deepwiki/council] synthesis level=%d parse error, escalating: %v", level, parseErr)
			continue
		}
		log.Printf("[deepwiki/council] synthesis level=%d success: %d sections", level, len(plan.Sections))
		return &plan, nil
	}

	// All compaction levels exhausted — return a minimal graceful fallback plan
	// so downstream page generation can still proceed.
	log.Printf("[deepwiki/council] synthesis planner: all levels exhausted, using fallback plan")
	return makeFallbackPlan(repoName, fileTree), nil
}

// synthesisRetryable returns true if the error warrants retrying at a higher
// compaction level (i.e. the model timed out or rejected too-large input).
func synthesisRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "context length") ||
		strings.Contains(msg, "too long") ||
		strings.Contains(msg, "token")
}

// makeFallbackPlan builds a minimal single-section wiki plan used when the
// synthesis planner fails at every compaction level.
func makeFallbackPlan(repoName, fileTree string) *FinalPlan {
	var focusFiles []string
	for _, line := range strings.SplitN(fileTree, "\n", 30) {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "//") {
			focusFiles = append(focusFiles, line)
		}
		if len(focusFiles) >= 5 {
			break
		}
	}
	return &FinalPlan{
		Title:       repoName + " Wiki",
		Description: "Auto-generated wiki for the " + repoName + " codebase.",
		Sections: []FinalSection{{
			ID:          "overview",
			Title:       "Overview",
			Description: "High-level overview of the codebase.",
			Pages: []FinalPage{{
				ID:               "overview-page",
				Title:            "Overview",
				Kind:             "overview",
				FocusFiles:       focusFiles,
				RequiredSections: []string{"Introduction", "Architecture"},
				SectionQueries: map[string]string{
					"Introduction": "what does " + repoName + " do and why does it exist",
					"Architecture": "what are the main components and how do they interact",
				},
				ModelHint:  "heavy",
				Importance: "high",
			}},
		}},
	}
}

// verifyPlan is the Integrator check: removes focus_files that don't exist in
// the graph and removes related_pages references to non-existent page IDs.
// This is pure deterministic logic — no LLM involved.
func (pc *PlannerCouncil) verifyPlan(plan *FinalPlan, graph *CodeGraph) *FinalPlan {
	// Build set of all valid page IDs in this plan
	pageIDs := make(map[string]bool)
	for _, sec := range plan.Sections {
		for _, p := range sec.Pages {
			pageIDs[p.ID] = true
		}
	}

	// Build set of all valid file paths in graph (relative and absolute forms)
	validPaths := make(map[string]bool)
	for fp := range graph.FileEntities {
		validPaths[fp] = true
		validPaths[graphRelPath(graph.RepoPath, fp)] = true
		validPaths[filepath.Base(fp)] = true
	}
	for fp := range graph.FileChunks {
		validPaths[fp] = true
		validPaths[graphRelPath(graph.RepoPath, fp)] = true
	}

	for si := range plan.Sections {
		for pi := range plan.Sections[si].Pages {
			page := &plan.Sections[si].Pages[pi]

			// Filter focus_files
			var validFiles []string
			for _, fp := range page.FocusFiles {
				if validPaths[fp] || validPaths[strings.TrimPrefix(fp, "/")] {
					validFiles = append(validFiles, fp)
				}
			}
			page.FocusFiles = validFiles

			// Filter related_pages to only IDs defined in this plan
			var validRelated []string
			for _, rid := range page.RelatedPages {
				if pageIDs[rid] && rid != page.ID {
					validRelated = append(validRelated, rid)
				}
			}
			page.RelatedPages = validRelated

			// Default model_hint if missing
			if page.ModelHint == "" {
				page.ModelHint = "medium"
			}

			// Ensure SectionQueries map is initialised
			if page.SectionQueries == nil {
				page.SectionQueries = make(map[string]string)
			}
		}
	}

	return plan
}

// callLLM makes a single LLM call using withCallDeadline (same as page builder).
// The deadline is independent of the parent context so a tight caller deadline
// doesn't abort individual LLM calls prematurely.
// Default: 10 minutes.  Override with DEEPWIKI_CALL_TIMEOUT_MINUTES env var.
// Set DEEPWIKI_CALL_TIMEOUT_MINUTES=0 to disable the timeout entirely.
func (pc *PlannerCouncil) callLLM(ctx context.Context, client LLMClient, system, user string) (string, error) {
	if ctx.Err() == context.Canceled {
		return "", context.Canceled
	}
	callCtx, cancel := withCallDeadline(ctx)
	defer cancel()
	return client.Generate(callCtx, system, user)
}
