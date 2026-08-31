package vault

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"filippo.io/age"
)

// IdentityFile is the default identity file name.
const IdentityFile = "identity"

// RecipientsFile is the default recipients file name.
const RecipientsFile = "recipients.txt"

// GenerateIdentity generates a new age X25519 identity.
// Returns the identity (private key) and the public key string.
func GenerateIdentity() (*age.X25519Identity, string, error) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate identity: %w", err)
	}
	return identity, identity.Recipient().String(), nil
}

// SaveIdentity saves an X25519 identity to a file with secure permissions.
// The file is written with 0600 permissions (owner read/write only).
func SaveIdentity(identity *age.X25519Identity, path string) error {
	// Ensure directory exists
	dir := expandPath(filepathDir(path))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Write with secure permissions
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# age identity file\n")
	fmt.Fprintf(&buf, "# public key: %s\n", identity.Recipient().String())
	fmt.Fprintf(&buf, "%s\n", identity.String())

	if err := os.WriteFile(expandPath(path), buf.Bytes(), 0600); err != nil {
		return fmt.Errorf("failed to write identity: %w", err)
	}

	return nil
}

// LoadIdentity loads a single identity from a file.
// Returns the identity or an error if the file cannot be read or parsed.
func LoadIdentity(path string) (age.Identity, error) {
	data, err := os.ReadFile(expandPath(path))
	if err != nil {
		return nil, fmt.Errorf("failed to read identity file: %w", err)
	}

	identities, err := age.ParseIdentities(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse identity: %w", err)
	}

	if len(identities) == 0 {
		return nil, fmt.Errorf("no identities found in file")
	}

	// Return the first identity
	return identities[0], nil
}

// LoadIdentities loads all identities from a file.
// A single file can contain multiple identities.
func LoadIdentities(path string) ([]age.Identity, error) {
	data, err := os.ReadFile(expandPath(path))
	if err != nil {
		return nil, fmt.Errorf("failed to read identity file: %w", err)
	}

	identities, err := age.ParseIdentities(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse identities: %w", err)
	}

	return identities, nil
}

// SavePublicKey saves a public key (recipient) to a file.
// This is the file you share with team members.
func SavePublicKey(publicKey string, path string) error {
	dir := expandPath(filepathDir(path))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# age public key\n")
	fmt.Fprintf(&buf, "%s\n", publicKey)

	// Public key can have normal permissions
	if err := os.WriteFile(expandPath(path), buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write public key: %w", err)
	}

	return nil
}

// LoadPublicKey loads a public key from a file.
func LoadPublicKey(path string) (string, error) {
	data, err := os.ReadFile(expandPath(path))
	if err != nil {
		return "", fmt.Errorf("failed to read public key file: %w", err)
	}

	// Parse to validate
	recipients, err := age.ParseRecipients(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("failed to parse public key: %w", err)
	}

	if len(recipients) == 0 {
		return "", fmt.Errorf("no public keys found in file")
	}

	return strings.TrimSpace(string(data)), nil
}

// ParseRecipientsFile parses a recipients file containing multiple public keys.
// Returns a slice of recipients (for encryption) and the raw key strings.
func ParseRecipientsFile(path string) ([]age.Recipient, []string, error) {
	file, err := os.Open(expandPath(path))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open recipients file: %w", err)
	}
	defer file.Close()

	var recipients []age.Recipient
	var keyStrings []string

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse the public key
		rec, err := parseRecipient(line)
		if err != nil {
			continue // Skip invalid lines
		}

		recipients = append(recipients, rec)
		keyStrings = append(keyStrings, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("failed to read recipients file: %w", err)
	}

	return recipients, keyStrings, nil
}

// parseRecipient parses a single recipient string.
func parseRecipient(s string) (age.Recipient, error) {
	recipients, err := age.ParseRecipients(strings.NewReader(s + "\n"))
	if err != nil {
		return nil, err
	}
	if len(recipients) == 0 {
		return nil, fmt.Errorf("no recipient found")
	}
	return recipients[0], nil
}

// ParseRecipients parses multiple recipient strings.
func ParseRecipients(keys []string) ([]age.Recipient, error) {
	var recipients []age.Recipient
	for _, key := range keys {
		rec, err := parseRecipient(key)
		if err != nil {
			return nil, fmt.Errorf("invalid recipient %q: %w", key, err)
		}
		recipients = append(recipients, rec)
	}
	return recipients, nil
}

// WriteRecipientsFile writes a recipients file with the given public keys.
func WriteRecipientsFile(path string, keys []string) error {
	dir := expandPath(filepathDir(path))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("# age recipients - one public key per line\n")
	buf.WriteString("# each listed key can decrypt the vault\n")
	for _, key := range keys {
		buf.WriteString(key)
		buf.WriteByte('\n')
	}

	if err := os.WriteFile(expandPath(path), buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write recipients file: %w", err)
	}

	return nil
}

