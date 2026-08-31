# Sub-Agent Table Rendering Plan

## Overview

Based on the SDK implementation in `swarm-sdk/tools/ii/shared_state.go`, this document describes how the TUI should render sub-agent progress in a compact table format.

## SDK Implementation (DONE)

The SDK already has owner-based task tracking:

### TodoItem Fields
```go
type TodoItem struct {
    ID       string
    Content  string
    Status   TodoStatus      // pending, in_progress, completed
    Priority TodoPriority
    DependsOn []string       // Dependencies
    Blocks   []string        // Reverse dependencies (computed)
    OwnerID  string          // ✅ Links task to sub-agent
    Sequence int             // ✅ Order within owner group
}
```

### TodoManager Methods for Sub-Agent Tracking
```go
// Set the current owner context (called by subagent executor)
SetContextOwner(ownerID string)

// Get all tasks for a specific owner
GetByOwner(ownerID string) []TodoItem

// Get progress summary: (completed, total, current_task)
GetOwnerProgress(ownerID string) (completed, total int, current string)

// Clear all tasks for an owner (when sub-agent finishes)
ClearOwner(ownerID string)

// Set callback for progress updates
SetProgressCallback(cb ProgressCallback)
type ProgressCallback func(ownerID string, completed, total int, current string)

// Context helpers
ContextWithTodoManager(ctx, tm) context.Context
TodoManagerFromContext(ctx) *TodoManager
```

---

## Data Model

### SubAgentTable

```go
// internal/chat/subagent/table.go

// SubAgentTable maintains state for rendering multiple sub-agents in a compact table.
type SubAgentTable struct {
    rows       []*SubAgentRow
    allDone    bool
    lastUpdate time.Time
}

// SubAgentRow represents one sub-agent in the table.
type SubAgentRow struct {
    OwnerID      string       // Matches TodoManager owner ID
    AgentName    string       // Human-readable name
    Status       AgentStatus  // working, streaming, complete, error
    TaskSummary  string       // Truncated task description
    CurrentTodo  string       // Content of in-progress todo (from GetOwnerProgress)
    Progress     Progress     // Todo completion progress
}

type AgentStatus string

const (
    StatusWorking   AgentStatus = "working"
    StatusStreaming AgentStatus = "streaming"
    StatusComplete  AgentStatus = "complete"
    StatusError     AgentStatus = "error"
)

type Progress struct {
    Completed int
    Total     int
}
```

### ProgressCallback Integration

The TUI should register a `ProgressCallback` with the TodoManager:

```go
// In app initialization or sdk_integration.go
todoManager.SetProgressCallback(func(ownerID string, completed, total int, current string) {
    // Dispatch to TUI update channel
    m.progressUpdates <- ProgressUpdate{
        OwnerID:   ownerID,
        Completed: completed,
        Total:     total,
        Current:   current,
    }
})
```

---

## Rendering

### When Multiple Sub-Agents Active (3+)

```
┌────────────────────────────────────────────────────────────────────────────┐
│ ⚡ Sub-Agents                                                    3 active │
├──────────────┬──────────┬───────────────────────────────┬─────────────────┤
│ Agent        │ Status   │ Current Task / Todo           │ Progress        │
├──────────────┼──────────┼───────────────────────────────┼─────────────────┤
│ code_fmt     │ ⏳ work  │ Applying gofmt to auth.go     │ ████████░░ 2/3  │
│ text_sum     │ ✓ done   │ Summarize README              │ ██████████ 3/3  │
│ data_val     │ ⏳ work  │ Validating config.yaml        │ ████░░░░░░ 1/2  │
└──────────────┴──────────┴───────────────────────────────┴─────────────────┘
```

### When 1-2 Sub-Agents Active

```
┌────────────────────────────────────────────────────────────────────────────┐
│ ⚡ code_formatter                                                          │
├────────────────────────────────────────────────────────────────────────────┤
│ Task: Format the auth module                                               │
│                                                                            │
│ Progress: ████████░░░░░░░░ 2/3                                             │
│ Current: Applying gofmt to auth.go...                                      │
└────────────────────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────────────────────┐
│ ⚡ text_summarizer                                                         │
├────────────────────────────────────────────────────────────────────────────┤
│ Task: Summarize README                                                     │
│                                                                            │
│ Progress: ████████████████ 3/3 ✓                                           │
│ Complete!                                                                  │
└────────────────────────────────────────────────────────────────────────────┘
```

### When All Complete

```
┌────────────────────────────────────────────────────────────────────────────┐
│ ✓ 3 sub-agents complete                                                    │
│                                                                            │
│ code_formatter · text_summarizer · data_validator                         │
└────────────────────────────────────────────────────────────────────────────┘
```

