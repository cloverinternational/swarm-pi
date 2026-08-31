package todo

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// TodoRenderer renders TaskManage results as a compact operation group and
// retains parsing support for historical todo result shapes.
type TodoRenderer struct{}

// New creates a new TodoRenderer.
func New() *TodoRenderer {
	return &TodoRenderer{}
}

// statusIcons maps todo status strings to styled icon characters.
var statusIcons = map[string]string{
	"pending":     shared.AnsiFgDim + "\u25cb" + shared.AnsiReset,         // ○
	"in_progress": "\x1b[38;2;249;226;175m" + "\u25c9" + shared.AnsiReset, // ◉ (yellow)
	"completed":   shared.AnsiFgGreen + "\u2713" + shared.AnsiReset,       // ✓
}

// priorityBadges maps priority strings to styled badge characters.
var priorityBadges = map[string]string{
	"high":   shared.AnsiFgRed + "\u25b2" + shared.AnsiReset,         // ▲ (red)
	"medium": "\x1b[38;2;249;226;175m" + "\u2500" + shared.AnsiReset, // ─ (yellow)
	"low":    shared.AnsiFgDim + "\u25bd" + shared.AnsiReset,         // ▽ (dim)
}

// CanRender returns true for TaskManage and historical todo/task tools whose
// output still needs to be displayed during migration.
func (r *TodoRenderer) CanRender(ctx *toolrender.RenderContext) bool {
	name := ctx.ToolName

	// Direct name matches
	switch name {
	case "TaskManage", "TodoWrite", "TodoRead", "todo_write", "todo_read", "TodoUpdate",
		"TaskCreate", "TaskUpdate", "TaskGet", "TaskList":
		return true
	}

	// MCP tool name patterns
	lower := strings.ToLower(name)
	if strings.Contains(lower, "todo") {
		return true
	}

	// Content-based fallback: JSON with "id", "status", and "content"
	if ctx.Output != "" {
		output := strings.TrimSpace(ctx.Output)
		if (strings.HasPrefix(output, "{") || strings.HasPrefix(output, "[")) &&
			strings.Contains(output, `"id"`) &&
			strings.Contains(output, `"status"`) &&
			strings.Contains(output, `"content"`) {
			return true
		}
	}

	return false
}

// Render returns styled output lines for todo tool results.
// It parses the output and renders todo items with status icons and priority badges.
func (r *TodoRenderer) Render(ctx *toolrender.RenderContext, _ toolrender.CachedResult) []string {
	// WHY THE FINAL CLAMP: the three layout paths below each size their content
	// with a FLOOR rather than a cap — renderTaskBatch uses max(width-2, 24),
	// renderTodoItems uses max(width-20, 20), and renderTextOutput does not
	// measure at all. On any pane narrower than those floors they emit content
	// wider than the viewport, and the six-column "    ⎿ " gutter plus the
	// status icon and priority badge sit on top of that. The fully assembled
	// line is the only place all of those are visible, so it is the only place
	// the bound can be enforced.
	//
	// FitLines also expands tabs: task content is model-authored text, and a
	// tab in it measures one column but draws up to four, shearing the icon
	// column of every line to its right.
	return shared.FitLines(r.render(ctx), ctx.Width)
}

// render produces the unclamped lines; Render applies the viewport bound.
func (r *TodoRenderer) render(ctx *toolrender.RenderContext) []string {
	output := ctx.Output
	width := ctx.Width

	if output == "" {
		return []string{"    " + shared.AnsiFgMuted + "\u23bf" + shared.AnsiReset + " " + shared.AnsiFgDim + i18n.T("toolrender.todo.no_todos") + shared.AnsiReset}
	}

	var batch taskBatchResult
	if err := json.Unmarshal([]byte(output), &batch); err == nil && batch.Status != "" && batch.Results != nil {
		return r.renderTaskBatch(batch, width)
	}
	if strings.EqualFold(ctx.ToolName, "TaskManage") {
		return []string{"    " + shared.AnsiFgMuted + "⎿" + shared.AnsiReset + " " + shared.AnsiFgDim + i18n.T("toolrender.todo.state_unavailable") + shared.AnsiReset}
	}

	// Historical result shapes remain readable while stored conversations age out.
	var todoItems []todoItem
	if err := json.Unmarshal([]byte(output), &todoItems); err == nil && len(todoItems) > 0 {
		return r.renderTodoItems(todoItems, width)
	}

	// Try to parse as a single todo object
	var singleTodo todoItem
	if err := json.Unmarshal([]byte(output), &singleTodo); err == nil && singleTodo.Content != "" {
		return r.renderTodoItems([]todoItem{singleTodo}, width)
	}

	// Try to parse as a wrapper object with a "todos" array
	var wrapper struct {
		Todos []todoItem `json:"todos"`
	}
	if err := json.Unmarshal([]byte(output), &wrapper); err == nil && len(wrapper.Todos) > 0 {
		return r.renderTodoItems(wrapper.Todos, width)
	}

	// Fall back to line-based text parsing
	return r.renderTextOutput(output, width)
}

