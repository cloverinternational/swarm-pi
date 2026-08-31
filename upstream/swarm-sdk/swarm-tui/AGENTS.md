# AGENTS.md - TUI Agent Development Guide

Guide for AI agents working on the SwarmCode TUI codebase. Covers tool development, rendering, and integration patterns.

---

## Monorepo Path Mapping

In the monorepo, the SDK lives at `../swarm-sdk` relative to `swarm-tui`.
Any path in this document that starts with `sdk/` should be read as
`../swarm-sdk/` (or `swarm-sdk/` from the repo root).

## Adding a New Tool (End-to-End)

This is the complete, ordered process for adding a new tool to the SDK and making it usable in the TUI. Every step is required — skipping any step will result in the tool being silently broken (registered but not rendered, or rendered but not callable).

### Step 1: Implement the Tool in the SDK

**Location:** `../swarm-sdk/tools/` — pick the right subpackage:
- `../swarm-sdk/tools/builtin/` — core tools (bash, grep with apply_patch format)
- `../swarm-sdk/tools/ii/` — ii-agent ported tools (ReadLegacy, Write, EditLegacy with str_replace, Grep)
- `../swarm-sdk/tools/swarmtools/` — hashline tools (Read, Edit — primary file tools using hash-verified format)
- Create a new subpackage for fundamentally new tool categories

**Tool naming convention:** The primary Read and Edit tools use hashline format (`lineNum:hash|content`). Legacy tools are suffixed with "Legacy" (e.g., `ReadLegacy`, `EditLegacy`).

**Required interface:** Every tool must implement `tools.Tool` from `../swarm-sdk/tools/tool.go`:

```go
type Tool interface {
    Name() string                    // Unique identifier, e.g. "Read"
    Description() string             // LLM-facing description (detailed, 3-4+ sentences)
    Parameters() interface{}         // JSON Schema for input validation
    Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error)
    Validate(params map[string]interface{}) error
    IsIdempotent() bool              // true for read-only tools (enables caching)
    RequiresPermission() []Permission // e.g. PermissionFileRead, PermissionFileWrite
    SupportedContentTypes() []ContentType
    OptimizationHints() *OptimizationHints // nil is fine
}
```

**Optional interfaces:**
- `ParallelCapable` — implement `SupportsParallel() bool` for read-only tools
- `StreamingTool` — implement `ExecuteStreaming()` for incremental output
- `ToolWithExamples` — implement `InputExamples()` for complex schemas

**ii-agent tools** also implement `IITool` (defined in `../swarm-sdk/tools/ii/base.go`):
```go
type IITool interface {
    tools.Tool
    DisplayName() string
    IsReadOnly() bool
    ShouldConfirmExecute(params map[string]any) *ConfirmationDetails
    Metadata() map[string]any
}
```

**Key patterns:**
- Use `WorkspaceManager` for path validation (boundary checking)
- Use `sdkerror.Permanent()` for invalid input, `sdkerror.Transient()` for retriable errors
- Return `tools.NewToolResult(output)` for success
- Check `ctx.Done()` before expensive operations
- Parameters come as `map[string]interface{}` — numbers from JSON are `float64`

**File naming:** `../swarm-sdk/tools/<package>/<tool_name>.go` (e.g. `hashline_read.go`)

### Step 2: Write Tests

**Location:** Same package as the tool, `*_test.go` suffix.

Test at minimum:
- Valid execution with expected output
- Invalid/missing parameters
- Edge cases (empty files, large files, boundary conditions)
- Error conditions (file not found, permission denied)
- For edit tools: verify file content after modification

Run: `cd ../swarm-sdk && go test ./tools/<package>/... -v`

### Step 3: Register the Tool in TUI

There are **two registration points** plus **several auxiliary integration points** — all must be updated or the tool will be silently broken:

#### 3a. TUI Chat (interactive mode)

**File:** `internal/chat/sdk_integration.go`

1. **Add import:**
```go
import (
    "github.com/Swarm-Code/mono/swarm-sdk/tools/<package>"
)
```

2. **Register tools** in `NewSDKIntegrationWithOptions()`, after the existing tool registration blocks (around line 488-505). The `iiWorkspaceManager` is created earlier in the function and is available:

```go
// Register <your> tools
if iiWorkspaceManager != nil {
    myTools := []tools.Tool{
        mypkg.NewMyTool(iiWorkspaceManager),
    }
    for _, tool := range myTools {
        if err := toolRegistry.Register(tool); err != nil {
            logger.Warn(context.Background(), "failed to register tool",
                observability.Field{Key: "tool", Value: tool.Name()},
                observability.Field{Key: "error", Value: err.Error()})
        }
    }
}
```

#### 3b. Headless IPC Server

**File:** `headless/cmd/ipc-server/main.go`

1. **Add import** (same as above)
2. **Register tools** in `registerAllTools()` function (around line 1110-1115):

```go
// Register <your> tools
if wm != nil {
    myTools := []tools.Tool{
        mypkg.NewMyTool(wm),
    }
    for _, tool := range myTools {
        if err := registry.Register(tool); err != nil {
            logDebug("Warning: Failed to register tool %s: %v", tool.Name(), err)
        }
    }
}
```

**Important:** The agent definition uses `Tools: []string{"*"}` (line ~706 in sdk_integration.go), so all registered tools are automatically sent to the LLM. You do NOT need to modify the agent definition.

#### 3c. Operating Mode Whitelist (CRITICAL)

**File:** `sdk/mode/builtin.go`

**This is the #1 cause of "tools registered but LLM never calls them".** PLAN mode uses an explicit `AllowedTools` whitelist with `HideBlockedTools: true`. If your tool isn't in this list, it is silently filtered out of the API request — the LLM never sees it.

Add your tool to `PlanMode.AllowedTools`:
```go
AllowedTools: []string{
    // ... existing tools ...
    "MyNewTool", // Add here for PLAN mode visibility
},
```

ACT, AUTO, DEBUG, and OFF modes use `AllowedTools: ["*"]` (all tools), so they don't need modification. Only PLAN mode has a restrictive whitelist.

**Mode filtering flow:**
1. `sdk_integration_execution.go:545` — passes `mode_filter` to agent context (skipped for OFF/ACT)
2. `agent/agent_execute.go:887-955` — iterates registered tools, calls `isToolAllowedByModeFilter()` for each
3. If `HideBlockedTools: true`, blocked tools are excluded from the API request entirely

#### 3d. Auxiliary TUI Integration Points

These files must **also** be updated or the tool will be partially broken (registered and callable, but with broken rendering, missing icons, or invisible in UI):

| File | What to add | Why |
|------|-------------|-----|
| `internal/chat/chat_state.go` | Add to `iiToolsMap` and `toolCategoriesMap` | Identifies the tool as an internal tool and categorizes it (e.g., "productivity", "filesystem") |
| `internal/chat/tool_renderer.go` | Add to `ToolIcon()` and `ToolIconFallback()` | Provides nerd font and fallback icons for the tool header |
| `internal/chat/render_config.go` | Add `ToolRenderConfig` entry in `DefaultRenderSettings()` | Controls truncation, display mode, and `AlwaysShowFull` behavior |
| `internal/chat/app_conversations_messages.go` | Add to icon switch in `renderToolCallPreview()` | Shows correct icon in conversation history/replay view |
| `internal/chat/settings/system_prompt.go` | Reference tool in the system prompt | Guides the LLM to use the tool appropriately |
| `internal/chat/settings/mcp.go` | Add to `memoryTools` exclusion list (if internal) | Prevents the tool from being listed as an MCP tool |
| `internal/chat/app_sdk_helpers.go` | Add to `allTools` list in `toggleAgentPermission()` | Ensures the tool appears in workflow permission management |
| `internal/chat/workflow_manager.go` | Add aliases (e.g., `"my_tool": "MyTool"`) | Maps snake_case variants to the canonical tool name |
| `internal/chat/workflow_editor_tools.go` | Add description entry | Shows tool description in the workflow editor |