---

## Column Specifications

### Agent Column (12 chars)
- Show truncated agent name (e.g., "code_fmt" for "code_formatter")
- Icon prefix: ⏳ for working, ✓ for complete, ✗ for error

### Status Column (10 chars)
- "work" - working
- "stream" - streaming content
- "done" - complete
- "error" - failed

### Current Task / Todo Column (flexible)
- Show current in-progress todo subject
- If no todo progress, show truncated task description
- Max width: remaining space minus progress column

### Progress Column (12 chars)
- Visual bar: ████████░░ 2/3
- If complete: ██████████ ✓
- If no todos: just show status

---

## Update Flow

### 1. Register Progress Callback with SDK

```go
// In app initialization or sdk_integration.go
func (m *Model) setupTodoProgressCallback() {
    tm := ii.GetTodoManager() // or get from context
    
    tm.SetProgressCallback(func(ownerID string, completed, total int, current string) {
        // Dispatch to TUI update channel
        m.progressUpdates <- ProgressUpdate{
            OwnerID:   ownerID,
            Completed: completed,
            Total:     total,
            Current:   current,
        }
    })
}
```

### 2. Handle Progress Updates in TUI

```go
// In internal/chat/messages.go or similar

type ProgressUpdate struct {
    OwnerID   string
    Completed int
    Total     int
    Current   string
}

func (m *Model) handleProgressUpdate(update ProgressUpdate) {
    // Find the sub-agent row by matching ownerID
    for _, row := range m.subAgentTable.rows {
        if row.OwnerID == update.OwnerID {
            row.Progress.Completed = update.Completed
            row.Progress.Total = update.Total
            row.CurrentTodo = update.Current
            m.subAgentTable.lastUpdate = time.Now()
            break
        }
    }
}
```

### 3. Receive SubAgentUpdate from SDK (existing)

```go
func (m *Model) handleSubAgentUpdate(update agent.SubAgentUpdate) {
    // Use agent ID as owner ID
    ownerID := update.AgentID
    
    // Find or create row
    row := m.subAgentTable.GetOrCreate(ownerID, update.AgentName)
    
    // Update status based on inner update type
    switch update.Update.(type) {
    case agent.ContentUpdate:
        row.Status = StatusStreaming
    case agent.ToolCallUpdate:
        row.Status = StatusWorking
    case agent.SubAgentCompleteUpdate:
        row.Status = StatusComplete
    }
}
```

### 4. Query Progress on Demand (alternative to callback)

```go
func (m *Model) refreshProgress() {
    tm := ii.GetTodoManager()
    
    for _, row := range m.subAgentTable.rows {
        completed, total, current := tm.GetOwnerProgress(row.OwnerID)
        row.Progress.Completed = completed
        row.Progress.Total = total
        row.CurrentTodo = current
    }
}
```

---

## Implementation Files

| File | Purpose |
|------|---------|
| `internal/chat/subagent/table.go` | NEW - SubAgentTable struct and rendering |
| `internal/chat/subagent/row.go` | NEW - SubAgentRow rendering logic |
| `internal/chat/subagent/manager.go` | MODIFY - Track table state |
| `internal/chat/subagent/render.go` | MODIFY - Use table when multiple agents |
| `internal/chat/messages.go` | MODIFY - Handle TodoProgressUpdate |

---

## Code Skeleton

### `internal/chat/subagent/table.go`

