package builtin

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// mustUnmarshal decodes a tool result's JSON output into dst, failing the
// test immediately on any decode error. vault_add's jsonResult wraps the
// JSON payload in an XML envelope (<result><data><![CDATA[...]]></data></result>),
// so the CDATA body is extracted first.
func mustUnmarshal(t *testing.T, res *tools.ToolResult, dst any) {
	t.Helper()
	body := res.Output
	if start := strings.Index(body, "<![CDATA["); start >= 0 {
		if end := strings.Index(body[start:], "]]>"); end >= 0 {
			body = body[start+len("<![CDATA[") : start+end]
		}
	}
	if err := json.Unmarshal([]byte(body), dst); err != nil {
		t.Fatalf("unmarshal tool result %q: %v", res.Output, err)
	}
}

// TestVaultAddRejectsGarbageSSHKeySecret proves issue #219's core acceptance
// test: calling vault_add on an existing (or new) ssh_key credential with a
// 'secret' value that is obviously not real key material (the reported
// reproduction: a placeholder/garbage string) must NOT return a bare
// {success:true} indistinguishable from a real replacement -- it must be
// rejected with an explicit error, and the credential must be left
// completely untouched (both the original secret AND the ACL fields the
// caller was trying to change).
func TestVaultAddRejectsGarbageSSHKeySecret(t *testing.T) {
	dir := t.TempDir()
	provider := setupPassphraseVaultProvider(t, dir)
	prev := vault.GetDefaultVaultProvider()
	vault.SetDefaultVaultProvider(provider)
	defer vault.SetDefaultVaultProvider(prev)

	addTool := &VaultAddTool{}
	ctx := context.Background()

	realKey := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZWQyNTUxOQAAACDUMMSGrealKeyBytesHere==\n-----END OPENSSH PRIVATE KEY-----"
	res, err := addTool.Run(ctx, VaultAddParams{
		ID:     "clover-api-vps-key",
		Kind:   vault.CredentialKindSSHKey,
		Secret: realKey,
		Target: "/tmp/clover-key",
	})
	if err != nil {
		t.Fatalf("initial add: %v", err)
	}
	var addResult VaultAddResult
	mustUnmarshal(t, res, &addResult)
	if !addResult.Success {
		t.Fatalf("initial add with a real-looking key should succeed: %+v", addResult)
	}

	// The reported reproduction: pass a garbage placeholder as 'secret' while
	// trying to add a new entry to allowedCommands.
	res2, err := addTool.Run(ctx, VaultAddParams{
		ID:              "clover-api-vps-key",
		Kind:            vault.CredentialKindSSHKey,
		Secret:          "REDACTED-PLACEHOLDER-NOT-A-REAL-KEY",
		AllowedCommands: []string{"rsync *", "scp *"},
	})
	if err != nil {
		t.Fatalf("garbage-secret add: %v", err)
	}
	var badResult VaultAddResult
	mustUnmarshal(t, res2, &badResult)
	if badResult.Success {
		t.Fatalf("garbage ssh_key secret must be rejected, got success: %+v", badResult)
	}
	if badResult.Error == "" {
		t.Fatalf("expected a non-empty error naming the validation failure, got: %+v", badResult)
	}

	// The credential must be completely untouched: secret intact, ACL NOT
	// updated (since the whole call was rejected).
	cred, err := provider.GetVault().ResolveCredential(ctx, "clover-api-vps-key", "")
	if err != nil {
		t.Fatalf("resolve after rejected update: %v", err)
	}
	if cred.Secret != realKey {
		t.Fatalf("secret was modified by a rejected call: got %q", cred.Secret)
	}
	if len(cred.AllowedCommands) != 0 {
		t.Fatalf("ACL fields were modified by a rejected call: %+v", cred.AllowedCommands)
	}
}

