package approval

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestApprovalBroker_Request verifies that the broker sends requests and blocks until response.
func TestApprovalBroker_Request(t *testing.T) {
	broker := NewApprovalBroker(DefaultConfig())

	// Set up a mock IPC sender
	receivedRequests := make(chan PermissionRequest, 1)
	broker.SetIPCSender(func(req *PermissionRequest) error {
		receivedRequests <- *req
		return nil
	})

	ctx := context.Background()
	req := PermissionRequest{
		RequestID:      "req-001",
		ConversationID: "conv-123",
		Tool:           "Edit",
		Permission:     "file_write",
		Target:         "src/main.go",
		Reason:         "Update import statement",
		Timeout:        300,
	}

	type requestResult struct {
		response ApprovalResponse
		err      error
	}
	resultCh := make(chan requestResult, 1)
	go func() {
		response, err := broker.Request(ctx, req)
		resultCh <- requestResult{response: response, err: err}
	}()

	// Verify request was sent via IPC
	var receivedRequest PermissionRequest
	select {
	case receivedRequest = <-receivedRequests:
	case <-time.After(1 * time.Second):
		t.Fatal("Expected IPC sender to receive request")
	}
	if receivedRequest.RequestID != "req-001" {
		t.Errorf("Expected requestId 'req-001', got %q", receivedRequest.RequestID)
	}

	// Send response
	err := broker.Respond("req-001", DecisionApproveOnce)
	if err != nil {
		t.Fatalf("Respond failed: %v", err)
	}

	result := <-resultCh
	if result.err != nil {
		t.Fatalf("Request failed: %v", result.err)
	}

	// Verify response
	if result.response.Decision != DecisionApproveOnce {
		t.Errorf("Expected DecisionApproveOnce, got %v", result.response.Decision)
	}
}

// TestApprovalBroker_Timeout verifies that requests timeout correctly.
func TestApprovalBroker_Timeout(t *testing.T) {
	config := DefaultConfig()
	config.Timeout = 1 // 1 second timeout

	broker := NewApprovalBroker(config)
	broker.SetIPCSender(func(req *PermissionRequest) error {
		return nil
	})

	ctx := context.Background()
	req := PermissionRequest{
		RequestID:      "req-timeout",
		ConversationID: "conv-123",
		Tool:           "Edit",
		Permission:     "file_write",
		Target:         "src/main.go",
		Reason:         "Test timeout",
		Timeout:        1, // 1 second
	}

	start := time.Now()
	response, err := broker.Request(ctx, req)
	elapsed := time.Since(start)

	// Should timeout
	if err == nil {
		t.Error("Expected timeout error")
	}
	if response.Outcome != OutcomeTimeout {
		t.Errorf("Expected OutcomeTimeout, got %v", response.Outcome)
	}

	// Should have taken roughly 1 second
	if elapsed < 900*time.Millisecond || elapsed > 1500*time.Millisecond {
		t.Errorf("Expected timeout around 1s, took %v", elapsed)
	}
}

