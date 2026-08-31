package update

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// PublicKeyHex is the hardcoded public key for signature verification
// This should be replaced with the actual public key generated for your releases
// Generate with: go run tools/release/generate_keys.go
const PublicKeyHex = "REPLACE_WITH_YOUR_PUBLIC_KEY"

// Verifier handles checksum and signature verification
type Verifier struct {
	publicKey ed25519.PublicKey
}

// NewVerifier creates a new verifier
func NewVerifier() *Verifier {
	// Parse the public key from hex
	publicKeyBytes, err := hex.DecodeString(PublicKeyHex)
	if err != nil || len(publicKeyBytes) != ed25519.PublicKeySize {
		// If the key is invalid, return a verifier without signature support
		// Signature verification will be skipped
		return &Verifier{publicKey: nil}
	}

	return &Verifier{
		publicKey: ed25519.PublicKey(publicKeyBytes),
	}
}

// HasSignatureSupport returns true if signature verification is available
func (v *Verifier) HasSignatureSupport() bool {
	return v.publicKey != nil
}

// VerifyChecksum verifies that a file matches the expected SHA256 checksum
func (v *Verifier) VerifyChecksum(filePath, expectedHash string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("failed to calculate hash: %w", err)
	}

	actualHash := hex.EncodeToString(hash.Sum(nil))

	// Normalize hashes (remove spaces, lowercase)
	expectedHash = normalizeHash(expectedHash)
	actualHash = normalizeHash(actualHash)

	if actualHash != expectedHash {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHash, actualHash)
	}

	return nil
}

// VerifySignature verifies the Ed25519 signature of a file
func (v *Verifier) VerifySignature(filePath string, signatureHex string) error {
	if !v.HasSignatureSupport() {
		return fmt.Errorf("signature verification not available (no public key configured)")
	}

	// Read the file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Decode the signature
	signature, err := hex.DecodeString(normalizeHash(signatureHex))
	if err != nil {
		return fmt.Errorf("failed to decode signature: %w", err)
	}

	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("invalid signature size: expected %d, got %d", ed25519.SignatureSize, len(signature))
	}

	// Verify the signature
	// We sign the SHA256 hash of the file to keep signatures small
	fileHash := sha256.Sum256(data)
	if !ed25519.Verify(v.publicKey, fileHash[:], signature) {
		return fmt.Errorf("signature verification failed: signature does not match")
	}

	return nil
}

// VerifyChecksumAndSignature verifies both checksum and signature
func (v *Verifier) VerifyChecksumAndSignature(filePath, expectedHash, signatureHex string) error {
	// First verify checksum
	if err := v.VerifyChecksum(filePath, expectedHash); err != nil {
		return err
	}

	// Then verify signature if available
	if signatureHex != "" && v.HasSignatureSupport() {
		return v.VerifySignature(filePath, signatureHex)
	}

	return nil
}

// VerifyDownloadResult verifies a download result against a checksum and signature
func (v *Verifier) VerifyDownloadResult(result *DownloadResult, expectedHash, signatureHex string) error {
	// If we calculated the hash during download, use it
	if result.SHA256 != "" {
		expectedHash = normalizeHash(expectedHash)
		actualHash := normalizeHash(result.SHA256)

		if actualHash != expectedHash {
			return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHash, actualHash)
		}

		result.Verified = true
		result.VerifiedHash = actualHash
	} else {
		// Otherwise, calculate from file
		if err := v.VerifyChecksum(result.FilePath, expectedHash); err != nil {
			return err
		}
		result.Verified = true
		result.VerifiedHash = expectedHash
	}

	// Verify signature if available
	if signatureHex != "" && v.HasSignatureSupport() {
		if err := v.VerifySignature(result.FilePath, signatureHex); err != nil {
			result.Verified = false
			return fmt.Errorf("signature verification failed: %w", err)
		}
	}

	return nil
}

// CalculateFileHash calculates the SHA256 hash of a file
func (v *Verifier) CalculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to calculate hash: %w", err)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// normalizeHash normalizes a hash string for comparison
func normalizeHash(hash string) string {
	// Lowercase and trim whitespace
	hash = normalizeHashTrim(hash)
	return hash
}

// normalizeHashTrim is a helper that trims whitespace
func normalizeHashTrim(hash string) string {
	// Remove any whitespace and convert to lowercase
	result := make([]byte, 0, len(hash))
	for _, c := range []byte(hash) {
		if c >= 'A' && c <= 'F' {
			result = append(result, c+32) // Convert to lowercase
		} else if c >= 'a' && c <= 'f' || c >= '0' && c <= '9' {
			result = append(result, c)
		}
		// Skip other characters (whitespace, etc.)
	}
	return string(result)
}