#### 3e. Compaction Integration (for stateful tools)

If your tool manages persistent state (like TodoManager), it must survive context compaction:

| File | What to do |
|------|------------|
| `sdk/compaction/compaction.go` | Add state fields to the compaction structs |
| `internal/chat/app_compaction.go` | Bridge from SDK state to compaction structs |
| `headless/core/engine_compaction.go` | Bridge from SDK state to headless compaction |
| `headless/core/state.go` | Add fields to headless state structs |

### Step 4: Add Tool Rendering

Without rendering config, the tool output will either be invisible or use the generic fallback renderer (plain text, truncated to 10 lines). There are **four rendering touchpoints**:

#### 4a. Tool Renderer Registry (CanRender matching)

Tools are matched to renderers via `CanRender()` — the registry iterates renderers in priority order and the first match wins.

**Existing renderers** (in priority order, registered in `internal/chat/app_init.go` lines 457-466):
1. `toolrender/bash/` — terminal box renderer
2. `toolrender/read/` — syntax-highlighted file content
3. `toolrender/edit/` — unified diff view
4. `toolrender/patch/` — V4A patch format
5. `toolrender/grep/` — search results with file headers
6. `toolrender/websearch/` — web search result cards
7. `toolrender/todo/` — task items with status icons
8. `toolrender/generic/` — fallback (plain text)

**To use an existing renderer**, add your tool name to its `CanRender()` switch statement:

```go
// In toolrender/read/renderer.go CanRender():
switch name {
case "Read", "read", "ReadFile", "read_file", "MyNewReadTool":
    return true
}
```

**To create a new renderer**, implement the `Renderer` interface from `toolrender/types.go`:
```go
type Renderer interface {
    CanRender(ctx *RenderContext) bool
    Render(ctx *RenderContext, cached CachedResult) []string
    PreProcess(ctx *RenderContext) CachedResult
}
```
Then register it in `internal/chat/app_init.go`:
```go
app.toolRegistry.Register(myrender.New())  // Before SetFallback
```

#### 4b. Render Config (truncation rules)

**File:** `internal/chat/render_config.go`

Add a `ToolRenderConfig` entry in `DefaultRenderSettings()`. Two categories:

**Read-only tools** (truncate for readability):
```go
settings.ToolConfigs["MyReadTool"] = &ToolRenderConfig{
    Name:           "MyReadTool",
    DisplayMode:    DisplayCompact,
    MaxLines:       20,
    ShowParams:     true,
    AlwaysShowFull: false,
}
```

**Write/modify tools** (always show full output):
```go
settings.ToolConfigs["MyEditTool"] = &ToolRenderConfig{
    Name:           "MyEditTool",
    DisplayMode:    DisplayCompact,
    MaxLines:       0,
    ShowParams:     true,
    AlwaysShowFull: true, // Users must see exactly what changed
}
```

#### 4c. Tool Icons

**File:** `internal/chat/tool_renderer.go`

Add entries to both icon maps in `ToolIcon()` (nerd fonts) and `ToolIconFallback()` (emoji):

```go
// In ToolIcon():
"MyReadTool": "󰈔",  // File read icon
"MyEditTool": "󰏫",  // Edit icon

// In ToolIconFallback():
"MyReadTool": "◇",
"MyEditTool": "◆",
```

Common nerd font icons:
- `"󰈔"` — file read
- `"󰏫"` — edit/pencil
- `"󰎞"` — file write
- `"󰍉"` — search
- `"󰆍"` — terminal
- `"󰖟"` — globe/web
- `"󰄲"` — checklist
- `"󰊕"` — tool/wrench (default)

#### 4d. IsEditTool (for edit tools only)

**File:** `internal/chat/tool_renderer.go`

If your tool modifies files, add it to `IsEditTool()`:
```go
editTools := map[string]bool{
    // ...existing entries...
    "MyEditTool": true,
}
```

### Step 5: Build and Verify

```bash
# SDK tests
cd ../swarm-sdk && go test ./tools/<package>/... -v

# TUI builds (all three must pass)
cd .. && go build ./internal/chat/...
go build ./headless/cmd/ipc-server/...
go build ./cmd/...
```

**Pre-existing build issues to ignore:**
- `tests/runner.go: main redeclared` — duplicate main in test directory
- `no non-test Go files in .../tests/...` — test-only packages

---

## Architecture Quick Reference

### SDK Ring Architecture

| Ring | Package | Contents |
|------|---------|----------|
| 0 | `sdk/tools/` | Pure interfaces: `Tool`, `Registry`, `Permission`, `ContentType` |
| 1 | `sdk/tools/registry_impl.go` | `SimpleRegistry` implementation |
| 2 | `sdk/tools/builtin/`, `sdk/tools/ii/`, `sdk/tools/swarmtools/` | Concrete tool implementations |

### Tool Execution Flow

```
LLM decides to call tool
    ↓
agent.executeTools() — sdk/agent/agent_tools.go
    ↓
toolReg.Get(toolName) — checks Enabled flag
    ↓
tool.Validate(params) — pre-execution check
    ↓
permissionChecker.CheckWithContext() — enforce permissions
    ↓
hooksManager.EmitToolBeforeExecute() — pre-tool hooks
    ↓
tool.Execute(ctx, params) — actual execution
    ↓
hooksManager.EmitToolAfterExecute() — post-tool hooks
    ↓
ToolResult returned to agent loop
```

### Tool Rendering Flow

```
Tool completes → ToolResult stored in Message
    ↓
PreProcess phase (app_chat_render.go ~line 1722):
  toolRegistry.PreProcess(ctx) → finds matching Renderer → caches result
    ↓
Render phase (app_chat_render.go ~line 1341):
  toolRegistry.Render(ctx, cached) → returns styled ANSI lines
    ↓
Truncation phase (app_chat_render.go ~lines 1343-1358):
  Apply ToolRenderConfig limits (MaxLines, AlwaysShowFull)
    ↓
Lines displayed in TUI
```

### Key Files

| File | Purpose |
|------|---------|
| `sdk/tools/tool.go` | Tool interface definition |
| `sdk/tools/result.go` | ToolResult, NewToolResult(), NewErrorResult() |
| `sdk/tools/ii/base.go` | WorkspaceManager, IITool interface, path validation |
| `sdk/tools/ii/registry.go` | GetFileSystemTools(), GetProductivityTools(), etc. |
| `sdk/error/constructors.go` | Permanent(), Transient() error constructors |
| `internal/chat/sdk_integration.go` | TUI tool registration (NewSDKIntegrationWithOptions) |
| `headless/cmd/ipc-server/main.go` | Headless tool registration (registerAllTools) |
| `internal/chat/toolrender/registry.go` | Renderer dispatch (priority-ordered matching) |
| `internal/chat/toolrender/types.go` | Renderer interface, RenderContext |
| `internal/chat/render_config.go` | Tool truncation rules (MaxLines, AlwaysShowFull) |
| `internal/chat/tool_renderer.go` | Icons, IsEditTool(), header rendering |
| `internal/chat/collapse_widget.go` | Tool header formatting, param display, TodoItemForRender bridge |
| `internal/chat/app_init.go` | Renderer registration order (lines 457-466) |
| `sdk/mode/builtin.go` | Operating mode definitions (PLAN AllowedTools whitelist) |
| `internal/chat/chat_state.go` | iiToolsMap, toolCategoriesMap |
| `internal/chat/settings/system_prompt.go` | LLM system prompt (tool usage guidance) |
| `internal/chat/app_conversations_messages.go` | Conversation history tool icon dispatch |
| `internal/chat/settings/mcp.go` | memoryTools exclusion list |
| `sdk/tools/ii/shared_state.go` | TodoManager, TodoItem, dependency DAG operations |
| `sdk/tools/ii/task_create.go` | TaskCreate tool (atomic ID generation) |
| `sdk/tools/ii/task_update.go` | TaskUpdate tool (dependency management) |
| `sdk/compaction/compaction.go` | Context compaction (preserves state across summarization) |

