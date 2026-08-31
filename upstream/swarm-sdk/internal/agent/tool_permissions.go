package agent

import (
	"context"
	"fmt"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

func preflightToolExecution(ctx context.Context, registry tools.Registry, tool tools.Tool, name string, params map[string]any, agentID string, conversationID string, mode string) error {
	// Validate parameters if the tool opts-in to validation.
	if vt, ok := tool.(tools.ValidatableTool); ok {
		if err := vt.Validate(params); err != nil {
			return sdkerr.Permanent("tool.invalid_parameters",
				fmt.Sprintf("validation failed for %s: %v", name, err))
		}
	}

	// Check permission requirements if the tool declares any.
	var required []tools.Permission
	if pt, ok := tool.(tools.PermissionedTool); ok {
		required = pt.RequiresPermission()
	}
	if len(required) == 0 {
		return nil
	}

	provider, ok := registry.(tools.PermissionContextProvider)
	if !ok || provider.PermissionChecker() == nil {
		return sdkerr.Permanent("tool.permission_checker_missing",
			fmt.Sprintf("permission checker not configured for tool %s", name))
	}

	scope := tools.ScopeGlobal
	scopeID := ""
	if reg, ok := provider.Registration(name); ok && reg != nil {
		scope = reg.Scope
		scopeID = reg.ScopeID
	}

	ctxData := tools.BuildPermissionContext(
		name,
		params,
		required,
		scope,
		scopeID,
		agentID,
		conversationID,
		mode,
	)

	if !provider.PermissionChecker().CheckWithContext(ctx, required, ctxData) {
		return sdkerr.Permanent("tool.permission_denied",
			fmt.Sprintf("permission denied for tool %s", name))
	}

	return nil
}
