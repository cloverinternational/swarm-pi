package cloudsync

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// ============================================================
// Tests for 021-cloud-settings-sync: Passphrase Encryption
// ============================================================

// --- Key Derivation Tests ---

func TestDeriveKey_ProducesCorrectLength(t *testing.T) {
	key := DeriveKeyFromPassphrase("my-secret-passphrase", []byte("random-salt"))

	if len(key) != 32 {
		t.Errorf("expected 32-byte key, got %d bytes", len(key))
	}
}

func TestDeriveKey_DeterministicForSameInputs(t *testing.T) {
	passphrase := "my-secret-passphrase"
	salt := []byte("random-salt")

	key1 := DeriveKeyFromPassphrase(passphrase, salt)
	key2 := DeriveKeyFromPassphrase(passphrase, salt)

	if !bytes.Equal(key1, key2) {
		t.Error("expected same key for same inputs")
	}
}

func TestDeriveKey_DifferentForDifferentPassphrases(t *testing.T) {
	salt := []byte("random-salt")

	key1 := DeriveKeyFromPassphrase("passphrase1", salt)
	key2 := DeriveKeyFromPassphrase("passphrase2", salt)

	if bytes.Equal(key1, key2) {
		t.Error("expected different keys for different passphrases")
	}
}

func TestDeriveKey_DifferentForDifferentSalts(t *testing.T) {
	passphrase := "my-secret-passphrase"

	key1 := DeriveKeyFromPassphrase(passphrase, []byte("salt1"))
	key2 := DeriveKeyFromPassphrase(passphrase, []byte("salt2"))

	if bytes.Equal(key1, key2) {
		t.Error("expected different keys for different salts")
	}
}

// --- Encryption Mode Config Tests ---

func TestEncryptionMode_DefaultIsDevice(t *testing.T) {
	dir := t.TempDir()
	ec := NewEncryptionConfig(dir)

	if ec.Mode() != EncryptionModeDevice {
		t.Errorf("expected default mode='device', got '%s'", ec.Mode())
	}
}

func TestEncryptionMode_CanSetToPassphrase(t *testing.T) {
	dir := t.TempDir()
	ec := NewEncryptionConfig(dir)

	err := ec.SetMode(EncryptionModePassphrase)
	if err != nil {
		t.Fatalf("SetMode returned error: %v", err)
	}

	if ec.Mode() != EncryptionModePassphrase {
		t.Errorf("expected mode='passphrase', got '%s'", ec.Mode())
	}
}

func TestEncryptionMode_CanSetToDevice(t *testing.T) {
	dir := t.TempDir()
	ec := NewEncryptionConfig(dir)
	ec.SetMode(EncryptionModePassphrase) // Start with passphrase

	err := ec.SetMode(EncryptionModeDevice)
	if err != nil {
		t.Fatalf("SetMode returned error: %v", err)
	}

	if ec.Mode() != EncryptionModeDevice {
		t.Errorf("expected mode='device', got '%s'", ec.Mode())
	}
}

func TestEncryptionMode_RejectsInvalidMode(t *testing.T) {
	dir := t.TempDir()
	ec := NewEncryptionConfig(dir)

	err := ec.SetMode("invalid")
	if err == nil {
		t.Error("expected error for invalid mode")
	}
}

func TestEncryptionMode_PersistsToFile(t *testing.T) {
	dir := t.TempDir()

	// Set mode
	ec1 := NewEncryptionConfig(dir)
	ec1.SetMode(EncryptionModePassphrase)

	// Load from file
	ec2 := NewEncryptionConfig(dir)
	if ec2.Mode() != EncryptionModePassphrase {
		t.Errorf("expected persisted mode='passphrase', got '%s'", ec2.Mode())
	}
}

// --- Passphrase Storage Tests ---

func TestPassphrase_IsNotSetInitially(t *testing.T) {
	dir := t.TempDir()
	ec := NewEncryptionConfig(dir)

	if ec.HasPassphrase() {
		t.Error("expected no passphrase initially")
	}
}

func TestPassphrase_CanBeSet(t *testing.T) {
	dir := t.TempDir()
	ec := NewEncryptionConfig(dir)

	err := ec.SetPassphrase("my-secret-passphrase")
	if err != nil {
		t.Fatalf("SetPassphrase returned error: %v", err)
	}

	if !ec.HasPassphrase() {
		t.Error("expected passphrase to be set")
	}
}

func TestPassphrase_RejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	ec := NewEncryptionConfig(dir)

	err := ec.SetPassphrase("")
	if err == nil {
		t.Error("expected error for empty passphrase")
	}
}

func TestPassphrase_DerivedKeyIsConsistent(t *testing.T) {
	dir := t.TempDir()
	ec := NewEncryptionConfig(dir)

	ec.SetPassphrase("my-secret-passphrase")

	key1 := ec.GetDerivedKey()
	key2 := ec.GetDerivedKey()

	if !bytes.Equal(key1, key2) {
		t.Error("expected consistent derived key")
	}
}

