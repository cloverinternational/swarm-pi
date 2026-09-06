package deepwiki

// repair_agent.go — post-generation markdown quality pass.
//
// RepairAgent runs a fast LLM call to fix formatting issues in a generated
// WikiPage. It only fires when detectIssues finds real problems, so clean
// pages cost zero extra LLM calls.

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// RepairAgent does a lightweight markdown quality pass after each page is
// generated, fixing common LLM output artifacts without rewriting content.
type RepairAgent struct {
	llm LLMClient
}

// NewRepairAgent creates a RepairAgent backed by the given LLM client.
// Pass the fast/fallback model so repairs are cheap.
func NewRepairAgent(llm LLMClient) *RepairAgent {
	return &RepairAgent{llm: llm}
}

// RepairPage checks page.Content for formatting issues and returns the fixed
// content. If no issues are found, or if the repair call fails, it returns
// the original content unchanged.
func (r *RepairAgent) RepairPage(ctx context.Context, page *WikiPage) (string, error) {
	if page == nil || page.Content == "" {
		return page.Content, nil
	}
	issues := detectIssues(page.Content)
	if len(issues) == 0 {
		return page.Content, nil
	}

	issueList := strings.Join(issues, "\n- ")
	system := `You are a markdown formatting repair tool. Fix ONLY the listed issues and return the corrected markdown. Do not rewrite, summarize, or change any content — only fix the specific formatting problems listed. Return the full corrected document with no preamble or explanation.`
	user := fmt.Sprintf(
		"Page title: %s\n\nIssues to fix:\n- %s\n\n---\n\n%s",
		page.Title, issueList, page.Content,
	)

	fixed, err := r.llm.Generate(ctx, system, user)
	if err != nil {
		// Repair failure is non-fatal — return original
		return page.Content, nil
	}

	fixed = strings.TrimSpace(fixed)
	// Strip any accidental markdown fence wrapper the LLM might add
	if after, ok := strings.CutPrefix(fixed, "```markdown"); ok {
		fixed = after
		fixed = strings.TrimSuffix(strings.TrimSpace(fixed), "```")
		fixed = strings.TrimSpace(fixed)
	}
	// Sanity check: repaired content must be at least 60% of original length
	// to guard against LLM over-truncation.
	if len(fixed) < len(page.Content)*6/10 {
		return page.Content, nil
	}
	return fixed, nil
}

// ---------------------------------------------------------------------------
// Issue detection — pure static analysis, no LLM
// ---------------------------------------------------------------------------

func detectIssues(content string) []string {
	var issues []string

	lines := strings.Split(content, "\n")

	// 1. Unclosed code fence — odd number of ``` occurrences
	fenceCount := strings.Count(content, "```")
	if fenceCount%2 != 0 {
		issues = append(issues, "unclosed code fence (odd number of ``` delimiters)")
	}

	// 2. Mermaid block with no recognised diagram type on its first content line
	inMermaid := false
	mermaidFirstLine := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "```mermaid" {
			inMermaid = true
			mermaidFirstLine = true
			continue
		}
		if inMermaid && mermaidFirstLine {
			mermaidFirstLine = false
			keywords := []string{"graph", "sequenceDiagram", "flowchart", "classDiagram",
				"erDiagram", "gantt", "pie", "gitGraph", "stateDiagram", "mindmap", "timeline"}
			found := false
			for _, kw := range keywords {
				if strings.HasPrefix(trimmed, kw) {
					found = true
					break
				}
			}
			if !found && trimmed != "" && trimmed != "```" {
				issues = append(issues, fmt.Sprintf("mermaid block has unrecognised diagram type: %q", trimmed))
			}
		}
		if inMermaid && trimmed == "```" {
			inMermaid = false
		}
	}

	// 3. Heading hierarchy skip (e.g. # → ####, gap > 1)
	prevLevel := 0
	for _, line := range lines {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		level := 0
		for _, ch := range line {
			if ch == '#' {
				level++
			} else {
				break
			}
		}
		if level > 0 && level <= 6 {
			if prevLevel > 0 && level > prevLevel+1 {
				issues = append(issues, fmt.Sprintf("heading hierarchy jumps from H%d to H%d", prevLevel, level))
			}
			prevLevel = level
		}
	}

	// 4. Duplicate section headings
	seen := map[string]int{}
	for _, line := range lines {
		if strings.HasPrefix(line, "#") {
			heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
			key := strings.ToLower(heading)
			seen[key]++
			if seen[key] == 2 {
				issues = append(issues, fmt.Sprintf("duplicate heading %q", heading))
			}
		}
	}

	// 5. Truncated content — last non-empty line looks mid-sentence
	lastLine := ""
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t != "" {
			lastLine = t
			break
		}
	}
	if lastLine != "" {
		// Skip headings, list items, code blocks, table rows
		isStructural := strings.HasPrefix(lastLine, "#") ||
			strings.HasPrefix(lastLine, "-") ||
			strings.HasPrefix(lastLine, "*") ||
			strings.HasPrefix(lastLine, "|") ||
			strings.HasPrefix(lastLine, "```") ||
			strings.HasPrefix(lastLine, ">")
		if !isStructural {
			endings := []byte{'.', '?', '!', ':', '*', ')', ']', '"', '\''}
			ended := false
			last := lastLine[len(lastLine)-1]
			if slices.Contains(endings, last) {
				ended = true
			}
			if !ended && len(lastLine) > 20 {
				issues = append(issues, "content appears truncated (last sentence has no terminal punctuation)")
			}
		}
	}

	// 6. Broken markdown table — column count mismatch between header and separator
	for i := 0; i+1 < len(lines); i++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(lines[i+1]), "|") {
			continue
		}
		// Check it's actually a separator row
		sep := strings.ReplaceAll(lines[i+1], " ", "")
		sep = strings.ReplaceAll(sep, "-", "")
		sep = strings.ReplaceAll(sep, ":", "")
		allPipes := true
		for _, ch := range sep {
			if ch != '|' {
				allPipes = false
				break
			}
		}
		if !allPipes {
			continue
		}
		countPipes := func(s string) int {
			return strings.Count(s, "|") - 1
		}
		headerCols := countPipes(lines[i])
		sepCols := countPipes(lines[i+1])
		if headerCols > 0 && sepCols > 0 && headerCols != sepCols {
			issues = append(issues, fmt.Sprintf("markdown table column count mismatch: header has %d, separator has %d", headerCols, sepCols))
		}
	}

	return issues
}
