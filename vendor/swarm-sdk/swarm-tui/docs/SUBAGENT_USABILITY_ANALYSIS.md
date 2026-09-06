# Subagent Tool Usability Analysis

## Overview
This document analyzes the SubagentTool implementation for potential errors, misuse cases, and usability friction points from an LLM perspective.

## Key Issues Identified

### 1. **Parameter Confusion and Conflicts**
The tool has multiple overlapping ways to configure sub-agents, creating potential for confusion:

- **`agent_id`** - The preferred way to invoke a specific agent
- **`preset`** - Deprecated but still functional (marked as "DEPRECATED: Use agent_id instead")
- **`system_prompt`** - Overrides everything when specified
- **`model`** - Optional model override with fuzzy matching

**Issue**: The LLM might specify conflicting parameters:
- What happens if both `agent_id` AND `preset` are specified?
- What if `agent_id` AND `system_prompt` are both provided?
- The priority order (system_prompt > agent_id > preset) is not clearly communicated in the parameter descriptions

**Recommendation**: Add validation to reject conflicting parameters or clearly document the precedence in the parameter descriptions.

### 2. **Agent Discovery Limitations**
While the tool dynamically generates an enum of available agents, there are several issues:

- **No Agent Descriptions**: The enum only shows agent IDs, not what each agent does
- **Duplicate Agents**: Preset agents appear both as standalone IDs and in the deprecated preset enum
- **Missing Context**: The LLM doesn't know the capabilities of custom agents from settings

**Example Problem**: The LLM sees `["general-assistant", "code-reviewer", "my-custom-agent"]` but has no information about what "my-custom-agent" does.

**Recommendation**: Include agent descriptions in the schema or provide a separate discovery mechanism.

### 3. **Fuzzy Model Matching Pitfalls**
The fuzzy matching for models is helpful but can lead to unexpected behavior:

```go
// If fuzzy matching fails, use the raw input and let provider handle the error
model = modelParam
```

**Issue**: If an LLM provides "claude-4-6-sonnet" (hallucinated model), it passes through to the provider which will fail at runtime rather than providing immediate feedback.

**Recommendation**: Consider validating against known models and providing clearer error messages.

### 4. **Background Execution Complexity**
Two different background modes create confusion:

```go
"run_in_background": {
    "type": "boolean",
    "description": "If true, launch the sub-agent as a background task..."
},
"auto_background_seconds": {
    "type": "integer", 
    "description": "If set, start the task inline but automatically promote to background..."
}
```

**Issue**: The LLM might not understand when to use which mode. The difference between "always background" vs "maybe background" is subtle.

**Recommendation**: Simplify to a single background parameter with clear use cases.

### 5. **Resume Feature Without Discovery**
The `resume` parameter expects an agent_id from a prior run:

```go
"resume": {
    "description": "Optional agent_id of a prior Subagent run to resume..."
}
```

**Issue**: The LLM has no way to discover what prior agent IDs exist or are valid to resume.

**Recommendation**: Either remove this feature or provide a discovery mechanism.

### 6. **Preset as Agent ID Ambiguity**
The code treats presets as agent IDs for backward compatibility:

```go
// Handle preset as agent_id for backward compatibility
if agentID == "" && preset != "" {
    agentID = preset
}
```

**Issue**: This creates confusion - are "code_formatter" in the agent_id enum and "code_formatter" in the preset enum the same thing?

### 7. **Model Override Guidance**
The model parameter description says:
> "Optional model override. Only use when the user explicitly requests a specific model (e.g. 'use sonnet', 'with haiku')"

**Issue**: This puts the burden on the LLM to parse user intent. What about "can you make it faster?" or "use a smarter model"?

**Recommendation**: Provide clearer examples or remove this guidance.

### 8. **Tool Inheritance Complexity**
Sub-agents inherit tools from the parent, but the filtering logic is complex:

```go
// If agent specifies "*", copy all tools (except self-referential ones)
copyAll := len(allowedTools) == 1 && allowedTools[0] == "*"
```

**Issue**: The LLM doesn't know which tools will be available to the sub-agent.

### 9. **Error Messages Lack Context**
When an unknown agent_id is provided:

```go
return nil, sdkerror.Permanent("delegate_task.unknown_agent_id",
    fmt.Sprintf("agent with ID '%s' not found", agentID))
```

**Issue**: The error doesn't suggest valid alternatives or explain why the agent wasn't found.

**Recommendation**: Include available agent IDs in the error message.

### 10. **Result Size Capping**
The tool silently truncates results over 8MB:

```go
const maxSyncResultBytes = 8 * 1024 * 1024 // 8 MB
if len(result) > maxSyncResultBytes {
    // ... truncate ...
}
```

**Issue**: The LLM might not expect truncation and could miss important information.

**Recommendation**: Document this limitation in the tool description.

## Usability Improvements

### 1. **Simplify Parameter Schema**
Remove deprecated parameters and clarify precedence:

```json
{
    "task": "...",
    "agent_id": "... (use this OR system_prompt, not both)",
    "system_prompt": "... (overrides agent_id if specified)",
    "model": "... (optional, only for explicit user requests)"
}
```

### 2. **Add Agent Discovery Tool**
Create a separate tool to list available agents with descriptions:

```
list_available_agents() -> {
    "agents": [
        {
            "id": "code-reviewer",
            "description": "Specialized agent for code review and analysis",
            "tools": ["file_read", "grep", "list_dir"],
            "recommended_for": ["code review", "bug finding", "security analysis"]
        }
    ]
}
```

### 3. **Validate Parameters Early**
Add validation in the Execute method:

```go
if agentID != "" && preset != "" {
    return nil, sdkerror.Permanent("conflicting_parameters", 
        "Cannot specify both agent_id and preset. Use agent_id only.")
}
```

### 4. **Improve Error Context**
Provide helpful errors with suggestions:

```go
if agentNotFound {
    availableAgents := t.getAvailableAgentIDs()
    return nil, sdkerror.Permanent("unknown_agent",
        fmt.Sprintf("Agent '%s' not found. Available agents: %v", 
            agentID, availableAgents))
}
```

### 5. **Document Model Behavior**
Be explicit about model behavior in the description:

```json
"model": {
    "description": "Model override (e.g., 'sonnet', 'gpt-4'). Uses fuzzy matching. Invalid models will fail at runtime. Leave empty to use agent's default model."
}
```

## Conclusion

The SubagentTool is powerful but has several usability issues that could lead to LLM confusion or errors:

1. **Too many ways to configure agents** (agent_id, preset, system_prompt)
2. **Lack of discovery mechanisms** for agents and their capabilities
3. **Silent failures and truncations** without clear documentation
4. **Complex parameter interactions** without clear precedence rules
5. **Ambiguous guidance** about when to use certain features

The tool would benefit from simplification, better discovery mechanisms, and clearer parameter documentation to reduce misuse potential.