package vault

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"filippo.io/age"
)

// AgeStorage provides encrypted credential storage using age encryption.
// The vault file is stored in binary format (not JSON) for maximum security.
type AgeStorage struct {
	path      string
	encryptor *AgeEncryptor
	creds     map[string]*Credential
	mu        sync.RWMutex
}

// AgeEncryptor wraps age encryption for vault use.
type AgeEncryptor struct {
	recipient age.Recipient
	identity  age.Identity
}

// NewAgeEncryptorFromPassphrase creates an encryptor from a passphrase.
func NewAgeEncryptorFromPassphrase(passphrase string) (*AgeEncryptor, error) {
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, fmt.Errorf("failed to create scrypt recipient: %w", err)
	}

	identity, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, fmt.Errorf("failed to create scrypt identity: %w", err)
	}

	return &AgeEncryptor{
		recipient: recipient,
		identity:  identity,
	}, nil
}

// Encrypt encrypts data and returns binary ciphertext.
func (e *AgeEncryptor) Encrypt(plaintext []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, e.recipient)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decrypt decrypts binary ciphertext.
func (e *AgeEncryptor) Decrypt(ciphertext []byte) ([]byte, error) {
	r, err := age.Decrypt(bytes.NewReader(ciphertext), e.identity)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// vaultHeader is a binary header for the vault file.
// Format: [magic(4)] [version(2)] [checksum(32)] [created_at(8)] [updated_at(8)] [encrypted_data]
type vaultHeader struct {
	Magic     [4]byte // "SVLT"
	Version   uint16
	Checksum  [32]byte // SHA-256 of plaintext
	CreatedAt int64    // Unix timestamp
	UpdatedAt int64    // Unix timestamp
}

const (
	vaultMagic   = "SVLT"
	vaultVersion = 1
)

// NewAgeStorage creates a new encrypted storage.
func NewAgeStorage(path string, encryptor *AgeEncryptor) (*AgeStorage, error) {
	s := &AgeStorage{
		path:      expandPath(path),
		encryptor: encryptor,
		creds:     make(map[string]*Credential),
	}

	// Load existing if present
	if _, err := os.Stat(s.path); err == nil {
		if err := s.load(); err != nil {
			return nil, fmt.Errorf("failed to load vault: %w", err)
		}
	}

	return s, nil
}

// load loads the vault from disk.
func (s *AgeStorage) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	if len(data) < 54 { // Minimum header size: 4+2+32+8+8
		return errors.New("vault file too small or corrupted")
	}

	// Parse header
	var header vaultHeader
	copy(header.Magic[:], data[0:4])
	header.Version = binary.LittleEndian.Uint16(data[4:6])
	copy(header.Checksum[:], data[6:38])
	header.CreatedAt = int64(binary.LittleEndian.Uint64(data[38:46]))
	header.UpdatedAt = int64(binary.LittleEndian.Uint64(data[46:54]))

	// Verify magic
	if string(header.Magic[:]) != vaultMagic {
		return errors.New("invalid vault file format")
	}

	// Check version
	if header.Version != vaultVersion {
		return fmt.Errorf("unsupported vault version: %d", header.Version)
	}

	// Decrypt payload
	encrypted := data[54:]
	plaintext, err := s.encryptor.Decrypt(encrypted)
	if err != nil {
		return fmt.Errorf("failed to decrypt vault: %w", err)
	}

	// Verify checksum
	checksum := sha256.Sum256(plaintext)
	if checksum != header.Checksum {
		return errors.New("vault checksum mismatch (possible corruption or wrong passphrase)")
	}

	// Parse credentials
	var creds map[string]*Credential
	if err := json.Unmarshal(plaintext, &creds); err != nil {
		return fmt.Errorf("invalid credential data: %w", err)
	}

	s.creds = creds
	return nil
}

