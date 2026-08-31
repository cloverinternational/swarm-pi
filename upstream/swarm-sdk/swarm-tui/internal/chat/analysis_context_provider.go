package chat

import (
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// analysisContextProvider implements builtin.AnalysisContextProvider
// by pulling context from the live TUI session.
type analysisContextProvider struct {
	getConversationMessages func(n int) []string
}

// GetRecentMessages returns the last N conversation messages as "role: content" summaries.
func (p *analysisContextProvider) GetRecentMessages(n int) []string {
	if p.getConversationMessages != nil {
		return p.getConversationMessages(n)
	}
	return nil
}

// GetActivePlan returns the full task plan from the task system.
func (p *analysisContextProvider) GetActivePlan() string {
	mgr := ii.GetTodoManager()
	if mgr == nil {
		return ""
	}
	var planParts []string
	for _, task := range mgr.Todos() {
		status := string(task.Status)
		if status == "" {
			status = "pending"
		}
		cat := string(task.Category)
		if cat == "" {
			cat = "acting"
		}
		entry := fmt.Sprintf("[%s][%s] #%s %s", status, cat, task.ID, task.Content)
		planParts = append(planParts, entry)
	}
	if len(planParts) == 0 {
		return ""
	}
	return strings.Join(planParts, "\n")
}

// GetTaskHistory returns ALL tasks with full details.
func (p *analysisContextProvider) GetTaskHistory() []string {
	mgr := ii.GetTodoManager()
	if mgr == nil {
		return nil
	}
	var history []string
	for _, task := range mgr.Todos() {
		status := string(task.Status)
		if status == "" {
			status = "pending"
		}
		cat := string(task.Category)
		if cat == "" {
			cat = "acting"
		}
		entry := fmt.Sprintf("[%s][%s] #%s %s", status, cat, task.ID, task.Content)
		if len(task.DependsOn) > 0 {
			entry += fmt.Sprintf(" (blocked_by: %s)", strings.Join(task.DependsOn, ","))
		}
		if len(task.Blocks) > 0 {
			entry += fmt.Sprintf(" (blocks: %s)", strings.Join(task.Blocks, ","))
		}
		if task.OwnerID != "" {
			entry += fmt.Sprintf(" (owner: %s)", task.OwnerID)
		}
		history = append(history, entry)
	}
	return history
}