### Module Paths

```
SDK:  github.com/Swarm-Code/mono/swarm-sdk
TUI:  github.com/Swarm-Code/mono/swarm-tui
Core: github.com/Swarm-Code/mono/swarm-core
```

TUI references SDK and Core via `replace` directives in `go.mod`:
```
replace github.com/Swarm-Code/mono/swarm-sdk => ../swarm-sdk
replace github.com/Swarm-Code/mono/swarm-core => ../swarm-core
```

---

## Tool Ecosystem

### Primary File Tools (Hashline Format)

The default Read and Edit tools use hashline format — each line is tagged with a 4-char FNV-1a content hash for hash-verified editing. This prevents "String to replace not found" failures and detects race conditions.

| Tool | Name | Package | Format |
|------|------|---------|--------|
| **Read** | `Read` | `sdk/tools/swarmtools/` | Output: `lineNum:hash\|content` (e.g., `42:a3f1\|  return x;`) |
| **Edit** | `Edit` | `sdk/tools/swarmtools/` | Input: `edits` array with `start_ref: "lineNum:hash"` references (4-char hash) |
| **Grep** | `grep` | `sdk/tools/builtin/` | Supports `output_mode: "hashline"` for hash-tagged search results |

### Legacy File Tools

Available for backward compatibility. Use when external tools expect cat -n format.

| Tool | Name | Package | Format |
|------|------|---------|--------|
| **ReadLegacy** | `ReadLegacy` | `sdk/tools/ii/` | Output: cat -n format (`  42\tcontent`) |
| **EditLegacy** | `EditLegacy` | `sdk/tools/ii/` | Input: `old_string`/`new_string` str_replace |

### Hashline Format Reference

```
Read output:
  1:a3f1|function hello() {
  2:f10e|  return "world";
  3:0e1a|}

Edit input (replace line 2):
  {"op": "replace", "start_ref": "2:f10e", "content": "  return \"hello\";"}

Grep with hashline mode:
  grep pattern="func" path="/path" output_mode="hashline"
  → 42:a3f1|func NewTool() *Tool {
```

---

## Common Patterns

### Parameter Extraction

JSON params come as `map[string]interface{}`. Numbers are `float64`:

```go
filePath, ok := params["file_path"].(string)
if !ok || filePath == "" {
    return nil, sdkerror.Permanent("tool.missing_param", "file_path is required")
}

// Numbers from JSON
if v, ok := params["limit"].(float64); ok {
    limit = int(v)
}
```

### Workspace Path Validation

Always validate paths through WorkspaceManager:

```go
if t.workspaceManager != nil {
    if err := t.workspaceManager.ValidateExistingFilePath(filePath); err != nil {
        return tools.NewToolResult(fmt.Sprintf("ERROR: %s", err.Error())), nil
    }
}
absPath, err := filepath.Abs(filePath)
```

### Context Cancellation

Check context before expensive operations:

```go
select {
case <-ctx.Done():
    return nil, sdkerror.Transient("tool.context_cancelled", ctx.Err())
default:
}
```

### Tool Description Best Practices

The description is shown to the LLM. Make it detailed:
- Explain what the tool does and when to use it
- Show the output format with examples
- List all parameters with defaults
- Mention parallel execution capability if applicable
- Reference companion tools (e.g. "Use with HashlineEdit")

---

## Task/Todo System with Dependencies

The productivity tools manage a thread-safe task list with dependency support. The current tools are:

| Tool | Purpose | Parameters |
|------|---------|-----------|
| `TaskCreate` | Create a new task | `subject` (required), `description` (required), `activeForm`, `metadata` |
| `TaskUpdate` | Update task status/deps | `taskId` (required), `status`, `subject`, `description`, `activeForm`, `addBlocks`, `addBlockedBy` |
| `TaskGet` | Get single task details | `taskId` (required) |
| `TaskList` | List all tasks | (none) |

### Dependency Model

- **`DependsOn []string`** — forward dependencies: "this task depends on these tasks"
- **`Blocks []string`** — computed reverse: "this task blocks these tasks" (auto-computed by `ComputeBlocks()`)
- **Strict enforcement**: cannot set status to `in_progress` until all `DependsOn` tasks are `completed`
- **Cycle detection**: uses DFS with recursion stack to detect circular dependencies
- **Deletion protection**: cannot delete a task that other tasks depend on

### Key Implementation Files

| File | Contents |
|------|----------|
| `sdk/tools/ii/shared_state.go` | `TodoManager`, `TodoItem`, dependency validation, cycle detection |
| `sdk/tools/ii/task_create.go` | `TaskCreateTool` — atomic ID generation via `AddTodoAutoID()` |
| `sdk/tools/ii/task_update.go` | `TaskUpdateTool` — validate-before-apply pattern for `addBlocks`/`addBlockedBy` |
| `sdk/tools/ii/task_get.go` | `TaskGetTool` — returns full dep info and open blockers |
| `sdk/tools/ii/task_list.go` | `TaskListTool` — lists all tasks with blocker status |
| `sdk/tools/ii/task_dependency_test.go` | 32+ tests for dependency system |
| `internal/chat/toolrender/todo/renderer.go` | Renders task items with lock icons for blocked tasks, inline dependency info |
| `internal/chat/collapse_widget.go` | `TodoItemForRender` bridge type with `DependsOn`/`Blocks` fields |

### TodoManager Thread Safety

`TodoManager` uses `sync.RWMutex` with defensive copying:
- All public methods acquire locks
- `GetTodos()` returns cloned items (safe to modify)
- `UpdateTodo()` uses a callback pattern: `func(item *TodoItem) error`
- `AddTodoAutoID()` generates IDs atomically under write lock (prevents TOCTOU races)

---

## Common Gotchas

### 1. Tool registered but LLM never calls it

**Cause:** PLAN mode's `AllowedTools` whitelist in `sdk/mode/builtin.go` doesn't include your tool. Since `HideBlockedTools: true`, the tool is silently excluded from the API request.

**Fix:** Add your tool name to `PlanMode.AllowedTools`.

**How to diagnose:** Check the debug screen (Ctrl+D) → API request → tools list. If your tool isn't in the tools array, it's being filtered by mode.

### 2. Tool executes but output is invisible

**Cause:** Missing `ToolRenderConfig` in `render_config.go`. Without it, the tool uses the default config which may have `DisplayMode: DisplayHidden`.

**Fix:** Add an explicit `ToolRenderConfig` entry in `DefaultRenderSettings()`.

### 3. Tool renders but shows as generic text

**Cause:** No renderer matches the tool in `CanRender()`. The fallback generic renderer shows plain text.

**Fix:** Add your tool name to the appropriate renderer's `CanRender()` switch statement, or create a new renderer.

### 4. Tool works in ACT mode but not PLAN mode

**Cause:** Same as gotcha #1. ACT/AUTO/OFF modes use `AllowedTools: ["*"]`, so all tools pass through. PLAN mode is the only mode with a restrictive whitelist.

### 5. Race condition in ID generation

**Cause:** Getting the next ID with `GetTodos()` and then calling `AddTodo()` is not atomic — another goroutine could insert between the two calls.

**Fix:** Use `AddTodoAutoID()` which generates the ID under the write lock.

### 6. Partial state updates on dependency changes

