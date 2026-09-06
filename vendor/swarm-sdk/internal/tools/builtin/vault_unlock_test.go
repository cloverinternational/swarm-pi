package builtin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
)

type fakeVaultProvider struct {
	enabled bool
}

func (p *fakeVaultProvider) GetExecutor() *vault.Executor { return nil }
func (p *fakeVaultProvider) GetVault() *vault.Vault       { return nil }
func (p *fakeVaultProvider) GetProjectID() string         { return "" }
func (p *fakeVaultProvider) IsEnabled() bool              { return p.enabled }
func (p *fakeVaultProvider) GetIdentityPath() string      { return "" }
func (p *fakeVaultProvider) GetTwoPersonStore() (*vault.TwoPersonStorage, string) {
	return nil, ""
}

type fakeVaultUnlocker struct {
	calls int
	fn    func(context.Context, VaultUnlockRequest) (VaultUnlockResult, error)
}

type failingListStorage struct {
	vault.Storage
}

func (failingListStorage) List(context.Context, vault.CredentialFilter) ([]vault.Credential, error) {
	return nil, errors.New("metadata unavailable")
}

func (u *fakeVaultUnlocker) UnlockVault(ctx context.Context, req VaultUnlockRequest) (VaultUnlockResult, error) {
	u.calls++
	return u.fn(ctx, req)
}

func withVaultProvider(t *testing.T, provider vault.VaultProvider) {
	t.Helper()
	previous := vault.GetDefaultVaultProvider()
	vault.SetDefaultVaultProvider(provider)
	t.Cleanup(func() { vault.SetDefaultVaultProvider(previous) })
}

func enabledMemoryProvider() vault.VaultProvider {
	config := vault.VaultConfig{Enabled: true, DefaultMode: vault.ModeBalanced}
	v := vault.NewVault(vault.NewMemoryStorage(), config)
	return vault.NewVaultProvider(vault.NewExecutor(v, config, nil), v, "")
}

func requireOutputContains(t *testing.T, output string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(output, value) {
			t.Fatalf("output %q does not contain %q", output, value)
		}
	}
}

func TestVaultUnlockHeadlessReturnsImmediately(t *testing.T) {
	withVaultProvider(t, &fakeVaultProvider{})

	tests := []struct {
		name string
		run  func() string
	}{
		{"exec", func() string {
			result, _ := (&VaultExecTool{}).Run(context.Background(), VaultExecParams{
				CredentialID: "id", Command: "ignored", UnlockIfNeeded: true,
			})
			return result.Output
		}},
		{"list", func() string {
			result, _ := (&VaultListTool{}).Run(context.Background(), VaultListParams{UnlockIfNeeded: true})
			return result.Output
		}},
		{"add", func() string {
			result, _ := (&VaultAddTool{}).Run(context.Background(), VaultAddParams{
				ID: "id", Kind: vault.CredentialKindAPIKey, Secret: "fake", UnlockIfNeeded: true,
			})
			return result.Output
		}},
		{"approve", func() string {
			result, _ := (&VaultApproveTool{}).Run(context.Background(), VaultApproveParams{
				RequestID: "request", UnlockIfNeeded: true,
			})
			return result.Output
		}},
		{"status", func() string {
			result, _ := (&VaultTwoPersonStatusTool{}).Run(context.Background(), VaultTwoPersonStatusParams{
				RequestID: "request", UnlockIfNeeded: true,
			})
			return result.Output
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireOutputContains(t, test.run(), "interactive_unlock_unavailable")
		})
	}
}