// save saves the vault to disk in binary format.
func (s *AgeStorage) save() error {
	// Serialize credentials
	plaintext, err := json.Marshal(s.creds)
	if err != nil {
		return err
	}

	// Calculate checksum
	checksum := sha256.Sum256(plaintext)

	// Encrypt
	encrypted, err := s.encryptor.Encrypt(plaintext)
	if err != nil {
		return err
	}

	// Build header
	now := time.Now().Unix()
	header := vaultHeader{
		Version:   vaultVersion,
		Checksum:  checksum,
		UpdatedAt: now,
	}
	copy(header.Magic[:], vaultMagic)

	// Preserve creation time from existing file
	if data, err := os.ReadFile(s.path); err == nil && len(data) >= 54 {
		header.CreatedAt = int64(binary.LittleEndian.Uint64(data[38:46]))
	}
	if header.CreatedAt == 0 {
		header.CreatedAt = now
	}

	// Build output buffer
	var buf bytes.Buffer
	buf.Write(header.Magic[:])
	binary.Write(&buf, binary.LittleEndian, header.Version)
	buf.Write(header.Checksum[:])
	binary.Write(&buf, binary.LittleEndian, uint64(header.CreatedAt))
	binary.Write(&buf, binary.LittleEndian, uint64(header.UpdatedAt))
	buf.Write(encrypted)

	// Ensure directory exists
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	// Write atomically with secure permissions
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, buf.Bytes(), 0600); err != nil {
		return err
	}

	return os.Rename(tmpPath, s.path)
}

// Store stores a credential.
func (s *AgeStorage) Store(ctx context.Context, cred Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	cred.CreatedAt = now
	cred.UpdatedAt = now

	c := cred
	s.creds[cred.ID] = &c

	return s.save()
}

// Retrieve retrieves a credential by ID.
func (s *AgeStorage) Retrieve(ctx context.Context, id string) (*Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cred, ok := s.creds[id]
	if !ok {
		return nil, ErrNotFound
	}

	if cred.IsExpired() {
		return nil, ErrExpired
	}

	c := *cred
	return &c, nil
}

// Delete deletes a credential.
func (s *AgeStorage) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.creds[id]; !ok {
		return ErrNotFound
	}

	delete(s.creds, id)

	return s.save()
}

// List lists credentials matching filter.
func (s *AgeStorage) List(ctx context.Context, filter CredentialFilter) ([]Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Credential

	for _, cred := range s.creds {
		// Apply filters
		if filter.Kind != "" && cred.Kind != filter.Kind {
			continue
		}
		if filter.Scope != "" && cred.Scope != filter.Scope {
			continue
		}
		if filter.ProjectID != "" && cred.ProjectID != filter.ProjectID {
			continue
		}
		if !filter.IncludeExpired && cred.IsExpired() {
			continue
		}

		// Tag filter
		if len(filter.Tags) > 0 {
			tagSet := make(map[string]bool)
			for _, t := range cred.Tags {
				tagSet[t] = true
			}
			hasAll := true
			for _, t := range filter.Tags {
				if !tagSet[t] {
					hasAll = false
					break
				}
			}
			if !hasAll {
				continue
			}
		}

		c := *cred
		// Hide secret unless explicitly requested (CLI only)
		if !filter.IncludeSecrets {
			c.Secret = ""
		}
		result = append(result, c)
	}

	return result, nil
}

// RetrieveForExecution retrieves a credential for execution.
func (s *AgeStorage) RetrieveForExecution(ctx context.Context, id string, projectID string) (*Credential, error) {
	return s.Retrieve(ctx, id)
}

// UpdateLastUsed updates the last used timestamp.
func (s *AgeStorage) UpdateLastUsed(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cred, ok := s.creds[id]
	if !ok {
		return ErrNotFound
	}

	now := time.Now()
	cred.LastUsedAt = &now

	return s.save()
}

// GetPath returns the vault file path.
func (s *AgeStorage) GetPath() string {
	return s.path
}

// MemoryStorage provides in-memory credential storage (for testing).
type MemoryStorage struct {
	creds map[string]*Credential
	mu    sync.RWMutex
}

// NewMemoryStorage creates a new in-memory storage.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		creds: make(map[string]*Credential),
	}
}

// Store stores a credential.
func (s *MemoryStorage) Store(ctx context.Context, cred Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	cred.CreatedAt = now
	cred.UpdatedAt = now

	c := cred
	s.creds[cred.ID] = &c
	return nil
}

// Retrieve retrieves a credential.
func (s *MemoryStorage) Retrieve(ctx context.Context, id string) (*Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cred, ok := s.creds[id]
	if !ok {
		return nil, ErrNotFound
	}
	if cred.IsExpired() {
		return nil, ErrExpired
	}
	c := *cred
	return &c, nil
}

// Delete deletes a credential.
func (s *MemoryStorage) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.creds[id]; !ok {
		return ErrNotFound
	}
	delete(s.creds, id)
	return nil
}

// List lists credentials.
func (s *MemoryStorage) List(ctx context.Context, filter CredentialFilter) ([]Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Credential
	for _, cred := range s.creds {
		if filter.Kind != "" && cred.Kind != filter.Kind {
			continue
		}
		if filter.Scope != "" && cred.Scope != filter.Scope {
			continue
		}
		if !filter.IncludeExpired && cred.IsExpired() {
			continue
		}
		c := *cred
		if !filter.IncludeSecrets {
			c.Secret = ""
		}
		result = append(result, c)
	}
	return result, nil
}

