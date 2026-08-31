package vault

import (
	"context"
	"sync"
)

// VaultProvider provides access to vault functionality for tools.
// It is injected into tools that need credential access.
type VaultProvider interface {
	// GetExecutor returns the credential executor.
	GetExecutor() *Executor

	// GetVault returns the vault for credential lookup.
	GetVault() *Vault

	// GetProjectID returns the current project ID for scope resolution.
	GetProjectID() string

	// IsEnabled returns true if vault is configured and available.
	IsEnabled() bool

	// GetIdentityPath returns the path to the current user's age identity file
	// (private key), used for Two-Person Integrity begin/approve. Empty when no
	// identity is configured.
	GetIdentityPath() string

	// GetTwoPersonStore returns the project two-person storage and the roster
	// (recipients.txt) path, if a team/two-person vault is configured. Returns
	// (nil, "") when sealing sensitive credentials is not available.
	GetTwoPersonStore() (*TwoPersonStorage, string)
}

// SimpleVaultProvider is a simple implementation of VaultProvider.
type SimpleVaultProvider struct {
	executor       *Executor
	vault          *Vault
	projectID      string
	enabled        bool
	scopeKnown     bool
	globalEnabled  bool
	projectEnabled bool
	identityPath   string
	twoPersonStore *TwoPersonStorage
	recipientsPath string
}

// NewVaultProvider creates a new vault provider.
func NewVaultProvider(executor *Executor, vault *Vault, projectID string) *SimpleVaultProvider {
	return &SimpleVaultProvider{
		executor:  executor,
		vault:     vault,
		projectID: projectID,
		enabled:   executor != nil && vault != nil,
	}
}

// WithAvailableScopes records which logical stores are actually unlocked. It
// lets an interactive host request a missing scope while another remains live.
func (p *SimpleVaultProvider) WithAvailableScopes(global, project bool) *SimpleVaultProvider {
	p.scopeKnown = true
	p.globalEnabled = global
	p.projectEnabled = project
	return p
}

// IsScopeEnabled reports whether the requested logical scope is loaded.
func (p *SimpleVaultProvider) IsScopeEnabled(scope CredentialScope) bool {
	if !p.enabled {
		return false
	}
	if !p.scopeKnown || scope == "" {
		return true
	}
	if scope == ScopeProject {
		return p.projectEnabled
	}
	return p.globalEnabled
}

// WithIdentityPath sets the user's identity file path (for two-person flows).
func (p *SimpleVaultProvider) WithIdentityPath(path string) *SimpleVaultProvider {
	p.identityPath = path
	return p
}

// GetIdentityPath returns the configured identity file path.
func (p *SimpleVaultProvider) GetIdentityPath() string {
	return p.identityPath
}

// WithTwoPersonStore sets the project two-person store and roster path for
// sealing sensitive credentials.
func (p *SimpleVaultProvider) WithTwoPersonStore(store *TwoPersonStorage, recipientsPath string) *SimpleVaultProvider {
	p.twoPersonStore = store
	p.recipientsPath = recipientsPath
	return p
}

// GetTwoPersonStore returns the two-person store and roster path (may be nil/"").
func (p *SimpleVaultProvider) GetTwoPersonStore() (*TwoPersonStorage, string) {
	return p.twoPersonStore, p.recipientsPath
}

// GetExecutor returns the credential executor.
func (p *SimpleVaultProvider) GetExecutor() *Executor {
	return p.executor
}

// GetVault returns the vault.
func (p *SimpleVaultProvider) GetVault() *Vault {
	return p.vault
}

// GetProjectID returns the current project ID.
func (p *SimpleVaultProvider) GetProjectID() string {
	return p.projectID
}

// IsEnabled returns true if vault is configured.
func (p *SimpleVaultProvider) IsEnabled() bool {
	return p.enabled
}

// NoOpVaultProvider is a no-op provider for when vault is not configured.
type NoOpVaultProvider struct{}