// PreProcess returns nil. Todo output is fast enough to render live.
func (r *TodoRenderer) PreProcess(_ *toolrender.RenderContext) toolrender.CachedResult {
	return nil
}

// todoItem represents a single todo parsed from JSON.
type todoItem struct {
	ID        string   `json:"id"`
	Content   string   `json:"content"`
	Subject   string   `json:"subject"`
	Status    string   `json:"status"`
	Priority  string   `json:"priority"`
	DependsOn []string `json:"depends_on,omitempty"`
	Blocks    []string `json:"blocks,omitempty"`
}

type taskBatchResult struct {
	Status  string                `json:"status"`
	Results []taskOperationResult `json:"results"`
}

type taskOperationResult struct {
	Key    string              `json:"key"`
	Op     string              `json:"op"`
	Status string              `json:"status"`
	Data   *taskOperationData  `json:"data,omitempty"`
	Error  *taskOperationError `json:"error,omitempty"`
}

type taskOperationData struct {
	Task  *todoItem  `json:"task,omitempty"`
	Tasks []todoItem `json:"tasks,omitempty"`
}

type taskOperationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r *TodoRenderer) renderTaskBatch(batch taskBatchResult, width int) []string {
	taskOrder := make([]string, 0, len(batch.Results))
	tasksByID := make(map[string]todoItem, len(batch.Results))
	var errors []string
	anonymous := 0
	for _, operation := range batch.Results {
		if operation.Error != nil {
			message := operation.Error.Message
			if message == "" {
				message = i18n.T("toolrender.todo.update_failed")
			}
			errors = append(errors, message)
			continue
		}
		if operation.Status != "succeeded" || operation.Data == nil {
			continue
		}
		if operation.Data.Task != nil {
			anonymous = collectTaskResult(tasksByID, &taskOrder, *operation.Data.Task, anonymous)
			continue
		}
		for _, task := range operation.Data.Tasks {
			anonymous = collectTaskResult(tasksByID, &taskOrder, task, anonymous)
		}
	}
	lines := make([]string, 0, len(taskOrder)+len(errors))
	for _, id := range taskOrder {
		lines = append(lines, renderTaskResult(tasksByID[id], width, len(lines) == 0))
	}
	for _, message := range errors {
		line := taskResultPrefix(len(lines) == 0) + shared.AnsiFgRed + "✗ " + message + shared.AnsiReset
		lines = append(lines, shared.TruncateANSI(line, max(width-2, 24), "…")+shared.AnsiReset)
	}
	if len(lines) == 0 {
		return []string{"    " + shared.AnsiFgMuted + "⎿" + shared.AnsiReset + " " + shared.AnsiFgDim + i18n.T("toolrender.todo.unchanged") + shared.AnsiReset}
	}
	return lines
}

func collectTaskResult(tasks map[string]todoItem, order *[]string, task todoItem, anonymous int) int {
	key := task.ID
	if key == "" {
		key = fmt.Sprintf("__anonymous_%d", anonymous)
		anonymous++
	}
	if _, exists := tasks[key]; !exists {
		*order = append(*order, key)
	}
	tasks[key] = task
	return anonymous
}

func renderTaskResult(task todoItem, width int, first bool) string {
	icon := statusIcons[task.Status]
	if icon == "" {
		icon = shared.AnsiFgDim + "?" + shared.AnsiReset
	}
	content := task.Content
	if content == "" {
		content = task.Subject
	}
	if content == "" {
		content = i18n.T("toolrender.todo.task_number", task.ID)
	}
	if task.ID != "" {
		content = "#" + task.ID + " " + content
	}
	prefix := taskResultPrefix(first)
	contentWidth := max(width-shared.PrintableWidth(prefix)-4, 1)
	content = shared.TruncateANSI(content, contentWidth, "…")
	if task.Status == "completed" {
		content = shared.AnsiStrikethroughDim + content + shared.AnsiReset
	} else if task.Status == "in_progress" {
		content = shared.AnsiBold + content + shared.AnsiReset
	}
	return prefix + icon + " " + content + shared.AnsiReset
}

