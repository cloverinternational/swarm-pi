package bgprocess

import (
	"context"
	"slices"
	"sync"
)

// RuleEffect determines whether a rule allows or denies access.
type RuleEffect string

const (
	EffectAllow RuleEffect = "allow"
	EffectDeny  RuleEffect = "deny"
)

// VisibilityRule defines a single ABAC rule.
type VisibilityRule struct {
	Name      string
	Priority  int // Higher priority rules are evaluated first
	Actions   []ControlAction
	Condition RuleCondition
	Effect    RuleEffect
}

// RuleCondition is a function that evaluates whether a rule applies.
type RuleCondition func(subject OwnerInfo, process ProcessInfo) bool

// SimpleVisibilityPolicy implements VisibilityPolicy with configurable rules.
type SimpleVisibilityPolicy struct {
	rules  []VisibilityRule
	mu     sync.RWMutex
	admins map[string]bool // User IDs with admin access
}

// NewSimpleVisibilityPolicy creates a new policy with default rules.
func NewSimpleVisibilityPolicy() *SimpleVisibilityPolicy {
	p := &SimpleVisibilityPolicy{
		rules:  make([]VisibilityRule, 0),
		admins: make(map[string]bool),
	}

	// Add default rules
	p.AddOwnerRule()
	p.AddConversationRule()
	p.AddAdminRule()

	return p
}

// AddRule adds a custom rule to the policy.
func (p *SimpleVisibilityPolicy) AddRule(rule VisibilityRule) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Insert in priority order (higher priority first)
	inserted := false
	for i, r := range p.rules {
		if rule.Priority > r.Priority {
			p.rules = append(p.rules[:i], append([]VisibilityRule{rule}, p.rules[i:]...)...)
			inserted = true
			break
		}
	}
	if !inserted {
		p.rules = append(p.rules, rule)
	}
}

// AddOwnerRule adds the default owner rule: owners can always access their own processes.
func (p *SimpleVisibilityPolicy) AddOwnerRule() {
	p.AddRule(VisibilityRule{
		Name:     "owner_access",
		Priority: 100,
		Actions:  []ControlAction{ActionView, ActionRead, ActionCancel, ActionPause, ActionResume, ActionSignal},
		Condition: func(subject OwnerInfo, process ProcessInfo) bool {
			// User owns the process
			if subject.UserID != "" && subject.UserID == process.Owner.UserID {
				return true
			}
			// Agent owns the process
			if subject.AgentID != "" && subject.AgentID == process.Owner.AgentID {
				return true
			}
			return false
		},
		Effect: EffectAllow,
	})
}

// AddConversationRule adds the conversation rule: same conversation can view processes.
func (p *SimpleVisibilityPolicy) AddConversationRule() {
	p.AddRule(VisibilityRule{
		Name:     "conversation_access",
		Priority: 90,
		Actions:  []ControlAction{ActionView, ActionRead}, // Read-only for conversation peers
		Condition: func(subject OwnerInfo, process ProcessInfo) bool {
			// Same conversation can view (but not control by default)
			return subject.ConversationID != "" &&
				subject.ConversationID == process.Owner.ConversationID
		},
		Effect: EffectAllow,
	})
}

// AddAdminRule adds the admin rule: admins can access all processes.
func (p *SimpleVisibilityPolicy) AddAdminRule() {
	p.AddRule(VisibilityRule{
		Name:     "admin_access",
		Priority: 200, // Highest priority
		Actions:  []ControlAction{ActionView, ActionRead, ActionCancel, ActionPause, ActionResume, ActionSignal},
		Condition: func(subject OwnerInfo, process ProcessInfo) bool {
			return subject.Role == "admin"
		},
		Effect: EffectAllow,
	})
}

// SetAdmins sets the list of user IDs that have admin access.
func (p *SimpleVisibilityPolicy) SetAdmins(userIDs []string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.admins = make(map[string]bool)
	for _, id := range userIDs {
		p.admins[id] = true
	}
}

// IsAdmin checks if a user ID has admin access.
func (p *SimpleVisibilityPolicy) IsAdmin(userID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.admins[userID]
}

// CanView checks if the subject can view process information.
func (p *SimpleVisibilityPolicy) CanView(ctx context.Context, subject OwnerInfo, process ProcessInfo) (bool, error) {
	return p.checkAccess(ctx, subject, process, ActionView)
}

// CanControl checks if the subject can control the process.
func (p *SimpleVisibilityPolicy) CanControl(ctx context.Context, subject OwnerInfo, process ProcessInfo, action ControlAction) (bool, error) {
	return p.checkAccess(ctx, subject, process, action)
}

