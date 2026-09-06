package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
)

func TestVaultUnlockBrokerResumesAfterProviderPublication(t *testing.T) {
	prev := vault.GetDefaultVaultProvider()
	defer vault.SetDefaultVaultProvider(prev)
	vault.SetDefaultVaultProvider(nil)

	broker := NewVaultUnlockBroker()
	requests := make(chan vaultUnlockRequest, 1)
	broker.SetDispatcher(func(msg tea.Msg) {
		if req, ok := msg.(vaultUnlockRequestMsg); ok {
			requests <- req.request
		}
	})

	done := make(chan error, 1)
	go func() {
		done <- broker.Request(context.Background(), "vault_list", "global")
	}()

	var req vaultUnlockRequest
	select {
	case req = <-requests:
	case <-time.After(time.Second):
		t.Fatal("unlock request was not dispatched")
	}
	if req.Tool != "vault_list" || req.Scope != "global" {
		t.Fatalf("unlock request = %#v", req)
	}

	storage := vault.NewMemoryStorage()
	v := vault.NewVault(storage, vault.VaultConfig{Enabled: true})
	exec := vault.NewExecutor(v, vault.VaultConfig{Enabled: true}, nil)
	vault.SetDefaultVaultProvider(vault.NewVaultProvider(exec, v, ""))
	if err := broker.Respond(req.ID, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Request returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("unlock request did not resume")
	}
}

func TestVaultUnlockBrokerContextCancellation(t *testing.T) {
	prev := vault.GetDefaultVaultProvider()
	defer vault.SetDefaultVaultProvider(prev)
	vault.SetDefaultVaultProvider(nil)

	broker := NewVaultUnlockBroker()
	dispatched := false
	broker.SetDispatcher(func(tea.Msg) { dispatched = true })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := broker.Request(ctx, "vault_exec", "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Request error = %v, want context.Canceled", err)
	}
	if dispatched {
		t.Fatal("pre-canceled request dispatched UI work")
	}
}

func TestVaultUnlockBrokerMapsUserCancellationToUnlockedFalse(t *testing.T) {
	prev := vault.GetDefaultVaultProvider()
	defer vault.SetDefaultVaultProvider(prev)
	vault.SetDefaultVaultProvider(nil)

	broker := NewVaultUnlockBroker()
	requests := make(chan vaultUnlockRequest, 1)
	broker.SetDispatcher(func(msg tea.Msg) {
		if req, ok := msg.(vaultUnlockRequestMsg); ok {
			requests <- req.request
		}
	})

	done := make(chan struct {
		result bool
		err    error
	}, 1)
	go func() {
		result, err := broker.UnlockVault(context.Background(), builtin.VaultUnlockRequest{
			Operation: "vault_exec",
			Scope:     "project",
		})
		done <- struct {
			result bool
			err    error
		}{result: result.Unlocked, err: err}
	}()

	var req vaultUnlockRequest
	select {
	case req = <-requests:
	case <-time.After(time.Second):
		t.Fatal("unlock request was not dispatched")
	}
	if err := broker.Respond(req.ID, errVaultUnlockCanceled); err != nil {
		t.Fatal(err)
	}
	var outcome struct {
		result bool
		err    error
	}
	select {
	case outcome = <-done:
	case <-time.After(time.Second):
		t.Fatal("UnlockVault did not return after cancellation")
	}
	if outcome.err != nil {
		t.Fatalf("UnlockVault error = %v, want nil", outcome.err)
	}
	if outcome.result {
		t.Fatal("UnlockVault reported user cancellation as unlocked")
	}
}

func TestVaultUnlockBrokerWithoutDispatcherDoesNotBlock(t *testing.T) {
	prev := vault.GetDefaultVaultProvider()
	defer vault.SetDefaultVaultProvider(prev)
	vault.SetDefaultVaultProvider(nil)

	err := NewVaultUnlockBroker().Request(context.Background(), "vault_list", "")
	if err == nil {
		t.Fatal("Request unexpectedly succeeded without a dispatcher")
	}
}

func TestVaultUnlockBrokerCanceledWaiterDoesNotBlockBehindActiveRequest(t *testing.T) {
	prev := vault.GetDefaultVaultProvider()
	defer vault.SetDefaultVaultProvider(prev)
	vault.SetDefaultVaultProvider(nil)

	broker := NewVaultUnlockBroker()
	requests := make(chan vaultUnlockRequest, 1)
	broker.SetDispatcher(func(msg tea.Msg) {
		if req, ok := msg.(vaultUnlockRequestMsg); ok {
			requests <- req.request
		}
	})

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- broker.Request(context.Background(), "vault_list", "global")
	}()
	first := <-requests

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := broker.Request(ctx, "vault_add", "project"); !errors.Is(err, context.Canceled) {
		t.Fatalf("second request error = %v, want context.Canceled", err)
	}
	if err := broker.Respond(first.ID, errVaultUnlockCanceled); err != nil {
		t.Fatal(err)
	}
	if err := <-firstDone; !errors.Is(err, errVaultUnlockCanceled) {
		t.Fatalf("first request error = %v", err)
	}
}