func TestPassphrase_SaltIsPersisted(t *testing.T) {
	dir := t.TempDir()

	// Set passphrase (generates salt)
	ec1 := NewEncryptionConfig(dir)
	ec1.SetPassphrase("my-secret-passphrase")
	key1 := ec1.GetDerivedKey()

	// Load and set same passphrase
	ec2 := NewEncryptionConfig(dir)
	ec2.SetPassphrase("my-secret-passphrase")
	key2 := ec2.GetDerivedKey()

	if !bytes.Equal(key1, key2) {
		t.Error("expected same derived key when using persisted salt")
	}
}

func TestPassphrase_ClearRemovesKey(t *testing.T) {
	dir := t.TempDir()
	ec := NewEncryptionConfig(dir)

	ec.SetPassphrase("my-secret-passphrase")
	ec.ClearPassphrase()

	if ec.HasPassphrase() {
		t.Error("expected passphrase to be cleared")
	}
}

// --- Passphrase Encryption/Decryption Tests ---

func TestPassphraseEncrypt_ProducesCiphertext(t *testing.T) {
	dir := t.TempDir()
	ec := NewEncryptionConfig(dir)
	ec.SetMode(EncryptionModePassphrase)
	ec.SetPassphrase("my-secret-passphrase")

	plaintext := []byte("hello world")
	ciphertext, info, err := ec.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}

	if len(ciphertext) == 0 {
		t.Error("expected non-empty ciphertext")
	}
	if info == nil {
		t.Error("expected encryption info")
	}
	if info.Alg != "AES-256-GCM" {
		t.Errorf("expected alg='AES-256-GCM', got '%s'", info.Alg)
	}
}

func TestPassphraseDecrypt_RecoversPlaintext(t *testing.T) {
	dir := t.TempDir()
	ec := NewEncryptionConfig(dir)
	ec.SetMode(EncryptionModePassphrase)
	ec.SetPassphrase("my-secret-passphrase")

	plaintext := []byte("hello world")
	ciphertext, info, _ := ec.Encrypt(plaintext)

	recovered, err := ec.Decrypt(ciphertext, info)
	if err != nil {
		t.Fatalf("Decrypt returned error: %v", err)
	}

	if !bytes.Equal(recovered, plaintext) {
		t.Errorf("expected '%s', got '%s'", plaintext, recovered)
	}
}

func TestPassphraseDecrypt_FailsWithWrongPassphrase(t *testing.T) {
	dir := t.TempDir()
	ec := NewEncryptionConfig(dir)
	ec.SetMode(EncryptionModePassphrase)
	ec.SetPassphrase("correct-passphrase")

	plaintext := []byte("hello world")
	ciphertext, info, _ := ec.Encrypt(plaintext)

	// Change passphrase
	ec.SetPassphrase("wrong-passphrase")

	_, err := ec.Decrypt(ciphertext, info)
	if err == nil {
		t.Error("expected error when decrypting with wrong passphrase")
	}
}

func TestPassphraseEncrypt_RequiresPassphraseToBeSet(t *testing.T) {
	dir := t.TempDir()
	ec := NewEncryptionConfig(dir)
	ec.SetMode(EncryptionModePassphrase)
	// Don't set passphrase

	_, _, err := ec.Encrypt([]byte("hello"))
	if err == nil {
		t.Error("expected error when passphrase not set")
	}
}

// --- Device Mode Fallback Tests ---

func TestDeviceMode_UsesExistingEncryption(t *testing.T) {
	dir := t.TempDir()
	ec := NewEncryptionConfig(dir)
	// Default is device mode

	plaintext := []byte("hello world")
	ciphertext, info, err := ec.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}

	recovered, err := ec.Decrypt(ciphertext, info)
	if err != nil {
		t.Fatalf("Decrypt returned error: %v", err)
	}

	if !bytes.Equal(recovered, plaintext) {
		t.Errorf("expected '%s', got '%s'", plaintext, recovered)
	}
}

// --- Config File Tests ---

func TestEncryptionConfig_CreatesConfigDir(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "subdir")

	ec := NewEncryptionConfig(subdir)
	ec.SetMode(EncryptionModePassphrase)

	if _, err := os.Stat(subdir); os.IsNotExist(err) {
		t.Error("expected config directory to be created")
	}
}

func TestEncryptionConfig_SecuresFile(t *testing.T) {
	dir := t.TempDir()
	ec := NewEncryptionConfig(dir)
	ec.SetPassphrase("secret")

	// Check file permissions (should be 0600)
	configFile := filepath.Join(dir, "encryption_config.json")
	info, err := os.Stat(configFile)
	if err != nil {
		t.Fatalf("failed to stat config file: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected file permissions 0600, got %o", perm)
	}
}