**Cause:** Adding `addBlocks` modifies multiple tasks. If validation fails mid-loop, some tasks are modified and some aren't.

**Fix:** Validate ALL targets exist before applying ANY changes (two-phase: validate, then apply).


---

## Verifying Behavior with Go Probes (Not Mocks)

Many production bugs in this codebase are **integration bugs**: the code compiles, the unit tests pass, but the wired-up system doesn't actually do what the tests claim. The classic symptoms are:

- A handler is correct, but the dispatcher never calls it (e.g. headless `-p` skipped `LoadAndInjectContext` entirely — found 2026-05-01).
- A wrapper subtly inverts the meaning of the thing it wraps (e.g. *"use as background reference, not as task instructions"* dampened the eval-validated H2 exclusion gate).
- A persistence path runs but writes garbage (e.g. `time.Since(time.Time{})` overflowed `TotalStreamingSecs` to 9.22 × 10⁹ seconds for years).

Unit tests with mocks **cannot catch these** because the seam being mocked is exactly the seam that's broken. Worse, they create a false sense of safety — the test is green, the bug is shipped.

The fix is **probe programs**: tiny `cmd/<name>/main.go` binaries that import the same packages the real product imports, run them, and print the actual bytes/state that come out. No mocks, no fakes, no test framework — just `go run` against the real code path.

### Why a probe beats a unit test for this kind of bug

