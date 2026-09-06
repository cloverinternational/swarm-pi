package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// GoldAnalyzer handles Gold tier analysis using an LLM agent
type GoldAnalyzer struct {
	provider provider.Provider
	logger   observability.Logger
}

// NewGoldAnalyzer creates a Gold tier analyzer with the given provider
func NewGoldAnalyzer(p provider.Provider, logger observability.Logger) *GoldAnalyzer {
	return &GoldAnalyzer{
		provider: p,
		logger:   logger,
	}
}

// RunGoldAnalysis spawns an agent to read Bronze/Silver and write Gold insights.
// Now actually executes the LLM call instead of discarding the prompt.
func (g *GoldAnalyzer) RunGoldAnalysis(ctx context.Context, projectDir string, hourly bool) error {
	period := "daily"
	if hourly {
		period = "hourly"
	}

	bronzeDir := filepath.Join(projectDir, ".swarm", "bronze")
	goldDir := filepath.Join(projectDir, ".swarm", "gold", period)

	if err := os.MkdirAll(goldDir, 0755); err != nil {
		return fmt.Errorf("failed to create gold dir: %w", err)
	}

	// Build the analysis prompt
	prompt := fmt.Sprintf(`You are the Gold Tier Analysis Agent.

Read Bronze events from: %s
Write Gold insights to: %s
Analysis period: %s

Your task:
1. Read Bronze JSONL files from the relevant time window
2. Identify patterns, anomalies, or useful observations
3. Write insights as markdown files to the Gold directory
4. Use descriptive filenames: YYYY-MM-DD-HH-topic.md
5. Include frontmatter: timestamp, category (pattern/anomaly/recommendation)

Write only genuinely useful insights. Skip trivial observations.`, bronzeDir, goldDir, period)

	// Execute LLM analysis (was previously discarded with "_ = prompt")
	if g.provider != nil {
		// Build message using the canonical conversation.Message type
		msg := &conversation.Message{
			Role:    "user",
			Content: prompt,
		}

		req := provider.ChatRequest{
			Messages:     []*conversation.Message{msg},
			SystemPrompt: "You are a Gold Tier Analysis Agent that synthesizes Bronze tier events into high-level insights.",
		}

		resp, err := g.provider.Chat(ctx, req)
		if err != nil {
			if g.logger != nil {
				g.logger.Error(ctx, "gold_analysis_failed", observability.F("error", err.Error()))
			}
			return fmt.Errorf("gold analysis LLM call failed: %w", err)
		}

		// Write the LLM response to a Gold insight file
		timestamp := time.Now().Format("2006-01-02-15-04")
		insightFile := filepath.Join(goldDir, fmt.Sprintf("%s-gold-insight.md", timestamp))

		content := fmt.Sprintf("---\ntimestamp: %s\ncategory: gold-analysis\nperiod: %s\n---\n\n%s",
			time.Now().Format(time.RFC3339), period, resp.Message.Content)

		if err := os.WriteFile(insightFile, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write gold insight: %w", err)
		}

		if g.logger != nil {
			g.logger.Info(ctx, "gold_analysis_complete",
				observability.F("insight_file", insightFile),
				observability.F("period", period))
		}
	}

	return nil
}

// GoldDir returns the Gold tier directory for a project.
func GoldDir(projectDir string, period string) string {
	return filepath.Join(projectDir, ".swarm", "gold", period)
}
