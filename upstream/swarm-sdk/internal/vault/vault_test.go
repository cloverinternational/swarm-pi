package vault

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestVaultEndToEnd(t *testing.T) {
	// Create memory storage for testing
	storage := NewMemoryStorage()

	// Create vault
	v := NewVault(storage, VaultConfig{
		Enabled:     true,
		DefaultMode: ModeYOLO,
	})

	// Create executor
	executor := NewExecutor(v, VaultConfig{
		DefaultMode: ModeYOLO,
	}, nil)

	ctx := context.Background()

	// Test 1: Add a credential
	cred := Credential{
		ID:              "test-aws",
		Name:            "Test AWS Key",
		Kind:            CredentialKindAWSAccessKey,
		Secret:          "AKIAIOSFODNN7EXAMPLE",
		Scope:           ScopeGlobal,
		AllowedTools:    []string{"bash"},
		AllowedCommands: []string{"aws *"},
		AllowedHosts:    []string{"*.amazonaws.com"},
		Inject: InjectConfig{
			Method: InjectEnv,
			Target: "AWS_ACCESS_KEY_ID",
		},
	}

	err := storage.Store(ctx, cred)
	if err != nil {
		t.Fatalf("Failed to store credential: %v", err)
	}
	t.Logf("✓ Stored credential: %s", cred.ID)

	// Test 2: List credentials
	creds, err := storage.List(ctx, CredentialFilter{})
	if err != nil {
		t.Fatalf("Failed to list credentials: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("Expected 1 credential, got %d", len(creds))
	}
	t.Logf("✓ Listed %d credential(s)", len(creds))

	// Test 3: Retrieve credential
	retrieved, err := storage.Retrieve(ctx, "test-aws")
	if err != nil {
		t.Fatalf("Failed to retrieve credential: %v", err)
	}
	if retrieved.Secret != "AKIAIOSFODNN7EXAMPLE" {
		t.Fatalf("Secret mismatch")
	}
	t.Logf("✓ Retrieved credential: %s (secret length: %d)", retrieved.ID, len(retrieved.Secret))

	// Test 4: Check constraints
	if !retrieved.CanUseTool("bash") {
		t.Fatal("Should be able to use bash tool")
	}
	if retrieved.CanUseTool("curl") {
		t.Fatal("Should NOT be able to use curl tool")
	}
	t.Logf("✓ Tool constraints work: bash=allowed, curl=denied")

	if !retrieved.CanUseCommand("aws s3 ls") {
		t.Fatal("Should be able to use 'aws s3 ls' command")
	}
	if retrieved.CanUseCommand("gcloud auth login") {
		t.Fatal("Should NOT be able to use 'gcloud auth login' command")
	}
	t.Logf("✓ Command constraints work: 'aws *'=allowed, 'gcloud *'=denied")

	if !retrieved.CanUseHost("s3.amazonaws.com") {
		t.Fatal("Should be able to use s3.amazonaws.com host")
	}
	if retrieved.CanUseHost("github.com") {
		t.Fatal("Should NOT be able to use github.com host")
	}
	t.Logf("✓ Host constraints work: '*.amazonaws.com'=allowed, github.com=denied")

	// Test 5: Verify executor exists
	if executor == nil {
		t.Fatal("Executor should not be nil")
	}
	t.Logf("✓ Executor created successfully")

	// Test 6: Verify vault exists
	if v == nil {
		t.Fatal("Vault should not be nil")
	}
	t.Logf("✓ Vault created successfully")
}

func TestOutputRedactor(t *testing.T) {
	redactor := NewOutputRedactor()

	secret := "AKIAIOSFODNN7EXAMPLE"

	tests := []struct {
		name         string
		input        string
		secret       string
		wantRedacted bool
	}{
		{
			name:         "exact match",
			input:        "Key: AKIAIOSFODNN7EXAMPLE",
			secret:       secret,
			wantRedacted: true,
		},
		{
			name:         "no match",
			input:        "No secrets here",
			secret:       secret,
			wantRedacted: false,
		},
		{
			name:         "empty secret",
			input:        "Some text",
			secret:       "",
			wantRedacted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, count := redactor.Redact(tt.input, tt.secret)

			if tt.wantRedacted && count == 0 {
				t.Errorf("Expected redactions, got 0")
			}
			if !tt.wantRedacted && count > 0 {
				t.Errorf("Expected no redactions, got %d", count)
			}

			// Verify secret is not in output (if there was a secret)
			if tt.secret != "" && containsStr(output, tt.secret) {
				t.Errorf("Secret still present in output: %s", output)
			}

			t.Logf("Input: %s", tt.input)
			t.Logf("Output: %s", output)
			t.Logf("Redactions: %d", count)
		})
	}
}

func TestHostMatching(t *testing.T) {
	tests := []struct {
		allowedHosts []string
		host         string
		want         bool
	}{
		{[]string{}, "any.host.com", true}, // No restriction
		{[]string{"github.com"}, "github.com", true},
		{[]string{"github.com"}, "gitlab.com", false},
		{[]string{"*.internal.com"}, "server.internal.com", true},
		{[]string{"*.internal.com"}, "server.external.com", false},
		{[]string{"github.com", "gitlab.com"}, "github.com", true},
		{[]string{"github.com", "gitlab.com"}, "bitbucket.com", false},
	}

	for i, tt := range tests {
		cred := &Credential{AllowedHosts: tt.allowedHosts}
		got := cred.CanUseHost(tt.host)
		if got != tt.want {
			t.Errorf("Test %d: CanUseHost(%q) with allowedHosts %v = %v, want %v",
				i, tt.host, tt.allowedHosts, got, tt.want)
		} else {
			t.Logf("✓ Test %d: %q with %v = %v", i, tt.host, tt.allowedHosts, got)
		}
	}
}

