package bgprocess

import (
	"context"
	"testing"
)

func TestSimpleVisibilityPolicy_OwnerAccess(t *testing.T) {
	policy := NewSimpleVisibilityPolicy()

	owner := OwnerInfo{
		UserID:  "user-1",
		AgentID: "agent-1",
	}

	process := ProcessInfo{
		Owner: owner,
	}

	// Owner should be able to view
	allowed, err := policy.CanView(context.Background(), owner, process)
	if err != nil {
		t.Fatalf("CanView() error = %v", err)
	}
	if !allowed {
		t.Error("Owner should be able to view their own process")
	}

	// Owner should be able to control
	allowed, err = policy.CanControl(context.Background(), owner, process, ActionCancel)
	if err != nil {
		t.Fatalf("CanControl() error = %v", err)
	}
	if !allowed {
		t.Error("Owner should be able to control their own process")
	}
}

func TestSimpleVisibilityPolicy_ConversationAccess(t *testing.T) {
	policy := NewSimpleVisibilityPolicy()

	processOwner := OwnerInfo{
		UserID:         "user-1",
		ConversationID: "conv-1",
	}

	peerInConversation := OwnerInfo{
		UserID:         "user-2",
		ConversationID: "conv-1",
	}

	outsideUser := OwnerInfo{
		UserID:         "user-3",
		ConversationID: "conv-2",
	}

	process := ProcessInfo{
		Owner: processOwner,
	}

	// Peer in same conversation should be able to view
	allowed, err := policy.CanView(context.Background(), peerInConversation, process)
	if err != nil {
		t.Fatalf("CanView() error = %v", err)
	}
	if !allowed {
		t.Error("Peer in same conversation should be able to view")
	}

	// But not control (by default)
	allowed, err = policy.CanControl(context.Background(), peerInConversation, process, ActionCancel)
	if err != nil {
		t.Fatalf("CanControl() error = %v", err)
	}
	if allowed {
		t.Error("Peer in same conversation should NOT be able to cancel by default")
	}

	// Outside user should NOT be able to view
	allowed, err = policy.CanView(context.Background(), outsideUser, process)
	if err != nil {
		t.Fatalf("CanView() error = %v", err)
	}
	if allowed {
		t.Error("Outside user should NOT be able to view")
	}
}

func TestSimpleVisibilityPolicy_AdminAccess(t *testing.T) {
	policy := NewSimpleVisibilityPolicy()
	policy.SetAdmins([]string{"admin-1"})

	admin := OwnerInfo{
		UserID: "admin-1",
	}

	regularUser := OwnerInfo{
		UserID: "user-1",
	}

	process := ProcessInfo{
		Owner: regularUser,
	}

	// Admin should be able to view any process
	allowed, err := policy.CanView(context.Background(), admin, process)
	if err != nil {
		t.Fatalf("CanView() error = %v", err)
	}
	if !allowed {
		t.Error("Admin should be able to view any process")
	}

	// Admin should be able to control any process
	allowed, err = policy.CanControl(context.Background(), admin, process, ActionCancel)
	if err != nil {
		t.Fatalf("CanControl() error = %v", err)
	}
	if !allowed {
		t.Error("Admin should be able to control any process")
	}
}

func TestSimpleVisibilityPolicy_FilterVisible(t *testing.T) {
	policy := NewSimpleVisibilityPolicy()

	user1 := OwnerInfo{UserID: "user-1", ConversationID: "conv-1"}
	user2 := OwnerInfo{UserID: "user-2", ConversationID: "conv-2"}

	processes := []ProcessInfo{
		{Owner: user1},
		{Owner: user2},
		{Owner: OwnerInfo{UserID: "user-3", ConversationID: "conv-1"}},
	}

	// User 1 should see their own process and conv-1 process
	visible, err := policy.FilterVisible(context.Background(), user1, processes)
	if err != nil {
		t.Fatalf("FilterVisible() error = %v", err)
	}

	if len(visible) < 2 {
		t.Errorf("Expected at least 2 visible processes for user-1, got %d", len(visible))
	}

	// User 2 should only see their own
	visible, err = policy.FilterVisible(context.Background(), user2, processes)
	if err != nil {
		t.Fatalf("FilterVisible() error = %v", err)
	}

	if len(visible) < 1 {
		t.Errorf("Expected at least 1 visible process for user-2, got %d", len(visible))
	}
}

