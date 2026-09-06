# Subagent Tool Improvement Recommendations

## Quick Wins (High Impact, Low Effort)

### 1. **Add Parameter Validation**
```go
// In Execute method, add early validation:
if agentID != "" && preset != "" {
    return nil, sdkerror.Permanent("delegate_task.conflicting_params",
        "Cannot specify both 'agent_id' and 'preset'. The 'preset' parameter is deprecated - use 'agent_id' instead.")
}

if systemPrompt != "" && agentID != "" {
    // Log warning but allow (since system_prompt takes precedence)
    t.logger.Warn(ctx, "delegate_task.system_prompt_overrides",
        observability.Field{Key: "info", Value: "system_prompt specified - will override agent_id"})
}

if autoBackgroundSecs < 0 {
    return nil, sdkerror.Permanent("delegate_task.invalid_timeout",
        "auto_background_seconds must be positive")
}
```

### 2. **Improve Error Messages**
```go
// When agent not found:
availableAgents := []string{}
for id := range t.customAgentDefs {
    availableAgents = append(availableAgents, id)
}
availableAgents = append(availableAgents, 
    "general-assistant", "code-reviewer", "research-agent", 
    "background-worker", "code_formatter", "text_summarizer",
    "data_validator", "error_analyzer", "question_answerer")

return nil, sdkerror.Permanent("delegate_task.unknown_agent_id",
    fmt.Sprintf("Agent '%s' not found. Available agents: %v", 
        agentID, strings.Join(availableAgents, ", ")))
```

### 3. **Update Parameter Descriptions**
```go
"agent_id": map[string]interface{}{
    "type":        "string",
    "enum":        allAgentIDs,
    "description": "The agent to invoke. Cannot be used with 'preset' or 'system_prompt'. Available agents: general-assistant (multipurpose), code-reviewer (code analysis), research-agent (exploration), background-worker (long tasks), etc.",
},

"model": map[string]interface{}{
    "type":        "string", 
    "description": "Optional model override. Examples: 'sonnet', 'haiku', 'gpt-4'. Uses fuzzy matching (e.g., 'sonnet' → 'claude-3-5-sonnet-20241022'). Invalid models will fail at agent startup.",
},
```

## Medium-Term Improvements

### 1. **Simplify Background Execution**
Replace two background parameters with one:

```go
"background_mode": map[string]interface{}{
    "type": "string",
    "enum": []string{"sync", "async", "auto"},
    "description": "Execution mode: 'sync' (wait for completion), 'async' (return immediately), 'auto' (promote to background after timeout)",
},
"background_timeout_seconds": map[string]interface{}{
    "type":        "integer",
    "description": "For 'auto' mode: seconds before promoting to background (default: 30)",
},
```

### 2. **Add Agent Capability Hints**
Include agent capabilities in the parameter schema:

```go
// In Parameters() method:
"agent_id": map[string]interface{}{
    "type":        "string",
    "enum":        allAgentIDs,
    "description": "The agent to invoke",
    "x-agent-info": map[string]interface{}{
        "general-assistant": "Multipurpose AI for general tasks",
        "code-reviewer":     "Code analysis, bug finding, security review", 
        "research-agent":    "Codebase exploration and information gathering",
        // ... etc
    },
},
```

### 3. **Remove Deprecated Parameters**
Phase out the `preset` parameter entirely:

```go
// Log deprecation warning if preset is used
if preset != "" && agentID == "" {
    t.logger.Warn(ctx, "delegate_task.deprecated_preset",
        observability.Field{Key: "preset", Value: preset},
        observability.Field{Key: "suggestion", Value: fmt.Sprintf("Use agent_id='%s' instead", preset)})
    agentID = preset
}
```

## Long-Term Architecture Changes

### 1. **Agent Discovery Service**
Create a separate tool for agent discovery:

```go
type ListAgentsToolResult struct {
    Agents []AgentInfo `json:"agents"`
}

type AgentInfo struct {
    ID           string   `json:"id"`
    Name         string   `json:"name"`
    Description  string   `json:"description"`
    Capabilities []string `json:"capabilities"`
    Tools        []string `json:"tools"`
    DefaultModel string   `json:"default_model"`
}
```

### 2. **Structured Parameter Validation**
Use a proper schema validation library:

```go
type SubagentParams struct {
    Task                   string  `json:"task" validate:"required"`
    AgentID               string  `json:"agent_id" validate:"required_without=SystemPrompt"`
    SystemPrompt          string  `json:"system_prompt" validate:"required_without=AgentID"`
    Model                 string  `json:"model" validate:"omitempty,model_name"`
    RunInBackground       bool    `json:"run_in_background"`
    AutoBackgroundSeconds int     `json:"auto_background_seconds" validate:"omitempty,min=1"`
}
```

### 3. **Result Streaming for Large Outputs**
Instead of truncating at 8MB, implement streaming:

```go
type SubagentStreamResult struct {
    AgentID      string `json:"agent_id"`
    Status       string `json:"status"`
    PartialResult string `json:"partial_result"`
    BytesTotal    int    `json:"bytes_total"`
    BytesReturned int    `json:"bytes_returned"`
    StreamToken   string `json:"stream_token"` // For fetching more
}
```

## Implementation Priority

1. **Immediate** (This Week):
   - Parameter validation for conflicts
   - Better error messages with available agents
   - Update parameter descriptions

2. **Short-term** (Next Sprint):
   - Simplify background execution modes
   - Add capability hints to schema
   - Deprecation warnings for preset

3. **Medium-term** (Next Month):
   - Agent discovery tool
   - Structured validation
   - Remove deprecated parameters

4. **Long-term** (Next Quarter):
   - Result streaming
   - Full agent capability API
   - Dynamic agent registration

## Testing Recommendations

1. **Add LLM Simulation Tests**: Create tests that simulate common LLM mistakes
2. **Parameter Combination Matrix**: Test all valid/invalid parameter combinations
3. **Error Message Clarity**: Verify error messages provide actionable guidance
4. **Performance Tests**: Ensure schema generation doesn't impact performance

## Success Metrics

- Reduction in invalid tool calls by 80%
- Elimination of "agent not found" errors through better discovery
- 50% reduction in parameter-related support questions
- Zero instances of silent truncation surprises