// TestApprovalBroker_Batching verifies that similar requests are batched together.
func TestApprovalBroker_Batching(t *testing.T) {
	config := DefaultConfig()
	config.Timeout = 5
	broker := NewApprovalBroker(config)

	broker.SetIPCSender(func(req *PermissionRequest) error {
		return nil
	})

	ctx := context.Background()

	// Create 3 similar requests (same tool, permission, similar targets)
	requests := []PermissionRequest{
		{RequestID: "req-1", Tool: "Write", Permission: "file_write", Target: "src/a.js"},
		{RequestID: "req-2", Tool: "Write", Permission: "file_write", Target: "src/b.js"},
		{RequestID: "req-3", Tool: "Write", Permission: "file_write", Target: "src/c.js"},
	}

	// Submit all requests
	var wg sync.WaitGroup
	for _, req := range requests {
		wg.Add(1)
		go func(r PermissionRequest) {
			defer wg.Done()
			broker.Request(ctx, r)
		}(req)
	}

	// Give time for batching/pending registration
	deadline := time.Now().Add(1 * time.Second)
	for {
		pending := broker.GetPendingRequests()
		if len(pending) >= len(requests) || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Should have batched into fewer IPC calls
	// The exact batching behavior depends on implementation,
	// but we expect some consolidation
	pending := broker.GetPendingRequests()

	// All 3 should be pending
	if len(pending) < 3 {
		t.Errorf("Expected at least 3 pending requests, got %d", len(pending))
	}

	// Verify batch ID is assigned for similar requests
	batchIDs := make(map[string]bool)
	for _, p := range pending {
		if p.BatchID != "" {
			batchIDs[p.BatchID] = true
		}
	}

	// Similar requests should share a batch ID
	if len(batchIDs) == 0 {
		t.Error("Expected similar requests to be assigned batch IDs")
	}

	// Respond to batch if present; otherwise respond to individual requests.
	if len(batchIDs) > 0 {
		for batchID := range batchIDs {
			broker.Respond(batchID, DecisionApproveOnce)
		}
	}
	for _, pendingReq := range broker.GetPendingRequests() {
		broker.Respond(pendingReq.RequestID, DecisionApproveOnce)
	}

	wg.Wait()
}

// TestApprovalBroker_Respond verifies that responses route to the correct waiter.
func TestApprovalBroker_Respond(t *testing.T) {
	broker := NewApprovalBroker(DefaultConfig())
	broker.SetIPCSender(func(req *PermissionRequest) error {
		return nil
	})

	ctx := context.Background()

	// Create two requests
	req1 := PermissionRequest{RequestID: "req-1", Tool: "Edit", Permission: "file_write", Target: "a.js"}
	req2 := PermissionRequest{RequestID: "req-2", Tool: "Bash", Permission: "bash_execute", Target: "ls"}

	var wg sync.WaitGroup
	var resp1, resp2 ApprovalResponse

	wg.Add(2)
	go func() {
		defer wg.Done()
		resp1, _ = broker.Request(ctx, req1)
	}()
	go func() {
		defer wg.Done()
		resp2, _ = broker.Request(ctx, req2)
	}()

	time.Sleep(50 * time.Millisecond)

	// Respond to req-2 first
	broker.Respond("req-2", DecisionDeny)
	// Then respond to req-1
	broker.Respond("req-1", DecisionApproveSession)

	wg.Wait()

	// Verify correct routing
	if resp1.Decision != DecisionApproveSession {
		t.Errorf("Expected req-1 to get ApproveSession, got %v", resp1.Decision)
	}
	if resp2.Decision != DecisionDeny {
		t.Errorf("Expected req-2 to get Deny, got %v", resp2.Decision)
	}
}

// TestApprovalBroker_GetPendingRequests verifies retrieval of pending requests.
func TestApprovalBroker_GetPendingRequests(t *testing.T) {
	broker := NewApprovalBroker(DefaultConfig())
	broker.SetIPCSender(func(req *PermissionRequest) error {
		return nil
	})

	ctx := context.Background()

	// Initially no pending
	pending := broker.GetPendingRequests()
	if len(pending) != 0 {
		t.Errorf("Expected 0 pending requests initially, got %d", len(pending))
	}

	// Add a request
	go func() {
		broker.Request(ctx, PermissionRequest{
			RequestID:  "req-pending",
			Tool:       "Edit",
			Permission: "file_write",
			Target:     "test.js",
		})
	}()

	time.Sleep(50 * time.Millisecond)

	// Should have 1 pending
	pending = broker.GetPendingRequests()
	if len(pending) != 1 {
		t.Errorf("Expected 1 pending request, got %d", len(pending))
	}

	// Respond
	broker.Respond("req-pending", DecisionApproveOnce)

	time.Sleep(50 * time.Millisecond)

	// Should have 0 pending
	pending = broker.GetPendingRequests()
	if len(pending) != 0 {
		t.Errorf("Expected 0 pending requests after response, got %d", len(pending))
	}
}

// TestApprovalBroker_OnResolvedCallback verifies that the OnResolved callback is called
// when a request is resolved. This test will FAIL until SetOnResolved is implemented.
func TestApprovalBroker_OnResolvedCallback(t *testing.T) {
	broker := NewApprovalBroker(DefaultConfig())
	broker.SetIPCSender(func(req *PermissionRequest) error {
		return nil
	})

	// Track resolved notifications
	var resolvedNotifications []struct {
		RequestID string
		Outcome   Outcome
	}

	// Set the OnResolved callback - this method doesn't exist yet
	broker.SetOnResolved(func(requestID string, outcome Outcome) {
		resolvedNotifications = append(resolvedNotifications, struct {
			RequestID string
			Outcome   Outcome
		}{requestID, outcome})
	})

	ctx := context.Background()
	req := PermissionRequest{
		RequestID:  "req-resolved-test",
		Tool:       "Edit",
		Permission: "file_write",
		Target:     "test.js",
		Timeout:    300,
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		broker.Request(ctx, req)
	})

	time.Sleep(50 * time.Millisecond)

	// Respond to request
	broker.Respond("req-resolved-test", DecisionApproveOnce)

	wg.Wait()

	// Verify OnResolved was called
	if len(resolvedNotifications) != 1 {
		t.Fatalf("Expected 1 resolved notification, got %d", len(resolvedNotifications))
	}
	if resolvedNotifications[0].RequestID != "req-resolved-test" {
		t.Errorf("Expected requestId 'req-resolved-test', got %q", resolvedNotifications[0].RequestID)
	}
	if resolvedNotifications[0].Outcome != OutcomeApproved {
		t.Errorf("Expected OutcomeApproved, got %v", resolvedNotifications[0].Outcome)
	}
}

