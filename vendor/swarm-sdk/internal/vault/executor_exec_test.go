package vault

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestExecutor builds a YOLO-mode executor backed by memory storage.
func newTestExecutor(t *testing.T, creds ...Credential) *Executor {
	t.Helper()
	storage := NewMemoryStorage()
	ctx := context.Background()
	for _, c := range creds {
		if err := storage.Store(ctx, c); err != nil {
			t.Fatalf("store %s: %v", c.ID, err)
		}
	}
	v := NewVault(storage, VaultConfig{Enabled: true, DefaultMode: ModeYOLO})
	return NewExecutor(v, VaultConfig{DefaultMode: ModeYOLO}, nil)
}

// TestExecuteReturnsRealExitCode verifies the success path no longer hardcodes
// ExitCode 0 - a command that exits non-zero must surface its real code.
func TestExecuteReturnsRealExitCode(t *testing.T) {
	e := newTestExecutor(t, Credential{
		ID: "k", Kind: CredentialKindAPIKey, Secret: "s3cr3t",
		Scope:  ScopeGlobal,
		Inject: InjectConfig{Method: InjectEnv, Target: "TOKEN"},
	})

	res, err := e.Execute(context.Background(), ExecutionRequest{
		CredentialID: "k",
		Command:      "sh",
		Args:         []string{"-c", "exit 7"},
		Tool:         "bash",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", res.ExitCode)
	}
}

// TestExecuteCapturesStderr verifies stderr is surfaced (previously dropped).
func TestExecuteCapturesStderr(t *testing.T) {
	e := newTestExecutor(t, Credential{
		ID: "k", Kind: CredentialKindAPIKey, Secret: "s3cr3t",
		Scope:  ScopeGlobal,
		Inject: InjectConfig{Method: InjectEnv, Target: "TOKEN"},
	})

	res, err := e.Execute(context.Background(), ExecutionRequest{
		CredentialID: "k",
		Command:      "sh",
		Args:         []string{"-c", "echo boom 1>&2"},
		Tool:         "bash",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Stderr, "boom") {
		t.Fatalf("Stderr = %q, want it to contain 'boom'", res.Stderr)
	}
}

// TestExecuteRedactsInjectedSecret verifies the used credential is scrubbed
// from output on the success path.
func TestExecuteRedactsInjectedSecret(t *testing.T) {
	e := newTestExecutor(t, Credential{
		ID: "k", Kind: CredentialKindAPIKey, Secret: "AKIAINJECTEDSECRET",
		Scope:  ScopeGlobal,
		Inject: InjectConfig{Method: InjectEnv, Target: "TOKEN"},
	})

	res, err := e.Execute(context.Background(), ExecutionRequest{
		CredentialID: "k",
		Command:      "sh",
		Args:         []string{"-c", "echo $TOKEN"},
		Tool:         "bash",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(res.Stdout, "AKIAINJECTEDSECRET") {
		t.Fatalf("injected secret leaked in stdout: %q", res.Stdout)
	}
	if res.RedactedCount == 0 {
		t.Fatalf("expected redactions, got 0")
	}
}

// TestExecuteRedactsSiblingSecret verifies that a DIFFERENT stored credential
// echoed by the command is also scrubbed - not just the injected one.
func TestExecuteRedactsSiblingSecret(t *testing.T) {
	e := newTestExecutor(t,
		Credential{
			ID: "used", Kind: CredentialKindAPIKey, Secret: "USEDSECRETVALUE",
			Scope: ScopeGlobal, Inject: InjectConfig{Method: InjectEnv, Target: "TOKEN"},
		},
		Credential{
			ID: "other", Kind: CredentialKindAPIKey, Secret: "SIBLINGSECRETVALUE",
			Scope: ScopeGlobal, Inject: InjectConfig{Method: InjectEnv, Target: "OTHER"},
		},
	)

	// The command only injects "used" but prints the sibling's raw value.
	res, err := e.Execute(context.Background(), ExecutionRequest{
		CredentialID: "used",
		Command:      "sh",
		Args:         []string{"-c", "echo SIBLINGSECRETVALUE"},
		Tool:         "bash",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(res.Stdout, "SIBLINGSECRETVALUE") {
		t.Fatalf("sibling secret leaked in stdout: %q", res.Stdout)
	}
}

// TestBalancedModeGatesHighRiskKinds verifies untagged AWS/SSH/password/TLS
// keys require approval in balanced mode.
func TestBalancedModeGatesHighRiskKinds(t *testing.T) {
	cases := []struct {
		kind         CredentialKind
		wantApproval bool
	}{
		{CredentialKindAWSSecretKey, true},
		{CredentialKindSSHKey, true},
		{CredentialKindPassword, true},
		{CredentialKindTLSKey, true},
		{CredentialKindAPIKey, false},
		{CredentialKindBearerToken, false},
	}
	for _, tc := range cases {
		c := &Credential{Kind: tc.kind}
		if got := c.RequiresApprovalByDefault(); got != tc.wantApproval {
			t.Errorf("kind %s: RequiresApprovalByDefault() = %v, want %v", tc.kind, got, tc.wantApproval)
		}
	}
}

// newBalancedExecutor builds a balanced-mode executor for approval tests.
func newBalancedExecutor(t *testing.T, creds ...Credential) *Executor {
	t.Helper()
	storage := NewMemoryStorage()
	ctx := context.Background()
	for _, c := range creds {
		if err := storage.Store(ctx, c); err != nil {
			t.Fatalf("store %s: %v", c.ID, err)
		}
	}
	v := NewVault(storage, VaultConfig{Enabled: true, DefaultMode: ModeBalanced})
	return NewExecutor(v, VaultConfig{DefaultMode: ModeBalanced}, nil)
}

// TestDelegatedModeSkipsOnlyInnerApproval verifies that a trusted interactive
// host can own the human approval prompt without weakening credential
// constraints enforced before the vault's approval-mode gate.
func TestDelegatedModeSkipsOnlyInnerApproval(t *testing.T) {
	storage := NewMemoryStorage()
	ctx := context.Background()
	cred := Credential{
		ID:              "aws",
		Kind:            CredentialKindAWSSecretKey,
		Secret:          "AKIAEXAMPLESECRET",
		Scope:           ScopeGlobal,
		AllowedTools:    []string{"bash"},
		AllowedCommands: []string{"go"},
		AllowedHosts:    []string{"example.com"},
		Inject:          InjectConfig{Method: InjectEnv, Target: "AWS_SECRET_ACCESS_KEY"},
	}
	if err := storage.Store(ctx, cred); err != nil {
		t.Fatalf("store credential: %v", err)
	}
	v := NewVault(storage, VaultConfig{Enabled: true, DefaultMode: ModeDelegated})
	e := NewExecutor(v, VaultConfig{Enabled: true, DefaultMode: ModeDelegated}, nil)

	if err := e.checkPermission(ctx, &cred, ExecutionRequest{
		CredentialID: cred.ID,
		Command:      "go",
		Args:         []string{"env", "GOOS"},
		Tool:         "bash",
		Host:         "example.com",
	}); err != nil {
		t.Fatalf("delegated permission returned an inner approval: %v", err)
	}

	_, err := e.Execute(ctx, ExecutionRequest{
		CredentialID: cred.ID,
		Command:      "go",
		Args:         []string{"env", "GOOS"},
		Tool:         "python",
		Host:         "example.com",
	})
	if !errors.Is(err, ErrToolNotAllowed) {
		t.Fatalf("delegated mode bypassed tool restriction: %v", err)
	}

	_, err = e.Execute(ctx, ExecutionRequest{
		CredentialID: cred.ID,
		Command:      "go",
		Args:         []string{"env", "GOOS"},
		Tool:         "bash",
	})
	if !errors.Is(err, ErrHostUnverifiable) {
		t.Fatalf("delegated mode bypassed explicit-host requirement: %v", err)
	}

	_, err = e.Execute(ctx, ExecutionRequest{
		CredentialID: cred.ID,
		Command:      "sh",
		Tool:         "bash",
		Host:         "example.com",
	})
	if !errors.Is(err, ErrCommandNotAllowed) {
		t.Fatalf("delegated mode bypassed command restriction: %v", err)
	}

	expiredAt := time.Now().Add(-time.Minute)
	expired := cred
	expired.ID = "expired"
	expired.ExpiresAt = &expiredAt
	if err := storage.Store(ctx, expired); err != nil {
		t.Fatalf("store expired credential: %v", err)
	}
	_, err = e.Execute(ctx, ExecutionRequest{
		CredentialID: expired.ID,
		Command:      "go",
		Tool:         "bash",
		Host:         "example.com",
	})
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("delegated mode bypassed expiration: %v", err)
	}
}

// TestHostRestrictedRequiresExplicitHost verifies a host-scoped credential is
// denied when no explicit host is given (deny on ambiguity), and allowed when
// the correct host is passed explicitly.
func TestHostRestrictedRequiresExplicitHost(t *testing.T) {
	e := newTestExecutor(t, Credential{
		ID: "gh", Kind: CredentialKindAPIKey, Secret: "s3cr3t", Scope: ScopeGlobal,
		AllowedHosts: []string{"github.com"},
		Inject:       InjectConfig{Method: InjectEnv, Target: "TOKEN"},
	})

	// No explicit host - must be denied even though the command mentions github.com.
	_, err := e.Execute(context.Background(), ExecutionRequest{
		CredentialID: "gh", Command: "echo", Args: []string{"https://github.com"}, Tool: "bash",
	})
	if !errors.Is(err, ErrHostUnverifiable) {
		t.Fatalf("expected ErrHostUnverifiable, got %v", err)
	}

	// Wrong explicit host - denied.
	_, err = e.Execute(context.Background(), ExecutionRequest{
		CredentialID: "gh", Command: "echo", Args: []string{"hi"}, Host: "evil.com", Tool: "bash",
	})
	if !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("expected ErrHostNotAllowed for evil.com, got %v", err)
	}

	// Correct explicit host - allowed.
	res, err := e.Execute(context.Background(), ExecutionRequest{
		CredentialID: "gh", Command: "echo", Args: []string{"hi"}, Host: "github.com", Tool: "bash",
	})
	if err != nil {
		t.Fatalf("expected success with correct host, got %v", err)
	}
	if !strings.Contains(res.Stdout, "hi") {
		t.Fatalf("unexpected stdout %q", res.Stdout)
	}
}

// TestApprovalRoundTripIsArgsScoped verifies the structured approval flow: a
// high-risk cred needs approval, the ApprovalID unlocks the SAME command+args,
// and a grant for one arg set does NOT authorize a different arg set.
func TestApprovalRoundTripIsArgsScoped(t *testing.T) {
	e := newBalancedExecutor(t, Credential{
		ID: "aws", Kind: CredentialKindAWSSecretKey, Secret: "AKIAEXAMPLESECRET",
		Scope: ScopeGlobal, Inject: InjectConfig{Method: InjectEnv, Target: "AWS"},
	})

	reqLs := ExecutionRequest{CredentialID: "aws", Command: "sh", Args: []string{"-c", "echo ls"}, Tool: "bash"}

	// First call - needs approval.
	_, err := e.Execute(context.Background(), reqLs)
	var apErr *ApprovalError
	if !errors.As(err, &apErr) {
		t.Fatalf("expected *ApprovalError, got %v", err)
	}
	if apErr.ApprovalID == "" {
		t.Fatal("expected non-empty ApprovalID")
	}

	// Approve, then the SAME command+args succeeds.
	if err := e.Approve(apErr.ApprovalID); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, err := e.Execute(context.Background(), reqLs); err != nil {
		t.Fatalf("expected success after approval, got %v", err)
	}

	// A DIFFERENT arg set must NOT be covered by the prior grant.
	reqRm := ExecutionRequest{CredentialID: "aws", Command: "sh", Args: []string{"-c", "echo rm"}, Tool: "bash"}
	_, err = e.Execute(context.Background(), reqRm)
	if !errors.As(err, &apErr) {
		t.Fatalf("expected new approval for different args, got %v", err)
	}

	// Re-using a consumed approvalId fails.
	if err := e.Approve("apr-doesnotexist"); err == nil {
		t.Fatal("expected error for unknown approvalId")
	}
}

// TestExecuteRedactsPEMKey verifies a multi-line private key is redacted whole,
// including BEGIN/END markers, via the entropy/PEM pass.
func TestExecuteRedactsPEMKey(t *testing.T) {
	e := newTestExecutor(t, Credential{
		ID: "k", Kind: CredentialKindAPIKey, Secret: "unrelated", Scope: ScopeGlobal,
		Inject: InjectConfig{Method: InjectEnv, Target: "TOKEN"},
	})

	pem := "-----BEGIN OPENSSH PRIVATE KEY-----\\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAA\\nAAABBBCCCDDD\\n-----END OPENSSH PRIVATE KEY-----"
	res, err := e.Execute(context.Background(), ExecutionRequest{
		CredentialID: "k", Command: "printf", Args: []string{pem}, Tool: "bash",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(res.Stdout, "BEGIN OPENSSH") || strings.Contains(res.Stdout, "b3BlbnNz") {
		t.Fatalf("PEM key leaked in stdout: %q", res.Stdout)
	}
	if res.SafeToParse {
		t.Fatal("SafeToParse should be false when redaction occurred")
	}
}

// TestConcurrentExecuteNoRace runs many Execute calls concurrently to surface
// any shared-state race in redaction (run with -race).
func TestConcurrentExecuteNoRace(t *testing.T) {
	e := newTestExecutor(t, Credential{
		ID: "k", Kind: CredentialKindAPIKey, Secret: "SHAREDSECRETVALUE", Scope: ScopeGlobal,
		Inject: InjectConfig{Method: InjectEnv, Target: "TOKEN"},
	})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := e.Execute(context.Background(), ExecutionRequest{
				CredentialID: "k", Command: "sh", Args: []string{"-c", "echo $TOKEN"}, Tool: "bash",
			})
			if err != nil {
				t.Errorf("execute: %v", err)
				return
			}
			if strings.Contains(res.Stdout, "SHAREDSECRETVALUE") {
				t.Errorf("secret leaked under concurrency: %q", res.Stdout)
			}
		}()
	}
	wg.Wait()
}
