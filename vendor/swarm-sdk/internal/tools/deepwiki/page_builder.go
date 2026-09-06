package deepwiki

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ChunkedPageBuilder generates a single wiki page using 4 structured passes.
// Each pass has a specific job with scoped context — no single LLM call does everything.
//
// Pass 1 — Skeleton:  fast model, plan sections with targeted search queries
// Pass 2 — Sections:  medium model, write each section sequentially, appending
// Pass 3 — Synthesis: medium/heavy model, add intro + spanning diagram + cross-refs
// Pass 4 — Reflector: different fast model, verify file refs and entity claims
//
// The ACE framework (Incremental Context Engineering) principle is applied:
// global_page_summaries grows after every completed page, giving subsequent
// pages awareness of what has already been covered.
type ChunkedPageBuilder struct {
	fastLLM   LLMClient // Pass 1 (skeleton) and Pass 4 (reflector)
	mediumLLM LLMClient // Pass 2 (sections) and Pass 3 (synthesis)
	embedder  *Embedder
	graph     *CodeGraph
	language  string

	// ACE framework: accumulates one-line summaries from completed pages.
	// Each entry is a PageSummary appended after a page finishes.
	// Subsequent pages see these summaries in Pass 1 to avoid repetition.
	pageSummaries []PageSummary

	// Request logger for capturing LLM telemetry
	logger *RequestLogger
}

// NewChunkedPageBuilder creates a builder.
// If mediumLLM is nil, fastLLM is used for passes 2 and 3 too.
func NewChunkedPageBuilder(fastLLM, mediumLLM LLMClient, embedder *Embedder, graph *CodeGraph, language string) *ChunkedPageBuilder {
	if language == "" {
		language = "English"
	}
	med := mediumLLM
	if med == nil {
		med = fastLLM
	}
	return &ChunkedPageBuilder{
		fastLLM:   fastLLM,
		mediumLLM: med,
		embedder:  embedder,
		graph:     graph,
		language:  language,
	}
}

// SetLogger sets the request logger for capturing LLM telemetry.
func (b *ChunkedPageBuilder) SetLogger(logger *RequestLogger) {
	b.logger = logger
}

// AppendSummary adds a completed page summary to the ACE delta document.
// Call this after each page completes so subsequent pages see prior coverage.
func (b *ChunkedPageBuilder) AppendSummary(summary PageSummary) {
	b.pageSummaries = append(b.pageSummaries, summary)
}

// Build generates a complete wiki page using 4 structured passes.
// Returns the final page content and a PageSummary for the ACE delta.
func (b *ChunkedPageBuilder) Build(ctx context.Context, page *FinalPage) (content string, summary PageSummary, err error) {
	// Pre-compute in-degree for entity importance ranking (used in context assembly)
	inDegree := make(map[string]int)
	for _, edge := range b.graph.Edges {
		if edge.Kind == EdgeCalls || edge.Kind == EdgeReferences {
			inDegree[edge.To]++
		}
	}

	relRef := func(filePath string, start, end int) string {
		rel := graphRelPath(b.graph.RepoPath, filePath)
		if rel == "" {
			rel = filePath
		}
		if start > 0 && end > 0 {
			return fmt.Sprintf("%s:L%d-%d", rel, start, end)
		}
		return rel
	}

	// --- PASS 1: SKELETON ---
	sigContext := b.buildSignatureContext(page, relRef)
	sectionPlan, err := b.passOneSkeleton(ctx, page, sigContext)
	if err != nil {
		// Fallback: derive section plan from required_sections
		sectionPlan = b.fallbackSectionPlan(page)
	}

	// Integrator check: verify files_to_read against graph (deterministic, no LLM)
	sectionPlan = b.verifySectionPlan(sectionPlan, page)

	// --- PASS 2: SECTION WRITING ---
	growingPage, err := b.passTwoSections(ctx, page, sectionPlan, inDegree, relRef)
	if err != nil {
		return "", PageSummary{}, fmt.Errorf("section pass: %w", err)
	}

	// --- PASS 3: SYNTHESIS ---
	finalDraft, err := b.passThreeSynthesis(ctx, page, growingPage)
	if err != nil {
		// Fallback: use growingPage as-is (synthesis is additive, not critical)
		finalDraft = growingPage
	}

	// --- PASS 4: REFLECTOR ---
	report, reflErr := b.passFourReflector(ctx, page, finalDraft)
	if reflErr == nil && !report.Valid && len(report.Issues) > 0 {
		// Append verification notes surgically — do NOT regenerate whole page
		finalDraft = b.appendVerificationNotes(finalDraft, report)
	}

	// ACE delta: extract summary for subsequent pages
	summary = builderExtractSummary(page, finalDraft)

	return finalDraft, summary, nil
}

