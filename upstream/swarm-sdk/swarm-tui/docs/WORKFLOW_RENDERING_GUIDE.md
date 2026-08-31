# Workflow Rendering System - Integration Guide

## Overview

The new workflow rendering system (`workflow_render.go`, `workflow_action_render.go`, `workflow_stream_render.go`) provides comprehensive, streaming-aware rendering for workflow execution in the TUI. It follows the same patterns and best practices as the existing bash terminal, subagent, and streaming renderers.

## Architecture

### Three-Tier Rendering System

```
1. WorkflowRenderer (workflow_render.go)
   ├─ Renders complete workflow execution state
   ├─ Manages group hierarchy and layers
   ├─ Handles progress tracking and metrics
   └─ Coordinates with action and stream renderers

2. WorkflowActionRenderer (workflow_action_render.go)
   ├─ Renders individual actions/tools
   ├─ Handles tool calls with streaming output
   ├─ Renders conditions, approvals, decisions
   └─ Provides action timeline/tree views

3. WorkflowStreamRenderer (workflow_stream_render.go)
   ├─ Buffers streaming events
   ├─ Renders live agent output
   ├─ Shows real-time metrics and activity logs
   └─ Provides smooth animations and transitions
```

## Key Design Principles

### 1. **Unified Visual Language**
- Uses consistent palette colors across all renderers
- Tool icons and status indicators match bash/subagent renderers
- Tree connectors (`├─`, `└─`, `│`) for hierarchy visualization
- Box styling with rounded borders and colors indicating state

### 2. **Streaming-First Design**
- All renderers support live streaming with animations
- PulseText indicators show ongoing work
- Content accumulates smoothly without jarring updates
- Progress indicators update in real-time

### 3. **Intelligent Truncation**
- Respects width constraints while preserving readability
- Shows last N lines when truncated (tail behavior)
- Provides visual indicators for hidden content ("↑ +N earlier lines")
- Expandable via UI controls (not hardcoded)

### 4. **Hierarchical Rendering**
- Groups organized into execution layers (dependency-aware)
- Agents nested under groups with proper indentation
- Actions shown as timeline or tree depending on workflow type
- Dependency visualization for complex workflows

### 5. **Animation and Polish**
- Pulsing/fading effects for "working" states
- Smooth frame-by-frame animation support
- Color transitions based on status
- Interactive controls (expand/collapse, detail levels)

## Core Components

### WorkflowRenderConfig

Configures all rendering behavior:

```go
type WorkflowRenderConfig struct {
    // Display levels
    CollapsedMaxLines int       // Default: 8
    ExpandedMaxLines  int       // Default: 50
    VerboseMaxLines   int       // Default: 100

    // Streaming indicators
    ShowStreamMarker   bool     // Show pulsing "working"
    StreamMarkerText   string   // "working", "executing", etc.
    StreamMarkerSpeed  float64  // Animation speed

    // Truncation
    ShowTruncationInfo  bool
    TruncationIndicator string  // "↑ +%d earlier lines"

    // Visual options
    ShowTreeStructure      bool  // Render as hierarchy tree
    ShowGroupDependencies  bool  // Show dependency arrows
    ShowAgentMetrics       bool  // Token/cost/duration

    // Formatting
    MaxToolPreviewLen  int   // For parameter preview
    MaxPathLen         int   // For file paths
    MaxCommandLen      int   // For commands
    WrapWidth          int   // Context-aware wrapping

    UseColors bool  // Enable colors
    ShowIcons bool  // Requires nerd font
}
```

### WorkflowRenderState

Tracks UI state during rendering:

```go
type WorkflowRenderState struct {
    IsExpanded  bool  // User expanded view
    IsVerbose   bool  // Verbose mode active
    SelectedIdx int   // For interactive selection
    AnimFrame   int   // Current animation frame
    ScrollOffset int  // For scrolling through content
}
```

## Integration Patterns

### Pattern 1: Basic Workflow Rendering

```go
// Create renderer
renderer := NewWorkflowRenderer(width)
renderer.SetConfig(config)

// Render complete workflow
lines := renderer.RenderWorkflow(workflowState, isStreaming)

// Update animation frame (call every render frame)
renderer.UpdateAnimFrame(frame)
```

### Pattern 2: Streaming Events

```go
// Create streaming renderer
streamRenderer := NewWorkflowStreamRenderer(width)

// Add events as they arrive
streamRenderer.AddStreamEvent(&WorkflowStreamEvent{
    Type:      "agent_output",
    AgentName: "Agent1",
    Content:   "Processing data...",
    Status:    "info",
})

// Render live agent output
lines := streamRenderer.RenderStreamingAgentOutput(
    agentID, agentName, groupName,
    isStreaming, animFrame,
)
```