// RetrieveForExecution retrieves a credential for execution.
func (s *MemoryStorage) RetrieveForExecution(ctx context.Context, id string, projectID string) (*Credential, error) {
	return s.Retrieve(ctx, id)
}

// UpdateLastUsed updates the last used timestamp.
func (s *MemoryStorage) UpdateLastUsed(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cred, ok := s.creds[id]
	if !ok {
		return ErrNotFound
	}
	now := time.Now()
	cred.LastUsedAt = &now
	return nil
}

// MultiRecipientStorage provides encrypted storage using age multi-recipient encryption.
// The vault is encrypted to multiple recipients (public keys) and can be decrypted by
// any matching identity (private key). This enables team vaults in git repositories.
type MultiRecipientStorage struct {
	path       string
	identities []age.Identity  // For decryption (any matching one works)
	recipients []age.Recipient // For encryption (encrypt to all)
	creds      map[string]*Credential
	mu         sync.RWMutex
}

// NewMultiRecipientStorage creates storage for a team vault.
// identities: the user's private key(s) for decryption
// recipients: all team members' public keys for re-encryption
func NewMultiRecipientStorage(path string, identities []age.Identity, recipients []age.Recipient) (*MultiRecipientStorage, error) {
	s := &MultiRecipientStorage{
		path:       expandPath(path),
		identities: identities,
		recipients: recipients,
		creds:      make(map[string]*Credential),
	}

	// Load existing if present
	if _, err := os.Stat(s.path); err == nil {
		if err := s.load(); err != nil {
			return nil, fmt.Errorf("failed to load vault: %w", err)
		}
	}

	return s, nil
}

// NewMultiRecipientStorageFromFiles creates storage from identity and recipients files.
// identityPath: user's private key file (e.g., ~/.swarm/vault/identity)
// recipientsPath: team's public keys file (e.g., .swarm/vault/recipients.txt)
func NewMultiRecipientStorageFromFiles(vaultPath, identityPath, recipientsPath string) (*MultiRecipientStorage, error) {
	// Load identity
	identities, err := LoadIdentities(identityPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load identity: %w", err)
	}

	// Load recipients
	recipients, _, err := ParseRecipientsFile(recipientsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load recipients: %w", err)
	}

	return NewMultiRecipientStorage(vaultPath, identities, recipients)
}

// load loads the vault from disk.
func (s *MultiRecipientStorage) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	if len(data) < 54 {
		return errors.New("vault file too small or corrupted")
	}

	// Parse header (same format as AgeStorage)
	var header vaultHeader
	copy(header.Magic[:], data[0:4])
	header.Version = binary.LittleEndian.Uint16(data[4:6])
	copy(header.Checksum[:], data[6:38])
	header.CreatedAt = int64(binary.LittleEndian.Uint64(data[38:46]))
	header.UpdatedAt = int64(binary.LittleEndian.Uint64(data[46:54]))

	// Verify magic
	if string(header.Magic[:]) != vaultMagic {
		return errors.New("invalid vault file format")
	}

	// Check version
	if header.Version != vaultVersion {
		return fmt.Errorf("unsupported vault version: %d", header.Version)
	}

	// Decrypt payload - age tries all identities automatically
	encrypted := data[54:]
	r, err := age.Decrypt(bytes.NewReader(encrypted), s.identities...)
	if err != nil {
		return fmt.Errorf("failed to decrypt vault (no matching identity): %w", err)
	}
	plaintext, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("failed to read decrypted data: %w", err)
	}

	// Verify checksum
	checksum := sha256.Sum256(plaintext)
	if checksum != header.Checksum {
		return errors.New("vault checksum mismatch (possible corruption)")
	}

	// Parse credentials
	var creds map[string]*Credential
	if err := json.Unmarshal(plaintext, &creds); err != nil {
		return fmt.Errorf("invalid credential data: %w", err)
	}

	s.creds = creds
	return nil
}