// TestVaultAddAcceptsRealSSHKeyReplacement is the near-miss that must remain
// allowed: a well-formed real key for kind=ssh_key must continue to succeed
// and actually replace the stored secret, exactly as before this fix.
func TestVaultAddAcceptsRealSSHKeyReplacement(t *testing.T) {
	dir := t.TempDir()
	provider := setupPassphraseVaultProvider(t, dir)
	prev := vault.GetDefaultVaultProvider()
	vault.SetDefaultVaultProvider(provider)
	defer vault.SetDefaultVaultProvider(prev)

	addTool := &VaultAddTool{}
	ctx := context.Background()

	key1 := "-----BEGIN OPENSSH PRIVATE KEY-----\nfirstkeybytes\n-----END OPENSSH PRIVATE KEY-----"
	key2 := "-----BEGIN OPENSSH PRIVATE KEY-----\nsecondkeybytes\n-----END OPENSSH PRIVATE KEY-----"

	if _, err := addTool.Run(ctx, VaultAddParams{ID: "host-key", Kind: vault.CredentialKindSSHKey, Secret: key1}); err != nil {
		t.Fatalf("add key1: %v", err)
	}
	res, err := addTool.Run(ctx, VaultAddParams{ID: "host-key", Kind: vault.CredentialKindSSHKey, Secret: key2})
	if err != nil {
		t.Fatalf("add key2: %v", err)
	}
	var result VaultAddResult
	mustUnmarshal(t, res, &result)
	if !result.Success {
		t.Fatalf("replacing with a second real key should succeed: %+v", result)
	}
	cred, err := provider.GetVault().ResolveCredential(ctx, "host-key", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cred.Secret != key2 {
		t.Fatalf("secret = %q, want the replacement key2", cred.Secret)
	}
}

// TestVaultAddMetadataOnlyUpdateOmitsSecret proves the "Expected" fix's
// primary path from issue #219: omitting 'secret' entirely on an existing
// credential id updates ONLY the ACL/metadata fields and leaves the stored
// secret completely untouched, with the response flagging MetadataOnly=true
// so a caller can positively confirm what happened instead of guessing.
func TestVaultAddMetadataOnlyUpdateOmitsSecret(t *testing.T) {
	dir := t.TempDir()
	provider := setupPassphraseVaultProvider(t, dir)
	prev := vault.GetDefaultVaultProvider()
	vault.SetDefaultVaultProvider(provider)
	defer vault.SetDefaultVaultProvider(prev)

	addTool := &VaultAddTool{}
	ctx := context.Background()

	realKey := "-----BEGIN OPENSSH PRIVATE KEY-----\noriginalkeybytes\n-----END OPENSSH PRIVATE KEY-----"
	if _, err := addTool.Run(ctx, VaultAddParams{
		ID:              "clover-api-vps-key",
		Kind:            vault.CredentialKindSSHKey,
		Secret:          realKey,
		Target:          filepath.Join(dir, "clover-key"),
		AllowedCommands: []string{"rsync *"},
	}); err != nil {
		t.Fatalf("initial add: %v", err)
	}

	// Metadata-only update: no 'secret' at all, just extending the
	// allow-list -- exactly the scenario the issue reporter needed and had
	// no safe way to do.
	res, err := addTool.Run(ctx, VaultAddParams{
		ID:              "clover-api-vps-key",
		Kind:            vault.CredentialKindSSHKey,
		AllowedCommands: []string{"rsync *", "scp *"},
	})
	if err != nil {
		t.Fatalf("metadata-only update: %v", err)
	}
	var result VaultAddResult
	mustUnmarshal(t, res, &result)
	if !result.Success {
		t.Fatalf("metadata-only update should succeed: %+v", result)
	}
	if !result.MetadataOnly {
		t.Fatalf("expected MetadataOnly=true in the response: %+v", result)
	}

	cred, err := provider.GetVault().ResolveCredential(ctx, "clover-api-vps-key", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cred.Secret != realKey {
		t.Fatalf("metadata-only update modified the secret: got %q, want %q", cred.Secret, realKey)
	}
	if len(cred.AllowedCommands) != 2 || cred.AllowedCommands[1] != "scp *" {
		t.Fatalf("ACL fields were not applied: %+v", cred.AllowedCommands)
	}
}

// TestVaultAddMetadataOnlyUpdateRequiresExistingCredential proves omitting
// 'secret' for a BRAND NEW credential id is still rejected -- the allowance
// is specifically for updating an existing credential, not for creating one
// with no secret at all.
func TestVaultAddMetadataOnlyUpdateRequiresExistingCredential(t *testing.T) {
	dir := t.TempDir()
	provider := setupPassphraseVaultProvider(t, dir)
	prev := vault.GetDefaultVaultProvider()
	vault.SetDefaultVaultProvider(provider)
	defer vault.SetDefaultVaultProvider(prev)

	addTool := &VaultAddTool{}
	ctx := context.Background()

	res, err := addTool.Run(ctx, VaultAddParams{ID: "brand-new", Kind: vault.CredentialKindAPIKey})
	if err != nil {
		t.Fatalf("add with no secret: %v", err)
	}
	var result VaultAddResult
	mustUnmarshal(t, res, &result)
	if result.Success {
		t.Fatalf("expected failure creating a new credential with no secret, got: %+v", result)
	}
}