### Pattern 3: Action Rendering

```go
// Create action renderer
actionRenderer := NewWorkflowActionRenderer(width)

// Render a tool action
lines := actionRenderer.RenderAction(action, isStreaming, animFrame)

// Or render a timeline of actions
lines := actionRenderer.RenderActionList(actions, isStreaming, animFrame)
```

### Pattern 4: Interactive Display

```go
// Toggle expansion
if userPressedE {
    renderer.SetExpanded(!currentState.IsExpanded)
}

// Toggle verbose mode
if userPressedV {
    renderer.SetVerbose(!currentState.IsVerbose)
}

// Update width on window resize
if widthChanged {
    renderer.SetWidth(newWidth)
}
```

## Rendering Rules & Tricks

### 1. **Color Palette Usage**

Match the existing palette from `palette.go`:

```go
palette.Accent        // Primary action/highlight
palette.Success       // Completed/success states
palette.Error         // Errors/failures
palette.Warning       // Running/in-progress
palette.Info          // Informational
palette.TextDim       // Slightly visible text
palette.TextMuted     // Very dim/background text
palette.Border        // Box borders
```

### 2. **Status Icons**

Keep consistent with other renderers:

```
✓ - Success/Completed
✗ - Failed/Error
◐ - Running/In Progress
○ - Pending/Not Started
⊘ - Skipped
⊕ - Decision/Branch
◆ - Event/Marker
▸ - Output/Content
```

### 3. **Tree Connectors**

Use for hierarchical structure:

```
├─ Middle item
└─ Last item
│  Vertical continuation
```

### 4. **ANSI Width Handling**

Use `ansi.StringWidth()` from charmbracelet for width calculations that ignore escape codes.

### 5. **Wrapping Long Content**

Apply these wrapping rules:

```go
1. Calculate content width as: totalWidth - 4 (borders/padding)
2. Truncate with "..." when exceeding maxLen
3. For paths: show filename, truncate directory
4. For commands: show prefix, indicate truncation
5. For output: wrap at word boundaries
```

### 6. **Streaming Animations**

Use `RenderPulseTextWithDots()` from palette utilities:

```go
pulseConfig := PulseTextConfig{
    BaseColor:    palette.Accent,
    DimColor:     palette.TextMuted,
    PulseSpeed:   0.15,        // Update rate
    MinIntensity: 0.3,         // Minimum opacity
    MaxIntensity: 1.0,         // Maximum opacity
}
marker := RenderPulseTextWithDots("working", animFrame, pulseConfig)
```

### 7. **Truncation Indicators**

Show what was hidden:

```
Before truncation: "↑ +12 earlier lines"
After expansion: show full content with marker "expanded"
```

### 8. **Box Rendering**

Use lipgloss consistently:

```go
boxStyle := lipgloss.NewStyle().
    Width(contentWidth).
    Border(lipgloss.RoundedBorder()).
    BorderForeground(lipgloss.Color(borderColor)).
    Padding(0, 1)

boxed := boxStyle.Render(content)
```

## Performance Considerations

### 1. **Caching**

The workflow renderer includes a simple cache:

```go
// Cache key: "tree_groupID_depth"
if cached, ok := r.groupRenderCache[cacheKey]; ok {
    if time.Since(r.lastCacheTime) < 100*time.Millisecond {
        return cached
    }
}

// Cache is cleared on:
// - Width changes
// - Expansion state changes
// - Verbose mode changes
```

### 2. **Event Buffer Management**

Stream buffer keeps only recent events:

```go
// Bounded to maxEvents (default 100)
if len(b.events) > b.maxEvents {
    b.events = b.events[len(b.events)-b.maxEvents:]
}
```

### 3. **Lazy Rendering**

Don't render entire workflow every frame:

```go
// Only render:
// 1. When events arrive (streaming)
// 2. When user interaction occurs
// 3. On animation frame changes (every ~16ms)
// 4. On window resize
```

## Common Patterns from Other Renderers

### From bash_terminal_render.go

1. **Terminal-style boxes** with thick borders
2. **ANSI code preservation** during wrapping
3. **Status indicators** (running, success, error)
4. **Tree connectors** for hierarchical display

### From subagent_render.go

1. **Tool-specific colors** for visual differentiation
2. **Content blocks** (thinking, output, tool calls)
3. **Compact vs verbose modes** for truncation
4. **Metric display** (token count, duration)
5. **Tree rendering** with connectors and indentation

### From subagent_stream.go

1. **Streaming markers** with pulsing animation
2. **Truncation with indicators** (show N more lines)
3. **Frame-based animation** (call UpdateAnimFrame)
4. **Config-driven rendering** (StreamConfig pattern)

