package cloudsync

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	encryptionKeyFile    = "cloud_encryption_key.json"
	encryptionKeyVersion = 1
	encryptionAlg        = "AES-256-GCM"
)

type encryptionKeyState struct {
	Version int    `json:"version"`
	Key     string `json:"key"`
}

func encryptPayload(configDir string, plaintext []byte) (string, *EncryptionInfo, error) {
	key, version, err := loadOrCreateEncryptionKey(configDir)
	if err != nil {
		return "", nil, err
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
		Version: version,
	}, nil
}

func decryptPayload(configDir string, payload string, info *EncryptionInfo) ([]byte, error) {
	if info == nil {
		return nil, fmt.Errorf("encryption metadata required")
	}
	if info.Alg != encryptionAlg {
		return nil, fmt.Errorf("unsupported encryption algorithm %s", info.Alg)
	}
	if info.Version != encryptionKeyVersion {
		return nil, fmt.Errorf("unsupported encryption version %d", info.Version)
	}

	key, _, err := loadEncryptionKey(configDir)
	if err != nil {
		return nil, err
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

	ciphertext, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("invalid encrypted payload: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt payload")
	}

	return plaintext, nil
}

func loadEncryptionKey(configDir string) ([]byte, int, error) {
	path := filepath.Join(configDir, encryptionKeyFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("missing encryption key")
	}

	var state encryptionKeyState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, 0, fmt.Errorf("invalid encryption key file")
	}
	if state.Version != encryptionKeyVersion {
		return nil, 0, fmt.Errorf("unsupported encryption key version")
	}

	keyBytes, err := base64.StdEncoding.DecodeString(state.Key)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid encryption key data")
	}
	if len(keyBytes) != 32 {
		return nil, 0, fmt.Errorf("invalid encryption key length")
	}

	return keyBytes, state.Version, nil
}

func loadOrCreateEncryptionKey(configDir string) ([]byte, int, error) {
	if key, version, err := loadEncryptionKey(configDir); err == nil {
		return key, version, nil
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, 0, err
	}

	state := encryptionKeyState{
		Version: encryptionKeyVersion,
		Key:     base64.StdEncoding.EncodeToString(key),
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, 0, err
	}

	if err := os.MkdirAll(configDir, 0700); err != nil {
		return nil, 0, err
	}
	if err := os.WriteFile(filepath.Join(configDir, encryptionKeyFile), data, 0600); err != nil {
		return nil, 0, err
	}

	return key, state.Version, nil
}
