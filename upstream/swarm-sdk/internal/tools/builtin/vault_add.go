package builtin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
)

// VaultAddParams are parameters for the vault_add tool.
type VaultAddParams struct {
	// UnlockIfNeeded asks an interactive host to unlock a locked vault.
	UnlockIfNeeded bool `json:"unlockIfNeeded,omitempty" description:"If true, ask the host to unlock a locked vault; TUI support is optional and headless hosts return immediately"`

	// ID is a unique identifier for the credential.
	ID string `json:"id" description:"Unique ID for the credential (e.g., 'github-token', 'aws-prod')" required:"true"`

	// Name is a human-readable name.
	Name string `json:"name,omitempty" description:"Human-readable name"`

	// Kind is the type of credential.
	Kind vault.CredentialKind `json:"kind" description:"Credential kind: api_key, bearer_token, ssh_key, aws_access_key, aws_secret_key, password, env_var" required:"true"`

	// Secret is the secret value to store.
	// Required when adding a brand-new credential. May be omitted when
	// updating an EXISTING credential id to change only ACL fields
	// (allowedTools/allowedCommands/allowedHosts/tags/target/expire) while
	// leaving the stored secret untouched -- the vault never exposes a
	// secret back to a caller, so there is no other safe way to make an
	// ACL-only change to an existing credential (issue #219).
	Secret string `json:"secret,omitempty" description:"The secret value to store (encrypted at rest in the age vault, or base64-obfuscated (NOT encrypted) in a transparent vault). Omit to update ACL/metadata fields on an EXISTING credential id without touching its stored secret."`

	// Scope is global or project.
	Scope vault.CredentialScope `json:"scope,omitempty" description:"Scope: global (default) or project"`

	// AllowedTools restricts which tools can use this credential.
	AllowedTools []string `json:"allowedTools,omitempty" description:"Tools allowed to use this credential (e.g., ['bash', 'curl'])"`

	// AllowedCommands restricts which commands can use this credential.
	AllowedCommands []string `json:"allowedCommands,omitempty" description:"Command patterns allowed (e.g., ['aws *', 'git clone *'])"`

	// AllowedHosts restricts which hosts this credential can access.
	AllowedHosts []string `json:"allowedHosts,omitempty" description:"Host patterns allowed (e.g., ['github.com', '*.amazonaws.com'])"`

	// Tags for organization.
	Tags []string `json:"tags,omitempty" description:"Tags for filtering (e.g., ['sensitive', 'production', 'ci'])"`

	// Target is the env var name or file path for injection.
	Target string `json:"target,omitempty" description:"Env var name or file path for injection (e.g., 'GITHUB_TOKEN', '/tmp/ssh-key')"`

	// Expire is the expiration duration (e.g., '24h', '7d', '30d').
	Expire string `json:"expire,omitempty" description:"Expiration duration: 24h, 7d, 30d, 90d, 1y"`

	// Threshold, when >= 2, stores the credential with Two-Person Integrity: at
	// least this many DISTINCT team members must approve before it can be used.
	// Requires a team roster (recipients.txt). A "sensitive" tag also enables
	// two-person storage with a default threshold of 2.
	Threshold int `json:"threshold,omitempty" description:"If >=2, store as two-person (N-of-M): this many distinct approvers required to use it. Requires a team roster."`
}

// VaultAddResult is the result of vault_add.
type VaultAddResult struct {
	// Success indicates whether the credential was stored.
	Success bool `json:"success"`

	// CredentialID that was added.
	CredentialID string `json:"credentialId"`

	// Scope of the stored credential.
	Scope string `json:"scope"`

	// Kind of the stored credential.
	Kind string `json:"kind"`

	// Error message if storage failed.
	Error string `json:"error,omitempty"`

	// Warning about requiring user confirmation.
	Warning string `json:"warning,omitempty"`

	// MetadataOnly is true when this call updated ACL/metadata fields on an
	// existing credential without touching the stored secret (secret was
	// omitted). Absent or false means the secret was created or replaced.
	MetadataOnly bool `json:"metadataOnly,omitempty"`

	// Status reports structured unlock outcomes when applicable.
	Status string `json:"status,omitempty"`
}

// VaultAddTool adds credentials to the vault.
type VaultAddTool struct {
	tools.BaseTool
	unlocker VaultUnlocker
}

// NewVaultAddTool creates a new vault_add tool.
func NewVaultAddTool() tools.Tool {
	return NewVaultAddToolWithUnlocker(nil)
}

// NewVaultAddToolWithUnlocker creates a vault_add tool with an optional host unlock broker.
func NewVaultAddToolWithUnlocker(unlocker VaultUnlocker) tools.Tool {
	return tools.Typed[VaultAddParams](&VaultAddTool{unlocker: unlocker})
}

// Name returns the tool name.
func (t *VaultAddTool) Name() string { return "vault_add" }

