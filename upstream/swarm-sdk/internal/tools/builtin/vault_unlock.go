package builtin

import (
	"context"
	"errors"
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
)

// VaultUnlockRequest describes why a tool needs the host to unlock the vault.
// It deliberately contains no UI-specific types.
type VaultUnlockRequest struct {
	Operation string
	Scope     string
}

// VaultUnlockResult reports whether the host completed an unlock.
type VaultUnlockResult struct {
	Unlocked bool
}

// VaultUnlocker is an optional host-provided broker for interactive vault
// unlock. Implementations may use a TUI, GUI, or any other interaction model.
type VaultUnlocker interface {
	UnlockVault(context.Context, VaultUnlockRequest) (VaultUnlockResult, error)
}

type vaultUnlockOutcome struct {
	provider vault.VaultProvider
	status   string
	err      string
}

func ensureVaultUnlocked(ctx context.Context, unlockIfNeeded bool, unlocker VaultUnlocker, operation, scope string) vaultUnlockOutcome {
	provider := vault.GetDefaultVaultProvider()
	// Auto-load the shared cleartext vault if the default provider is still
	// the no-op provider (meaning no session has installed one yet) but a
	// cleartext store exists on disk. This makes the vault available to every
	// agent and TUI session without an explicit unlock. Tests that explicitly
	// install a provider are respected.
	if _, isNoop := provider.(*vault.NoOpVaultProvider); isNoop {
		if home, err := os.UserHomeDir(); err == nil {
			vault.AutoLoadInto(home, "", "")
			provider = vault.GetDefaultVaultProvider()
		}
	}
	requestedScope := vault.CredentialScope(scope)
	if vault.IsProviderScopeEnabled(provider, requestedScope) || !unlockIfNeeded {
		return vaultUnlockOutcome{provider: provider}
	}
	if unlocker == nil {
		return vaultUnlockOutcome{
			provider: provider,
			status:   "interactive_unlock_unavailable",
			err:      "vault is locked and interactive unlock is unavailable in this host",
		}
	}

	result, err := unlocker.UnlockVault(ctx, VaultUnlockRequest{Operation: operation, Scope: scope})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return vaultUnlockOutcome{provider: provider, status: "unlock_cancelled", err: "vault unlock was cancelled"}
		}
		return vaultUnlockOutcome{provider: provider, status: "unlock_failed", err: err.Error()}
	}
	if !result.Unlocked {
		return vaultUnlockOutcome{provider: provider, status: "unlock_cancelled", err: "vault unlock was cancelled"}
	}

	// The broker installs the newly unlocked provider. Re-read it rather than
	// retaining the locked provider captured before interaction.
	provider = vault.GetDefaultVaultProvider()
	if !vault.IsProviderScopeEnabled(provider, requestedScope) {
		return vaultUnlockOutcome{provider: provider, status: "unlock_failed", err: "vault remained locked after unlock"}
	}
	return vaultUnlockOutcome{provider: provider}
}
