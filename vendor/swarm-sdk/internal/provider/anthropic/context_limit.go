package anthropic

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// contextLimitExceeded describes a parsed "input length and max_tokens exceed
// context limit" error from the Anthropic API.
type contextLimitExceeded struct {
	InputLength int // tokens the API counted as input
	MaxTokens   int // max_tokens we sent
	ContextSize int // model's context window (input + max_tokens must be ≤ this)
}

// reContextLimit matches the Anthropic error message:
//
//	"input length and `max_tokens` exceed context limit: 182530 + 31999 > 200000, ..."
var reContextLimit = regexp.MustCompile(
	`input length and .max_tokens. exceed context limit:\s*(\d+)\s*\+\s*(\d+)\s*>\s*(\d+)`,
)

// parseContextLimitExceeded attempts to parse the "exceed context limit" error.
// Returns nil if the message doesn't match the expected pattern.
func parseContextLimitExceeded(msg string) *contextLimitExceeded {
	matches := reContextLimit.FindStringSubmatch(msg)
	if len(matches) < 4 {
		return nil
	}
	inputLen, err1 := strconv.Atoi(matches[1])
	maxTok, err2 := strconv.Atoi(matches[2])
	ctxSize, err3 := strconv.Atoi(matches[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return nil
	}
	return &contextLimitExceeded{
		InputLength: inputLen,
		MaxTokens:   maxTok,
		ContextSize: ctxSize,
	}
}

// clampedMaxTokens computes a new max_tokens value that will satisfy the
// Anthropic constraint: input_length + max_tokens ≤ contextSize.
//
// It uses the API-reported inputLength (authoritative) rather than any
// client-side estimate, so the clamped value is guaranteed to be accepted.
//
// Returns 0 if there is no room for any output tokens (input already fills
// the entire context window), signalling that the caller should return the
// original error rather than retry with 0.
func (e *contextLimitExceeded) clampedMaxTokens() int {
	available := e.ContextSize - e.InputLength
	if available <= 0 {
		return 0
	}
	return available
}

// clampResult carries the details of a successful max_tokens clamp-and-retry.
type clampResult struct {
	originalMaxTokens int
	clampedMaxTokens  int
	inputLength       int
	contextSize       int
}

// tryClampMaxTokens inspects a categorized API error for the "input length and
// max_tokens exceed context limit" pattern.  If it matches and there is room
// to clamp max_tokens, it mutates the request in-place and returns a
// clampResult with details.  Otherwise it returns (_, false) and the request
// is untouched.
//
// CONTRACT: the caller must rebuild the HTTP request after this returns true,
// since anthropicReq.MaxTokens has been overwritten.
func (p *Provider) tryClampMaxTokens(ctx context.Context, categorizedErr error, anthropicReq *MessageRequest) (*clampResult, bool) {
	// The error must be a Permanent error with code "anthropic.invalid_request"
	// containing the context-limit-exceeded message.
	if categorizedErr == nil {
		return nil, false
	}

	// Extract the inner SDK error to check the code and message.
	msg := categorizedErr.Error()

	// Quick substring check before running the regex.
	if !strings.Contains(msg, "exceed context limit") {
		return nil, false
	}

	parsed := parseContextLimitExceeded(msg)
	if parsed == nil {
		return nil, false
	}

	clamped := parsed.clampedMaxTokens()
	if clamped <= 0 {
		// Input alone exceeds the context window — nothing we can do.
		p.logger.Warn(ctx, "anthropic.context_limit.input_exceeds_window",
			observability.F("input_length", parsed.InputLength),
			observability.F("context_size", parsed.ContextSize),
		)
		return nil, false
	}

	// Sanity: don't retry if clamped value is the same or higher (shouldn't happen).
	if clamped >= anthropicReq.MaxTokens {
		return nil, false
	}

	p.logger.Info(ctx, "anthropic.context_limit.clamping_max_tokens",
		observability.F("model", anthropicReq.Model),
		observability.F("original_max_tokens", anthropicReq.MaxTokens),
		observability.F("clamped_max_tokens", clamped),
		observability.F("input_length", parsed.InputLength),
		observability.F("context_size", parsed.ContextSize),
	)

	result := &clampResult{
		originalMaxTokens: anthropicReq.MaxTokens,
		clampedMaxTokens:  clamped,
		inputLength:       parsed.InputLength,
		contextSize:       parsed.ContextSize,
	}

	// Mutate the request in-place — caller rebuilds the HTTP request.
	anthropicReq.MaxTokens = clamped

	// Also clamp thinking budget if present — the same constraint applies
	// (budget_tokens must be < max_tokens for manual thinking).
	if anthropicReq.Thinking != nil && anthropicReq.Thinking.BudgetTokens > 0 {
		if anthropicReq.Thinking.BudgetTokens >= clamped {
			newBudget := clamped - 1
			if newBudget < 1024 {
				// Budget would be below the minimum (1024).
				// Disable manual thinking rather than sending an invalid budget.
				p.logger.Warn(ctx, "anthropic.context_limit.thinking_budget_disabled",
					observability.F("original_budget", anthropicReq.Thinking.BudgetTokens),
					observability.F("clamped_max_tokens", clamped),
				)
				anthropicReq.Thinking = nil
			} else {
				p.logger.Warn(ctx, "anthropic.context_limit.thinking_budget_clamped",
					observability.F("original_budget", anthropicReq.Thinking.BudgetTokens),
					observability.F("new_budget", newBudget),
				)
				anthropicReq.Thinking.BudgetTokens = newBudget
			}
		}
	}

	return result, true
}
