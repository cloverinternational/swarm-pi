package deepwiki

import (
	"fmt"
	"sort"
	"strings"
)

// ============================================================
// PLANNER COUNCIL PROMPTS  (Stage 3)
// ============================================================

// promptCodeFirstPlanner builds Planner A's prompt.
// Planner A sees ONLY deterministic file content — no embeddings.
// Its job: identify structural layers and architectural boundaries from real code.
func promptCodeFirstPlanner(gl *GroundingLayer, repoName, language string) (system, user string) {
	if language == "" {
		language = "English"
	}

	system = "You are a senior software architect analyzing the " + repoName + " codebase.\n" +
		"You MUST respond in " + language + ".\n" +
		"You produce valid JSON only — no markdown fences, no explanation outside the JSON.\n\n" +
		"Rules:\n" +
		"- Use ONLY names, paths, and identifiers visible in the context below.\n" +
		"- Do not invent package names or file paths.\n" +
		"- Layer names must be specific to THIS codebase — derived from the actual code.\n" +
		"- Boundaries must name the real interface or contract that connects two layers."

	var sb strings.Builder

	// Doc files first — most important for understanding the repo
	if len(gl.DocFiles) > 0 {
		fmt.Fprintf(&sb, "## Documentation\n")
		for _, df := range gl.DocFiles {
			fmt.Fprintf(&sb, "\n### %s\n%s\n", df.Path, df.Content)
		}
	}

	// File tree
	// Cap the file tree: Planner A needs it to verify paths, not enumerate
	// every leaf.  4 k tokens (≈ 800 paths at 5 tokens/path) is sufficient.
	cappedTree := hardCapTokens(gl.FileTree, 4_000)
	fmt.Fprintf(&sb, "\n## File Tree\n```\n%s```\n", cappedTree)

	// Entry point files (full content)
	if len(gl.TopFiles) > 0 {
		fmt.Fprintf(&sb, "\n## Key Files (highest entity density)\n")
		for _, tf := range gl.TopFiles {
			fmt.Fprintf(&sb, "\n### %s\n```\n%s\n```\n", tf.Path, tf.Content)
		}
	}

	// Interface definitions
	if len(gl.Interfaces) > 0 {
		fmt.Fprintf(&sb, "\n## Interfaces (contracts between layers)\n")
		// Cap at 40 interfaces — signatures for a full 100-interface list easily
		// consume 10k+ tokens and overwhelm Planner A's signal-to-noise ratio.
		maxIfaces := 40
		for i, iface := range gl.Interfaces {
			if i >= maxIfaces {
				fmt.Fprintf(&sb, "\n... (%d more interfaces omitted)\n", len(gl.Interfaces)-maxIfaces)
				break
			}
			rel := graphRelPath("", iface.FilePath)
			fmt.Fprintf(&sb, "\n### %s  [%s:L%d-%d]\n", iface.Name, rel, iface.StartLine, iface.EndLine)
			if iface.DocComment != "" {
				fmt.Fprintf(&sb, "Doc: %s\n", iface.DocComment)
			}
			if iface.Signature != "" {
				fmt.Fprintf(&sb, "```\n%s\n```\n", iface.Signature)
			}
		}
	}

	// Import graph (top 30 heaviest edges)
	if len(gl.ImportGraph) > 0 {
		fmt.Fprintf(&sb, "\n## Import Graph (A imports B — top 30 by out-degree)\n")
		type edge struct {
			from string
			tos  []string
		}
		var edges []edge
		for from, tos := range gl.ImportGraph {
			edges = append(edges, edge{from, tos})
		}
		// Sort by out-degree descending
		sort.Slice(edges, func(i, j int) bool {
			return len(edges[i].tos) > len(edges[j].tos)
		})
		shown := 0
		for _, e := range edges {
			if shown >= 30 {
				break
			}
			for _, to := range e.tos {
				fmt.Fprintf(&sb, "  %s → %s\n", e.from, to)
			}
			shown++
		}
	}

	user = fmt.Sprintf(
		"Analyze the %s codebase and identify its structural architecture.\n\n"+
			"<codebase_context>\n%s\n</codebase_context>\n\n"+
			"Respond with ONLY this JSON:\n"+
			"{\n"+
			"  \"layers\": [\n"+
			"    {\n"+
			"      \"name\": \"Layer name specific to this codebase\",\n"+
			"      \"files\": [\"path/to/file.go\"],\n"+
			"      \"interfaces\": [\"InterfaceName\"],\n"+
			"      \"entry_points\": [\"path/to/main.go\"]\n"+
			"    }\n"+
			"  ],\n"+
			"  \"boundaries\": [\n"+
			"    \"LayerA communicates with LayerB via InterfaceX\"\n"+
			"  ]\n"+
			"}",
		repoName, sb.String())

	return system, user
}

