package tools

import "testing"

func TestBuildPermissionContext_NormalizesVaultCredentialID(t *testing.T) {
	ctxData := BuildPermissionContext(
		"vault_exec",
		map[string]any{
			"credentialId": "github-token",
			"command":      "gh",
			"args":         []string{"repo", "view"},
		},
		[]Permission{PermissionSecretAccess},
		ScopeGlobal,
		"",
		"",
		"",
		"",
	)

	if got := ctxData["credential"]; got != "github-token" {
		t.Fatalf("credential = %#v, want %q", got, "github-token")
	}
}

func TestBuildPermissionContext_AcceptsLegacyVaultCredential(t *testing.T) {
	ctxData := BuildPermissionContext(
		"vault_exec",
		map[string]any{"credential": "legacy-token"},
		[]Permission{PermissionSecretAccess},
		ScopeGlobal,
		"",
		"",
		"",
		"",
	)

	if got := ctxData["credential"]; got != "legacy-token" {
		t.Fatalf("credential = %#v, want %q", got, "legacy-token")
	}
}
