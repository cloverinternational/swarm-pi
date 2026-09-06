package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/plan"
)

func TestPlanBroker_RequestPlanApprovalUsesTUI(t *testing.T) {
	broker := NewPlanBroker()
	broker.SetCurrentPlan("agent-a", "plan-a")
	broker.SetCurrentPlan("agent-b", "plan-b")
	requests := make(chan planApprovalRequestMsg, 1)
	broker.SetDispatcher(func(msg tea.Msg) {
		if request, ok := msg.(planApprovalRequestMsg); ok {
			requests <- request
		}
	})

	responses := make(chan plan.ApprovalResponse, 1)
	errors := make(chan error, 1)
	go func() {
		ctx := context.WithValue(context.Background(), "agent_id", "agent-a")
		response, err := broker.RequestPlanApproval(ctx, "terminal plan")
		if err != nil {
			errors <- err
			return
		}
		responses <- response
	}()

	select {
	case request := <-requests:
		if request.requestID != "plan-approval" {
			t.Fatalf("request ID = %q", request.requestID)
		}
		if request.plan != "terminal plan" {
			t.Fatalf("plan = %q", request.plan)
		}
		broker.RespondApproved(request.plan, false)
	case <-time.After(2 * time.Second):
		t.Fatal("plan approval was not dispatched to the TUI")
	}

	select {
	case err := <-errors:
		t.Fatal(err)
	case response := <-responses:
		if !response.Approved {
			t.Fatal("plan was not approved")
		}
		if response.EditedPlan != "terminal plan" {
			t.Fatalf("edited plan = %q", response.EditedPlan)
		}
		if got := broker.CurrentPlanID(context.Background(), "agent-a"); got != "" {
			t.Fatalf("requesting agent plan remained active: %q", got)
		}
		if got := broker.CurrentPlanID(context.Background(), "agent-b"); got != "plan-b" {
			t.Fatalf("unrelated agent plan was changed: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("plan approval did not return the TUI response")
	}
}

func TestPlanBrokerRejectsConcurrentApproval(t *testing.T) {
	broker := NewPlanBroker()
	requests := make(chan planApprovalRequestMsg, 1)
	broker.SetDispatcher(func(msg tea.Msg) {
		if request, ok := msg.(planApprovalRequestMsg); ok {
			requests <- request
		}
	})

	firstDone := make(chan error, 1)
	go func() {
		_, err := broker.RequestPlanApproval(context.Background(), "first")
		firstDone <- err
	}()
	select {
	case <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("first approval was not dispatched")
	}

	if _, err := broker.RequestPlanApproval(context.Background(), "second"); err == nil ||
		!strings.Contains(err.Error(), "already pending") {
		t.Fatalf("second approval error = %v", err)
	}
	broker.RespondRejected("retry")
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first approval failed after second was rejected: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first approval remained blocked")
	}
}

func TestPlanBrokerHonorsCancellation(t *testing.T) {
	broker := NewPlanBroker()
	requests := make(chan planApprovalRequestMsg, 1)
	broker.SetDispatcher(func(msg tea.Msg) {
		if request, ok := msg.(planApprovalRequestMsg); ok {
			requests <- request
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := broker.RequestPlanApproval(ctx, "cancel me")
		done <- err
	}()
	select {
	case <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("approval was not dispatched")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled approval remained blocked")
	}
	if broker.HasPendingApproval() {
		t.Fatal("canceled approval remains pending")
	}
}

func TestSDKIntegrationCloseCancelsPendingPlanApproval(t *testing.T) {
	broker := NewPlanBroker()
	requests := make(chan planApprovalRequestMsg, 1)
	broker.SetDispatcher(func(msg tea.Msg) {
		if request, ok := msg.(planApprovalRequestMsg); ok {
			requests <- request
		}
	})
	done := make(chan error, 1)
	go func() {
		_, err := broker.RequestPlanApproval(context.Background(), "shutdown")
		done <- err
	}()
	select {
	case <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("approval was not dispatched")
	}

	integration := &SDKIntegration{planBroker: broker}
	if err := integration.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown left approval blocked")
	}
}