// passOneSkeleton asks a fast model to plan section headings + search queries.
func (b *ChunkedPageBuilder) passOneSkeleton(ctx context.Context, page *FinalPage, sigContext string) ([]SectionPlan, error) {
	system, user := promptSkeletonPass(page, sigContext, b.pageSummaries, b.language)
	start := time.Now()
	resp, err := b.callLLM(ctx, b.fastLLM, system, user)
	if err != nil {
		return nil, err
	}
	var plan []SectionPlan
	clean := repairJSON(stripJSONFences(resp))
	parseErr := json.Unmarshal([]byte(clean), &plan)
	logLLMCall(LLMCallLog{
		CallType:    "skeleton_pass",
		System:      system,
		User:        user,
		RawResponse: resp,
		Cleaned:     clean,
		ParseOK:     parseErr == nil,
		ParseError:  llmErrStr(parseErr),
		DurationMs:  time.Since(start).Milliseconds(),
	})
	if parseErr != nil {
		return nil, fmt.Errorf("parse skeleton: %w", parseErr)
	}
	return plan, nil
}

// passTwoSections writes each section sequentially, appending to a growing page.
// Each section gets its own scoped context — entity context, filtered chunks,
// and the content written so far (ACE delta pattern).
func (b *ChunkedPageBuilder) passTwoSections(ctx context.Context, page *FinalPage, sections []SectionPlan, inDegree map[string]int, relRef func(string, int, int) string) (string, error) {
	var growingPage strings.Builder

	for _, section := range sections {
		if ctx.Err() == context.Canceled {
			return growingPage.String(), context.Canceled
		}

		// Entity context scoped to this section's files
		entityContext := b.buildEntityContextForFiles(section.FilesToRead, inDegree, relRef, 3000)

		// Chunk search with section-specific query (NOT the generic page title)
		var chunkParts []string
		if b.embedder != nil && section.SearchQuery != "" {
			searchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			rawResults, sErr := b.embedder.Search(searchCtx, b.graph, section.SearchQuery, 10)
			cancel()
			if sErr == nil {
				// Filter distractors before sending to LLM
				filtered := FilterChunks(rawResults, section.SearchQuery, 6, 0.15)
				for _, r := range filtered {
					if r.Chunk != nil {
						ref := relRef(r.Chunk.FilePath, r.Chunk.StartLine, r.Chunk.EndLine)
						chunkParts = append(chunkParts, fmt.Sprintf("// %s\n%s", ref, r.Chunk.Content))
					}
				}
			}
		}

		entityCtx := entityContext
		// Cap total chunk context to a token budget using the same
		// compaction.EstimateTokens heuristic (4 chars ≈ 1 token).
		// This prevents any single section pass from blowing the model context.
		chunkCtx := capToTokenBudget(chunkParts, "\n---\n", sectionChunkBudgetTokens)
		if entityCtx == "" && chunkCtx == "" {
			entityCtx = "(no matching context found for this section)"
		}

		// Pass only the tail of what's written (avoid bloating context window)
		above := growingPage.String()
		if len(above) > 2000 {
			above = "...(earlier content omitted)...\n" + above[len(above)-2000:]
		}

		system, user := promptSectionPass(
			b.graph.RepoName, page.Title,
			section.Heading, section.Scope,
			entityCtx, chunkCtx, above,
			b.language,
		)

		sectionContent, err := b.callLLM(ctx, b.mediumLLM, system, user)
		if err != nil {
			// Skip failed section with a placeholder — don't abort the whole page
			growingPage.WriteString(fmt.Sprintf("\n## %s\n\n*Section generation failed: %v*\n\n", section.Heading, err))
			continue
		}

		growingPage.WriteString(sectionContent)
		growingPage.WriteString("\n\n")
	}

	return growingPage.String(), nil
}

// passThreeSynthesis adds intro, spanning Mermaid diagram, and cross-refs.
// Instructs the model NOT to rewrite existing prose — only ADD missing pieces.
func (b *ChunkedPageBuilder) passThreeSynthesis(ctx context.Context, page *FinalPage, growingPage string) (string, error) {
	// Collect related page summaries for cross-referencing
	relatedIDs := make(map[string]bool, len(page.RelatedPages))
	for _, rid := range page.RelatedPages {
		relatedIDs[rid] = true
	}
	var relatedSummaries []PageSummary
	for _, ps := range b.pageSummaries {
		if relatedIDs[ps.PageID] {
			relatedSummaries = append(relatedSummaries, ps)
		}
	}

	system, user := promptSynthesisPass(
		b.graph.RepoName, page.Title, page.Kind,
		growingPage, relatedSummaries, page.FocusFiles,
		b.language,
	)
	return b.callLLM(ctx, b.mediumLLM, system, user)
}

