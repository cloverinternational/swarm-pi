package chat

import (
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// HasPermission reports whether the given permission is statically allowed under
// the current config WITHOUT triggering the interactive broker.  Use this for
// internal guard-checks (hook registration, feature flags, etc.) that must not
// pop up an approval modal.  Tool execution uses CheckWithContext instead.
func (sdk *SDKIntegration) HasPermission(permission tools.Permission) bool {
	if sdk.permissionChecker == nil {
		return false
	}
	// Use a non-interactive policy evaluation: check session grants first, then
	// evaluate the effective merged config directly without calling the broker.
	if sdk.permissionChecker.HasSessionGrant(permission) {
		return true
	}
	effective := sdk.permissionChecker.EffectiveConfig(nil)
	req := tools.BuildPermissionEvaluationRequest([]tools.Permission{permission}, nil)
	engine := tools.NewPermissionEngine(effective)
	decision := engine.Evaluate(req)
	return decision.Policy == tools.PolicyAllow || decision.Policy == tools.PolicySandbox
}

// PermissionPolicies returns the active permission policies.
func (sdk *SDKIntegration) PermissionPolicies() map[tools.Permission]tools.PermissionPolicy {
	if sdk.permissionConfig == nil {
		return tools.DefaultPermissionPolicies()
	}
	return sdk.permissionConfig.PoliciesMap()
}

// UpdatePermissionPolicy updates a permission policy and persists it.
func (sdk *SDKIntegration) UpdatePermissionPolicy(permission tools.Permission, policy tools.PermissionPolicy) error {
	if sdk.permissionConfig == nil {
		return fmt.Errorf("permission config not initialized")
	}
	sdk.permissionConfig.SetPolicy(permission, policy)
	if err := sdk.permissionConfig.Save(); err != nil {
		return err
	}
	if sdk.permissionChecker != nil {
		sdk.permissionChecker.SetConfig(sdk.permissionConfig.Config())
	}
	return nil
}

// UpdatePermissionLevel updates the permission level and persists it.
func (sdk *SDKIntegration) UpdatePermissionLevel(level tools.PermissionLevel) error {
	if sdk.permissionConfig == nil {
		return fmt.Errorf("permission config not initialized")
	}
	config := sdk.permissionConfig.Config()
	config.Level = level
	sdk.permissionConfig.SetConfig(config)
	if err := sdk.permissionConfig.Save(); err != nil {
		return err
	}
	if sdk.permissionChecker != nil {
		sdk.permissionChecker.SetConfig(sdk.permissionConfig.Config())
	}
	return nil
}

// WorkspaceRoot returns the current workspace root for sandboxing.
func (sdk *SDKIntegration) WorkspaceRoot() string {
	if sdk.sdkClient != nil {
		if dir := sdk.sdkClient.WorkspaceDir(); dir != "" {
			return dir
		}
	}
	return sdk.workspaceRoot
}

// ProjectRoot returns the stable primary checkout used for project identity.
func (sdk *SDKIntegration) ProjectRoot() string {
	if sdk.projectRoot != "" {
		return sdk.projectRoot
	}
	return sdk.WorkspaceRoot()
}

// PermissionFullConfig returns the full permission config (for rules + overrides).
func (sdk *SDKIntegration) PermissionFullConfig() tools.PermissionConfig {
	if sdk.permissionConfig == nil {
		return tools.DefaultPermissionConfig()
	}
	return sdk.permissionConfig.Config()
}

// ProjectPermissionConfig returns the project-level permission config.
func (sdk *SDKIntegration) ProjectPermissionConfig() tools.PermissionConfig {
	if sdk.workspaceRoot == "" {
		return tools.PermissionConfig{}
	}
	config, err := LoadProjectPermissionConfig(sdk.workspaceRoot)
	if err != nil {
		logDebug("Failed to load project permission config: %v", err)
		return tools.PermissionConfig{}
	}
	return config
}

// AddPermissionRule adds a permission rule to the global or project config.
func (sdk *SDKIntegration) AddPermissionRule(rule tools.PermissionRule, workspace bool) error {
	if workspace {
		if sdk.workspaceRoot == "" {
			return fmt.Errorf("no workspace root configured")
		}
		projectConfig, err := LoadProjectPermissionConfig(sdk.workspaceRoot)
		if err != nil {
			return fmt.Errorf("failed to load project config: %w", err)
		}
		projectConfig.Rules = append(projectConfig.Rules, rule)
		if err := SaveProjectPermissionConfig(sdk.workspaceRoot, projectConfig); err != nil {
			return fmt.Errorf("failed to save project config: %w", err)
		}
		// Update the checker's project config
		if sdk.permissionChecker != nil {
			sdk.permissionChecker.SetProjectConfig(projectConfig)
		}
		return nil
	}

	// Global config
	if sdk.permissionConfig == nil {
		return fmt.Errorf("permission config not initialized")
	}
	config := sdk.permissionConfig.Config()
	config.Rules = append(config.Rules, rule)
	sdk.permissionConfig.SetConfig(config)
	if err := sdk.permissionConfig.Save(); err != nil {
		return err
	}
	if sdk.permissionChecker != nil {
		sdk.permissionChecker.SetConfig(config)
	}
	return nil
}

// DeletePermissionRule removes a permission rule by index from the global or project config.
func (sdk *SDKIntegration) DeletePermissionRule(ruleIndex int, workspace bool) error {
	if workspace {
		if sdk.workspaceRoot == "" {
			return fmt.Errorf("no workspace root configured")
		}
		projectConfig, err := LoadProjectPermissionConfig(sdk.workspaceRoot)
		if err != nil {
			return fmt.Errorf("failed to load project config: %w", err)
		}
		if ruleIndex < 0 || ruleIndex >= len(projectConfig.Rules) {
			return fmt.Errorf("rule index out of range: %d", ruleIndex)
		}
		projectConfig.Rules = append(projectConfig.Rules[:ruleIndex], projectConfig.Rules[ruleIndex+1:]...)
		if err := SaveProjectPermissionConfig(sdk.workspaceRoot, projectConfig); err != nil {
			return fmt.Errorf("failed to save project config: %w", err)
		}
		if sdk.permissionChecker != nil {
			sdk.permissionChecker.SetProjectConfig(projectConfig)
		}
		return nil
	}

	// Global config
	if sdk.permissionConfig == nil {
		return fmt.Errorf("permission config not initialized")
	}
	config := sdk.permissionConfig.Config()
	if ruleIndex < 0 || ruleIndex >= len(config.Rules) {
		return fmt.Errorf("rule index out of range: %d", ruleIndex)
	}
	config.Rules = append(config.Rules[:ruleIndex], config.Rules[ruleIndex+1:]...)
	sdk.permissionConfig.SetConfig(config)
	if err := sdk.permissionConfig.Save(); err != nil {
		return err
	}
	if sdk.permissionChecker != nil {
		sdk.permissionChecker.SetConfig(config)
	}
	return nil
}

// DeleteToolOverride removes a tool override from the global config.
func (sdk *SDKIntegration) DeleteToolOverride(tool string) error {
	if sdk.permissionConfig == nil {
		return fmt.Errorf("permission config not initialized")
	}
	config := sdk.permissionConfig.Config()
	if config.Overrides.Tools == nil {
		return nil
	}
	delete(config.Overrides.Tools, tool)
	sdk.permissionConfig.SetConfig(config)
	if err := sdk.permissionConfig.Save(); err != nil {
		return err
	}
	if sdk.permissionChecker != nil {
		sdk.permissionChecker.SetConfig(config)
	}
	return nil
}
