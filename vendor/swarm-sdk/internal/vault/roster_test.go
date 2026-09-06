package vault

import (
	"path/filepath"
	"testing"

	"filippo.io/age"
)

// TestRosterAllowMembersRevoke exercises the roster lifecycle over recipients.txt.
func TestRosterAllowMembersRevoke(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "recipients.txt")

	id1, _ := age.GenerateX25519Identity()
	id2, _ := age.GenerateX25519Identity()
	k1 := id1.Recipient().String()
	k2 := id2.Recipient().String()

	if _, err := AllowMember(rp, k1, "Alice"); err != nil {
		t.Fatalf("allow k1: %v", err)
	}
	if _, err := AllowMember(rp, k2, "Bob"); err != nil {
		t.Fatalf("allow k2: %v", err)
	}
	// Duplicate rejected.
	if _, err := AllowMember(rp, k1, "Alice again"); err == nil {
		t.Fatal("expected duplicate allow to fail")
	}
	// Invalid key rejected.
	if _, err := AllowMember(rp, "not-a-key", ""); err == nil {
		t.Fatal("expected invalid key to be rejected")
	}

	members, err := Members(rp)
	if err != nil || len(members) != 2 {
		t.Fatalf("expected 2 members, got %d (%v)", len(members), err)
	}

	if _, err := RevokeMember(rp, k1); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	members, _ = Members(rp)
	if len(members) != 1 || members[0].PublicKey != k2 {
		t.Fatalf("expected only k2 to remain, got %+v", members)
	}
}

// TestSealNewTwoPersonViaRoster seals a sensitive credential to the roster and
// verifies it resolves as two-person.
func TestSealNewTwoPersonViaRoster(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "recipients.txt")
	id1, _ := age.GenerateX25519Identity()
	id2, _ := age.GenerateX25519Identity()
	if _, err := AllowMember(rp, id1.Recipient().String(), "Alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := AllowMember(rp, id2.Recipient().String(), "Bob"); err != nil {
		t.Fatal(err)
	}

	store, err := NewTwoPersonStorage(filepath.Join(dir, "twoperson.json"))
	if err != nil {
		t.Fatal(err)
	}
	cred := Credential{
		ID: "db", Kind: CredentialKindPassword, Secret: "s3cr3t-db-pass",
		Scope: ScopeProject, Inject: InjectConfig{Method: InjectEnv, Target: "PGPASSWORD"},
	}
	if err := SealNewTwoPerson(store, rp, cred, 2); err != nil {
		t.Fatalf("seal: %v", err)
	}

	// Round-trips: id1 begins (one share), id2 approves (second), reconstruct.
	env, err := store.GetEnvelope("db")
	if err != nil {
		t.Fatal(err)
	}
	b := newTwoPersonBroker(0)
	req, err := b.begin("db", "psql", "reason", env, id1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.approve(req.ID, id2); err != nil {
		t.Fatal(err)
	}
	secret, err := b.reconstruct(req.ID)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if string(secret) != "s3cr3t-db-pass" {
		t.Fatalf("reconstructed secret mismatch: %q", secret)
	}
}

// TestSealNewTwoPersonRejectsSmallRoster requires roster >= threshold.
func TestSealNewTwoPersonRejectsSmallRoster(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "recipients.txt")
	id1, _ := age.GenerateX25519Identity()
	if _, err := AllowMember(rp, id1.Recipient().String(), "solo"); err != nil {
		t.Fatal(err)
	}
	store, _ := NewTwoPersonStorage(filepath.Join(dir, "tp.json"))
	cred := Credential{ID: "x", Kind: CredentialKindAPIKey, Secret: "v", Scope: ScopeProject}
	if err := SealNewTwoPerson(store, rp, cred, 2); err == nil {
		t.Fatal("expected seal to fail with roster smaller than threshold")
	}
}

func TestMembersNoRoster(t *testing.T) {
	dir := t.TempDir()
	members, err := Members(filepath.Join(dir, "nope.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 0 {
		t.Fatal("expected empty roster")
	}
}