// save saves the vault to disk, encrypting to all recipients.
func (s *MultiRecipientStorage) save() error {
	// Serialize credentials
	plaintext, err := json.Marshal(s.creds)
	if err != nil {
		return err
	}

	// Calculate checksum
	checksum := sha256.Sum256(plaintext)

	// Encrypt to ALL recipients
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, s.recipients...)
	if err != nil {
		return fmt.Errorf("failed to create encryptor: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return fmt.Errorf("failed to encrypt: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("failed to finalize encryption: %w", err)
	}
	encrypted := buf.Bytes()

	// Build header
	now := time.Now().Unix()
	header := vaultHeader{
		Version:   vaultVersion,
		Checksum:  checksum,
		UpdatedAt: now,
	}
	copy(header.Magic[:], vaultMagic)

	// Preserve creation time from existing file
	if data, err := os.ReadFile(s.path); err == nil && len(data) >= 54 {
		header.CreatedAt = int64(binary.LittleEndian.Uint64(data[38:46]))
	}
	if header.CreatedAt == 0 {
		header.CreatedAt = now
	}

	// Build output buffer
	var outBuf bytes.Buffer
	outBuf.Write(header.Magic[:])
	binary.Write(&outBuf, binary.LittleEndian, header.Version)
	outBuf.Write(header.Checksum[:])
	binary.Write(&outBuf, binary.LittleEndian, uint64(header.CreatedAt))
	binary.Write(&outBuf, binary.LittleEndian, uint64(header.UpdatedAt))
	outBuf.Write(encrypted)

	// Ensure directory exists
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	// Write atomically
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, outBuf.Bytes(), 0644); err != nil {
		return err
	}

	return os.Rename(tmpPath, s.path)
}

// Store stores a credential.
func (s *MultiRecipientStorage) Store(ctx context.Context, cred Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	cred.CreatedAt = now
	cred.UpdatedAt = now

	c := cred
	s.creds[cred.ID] = &c

	return s.save()
}

// Retrieve retrieves a credential by ID.
func (s *MultiRecipientStorage) Retrieve(ctx context.Context, id string) (*Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cred, ok := s.creds[id]
	if !ok {
		return nil, ErrNotFound
	}

	if cred.IsExpired() {
		return nil, ErrExpired
	}

	c := *cred
	return &c, nil
}

// Delete deletes a credential.
func (s *MultiRecipientStorage) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.creds[id]; !ok {
		return ErrNotFound
	}

	delete(s.creds, id)

	return s.save()
}

// List lists credentials matching filter.
func (s *MultiRecipientStorage) List(ctx context.Context, filter CredentialFilter) ([]Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Credential

	for _, cred := range s.creds {
		// Apply filters
		if filter.Kind != "" && cred.Kind != filter.Kind {
			continue
		}
		if filter.Scope != "" && cred.Scope != filter.Scope {
			continue
		}
		if filter.ProjectID != "" && cred.ProjectID != filter.ProjectID {
			continue
		}
		if !filter.IncludeExpired && cred.IsExpired() {
			continue
		}

		// Tag filter
		if len(filter.Tags) > 0 {
			tagSet := make(map[string]bool)
			for _, t := range cred.Tags {
				tagSet[t] = true
			}
			hasAll := true
			for _, t := range filter.Tags {
				if !tagSet[t] {
					hasAll = false
					break
				}
			}
			if !hasAll {
				continue
			}
		}

		c := *cred
		// Hide secret unless explicitly requested
		if !filter.IncludeSecrets {
			c.Secret = ""
		}
		result = append(result, c)
	}

	return result, nil
}

// RetrieveForExecution retrieves a credential for execution.
func (s *MultiRecipientStorage) RetrieveForExecution(ctx context.Context, id string, projectID string) (*Credential, error) {
	return s.Retrieve(ctx, id)
}

// UpdateLastUsed updates the last used timestamp.
func (s *MultiRecipientStorage) UpdateLastUsed(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cred, ok := s.creds[id]
	if !ok {
		return ErrNotFound
	}

	now := time.Now()
	cred.LastUsedAt = &now

	return s.save()
}

// GetPath returns the vault file path.
func (s *MultiRecipientStorage) GetPath() string {
	return s.path
}

// GetRecipients returns the current recipients (for display).
func (s *MultiRecipientStorage) GetRecipients() []age.Recipient {
	return s.recipients
}

// SetRecipients updates the recipients and re-encrypts.
// Use this when team membership changes.
func (s *MultiRecipientStorage) SetRecipients(recipients []age.Recipient) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recipients = recipients
	return s.save()
}

// ReloadRecipients reloads recipients from file and re-encrypts.
func (s *MultiRecipientStorage) ReloadRecipients(recipientsPath string) error {
	recipients, _, err := ParseRecipientsFile(recipientsPath)
	if err != nil {
		return fmt.Errorf("failed to reload recipients: %w", err)
	}
	return s.SetRecipients(recipients)
}