// AddRecipient adds a public key to an existing recipients file.
func AddRecipient(path string, publicKey string) error {
	// Load existing
	_, keys, err := ParseRecipientsFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read recipients file: %w", err)
	}

	// Check for duplicate
	if slices.Contains(keys, publicKey) {
		return fmt.Errorf("recipient already exists")
	}

	// Add new
	keys = append(keys, publicKey)

	return WriteRecipientsFile(path, keys)
}

// RemoveRecipient removes a public key from a recipients file.
func RemoveRecipient(path string, publicKey string) error {
	// Load existing
	_, keys, err := ParseRecipientsFile(path)
	if err != nil {
		return fmt.Errorf("failed to read recipients file: %w", err)
	}

	// Remove matching
	var newKeys []string
	for _, k := range keys {
		if k != publicKey {
			newKeys = append(newKeys, k)
		}
	}

	if len(newKeys) == len(keys) {
		return fmt.Errorf("recipient not found")
	}

	return WriteRecipientsFile(path, newKeys)
}

// IdentityExists checks if an identity file exists.
func IdentityExists(path string) bool {
	_, err := os.Stat(expandPath(path))
	return err == nil
}

// RecipientsExist checks if a recipients file exists.
func RecipientsExist(path string) bool {
	_, err := os.Stat(expandPath(path))
	return err == nil
}

// filepathDir extracts the directory part of a path.
func filepathDir(path string) string {
	dir := ""
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == os.PathSeparator {
			dir = path[:i]
			break
		}
	}
	if dir == "" {
		return "."
	}
	return dir
}

// EnsureIdentity ensures an identity exists, generating one if needed.
// Returns the X25519 identity and whether it was newly generated.
func EnsureIdentity(path string) (*age.X25519Identity, bool, error) {
	// Try to load existing
	if IdentityExists(path) {
		identity, err := LoadX25519Identity(path)
		if err != nil {
			return nil, false, err
		}
		return identity, false, nil
	}

	// Generate new
	identity, _, err := GenerateIdentity()
	if err != nil {
		return nil, false, err
	}

	if err := SaveIdentity(identity, path); err != nil {
		return nil, false, err
	}

	return identity, true, nil
}

// LoadX25519Identity loads a single X25519 identity from a file.
// This returns the concrete type for access to Recipient() method.
func LoadX25519Identity(path string) (*age.X25519Identity, error) {
	data, err := os.ReadFile(expandPath(path))
	if err != nil {
		return nil, fmt.Errorf("failed to read identity file: %w", err)
	}

	identities, err := age.ParseIdentities(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse identity: %w", err)
	}

	if len(identities) == 0 {
		return nil, fmt.Errorf("no identities found in file")
	}

	// Cast to X25519Identity
	if x25519, ok := identities[0].(*age.X25519Identity); ok {
		return x25519, nil
	}

	return nil, fmt.Errorf("identity is not an X25519 identity")
}

// LoadIdentityWithPublicKey loads an X25519 identity and returns both the identity and its public key.
func LoadIdentityWithPublicKey(path string) (*age.X25519Identity, string, error) {
	identity, err := LoadX25519Identity(path)
	if err != nil {
		return nil, "", err
	}
	return identity, identity.Recipient().String(), nil
}

// RecipientInfo contains information about a recipient.
type RecipientInfo struct {
	PublicKey string `json:"publicKey"`
	Comment   string `json:"comment,omitempty"`
}

// ParseRecipientsWithComments parses a recipients file and extracts comments.
func ParseRecipientsWithComments(path string) ([]RecipientInfo, error) {
	file, err := os.Open(expandPath(path))
	if err != nil {
		return nil, fmt.Errorf("failed to open recipients file: %w", err)
	}
	defer file.Close()

	var infos []RecipientInfo
	var lastComment string

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" {
			lastComment = ""
			continue
		}

		if after, ok := strings.CutPrefix(line, "#"); ok {
			// Store comment for next key
			lastComment = strings.TrimSpace(after)
			continue
		}

		// This is a key
		infos = append(infos, RecipientInfo{
			PublicKey: line,
			Comment:   lastComment,
		})
		lastComment = ""
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read recipients file: %w", err)
	}

	return infos, nil
}

// WriteRecipientsFileWithComments writes a recipients file with comments.
func WriteRecipientsFileWithComments(path string, infos []RecipientInfo) error {
	dir := expandPath(filepathDir(path))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("# age recipients - one public key per line\n")

	for _, info := range infos {
		if info.Comment != "" {
			buf.WriteString("# ")
			buf.WriteString(info.Comment)
			buf.WriteByte('\n')
		}
		buf.WriteString(info.PublicKey)
		buf.WriteByte('\n')
	}

	if err := os.WriteFile(expandPath(path), buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write recipients file: %w", err)
	}

	return nil
}

// Verify imports
var _ io.Reader = (*bytes.Buffer)(nil)
