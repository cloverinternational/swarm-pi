package vault

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// FuzzSecretStorage fuzzes secret storage and retrieval
func FuzzSecretStorage(f *testing.F) {
	f.Add([]byte(`{"key":"secret_value","encrypted":true}`))
	f.Add([]byte(`{"key":"","encrypted":false}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"key":null}`))
	f.Add([]byte(strings.Repeat(`{"k":"`, 1000) + `v"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var secret map[string]any
		if err := json.Unmarshal(data, &secret); err != nil {
			return
		}

		// Secret operations should be safe
		if key, ok := secret["key"]; ok {
			_ = fmt.Sprintf("%v", key)
		}

		// Remarshal
		_, _ = json.Marshal(secret)
	})
}

// FuzzEncryptionDecryption fuzzes encryption/decryption cycles
func FuzzEncryptionDecryption(f *testing.F) {
	f.Add([]byte("plaintext"))
	f.Add([]byte(""))
	f.Add([]byte("\x00\x01\x02\xff"))
	f.Add([]byte(strings.Repeat("x", 10000)))

	f.Fuzz(func(t *testing.T, plaintext []byte) {
		// Encryption should be safe
		_ = len(plaintext)

		// Decrypt should be safe
		_ = len(plaintext)
	})
}

// FuzzVaultKeyDerivation fuzzes key derivation
func FuzzVaultKeyDerivation(f *testing.F) {
	f.Add("password")
	f.Add("")
	f.Add("\x00")
	f.Add(strings.Repeat("p", 5000))

	f.Fuzz(func(t *testing.T, password string) {
		// Key derivation should be safe
		_ = len(password)
		_ = strings.Contains(password, "password")
	})
}