// passFourReflector verifies the page using a fast model (different from Pass 3).
// Cross-model verification catches hallucinations that the generating model missed.
func (b *ChunkedPageBuilder) passFourReflector(ctx context.Context, page *FinalPage, content string) (*VerificationReport, error) {
	// Ground truth: all valid entity names and file paths from the CodeGraph
	var validEntities []string
	for qname := range b.graph.Entities {
		validEntities = append(validEntities, qname)
	}
	var validPaths []string
	for fp := range b.graph.FileEntities {
		validPaths = append(validPaths, graphRelPath(b.graph.RepoPath, fp))
	}

	system, user := promptReflectorPass(page.Title, content, validEntities, validPaths, b.language)
	start := time.Now()
	resp, err := b.callLLM(ctx, b.fastLLM, system, user)
	if err != nil {
		// Verification failure is non-fatal — skip and keep the draft
		return &VerificationReport{Valid: true}, nil
	}
	var report VerificationReport
	clean := repairJSON(stripJSONFences(resp))
	parseErr := json.Unmarshal([]byte(clean), &report)
	logLLMCall(LLMCallLog{
		CallType:    "reflector_pass",
		System:      system,
		User:        user,
		RawResponse: resp,
		Cleaned:     clean,
		ParseOK:     parseErr == nil,
		ParseError:  llmErrStr(parseErr),
		DurationMs:  time.Since(start).Milliseconds(),
	})
	if parseErr != nil {
		return &VerificationReport{Valid: true}, nil
	}
	return &report, nil
}

// appendVerificationNotes appends reflector issues as callouts at the end of
// the page. This is intentionally conservative — we mark rather than
// regenerate to avoid hallucination spirals.
func (b *ChunkedPageBuilder) appendVerificationNotes(content string, report *VerificationReport) string {
	if len(report.Issues) == 0 {
		return content
	}
	var notes strings.Builder
	notes.WriteString("\n\n---\n*Verification notes (auto-generated by Reflector):*\n")
	for _, issue := range report.Issues {
		notes.WriteString(fmt.Sprintf("- **%s** in `%s`: %s\n", issue.Type, issue.Location, issue.Description))
	}
	return content + notes.String()
}

