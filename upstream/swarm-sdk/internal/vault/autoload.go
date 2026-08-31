package vault

// Non-interactive vault auto-load for the headless harness (swarmos -p) and the
// daemon. Unlike the TUI unlock flow, this NEVER prompts for a passphrase: it
// loads only what can be opened without interaction —
//
//   - a transparent (base64, no-password) global store, if present; and
//   - an identity-based team vault (MultiRecipientStorage) + a two-person store
//     for the project, if an age identity + recipients.txt exist.
//
// The user's private age identity (0600) stays on disk; only its path is carried
// so two-person begin/approve can load it on demand. Sensitive credentials still
// require the two-person approval flow — auto-load makes them AVAILABLE, not
// silently usable.

import (
	"context"
	"os"
	"path/filepath"
)

// AutoLoadPaths describes where the non-interactive loader looks for material.
type AutoLoadPaths struct {
	// IdentityPath is the user's age identity file (private key).
	IdentityPath string
	// RecipientsPath is the project recipients.txt (team public keys).
	RecipientsPath string
	// TeamVaultPath is the identity-encrypted project vault (MultiRecipientStorage).
	TeamVaultPath string
	// TwoPersonPath is the two-person (2-of-N) project vault JSON.
	TwoPersonPath string
	// TransparentPath is the no-password global store.
	TransparentPath string
	// ProjectID scopes project storage.
	ProjectID string
}

// DefaultAutoLoadPaths returns the conventional locations given a home dir and a
// workspace root. Global material lives under ~/.swarm/vault; project material
// under <workspace>/.swarm/vault.
func DefaultAutoLoadPaths(home, workspaceRoot, projectID string) AutoLoadPaths {
	p := AutoLoadPaths{
		IdentityPath:    filepath.Join(home, ".swarm", "vault", "identity"),
		TransparentPath: filepath.Join(home, ".swarm", "vault", "credentials.json"),
		ProjectID:       projectID,
	}
	if workspaceRoot != "" {
		p.RecipientsPath = filepath.Join(workspaceRoot, ".swarm", "vault", "recipients.txt")
		p.TeamVaultPath = filepath.Join(workspaceRoot, ".swarm", "vault", "project.vault")
		p.TwoPersonPath = filepath.Join(workspaceRoot, ".swarm", "vault", "twoperson.json")
	}
	return p
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(expandPath(path))
	return err == nil
}

// AutoLoadProvider builds a VaultProvider from whatever can be opened WITHOUT a
// passphrase. Returns (nil, false) when nothing is available. It is safe to call
// on every startup; it never blocks or prompts.
//
// Precedence:
//   - global := transparent store (if present)
//   - project := identity-based team vault (if identity+recipients+vault present)
//   - project two-person store is layered so sensitive creds resolve as 2-of-N
func AutoLoadProvider(paths AutoLoadPaths) (VaultProvider, bool) {
	var globalStorage Storage
	var projectStorage Storage

	// Global transparent store (no password).
	if fileExists(paths.TransparentPath) {
		if ts, err := NewTransparentStorage(paths.TransparentPath); err == nil {
			globalStorage = ts
		}
	}

	// Identity-based team vault for the project (requires the user's identity).
	haveIdentity := fileExists(paths.IdentityPath)
	if haveIdentity && fileExists(paths.RecipientsPath) && fileExists(paths.TeamVaultPath) {
		if mrs, err := NewMultiRecipientStorageFromFiles(paths.TeamVaultPath, paths.IdentityPath, paths.RecipientsPath); err == nil {
			projectStorage = mrs
		}
	}

	// Two-person store for the project (sensitive, 2-of-N). If present it is
	// LAYERED IN FRONT of any plain team vault so sensitive ids resolve as
	// two-person (and can never be down-graded), while normal 1-of-N creds in
	// the team vault remain resolvable via the fallthrough layer.
	var twoPersonStore *TwoPersonStorage
	if fileExists(paths.TwoPersonPath) {
		if tps, err := NewTwoPersonStorage(paths.TwoPersonPath); err == nil {
			twoPersonStore = tps
			if projectStorage != nil {
				projectStorage = newLayeredStorage(tps, projectStorage)
			} else {
				projectStorage = tps
			}
		}
	}

	if globalStorage == nil && projectStorage == nil {
		return nil, false
	}

	// If global is still nil, use an empty in-memory store so Vault always has a
	// non-nil global backend.
	if globalStorage == nil {
		globalStorage = NewMemoryStorage()
	}

	v := NewVault(globalStorage, VaultConfig{Enabled: true, DefaultMode: ModeYOLO, TrustedUnlocked: true})
	if projectStorage != nil && paths.ProjectID != "" {
		v.AddProjectStorage(paths.ProjectID, projectStorage)
	}

	exec := NewExecutor(v, VaultConfig{Enabled: true, DefaultMode: ModeYOLO, TrustedUnlocked: true}, nil)
	provider := NewVaultProvider(exec, v, "")
	if haveIdentity {
		provider = provider.WithIdentityPath(paths.IdentityPath)
	}
	// Expose the two-person store + roster for the vault_add sensitive path. If a
	// roster exists but no two-person store yet, create an empty one at the
	// conventional path so sensitive creds can be sealed on demand.
	if twoPersonStore == nil && haveIdentity && fileExists(paths.RecipientsPath) && paths.TwoPersonPath != "" {
		if tps, err := NewTwoPersonStorage(paths.TwoPersonPath); err == nil {
			twoPersonStore = tps
		}
	}
	if twoPersonStore != nil && fileExists(paths.RecipientsPath) {
		provider = provider.WithTwoPersonStore(twoPersonStore, paths.RecipientsPath)
	}
	// Reject a provider that opens but cannot actually list (e.g. a corrupt team
	// vault): better to report "no vault" than to advertise IsEnabled()==true
	// while every operation fails.
	if !verifyProviderUsable(provider) {
		return nil, false
	}
	return provider, true
}

