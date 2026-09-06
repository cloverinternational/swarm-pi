package silver

import (
	"strings"
)

// domainProfile defines the tool usage patterns and keyword signals
// that characterize each domain.
type domainProfile struct {
	Name string

	// StrongTools are tools that strongly indicate this domain.
	// If a window is dominated by these tools, confidence is high.
	StrongTools map[string]float64 // tool -> weight

	// Keywords in user prompts or tool params that signal this domain.
	Keywords []string

	// FileExtensions that indicate this domain when seen in file paths.
	FileExtensions []string
}

var domainProfiles = []domainProfile{
	{
		Name: DomainSoftwareEngineering,
		StrongTools: map[string]float64{
			"Edit":            1.0,
			"Write":           0.8,
			"grep":            0.9,
			"Read":            0.5, // common across domains
			"MultiEdit":       1.0,
			"semantic_grep":   1.0,
			"semantic_rename": 1.0,
		},
		Keywords: []string{
			"test", "build", "compile", "import", "function", "struct",
			"package", "module", "git", "commit", "branch", "merge",
			"refactor", "lint", "fmt", "debug", "breakpoint",
		},
		FileExtensions: []string{".go", ".ts", ".tsx", ".js", ".py", ".rs", ".java"},
	},
	{
		Name: DomainFinancialResearch,
		StrongTools: map[string]float64{
			"websearch":     1.0,
			"agent_browser": 0.9,
			"web_search":    1.0,
		},
		Keywords: []string{
			"stock", "ticker", "market", "revenue", "earnings", "report",
			"financial", "quarterly", "supply chain", "quartr", "sec",
			"valuation", "analyst", "price target", "market cap",
			"backfill", "ingestion", "etf", "portfolio",
		},
		FileExtensions: []string{".tex", ".bib"},
	},
	{
		Name: DomainScientificAnalysis,
		StrongTools: map[string]float64{
			"Bash": 0.3, // only when combined with R/Python science patterns
		},
		Keywords: []string{
			"diagram", "figure", "plot", "ggplot", "r script", "latex",
			"photonic", "quantum", "analysis", "dataset", "correlation",
			"hypothesis", "experiment", "citation", "academic", "paper",
			"pdf report", "extreme report",
		},
		FileExtensions: []string{".R", ".r", ".Rmd", ".ipynb", ".tex"},
	},
	{
		Name: DomainBusinessOperations,
		StrongTools: map[string]float64{
			"Bash":  0.6,
			"Shell": 0.7,
		},
		Keywords: []string{
			"deploy", "dag", "airflow", "clickhouse", "pipeline",
			"infrastructure", "terraform", "docker", "kubernetes",
			"monitoring", "backfill", "cron", "migration",
			"production", "staging", "rollback",
		},
		FileExtensions: []string{".yaml", ".yml", ".tf", ".sh"},
	},
}

// ClassifyDomain determines the activity domain for a set of event windows.
// It uses a weighted scoring approach based on tool usage patterns, keywords
// in user prompts and tool parameters, and file extension detection.
func ClassifyDomain(windows []EventWindow) DomainClassification {
	if len(windows) == 0 {
		return DomainClassification{Domain: DomainUnknown, Confidence: 0}
	}

	// Aggregate tool counts and collect text signals across all windows.
	toolCounts := make(map[string]int)
	var allText []string // user prompts and file paths for keyword matching

	for _, w := range windows {
		for tool, count := range w.ToolCounts {
			toolCounts[tool] += count
		}
		for _, evt := range w.Events {
			// Collect user prompts.
			if evt.Type == "user.prompt_submit" {
				if prompt, ok := evt.Payload["prompt"].(string); ok {
					allText = append(allText, strings.ToLower(prompt))
				}
				if content, ok := evt.Payload["content"].(string); ok {
					allText = append(allText, strings.ToLower(content))
				}
			}
			// Collect file paths from tool params.
			if fp, ok := evt.Payload["file_path"].(string); ok {
				allText = append(allText, strings.ToLower(fp))
			}
			if ti, ok := evt.Payload["tool_input"].(string); ok {
				allText = append(allText, strings.ToLower(ti))
			}
		}
	}

	combinedText := strings.Join(allText, " ")
	totalToolCalls := 0
	for _, c := range toolCounts {
		totalToolCalls += c
	}
	if totalToolCalls == 0 {
		return DomainClassification{Domain: DomainUnknown, Confidence: 0}
	}

	// Score each domain profile.
	type scored struct {
		domain  string
		score   float64
		signals DomainSignals
	}

	var results []scored
	for _, profile := range domainProfiles {
		var score float64
		var tools, keywords, artifacts []string

		// Tool-based scoring: weight by tool frequency * profile weight.
		for tool, weight := range profile.StrongTools {
			if count, ok := toolCounts[tool]; ok {
				toolScore := (float64(count) / float64(totalToolCalls)) * weight
				score += toolScore
				tools = append(tools, tool)
			}
		}

		// Keyword-based scoring.
		for _, kw := range profile.Keywords {
			if strings.Contains(combinedText, kw) {
				score += 0.05
				keywords = append(keywords, kw)
			}
		}

		// File extension scoring.
		for _, ext := range profile.FileExtensions {
			if strings.Contains(combinedText, ext) {
				score += 0.03
				artifacts = append(artifacts, ext)
			}
		}

		if score > 0 {
			results = append(results, scored{
				domain: profile.Name,
				score:  score,
				signals: DomainSignals{
					Tools:      tools,
					Keywords:   keywords,
					Artifacts:  artifacts,
					Confidence: score,
				},
			})
		}
	}

	if len(results) == 0 {
		return DomainClassification{Domain: DomainUnknown, Confidence: 0}
	}

	// Find the highest-scoring domain.
	best := results[0]
	for _, r := range results[1:] {
		if r.score > best.score {
			best = r
		}
	}

	// Normalize confidence to 0-1 range. Cap at 1.0.
	confidence := best.score
	if confidence > 1.0 {
		confidence = 1.0
	}
	best.signals.Confidence = confidence

	return DomainClassification{
		Domain:     best.domain,
		Confidence: confidence,
		Signals:    best.signals,
	}
}

// ClassifyWindowDomain classifies a single event window. Useful for detecting
// mid-session domain switches.
func ClassifyWindowDomain(window EventWindow) DomainClassification {
	return ClassifyDomain([]EventWindow{window})
}