// promptSemanticPlanner builds Planner B's prompt.
// Planner B sees ONLY topic clusters from embeddings — no raw file reads.
// Its job: identify thematic areas and workflows from semantic groupings.
func promptSemanticPlanner(clusters []TopicCluster, repoName, language string) (system, user string) {
	if language == "" {
		language = "English"
	}

	system = "You are a domain analyst identifying thematic areas in the " + repoName + " codebase.\n" +
		"You MUST respond in " + language + ".\n" +
		"You produce valid JSON only — no markdown fences, no explanation outside the JSON.\n\n" +
		"Rules:\n" +
		"- Theme names must use DOMAIN language from the code — not generic terms like 'Utils' or 'Helpers'.\n" +
		"- Workflows describe runtime sequences: what calls what in what order.\n" +
		"- Concepts describe domain rules, algorithms, or design patterns.\n" +
		"- DataFlows describe how data is transformed or moved through the system."

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Semantic Topic Clusters (derived from embedding similarity)\n")
	for i, cluster := range clusters {
		fmt.Fprintf(&sb, "\n### Cluster %d: %s\n", i+1, cluster.Label)
		fmt.Fprintf(&sb, "Files (%d):\n", len(cluster.Files))
		for _, f := range cluster.Files {
			fmt.Fprintf(&sb, "  - %s\n", f)
		}
		if len(cluster.RepresentativeChunks) > 0 {
			fmt.Fprintf(&sb, "Representative code samples:\n")
			for j, chunk := range cluster.RepresentativeChunks {
				fmt.Fprintf(&sb, "```\n// sample %d\n%s\n```\n", j+1, chunk)
			}
		}
	}

	user = fmt.Sprintf(
		"Analyze the semantic clusters from the %s codebase and identify its thematic areas.\n\n"+
			"<semantic_clusters>\n%s\n</semantic_clusters>\n\n"+
			"Respond with ONLY this JSON:\n"+
			"{\n"+
			"  \"themes\": [\n"+
			"    {\n"+
			"      \"name\": \"Theme name using domain language\",\n"+
			"      \"files\": [\"path/to/file.go\"],\n"+
			"      \"workflows\": [\"Step A triggers Step B which produces output C\"],\n"+
			"      \"concepts\": [\"Domain concept or algorithm name\"],\n"+
			"      \"data_flows\": [\"Input X is transformed by Y into Z\"]\n"+
			"    }\n"+
			"  ]\n"+
			"}",
		repoName, sb.String())

	return system, user
}

