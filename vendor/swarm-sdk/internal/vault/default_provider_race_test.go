package vault

import (
	"context"
	"sync"
	"testing"
)

// TestDefaultProviderConcurrentSetGet is a regression test for a reported
// "vault_add causes a memory panic" bug. In the TUI, unlocking or locking the
// vault (settings.VaultSettings -> SDKIntegration.SetVaultProvider) calls
// SetDefaultVaultProvider on the UI goroutine, while an in-flight agent tool
// call (vault_add / vault_list / vault_exec Run()) concurrently calls
// GetDefaultVaultProvider() from a background goroutine. defaultProvider used
// to be a plain package-level var of interface type with no synchronization,
// so this was an unsynchronized concurrent read/write of an interface value —
// a data race the Go memory model does not guarantee is safe. A torn
// interface read (a type word from one write paired with a data word from
// another) can dereference garbage when a method is invoked on it, matching
// the reported "runtime error: invalid memory address or nil pointer
// dereference" panic. Fixed by guarding defaultProvider with defaultProviderMu
// (see provider.go). This test fails under `go test -race` if the
// synchronization regresses.
func TestDefaultProviderConcurrentSetGet(t *testing.T) {
	storage := NewMemoryStorage()
	v := NewVault(storage, VaultConfig{Enabled: true, DefaultMode: ModeBalanced})
	exec := NewExecutor(v, VaultConfig{Enabled: true, DefaultMode: ModeBalanced}, nil)
	realProvider := NewVaultProvider(exec, v, "")
	noOp := NewNoOpVaultProvider()

	prev := GetDefaultVaultProvider()
	defer SetDefaultVaultProvider(prev)

	stop := make(chan struct{})
	var writerWG sync.WaitGroup
	var readerWG sync.WaitGroup

	// Writer goroutine: simulates the TUI unlocking/locking the vault
	// repeatedly (SetVaultProvider on unlock, no-op provider on lock).
	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			SetDefaultVaultProvider(realProvider)
			SetDefaultVaultProvider(noOp)
		}
	}()

	// Reader goroutines: simulate concurrent vault_add / vault_list tool calls.
	for i := 0; i < 8; i++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			ctx := context.Background()
			for j := 0; j < 2000; j++ {
				p := GetDefaultVaultProvider()
				if p.IsEnabled() {
					_, _ = p.GetVault().List(ctx, CredentialFilter{}, p.GetProjectID())
				}
			}
		}()
	}

	// Wait for readers only, then stop the writer.
	readerWG.Wait()
	close(stop)
	writerWG.Wait()
}

func TestSetDefaultVaultProviderNilUsesNoOp(t *testing.T) {
	prev := GetDefaultVaultProvider()
	defer SetDefaultVaultProvider(prev)

	SetDefaultVaultProvider(nil)
	got := GetDefaultVaultProvider()
	if got == nil {
		t.Fatal("GetDefaultVaultProvider returned nil")
	}
	if got.IsEnabled() {
		t.Fatal("nil provider should normalize to a disabled no-op provider")
	}
}
