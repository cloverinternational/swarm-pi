package mcp

import (
	"context"
	"fmt"
	"testing"
	"time"

	mcpsdk "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/mcp"
)

func TestRunServerBackoffAttempt(t *testing.T) {
	manager := NewRuntimeManager(nil, nil, nil, nil, nil)
	connectCalls := 0
	sleepAttempts := []int{}
	manager.connectOnceFunc = func(ctx context.Context, state *ServerState) (*mcpsdk.Client, error) {
		connectCalls++
		return nil, fmt.Errorf("boom")
	}
	manager.sleepFn = func(ctx context.Context, base, max time.Duration, attempt int) bool {
		sleepAttempts = append(sleepAttempts, attempt)
		return false
	}

	state := &ServerState{
		Config:  ServerConfig{Name: "alpha", Enabled: true},
		Tools:   make(map[string]*ToolState),
		toolMap: make(map[string]string),
		Status:  ServerStatus{State: "disconnected"},
	}
	manager.runServer(context.Background(), state)

	if connectCalls != 1 {
		t.Fatalf("expected 1 connect attempt, got %d", connectCalls)
	}
	if len(sleepAttempts) != 1 || sleepAttempts[0] != 1 {
		t.Fatalf("expected backoff attempt 1, got %v", sleepAttempts)
	}
	if state.Status.State != "error" {
		t.Fatalf("expected error status, got %s", state.Status.State)
	}
}

func TestRunServerStopsOnCredentialError(t *testing.T) {
	manager := NewRuntimeManager(nil, nil, nil, nil, nil)
	sleepCalls := 0
	manager.connectOnceFunc = func(ctx context.Context, state *ServerState) (*mcpsdk.Client, error) {
		return nil, missingCredentialError("alpha", "token")
	}
	manager.sleepFn = func(ctx context.Context, base, max time.Duration, attempt int) bool {
		sleepCalls++
		return true
	}

	state := &ServerState{
		Config:  ServerConfig{Name: "alpha", Enabled: true},
		Tools:   make(map[string]*ToolState),
		toolMap: make(map[string]string),
		Status:  ServerStatus{State: "disconnected"},
	}
	manager.runServer(context.Background(), state)

	if sleepCalls != 0 {
		t.Fatalf("expected no backoff on credential error")
	}
	if state.Status.State != "error" || state.Status.LastError == "" {
		t.Fatalf("expected credential error status to be set")
	}
}