func TestVaultUnlockValidatesRequiredParamsBeforeBroker(t *testing.T) {
	withVaultProvider(t, &fakeVaultProvider{})
	unlocker := &fakeVaultUnlocker{fn: func(context.Context, VaultUnlockRequest) (VaultUnlockResult, error) {
		return VaultUnlockResult{Unlocked: true}, nil
	}}

	results := []string{}
	result, _ := (&VaultExecTool{unlocker: unlocker}).Run(context.Background(), VaultExecParams{UnlockIfNeeded: true})
	results = append(results, result.Output)
	result, _ = (&VaultAddTool{unlocker: unlocker}).Run(context.Background(), VaultAddParams{UnlockIfNeeded: true})
	results = append(results, result.Output)
	invalidExpiration, _ := (&VaultAddTool{unlocker: unlocker}).Run(context.Background(), VaultAddParams{
		ID: "id", Kind: vault.CredentialKindAPIKey, Secret: "fake", Expire: "invalid", UnlockIfNeeded: true,
	})
	requireOutputContains(t, invalidExpiration.Output, "invalid expiration")
	approveResult, _ := (&VaultApproveTool{unlocker: unlocker}).Run(context.Background(), VaultApproveParams{UnlockIfNeeded: true})
	results = append(results, approveResult.Output)
	statusResult, _ := (&VaultTwoPersonStatusTool{unlocker: unlocker}).Run(context.Background(), VaultTwoPersonStatusParams{UnlockIfNeeded: true})
	results = append(results, statusResult.Output)

	if unlocker.calls != 0 {
		t.Fatalf("unlock broker called %d times for structurally invalid requests", unlocker.calls)
	}
	for _, output := range results {
		requireOutputContains(t, output, "required")
	}
}

func TestVaultUnlockRereadsProviderAndContinuesSameOperation(t *testing.T) {
	withVaultProvider(t, &fakeVaultProvider{})
	unlocked := enabledMemoryProvider()
	if err := unlocked.GetVault().Store(context.Background(), vault.Credential{
		ID: "visible-after-unlock", Kind: vault.CredentialKindAPIKey, Secret: "not-real",
	}); err != nil {
		t.Fatal(err)
	}
	unlocker := &fakeVaultUnlocker{fn: func(_ context.Context, req VaultUnlockRequest) (VaultUnlockResult, error) {
		if req.Operation != "vault_list" {
			t.Fatalf("operation = %q", req.Operation)
		}
		vault.SetDefaultVaultProvider(unlocked)
		return VaultUnlockResult{Unlocked: true}, nil
	}}

	result, err := (&VaultListTool{unlocker: unlocker}).Run(context.Background(), VaultListParams{UnlockIfNeeded: true})
	if err != nil {
		t.Fatal(err)
	}
	if unlocker.calls != 1 {
		t.Fatalf("unlock calls = %d", unlocker.calls)
	}
	requireOutputContains(t, result.Output, "visible-after-unlock")
}

