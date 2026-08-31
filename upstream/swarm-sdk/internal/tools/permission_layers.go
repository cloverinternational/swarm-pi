package tools

import "maps"

// MergePermissionConfigs overlays permission configs in order of increasing precedence.
// Later configs override earlier values where fields are set.
func MergePermissionConfigs(base PermissionConfig, overlays ...PermissionConfig) PermissionConfig {
	merged := clonePermissionConfig(base)
	for _, overlay := range overlays {
		applyPermissionConfig(&merged, overlay)
	}
	return normalizePermissionConfig(merged)
}

// EvaluatePermissionWithLayers evaluates a request against ordered config layers.
// Layers should be provided in increasing precedence order (global -> project -> mode -> agent -> session).
// Returns the decision and the effective merged config.
//
// Evaluation order:
//  1. Rules are evaluated from highest-precedence layer downward (first match wins).
//  2. If no rule matches, the merged effective config (overrides + defaults + level) decides.
//
// Note: the effective config returned includes all accumulated rules so callers can
// inspect them; the engine also receives them for consistent secondary evaluation.
func EvaluatePermissionWithLayers(request PermissionEvaluationRequest, layers []PermissionConfig) (PermissionDecision, PermissionConfig) {
	// Phase 1: rule match — iterate layers from highest to lowest precedence.
	// Layers with no rules are still considered for config merging below.
	for layerIndex := len(layers) - 1; layerIndex >= 0; layerIndex-- {
		layer := layers[layerIndex]
		if len(layer.Rules) == 0 {
			continue
		}
		sorted := sortRulesByPriority(layer.Rules)
		for _, rule := range sorted {
			if rule.When.Matches(request) {
				decision := decisionFromPolicy(rule.Then.Policy, rule.Then.Reason, "rule", rule.ID)
				// Build effective config but keep rules intact.
				effective := MergePermissionConfigs(DefaultPermissionConfig(), layers...)
				return decision, effective
			}
		}
	}

	// Phase 2: no rule matched — merge all layers and let the engine decide via
	// overrides, defaults, and the permission level (YOLO / balanced / always-ask …).
	// Rules are preserved in effective so callers can still inspect them.
	effective := MergePermissionConfigs(DefaultPermissionConfig(), layers...)
	engine := NewPermissionEngine(effective)
	return engine.Evaluate(request), effective
}

func applyPermissionConfig(target *PermissionConfig, overlay PermissionConfig) {
	if overlay.Version != 0 {
		target.Version = overlay.Version
	}
	if overlay.Level != "" {
		target.Level = overlay.Level
	}
	if overlay.TimeoutSeconds != 0 {
		target.TimeoutSeconds = overlay.TimeoutSeconds
	}
	if overlay.TimeoutBehavior != "" {
		target.TimeoutBehavior = overlay.TimeoutBehavior
	}
	if overlay.Defaults.Policies != nil {
		if target.Defaults.Policies == nil {
			target.Defaults.Policies = DefaultPermissionPolicies()
		}
		maps.Copy(target.Defaults.Policies, overlay.Defaults.Policies)
	}
	if overlay.Overrides.Tools != nil {
		if target.Overrides.Tools == nil {
			target.Overrides.Tools = make(map[string]OverridePolicy)
		}
		maps.Copy(target.Overrides.Tools, overlay.Overrides.Tools)
	}
	if overlay.Overrides.Permissions != nil {
		if target.Overrides.Permissions == nil {
			target.Overrides.Permissions = make(map[Permission]OverridePolicy)
		}
		maps.Copy(target.Overrides.Permissions, overlay.Overrides.Permissions)
	}
	if overlay.Rules != nil {
		target.Rules = append(target.Rules, overlay.Rules...)
	}
	if overlay.Metadata.UpdatedAt != "" {
		target.Metadata.UpdatedAt = overlay.Metadata.UpdatedAt
	}
	if overlay.Metadata.Source != "" {
		target.Metadata.Source = overlay.Metadata.Source
	}
}

func clonePermissionConfig(config PermissionConfig) PermissionConfig {
	cloned := config
	if config.Defaults.Policies != nil {
		cloned.Defaults.Policies = make(map[Permission]PermissionPolicy, len(config.Defaults.Policies))
		maps.Copy(cloned.Defaults.Policies, config.Defaults.Policies)
	}
	if config.Overrides.Tools != nil {
		cloned.Overrides.Tools = make(map[string]OverridePolicy, len(config.Overrides.Tools))
		maps.Copy(cloned.Overrides.Tools, config.Overrides.Tools)
	}
	if config.Overrides.Permissions != nil {
		cloned.Overrides.Permissions = make(map[Permission]OverridePolicy, len(config.Overrides.Permissions))
		maps.Copy(cloned.Overrides.Permissions, config.Overrides.Permissions)
	}
	if config.Rules != nil {
		cloned.Rules = append([]PermissionRule(nil), config.Rules...)
	}
	return cloned
}
