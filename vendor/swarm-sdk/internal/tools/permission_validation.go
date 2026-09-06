package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

var validPermissionLevels = map[PermissionLevel]struct{}{
	LevelAlwaysAsk:  {},
	LevelBalanced:   {},
	LevelPermissive: {},
	LevelYOLO:       {},
}

var validPermissionPolicies = map[PermissionPolicy]struct{}{
	PolicyAllow:   {},
	PolicyDeny:    {},
	PolicyAsk:     {},
	PolicySandbox: {},
}

var validOverridePolicies = map[OverridePolicy]struct{}{
	OverrideAlwaysAllow: {},
	OverrideAlwaysAsk:   {},
	OverrideAlwaysDeny:  {},
}

var validPermissions = func() map[Permission]struct{} {
	result := make(map[Permission]struct{})
	for _, perm := range AllPermissions() {
		result[perm] = struct{}{}
	}
	return result
}()

// DecodePermissionConfigStrict decodes a permissions config and rejects unknown fields.
func DecodePermissionConfigStrict(data []byte) (PermissionConfig, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var cfg PermissionConfig
	if err := decoder.Decode(&cfg); err != nil {
		return PermissionConfig{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return PermissionConfig{}, err
	}
	if err := ValidatePermissionConfig(cfg); err != nil {
		return PermissionConfig{}, err
	}
	return cfg, nil
}

// ValidatePermissionConfig enforces schema correctness for permission configs.
func ValidatePermissionConfig(config PermissionConfig) error {
	if config.Version != 1 {
		return fmt.Errorf("permissions config version must be 1, got %d", config.Version)
	}
	if config.Level != "" && !isValidPermissionLevel(config.Level) {
		return fmt.Errorf("invalid permission level: %q", config.Level)
	}
	if config.TimeoutSeconds < 0 {
		return fmt.Errorf("timeoutSeconds must be non-negative, got %d", config.TimeoutSeconds)
	}
	if config.TimeoutBehavior != "" && config.TimeoutBehavior != "stop" && config.TimeoutBehavior != "continue" {
		return fmt.Errorf("invalid timeoutBehavior: %q", config.TimeoutBehavior)
	}
	if err := validatePolicyMap(config.Defaults.Policies, "defaults.policies"); err != nil {
		return err
	}
	if err := validateOverridePolicies(config.Overrides.Tools, config.Overrides.Permissions); err != nil {
		return err
	}
	if err := validatePermissionRules(config.Rules); err != nil {
		return err
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	if decoder.More() {
		return fmt.Errorf("unexpected additional JSON content")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("unexpected trailing content")
	}
	return nil
}

func isValidPermissionLevel(level PermissionLevel) bool {
	_, ok := validPermissionLevels[level]
	return ok
}

func isValidPermissionPolicy(policy PermissionPolicy) bool {
	_, ok := validPermissionPolicies[policy]
	return ok
}

func isValidOverridePolicy(policy OverridePolicy) bool {
	_, ok := validOverridePolicies[policy]
	return ok
}

func isValidPermission(permission Permission) bool {
	_, ok := validPermissions[permission]
	return ok
}

func validatePolicyMap(policies map[Permission]PermissionPolicy, path string) error {
	if policies == nil {
		return nil
	}
	for perm, policy := range policies {
		if strings.TrimSpace(string(perm)) == "" {
			return fmt.Errorf("%s contains empty permission key", path)
		}
		if !isValidPermission(perm) {
			return fmt.Errorf("%s contains unknown permission: %q", path, perm)
		}
		if !isValidPermissionPolicy(policy) {
			return fmt.Errorf("%s.%s has invalid policy %q", path, perm, policy)
		}
	}
	return nil
}

func validateOverridePolicies(toolOverrides map[string]OverridePolicy, permissionOverrides map[Permission]OverridePolicy) error {
	for tool, policy := range toolOverrides {
		if strings.TrimSpace(tool) == "" {
			return fmt.Errorf("overrides.tools contains empty tool name")
		}
		if !isValidOverridePolicy(policy) {
			return fmt.Errorf("overrides.tools.%s has invalid policy %q", tool, policy)
		}
	}
	for perm, policy := range permissionOverrides {
		if strings.TrimSpace(string(perm)) == "" {
			return fmt.Errorf("overrides.permissions contains empty permission key")
		}
		if !isValidPermission(perm) {
			return fmt.Errorf("overrides.permissions contains unknown permission: %q", perm)
		}
		if !isValidOverridePolicy(policy) {
			return fmt.Errorf("overrides.permissions.%s has invalid policy %q", perm, policy)
		}
	}
	return nil
}

func validatePermissionRules(rules []PermissionRule) error {
	for index, rule := range rules {
		if err := validatePermissionRule(rule, index); err != nil {
			return err
		}
	}
	return nil
}

func validatePermissionRule(rule PermissionRule, index int) error {
	if rule.Then.Policy == "" {
		return fmt.Errorf("rules[%d].then.policy is required", index)
	}
	if !isValidPermissionPolicy(rule.Then.Policy) {
		return fmt.Errorf("rules[%d].then.policy has invalid value %q", index, rule.Then.Policy)
	}
	if err := validateRuleMatch(rule.When, index); err != nil {
		return err
	}
	return nil
}

func validateRuleMatch(match PermissionRuleMatch, index int) error {
	if err := validatePermissionList(match.Permissions, fmt.Sprintf("rules[%d].when.permissions", index)); err != nil {
		return err
	}
	if err := validatePatternMatch(match.Paths, fmt.Sprintf("rules[%d].when.paths", index)); err != nil {
		return err
	}
	if err := validatePatternMatch(match.Commands, fmt.Sprintf("rules[%d].when.commands", index)); err != nil {
		return err
	}
	if err := validatePatternMatch(match.URLs, fmt.Sprintf("rules[%d].when.urls", index)); err != nil {
		return err
	}
	if err := validateNumericMatch(match.SizeBytes, fmt.Sprintf("rules[%d].when.sizeBytes", index)); err != nil {
		return err
	}
	return nil
}

func validatePermissionList(permissions []Permission, path string) error {
	for _, perm := range permissions {
		if strings.TrimSpace(string(perm)) == "" {
			return fmt.Errorf("%s contains empty permission", path)
		}
		if !isValidPermission(perm) {
			return fmt.Errorf("%s contains unknown permission %q", path, perm)
		}
	}
	return nil
}

func validatePatternMatch(match *PermissionPatternMatch, path string) error {
	if match == nil {
		return nil
	}
	for index, pattern := range match.Glob {
		if strings.TrimSpace(pattern) == "" {
			return fmt.Errorf("%s.glob[%d] is empty", path, index)
		}
		if _, err := doublestar.PathMatch(pattern, ""); err != nil {
			return fmt.Errorf("%s.glob[%d] invalid: %w", path, index, err)
		}
	}
	for index, pattern := range match.Regex {
		if strings.TrimSpace(pattern) == "" {
			return fmt.Errorf("%s.regex[%d] is empty", path, index)
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("%s.regex[%d] invalid: %w", path, index, err)
		}
	}
	if match.Not != nil {
		if err := validatePatternMatch(match.Not, path+".not"); err != nil {
			return err
		}
	}
	return nil
}

func validateNumericMatch(match *PermissionNumericMatch, path string) error {
	if match == nil {
		return nil
	}
	if match.Eq == nil && match.Gt == nil && match.Gte == nil && match.Lt == nil && match.Lte == nil && match.Between == nil {
		return fmt.Errorf("%s must include at least one comparator", path)
	}
	if match.Between != nil {
		if match.Between.Min > match.Between.Max {
			return fmt.Errorf("%s.between.min must be <= between.max", path)
		}
	}
	return nil
}