// buildSignatureContext assembles entity name + signature (NO body) from focus files.
// Used by Pass 1 (skeleton) — needs names to plan sections, not full code.
func (b *ChunkedPageBuilder) buildSignatureContext(page *FinalPage, relRef func(string, int, int) string) string {
	var parts []string
	seen := make(map[string]bool)
	for _, fp := range page.FocusFiles {
		names := b.entityNamesForFile(fp)
		for _, qname := range names {
			if seen[qname] {
				continue
			}
			seen[qname] = true
			ent, ok := b.graph.Entities[qname]
			if !ok || ent.Kind == KindFile || ent.Kind == KindImport {
				continue
			}
			ref := relRef(ent.FilePath, ent.StartLine, ent.EndLine)
			line := fmt.Sprintf("[%s] %s  [%s]", ent.Kind, ent.Name, ref)
			if ent.Signature != "" {
				sig := ent.Signature
				if len(sig) > 80 {
					sig = sig[:80] + "..."
				}
				line += "  " + sig
			}
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, "\n")
}

// buildEntityContextForFiles assembles entity name+sig+body for specific files.
// Used by Pass 2 (section writing) — provides actual code for the section.
func (b *ChunkedPageBuilder) buildEntityContextForFiles(files []string, inDegree map[string]int, relRef func(string, int, int) string, maxBody int) string {
	var parts []string
	seen := make(map[string]bool)
	for _, fp := range files {
		names := b.entityNamesForFile(fp)
		for _, qname := range names {
			if seen[qname] {
				continue
			}
			seen[qname] = true
			ent, ok := b.graph.Entities[qname]
			if !ok || ent.Kind == KindFile || ent.Kind == KindImport {
				continue
			}
			ref := relRef(ent.FilePath, ent.StartLine, ent.EndLine)
			callers := inDegree[ent.QualifiedName]
			var sb strings.Builder
			if callers > 0 {
				fmt.Fprintf(&sb, "### %s %s  [%s, %d callers]\n", ent.Kind, ent.Name, ref, callers)
			} else {
				fmt.Fprintf(&sb, "### %s %s  [%s]\n", ent.Kind, ent.Name, ref)
			}
			if ent.DocComment != "" {
				fmt.Fprintf(&sb, "Doc: %s\n", ent.DocComment)
			}
			if ent.Signature != "" {
				fmt.Fprintf(&sb, "```\n%s\n```\n", ent.Signature)
			}
			if ent.Body != "" && maxBody > 0 {
				body := ent.Body
				if len(body) > maxBody {
					body = body[:maxBody] + "\n// ... truncated"
				}
				fmt.Fprintf(&sb, "\nSource:\n```\n%s\n```\n", body)
			}
			parts = append(parts, sb.String())
		}
	}
	return strings.Join(parts, "\n---\n")
}

// entityNamesForFile looks up entity names for a file path, trying both
// absolute and relative forms since the graph may index either.
func (b *ChunkedPageBuilder) entityNamesForFile(fp string) []string {
	names := b.graph.FileEntities[fp]
	if len(names) == 0 {
		// Try with repo root prepended
		names = b.graph.FileEntities[b.graph.RepoPath+"/"+fp]
	}
	if len(names) == 0 {
		// Try as absolute path reconstructed from relative
		names = b.graph.FileEntities[b.graph.RepoPath+string('/'+0)+fp]
	}
	return names
}

// fallbackSectionPlan builds a basic section plan from required_sections
// when the skeleton LLM call fails.
func (b *ChunkedPageBuilder) fallbackSectionPlan(page *FinalPage) []SectionPlan {
	var plan []SectionPlan
	for _, heading := range page.RequiredSections {
		query := heading
		if q, ok := page.SectionQueries[heading]; ok && q != "" {
			query = q
		}
		plan = append(plan, SectionPlan{
			Heading:     heading,
			Scope:       "Content for " + heading,
			SearchQuery: query,
			FilesToRead: page.FocusFiles,
		})
	}
	// If no required sections, create a single default section
	if len(plan) == 0 {
		plan = append(plan, SectionPlan{
			Heading:     "Overview",
			Scope:       "Overview of " + page.Title,
			SearchQuery: page.Title,
			FilesToRead: page.FocusFiles,
		})
	}
	return plan
}

// verifySectionPlan removes files_to_read entries that don't exist in the graph.
// Falls back to using all focus_files if a section's files_to_read is empty after filtering.
func (b *ChunkedPageBuilder) verifySectionPlan(sections []SectionPlan, page *FinalPage) []SectionPlan {
	validPaths := make(map[string]bool, len(page.FocusFiles))
	for _, fp := range page.FocusFiles {
		validPaths[fp] = true
	}
	for i := range sections {
		var valid []string
		for _, fp := range sections[i].FilesToRead {
			if validPaths[fp] {
				valid = append(valid, fp)
			}
		}
		if len(valid) == 0 {
			valid = page.FocusFiles
		}
		sections[i].FilesToRead = valid
	}
	return sections
}

// builderExtractSummary builds the ACE delta PageSummary for the completed page.
// Deterministic — no LLM. Extracts H2 headings + first sentence of each.
func builderExtractSummary(page *FinalPage, content string) PageSummary {
	var bullets []string
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		heading := strings.TrimPrefix(line, "## ")
		// Find first non-empty, non-heading, non-code-fence line after heading
		for j := i + 1; j < len(lines) && j < i+6; j++ {
			trimmed := strings.TrimSpace(lines[j])
			if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "```") {
				continue
			}
			sentence := trimmed
			if len(sentence) > 120 {
				sentence = sentence[:120] + "..."
			}
			bullets = append(bullets, heading+": "+sentence)
			break
		}
		if len(bullets) >= 3 {
			break
		}
	}

	return PageSummary{
		PageID:   page.ID,
		Title:    page.Title,
		Bullets:  bullets,
		Files:    page.FocusFiles,
		Entities: page.FocusEntities,
	}
}

// callLLM makes a single LLM call with an independent pageLLMTimeout deadline.
func (b *ChunkedPageBuilder) callLLM(ctx context.Context, client LLMClient, system, user string) (string, error) {
	if ctx.Err() == context.Canceled {
		return "", context.Canceled
	}
	callCtx, cancel := withCallDeadline(ctx)
	defer cancel()

	start := time.Now()
	resp, err := client.Generate(callCtx, system, user)
	duration := time.Since(start)

	// Log the request if logger is set
	if b.logger != nil {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		b.logger.Log(&RequestLog{
			Timestamp: start,
			Phase:     "v2_builder",
			Stage:     "callLLM",
			Provider:  client.Provider(),
			Model:     client.ModelName(),
			Request: RequestPayload{
				System: system,
				User:   user,
			},
			Response: ResponsePayload{
				Raw: resp,
			},
			Timing: TimingInfo{
				Start:    start,
				End:      time.Now(),
				Duration: duration,
			},
			Metadata: map[string]any{
				"error": errMsg,
			},
		})
	}

	return resp, err
}