// promptSynthesisPlanner builds the Synthesis Planner's prompt.
// It receives both A and B outputs as pre-marshaled (and optionally
// pre-compressed) JSON strings, so the caller can apply compressSynthesisInputs
// before the prompt is assembled.
func promptSynthesisPlanner(structuralJSON, thematicJSON, fileTree string, repoName, language string, maxPages int) (system, user string) {
	if language == "" {
		language = "English"
	}
	if maxPages <= 0 {
		maxPages = 20
	}

	system = "You are planning a structured developer wiki for the " + repoName + " codebase.\n" +
		"You MUST respond in " + language + ".\n" +
		"You produce valid JSON only — no markdown fences, no explanation outside the JSON.\n\n" +
		"Rules:\n" +
		"- focus_files MUST be paths that appear verbatim in the File Tree provided.\n" +
		"- required_sections is an ORDERED list of H2 headings for this page.\n" +
		"- section_queries maps each heading to a SPECIFIC 10-15 word search query describing\n" +
		"  the mechanism to explain — NOT the heading title paraphrased.\n" +
		"- related_pages lists only page IDs defined in THIS plan.\n" +
		"- model_hint: 'fast' for simple reference pages, 'medium' for most pages,\n" +
		"  'heavy' for the Overview and complex architecture pages.\n" +
		"- First section must always be 'Overview' with exactly one overview page.\n" +
		"- Section and page titles must be specific to THIS codebase."

	user = fmt.Sprintf(
		"Merge the structural and thematic analyses into a final wiki plan for %s.\n\n"+
			"<structural_analysis title=\"Planner A: code-first\">\n%s\n</structural_analysis>\n\n"+
			"<thematic_analysis title=\"Planner B: semantic\">\n%s\n</thematic_analysis>\n\n"+
			"<file_tree title=\"Ground truth — focus_files must appear here exactly\">\n%s\n</file_tree>\n\n"+
			"Generate as many pages as the codebase warrants — small repos need 10–20, large ones 50+.\n"+
			"Aim for %d pages or more if complexity demands it. Every significant subsystem,\n"+
			"component, and concept should have its own page. Err on completeness, not brevity.\n"+
			"Respond with ONLY this JSON:\n"+
			"{\n"+
			"  \"title\": \"%s Wiki\",\n"+
			"  \"description\": \"One sentence: what this repo does and why it exists\",\n"+
			"  \"sections\": [\n"+
			"    {\n"+
			"      \"id\": \"section-slug\",\n"+
			"      \"title\": \"Section Title (codebase-specific)\",\n"+
			"      \"description\": \"What this section covers\",\n"+
			"      \"pages\": [\n"+
			"        {\n"+
			"          \"id\": \"page-slug\",\n"+
			"          \"title\": \"Page Title\",\n"+
			"          \"kind\": \"overview|architecture|flow|concepts|reference\",\n"+
			"          \"focus_entities\": [\"pkg.TypeName\"],\n"+
			"          \"focus_files\": [\"path/from/file/tree.go\"],\n"+
			"          \"required_sections\": [\"H2 Heading 1\", \"H2 Heading 2\"],\n"+
			"          \"section_queries\": {\n"+
			"            \"H2 Heading 1\": \"specific 10-15 word query about mechanism\"\n"+
			"          },\n"+
			"          \"related_pages\": [\"other-page-slug\"],\n"+
			"          \"model_hint\": \"fast|medium|heavy\",\n"+
			"          \"importance\": \"high|medium|low\"\n"+
			"        }\n"+
			"      ]\n"+
			"    }\n"+
			"  ]\n"+
			"}",
		repoName, structuralJSON, thematicJSON, fileTree, maxPages, repoName)

	return system, user
}

// ============================================================
// PAGE BUILDER PROMPTS  (Stage 4, 4 passes)
// ============================================================

// promptSkeletonPass builds Pass 1: section headings + scopes + search queries.
// Fast model. Minimal context. Decomposes the page into concrete sections.
func promptSkeletonPass(page *FinalPage, sigContext string, pageSummaries []PageSummary, language string) (system, user string) {
	if language == "" {
		language = "English"
	}

	system = "You are decomposing a wiki page plan into concrete, independently writable sections.\n" +
		"You MUST respond in " + language + ".\n" +
		"You produce a valid JSON array only — no markdown fences, no explanation outside the JSON.\n\n" +
		"Rules:\n" +
		"- search_query must be SPECIFIC to what this section needs to explain:\n" +
		"  10-15 words describing the mechanism, data flow, or concept — not the heading title.\n" +
		"- files_to_read must be a SUBSET of the focus_files list provided.\n" +
		"- scope is ONE sentence describing exactly what this section will cover.\n" +
		"- needs_diagram: true only if this section explains structure or flow that benefits from Mermaid.\n" +
		"- needs_table: true only if comparing 4+ items or documenting an API surface."

	var sb strings.Builder

	fmt.Fprintf(&sb, "## Page to decompose\n")
	fmt.Fprintf(&sb, "Title: %s\n", page.Title)
	fmt.Fprintf(&sb, "Kind: %s\n", page.Kind)
	fmt.Fprintf(&sb, "Required sections (ordered H2 headings):\n")
	for i, s := range page.RequiredSections {
		fmt.Fprintf(&sb, "  %d. %s\n", i+1, s)
		if q, ok := page.SectionQueries[s]; ok && q != "" {
			fmt.Fprintf(&sb, "     Hint query: %s\n", q)
		}
	}

	fmt.Fprintf(&sb, "\n## Focus files (files_to_read must be from this list)\n")
	for _, fp := range page.FocusFiles {
		fmt.Fprintf(&sb, "  - %s\n", fp)
	}

	if sigContext != "" {
		fmt.Fprintf(&sb, "\n## Entity signatures in focus files\n%s\n", sigContext)
	}

	// Last 5 page summaries to avoid duplication
	if len(pageSummaries) > 0 {
		fmt.Fprintf(&sb, "\n## Already covered in previous pages (DO NOT REPEAT)\n")
		start := 0
		if len(pageSummaries) > 5 {
			start = len(pageSummaries) - 5
		}
		for _, ps := range pageSummaries[start:] {
			fmt.Fprintf(&sb, "- **%s**: ", ps.Title)
			fmt.Fprintf(&sb, "%s\n", strings.Join(ps.Bullets, "; "))
		}
	}

	user = fmt.Sprintf(
		"Decompose the wiki page '%s' into its required sections.\n\n"+
			"<page_context>\n%s\n</page_context>\n\n"+
			"Respond with ONLY a JSON array:\n"+
			"[\n"+
			"  {\n"+
			"    \"heading\": \"Exact H2 heading text\",\n"+
			"    \"scope\": \"One sentence: what specifically this section covers\",\n"+
			"    \"search_query\": \"specific 10-15 word query about mechanism not title\",\n"+
			"    \"needs_diagram\": true,\n"+
			"    \"needs_table\": false,\n"+
			"    \"files_to_read\": [\"path/from/focus_files_list.go\"]\n"+
			"  }\n"+
			"]",
		page.Title, sb.String())

	return system, user
}