```go
package subagent

import (
    "fmt"
    "strings"
    "time"
    
    "github.com/charmbracelet/lipgloss"
)

// SubAgentTable maintains state for multiple sub-agents.
type SubAgentTable struct {
    rows       []*SubAgentRow
    allDone    bool
    lastUpdate time.Time
}

// NewSubAgentTable creates a new table.
func NewSubAgentTable() *SubAgentTable {
    return &SubAgentTable{
        rows: make([]*SubAgentRow, 0),
    }
}

// AddRow adds a sub-agent row.
func (t *SubAgentTable) AddRow(row *SubAgentRow) {
    t.rows = append(t.rows, row)
    t.lastUpdate = time.Now()
}

// GetOrCreate finds or creates a row for the agent.
func (t *SubAgentTable) GetOrCreate(ownerID, agentName string) *SubAgentRow {
    for _, row := range t.rows {
        if row.OwnerID == ownerID {
            return row
        }
    }
    row := &SubAgentRow{
        OwnerID:   ownerID,
        AgentName: agentName,
        Status:    StatusWorking,
    }
    t.AddRow(row)
    return row
}

// UpdateProgress updates progress for an agent.
func (t *SubAgentTable) UpdateProgress(ownerID string, completed, total int, currentTodo string) {
    for _, row := range t.rows {
        if row.OwnerID == ownerID {
            row.Progress.Completed = completed
            row.Progress.Total = total
            row.CurrentTodo = currentTodo
            t.lastUpdate = time.Now()
            break
        }
    }
}

// Render renders the table at the given width.
func (t *SubAgentTable) Render(width int) string {
    if len(t.rows) == 0 {
        return ""
    }
    
    // Check if all done
    t.allDone = true
    for _, row := range t.rows {
        if row.Status != StatusComplete && row.Status != StatusError {
            t.allDone = false
            break
        }
    }
    
    // Collapsed view when all done
    if t.allDone {
        return t.renderCollapsed(width)
    }
    
    // Table view for multiple agents
    if len(t.rows) >= 3 {
        return t.renderTable(width)
    }
    
    // Individual boxes for 1-2 agents
    return t.renderIndividual(width)
}

func (t *SubAgentTable) renderCollapsed(width int) string {
    names := make([]string, len(t.rows))
    for i, row := range t.rows {
        names[i] = row.AgentName
    }
    
    header := fmt.Sprintf("✓ %d sub-agents complete", len(t.rows))
    body := strings.Join(names, " · ")
    
    // Use lipgloss to render bordered box
    style := lipgloss.NewStyle().
        Border(lipgloss.RoundedBorder()).
        BorderForeground(lipgloss.Color("62")).
        Padding(0, 1).
        Width(width - 2)
    
    content := header + "\n\n" + body
    return style.Render(content)
}

func (t *SubAgentTable) renderTable(width int) string {
    // Column widths
    agentCol := 12
    statusCol := 10
    progressCol := 14
    taskCol := width - agentCol - statusCol - progressCol - 5 // 5 for borders/separators
    
    if taskCol < 20 {
        taskCol = 20 // Minimum
    }
    
    var sb strings.Builder
    
    // Header
    active := 0
    for _, row := range t.rows {
        if row.Status == StatusWorking || row.Status == StatusStreaming {
            active++
        }
    }
    
    headerStyle := lipgloss.NewStyle().
        Bold(true).
        Foreground(lipgloss.Color("86"))
    
    sb.WriteString("┌")
    sb.WriteString(strings.Repeat("─", width-2))
    sb.WriteString("┐\n")
    
    sb.WriteString("│ ")
    sb.WriteString(headerStyle.Render(fmt.Sprintf("⚡ Sub-Agents%*d active", width-30, active)))
    sb.WriteString(strings.Repeat(" ", width-16-len(fmt.Sprintf("%d", active))))
    sb.WriteString(" │\n")
    
    // Column headers
    sb.WriteString("├")
    sb.WriteString(strings.Repeat("─", agentCol + 1))
    sb.WriteString("┬")
    sb.WriteString(strings.Repeat("─", statusCol + 1))
    sb.WriteString("┬")
    sb.WriteString(strings.Repeat("─", taskCol + 1))
    sb.WriteString("┬")
    sb.WriteString(strings.Repeat("─", progressCol + 1))
    sb.WriteString("┤\n")
    
    // Rows
    for _, row := range t.rows {
        sb.WriteString(row.RenderTableRow(agentCol, statusCol, taskCol, progressCol))
    }
    
    sb.WriteString("└")
    sb.WriteString(strings.Repeat("─", width-2))
    sb.WriteString("┘")
    
    return sb.String()
}

func (t *SubAgentTable) renderIndividual(width int) string {
    var boxes []string
    for _, row := range t.rows {
        boxes = append(boxes, row.RenderBox(width))
    }
    return strings.Join(boxes, "\n\n")
}
```

### `internal/chat/subagent/row.go`

