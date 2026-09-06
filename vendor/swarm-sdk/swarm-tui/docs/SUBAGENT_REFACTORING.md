# Sub-Agent Invocation Refactoring

## Overview

This document describes the refactoring of the sub-agent invocation system to prioritize agent-based routing over direct model specification, with support for fuzzy model matching when models are explicitly requested.

## Changes Made

### 1. Fuzzy Model Matching Utility

Created `pkg/models/fuzzy_match.go` which provides:
- `FuzzyModelMatch(input string) (string, bool)` - Maps user-friendly model names to canonical identifiers
- Support for common aliases like "sonnet" → "claude-3-5-sonnet-20241022"
- Pattern-based matching for variations like "claude 3.5", "3.5 haiku", etc.
- Direct pass-through for unrecognized model names

### 2. Sub-Agent Tool Refactoring

Updated `tools/builtin/subagent.go` to:
- **Prioritize agent_id parameter**: The tool now prefers agent-based invocation
- **Support built-in agents**: Added definitions for general-assistant, code-reviewer, research-agent, and background-worker
- **Backward compatibility**: Preset names (code_formatter, etc.) can be used as agent_ids
- **Fuzzy model matching**: When a model is explicitly specified, it goes through fuzzy matching
- **Updated parameter schema**: agent_id is now prominently featured with enum values, preset is marked as deprecated

### 3. Invocation Logic

The new priority order is:
1. **Custom system prompt** - Highest priority, creates ad-hoc agent
2. **Agent ID** - Uses pre-configured agent (custom, built-in, or preset)
3. **Default** - Creates generic sub-agent with default configuration

Model selection follows this logic:
- If no model specified: Use agent's configured model or default to haiku
- If model specified: Apply fuzzy matching, then use the matched model

### 4. Key Benefits

- **Agent-first architecture**: Sub-agents are invoked by their role/purpose, not implementation details
- **User-friendly model names**: Users can say "use sonnet" instead of remembering "claude-3-5-sonnet-20241022"
- **Flexibility**: Explicit model overrides are still possible when needed
- **Better defaults**: Each agent type can have its own optimal model configuration

## Usage Examples

### Before (model-based):
```go
params := map[string]interface{}{
    "task": "Review this code",
    "model": "claude-3-5-sonnet-20241022", // Hard to remember
}
```

### After (agent-based with fuzzy matching):
```go
// Option 1: Use a pre-configured agent
params := map[string]interface{}{
    "task": "Review this code",
    "agent_id": "code-reviewer", // Clear intent
}

// Option 2: Override with fuzzy model name
params := map[string]interface{}{
    "task": "Review this code", 
    "agent_id": "code-reviewer",
    "model": "sonnet", // Fuzzy matched to full name
}

// Option 3: Just specify a model (backward compatible)
params := map[string]interface{}{
    "task": "General task",
    "model": "haiku", // Fuzzy matched
}
```

## Testing

Created comprehensive tests in:
- `pkg/models/fuzzy_match_test.go` - Tests fuzzy matching logic
- `tools/builtin/subagent_simple_test.go` - Tests parameter schema and integration

All tests pass successfully, confirming the implementation works as designed.