// promptSectionPass builds Pass 2: write a single section of a page.
// Called once per section. growingPage is everything written above this section.
func promptSectionPass(repoName, pageTitle, heading, scope string, entityContext, chunkContext, growingPage string, language string) (system, user string) {
	if language == "" {
		language = "English"
	}

	system = "You are writing one section of a developer wiki page for the " + repoName + " codebase.\n" +
		"You MUST respond in " + language + ".\n" +
		"Write valid GitHub-flavoured Markdown. No outer code fences around the whole response.\n\n" +
		"Rules:\n" +
		"- Write ONLY the '## " + heading + "' section.\n" +
		"- Start your response with '## " + heading + "' (the H2 heading).\n" +
		"- Do NOT include any other H2 headings.\n" +
		"- Do NOT repeat content from 'Already written above'.\n" +
		"- Cite file references INLINE as `file.go:L12-45` throughout prose.\n" +
		"- Every Mermaid diagram MUST be in a fenced block: ```mermaid ... ```\n" +
		"- Non-obvious behaviors go in blockquotes: > ⚠️ **Gotcha:** description\n" +
		"- Do NOT invent functionality not present in the provided context."

	var sb strings.Builder

	fmt.Fprintf(&sb, "## Page: %s\n", pageTitle)
	fmt.Fprintf(&sb, "## Section to write: %s\n", heading)
	fmt.Fprintf(&sb, "Scope: %s\n", scope)

	if growingPage != "" {
		above := growingPage
		if len(above) > 2000 {
			above = "...(earlier content omitted)...\n" + above[len(above)-2000:]
		}
		fmt.Fprintf(&sb, "\n## Already written above (DO NOT REPEAT)\n%s\n", above)
	}

	if entityContext != "" {
		fmt.Fprintf(&sb, "\n## Code entities\n%s\n", entityContext)
	}

	if chunkContext != "" {
		fmt.Fprintf(&sb, "\n## Relevant source chunks\n%s\n", chunkContext)
	}

	user = fmt.Sprintf(
		"Write the '## %s' section for the '%s' wiki page.\n\n"+
			"<context>\n%s\n</context>\n\n"+
			"Start your response with '## %s' and write only this section.\n"+
			"Cite code references inline as `file.go:L12-45`.",
		heading, pageTitle, sb.String(), heading)

	return system, user
}

