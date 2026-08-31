package deepwiki

import (
	"fmt"
)

// ---------------------------------------------------------------------------
// Prompt philosophy (based on production agent research):
//
// 1. Context engineering, not prompt engineering.
//    Each prompt receives STRUCTURED, ITEMIZED context — not narrative blobs.
//    We assemble exactly what the model needs for that step, no more.
//
// 2. Sequential pages with a clear visual contract.
//    Every page follows: Overview → Architecture diagram → Component breakdown
//    → Data/control-flow diagram → Code references → Gotchas & pitfalls.
//    The reader always knows where they are.
//
// 3. Mermaid diagrams are mandatory, not optional.
//    Visual representations of architecture, flow, and relationships are the
//    primary way readers orient themselves. Two diagrams minimum per page.
//
// 4. Page kind drives diagram choice.
//    "architecture" pages get classDiagram/graph TD component maps.
//    "flow" pages get sequenceDiagram or flowchart.
//    "reference" pages get a table + call-graph.
//    "overview" pages get a high-level graph of all key components.
//    "concepts" pages get a mindmap or ER diagram.
//
// 5. Code references are explicit, not implied.
//    Every section that describes behavior cites the exact file + entity name.
//    Readers can jump straight to the code.
//
// 6. Gotchas are a first-class section.
//    Hidden assumptions, edge cases, and non-obvious interactions deserve
//    their own callout block — not a footnote.
// ---------------------------------------------------------------------------

// diagramGuide returns diagram type guidance based on page kind.
func diagramGuide(kind string) string {
	switch kind {
	case "overview":
		return "Use a ```mermaid``` graph TD showing ALL major components as nodes with labeled edges.\n" +
			"Keep it high-level — one node per major package/module/service, not per function."
	case "architecture":
		return "Use a ```mermaid``` classDiagram or graph TD showing the structural relationships:\n" +
			"structs/interfaces and their fields, which types compose or embed others, dependency directions.\n" +
			"Then use a second diagram showing the component boundaries and responsibilities."
	case "flow":
		return "Use a ```mermaid``` sequenceDiagram for request/response or inter-component flows.\n" +
			"Use a flowchart for decision logic or processing pipelines.\n" +
			"Show the actual function/method names at each step, not generic labels."
	case "reference":
		return "Use a ```mermaid``` graph LR showing the call graph: which functions call which.\n" +
			"Use a table for the public API surface (function | signature | purpose)."
	case "concepts":
		return "Use a ```mermaid``` mindmap or graph TD showing how concepts relate.\n" +
			"Use an erDiagram if the page covers data models or schemas."
	default:
		return "Use at least two ```mermaid``` diagrams: one showing component structure (graph TD or classDiagram)\n" +
			"and one showing data or control flow (sequenceDiagram or flowchart)."
	}
}

// promptRegeneratePage builds prompts for rewriting an existing wiki page
// based on user instructions. Preserves Mermaid diagrams and inline code
// references unless the instructions explicitly ask to change them.
func promptRegeneratePage(repoName, pageTitle, existingContent, entityContext, chunkContext, instructions, language string) (system, user string) {
	if language == "" {
		language = "English"
	}

	system = "You are an expert software engineer editing a wiki page for the " + repoName + " codebase.\n" +
		"You MUST respond in " + language + ".\n\n" +
		"Output rules:\n" +
		"- Write valid GitHub-flavoured Markdown only. No outer code fences around the whole response.\n" +
		"- Preserve ALL Mermaid diagrams unless the instructions explicitly ask to change or remove them.\n" +
		"- If the instructions add new content involving structure or flow, ADD a new Mermaid diagram.\n" +
		"- Code references MUST use the inline format `file.go:L12-45` throughout the prose.\n" +
		"- Do NOT invent functionality not present in the provided context.\n" +
		"- Non-obvious behaviours go in blockquotes: > ⚠️ **Gotcha:** description"

	user = "Rewrite the wiki page titled **\"" + pageTitle + "\"** following these instructions:\n\n" +
		"<instructions>\n" + instructions + "\n</instructions>\n\n" +
		"<existing_content>\n" + existingContent + "\n</existing_content>\n\n" +
		"<code_entities>\n" + entityContext + "\n</code_entities>\n\n" +
		"<document_context>\n" + chunkContext + "\n</document_context>\n\n" +
		"Apply the instructions precisely. Keep everything correct in the existing content.\n" +
		"Preserve the page's structure and all Mermaid diagrams unless the instructions say otherwise.\n" +
		"Add > ⚠️ **Gotcha:** callouts for any non-obvious behaviour you discover in the new context."

	return system, user
}

// promptDeepResearch builds prompts for multi-turn deep research sessions.
// Each iteration builds on structured prior findings — never rewrites the
// whole context blob (ACE framework: incremental delta updates).
func promptDeepResearch(repoName, query, context, priorFindings, language string, iteration, maxIter int) (system, user string) {
	if language == "" {
		language = "English"
	}

	isFinal := iteration >= maxIter

	system = "You are an expert code analyst examining the " + repoName + " codebase.\n" +
		"You MUST respond in " + language + ".\n\n" +
		"Output rules:\n" +
		"- Structure findings as itemized bullets with metadata, not paragraphs.\n" +
		"- Every finding must cite the source file and entity name.\n" +
		"- Use Mermaid diagrams where they clarify relationships or flow.\n" +
		"- Do NOT repeat findings already captured in prior iterations."

	if iteration == 1 {
		user = fmt.Sprintf("Research this topic across the codebase: **%s**\n\n"+
			"<code_context>\n%s\n</code_context>\n\n"+
			"This is iteration 1 of %d.\n\n"+
			"Structure your response as:\n\n"+
			"## Research Plan\n"+
			"- What aspects will be investigated across all %d iterations\n"+
			"- Which files/packages are most relevant\n\n"+
			"## Initial Findings\n"+
			"Bullet list, each item: **[file -> entity]** finding\n\n"+
			"## Open Questions\n"+
			"What needs deeper investigation in the next iteration.",
			query, context, maxIter, maxIter)

	} else if isFinal {
		user = fmt.Sprintf("Provide the final synthesis for: **%s**\n\n"+
			"<prior_findings>\n%s\n</prior_findings>\n\n"+
			"<new_context>\n%s\n</new_context>\n\n"+
			"Structure your response as:\n\n"+
			"## Final Conclusion\n"+
			"A concise, direct answer to the research question.\n\n"+
			"## Architecture Overview\n"+
			"```mermaid\ngraph TD showing the key components and relationships discovered\n```\n\n"+
			"## Key Findings\n"+
			"Bullet list of the most important discoveries, each citing **[file -> entity]**\n\n"+
			"## Gotchas & Non-Obvious Behaviour\n"+
			"> ⚠️ **Gotcha:** each non-obvious finding as a callout\n\n"+
			"## Actionable Recommendations\n"+
			"Concrete next steps for a developer working in this area.",
			query, priorFindings, context)

	} else {
		user = fmt.Sprintf("Continue researching: **%s**\n\n"+
			"<prior_findings>\n%s\n</prior_findings>\n\n"+
			"<new_context>\n%s\n</new_context>\n\n"+
			"This is iteration %d of %d.\n\n"+
			"Structure your response as:\n\n"+
			"## Research Update %d\n"+
			"Only NEW findings not yet in prior_findings. Each item: **[file -> entity]** finding\n\n"+
			"## Refined Understanding\n"+
			"How does this new context change or deepen the picture?\n\n"+
			"## Open Questions\n"+
			"What still needs investigation.",
			query, priorFindings, context, iteration, maxIter, iteration)
	}

	return system, user
}