```go
package subagent

import (
    "fmt"
    "strings"
    
    "github.com/charmbracelet/lipgloss"
)

type SubAgentRow struct {
    OwnerID     string       // Matches TodoManager owner ID
    AgentName   string       // Human-readable name
    Status      AgentStatus
    TaskSummary string
    CurrentTodo string
    Progress    Progress
}

func (r *SubAgentRow) RenderTableRow(agentW, statusW, taskW, progressW int) string {
    // Agent name (truncated)
    name := r.AgentName
    if len(name) > agentW-2 {
        name = name[:agentW-2] + "~"
    }
    
    // Status icon and text
    var statusIcon, statusText string
    switch r.Status {
    case StatusWorking:
        statusIcon = "⏳"
        statusText = "work"
    case StatusStreaming:
        statusIcon = "⏳"
        statusText = "stream"
    case StatusComplete:
        statusIcon = "✓"
        statusText = "done"
    case StatusError:
        statusIcon = "✗"
        statusText = "error"
    }
    
    // Task or current todo
    task := r.CurrentTodo
    if task == "" {
        task = r.TaskSummary
    }
    if len(task) > taskW-2 {
        task = task[:taskW-5] + "..."
    }
    
    // Progress bar
    progress := r.renderProgressBar(progressW - 4)
    
    return fmt.Sprintf("│ %-*s │ %s %-*s │ %-*s │ %-*s │\n",
        agentW-2, statusIcon+" "+name,
        statusW-4, statusText,
        taskW-2, task,
        progressW-2, progress,
    )
}

func (r *SubAgentRow) renderProgressBar(width int) string {
    if r.Progress.Total == 0 {
        return strings.Repeat(" ", width)
    }
    
    completed := r.Progress.Completed
    total := r.Progress.Total
    
    if completed >= total {
        return lipgloss.NewStyle().
            Foreground(lipgloss.Color("86")).
            Render(strings.Repeat("█", width/2) + " ✓")
    }
    
    filled := (width * completed) / total
    bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
    
    text := fmt.Sprintf("%s %d/%d", bar[:width-5], completed, total)
    
    return lipgloss.NewStyle().
        Foreground(lipgloss.Color("178")).
        Render(text)
}

func (r *SubAgentRow) RenderBox(width int) string {
    var content strings.Builder
    
    // Header
    content.WriteString(fmt.Sprintf("Task: %s\n", r.TaskSummary))
    content.WriteString("\n")
    
    // Progress
    if r.Progress.Total > 0 {
        progress := r.renderProgressBar(width - 10)
        content.WriteString(fmt.Sprintf("Progress: %s\n", progress))
        
        if r.CurrentTodo != "" {
            content.WriteString(fmt.Sprintf("Current: %s", r.CurrentTodo))
            if r.Status == StatusStreaming {
                content.WriteString("...")
            }
            content.WriteString("\n")
        }
    }
    
    if r.Status == StatusComplete {
        content.WriteString("\n✓ Complete!\n")
    }
    
    style := lipgloss.NewStyle().
        Border(lipgloss.RoundedBorder()).
        BorderForeground(lipgloss.Color("62")).
        Padding(0, 1).
        Width(width - 2)
    
    header := fmt.Sprintf("⚡ %s", r.AgentName)
    headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
    
    fullContent := headerStyle.Render(header) + "\n" + 
        strings.Repeat("─", width-4) + "\n" + 
        content.String()
    
    return style.Render(fullContent)
}
```

---

## Integration Points

### 1. Register Progress Callback with SDK

```go
// In app initialization or sdk_integration.go

func (app *App) setupProgressCallback() {
    tm := ii.GetTodoManager()
    
    tm.SetProgressCallback(func(ownerID string, completed, total int, current string) {
        // Send update to TUI via channel
        app.progressCh <- ProgressUpdate{
            OwnerID:   ownerID,
            Completed: completed,
            Total:     total,
            Current:   current,
        }
    })
}

// In Model Update function
case update := <-m.progressCh:
    m.subAgentTable.UpdateProgress(
        update.OwnerID,
        update.Completed,
        update.Total,
        update.Current,
    )
```

### 2. Handle SubAgentUpdate to create rows

```go
// When SubAgentUpdate is received
func (m *Model) handleSubAgentUpdate(update agent.SubAgentUpdate) {
    ownerID := update.AgentID  // Agent ID becomes the owner ID
    
    row := m.subAgentTable.GetOrCreate(ownerID, update.AgentName)
    
    switch update.Update.(type) {
    case agent.SubAgentCompleteUpdate:
        row.Status = StatusComplete
    case agent.ContentUpdate:
        row.Status = StatusStreaming
    default:
        row.Status = StatusWorking
    }
}
```

### 2. Render table in main view

```go
// In app_chat_render.go

func (m *Model) renderSubAgentSection() string {
    if m.subAgentTable == nil || len(m.subAgentTable.rows) == 0 {
        return ""
    }
    
    return m.subAgentTable.Render(m.width)
}
```

---

## Color Scheme

| Element | Color Code | Usage |
|---------|------------|-------|
| Header | 86 (bright cyan) | Table header, agent names |
| Working | 178 (yellow) | In-progress progress bars |
| Complete | 86 (bright cyan) | Checkmarks, completed bars |
| Error | 196 (red) | Error indicators |
| Border | 62 (blue) | Box borders |

---

## Testing

1. Unit test: SubAgentTable with 0, 1, 2, 3+ rows
2. Unit test: Progress bar rendering at various percentages
3. Unit test: Collapse behavior when all complete
4. Integration test: Receive TodoProgressUpdate, verify table updates
5. Visual test: Manual inspection of rendered output
