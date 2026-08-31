package hooks

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

func TestHookToolsNoPermissionRequired(t *testing.T) {
	ht := &HookTools{}
	for _, tool := range ht.GetTools() {
		pt, ok := tool.(tools.PermissionedTool)
		if !ok {
			continue
		}
		perms := pt.RequiresPermission()
		if len(perms) != 0 {
			t.Errorf("Expected hook tool %q to require no permissions (hooks always allowed), got %v", tool.Name(), perms)
		}
	}
}
