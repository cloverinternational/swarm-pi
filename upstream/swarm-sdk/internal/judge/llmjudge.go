package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tokens"
)

// llmjudge.go implements Layer 2: an LLM judges the one question Layers 1 and 3
// cannot answer cheaply — "does this test meaningfully verify the behavior it
// claims to, and would it fail if the code regressed?"
//
// The judge is deliberately scoped to the non-deterministic question. The
// mechanical facts (assertion count, mock-only, skip) come from Layer 1, and
// the empirical ground truth comes from Layer 3. This division is the
// anti-trick design: the model is never the sole arbiter of a hard fail.

// Judge evaluates a single analyzed test and returns an *LLM judgement, or nil
// when no judgement could be produced (the caller then relies on the other
// layers). Implementations MUST NOT panic and MUST respect ctx cancellation.
type Judge interface {
	Evaluate(ctx context.Context, at AnalyzedTest, targetSrc string) *LLM
}

// NoopJudge returns nil for every test. It lets the batch runner operate with
// Layers 1+3 only (e.g. in CI without provider credentials).
type NoopJudge struct{}

// Evaluate implements Judge.
func (NoopJudge) Evaluate(context.Context, AnalyzedTest, string) *LLM { return nil }

// SDKJudge is an LLM-backed Judge built on the SDK sub-agent machinery, reusing
// the same factory/executor pattern as the autogenskills CuratorAgent.
type SDKJudge struct {
	factory      agent.Factory
	provCfg      provider.Config
	logger       observability.Logger
	maxBodyTok   int // per-test token budget for the test body
	maxTargetTok int // per-test token budget for the target source
}

// SDKJudgeConfig configures an SDKJudge.
type SDKJudgeConfig struct {
	Factory  agent.Factory
	Provider provider.Config // must have non-empty Name
	Logger   observability.Logger
	// MaxBodyTokens / MaxTargetTokens bound how much source is sent per test.
	// Zero falls back to sane defaults (1500 / 1500).
	MaxBodyTokens   int
	MaxTargetTokens int
}

// NewSDKJudge constructs an SDKJudge. Required: Factory, Provider.Name, Logger.
func NewSDKJudge(cfg SDKJudgeConfig) (*SDKJudge, error) {
	if cfg.Factory == nil {
		return nil, fmt.Errorf("judge: SDKJudge requires non-nil Factory")
	}
	if cfg.Provider.Name == "" {
		return nil, fmt.Errorf("judge: SDKJudge requires Provider with non-empty Name")
	}
	if cfg.Logger == nil {
		return nil, fmt.Errorf("judge: SDKJudge requires non-nil Logger")
	}
	body := cfg.MaxBodyTokens
	if body <= 0 {
		body = 1500
	}
	target := cfg.MaxTargetTokens
	if target <= 0 {
		target = 1500
	}
	return &SDKJudge{
		factory:      cfg.Factory,
		provCfg:      cfg.Provider,
		logger:       cfg.Logger,
		maxBodyTok:   body,
		maxTargetTok: target,
	}, nil
}

const judgeSystemPrompt = `You are a strict reviewer of Go test honesty.

A "real" test exercises the actual code under test and asserts on its observable
behavior, so it would FAIL if that code regressed. A "fake" test passes
regardless of whether the code works: it has no assertions, only exercises
mocks, asserts constants, or never calls the real code.

You are given a single Go test function and the source of the symbol(s) it
claims to test. Judge ONLY whether the test meaningfully verifies behavior.
Do not re-count assertions; that is done separately. Focus on substance.

Respond with STRICT JSON only, no markdown, exactly this shape:
{"verifies_behavior": <0-3 int>, "would_fail_if_broken": <bool>, "mock_overuse": <bool>, "reasoning": "<one sentence>"}

Scoring: 0 = verifies nothing; 1 = trivial/shallow; 2 = covers the main path;
3 = thoroughly verifies behavior including edge handling.`

// Evaluate implements Judge by running a one-shot sub-agent with a focused
// prompt and parsing its JSON reply. On any failure it logs and returns nil so
// Reduce falls back to the deterministic and mutation layers.
func (j *SDKJudge) Evaluate(ctx context.Context, at AnalyzedTest, targetSrc string) *LLM {
	ctx = agent.WithSubAgent(ctx)

	body := tokens.Truncate(at.Body, j.maxBodyTok)
	target := tokens.Truncate(targetSrc, j.maxTargetTok)
	prompt := j.buildPrompt(at, body, target)

	cfg := agent.SubAgentConfig{
		AgentID:        fmt.Sprintf("test-judge-%d", time.Now().UnixNano()),
		AgentName:      "Test Honesty Judge",
		Description:    "Judges whether a Go test meaningfully verifies behavior",
		ProviderConfig: j.provCfg,
		SystemPrompt:   judgeSystemPrompt,
		Tools:          nil, // no tools: the judge only reads and replies
		MaxTurns:       2,
		Timeout:        90 * time.Second,
	}

	sub, err := j.factory.CreateSubAgent(ctx, cfg)
	if err != nil {
		j.logger.Warn(ctx, "judge.create_subagent_failed", observability.F("err", err.Error()))
		return nil
	}
	exec, err := agent.NewSubAgentExecutor(sub)
	if err != nil {
		j.logger.Warn(ctx, "judge.executor_failed", observability.F("err", err.Error()))
		return nil
	}
	out, err := exec.Execute(ctx, prompt)
	if err != nil {
		j.logger.Warn(ctx, "judge.execute_failed",
			observability.F("test", at.TestName), observability.F("err", err.Error()))
		return nil
	}
	res := parseLLM(out)
	if res == nil {
		j.logger.Warn(ctx, "judge.parse_failed",
			observability.F("test", at.TestName), observability.F("raw", truncForLog(out)))
	}
	return res
}

func (j *SDKJudge) buildPrompt(at AnalyzedTest, body, target string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "TEST FUNCTION: %s (package %s)\n\n", at.TestName, at.Package)
	if len(at.RealSymbols) > 0 {
		fmt.Fprintf(&b, "Real symbols it references: %s\n\n", strings.Join(at.RealSymbols, ", "))
	}
	b.WriteString("TEST SOURCE:\n")
	b.WriteString(body)
	b.WriteString("\n\n")
	if strings.TrimSpace(target) != "" {
		b.WriteString("SOURCE OF THE CODE UNDER TEST (excerpt):\n")
		b.WriteString(target)
		b.WriteString("\n\n")
	}
	b.WriteString("Return the strict JSON judgement now.")
	return b.String()
}

// jsonObjRe extracts the first {...} object from a possibly chatty reply.
var jsonObjRe = regexp.MustCompile(`(?s)\{.*\}`)

// parseLLM tolerantly extracts the judgement JSON from raw model output. Returns
// nil when nothing parseable is present.
func parseLLM(raw string) *LLM {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	// Strip ```json fences if present.
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")

	candidate := raw
	if !strings.HasPrefix(strings.TrimSpace(candidate), "{") {
		if m := jsonObjRe.FindString(raw); m != "" {
			candidate = m
		}
	}
	var out LLM
	if err := json.Unmarshal([]byte(strings.TrimSpace(candidate)), &out); err != nil {
		return nil
	}
	// Clamp score to the documented 0-3 range.
	if out.VerifiesBehavior < 0 {
		out.VerifiesBehavior = 0
	}
	if out.VerifiesBehavior > 3 {
		out.VerifiesBehavior = 3
	}
	return &out
}

func truncForLog(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
