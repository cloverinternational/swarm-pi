package vault

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// TestBrokerHappyPath: requester begins, distinct approver approves, reconstruct
// yields the secret.
func TestBrokerHappyPath(t *testing.T) {
	ids, recs := makeTestRecipients(t, 3)
	secret := []byte("prod-secret-value-aaaaaaaaaaaaaa")
	env, err := twoPersonSeal("db-prod", secret, 2, recs)
	if err != nil {
		t.Fatal(err)
	}
	b := newTwoPersonBroker(time.Minute)

	req, err := b.begin("db-prod", "psql ...", "run migration", env, ids[0])
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// Not satisfiable with just the requester.
	if _, err := b.reconstruct(req.ID); err == nil {
		t.Fatal("SECURITY: reconstruct succeeded with only the requester")
	}

	// Re-begin because the failed reconstruct consumed the request.
	req, err = b.begin("db-prod", "psql ...", "run migration", env, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	satisfied, err := b.approve(req.ID, ids[1])
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !satisfied {
		t.Fatal("expected satisfied after second approver")
	}
	got, err := b.reconstruct(req.ID)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Fatal("reconstructed secret mismatch")
	}
	// Single-use: request is gone.
	if _, ok := b.snapshot(req.ID); ok {
		t.Fatal("request should be consumed after reconstruct")
	}
}

