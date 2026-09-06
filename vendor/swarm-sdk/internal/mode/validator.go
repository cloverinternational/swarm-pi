package mode

import (
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

// ModeValidator validates mode definitions.
type ModeValidator struct {
	logger observability.Logger
}

// NewModeValidator creates a new mode validator.
func NewModeValidator(logger observability.Logger) *ModeValidator {
	if logger == nil {
		logger = noop.NewLogger()
	}
	return &ModeValidator{
		logger: logger,
	}
}

// Validate validates a complete mode definition.
// This is called after loading from YAML to ensure the mode is valid.
func (v *ModeValidator) Validate(m *Mode) error {
	// Use the built-in validation
	if err := m.Validate(); err != nil {
		return err
	}

	// Additional validation checks
	if err := v.validateDependencies(m); err != nil {
		return err
	}

	if err := v.validateTransitionGraph(m); err != nil {
		return err
	}

	return nil
}

// validateDependencies checks for circular dependencies and missing dependencies.
func (v *ModeValidator) validateDependencies(m *Mode) error {
	// Build dependency graph
	graph := make(map[string][]string)
	for _, group := range m.Groups {
		graph[group.ID] = group.DependsOn
	}

	// Check each group
	for _, group := range m.Groups {
		// Verify dependencies exist
		for _, depID := range group.DependsOn {
			if m.GetGroup(depID) == nil {
				return &ModeError{
					Type:    ErrorTypeDependency,
					Message: fmt.Sprintf("group '%s' depends on non-existent group '%s'", group.ID, depID),
					ModeID:  m.ID,
					GroupID: group.ID,
				}
			}
		}

		// Check for circular dependencies
		if err := v.checkCircularDependency(group.ID, graph, make(map[string]bool), make(map[string]bool)); err != nil {
			return &ModeError{
				Type:    ErrorTypeDependency,
				Message: fmt.Sprintf("circular dependency detected for group '%s'", group.ID),
				ModeID:  m.ID,
				GroupID: group.ID,
				Err:     err,
			}
		}
	}

	return nil
}

// checkCircularDependency performs DFS to detect cycles.
func (v *ModeValidator) checkCircularDependency(
	groupID string,
	graph map[string][]string,
	visited map[string]bool,
	recStack map[string]bool,
) error {
	visited[groupID] = true
	recStack[groupID] = true

	// Visit all dependencies
	for _, depID := range graph[groupID] {
		if !visited[depID] {
			if err := v.checkCircularDependency(depID, graph, visited, recStack); err != nil {
				return err
			}
		} else if recStack[depID] {
			return fmt.Errorf("cycle detected: %s -> %s", groupID, depID)
		}
	}

	recStack[groupID] = false
	return nil
}

// validateTransitionGraph checks transition graph for issues.
func (v *ModeValidator) validateTransitionGraph(m *Mode) error {
	if len(m.Transitions) == 0 {
		// No transitions is valid (simple sequential mode)
		return nil
	}

	// Check for unreachable groups
	// (groups with no transitions leading to them and no dependencies)
	reachable := make(map[string]bool)

	// Mark groups with dependencies as potentially reachable
	for _, group := range m.Groups {
		if len(group.DependsOn) > 0 {
			reachable[group.ID] = true
		}
	}

	// Mark groups that are transition targets
	for _, trans := range m.Transitions {
		reachable[trans.To] = true
		reachable[trans.From] = true // Source is also reachable
	}

	// Check if any groups are unreachable
	// (only warn if there are transitions, otherwise it's a simple dependency-based mode)
	if len(m.Transitions) > 0 && len(m.Groups) > 1 {
		for _, group := range m.Groups {
			if !reachable[group.ID] && len(group.DependsOn) == 0 {
				v.logger.Warn(nil, "potentially unreachable group",
					observability.F("mode_id", m.ID),
					observability.F("group_id", group.ID))
			}
		}
	}

	return nil
}

// ValidateGroup validates a single agent group.
func (v *ModeValidator) ValidateGroup(group *AgentGroup) error {
	return group.Validate()
}

// ValidateTransitions validates all transitions in a mode.
func (v *ModeValidator) ValidateTransitions(m *Mode) error {
	for i, trans := range m.Transitions {
		if err := trans.Validate(); err != nil {
			return &ModeError{
				Type:    ErrorTypeValidation,
				Message: fmt.Sprintf("transition at index %d is invalid", i),
				ModeID:  m.ID,
				Err:     err,
			}
		}
	}

	return v.validateTransitionGraph(m)
}