func TestSimpleVisibilityPolicy_CustomRule(t *testing.T) {
	policy := NewSimpleVisibilityPolicy()

	// Add custom rule: users with role "supervisor" can view all
	policy.AddRule(VisibilityRule{
		Name:     "supervisor_view",
		Priority: 150, // Between admin and owner
		Actions:  []ControlAction{ActionView, ActionRead},
		Condition: func(subject OwnerInfo, process ProcessInfo) bool {
			return subject.Role == "supervisor"
		},
		Effect: EffectAllow,
	})

	supervisor := OwnerInfo{
		UserID: "supervisor-1",
		Role:   "supervisor",
	}

	regularUser := OwnerInfo{
		UserID: "user-1",
	}

	process := ProcessInfo{
		Owner: regularUser,
	}

	// Supervisor should be able to view
	allowed, err := policy.CanView(context.Background(), supervisor, process)
	if err != nil {
		t.Fatalf("CanView() error = %v", err)
	}
	if !allowed {
		t.Error("Supervisor should be able to view")
	}

	// But not cancel (rule only allows view/read)
	allowed, err = policy.CanControl(context.Background(), supervisor, process, ActionCancel)
	if err != nil {
		t.Fatalf("CanControl() error = %v", err)
	}
	if allowed {
		t.Error("Supervisor should NOT be able to cancel (not in rule actions)")
	}
}

func TestAllowAllPolicy(t *testing.T) {
	policy := NewAllowAllPolicy()

	user := OwnerInfo{UserID: "user-1"}
	process := ProcessInfo{Owner: OwnerInfo{UserID: "user-2"}}

	// Should allow view
	allowed, err := policy.CanView(context.Background(), user, process)
	if err != nil {
		t.Fatalf("CanView() error = %v", err)
	}
	if !allowed {
		t.Error("AllowAllPolicy should allow view")
	}

	// Should allow control
	allowed, err = policy.CanControl(context.Background(), user, process, ActionCancel)
	if err != nil {
		t.Fatalf("CanControl() error = %v", err)
	}
	if !allowed {
		t.Error("AllowAllPolicy should allow control")
	}

	// Should return all processes
	processes := []ProcessInfo{process, {Owner: user}}
	visible, err := policy.FilterVisible(context.Background(), user, processes)
	if err != nil {
		t.Fatalf("FilterVisible() error = %v", err)
	}
	if len(visible) != len(processes) {
		t.Errorf("AllowAllPolicy should return all processes, got %d/%d", len(visible), len(processes))
	}
}

func TestDenyAllPolicy(t *testing.T) {
	policy := NewDenyAllPolicy()

	user := OwnerInfo{UserID: "user-1"}
	process := ProcessInfo{Owner: user}

	// Should deny view even for owner
	allowed, err := policy.CanView(context.Background(), user, process)
	if err != nil {
		t.Fatalf("CanView() error = %v", err)
	}
	if allowed {
		t.Error("DenyAllPolicy should deny view")
	}

	// Should deny control
	allowed, err = policy.CanControl(context.Background(), user, process, ActionCancel)
	if err != nil {
		t.Fatalf("CanControl() error = %v", err)
	}
	if allowed {
		t.Error("DenyAllPolicy should deny control")
	}

	// Should return empty list
	processes := []ProcessInfo{process}
	visible, err := policy.FilterVisible(context.Background(), user, processes)
	if err != nil {
		t.Fatalf("FilterVisible() error = %v", err)
	}
	if len(visible) != 0 {
		t.Errorf("DenyAllPolicy should return empty list, got %d", len(visible))
	}
}

func TestCompositePolicy_AND(t *testing.T) {
	// Create policies
	policy1 := NewAllowAllPolicy()
	policy2 := NewSimpleVisibilityPolicy()

	composite := NewCompositePolicy(true, policy1, policy2) // AND

	owner := OwnerInfo{UserID: "user-1"}
	otherUser := OwnerInfo{UserID: "user-2"}
	process := ProcessInfo{Owner: owner}

	// Owner should pass (both policies allow)
	allowed, err := composite.CanView(context.Background(), owner, process)
	if err != nil {
		t.Fatalf("CanView() error = %v", err)
	}
	if !allowed {
		t.Error("AND composite should allow when both allow")
	}

	// Other user should fail (policy2 denies)
	allowed, err = composite.CanView(context.Background(), otherUser, process)
	if err != nil {
		t.Fatalf("CanView() error = %v", err)
	}
	if allowed {
		t.Error("AND composite should deny when any denies")
	}
}

func TestCompositePolicy_OR(t *testing.T) {
	// Create policies
	policy1 := NewDenyAllPolicy()
	policy2 := NewAllowAllPolicy()

	composite := NewCompositePolicy(false, policy1, policy2) // OR

	user := OwnerInfo{UserID: "user-1"}
	process := ProcessInfo{Owner: OwnerInfo{UserID: "user-2"}}

	// Should allow (policy2 allows, even though policy1 denies)
	allowed, err := composite.CanView(context.Background(), user, process)
	if err != nil {
		t.Fatalf("CanView() error = %v", err)
	}
	if !allowed {
		t.Error("OR composite should allow when any allows")
	}
}
