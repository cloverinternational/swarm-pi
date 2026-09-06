package cloudsync

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/pbkdf2"
)

// Encryption mode constants
const (
	EncryptionModeDevice     = "device"
	EncryptionModePassphrase = "passphrase"
)

const (
	encryptionConfigFile = "encryption_config.json"
	pbkdf2Iterations     = 100000
	keyLength            = 32
	saltLength           = 16
)

// Errors
var (
	ErrInvalidEncryptionMode = errors.New("invalid encryption mode")
	ErrEmptyPassphrase       = errors.New("passphrase cannot be empty")
	ErrPassphraseNotSet      = errors.New("passphrase not set")
)

// DeriveKeyFromPassphrase derives a 32-byte key using PBKDF2-SHA256
func DeriveKeyFromPassphrase(passphrase string, salt []byte) []byte {
	return pbkdf2.Key([]byte(passphrase), salt, pbkdf2Iterations, keyLength, sha256.New)
}

// encryptionConfigState is persisted to disk
type encryptionConfigState struct {
	Mode string `json:"mode"`
	Salt string `json:"salt,omitempty"` // Base64 encoded salt for passphrase mode
}

// EncryptionConfig manages encryption mode and passphrase
type EncryptionConfig struct {
	mu         sync.RWMutex
	configDir  string
	mode       string
	salt       []byte
	passphrase string // Not persisted - held in memory only
	derivedKey []byte // Cached derived key
}

// NewEncryptionConfig creates a new EncryptionConfig, loading existing state from disk
func NewEncryptionConfig(configDir string) *EncryptionConfig {
	ec := &EncryptionConfig{
		configDir: configDir,
		mode:      EncryptionModeDevice, // Default
	}

	// Try to load existing config
	ec.loadConfig()

	return ec
}

// loadConfig loads the config from disk
func (ec *EncryptionConfig) loadConfig() {
	path := filepath.Join(ec.configDir, encryptionConfigFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return // File doesn't exist, use defaults
	}

	var state encryptionConfigState
	if err := json.Unmarshal(data, &state); err != nil {
		return // Invalid file, use defaults
	}

	ec.mode = state.Mode
	if state.Salt != "" {
		ec.salt, _ = base64.StdEncoding.DecodeString(state.Salt)
	}
}

// saveConfig saves the config to disk
func (ec *EncryptionConfig) saveConfig() error {
	if err := os.MkdirAll(ec.configDir, 0700); err != nil {
		return err
	}

	state := encryptionConfigState{
		Mode: ec.mode,
	}
	if len(ec.salt) > 0 {
		state.Salt = base64.StdEncoding.EncodeToString(ec.salt)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(ec.configDir, encryptionConfigFile)
	return os.WriteFile(path, data, 0600)
}

// Mode returns the current encryption mode
func (ec *EncryptionConfig) Mode() string {
	ec.mu.RLock()
	defer ec.mu.RUnlock()
	return ec.mode
}

// SetMode sets the encryption mode
func (ec *EncryptionConfig) SetMode(mode string) error {
	if mode != EncryptionModeDevice && mode != EncryptionModePassphrase {
		return ErrInvalidEncryptionMode
	}

	ec.mu.Lock()
	defer ec.mu.Unlock()

	ec.mode = mode
	return ec.saveConfig()
}

// HasPassphrase returns true if a passphrase has been set
func (ec *EncryptionConfig) HasPassphrase() bool {
	ec.mu.RLock()
	defer ec.mu.RUnlock()
	return ec.passphrase != ""
}

// SetPassphrase sets the passphrase and derives the encryption key
func (ec *EncryptionConfig) SetPassphrase(passphrase string) error {
	if passphrase == "" {
		return ErrEmptyPassphrase
	}

	ec.mu.Lock()
	defer ec.mu.Unlock()

	ec.passphrase = passphrase

	// Generate salt if not already set (first time)
	if len(ec.salt) == 0 {
		ec.salt = make([]byte, saltLength)
		if _, err := rand.Read(ec.salt); err != nil {
			return err
		}
		// Save the salt to disk
		if err := ec.saveConfig(); err != nil {
			return err
		}
	}

	// Derive the key
	ec.derivedKey = DeriveKeyFromPassphrase(passphrase, ec.salt)

	return nil
}

// ClearPassphrase clears the passphrase and derived key from memory
func (ec *EncryptionConfig) ClearPassphrase() {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	ec.passphrase = ""
	ec.derivedKey = nil
}

// GetDerivedKey returns the derived key (requires passphrase to be set)
func (ec *EncryptionConfig) GetDerivedKey() []byte {
	ec.mu.RLock()
	defer ec.mu.RUnlock()
	return ec.derivedKey
}

// Encrypt encrypts plaintext using the current mode's key
func (ec *EncryptionConfig) Encrypt(plaintext []byte) (string, *EncryptionInfo, error) {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	var key []byte

	if ec.mode == EncryptionModePassphrase {
		if ec.derivedKey == nil {
			return "", nil, ErrPassphraseNotSet
		}
		key = ec.derivedKey
	} else {
		// Device mode - use existing device key
		var err error
		key, _, err = loadOrCreateEncryptionKey(ec.configDir)
		if err != nil {
			return "", nil, err
		}
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", nil, err
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	payload := base64.StdEncoding.EncodeToString(ciphertext)

	return payload, &EncryptionInfo{
		Alg:     encryptionAlg,
		Nonce:   base64.StdEncoding.EncodeToString(nonce),
		Version: encryptionKeyVersion,
	}, nil
}

// Decrypt decrypts ciphertext using the current mode's key
func (ec *EncryptionConfig) Decrypt(ciphertext string, info *EncryptionInfo) ([]byte, error) {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	if info == nil {
		return nil, fmt.Errorf("encryption metadata required")
	}
	if info.Alg != encryptionAlg {
		return nil, fmt.Errorf("unsupported encryption algorithm %s", info.Alg)
	}

	var key []byte

	if ec.mode == EncryptionModePassphrase {
		if ec.derivedKey == nil {
			return nil, ErrPassphraseNotSet
		}
		key = ec.derivedKey
	} else {
		// Device mode - use existing device key
		var err error
		key, _, err = loadEncryptionKey(ec.configDir)
		if err != nil {
			return nil, err
		}
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce, err := base64.StdEncoding.DecodeString(info.Nonce)
	if err != nil {
		return nil, fmt.Errorf("invalid nonce: %w", err)
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("invalid nonce length")
	}

	ciphertextBytes, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("invalid encrypted payload: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt payload")
	}

	return plaintext, nil
}