func TestCommandMatching(t *testing.T) {
	tests := []struct {
		allowedCommands []string
		command         string
		want            bool
	}{
		{[]string{}, "any command", true}, // No restriction
		{[]string{"aws *"}, "aws s3 ls", true},
		{[]string{"aws *"}, "aws ec2 describe-instances", true},
		{[]string{"aws *"}, "gcloud auth login", false},
		{[]string{"git clone *"}, "git clone https://github.com/user/repo", true},
		{[]string{"git clone *"}, "git push", false},
		{[]string{"ssh *", "scp *"}, "ssh user@host", true},
		{[]string{"ssh *", "scp *"}, "rsync -av src/ dest/", false},
	}

	for i, tt := range tests {
		cred := &Credential{AllowedCommands: tt.allowedCommands}
		got := cred.CanUseCommand(tt.command)
		if got != tt.want {
			t.Errorf("Test %d: CanUseCommand(%q) with allowedCommands %v = %v, want %v",
				i, tt.command, tt.allowedCommands, got, tt.want)
		} else {
			t.Logf("✓ Test %d: %q with %v = %v", i, tt.command, tt.allowedCommands, got)
		}
	}
}

func TestCredentialExpiration(t *testing.T) {
	// Not expired
	cred := &Credential{ExpiresAt: nil}
	if cred.IsExpired() {
		t.Error("Credential with no expiry should not be expired")
	}

	// Future expiry
	future := time.Now().Add(24 * time.Hour)
	cred = &Credential{ExpiresAt: &future}
	if cred.IsExpired() {
		t.Error("Credential with future expiry should not be expired")
	}

	// Past expiry
	past := time.Now().Add(-1 * time.Hour)
	cred = &Credential{ExpiresAt: &past}
	if !cred.IsExpired() {
		t.Error("Credential with past expiry should be expired")
	}

	t.Logf("✓ Expiration logic works correctly")
}

func TestAgeEncryption(t *testing.T) {
	// Create a temporary file for testing
	tmpDir := t.TempDir()
	vaultPath := tmpDir + "/test.vault"

	// Create encryptor
	encryptor, err := NewAgeEncryptorFromPassphrase("test-passphrase-123")
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}

	// Create storage
	storage, err := NewAgeStorage(vaultPath, encryptor)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	// Store a credential
	ctx := context.Background()
	cred := Credential{
		ID:     "test-key",
		Name:   "Test Key",
		Kind:   CredentialKindAPIKey,
		Secret: "super-secret-value-12345",
		Scope:  ScopeGlobal,
	}

	err = storage.Store(ctx, cred)
	if err != nil {
		t.Fatalf("Failed to store credential: %v", err)
	}
	t.Logf("✓ Stored credential with encryption")

	// Verify file was created
	if _, err := os.Stat(vaultPath); os.IsNotExist(err) {
		t.Fatalf("Vault file not created at %s", vaultPath)
	}
	t.Logf("✓ Vault file created at %s", vaultPath)

	// Create a new storage instance (simulates restart)
	storage2, err := NewAgeStorage(vaultPath, encryptor)
	if err != nil {
		t.Fatalf("Failed to reload storage: %v", err)
	}

	// Retrieve credential
	retrieved, err := storage2.Retrieve(ctx, "test-key")
	if err != nil {
		t.Fatalf("Failed to retrieve credential: %v", err)
	}

	if retrieved.Secret != "super-secret-value-12345" {
		t.Errorf("Secret mismatch: got %q, want %q", retrieved.Secret, "super-secret-value-12345")
	}
	t.Logf("✓ Retrieved credential with correct secret after reload")
}

func TestAgeEncryptionWrongPassphrase(t *testing.T) {
	// Create a temporary file for testing
	tmpDir := t.TempDir()
	vaultPath := tmpDir + "/test.vault"

	// Create encryptor with correct passphrase
	encryptor, err := NewAgeEncryptorFromPassphrase("correct-passphrase")
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}

	// Create storage and store a credential
	storage, err := NewAgeStorage(vaultPath, encryptor)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	ctx := context.Background()
	cred := Credential{
		ID:     "test-key",
		Secret: "secret-value",
		Scope:  ScopeGlobal,
	}
	_ = storage.Store(ctx, cred)

	// Try to load with wrong passphrase
	wrongEncryptor, err := NewAgeEncryptorFromPassphrase("wrong-passphrase")
	if err != nil {
		t.Fatalf("Failed to create wrong encryptor: %v", err)
	}

	_, err = NewAgeStorage(vaultPath, wrongEncryptor)
	if err == nil {
		t.Fatal("Expected error when loading with wrong passphrase")
	}
	t.Logf("✓ Wrong passphrase correctly rejected: %v", err)
}

// Helper function to check if string contains substring
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && len(substr) > 0 &&
		(s == substr || (len(s) > len(substr) && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