func TestVaultUnlockCancellationDoesNotMutate(t *testing.T) {
	locked := &fakeVaultProvider{}
	withVaultProvider(t, locked)
	unlocker := &fakeVaultUnlocker{fn: func(ctx context.Context, _ VaultUnlockRequest) (VaultUnlockResult, error) {
		<-ctx.Done()
		return VaultUnlockResult{}, ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := (&VaultAddTool{unlocker: unlocker}).Run(ctx, VaultAddParams{
		ID: "must-not-store", Kind: vault.CredentialKindAPIKey, Secret: "fake", UnlockIfNeeded: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	requireOutputContains(t, result.Output, "unlock_cancelled")
	if vault.GetDefaultVaultProvider() != locked {
		t.Fatal("cancelled unlock changed the default provider")
	}
}

func TestVaultUnlockErrorDoesNotContinue(t *testing.T) {
	locked := &fakeVaultProvider{}
	withVaultProvider(t, locked)
	unlocker := &fakeVaultUnlocker{fn: func(context.Context, VaultUnlockRequest) (VaultUnlockResult, error) {
		return VaultUnlockResult{}, errors.New("broker failure")
	}}

	result, err := (&VaultListTool{unlocker: unlocker}).Run(context.Background(), VaultListParams{UnlockIfNeeded: true})
	if err != nil {
		t.Fatal(err)
	}
	requireOutputContains(t, result.Output, "unlock_failed", "broker failure")
	if vault.GetDefaultVaultProvider() != locked {
		t.Fatal("failed unlock changed the default provider")
	}
}

func TestVaultUnlockErrorAfterProviderInstallDoesNotStore(t *testing.T) {
	withVaultProvider(t, &fakeVaultProvider{})
	unlocked := enabledMemoryProvider()
	unlocker := &fakeVaultUnlocker{fn: func(context.Context, VaultUnlockRequest) (VaultUnlockResult, error) {
		vault.SetDefaultVaultProvider(unlocked)
		return VaultUnlockResult{}, errors.New("broker failed after install")
	}}

	result, err := (&VaultAddTool{unlocker: unlocker}).Run(context.Background(), VaultAddParams{
		ID: "must-not-store", Kind: vault.CredentialKindAPIKey, Secret: "fake", UnlockIfNeeded: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	requireOutputContains(t, result.Output, "unlock_failed")
	credentials, err := unlocked.GetVault().List(context.Background(), vault.CredentialFilter{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 0 {
		t.Fatalf("failed unlock stored %d credential(s)", len(credentials))
	}
}

func TestVaultToolsWithUnlockerInjectsAllTools(t *testing.T) {
	unlocker := &fakeVaultUnlocker{fn: func(context.Context, VaultUnlockRequest) (VaultUnlockResult, error) {
		return VaultUnlockResult{}, nil
	}}
	if got := len(VaultToolsWithUnlocker(unlocker)); got != 5 {
		t.Fatalf("tool count = %d, want 5", got)
	}
}

func TestVaultAddProjectScopeUsesProjectStorage(t *testing.T) {
	config := vault.VaultConfig{Enabled: true, DefaultMode: vault.ModeBalanced}
	globalStore := vault.NewMemoryStorage()
	projectStore := vault.NewMemoryStorage()
	v := vault.NewVault(globalStore, config)
	v.AddProjectStorage("project-root", projectStore)
	provider := vault.NewVaultProvider(
		vault.NewExecutor(v, config, nil),
		v,
		"project-root",
	).WithAvailableScopes(false, true)
	withVaultProvider(t, provider)

	result, err := (&VaultAddTool{}).Run(context.Background(), VaultAddParams{
		ID:     "project-only",
		Kind:   vault.CredentialKindAPIKey,
		Secret: "fake",
		Scope:  vault.ScopeProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	requireOutputContains(t, result.Output, `"success": true`)

	projectCreds, err := projectStore.List(context.Background(), vault.CredentialFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(projectCreds) != 1 || projectCreds[0].ProjectID != "project-root" {
		t.Fatalf("project credentials = %#v", projectCreds)
	}
	globalCreds, err := globalStore.List(context.Background(), vault.CredentialFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(globalCreds) != 0 {
		t.Fatalf("global credentials = %#v, want none", globalCreds)
	}
}

func TestEnsureVaultUnlockedRequestsMissingScope(t *testing.T) {
	provider := enabledMemoryProvider().(*vault.SimpleVaultProvider)
	provider.WithAvailableScopes(true, false)
	withVaultProvider(t, provider)

	var got VaultUnlockRequest
	unlocker := &fakeVaultUnlocker{fn: func(_ context.Context, req VaultUnlockRequest) (VaultUnlockResult, error) {
		got = req
		provider.WithAvailableScopes(true, true)
		vault.SetDefaultVaultProvider(provider)
		return VaultUnlockResult{Unlocked: true}, nil
	}}
	outcome := ensureVaultUnlocked(context.Background(), true, unlocker, "vault_list", "project")
	if outcome.status != "" {
		t.Fatalf("unlock status = %q, error = %q", outcome.status, outcome.err)
	}
	if unlocker.calls != 1 || got.Scope != "project" || got.Operation != "vault_list" {
		t.Fatalf("unlock request = %#v, calls = %d", got, unlocker.calls)
	}
}

func TestVaultExecUnlocksProjectScopeWhenCredentialMissingGlobally(t *testing.T) {
	const projectID = "project-root"
	config := vault.VaultConfig{Enabled: true, DefaultMode: vault.ModeYOLO}

	globalStore := vault.NewMemoryStorage()
	projectStore := vault.NewMemoryStorage()
	if err := projectStore.Store(context.Background(), vault.Credential{
		ID:        "project-only",
		Kind:      vault.CredentialKindAPIKey,
		Secret:    "not-a-real-secret",
		Scope:     vault.ScopeProject,
		ProjectID: projectID,
		Inject: vault.InjectConfig{
			Method: vault.InjectEnv,
			Target: "ISSUE85_TEST_SECRET",
		},
	}); err != nil {
		t.Fatal(err)
	}
	globalVault := vault.NewVault(globalStore, config)
	// Keep project storage attached while marking its scope unavailable. The
	// tool must respect the provider's scope lock rather than reading through it.
	globalVault.AddProjectStorage(projectID, projectStore)
	globalProvider := vault.NewVaultProvider(
		vault.NewExecutor(globalVault, config, nil),
		globalVault,
		projectID,
	).WithAvailableScopes(true, false)
	withVaultProvider(t, globalProvider)

	unlockedVault := vault.NewVault(globalStore, config)
	unlockedVault.AddProjectStorage(projectID, projectStore)
	unlockedProvider := vault.NewVaultProvider(
		vault.NewExecutor(unlockedVault, config, nil),
		unlockedVault,
		projectID,
	).WithAvailableScopes(true, true)

	var got VaultUnlockRequest
	unlocker := &fakeVaultUnlocker{fn: func(_ context.Context, req VaultUnlockRequest) (VaultUnlockResult, error) {
		got = req
		vault.SetDefaultVaultProvider(unlockedProvider)
		return VaultUnlockResult{Unlocked: true}, nil
	}}

	result, err := (&VaultExecTool{unlocker: unlocker}).Run(context.Background(), VaultExecParams{
		CredentialID:   "project-only",
		Command:        "go",
		Args:           []string{"version"},
		UnlockIfNeeded: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unlocker.calls != 1 || got.Scope != "project" || got.Operation != "vault_exec" {
		t.Fatalf("unlock request = %#v, calls = %d", got, unlocker.calls)
	}
	requireOutputContains(t, result.Output, `"status": "ok"`, "go version")
}

func TestVaultExecUnlocksProjectWhenExpiredGlobalCredentialShadowsID(t *testing.T) {
	const projectID = "project-root"
	config := vault.VaultConfig{Enabled: true, DefaultMode: vault.ModeYOLO}
	expiredAt := time.Now().Add(-time.Hour)

	globalStore := vault.NewMemoryStorage()
	if err := globalStore.Store(context.Background(), vault.Credential{
		ID:        "shared-id",
		Kind:      vault.CredentialKindAPIKey,
		Secret:    "expired-global-secret",
		Scope:     vault.ScopeGlobal,
		ExpiresAt: &expiredAt,
	}); err != nil {
		t.Fatal(err)
	}
	projectStore := vault.NewMemoryStorage()
	if err := projectStore.Store(context.Background(), vault.Credential{
		ID:        "shared-id",
		Kind:      vault.CredentialKindAPIKey,
		Secret:    "valid-project-secret",
		Scope:     vault.ScopeProject,
		ProjectID: projectID,
	}); err != nil {
		t.Fatal(err)
	}

	lockedVault := vault.NewVault(globalStore, config)
	lockedVault.AddProjectStorage(projectID, projectStore)
	lockedProvider := vault.NewVaultProvider(
		vault.NewExecutor(lockedVault, config, nil),
		lockedVault,
		projectID,
	).WithAvailableScopes(true, false)
	withVaultProvider(t, lockedProvider)

	unlockedProvider := vault.NewVaultProvider(
		vault.NewExecutor(lockedVault, config, nil),
		lockedVault,
		projectID,
	).WithAvailableScopes(true, true)
	unlocker := &fakeVaultUnlocker{fn: func(context.Context, VaultUnlockRequest) (VaultUnlockResult, error) {
		vault.SetDefaultVaultProvider(unlockedProvider)
		return VaultUnlockResult{Unlocked: true}, nil
	}}

	result, err := (&VaultExecTool{unlocker: unlocker}).Run(context.Background(), VaultExecParams{
		CredentialID:   "shared-id",
		Command:        "go",
		Args:           []string{"version"},
		UnlockIfNeeded: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unlocker.calls != 1 {
		t.Fatalf("unlock calls = %d, want 1", unlocker.calls)
	}
	requireOutputContains(t, result.Output, `"status": "ok"`, "go version")
}

func TestVaultExecDoesNotUnlockProjectForGlobalCredential(t *testing.T) {
	const projectID = "project-root"
	config := vault.VaultConfig{Enabled: true, DefaultMode: vault.ModeYOLO}
	globalStore := vault.NewMemoryStorage()
	if err := globalStore.Store(context.Background(), vault.Credential{
		ID:     "global-only",
		Kind:   vault.CredentialKindAPIKey,
		Secret: "not-a-real-secret",
		Scope:  vault.ScopeGlobal,
		Inject: vault.InjectConfig{
			Method: vault.InjectEnv,
			Target: "ISSUE85_TEST_SECRET",
		},
	}); err != nil {
		t.Fatal(err)
	}
	v := vault.NewVault(globalStore, config)
	provider := vault.NewVaultProvider(
		vault.NewExecutor(v, config, nil),
		v,
		projectID,
	).WithAvailableScopes(true, false)
	withVaultProvider(t, provider)
	unlocker := &fakeVaultUnlocker{fn: func(context.Context, VaultUnlockRequest) (VaultUnlockResult, error) {
		t.Fatal("global credential unexpectedly requested project unlock")
		return VaultUnlockResult{}, nil
	}}

	result, err := (&VaultExecTool{unlocker: unlocker}).Run(context.Background(), VaultExecParams{
		CredentialID:   "global-only",
		Command:        "go",
		Args:           []string{"version"},
		UnlockIfNeeded: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unlocker.calls != 0 {
		t.Fatalf("unlock calls = %d, want 0", unlocker.calls)
	}
	requireOutputContains(t, result.Output, `"status": "ok"`, "go version")
}

func TestVaultExecStopsBeforeUnlockWhenGlobalMetadataLookupFails(t *testing.T) {
	const projectID = "project-root"
	config := vault.VaultConfig{Enabled: true, DefaultMode: vault.ModeYOLO}
	storage := failingListStorage{Storage: vault.NewMemoryStorage()}
	v := vault.NewVault(storage, config)
	provider := vault.NewVaultProvider(
		vault.NewExecutor(v, config, nil),
		v,
		projectID,
	).WithAvailableScopes(true, false)
	withVaultProvider(t, provider)
	unlocker := &fakeVaultUnlocker{fn: func(context.Context, VaultUnlockRequest) (VaultUnlockResult, error) {
		t.Fatal("metadata failure unexpectedly requested project unlock")
		return VaultUnlockResult{}, nil
	}}

	result, err := (&VaultExecTool{unlocker: unlocker}).Run(context.Background(), VaultExecParams{
		CredentialID:   "project-only",
		Command:        "go",
		Args:           []string{"version"},
		UnlockIfNeeded: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unlocker.calls != 0 {
		t.Fatalf("unlock calls = %d, want 0", unlocker.calls)
	}
	requireOutputContains(t, result.Output, "could not inspect available credentials", "metadata unavailable")
}

func TestVaultAddDefaultScopeRejectsProjectOnlyProvider(t *testing.T) {
	config := vault.VaultConfig{Enabled: true, DefaultMode: vault.ModeBalanced}
	globalFallback := vault.NewMemoryStorage()
	projectStore := vault.NewMemoryStorage()
	v := vault.NewVault(globalFallback, config)
	v.AddProjectStorage("project-root", projectStore)
	provider := vault.NewVaultProvider(
		vault.NewExecutor(v, config, nil),
		v,
		"project-root",
	).WithAvailableScopes(false, true)
	withVaultProvider(t, provider)

	result, err := (&VaultAddTool{}).Run(context.Background(), VaultAddParams{
		ID: "must-not-persist", Kind: vault.CredentialKindAPIKey, Secret: "fake",
	})
	if err != nil {
		t.Fatal(err)
	}
	requireOutputContains(t, result.Output, "global vault is not available")
	creds, err := globalFallback.List(context.Background(), vault.CredentialFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 0 {
		t.Fatalf("volatile fallback credentials = %#v, want none", creds)
	}
}