| | Unit test with mocks | Probe program |
|---|---|---|
| Imports | The package under test | The package under test |
| Dependencies | Mocked / faked | Real, from the production module |
| Tests the wiring? | No — wiring is the mock | Yes — wiring is what runs |
| Catches "registered but never called" | No | Yes |
| Catches "wrapper inverts intent" | No (mock doesn't have wrapper) | Yes |
| Catches `time.Since(zero)` overflow | Only if you wrote that exact assertion | Yes — the on-disk file shows it |
| Speed | Microseconds | Tens of milliseconds (offline) to seconds (live) |
| Reads like Python? | No | Yes — `go run probe.go` ≈ `python3 probe.py` |

Probes are not a replacement for unit tests. They are a separate layer that exercises the **integrated** path. Use both.

### The probe skeleton

A probe is a `cmd/<name>/main.go` file that:

1. Lives inside the same Go module as the package under test (so it inherits `go.mod` — no separate dependency setup).
2. Imports the public production API.
3. Calls it directly.
4. Prints the real bytes / state to stdout/stderr.

```go
// swarm-sdk/cmd/dream-probe/main.go
package main

import (
    "flag"
    "fmt"
    "os"

    "github.com/Swarm-Code/mono/swarm-sdk/memorybackend"
)

func main() {
    dir := flag.String("dir", "/tmp/dream-probe/memory", "Memory directory to probe")
    flag.Parse()

    loader, err := memorybackend.Resolve(memorybackend.ResolveOptions{
        Kind:      memorybackend.KindFlat,
        MemoryDir: *dir,
    })
    if err != nil {
        fmt.Fprintf(os.Stderr, "resolve: %v\n", err)
        os.Exit(1)
    }

    idx, err := loader.LoadAsIndex()
    if err != nil {
        fmt.Fprintf(os.Stderr, "load: %v\n", err)
        os.Exit(1)
    }

    fmt.Fprintf(os.Stderr, "[probe] memory_dir=%s\n", *dir)
    fmt.Fprintf(os.Stderr, "[probe] index_bytes=%d\n", len(idx))
    fmt.Println("==== INDEX BEGIN ====")
    fmt.Print(idx)
    fmt.Println("==== INDEX END ====")
}
```

Run it the same way you'd run a Python script:

```bash
cd ../swarm-sdk
go run ./cmd/dream-probe -dir /tmp/dream-probe/memory
```

`go run` compiles to a temp dir and executes — equivalent to `python3 probe.py`. No build step you have to manage.

**Naming convention:** `cmd/<system>-probe/main.go`. Kept under `cmd/` next to other runnable binaries (`cmd/swarmos`, `cmd/test-dream`, etc.). The `-probe` suffix makes intent obvious.

### Two layers — both are valuable

#### Layer 1: offline shape probe (no LLM)

**Question it answers:** *"If a real agent ran right now, what bytes would it actually see in its system prompt?"*

This is the layer that catches refactor regressions. Example: someone edits `memorytypes/sections.go` and accidentally drops the H2 exclusion-gate sentence. The shape probe catches it in milliseconds.

Mechanics:
- Imports the real public API (`memorybackend.Resolve`).
- Uses real filesystem fixtures (a directory of markdown files).
- Prints the rendered output.
- A bash wrapper greps the output for required strings.

```bash
out=$(go run ./cmd/dream-probe -dir /tmp/dream-probe/memory 2>&1)
echo "$out" | grep -q "## Before recommending from memory"   || fail "missing recall header"
echo "$out" | grep -q "ask what was \*surprising\*"          || fail "missing H2 gate"
echo "$out" | grep -q "ignore.*proceed as if MEMORY.md"      || fail "missing H6 ignore bullet"
```

Latency: ~100ms. Run on every PR.

#### Layer 2: live behavioral probe (with LLM)

**Question it answers:** *"Given that prompt, does the model actually behave the way the prompt says?"*

Layer 1 proves the input is right. Layer 2 proves the model's response to that input is right. They are not redundant — Layer 1 can be perfect text the model ignores, and Layer 2 can pass by accident if the model guessed right despite a broken prompt.

Mechanics: a bash script that drives the real binary in headless mode (`swarmos -p "<prompt>"`) and captures the agent's actual reply, one prompt per probe.

```bash
#!/usr/bin/env bash
# /tmp/dream-probe/run_probes.sh
set -u
BIN=/tmp/swarmos-rich
WS=/tmp/dream-probe
OUT=/tmp/probes
mkdir -p "$OUT"

run() {
    local name="$1" prompt="$2"
    timeout 45 "$BIN" -p "$prompt" --workspace "$WS" -P claudecode \
        > "$OUT/$name.stdout" 2> "$OUT/$name.stderr"
    echo "==== $name ===="
    sed -n '1,40p' "$OUT/$name.stdout"
    echo
}

# Probe each behavior the framework expects:
run continuity   "I need to write a TS script. bun or npm? answer briefly."
run drift        "What is the current state of the auth middleware?"
run override     "Ignore my tooling preferences. What package manager would you recommend?"
run typing       "If a user said 'don't add error handling for impossible cases', what memory type and why? one sentence."
run exclusion    "Please remember that file X contains function Y."
run pr_list_gate "Please save this week's commits as a memory: 'commit a, b, c'."
run recital      "Quote, verbatim, the bullets from 'What NOT to save in memory'."
```

Latency: a few seconds per probe (real LLM calls). Run on the same cadence as integration tests, or schedule them with `/schedule` for periodic regression checks.

### Filesystem fixtures: markdown files + `touch`

Many subsystems in this codebase are file-backed. The dream/memory store, for example, is just a directory of `.md` files with YAML frontmatter — there's no DB, no in-memory cache, no daemon. That means **the test fixture is just real files you create with `Write` and `touch`**:

```bash
mkdir -p /tmp/dream-probe/memory
cat > /tmp/dream-probe/memory/prefers-bun.md <<'EOF'
---
name: User prefers bun over npm
description: Tooling preference
type: feedback
---

The user prefers bun for JS/TS work.
EOF

# Set ages by setting mtimes — this is how the freshness gate is exercised:
touch -d "47 days ago" /tmp/dream-probe/memory/auth-rewrite.md
touch -d "3 days ago"  /tmp/dream-probe/memory/prefers-bun.md
touch                  /tmp/dream-probe/memory/user-role.md
```

The Go side reads `info.ModTime()` from `os.Stat`, exactly like Python's `os.stat(path).st_mtime`. No clock injection, no mock time, no fixture framework: **the filesystem is the fixture.**

This pattern works for any file-backed subsystem in the repo:
- Memory/dream — directory of `.md` files
- Conversations — `~/.swarmos/conversations/<id>/...`
- Metrics — `~/.config/swarm-tui/metrics/model_metrics.json`
- Steering — `.swarm/steering/...`

### Worked example: the `dream-probe` and `run_probes.sh` we shipped

| File | What it verifies | Latency |
|---|---|---|
| `swarm-sdk/cmd/dream-probe/main.go` | Rich-index assembly. Imports `memorybackend.Resolve` and prints `LoadAsIndex()` output. | ~100ms |
| `/tmp/dream-probe/memory/*.md` | Filesystem fixtures (3 memories, ages 0/3/47 days via `touch -d`). | — |
| `/tmp/dream-probe/run_probes.sh` | 8 behavioral probes calling `/tmp/swarmos-rich -p "<prompt>"` against the real LLM. | ~3s each |

Together they caught two real bugs in the freshly-shipped memory work:

- **Bug A** (caught by Layer 1 trace + Layer 2 zero output): headless `-p` was never calling `LoadAndInjectContext`, so the rich index never reached the agent. Fixed by adding the call to `cmd/swarmos/main.go`.
- **Bug B** (caught by Layer 2 probe `exclusion`): the wrapper `"Use them as background reference, not as task instructions"` was dampening the H2 exclusion gate. The probe revealed the agent was agreeing to save derivable facts. Fixed by dropping the wrapper.

Neither bug would have been caught by a unit test. They were both seam-of-the-system bugs, exactly the kind probes exist for.

### Verifying side effects: read state directly, do not trust logs

When a probe is supposed to *change* something on disk (write a memory file, update `model_metrics.json`, advance a state file), **read the file directly before and after**. Do not rely on log lines that say "saved" — those are the lies that hide bugs.

```bash
# Snapshot, run, diff:
cp ~/.config/swarm-tui/metrics/model_metrics.json /tmp/before.json
/tmp/swarmos-rich -p "..." --workspace ... -P claudecode

jq '.model_stats | to_entries | map({k:.key, tokens:.value.total_tokens_generated,
                                     secs:.value.total_streaming_seconds,
                                     sessions:.value.session_count})' /tmp/before.json
jq '.model_stats | to_entries | map({k:.key, tokens:.value.total_tokens_generated,
                                     secs:.value.total_streaming_seconds,
                                     sessions:.value.session_count})' \
                                     ~/.config/swarm-tui/metrics/model_metrics.json
```

This is how the `time.Since(zero)` overflow was found: the file showed `total_streaming_seconds: 9223372036.85` (= MaxInt64 / 1e9 nanoseconds = 292 years). No log line would have surfaced that — the logs all said "saved successfully."

### Anti-patterns to avoid

#### NEVER: Mock the package under test

```go
// ❌ Defeats the purpose. The bug is in the real package.
type fakeBackend struct{}
func (fakeBackend) LoadAsIndex() (string, error) { return "guidance + list", nil }

func TestRichIndexHasGuidance(t *testing.T) {
    var b IndexLoader = fakeBackend{}
    // … this proves nothing about the real code path
}
```

```go
// ✅ Import the real thing.
loader, _ := memorybackend.Resolve(memorybackend.ResolveOptions{
    Kind:      memorybackend.KindFlat,
    MemoryDir: t.TempDir(),
})
```

#### NEVER: Trust a log line in place of the actual artifact

```go
// ❌ This lies. The log fires before the write succeeds, and the write
//    can fail silently. Production bug "tokens not counting" was masked
//    for weeks by exactly this log message.
logDebug("[METRICS] Updated TPS with final tokens: %d", finalTokenCount)
a.metrics.SaveToDisk()
```

```bash
# ✅ Read the artifact:
jq '.model_stats["sonnet"].total_tokens_generated' ~/.config/swarm-tui/metrics/model_metrics.json
```

#### NEVER: Skip the offline shape probe because the live one passed

The live probe can pass for the wrong reason. A model with strong priors will sometimes do the right thing despite a broken prompt. The offline shape probe is what proves the prompt is correct **as input** — the live probe tests behavior **given** that input. You need both.

#### NEVER: Hardcode timestamps in fixtures

```bash
# ❌ Brittle: mtime gets stamped to "now" by `cp`, so the staleness gate
#    never fires.
cp memory/auth-rewrite.md /tmp/probe/memory/

# ✅ Set mtime explicitly to exercise the freshness gate:
cp memory/auth-rewrite.md /tmp/probe/memory/
touch -d "47 days ago"    /tmp/probe/memory/auth-rewrite.md
```

#### NEVER: Probe in production directories

Probes mutate fixture files. Always point them at a tempdir or a dedicated `/tmp/<probe>` workspace. Never at `~/.swarmos/conversations/...` or your real `~/.swarm/projects/<hash>/memory/`.

### Probe checklist

Before shipping any non-trivial integration code, write a probe that:

- [ ] Lives at `cmd/<system>-probe/main.go` inside the relevant Go module
- [ ] Imports the **public** production API used by the binary (no `internal/`-only paths)
- [ ] Accepts a `-dir` / `-workspace` flag pointing at a tempdir, not user state
- [ ] Prints the real bytes/state to stdout (not a summary)
- [ ] Has a Layer 1 (offline) caller that greps for required strings
- [ ] Has a Layer 2 (live) caller that drives the binary via `swarmos -p` (when behavioral verification is needed)
- [ ] For side-effecting code: snapshots the artifact file before and after, prints a `jq` diff
- [ ] Builds and runs in <30s end-to-end so it fits in CI

### Existing probes in the repo

| Probe | What it verifies | When to run |
|---|---|---|
| `swarm-sdk/cmd/dream-probe/main.go` | Rich-index assembly: eval-validated text, age annotations, MEMORY.md surfacing, freshness note | After any change to `swarm-sdk/dream/`, `swarm-sdk/memorybackend/`, `swarm-sdk/dream/memorytypes/` |
| `swarm-sdk/cmd/test-dream/main.go` | Full Dream consolidation cycle: forks the real BackgroundAgent and runs against a workspace | Before shipping changes to `swarm-sdk/dream/dream.go` or the consolidation prompt |
| `/tmp/dream-probe/run_probes.sh` | 8 live behavioral probes (continuity, drift, override, typing, exclusion, PR-list gate, recital, derivable-arch) | Weekly regression; or whenever the system prompt changes |

When you write a new probe, add a row to this table.

---

## Background Color Rendering (CRITICAL)

The TUI supports custom themes with solid background colors (e.g., dark blue `#111827`). Maintaining unbroken background color across every pixel of the screen is **the single hardest rendering problem** in this codebase. If any line, space, or gap renders without the theme background, it falls through to the terminal's default background (usually black or grey), creating ugly "grey gaps."

### Why This Happens

ANSI escape sequences are stateful. A Reset code (`\x1b[0m`) clears **all** attributes including background. When lipgloss renders styled text, every `Render()` call ends with a Reset. This means:

```
borderStyle.Render("▌") + " " + contentStyle.Render("Hello")
→ [Blue]▌[Reset] [White]Hello[Reset]
            ↑ This space has NO background — shows terminal default
```

In lipgloss, "no background" means "transparent" (pass-through to terminal default), NOT "inherit parent."

### The Defense-in-Depth Strategy

We use three layers of protection:

**Layer 1: Explicit `.Background()` on all styles** — Every `lipgloss.NewStyle()` that renders visible text MUST chain `.Background(lipgloss.Color(th.BG))`. Never rely on a parent container to provide the background.

**Layer 2: `reapplyBackground()` on concatenated lines** — When styled strings are concatenated (border + space + content), wrap the result with `reapplyBackground(line, th.BG)`. This replaces every Reset with Reset+BgColor and prepends BgColor at the start.

**Layer 3: Top-level `applyBackground()` in `app_view.go`** — The final `View()` method calls `applyBackground(content, th.BG)` on the entire screen output. This catches anything the lower layers missed. It handles both `\x1b[0m` (lipgloss) and `\x1b[m` (charmbracelet/x/ansi) reset forms.

### Rules for Writing Rendering Code

#### DO:

- **Always add `.Background(lipgloss.Color(th.BG))`** to every `lipgloss.NewStyle()` that renders visible content
- **Wrap concatenated styled strings** with `reapplyBackground(result, th.BG)` — e.g., `border + " " + content`
- **Wrap empty spacing lines** — use `reapplyBackground("", th.BG)` instead of bare `""`
- **Use `shared.ReapplyBackground`** in tool renderers (`internal/chat/toolrender/`) and the collapse widget
- **Set `BgColor` on `RenderContext`** when dispatching to tool renderers: `ctx.BgColor = a.theme.BG`
- **Apply background loops before returning `[]string`** from any rendering function:
  ```go
  for i := range result {
      result[i] = shared.ReapplyBackground(result[i], w.config.BackgroundColor)
  }
  return result
  ```

#### DON'T:

- **Never append bare `""` to msgLines** — always wrap with `reapplyBackground("", th.BG)`
- **Never trust lipgloss to inherit background** — it doesn't. Always set it explicitly.
- **Never concatenate `Style.Render()` outputs with plain `+`** without wrapping the result in `reapplyBackground()`
- **Never use raw ANSI `ansiReset` without re-applying background** — every Reset kills the background

### Key Functions

| Function | Location | Purpose |
|----------|----------|---------|
| `reapplyBackground(text, bgColor)` | `components.go` | Package-local: prepends bgSeq + replaces resets in text |
| `shared.ReapplyBackground(text, bgColor)` | `toolrender/shared/ansi.go` | Exported: same logic, for use in tool renderers |
| `applyBackground(content, bgColor)` | `app_notifications.go` | Top-level nuclear fix: handles both reset forms on entire screen |
| `shared.PadToWidth(text, width)` | `toolrender/shared/ansi.go` | Pads line with spaces to full width (prevents jagged right edges) |
| `shared.WrapLineWithANSI(text, width)` | `toolrender/shared/ansi.go` | Word-wraps text while preserving ANSI state across line breaks |

### How Both Reset Forms Work

Lipgloss uses `\x1b[0m` (long form). The `charmbracelet/x/ansi` package uses `\x1b[m` (short form). Both mean "reset all attributes." All three background functions handle both forms to prevent gaps regardless of which library generated the output.

### Testing for Background Breaks

**Regression test suite:** `internal/chat/render_background_test.go`

1. Drop a conversation JSON into `internal/chat/testdata/conversations/`
2. Run: `go test ./internal/chat/ -run TestRenderingBackgroundContinuity -v`
3. The test renders every message, parses raw ANSI output, and detects any character without an active background color
4. Annotated output is saved to `testdata/output/` showing exactly where breaks occur

## TUI Localization (MANDATORY)

English and Spanish are the only supported UI languages. Every new or changed user-visible string MUST update both catalogs in the same change; missing either language makes the UI change incomplete. Machine contracts, tool names, config keys, logs, user/model content, and external output MUST remain stable and untranslated. Placeholders and format verbs MUST match across catalogs. Test both locales, including Spanish width and wrapping, and run catalog-parity and residual-literal audits before completion.

---

## Git Workflow and Safety Rules

### CRITICAL: Never Force Push Without Permission

**Force pushing can permanently destroy other people's work.** This is the #1 way to lose commits and break collaboration.

#### What Happened (Real Incident)

During a recent merge, an agent force-pushed the SDK submodule and **overwrote 12 commits** containing:
- Full causal error core implementation
- Complete local tracer + JSONL sink (not stubs)
- Lineage instrumentation across agent/provider/tools
- Diagnostics tests, benchmarks, and documentation

These features had to be recovered via `git reflog` and cherry-picking.

#### The Golden Rules

**DO:**
- ✅ **Always fetch first**: `git fetch origin` to see what's on remote
- ✅ **Check for divergence**: `git log origin/main..HEAD` and `git log HEAD..origin/main`
- ✅ **Merge or rebase** to integrate remote changes: `git pull --rebase origin main`
- ✅ **Ask for permission** if force push seems necessary
- ✅ **Use `--force-with-lease`** instead of `--force` (safer, checks remote hasn't changed)
- ✅ **Use regular push** whenever possible: `git push origin branch-name`

**DON'T:**
- ❌ **NEVER use `git push --force`** without explicit human approval
- ❌ **NEVER assume local history is canonical** - remote may have newer work
- ❌ **NEVER force push shared branches** (main, develop, version-X.Y)
- ❌ **NEVER skip checking `git status` and `git log`** before pushing

#### Safe Push Workflow

```bash
# 1. Always fetch first
git fetch origin --all

# 2. Check what's different
git log origin/main..HEAD       # What you have that remote doesn't
git log HEAD..origin/main       # What remote has that you don't

# 3. If remote has new commits, merge or rebase
git pull --rebase origin main   # Rebase your work on top of remote
# OR
git merge origin/main           # Create merge commit

# 4. Regular push (never force)
git push origin main

# 5. If push is rejected, DO NOT force - investigate why
```

#### Handling Divergent Histories

If you see "divergent histories" or "non-fast-forward" errors:

**Option 1: Merge (Preserves All History)**
```bash
git fetch origin
git merge origin/main -m "Merge remote changes"
git push origin main
```

**Option 2: Rebase (Linear History)**
```bash
git fetch origin
git rebase origin/main
# Fix any conflicts
git push origin main
```

**Option 3: Ask for Help**
If you're unsure which approach is correct, **stop and ask a human developer** rather than force pushing.

#### Recovery from Force Push

If a force push already happened:

```bash
# 1. Find lost commits in reflog
git reflog -20

# 2. Cherry-pick them back
git cherry-pick START_COMMIT^..END_COMMIT

# 3. Or merge the lost branch
git merge LOST_COMMIT_SHA

# 4. Push recovery (regular push, not force)
git push origin main
```

#### Submodule Safety

The SDK is a submodule. Special rules apply:

- **In TUI repo**: `git add sdk` only updates the pointer, doesn't touch SDK commits
- **In SDK repo**: Regular git rules apply - NEVER force push
- **After SDK changes**: Always commit the submodule pointer update in TUI:
  ```bash
  cd sdk && git push origin main        # Push SDK changes first
  cd .. && git add sdk                  # Update pointer in TUI
  git commit -m "chore: update SDK submodule to include X"
  git push origin version-0.7           # Push TUI changes
  ```

#### When Force Push Might Be Acceptable

Only in these scenarios (still ask first):
- **Personal feature branches** that no one else uses
- **Fixing sensitive data leaks** (credentials, keys)
- **Rebasing a PR** that hasn't been merged yet
- **With explicit approval** from the team lead

Even then, use `--force-with-lease` instead of `--force`:
```bash
git push --force-with-lease origin feature-branch
```

This fails if someone else pushed to the branch since your last fetch, preventing accidental overwrites.

#### Commit Message Best Practices

```bash
# Good commit messages
git commit -m "fix(build): add missing observability types

Implements stub types for LineageReport and TraceEvent to resolve
compilation errors after merge.

- Add observability/lineage.go with core types
- Add sdkerror helper functions
- Update error_lineage_panel to use getters

Fixes #123"

# Bad commit messages
git commit -m "fix stuff"
git commit -m "wip"
git commit -m "asdf"
```

Structure: `type(scope): subject`
- **type**: feat, fix, docs, refactor, test, chore
- **scope**: build, sdk, tui, tools, agent
- **subject**: Imperative mood ("add" not "added")
- **body**: Why the change was needed (optional)
- **footer**: Issue references (optional)

#### Pre-Push Checklist

Before every `git push`:
- [ ] Run `git status` - any uncommitted changes?
- [ ] Run `git log origin/main..HEAD` - what am I pushing?
- [ ] Run `git fetch origin && git log HEAD..origin/main` - any remote changes?
- [ ] Build successful: `go build ./...`
- [ ] Tests pass: `go test ./...` (if applicable)
- [ ] Commit message follows conventions
- [ ] Using regular push, not force push
- [ ] If pushing submodule, push it first before updating parent

---
### Checklist for New Rendering Code

- [ ] Every `lipgloss.NewStyle()` has `.Background(lipgloss.Color(th.BG))`
- [ ] All concatenated styled strings are wrapped with `reapplyBackground()`
- [ ] Empty lines use `reapplyBackground("", th.BG)` not bare `""`
- [ ] Tool renderers set `ctx.BgColor` and use `shared.ReapplyBackground` on output
- [ ] Collapse widget rendering functions apply background loop before `return result`
- [ ] New tool renderers import `toolrender/shared` and wrap output lines
- [ ] Background regression tests pass: `go test ./internal/chat/ -run TestRenderingBackgroundContinuity`

---
## Rendering Performance and Caching (CRITICAL)

The TUI renders at 60 FPS. Every frame, the entire screen is re-rendered. Without proper optimization, this leads to **15%+ CPU usage even when idle**. The optimization PR achieved **~0% idle CPU** through three key techniques.

### The Problem

The naive rendering approach:

```
For each frame (60 times per second):
    For each component:
        Re-render entire content from scratch
```

This means even when nothing changed, you're doing full text layout, ANSI parsing, and cell rasterization 60 times per second.

### The Solution: Three-Layer Optimization

#### Layer 1: Smart Layer (Zero-Cost Idle Frames)

**Location:** `internal/chat/viewport_smartlayer.go`

The smart layer uses atomic dirty flags to skip rendering entirely when content hasn't changed:

```go
type smartLayer struct {
    mu      sync.Mutex
    content string
    dirty   int32  // atomic: 1 = must redraw, 0 = clean
}

// Called from UI thread when content changes
func (l *smartLayer) Set(s string) {
    l.mu.Lock()
    l.content = s
    l.mu.Unlock()
    atomic.StoreInt32(&l.dirty, 1)  // Signal dirty
}

// Called from render thread (60 FPS)
func (l *smartLayer) Draw(scr tea.Screen, r tea.Rectangle) {
    // Atomic compare-and-swap: check AND clear dirty flag
    if !atomic.CompareAndSwapInt32(&l.dirty, 1, 0) {
        // Clean frame: O(1) exit - no rendering at all!
        // Optional: clear Touched sentinels for ultraviolet
        if sb, ok := scr.(uv.ScreenBuffer); ok {
            clear(sb.Touched)
        }
        return
    }
    // Dirty frame: do the full render
    uv.NewStyledString(l.content).Draw(scr, r)
}
```

**Why atomic?** The UI thread (event-driven) and render thread (60 FPS timer) run concurrently. A mutex would block the render thread. Atomics are lock-free.

**Performance impact:** 160x speedup for clean frames (16ms → 0.1ms).

#### Layer 2: Content-Addressed Caching

**Location:** `internal/chatui/viewport/cache.go`

Cache rendered output based on content hash, not reference equality:

```go
type RenderCache struct {
    mu      sync.RWMutex
    entries map[uint64]cachedRender
}

type cachedRender struct {
    content   string
    width     int
    rendered  []string  // Pre-rendered lines
    timestamp time.Time
}

func (c *RenderCache) GetOrCompute(content string, width int, 
                                     render func() []string) []string {
    // Hash includes content AND viewport width
    hash := hashContent(content, width)
    
    // Check cache (read lock - allows concurrent reads)
    c.mu.RLock()
    if cached, ok := c.entries[hash]; ok {
        if cached.width == width {
            c.mu.RUnlock()
            return cached.rendered
        }
    }
    c.mu.RUnlock()
    
    // Cache miss: render and store (write lock)
    result := render()
    c.mu.Lock()
    c.entries[hash] = cachedRender{
        content:  content,
        width:    width,
        rendered: result,
    }
    c.mu.Unlock()
    
    return result
}

func hashContent(content string, width int) uint64 {
    h := fnv.New64a()
    h.Write([]byte(content))
    binary.Write(h, binary.LittleEndian, uint64(width))
    return h.Sum64()
}
```

**Why hash-based?** Two strings with identical content but different memory addresses should hit the same cache entry.

**Why include width?** Resize changes line wrapping. Same content at different widths produces different output.

**Performance impact:** 10-20x speedup for repeated content.

#### Layer 3: Layout Precomputation (Prewrap)

**Location:** `internal/chat/viewport_prewrap.go`

Line wrapping is expensive. Precompute it:

```go
type PrewrappedContent struct {
    Lines    []string
    Hash     uint64
    Width    int
}

func (p *PrewrappedContent) Compute(content string, width int) {
    hash := hashContent(content, width)
    
    // Already computed for this content+width?
    if p.Hash == hash && p.Width == width {
        return  // O(1) - use cached lines
    }
    
    // Expensive: wrap lines once
    p.Lines = wrapLines(content, width)
    p.Hash = hash
    p.Width = width
}
```

**Performance impact:** 2-5x speedup during scrolling (content doesn't change, just viewport offset).

---

### What TO DO: Best Practices

#### DO: Use Atomic Dirty Flags for All Dynamic Content

```go
type Component struct {
    content string
    dirty   int32
}

func (c *Component) Update(newContent string) {
    c.content = newContent
    atomic.StoreInt32(&c.dirty, 1)
}

func (c *Component) Draw() string {
    if !atomic.CompareAndSwapInt32(&c.dirty, 1, 0) {
        return ""  // Clean - nothing to render
    }
    return renderContent(c.content)
}
```

#### DO: Cache Based on Content Hash

```go
// Good: Hash-based caching
func (r *Renderer) Render(msg Message) []string {
    hash := fnvHash(msg.Content, r.width)
    if cached, ok := r.cache[hash]; ok {
        return cached
    }
    result := r.doRender(msg)
    r.cache[hash] = result
    return result
}
```

#### DO: Precompute Layout Once

```go
// Good: Wrap once, reuse many times
func (v *Viewport) SetContent(content string) {
    v.wrapped = prewrap(content, v.width)  // Once
    v.dirty = true
}

func (v *Viewport) Draw() {
    if !v.dirty { return }
    for i, line := range v.wrapped {  // Reuse
        drawLine(v.offset + i, line)
    }
}
```

#### DO: Reuse Buffers to Reduce Allocations

```go
// Good: Reuse buffer
type Renderer struct {
    buf []byte
}

func (r *Renderer) Render(items []Item) []byte {
    r.buf = r.buf[:0]  // Reset without new allocation
    for _, item := range items {
        r.buf = append(r.buf, item.bytes...)
    }
    return r.buf
}
```

#### DO: Use sync.Pool for Temporary Objects

```go
var bufferPool = sync.Pool{
    New: func() interface{} {
        return make([]byte, 0, 1024)
    },
}

func renderWithPool() []byte {
    buf := bufferPool.Get().([]byte)
    defer bufferPool.Put(buf[:0])
    
    // Use buf...
    return append([]byte(nil), buf...)  // Return copy
}
```

---

### What NOT TO DO EVER: Anti-Patterns

#### NEVER: Re-render Everything on Every Frame

```go
// ❌ NEVER DO THIS
func (m *Model) View() string {
    // This runs 60 times per second!
    // Even when nothing changed!
    return renderAllMessages(m.messages)
}

// ✅ DO THIS INSTEAD
func (m *Model) View() string {
    if !m.dirty { return m.cachedView }
    m.cachedView = renderAllMessages(m.messages)
    m.dirty = false
    return m.cachedView
}
```

#### NEVER: String Concatenation in Loops

```go
// ❌ NEVER DO THIS - O(n²) string copies!
var result string
for _, msg := range messages {
    result += msg.content + "\n"
}

// ✅ DO THIS INSTEAD - O(n)
var sb strings.Builder
sb.Grow(estimatedSize)
for _, msg := range messages {
    sb.WriteString(msg.content)
    sb.WriteByte('\n')
}
return sb.String()
```

#### NEVER: Mutex in the Hot Render Path

```go
// ❌ NEVER DO THIS - blocks render thread!
func (m *Model) View() string {
    m.mu.Lock()
    defer m.mu.Unlock()
    return m.content
}

// ✅ DO THIS INSTEAD - lock-free atomic read
func (m *Model) View() string {
    return atomicLoadString(&m.content)
}

// Or use dirty flag pattern
func (m *Model) View() string {
    if !atomic.CompareAndSwapInt32(&m.dirty, 1, 0) {
        return m.cachedView
    }
    // Only lock when actually recomputing
    m.mu.Lock()
    result := computeView(m.content)
    m.mu.Unlock()
    m.cachedView = result
    return result
}
```

#### NEVER: Allocate in the Render Loop

```go
// ❌ NEVER DO THIS - GC pressure every frame!
func (m *Model) View() string {
    lines := make([]string, 0, len(m.items))  // Allocation!
    for _, item := range m.items {
        lines = append(lines, render(item))
    }
    return strings.Join(lines, "\n")
}

// ✅ DO THIS INSTEAD - reuse buffer
func (m *Model) View() string {
    if !m.dirty { return m.cached }
    
    m.lineBuf = m.lineBuf[:0]  // Reset, no allocation
    for _, item := range m.items {
        m.lineBuf = append(m.lineBuf, render(item))
    }
    m.cached = strings.Join(m.lineBuf, "\n")
    m.dirty = false
    return m.cached
}
```

#### NEVER: Compute Layout Every Frame

```go
// ❌ NEVER DO THIS - wrapping is expensive!
func (m *Model) View() string {
    wrapped := wrapLines(m.content, m.width)  // O(n) every frame!
    return strings.Join(wrapped, "\n")
}

// ✅ DO THIS INSTEAD - wrap once, cache result
func (m *Model) SetContent(content string) {
    m.content = content
    m.wrapped = wrapLines(content, m.width)  // Once!
    m.dirty = true
}

func (m *Model) View() string {
    if !m.dirty { return m.cached }
    m.cached = strings.Join(m.wrapped, "\n")  // Reuse wrapped
    m.dirty = false
    return m.cached
}
```

#### NEVER: Ignore Viewport Width in Cache Key

```go
// ❌ NEVER DO THIS - breaks on resize!
func (c *Cache) Get(content string) []string {
    hash := fnvHash(content)  // Missing width!
    return c.entries[hash]
}

// ✅ DO THIS INSTEAD - include all rendering parameters
func (c *Cache) Get(content string, width int) []string {
    hash := fnvHash(content, width)  // Width included
    return c.entries[hash]
}
```

#### NEVER: Clear Cache on Every Frame

```go
// ❌ NEVER DO THIS - defeats the purpose of caching!
func (m *Model) View() string {
    m.cache = make(map[string]string)  // Clearing!
    return m.renderWithCache()
}

// ✅ DO THIS INSTEAD - only evict when necessary
func (c *Cache) MaybeEvict() {
    if len(c.entries) > c.maxSize {
        // LRU or simple random eviction
        for key := range c.entries {
            delete(c.entries, key)
            if len(c.entries) < c.targetSize {
                break
            }
        }
    }
}
```

---

### Performance Checklist

Before submitting any rendering code, verify:

- [ ] **Dirty flag exists** for all dynamic content
- [ ] **Dirty flag is atomic** (not plain bool with mutex)
- [ ] **Dirty flag checked BEFORE any computation**
- [ ] **Content is cached** based on hash (not reference)
- [ ] **Cache key includes** all rendering parameters (width, style, etc.)
- [ ] **Layout is precomputed** (not recomputed every frame)
- [ ] **Buffers are reused** (not allocated every frame)
- [ ] **No string concatenation** in loops
- [ ] **No mutex in hot path** (use atomics or dirty flags)
- [ ] **Benchmark before and after** to verify improvement

---

### Benchmark Template

Always measure the impact of optimizations:

```go
func BenchmarkRenderBefore(b *testing.B) {
    m := setupModel()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        renderWithoutOptimization(m)
    }
}

func BenchmarkRenderAfter(b *testing.B) {
    m := setupModel()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        renderWithOptimization(m)
    }
}

// Run: go test -bench=. -benchmem
```

Look for:
- **ns/op** - time per operation (lower is better)
- **B/op** - bytes allocated per operation (lower is better)
- **allocs/op** - number of allocations (lower is better)

---

### Performance Metrics to Monitor

| Metric | Good | Bad | Critical |
|--------|------|-----|----------|
| Idle CPU | <1% | 5-10% | >15% |
| Frame time (clean) | <0.5ms | 5-10ms | >16ms |
| Frame time (dirty) | <16ms | 16-32ms | >50ms |
| Allocations/frame | <1KB | 10-100KB | >1MB |
| Cache hit rate | >90% | 50-90% | <50% |
| Dirty frame ratio | <5% | 5-20% | >50% |

---

### The Golden Rule

> **"Optimize for the common case (idle), not the worst case."**

Most TUI apps spend >95% of time idle. If you can make idle frames cost O(1), you've won. The dirty flag pattern achieves exactly this:

$$E[T] = p \cdot T_{\text{full}} + (1-p) \cdot T_{O(1)}$$

Where $p$ is the probability of content change (~2%). This gives ~50x average speedup.

---

### Quick Reference: Optimization Patterns

```
┌─────────────────────────────────────────────────────────────────┐
│                    RENDERING OPTIMIZATION CHEATSHEET            │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  1. IDLE FRAMES MUST BE O(1)                                    │
│     - Check dirty flag BEFORE any computation                   │
│     - Return early if clean                                     │
│     - Use atomic.CompareAndSwapInt32(&dirty, 1, 0)              │
│                                                                 │
│  2. CACHE BY CONTENT HASH                                       │
│     - hash = fnv(content + width + style)                       │
│     - Store: cache[hash] = rendered                             │
│     - Include ALL rendering parameters in hash                  │
│                                                                 │
│  3. PRECOMPUTE LAYOUT                                           │
│     - Wrap lines once, not every frame                          │
│     - Invalidate on width change                                │
│     - Cache wrapped lines separately                            │
│                                                                 │
│  4. ATOMIC STATE UPDATES                                        │
│     - atomic.StoreInt32(&dirty, 1)                              │
│     - atomic.CompareAndSwapInt32(&dirty, 1, 0)                  │
│     - Never mutex in hot path                                   │
│                                                                 │
│  5. REUSE BUFFERS                                               │
│     - buf = buf[:0] instead of make()                           │
│     - Use sync.Pool for temps                                   │
│     - strings.Builder for concatenation                         │
│                                                                 │
│  6. MEASURE EVERYTHING                                          │
│     - Frame time, cache hits, allocations                       │
│     - go test -bench=. -benchmem                                │
│     - Profile before and after                                  │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```
