package vault

import (
	"context"
	"testing"
)

// TestLayeredStorageCoexistence proves the precedence fix: a two-person store
// layered in front of a plain (team) vault lets BOTH normal 1-of-N creds and
// sensitive 2-of-N creds resolve. Regression for the autoload overwrite bug.
func TestLayeredStorageCoexistence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Back: a plain store with a NORMAL credential (has a usable secret).
	back := NewMemoryStorage()
	if err := back.Store(ctx, Credential{
		ID: "normal-key", Kind: CredentialKindAPIKey, Secret: "plain-secret",
		Scope: ScopeProject, Inject: InjectConfig{Method: InjectEnv, Target: "TOKEN"},
	}); err != nil {
		t.Fatal(err)
	}

	// Front: a two-person store with a SENSITIVE credential.
	tps, err := NewTwoPersonStorage(dir + "/tp.json")
	if err != nil {
		t.Fatal(err)
	}
	_, recs := makeTestRecipients(t, 2)
	if err := tps.SealAndStore(ctx, Credential{
		ID: "sensitive-key", Kind: CredentialKindAWSSecretKey, Secret: "AKIASENSITIVE",
		Scope: ScopeProject, Inject: InjectConfig{Method: InjectEnv, Target: "AWS"},
	}, 2, recs); err != nil {
		t.Fatal(err)
	}

	layered := newLayeredStorage(tps, back)
	v := NewVault(layered, VaultConfig{Enabled: true, DefaultMode: ModeYOLO})
	pid := "proj"
	v.AddProjectStorage(pid, layered)

	// Normal cred resolves WITH its secret (via the back layer).
	c, err := v.ResolveCredential(ctx, "normal-key", pid)
	if err != nil {
		t.Fatalf("normal cred should resolve: %v", err)
	}
	if c.Secret != "plain-secret" {
		t.Fatalf("normal cred secret lost: %q", c.Secret)
	}

	// Sensitive cred is recognized as two-person (front layer).
	if !v.IsTwoPerson("sensitive-key", pid) {
		t.Fatal("sensitive cred should be two-person")
	}
	if v.IsTwoPerson("normal-key", pid) {
		t.Fatal("normal cred must NOT be two-person")
	}

	// List shows BOTH.
	creds, err := v.List(ctx, CredentialFilter{}, pid)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, c := range creds {
		ids[c.ID] = true
	}
	if !ids["normal-key"] || !ids["sensitive-key"] {
		t.Fatalf("List missing creds: %v", ids)
	}

	// Envelope for the sensitive cred is reachable through the layer.
	if _, err := v.ResolveTwoPersonEnvelope("sensitive-key", pid); err != nil {
		t.Fatalf("envelope should resolve through layer: %v", err)
	}
}
