package vault

import (
	"context"
	"strings"
	"testing"
)

// TestExecutorTwoPersonRoundTrip drives the full executor-level flow:
// Execute refuses (ErrTwoPersonRequired) -> BeginTwoPerson (requester share) ->
// ApproveTwoPerson (distinct approver) -> FinalizeTwoPerson runs the command
// with the reconstructed secret injected.
func TestExecutorTwoPersonRoundTrip(t *testing.T) {
	dir := t.TempDir()
	tps, err := NewTwoPersonStorage(dir + "/team.2p.json")
	if err != nil {
		t.Fatal(err)
	}
	ids, recs := makeTestRecipients(t, 3)
	ctx := context.Background()

	cred := Credential{
		ID: "db-prod", Kind: CredentialKindPassword, Secret: "SUPERSECRETDBPASSWORD",
		Scope: ScopeGlobal, Inject: InjectConfig{Method: InjectEnv, Target: "PGPASSWORD"},
	}
	if err := tps.SealAndStore(ctx, cred, 2, recs); err != nil {
		t.Fatal(err)
	}

	v := NewVault(tps, VaultConfig{Enabled: true, DefaultMode: ModeYOLO})
	e := NewExecutor(v, VaultConfig{DefaultMode: ModeYOLO}, nil)

	req := ExecutionRequest{
		CredentialID: "db-prod",
		Command:      "sh",
		Args:         []string{"-c", "echo $PGPASSWORD"},
		Tool:         "bash",
	}

	// Normal path must refuse.
	if _, err := e.Execute(ctx, req); err != ErrTwoPersonRequired {
		t.Fatalf("expected ErrTwoPersonRequired, got %v", err)
	}

	// Requester begins.
	tpReq, err := e.BeginTwoPerson(ctx, req, ids[0])
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	// Finalize before approval must fail (only 1 of 2 shares).
	if _, err := e.FinalizeTwoPerson(ctx, tpReq.ID); err == nil {
		t.Fatal("SECURITY: finalized with only the requester")
	}

	// Re-begin (previous request consumed by the failed finalize's reconstruct).
	tpReq, err = e.BeginTwoPerson(ctx, req, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	// Requester cannot self-approve.
	if _, err := e.ApproveTwoPerson(tpReq.ID, ids[0]); err == nil {
		t.Fatal("SECURITY: requester self-approved")
	}
	// Distinct approver approves.
	satisfied, err := e.ApproveTwoPerson(tpReq.ID, ids[1])
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !satisfied {
		t.Fatal("expected satisfied after distinct approver")
	}

	// Finalize runs the command with the reconstructed secret.
	res, err := e.FinalizeTwoPerson(ctx, tpReq.ID)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	// The secret is injected and echoed, but redaction must scrub it from output.
	if strings.Contains(res.Stdout, "SUPERSECRETDBPASSWORD") {
		t.Fatalf("SECURITY: secret leaked in finalize output: %q", res.Stdout)
	}
	if res.RedactedCount == 0 {
		t.Fatal("expected the injected secret to be redacted in output")
	}
}

func TestDelegatedModeDoesNotBypassTwoPersonIntegrity(t *testing.T) {
	tps, err := NewTwoPersonStorage(t.TempDir() + "/team.2p.json")
	if err != nil {
		t.Fatal(err)
	}
	_, recipients := makeTestRecipients(t, 2)
	ctx := context.Background()
	cred := Credential{
		ID:     "delegated-two-person",
		Kind:   CredentialKindPassword,
		Secret: "SUPERSECRETDBPASSWORD",
		Scope:  ScopeGlobal,
		Inject: InjectConfig{Method: InjectEnv, Target: "PGPASSWORD"},
	}
	if err := tps.SealAndStore(ctx, cred, 2, recipients); err != nil {
		t.Fatal(err)
	}

	v := NewVault(tps, VaultConfig{Enabled: true, DefaultMode: ModeDelegated})
	e := NewExecutor(v, VaultConfig{DefaultMode: ModeDelegated}, nil)
	_, err = e.Execute(ctx, ExecutionRequest{
		CredentialID: cred.ID,
		Command:      "go",
		Args:         []string{"version"},
		Tool:         "bash",
	})
	if err != ErrTwoPersonRequired {
		t.Fatalf("delegated mode bypassed two-person integrity: %v", err)
	}
}