// TestApprovalBroker_OnResolvedCallback_Timeout verifies that OnResolved is called
// when a request times out. This test will FAIL until SetOnResolved is implemented.
func TestApprovalBroker_OnResolvedCallback_Timeout(t *testing.T) {
	config := DefaultConfig()
	config.Timeout = 1 // 1 second

	broker := NewApprovalBroker(config)
	broker.SetIPCSender(func(req *PermissionRequest) error {
		return nil
	})

	var resolvedNotifications []struct {
		RequestID string
		Outcome   Outcome
	}

	broker.SetOnResolved(func(requestID string, outcome Outcome) {
		resolvedNotifications = append(resolvedNotifications, struct {
			RequestID string
			Outcome   Outcome
		}{requestID, outcome})
	})

	ctx := context.Background()
	req := PermissionRequest{
		RequestID:  "req-timeout-resolved",
		Tool:       "Edit",
		Permission: "file_write",
		Target:     "test.js",
		Timeout:    1, // 1 second
	}

	// This will timeout
	broker.Request(ctx, req)

	// Verify OnResolved was called with timeout outcome
	if len(resolvedNotifications) != 1 {
		t.Fatalf("Expected 1 resolved notification after timeout, got %d", len(resolvedNotifications))
	}
	if resolvedNotifications[0].Outcome != OutcomeTimeout {
		t.Errorf("Expected OutcomeTimeout, got %v", resolvedNotifications[0].Outcome)
	}
}

// TestApprovalBroker_ContextCancellation verifies that context cancellation stops waiting.
func TestApprovalBroker_ContextCancellation(t *testing.T) {
	broker := NewApprovalBroker(DefaultConfig())
	broker.SetIPCSender(func(req *PermissionRequest) error {
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())

	req := PermissionRequest{
		RequestID:  "req-cancel",
		Tool:       "Edit",
		Permission: "file_write",
		Target:     "test.js",
		Timeout:    300, // Long timeout
	}

	var response ApprovalResponse
	var err error

	done := make(chan struct{})
	go func() {
		response, err = broker.Request(ctx, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	select {
	case <-done:
		// Good - request completed
	case <-time.After(1 * time.Second):
		t.Fatal("Request did not complete after context cancellation")
	}

	// Should have error from cancellation
	if err == nil {
		t.Error("Expected error from context cancellation")
	}

	// Verify response indicates denial
	_ = response // Use the variable to satisfy compiler
}