// NewNoOpVaultProvider creates a no-op vault provider.
func NewNoOpVaultProvider() *NoOpVaultProvider {
	return &NoOpVaultProvider{}
}

// GetExecutor returns nil.
func (p *NoOpVaultProvider) GetExecutor() *Executor { return nil }

// GetVault returns nil.
func (p *NoOpVaultProvider) GetVault() *Vault { return nil }

// GetProjectID returns empty string.
func (p *NoOpVaultProvider) GetProjectID() string { return "" }

// IsEnabled returns false.
func (p *NoOpVaultProvider) IsEnabled() bool { return false }

// IsScopeEnabled always returns false for the no-op provider.
func (p *NoOpVaultProvider) IsScopeEnabled(CredentialScope) bool { return false }

// GetIdentityPath returns empty string.
func (p *NoOpVaultProvider) GetIdentityPath() string { return "" }

// GetTwoPersonStore returns nil (sealing not available).
func (p *NoOpVaultProvider) GetTwoPersonStore() (*TwoPersonStorage, string) { return nil, "" }

// Default vault provider (global, for tools that don't have provider injected).
//
// defaultProviderMu guards defaultProvider. Without it, SetDefaultVaultProvider
// (called by the TUI on every vault unlock/lock, e.g.
// swarm-tui/internal/chat/sdk_integration.go SetVaultProvider) races with
// GetDefaultVaultProvider (called by every vault_add/vault_list/vault_exec tool
// invocation, from agent goroutines running concurrently with the UI). Go does
// not guarantee atomicity for unsynchronized reads/writes of an interface
// value: a concurrent read can observe a "torn" value (a type word from one
// write paired with a data word from another), and invoking a method on that
// torn value dereferences garbage — surfacing as a "runtime error: invalid
// memory address or nil pointer dereference" panic in tool execution. `go test
// -race` reproduces this deterministically (see zzz_defaultprovider_race_test.go).
var (
	defaultProviderMu sync.RWMutex
	defaultProvider   VaultProvider = NewNoOpVaultProvider()
)

// SetDefaultVaultProvider sets the default vault provider.
// This is used by tools that don't have the provider injected.
func SetDefaultVaultProvider(p VaultProvider) {
	if p == nil {
		p = NewNoOpVaultProvider()
	}
	defaultProviderMu.Lock()
	defer defaultProviderMu.Unlock()
	defaultProvider = p
}

// GetDefaultVaultProvider returns the default vault provider.
func GetDefaultVaultProvider() VaultProvider {
	defaultProviderMu.RLock()
	defer defaultProviderMu.RUnlock()
	return defaultProvider
}

// IsProviderScopeEnabled checks scope-aware availability when exposed and
// otherwise preserves legacy provider-wide behavior.
func IsProviderScopeEnabled(provider VaultProvider, scope CredentialScope) bool {
	if provider == nil || !provider.IsEnabled() {
		return false
	}
	if scope == "" {
		return true
	}
	if scoped, ok := provider.(interface {
		IsScopeEnabled(CredentialScope) bool
	}); ok {
		return scoped.IsScopeEnabled(scope)
	}
	return true
}

// ExecuteWithContext is a helper that executes a command with a credential
// using the default vault provider.
func ExecuteWithContext(ctx context.Context, credentialID, command string) (*ExecutionResult, error) {
	provider := GetDefaultVaultProvider()
	if !provider.IsEnabled() {
		return nil, ErrVaultLocked
	}

	req := ExecutionRequest{
		CredentialID: credentialID,
		Command:      command,
		Tool:         "vault",
		ProjectID:    provider.GetProjectID(),
	}

	return provider.GetExecutor().Execute(ctx, req)
}

// ListWithContext lists credentials using the default vault provider.
func ListWithContext(ctx context.Context, filter CredentialFilter) ([]Credential, error) {
	provider := GetDefaultVaultProvider()
	if !provider.IsEnabled() {
		return nil, ErrVaultLocked
	}

	return provider.GetVault().List(ctx, filter, provider.GetProjectID())
}