## Migration Guide

### If Moving from Existing Workflow UI:

1. **Create renderer:**
   ```go
   renderer := NewWorkflowRenderer(screenWidth)
   ```

2. **Update on each frame:**
   ```go
   renderer.UpdateAnimFrame(frameNumber)
   lines := renderer.RenderWorkflow(state, isStreaming)
   ```

3. **Handle user input:**
   ```go
   case 'e': renderer.SetExpanded(!state.IsExpanded)
   case 'v': renderer.SetVerbose(!state.IsVerbose)
   ```

4. **Stream events:**
   ```go
   streamRenderer.AddStreamEvent(event)
   streamRenderer.CompleteStreamAgent(agentID)
   ```

## Testing & Validation Checklist

- [ ] Rendering works at various terminal widths (40, 80, 120, 160 chars)
- [ ] All status colors display correctly
- [ ] Streaming animations are smooth (no jitter)
- [ ] Truncation indicators appear correctly
- [ ] Box borders render properly
- [ ] No ANSI code escaping issues
- [ ] Performance acceptable with 10+ groups
- [ ] Expansion/collapse works smoothly
- [ ] Scrolling doesn't break rendering
- [ ] Agent output streams without buffering issues

## Troubleshooting

### Issue: Text appears garbled or colors wrong
- **Solution:** Check palette colors are initialized
- **Check:** `palette.Accent`, etc. are defined

### Issue: Boxes don't align
- **Solution:** Use `ansi.StringWidth()` for all width calculations
- **Check:** Width excludes escape codes

### Issue: Streaming feels choppy
- **Solution:** Ensure `UpdateAnimFrame()` called every render
- **Check:** AnimFrame increments smoothly

### Issue: Content truncated unexpectedly
- **Solution:** Check `CollapsedMaxLines` vs `ExpandedMaxLines`
- **Check:** `SetExpanded()` is being called

## API Reference

### WorkflowRenderer

```go
// Configuration
SetConfig(config WorkflowRenderConfig)
SetWidth(width int)
SetExpanded(expanded bool)
SetVerbose(verbose bool)
UpdateAnimFrame(frame int)

// Rendering
RenderWorkflow(state *WorkflowChatState, isStreaming bool) []string
RenderAgentOutput(output *WorkflowAgentOutput, isStreaming bool) []string

// Internals (helpers)
GetStatusColor(status interface{}) string
BuildGroupLayers(groups []*WorkflowGroupState) [][]string
```

### WorkflowActionRenderer

```go
// Configuration
SetConfig(config WorkflowRenderConfig)
SetWidth(width int)

// Rendering
RenderAction(action *WorkflowActionDisplay, isStreaming bool, animFrame int) []string
RenderActionList(actions []*WorkflowActionDisplay, isStreaming bool, animFrame int) []string
RenderActionTree(actions []*WorkflowActionDisplay, depths map[string]int, isStreaming bool, animFrame int) []string
```

### WorkflowStreamRenderer

```go
// Event Management
AddStreamEvent(event *WorkflowStreamEvent)
CompleteStreamAgent(agentID string)
GetStreamEvent(limit int) []*WorkflowStreamEvent

// Rendering
RenderStreamingAgentOutput(agentID, agentName, groupName string, isStreaming bool, animFrame int) []string
RenderStreamingGroup(groupID, groupName string, agentCount, completedAgents int, isStreaming bool, animFrame int) []string
RenderStreamingEvents(limit int) []string
RenderLiveMetrics(startTime time.Time, agentBuffers map[string]*AgentStreamingBuffer) []string
RenderStreamingBox(title string, content []string, isStreaming bool, statusIcon string) []string
```

## Best Practices

1. **Always call `UpdateAnimFrame()`** for every render pass
2. **Cache renderer instances** (don't create new each frame)
3. **Use config to customize** instead of modifying renderers
4. **Handle width changes** by calling `SetWidth()`
5. **Test with various terminal sizes** (40-200+ chars)
6. **Monitor performance** with many groups/agents (10+)
7. **Use palette colors** consistently across your UI
8. **Preserve ANSI codes** when wrapping text
9. **Provide user control** for expansion/verbose modes
10. **Stream events efficiently** (batch if possible)

## Future Enhancements

- [ ] Interactive group/agent selection
- [ ] Zoom levels for different detail scales
- [ ] Custom filtering (show only errors, running, etc.)
- [ ] Export to JSON/HTML for reporting
- [ ] Performance metrics visualization
- [ ] Workflow timeline with start/end markers
- [ ] Error recovery suggestions
- [ ] Parallel execution visualization