// Description returns the tool description.
func (t *VaultAddTool) Description() string {
	return `Add a new credential to the shared vault.

The vault is a shared local credential store. Once unlocked, all agents and
TUI sessions on this machine can use the same credentials freely.

Store credentials whenever the user provides one or you discover one during a
session. The credential is available immediately for use with vault_exec.

Example:
  vault_add {id: "github-token", kind: "bearer_token", secret: "ghp_...", target: "GITHUB_TOKEN"}
  -> {success: true, credentialId: "github-token"}

To change ONLY allowedTools/allowedCommands/allowedHosts/tags/target on an EXISTING
credential without touching its stored secret (the vault never returns a secret to you,
so there is no other safe way to re-supply it), omit 'secret' entirely:
  vault_add {id: "github-token", allowedCommands: ["gh *"]}
  -> {success: true, credentialId: "github-token", metadataOnly: true}
A 'secret' that IS supplied for kind=ssh_key is validated as PEM/OpenSSH key material and
rejected with an explicit error if it is not -- it is never silently accepted or discarded.

Set unlockIfNeeded:true if the vault might be locked.`
}

// Parameters returns the parameter schema.
func (t *VaultAddTool) Parameters() any { return tools.SchemaFor[VaultAddParams]() }

// IsIdempotent returns false — adding credentials changes state.
func (t *VaultAddTool) IsIdempotent() bool { return false }

// RequiresPermission returns secret access permission.
func (t *VaultAddTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionSecretAccess}
}

