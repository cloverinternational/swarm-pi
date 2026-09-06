# Sub-agent Output Formatting Implementation

## Summary
This document describes the implementation of improved output formatting for sub-agents in the Swarm TUI.

## Problem
Sub-agent outputs were displayed as raw text, including JSON responses, making them difficult to read. The main agent's outputs have nice formatting but sub-agents didn't benefit from this.

## Solution
Created a dedicated `SubagentRenderer` in the TUI's tool rendering system that:

1. **Detects structured output formats**:
   - JSON objects and arrays
   - TOON (Tool Output Notation) format
   - SubagentOutput tool's structured format

2. **Applies syntax highlighting**:
   - JSON keys in green
   - String values in cyan/blue
   - Numbers in orange
   - Booleans in yellow
   - Null values in muted gray

3. **Handles different output types**:
   - Pretty-prints JSON with proper indentation
   - Preserves TOON formatting
   - Formats SubagentOutput headers nicely
   - Falls back to plain text for unstructured output

## Implementation Details

### File Structure
- `/swarm-tui/internal/chat/toolrender/subagent/renderer.go` - The main renderer implementation
- `/swarm-tui/internal/chat/app_init.go` - Registration in the tool registry

### Key Methods
- `CanRender()` - Returns true for "Subagent" and "SubagentOutput" tools
- `Render()` - Main rendering logic with format detection
- `renderJSON()` - Pretty-prints and highlights JSON
- `renderSubagentOutputStructured()` - Handles SubagentOutput tool format
- `renderTOON()` - Preserves TOON formatting

### Integration
The renderer is registered in the app initialization before the generic fallback renderer:
```go
app.toolRegistry.Register(subagentrender.New())
```

## Design Decisions

### Why Not Add Synchronous Wait to SubagentOutput?
We considered adding a "wait" parameter to SubagentOutput that would block until the agent completes, but decided against it because:

1. **Tool timeout violations**: If a sub-agent runs longer than the tool's timeout_seconds, it would cause a timeout error
2. **User intent**: When users set `run_in_background=true`, they explicitly want async execution
3. **Separation of concerns**: The current design is correct:
   - `Subagent` tool decides whether to run sync or async based on parameters
   - `SubagentOutput` tool is for checking/streaming from background agents
   - This separation allows for proper timeout handling via `auto_background_seconds`

### Format Detection
The renderer uses simple heuristics to detect formats:
- JSON: Starts with `{` or `[` and ends with `}` or `]`
- TOON: Contains box-drawing characters like `│`, `├`, `└`, `─`
- Structured: Contains header lines with `key: value` format

## Future Improvements
1. Add support for YAML formatting
2. Enhance TOON rendering with color preservation
3. Add configuration for color schemes
4. Support for custom output formats via metadata

## Testing
To test the formatting, run a sub-agent that returns JSON:
```
Subagent task="Return a JSON object with status, message, and data fields"
```

The output should be pretty-printed with syntax highlighting instead of appearing as raw text.