// AutoLoadInto builds a provider from the default paths and installs it as the
// global default provider (used by agent tools). Returns true if a provider was
// installed. Non-interactive and safe to call unconditionally at startup.
func AutoLoadInto(home, workspaceRoot, projectID string) bool {
	paths := DefaultAutoLoadPaths(home, workspaceRoot, projectID)
	provider, ok := AutoLoadProvider(paths)
	if !ok {
		return false
	}
	SetDefaultVaultProvider(provider)
	return true
}

// verifyProviderUsable is a small sanity check used by callers/tests.
func verifyProviderUsable(p VaultProvider) bool {
	if p == nil || !p.IsEnabled() {
		return false
	}
	_, err := p.GetVault().List(context.Background(), CredentialFilter{}, p.GetProjectID())
	return err == nil
}

// layeredStorage resolves credentials against a front storage first, falling
// through to a back storage. It is used to layer a TwoPersonStorage (sensitive,
// 2-of-N) in front of a plain team vault so both coexist: sensitive ids resolve
// as two-person, normal ids fall through. The front storage's two-person
// awareness is exposed so the executor's IsTwoPerson/envelope lookups work.
type layeredStorage struct {
	front twoPersonAware // also a Storage
	back  Storage
}

func newLayeredStorage(front *TwoPersonStorage, back Storage) *layeredStorage {
	return &layeredStorage{front: front, back: back}
}

func (l *layeredStorage) frontStorage() Storage { return l.front.(Storage) }

func (l *layeredStorage) Store(ctx context.Context, cred Credential) error {
	// Metadata-only updates go to whichever layer already holds the id; default
	// to the back (normal) store for brand-new plain creds.
	if l.front.IsTwoPerson(cred.ID) {
		return l.frontStorage().Store(ctx, cred)
	}
	return l.back.Store(ctx, cred)
}

func (l *layeredStorage) Retrieve(ctx context.Context, id string) (*Credential, error) {
	if l.front.IsTwoPerson(id) {
		return l.frontStorage().Retrieve(ctx, id)
	}
	return l.back.Retrieve(ctx, id)
}

func (l *layeredStorage) Delete(ctx context.Context, id string) error {
	if l.front.IsTwoPerson(id) {
		return l.frontStorage().Delete(ctx, id)
	}
	return l.back.Delete(ctx, id)
}

func (l *layeredStorage) List(ctx context.Context, filter CredentialFilter) ([]Credential, error) {
	front, err := l.frontStorage().List(ctx, filter)
	if err != nil {
		return nil, err
	}
	back, err := l.back.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(front))
	out := make([]Credential, 0, len(front)+len(back))
	for _, c := range front {
		seen[c.ID] = true
		out = append(out, c)
	}
	for _, c := range back {
		if !seen[c.ID] {
			out = append(out, c)
		}
	}
	return out, nil
}

func (l *layeredStorage) RetrieveForExecution(ctx context.Context, id string, projectID string) (*Credential, error) {
	if l.front.IsTwoPerson(id) {
		return l.frontStorage().RetrieveForExecution(ctx, id, projectID)
	}
	return l.back.RetrieveForExecution(ctx, id, projectID)
}

func (l *layeredStorage) UpdateLastUsed(ctx context.Context, id string) error {
	if l.front.IsTwoPerson(id) {
		return l.frontStorage().UpdateLastUsed(ctx, id)
	}
	return l.back.UpdateLastUsed(ctx, id)
}

// IsTwoPerson exposes the front layer's two-person awareness.
func (l *layeredStorage) IsTwoPerson(id string) bool { return l.front.IsTwoPerson(id) }

// GetEnvelope exposes the front layer's envelope lookup.
func (l *layeredStorage) GetEnvelope(id string) (*TwoPersonEnvelope, error) {
	return l.front.GetEnvelope(id)
}

var (
	_ Storage        = (*layeredStorage)(nil)
	_ twoPersonAware = (*layeredStorage)(nil)
)