// promptSynthesisPass builds Pass 3: add intro, spanning diagram, and cross-refs.
// Called once per page after all sections are written.
// Instructs the LLM NOT to rewrite existing prose — only ADD missing pieces.
func promptSynthesisPass(repoName, pageTitle, pageKind, growingPage string, relatedSummaries []PageSummary, focusFiles []string, language string) (system, user string) {
	if language == "" {
		language = "English"
	}

	system = "You are finalizing a wiki page for the " + repoName + " codebase.\n" +
		"You MUST respond in " + language + ".\n" +
		"Write valid GitHub-flavoured Markdown. No outer code fences.\n\n" +
		"CRITICAL RULE: Do NOT rewrite or paraphrase existing prose.\n" +
		"You may ONLY add the specific elements listed below.\n" +
		"Emit the COMPLETE page with your additions inserted in the correct positions."

	dg := diagramGuide(pageKind)

	var sb strings.Builder

	fmt.Fprintf(&sb, "## Assembled page content (all sections already written)\n%s\n", growingPage)

	if len(relatedSummaries) > 0 {
		fmt.Fprintf(&sb, "\n## Related pages already in this wiki\n")
		for _, ps := range relatedSummaries {
			fmt.Fprintf(&sb, "- **%s** (id: %s): %s\n", ps.Title, ps.PageID, strings.Join(ps.Bullets, "; "))
		}
	}

	if len(focusFiles) > 0 {
		fmt.Fprintf(&sb, "\n## Focus files (for Relevant source files header)\n")
		for _, fp := range focusFiles {
			fmt.Fprintf(&sb, "  - %s\n", fp)
		}
	}
	user = fmt.Sprintf(
		"Finalize the '%s' wiki page by adding ONLY these elements:\n\n"+
			"1. A **'Relevant source files:'** line listing all focus files as inline refs\n"+
			"   (`path/to/file.go`) — place this as the VERY FIRST line of the page.\n\n"+
			"2. A one-paragraph introduction AFTER the source files line, BEFORE the first H2.\n\n"+
			"3. A spanning Mermaid diagram showing the overall structure of this page's topic.\n"+
			"   Diagram guidance: %s\n\n"+
			"4. Cross-reference notes where the related pages are relevant:\n"+
			"   '> See also: [Page Title]' — insert inline where appropriate.\n\n"+
			"5. A '> ⚠️ **Gotcha:**' callout for any non-obvious behavior visible in the content.\n\n"+
			"<assembled_page>\n%s\n</assembled_page>\n\n"+
			"Emit the COMPLETE finalized page now.",
		pageTitle, dg, sb.String())

	return system, user
}

// promptReflectorPass builds Pass 4: cross-model factual verification.
// Uses a DIFFERENT model than Pass 3 to catch hallucinations Pass 3 might miss.
func promptReflectorPass(pageTitle, pageContent string, validEntities []string, validFilePaths []string, language string) (system, user string) {
	if language == "" {
		language = "English"
	}

	system = "You are verifying a wiki page for factual accuracy against a codebase.\n" +
		"You MUST respond in " + language + ".\n" +
		"You produce valid JSON only — no markdown fences, no explanation outside the JSON.\n\n" +
		"Check rules:\n" +
		"- Flag a 'bad_file_ref' if a code reference like `file.go:L12-45` uses a path NOT in valid_file_paths.\n" +
		"- Flag a 'bad_entity_ref' if a backtick identifier like `FuncName` or `TypeName` does NOT appear\n" +
		"  in valid_entity_names AND is presented as a real code entity (not a variable name).\n" +
		"- Flag a 'hallucinated_claim' ONLY for definitive factual statements about code behavior\n" +
		"  that cannot be verified from the page content itself.\n" +
		"- Do NOT flag stylistic choices, diagram labels, or conceptual descriptions.\n" +
		"- Report ONLY real issues. Prefer valid:true with empty issues over over-flagging."

	// Cap entity and path lists to avoid context bloat
	entities := validEntities
	if len(entities) > 200 {
		entities = entities[:200]
	}
	paths := validFilePaths
	if len(paths) > 200 {
		paths = paths[:200]
	}

	user = fmt.Sprintf(
		"Verify the '%s' wiki page for factual accuracy.\n\n"+
			"<page_content>\n%s\n</page_content>\n\n"+
			"<valid_file_paths>\n%s\n</valid_file_paths>\n\n"+
			"<valid_entity_names>\n%s\n</valid_entity_names>\n\n"+
			"Respond with ONLY this JSON:\n"+
			"{\n"+
			"  \"valid\": true,\n"+
			"  \"issues\": [\n"+
			"    {\n"+
			"      \"type\": \"bad_file_ref|bad_entity_ref|hallucinated_claim\",\n"+
			"      \"location\": \"## Section Name or 'introduction'\",\n"+
			"      \"description\": \"what is wrong\"\n"+
			"    }\n"+
			"  ]\n"+
			"}",
		pageTitle, pageContent,
		strings.Join(paths, "\n"),
		strings.Join(entities, "\n"))

	return system, user
}