func taskResultPrefix(first bool) string {
	if first {
		return "    " + shared.AnsiFgMuted + "⎿" + shared.AnsiReset + " "
	}
	return "      "
}

// renderTodoItems renders a slice of parsed todo items.
func (r *TodoRenderer) renderTodoItems(items []todoItem, width int) []string {
	// Build a status map for checking if blockers are resolved
	statusByID := make(map[string]string, len(items))
	for _, item := range items {
		statusByID[item.ID] = item.Status
	}

	var result []string

	maxContent := max(width-20, 20)

	for i, item := range items {
		icon := statusIcons[item.Status]
		if icon == "" {
			icon = shared.AnsiFgDim + "?" + shared.AnsiReset
		}
		badge := priorityBadges[item.Priority]
		if badge == "" {
			badge = " "
		}

		// Determine if this task has unmet dependencies (is blocked)
		hasUnmetDeps := false
		if len(item.DependsOn) > 0 {
			for _, depID := range item.DependsOn {
				if status, ok := statusByID[depID]; ok && status != "completed" {
					hasUnmetDeps = true
					break
				}
			}
		}

		// Build dependency indicator
		var depInfo string
		if hasUnmetDeps {
			// Blocked: show lock icon
			depInfo = " " + shared.AnsiFgDim + "\U0001f512" + shared.AnsiReset
		}

		// Truncate content if needed
		content := item.Content
		// Width-aware, not byte-aware: len() counts bytes, so `content[:n]`
		// both mismeasured wide runes (two columns but three-plus bytes each)
		// and could slice a UTF-8 sequence in half, putting invalid bytes into
		// the frame.
		content = shared.ExpandTabsANSI(content)
		if shared.PrintableWidth(content) > maxContent {
			content = shared.TruncateANSI(content, maxContent, "...")
		}

		// Style content based on status
		var contentStyled string
		switch item.Status {
		case "completed":
			contentStyled = shared.AnsiStrikethroughDim + content + shared.AnsiReset
		case "in_progress":
			contentStyled = shared.AnsiBold + content + shared.AnsiReset
		default:
			contentStyled = content
		}

		prefix := "      "
		if i == 0 {
			prefix = "    " + shared.AnsiFgMuted + "\u23bf" + shared.AnsiReset + " "
		}

		line := fmt.Sprintf("%s%s %s %s%s", prefix, icon, badge, contentStyled, depInfo)

		// Add inline dependency info
		if len(item.DependsOn) > 0 && item.Status != "completed" {
			depIDs := strings.Join(item.DependsOn, ", ")
			line += " " + shared.AnsiFgDim + i18n.T("toolrender.todo.depends_on", depIDs) + shared.AnsiReset
		}

		result = append(result, line)
	}

	return result
}

// renderTextOutput handles non-JSON output by parsing it line by line.
func (r *TodoRenderer) renderTextOutput(output string, _ int) []string {
	lines := strings.Split(output, "\n")
	var result []string
	summaryRendered := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Skip JSON structural characters
		if trimmed == "[" || trimmed == "]" || trimmed == "{" || trimmed == "}," || trimmed == "}" {
			continue
		}

		// "Updated:" or "Summary:" lines
		if strings.HasPrefix(trimmed, "Updated:") || strings.HasPrefix(trimmed, "Summary:") {
			if len(result) == 0 {
				result = append(result, "    "+shared.AnsiFgMuted+"\u23bf"+shared.AnsiReset+" "+shared.AnsiFgDim+trimmed+shared.AnsiReset)
			} else {
				result = append(result, "      "+shared.AnsiFgDim+trimmed+shared.AnsiReset)
			}
			continue
		}

		// First meaningful line is a summary/status message
		if !summaryRendered {
			result = append(result, "    "+shared.AnsiFgMuted+"\u23bf"+shared.AnsiReset+" "+shared.AnsiFgDim+trimmed+shared.AnsiReset)
			summaryRendered = true
			continue
		}

		// Subsequent lines
		result = append(result, "      "+trimmed)
	}

	// If nothing rendered, show raw fallback
	if len(result) == 0 {
		firstLine := true
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if firstLine {
				result = append(result, "    "+shared.AnsiFgMuted+"\u23bf"+shared.AnsiReset+" "+trimmed)
				firstLine = false
			} else {
				result = append(result, "      "+trimmed)
			}
		}
	}

	return result
}
