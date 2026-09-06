// Package builtin — thin wrappers around the canonical TaskManage-event
// parsing helpers. The name classifiers (isTaskManageTool,
// normalizedTaskTool) delegate to the base internal/hooks package (see
// hooks/toolclass.go), shared with internal/skills/autogenskills. The
// operation-parsing helpers delegate to internal/tools/ii instead — ii
// cannot depend on internal/hooks (internal/hooks, via internal/tools,
// already depends on ii; the reverse would be an import cycle), so ii
// exposes these as plain map[string]any-based functions
// (see ii/task_manage_event.go) that both this package and autogenskills
// call directly with event.Data.
package builtin

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

func isTaskManageTool(name string) bool {
	return hooks.IsTaskManageTool(name)
}

func taskManageOperations(event hooks.Event) ([]ii.TaskOperation, bool) {
	return ii.TaskManageEventOperations(event.Data)
}

func successfulTaskManageOperations(event hooks.Event) ([]ii.TaskOperation, map[string]string, bool) {
	return ii.SuccessfulTaskManageEventOperations(event.Data)
}

func taskManageHasKind(event hooks.Event, kinds ...ii.TaskOperationKind) bool {
	return ii.TaskManageEventHasKind(event.Data, kinds...)
}

func taskManageIsReadOnly(event hooks.Event) bool {
	return ii.TaskManageEventIsReadOnly(event.Data)
}

func taskTargetID(target *ii.TaskTarget) string {
	if target == nil {
		return ""
	}
	return target.ID
}

func normalizedTaskTool(name string) string {
	return hooks.NormalizeToolName(name)
}