// SupportedContentTypes returns supported types.
func (t *VaultAddTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// sshPrivateKeyMarkers are the envelope strings that mark real PEM/OpenSSH
// private key material. This is a format sniff, not a parser: good enough to
// reject an obviously-not-a-key placeholder string (issue #219's reproduction
// was literal garbage text passed as a "secret" for kind=ssh_key) without
// adding a crypto/PEM parsing dependency to a builtin tool. A well-formed key
// of any supported algorithm (RSA/Ed25519/ECDSA, PKCS#1/PKCS#8/OpenSSH
// envelope) always contains one of these BEGIN markers.
var sshPrivateKeyMarkers = []string{
	"-----BEGIN OPENSSH PRIVATE KEY-----",
	"-----BEGIN RSA PRIVATE KEY-----",
	"-----BEGIN EC PRIVATE KEY-----",
	"-----BEGIN DSA PRIVATE KEY-----",
	"-----BEGIN PRIVATE KEY-----",
	"-----BEGIN ENCRYPTED PRIVATE KEY-----",
}

// looksLikeSSHPrivateKey reports whether secret has the envelope of a real
// PEM or OpenSSH private key. It intentionally only sniffs the BEGIN marker
// (not the END marker or base64 body) so a truncated-but-genuine key is not
// treated the same as garbage/placeholder text -- the goal is to catch the
// unambiguous non-key case, not to fully validate the key.
func looksLikeSSHPrivateKey(secret string) bool {
	trimmed := strings.TrimSpace(secret)
	for _, marker := range sshPrivateKeyMarkers {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}
	return false
}

// Run stores a credential in the vault.
func (t *VaultAddTool) Run(ctx context.Context, params VaultAddParams) (*tools.ToolResult, error) {
	if params.ID == "" {
		return jsonResult(VaultAddResult{
			Success: false,
			Error:   "id is required",
		}), nil
	}

	if params.Kind == "" {
		return jsonResult(VaultAddResult{
			Success: false,
			Error:   "kind is required",
		}), nil
	}

	// A "secret" that is provided but is unambiguously NOT valid material for
	// the declared kind must be rejected outright rather than silently
	// accepted -- a bare {success:true} here is indistinguishable from a real,
	// intended replacement, and issue #219's exact reproduction was a
	// placeholder/garbage string passed as an ssh_key secret. Only ssh_key has
	// a cheap, reliable format sniff; other kinds (api_key, bearer_token,
	// password, ...) have no fixed wire format to validate against and keep
	// accepting any non-empty string, as before.
	if params.Secret != "" && params.Kind == vault.CredentialKindSSHKey && !looksLikeSSHPrivateKey(params.Secret) {
		return jsonResult(VaultAddResult{
			Success: false,
			Error: "secret does not look like a valid ssh_key (expected PEM or OpenSSH private key material " +
				"starting with a \"-----BEGIN ... PRIVATE KEY-----\" line); the credential was NOT modified. " +
				"Omit 'secret' to update allowedTools/allowedCommands/allowedHosts/tags on an existing credential " +
				"without touching its stored key.",
		}), nil
	}

	var expiration time.Duration
	if params.Expire != "" {
		d, err := time.ParseDuration(params.Expire)
		if err != nil {
			return jsonResult(VaultAddResult{
				Success: false,
				Error:   fmt.Sprintf("invalid expiration duration '%s': %v", params.Expire, err),
			}), nil
		}
		expiration = d
	}

	requestedScope := params.Scope
	if requestedScope == "" {
		requestedScope = vault.ScopeGlobal
	}
	unlock := ensureVaultUnlocked(ctx, params.UnlockIfNeeded, t.unlocker, t.Name(), string(requestedScope))
	provider := unlock.provider
	if unlock.status != "" {
		return jsonResult(VaultAddResult{Success: false, Status: unlock.status, Error: unlock.err}), nil
	}
	if !provider.IsEnabled() {
		return jsonResult(VaultAddResult{
			Success: false,
			Error:   "vault is locked — tell the user to unlock the vault by typing /vault in the TUI (or Settings → Vault, or 'swarmos vault unlock' in CLI) before adding credentials",
		}), nil
	}
	if !vault.IsProviderScopeEnabled(provider, requestedScope) {
		return jsonResult(VaultAddResult{
			Success: false,
			Error:   fmt.Sprintf("%s vault is not available", requestedScope),
		}), nil
	}

	// Determine injection method based on kind
	inject := vault.InjectConfig{
		Method: vault.InjectEnv,
		Target: params.Target,
	}
	if params.Kind == vault.CredentialKindSSHKey {
		inject.Method = vault.InjectFile
	}

	v := provider.GetVault()
	// A metadata-only update: an existing credential id with 'secret' omitted
	// keeps its stored secret untouched and only applies the ACL/metadata
	// fields on this call. This is the only safe way to change
	// allowedTools/allowedCommands/allowedHosts/tags on an existing
	// credential, because the vault never exposes a secret back to any
	// caller for round-tripping (issue #219).
	metadataOnly := false
	if params.Secret == "" {
		existing, err := v.ResolveCredential(ctx, params.ID, provider.GetProjectID())
		if err != nil || existing == nil {
			return jsonResult(VaultAddResult{
				Success: false,
				Error:   "secret is required when adding a new credential (no existing credential found for id " + params.ID + ")",
			}), nil
		}
		params.Secret = existing.Secret
		if params.Target == "" {
			inject.Target = existing.Inject.Target
			inject.Method = existing.Inject.Method
		}
		metadataOnly = true
	}

	// Build credential
	cred := vault.Credential{
		ID:              params.ID,
		Name:            params.Name,
		Kind:            params.Kind,
		Secret:          params.Secret,
		Scope:           requestedScope,
		AllowedTools:    params.AllowedTools,
		AllowedCommands: params.AllowedCommands,
		AllowedHosts:    params.AllowedHosts,
		Tags:            params.Tags,
		Inject:          inject,
	}

	if cred.Scope == vault.ScopeProject {
		cred.ProjectID = provider.GetProjectID()
		if cred.ProjectID == "" ||
			!vault.IsProviderScopeEnabled(provider, vault.ScopeProject) {
			return jsonResult(VaultAddResult{
				Success: false,
				Error:   "project vault is not available for project-scoped credential",
			}), nil
		}
	}

	// Handle expiration
	if params.Expire != "" {
		exp := time.Now().Add(expiration)
		cred.ExpiresAt = &exp
	}

	// Two-person path: sensitive credentials are sealed to the team roster so
	// that a distinct second approver is required to use them.
	threshold := params.Threshold
	if threshold < 2 {
		for _, tag := range params.Tags {
			if tag == "sensitive" {
				threshold = 2
				break
			}
		}
	}
	if threshold >= 2 {
		store, recipientsPath := provider.GetTwoPersonStore()
		if store == nil || recipientsPath == "" {
			return jsonResult(VaultAddResult{
				Success: false,
				Error:   "two-person storage requires a team roster (recipients.txt) and a user identity; run 'swarmos vault init --project --team' and add members with 'swarmos vault allow' first",
			}), nil
		}
		if err := vault.SealNewTwoPerson(store, recipientsPath, cred, threshold); err != nil {
			return jsonResult(VaultAddResult{
				Success: false,
				Error:   fmt.Sprintf("failed to seal two-person credential: %v", err),
			}), nil
		}
		return jsonResult(VaultAddResult{
			Success:      true,
			CredentialID: params.ID,
			Scope:        string(cred.Scope),
			Kind:         string(cred.Kind),
			Warning: fmt.Sprintf("Credential sealed with Two-Person Integrity (threshold %d). "+
				"Using it requires %d distinct approvers via the vault_exec/vault_approve flow. "+
				"The secret is never returned to you and cannot be reconstructed by any single machine.", threshold, threshold),
		}), nil
	}

	// Store in vault
	if err := v.Store(ctx, cred); err != nil {
		return jsonResult(VaultAddResult{
			Success: false,
			Error:   fmt.Sprintf("failed to store credential: %v", err),
		}), nil
	}

	warning := "Credential stored. It can now be used with vault_exec."
	if metadataOnly {
		warning = "Credential metadata updated (allowedTools/allowedCommands/allowedHosts/tags/target); the stored secret was left unchanged."
	}
	return jsonResult(VaultAddResult{
		Success:      true,
		CredentialID: params.ID,
		Scope:        string(cred.Scope),
		Kind:         string(cred.Kind),
		MetadataOnly: metadataOnly,
		Warning:      warning,
	}), nil
}
