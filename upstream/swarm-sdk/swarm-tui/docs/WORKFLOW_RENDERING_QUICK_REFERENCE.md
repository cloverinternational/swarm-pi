# Workflow Rendering System - Quick Reference

## Files Overview

### Implementation (4 files, ~48KB)
- `workflow_render.go` - Main workflow renderer
- `workflow_action_render.go` - Action/tool rendering
- `workflow_stream_render.go` - Streaming support
- `workflow_render_examples.go` - Usage examples

### Documentation (2 files, ~24KB)
- `WORKFLOW_RENDERING_GUIDE.md` - Complete integration guide
- `WORKFLOW_RENDERING_IMPLEMENTATION_SUMMARY.md` - What was built

### Testing (1 file, ~11KB)
- `workflow_render_test.go` - 20+ unit tests (all passing)

## Core Types

### WorkflowRenderer
Main renderer for complete workflow visualization

```go
renderer := NewWorkflowRenderer(width)
renderer.SetConfig(config)
renderer.SetExpanded(true)
renderer.UpdateAnimFrame(frame)
lines := renderer.RenderWorkflow(state, isStreaming)
```

### WorkflowActionRenderer
Renders individual actions (tools, conditions, approvals)

```go
renderer := NewWorkflowActionRenderer(width)
lines := renderer.RenderAction(action, isStreaming, frame)
```

### WorkflowStreamRenderer
Handles streaming events and live output

```go
renderer := NewWorkflowStreamRenderer(width)
renderer.AddStreamEvent(event)
lines := renderer.RenderStreamingAgentOutput(...)
```

## Key Configuration Options

```go
config := DefaultWorkflowRenderConfig()
config.CollapsedMaxLines = 8        // Compact view
config.ExpandedMaxLines = 50        // Detailed view
config.VerboseMaxLines = 100        // Full view
config.ShowStreamMarker = true      // Pulsing indicator
config.ShowTreeStructure = true     // Hierarchy view
config.ShowAgentMetrics = true      // Token/cost/time
renderer.SetConfig(config)
```

## Supported Rendering Features

✓ Workflow header with status and timing
✓ Progress bar with percentage
✓ Group hierarchy with dependencies
✓ Agent status and metrics
✓ Streaming animations
✓ Tool call rendering with output
✓ Condition/decision rendering
✓ Approval status rendering
✓ Activity log with timestamps
✓ Live metrics (throughput, etc.)
✓ Smart truncation with indicators
✓ Expandable detail levels
✓ Color-coded status
✓ Tree-based visualization
✓ ANSI code preservation

## Rendering Rules Applied

1. **Color Scheme**: Consistent palette
   - Accent for primary
   - Success for completed
   - Error for failed
   - Warning for running
   - TextMuted for background

2. **Content Truncation**: Show last N lines with indicator
3. **Tree Structure**: ├─ ├─ └─ │ connectors
4. **Status Icons**: ✓ ✗ ◐ ○ ⊘ ◆
5. **Box Styling**: Rounded borders, status-aware colors
6. **ANSI Handling**: Preserve codes during wrapping
7. **Caching**: Time-based invalidation
8. **Animation**: Frame-based pulsing

## Integration Checklist

- [ ] Create renderer instance
- [ ] Call UpdateAnimFrame() each render pass
- [ ] Handle window resize with SetWidth()
- [ ] Respond to user input (expand/verbose)
- [ ] Add streaming events as they arrive
- [ ] Test with various terminal widths
- [ ] Verify colors display correctly
- [ ] Check streaming animations smooth

## Performance Targets

- Render time: <5ms for 5+ groups
- Memory per renderer: ~2KB base
- Event buffer: ~50KB for 100 events
- No external dependencies added

## Testing Status

✓ 20+ unit tests all passing
✓ Benchmark functions included
✓ Edge cases covered (nil, empty, etc.)
✓ Complete integration example
✓ No breaking changes

## Usage Example - Minimal

```go
// Create
renderer := NewWorkflowRenderer(120)

// Render each frame
renderer.UpdateAnimFrame(frame)
lines := renderer.RenderWorkflow(workflowState, isStreaming)
for _, line := range lines {
    fmt.Println(line)
}
```

## Usage Example - With Streaming

```go
// Create renderers
wfRenderer := NewWorkflowRenderer(120)
streamRenderer := NewWorkflowStreamRenderer(120)

// Add event
streamRenderer.AddStreamEvent(&WorkflowStreamEvent{
    Type:      "agent_output",
    AgentName: "Agent1",
    Content:   "Processing...",
})

// Render agent output
lines := streamRenderer.RenderStreamingAgentOutput(
    "agent-1", "Agent1", "Group1", true, frame)
```

## Common Patterns

### Expansion/Verbose Toggle
```go
if userPressedE {
    renderer.SetExpanded(!state.IsExpanded)
}
if userPressedV {
    renderer.SetVerbose(!state.IsVerbose)
}
```

### Window Resize
```go
if windowWidthChanged {
    renderer.SetWidth(newWidth)
}
```

### Animation Loop
```go
for frame := 0; frame < 60; frame++ {
    renderer.UpdateAnimFrame(frame)
    lines := renderer.RenderWorkflow(state, isStreaming)
    display(lines)
    time.Sleep(16 * time.Millisecond) // ~60fps
}
```

## Troubleshooting

| Issue | Solution |
|-------|----------|
| Garbled text | Check ANSI code stripping |
| Colors wrong | Verify palette initialized |
| Choppy animation | Ensure UpdateAnimFrame() called |
| Content truncated | Check detail level settings |
| Box misaligned | Use ansi.StringWidth() |

## API Summary

### WorkflowRenderer
- `SetConfig(config)` - Configure rendering
- `SetWidth(width)` - Update display width
- `SetExpanded(bool)` - Toggle detail level
- `SetVerbose(bool)` - Toggle verbose mode
- `UpdateAnimFrame(frame)` - Update animation
- `RenderWorkflow(state, streaming)` - Main render
- `RenderAgentOutput(output, streaming)` - Agent output

### WorkflowActionRenderer
- `SetConfig(config)` - Configure
- `SetWidth(width)` - Update width
- `RenderAction(action, streaming, frame)` - Single action
- `RenderActionList(actions, streaming, frame)` - Timeline
- `RenderActionTree(actions, depths, streaming, frame)` - Tree view

### WorkflowStreamRenderer
- `SetConfig(config)` - Configure
- `SetWidth(width)` - Update width
- `AddStreamEvent(event)` - Buffer event
- `CompleteStreamAgent(agentID)` - Mark done
- `RenderStreamingAgentOutput(...)` - Live output
- `RenderStreamingGroup(...)` - Group progress
- `RenderStreamingEvents(limit)` - Activity log
- `RenderLiveMetrics(time, buffers)` - Metrics

## Next Steps

1. Review `WORKFLOW_RENDERING_GUIDE.md` for detailed documentation
2. Check `workflow_render_examples.go` for usage patterns
3. Run tests: `go test -v ./internal/chat -run "Workflow.*Render"`
4. Integrate into your TUI with feedback from rendering
5. Customize colors/formatting via config

## Support

For questions or issues:
1. Check the integration guide
2. Review example code
3. Look at test cases for patterns
4. Check existing bash/subagent renderers for reference
5. Verify palette colors are initialized

---

**Status**: ✓ Complete and tested  
**Breaking Changes**: None  
**Ready for**: Integration and use  
