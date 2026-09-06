# Token Estimation Feature

## Overview

The TUI now includes **initial token estimation** that calculates and displays estimated token counts **before** the API call is made. This gives users immediate visibility into context size and helps with budget control.

## How It Works

### Flow

```
User sends message
    ↓
[NEW] Estimate tokens (system prompt + history + message)
    ↓
[NEW] Send UpdateTokenCount (IsEstimated: true) 
    ↓
Display "~450 tokens (estimated)" in UI
    ↓
Send UpdateStreamStart
    ↓
Execute agent (API call)
    ↓
Stream response chunks
    ↓
[EXISTING] Send UpdateTokenCount (IsEstimated: false, actual from API)
    ↓
Display "478 tokens (actual)" in UI
    ↓
Send UpdateMessageComplete
    ↓
Send UpdateStreamEnd
```

### Two Token Updates

The system now sends **two** token count updates:

1. **Initial Estimate** (before API call)
   - `IsEstimated: true`
   - Based on char/token ratios from analysis of 514 conversations
   - Includes +2% safety margin to avoid context window issues

2. **Actual Count** (after API response)
   - `IsEstimated: false`
   - Actual token usage from provider API
   - Replaces the estimate with real data

## Token Estimation Algorithm

### Provider-Specific Ratios

Based on analysis of 26,122 messages across 12 models:

| Provider | Chars/Token | Safety Margin | Final Ratio |
|----------|-------------|---------------|-------------|
| Anthropic (Claude) | 3.70 | +2% | 3.774 |
| Google (Gemini) | 3.80 | +2% | 3.876 |
| OpenAI (GPT) | 4.00 | +2% | 4.080 |
| Zhipu (GLM) | 3.50 | +2% | 3.570 |
| Meta (Llama) | 3.60 | +2% | 3.672 |
| Local Models | 3.60 | +2% | 3.672 |

**Confidence**: 97.74%  
**Average Error**: 2.26%  
**Safety Margin**: +2% (to avoid context window overflows)

### Pattern Adjustments

The estimator automatically adjusts for content patterns:

| Pattern | Adjustment | Reason |
|---------|------------|--------|
| Code blocks | 0.85× | Fewer tokens per character |
| Inline code | 0.95× | Slightly fewer tokens |
| Plain text | 1.05× | Standard tokenization |
| Markdown | 1.00× | Neutral |
| High special chars | 0.90× | Special chars compress better |

## Implementation

### Core Components

1. **`headless/sdk/token_estimator.go`**
   - `TokenEstimator` struct with provider ratios
   - `EstimateInitialContext()` - estimates system prompt + history + user message
   - Pattern detection helpers
   - Provider detection from model name

2. **`headless/sdk/bridge.go`** (modified)
   - Creates estimator instance
   - Gets system prompt from agent
   - Estimates tokens before `UpdateStreamStart`
   - Sends initial `UpdateTokenCount` with `IsEstimated: true`
   - Sends actual token count after API response with `IsEstimated: false`

3. **`headless/core/event.go`** (modified)
   - Added `IsEstimated bool` field to `TokenCountPayload`
   - Allows UI to distinguish estimates from actuals

### Key Functions

#### `NewTokenEstimator()`
Creates a new token estimator with provider ratios and pattern adjustments.

#### `EstimateInitialContext(systemPrompt, history, userMessage, provider)`
Estimates total token count for the complete context.

**Returns:**
```go
type EstimatedTokens struct {
    SystemPromptTokens int     // Tokens in system prompt
    HistoryTokens      int     // Tokens in conversation history
    UserMessageTokens  int     // Tokens in current user message
    TotalEstimated     int     // Sum of all components
    Provider           string  // Provider name
    SafetyMargin       float64 // 0.02 (2%)
}
```

#### `getProviderFromModel(model string)`
Extracts provider name from model string.

**Examples:**
- `"claude-sonnet-4-5"` → `"anthropic"`
- `"gpt-4"` → `"openai"`
- `"gemini-3-pro-preview"` → `"google"`

## Usage

### In Code

The token estimation happens automatically in `bridge.ExecuteMessage()`. No additional code is needed.

### For UI Integration

Listen for `UpdateTokenCount` events and check the `IsEstimated` field:

```go
case core.UpdateTokenCount:
    if payload, ok := update.Payload.(core.TokenCountPayload); ok {
        if payload.IsEstimated {
            // Display: "~450 tokens (estimated)"
            fmt.Printf("~%d tokens (estimated)\n", payload.TotalTokens)
        } else {
            // Display: "478 tokens (actual)"
            fmt.Printf("%d tokens (actual)\n", payload.TotalTokens)
        }
    }
```

### Example UI Display

```
Initial (before API call):
┌─────────────────────────────┐
│ Context: ~450 tokens (est)  │
└─────────────────────────────┘

After API response:
┌─────────────────────────────┐
│ Input:  478 tokens          │
│ Output: 145 tokens          │
│ Total:  623 tokens          │
└─────────────────────────────┘
```

## Benefits

### For Users
✅ **Immediate feedback** - See estimated tokens instantly  
✅ **Budget control** - Know cost before API call  
✅ **Context awareness** - Understand context size upfront  
✅ **Abort opportunity** - Can cancel if estimate too high  

### For Developers
✅ **Non-breaking** - Enhances existing token tracking  
✅ **Accurate** - 97.74% confidence, 2.26% avg error  
✅ **Safe** - +2% margin prevents context overflows  
✅ **Fast** - Estimation is instant (no API call)  

## Testing

### Unit Tests

```bash
cd headless/sdk
go test -v -run TestTokenEstimator
go test -v -run TestEstimateInitialContext
go test -v -run TestGetProviderFromModel
go test -v -run TestPatternDetection
```

### Benchmarks

```bash
cd headless/sdk
go test -bench=BenchmarkEstimateTokens
go test -bench=BenchmarkEstimateInitialContext
```

### Integration Test

Build and run the TUI:

```bash
make build
./swarm
```

Send a message and observe:
1. Estimated token count appears immediately
2. Actual token count appears after API response
3. UI updates from estimate to actual

## Performance

- **Estimation time**: < 1ms for typical messages
- **Memory overhead**: ~10KB for estimator instance
- **No network calls**: All computation is local

## Future Enhancements

1. **Cache system prompts** - Pre-calculate token counts for static system prompts
2. **Model-specific fine-tuning** - Adjust ratios per specific model version
3. **Real-time learning** - Update ratios based on actual API responses
4. **Cost estimation** - Display estimated cost based on provider pricing
5. **Warning thresholds** - Alert when approaching context window limits

## Technical Details

### Data Source

Token estimation is based on analysis of:
- **514 conversations**
- **26,122 messages**
- **5,485,565 characters**
- **1,499,471 tokens** (estimated)
- **Date range**: Jan 18 - Feb 3, 2026

See `token-counting-experiment/` directory for full analysis.

### Accuracy

| Metric | Value |
|--------|-------|
| Confidence | 97.74% |
| Average Error | 2.26% |
| Median Error | 2.67% |
| Safety Margin | +2% |

### Provider Detection

The system automatically detects providers from model names:

```go
"claude", "sonnet", "opus", "haiku" → "anthropic"
"gpt", "o1" → "openai"
"gemini" → "google"
"glm", "zai" → "zhipu"
"llama" → "meta"
".gguf" suffix → "local"
```

## Troubleshooting

### Estimate seems too high/low

The estimator uses conservative ratios with +2% safety margin. If estimates are consistently off:

1. Check provider detection: `getProviderFromModel(model)` should return correct provider
2. Verify pattern detection: Code blocks and special chars affect estimates
3. Consider model-specific differences: Fine-tune ratios if needed

### IsEstimated flag not working

Ensure you're checking the `IsEstimated` field in `TokenCountPayload`:

```go
if payload.IsEstimated {
    // This is an estimate
} else {
    // This is actual from API
}
```

### Two token counts appearing

This is expected behavior! The first count (estimated) appears immediately, then gets replaced by the actual count after the API response.

## References

- Token Counting Experiment: `token-counting-experiment/README.md`
- Analysis Report: `token-counting-experiment/output/token_analysis_report.md`
- Implementation: `headless/sdk/token_estimator.go`
- Integration: `headless/sdk/bridge.go`
- Tests: `headless/sdk/token_estimator_test.go`

---

**Version**: 1.0  
**Added**: 2026-02-04  
**Confidence**: 97.74%  
**Safety Margin**: +2%