// checkAccess evaluates all rules to determine access.
func (p *SimpleVisibilityPolicy) checkAccess(ctx context.Context, subject OwnerInfo, process ProcessInfo, action ControlAction) (bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Check if subject is an admin via UserID lookup
	if p.admins[subject.UserID] {
		subject.Role = "admin"
	}

	// Evaluate rules in priority order
	for _, rule := range p.rules {
		// Check if rule applies to this action
		actionMatches := slices.Contains(rule.Actions, action)
		if !actionMatches {
			continue
		}

		// Evaluate condition
		if rule.Condition(subject, process) {
			return rule.Effect == EffectAllow, nil
		}
	}

	// Default deny
	return false, nil
}

// FilterVisible filters a list of processes to only those visible to the subject.
func (p *SimpleVisibilityPolicy) FilterVisible(ctx context.Context, subject OwnerInfo, processes []ProcessInfo) ([]ProcessInfo, error) {
	result := make([]ProcessInfo, 0, len(processes))

	for _, proc := range processes {
		allowed, err := p.CanView(ctx, subject, proc)
		if err != nil {
			return nil, err
		}
		if allowed {
			result = append(result, proc)
		}
	}

	return result, nil
}

// AllowAllPolicy is a policy that allows all access (for testing or trusted environments).
type AllowAllPolicy struct{}

// NewAllowAllPolicy creates a policy that allows everything.
func NewAllowAllPolicy() *AllowAllPolicy {
	return &AllowAllPolicy{}
}

// CanView always returns true.
func (p *AllowAllPolicy) CanView(ctx context.Context, subject OwnerInfo, process ProcessInfo) (bool, error) {
	return true, nil
}

// CanControl always returns true.
func (p *AllowAllPolicy) CanControl(ctx context.Context, subject OwnerInfo, process ProcessInfo, action ControlAction) (bool, error) {
	return true, nil
}

// FilterVisible returns all processes.
func (p *AllowAllPolicy) FilterVisible(ctx context.Context, subject OwnerInfo, processes []ProcessInfo) ([]ProcessInfo, error) {
	return processes, nil
}

// DenyAllPolicy is a policy that denies all access (for testing).
type DenyAllPolicy struct{}

// NewDenyAllPolicy creates a policy that denies everything.
func NewDenyAllPolicy() *DenyAllPolicy {
	return &DenyAllPolicy{}
}

// CanView always returns false.
func (p *DenyAllPolicy) CanView(ctx context.Context, subject OwnerInfo, process ProcessInfo) (bool, error) {
	return false, nil
}

// CanControl always returns false.
func (p *DenyAllPolicy) CanControl(ctx context.Context, subject OwnerInfo, process ProcessInfo, action ControlAction) (bool, error) {
	return false, nil
}

// FilterVisible returns empty list.
func (p *DenyAllPolicy) FilterVisible(ctx context.Context, subject OwnerInfo, processes []ProcessInfo) ([]ProcessInfo, error) {
	return []ProcessInfo{}, nil
}

// CompositePolicy combines multiple policies with AND or OR logic.
type CompositePolicy struct {
	policies   []VisibilityPolicy
	requireAll bool // true = AND, false = OR
}

// NewCompositePolicy creates a composite policy.
func NewCompositePolicy(requireAll bool, policies ...VisibilityPolicy) *CompositePolicy {
	return &CompositePolicy{
		policies:   policies,
		requireAll: requireAll,
	}
}

// CanView checks all policies.
func (p *CompositePolicy) CanView(ctx context.Context, subject OwnerInfo, process ProcessInfo) (bool, error) {
	if p.requireAll {
		// AND: all must allow
		for _, policy := range p.policies {
			allowed, err := policy.CanView(ctx, subject, process)
			if err != nil {
				return false, err
			}
			if !allowed {
				return false, nil
			}
		}
		return true, nil
	}

	// OR: any must allow
	for _, policy := range p.policies {
		allowed, err := policy.CanView(ctx, subject, process)
		if err != nil {
			return false, err
		}
		if allowed {
			return true, nil
		}
	}
	return false, nil
}

// CanControl checks all policies.
func (p *CompositePolicy) CanControl(ctx context.Context, subject OwnerInfo, process ProcessInfo, action ControlAction) (bool, error) {
	if p.requireAll {
		for _, policy := range p.policies {
			allowed, err := policy.CanControl(ctx, subject, process, action)
			if err != nil {
				return false, err
			}
			if !allowed {
				return false, nil
			}
		}
		return true, nil
	}

	for _, policy := range p.policies {
		allowed, err := policy.CanControl(ctx, subject, process, action)
		if err != nil {
			return false, err
		}
		if allowed {
			return true, nil
		}
	}
	return false, nil
}

// FilterVisible filters using the primary policy (first one).
func (p *CompositePolicy) FilterVisible(ctx context.Context, subject OwnerInfo, processes []ProcessInfo) ([]ProcessInfo, error) {
	result := processes
	var err error

	for _, policy := range p.policies {
		result, err = policy.FilterVisible(ctx, subject, result)
		if err != nil {
			return nil, err
		}
		if p.requireAll {
			// For AND, we progressively filter
			continue
		}
		// For OR, we'd need to collect unique visible processes
		// For simplicity, OR mode just uses first policy
		break
	}

	return result, nil
}
