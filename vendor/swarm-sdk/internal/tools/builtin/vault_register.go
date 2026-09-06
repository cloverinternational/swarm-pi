package builtin

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
)

// VaultTools returns all vault tools for agent registration.
// These tools are always available to agents — even when the vault is locked.
// When locked, they return helpful messages telling the user how to unlock.
func VaultTools() []tools.Tool {
	return VaultToolsWithUnlocker(nil)
}

// VaultToolsWithUnlocker returns all vault tools wired to an optional
// host-neutral interactive unlock broker.
func VaultToolsWithUnlocker(unlocker VaultUnlocker) []tools.Tool {
	return []tools.Tool{
		NewVaultExecToolWithUnlocker(unlocker),
		NewVaultListToolWithUnlocker(unlocker),
		NewVaultAddToolWithUnlocker(unlocker),
		NewVaultApproveToolWithUnlocker(unlocker),
		NewVaultTwoPersonStatusToolWithUnlocker(unlocker),
	}
}

// ConfigureVault sets up the default vault provider and returns
// the vault tools for registration.
//
// If provider is nil, vault tools are still returned but will report
// "vault is locked" until a provider is set via SetVaultProvider.
//
// Example usage:
//
//	provider := vault.NewVaultProvider(executor, v, projectID)
//	vault.SetDefaultVaultProvider(provider)
//	agentBuilder.Tools(builtin.VaultTools()...)
func ConfigureVault(provider vault.VaultProvider) []tools.Tool {
	return ConfigureVaultWithUnlocker(provider, nil)
}

// ConfigureVaultWithUnlocker publishes the provider and returns vault tools
// wired to an optional interactive host capability.
func ConfigureVaultWithUnlocker(provider vault.VaultProvider, unlocker VaultUnlocker) []tools.Tool {
	vault.SetDefaultVaultProvider(provider)
	return VaultToolsWithUnlocker(unlocker)
}

// IsVaultConfigured returns true if a vault provider is configured.
func IsVaultConfigured() bool {
	return vault.GetDefaultVaultProvider().IsEnabled()
}