// TestBrokerRejectsSameApprover: the requester cannot self-approve (separation
// of duties).
func TestBrokerRejectsSameApprover(t *testing.T) {
	ids, recs := makeTestRecipients(t, 3)
	env, err := twoPersonSeal("c", []byte("secret-value-bbbbbbbbbbbbbbbbbb"), 2, recs)
	if err != nil {
		t.Fatal(err)
	}
	b := newTwoPersonBroker(time.Minute)
	req, err := b.begin("c", "cmd", "reason", env, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	// Same identity (the requester) tries to approve -> must be rejected.
	if _, err := b.approve(req.ID, ids[0]); err == nil {
		t.Fatal("SECURITY: requester was allowed to self-approve")
	}
	// Still not satisfiable.
	if _, err := b.reconstruct(req.ID); err == nil {
		t.Fatal("SECURITY: reconstruct after self-approval attempt")
	}
}

// TestBrokerRejectsNonRecipient: an identity not in the roster cannot approve.
func TestBrokerRejectsNonRecipient(t *testing.T) {
	ids, recs := makeTestRecipients(t, 2)
	env, err := twoPersonSeal("c", []byte("secret-value-cccccccccccccccc"), 2, recs)
	if err != nil {
		t.Fatal(err)
	}
	// A stranger not among recipients.
	strangers, _ := makeTestRecipients(t, 1)
	b := newTwoPersonBroker(time.Minute)
	req, err := b.begin("c", "cmd", "reason", env, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.approve(req.ID, strangers[0]); err == nil {
		t.Fatal("SECURITY: non-recipient stranger was allowed to approve")
	}
}

// TestBrokerThreeOfN: threshold 3 requires three distinct principals.
func TestBrokerThreeOfN(t *testing.T) {
	ids, recs := makeTestRecipients(t, 4)
	secret := []byte("very-sensitive-secret-3of4-00000")
	env, err := twoPersonSeal("c", secret, 3, recs)
	if err != nil {
		t.Fatal(err)
	}
	b := newTwoPersonBroker(time.Minute)
	req, err := b.begin("c", "cmd", "reason", env, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	// One approver -> still short.
	satisfied, err := b.approve(req.ID, ids[1])
	if err != nil {
		t.Fatal(err)
	}
	if satisfied {
		t.Fatal("should not be satisfied with 2 of 3")
	}
	// Second distinct approver -> satisfied.
	satisfied, err = b.approve(req.ID, ids[2])
	if err != nil {
		t.Fatal(err)
	}
	if !satisfied {
		t.Fatal("expected satisfied with 3 of 3")
	}
	got, err := b.reconstruct(req.ID)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("reconstruct 3-of-4: %v", err)
	}
}

// TestBrokerExpiry: an expired request cannot be approved.
func TestBrokerExpiry(t *testing.T) {
	ids, recs := makeTestRecipients(t, 2)
	env, err := twoPersonSeal("c", []byte("secret-value-dddddddddddddddd"), 2, recs)
	if err != nil {
		t.Fatal(err)
	}
	b := newTwoPersonBroker(time.Nanosecond)
	req, err := b.begin("c", "cmd", "reason", env, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := b.approve(req.ID, ids[1]); err == nil {
		t.Fatal("expected expired request to reject approval")
	}
}

// TestBrokerReconstructRejectsExpired verifies reconstruct re-checks TTL (F3):
// a request satisfied before expiry must not reconstruct after it expires.
func TestBrokerReconstructRejectsExpired(t *testing.T) {
	ids, recs := makeTestRecipients(t, 2)
	env, err := twoPersonSeal("c", []byte("secret-value-eeeeeeeeeeeeeeee"), 2, recs)
	if err != nil {
		t.Fatal(err)
	}
	b := newTwoPersonBroker(time.Minute)
	req, err := b.begin("c", "cmd", "reason", env, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.approve(req.ID, ids[1]); err != nil {
		t.Fatal(err)
	}
	// Force expiry after it was satisfied.
	b.mu.Lock()
	b.pending[req.ID].ExpiresAt = time.Now().Add(-time.Second)
	b.mu.Unlock()
	if _, err := b.reconstruct(req.ID); err == nil {
		t.Fatal("SECURITY: reconstructed an expired request")
	}
}

// TestBrokerPendingCap verifies the pending map is bounded (F2).
func TestBrokerPendingCap(t *testing.T) {
	ids, recs := makeTestRecipients(t, 2)
	env, err := twoPersonSeal("c", []byte("secret-value-ffffffffffffffff"), 2, recs)
	if err != nil {
		t.Fatal(err)
	}
	b := newTwoPersonBroker(time.Minute)
	b.maxPending = 3
	for i := 0; i < 3; i++ {
		if _, err := b.begin("c", "cmd", "reason", env, ids[0]); err != nil {
			t.Fatalf("begin %d: %v", i, err)
		}
	}
	if _, err := b.begin("c", "cmd", "reason", env, ids[0]); err == nil {
		t.Fatal("expected begin to reject once pending cap is reached")
	}
}

// TestExecutorRefusesTwoPersonCredential is the critical integration interlock:
// a two-person credential resolved through the normal single-principal Execute
// path MUST be refused (ErrTwoPersonRequired), never run with an empty secret.
func TestExecutorRefusesTwoPersonCredential(t *testing.T) {
	dir := t.TempDir()
	tps, err := NewTwoPersonStorage(dir + "/team.2p.json")
	if err != nil {
		t.Fatal(err)
	}
	_, recs := makeTestRecipients(t, 2)
	ctx := context.Background()
	cred := Credential{
		ID: "aws-prod", Kind: CredentialKindAWSSecretKey, Secret: "AKIAPRODSECRETVALUE",
		Scope: ScopeGlobal, Inject: InjectConfig{Method: InjectEnv, Target: "AWS"},
	}
	if err := tps.SealAndStore(ctx, cred, 2, recs); err != nil {
		t.Fatal(err)
	}

	// Wire the two-person storage as the global storage.
	v := NewVault(tps, VaultConfig{Enabled: true, DefaultMode: ModeYOLO})
	e := NewExecutor(v, VaultConfig{DefaultMode: ModeYOLO}, nil)

	_, err = e.Execute(ctx, ExecutionRequest{
		CredentialID: "aws-prod", Command: "echo", Args: []string{"hi"}, Tool: "bash",
	})
	if err == nil {
		t.Fatal("SECURITY: two-person credential executed via normal path")
	}
	if err != ErrTwoPersonRequired {
		t.Fatalf("expected ErrTwoPersonRequired, got %v", err)
	}
}

func TestBrokerSweepExpiredOnBegin(t *testing.T) {
	ids, recs := makeTestRecipients(t, 2)
	env, err := twoPersonSeal("c", []byte("secret-value-gggggggggggggggg"), 2, recs)
	if err != nil {
		t.Fatal(err)
	}
	b := newTwoPersonBroker(time.Minute)
	b.maxPending = 2
	r1, _ := b.begin("c", "cmd", "reason", env, ids[0])
	b.begin("c", "cmd", "reason", env, ids[0])
	// Expire r1 manually; the next begin should sweep it and succeed.
	b.mu.Lock()
	b.pending[r1.ID].ExpiresAt = time.Now().Add(-time.Second)
	b.mu.Unlock()
	if _, err := b.begin("c", "cmd", "reason", env, ids[0]); err != nil {
		t.Fatalf("expected begin to succeed after sweeping expired: %v", err)
	}
}
