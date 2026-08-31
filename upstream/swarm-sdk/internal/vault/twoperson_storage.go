package vault

// TwoPersonStorage persists sensitive credentials protected by Two-Person
// Integrity. Unlike AgeStorage / MultiRecipientStorage, it does NOT return a
// usable Secret on retrieval — a single machine only holds one Shamir share, so
// the plaintext is only reconstructable through the cross-principal approval
// flow (see executor two-person path). The file is safe to commit to git: it
// contains only public credential metadata plus ciphertext + age-encrypted
// shares.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const twoPersonVersion = "1"

// twoPersonRecord is the on-disk entry for one sensitive credential.
type twoPersonRecord struct {
	// Public, non-secret metadata (mirrors Credential minus the Secret).
	Name            string          `json:"name,omitempty"`
	Kind            CredentialKind  `json:"kind"`
	Scope           CredentialScope `json:"scope,omitempty"`
	ProjectID       string          `json:"projectId,omitempty"`
	AllowedTools    []string        `json:"allowedTools,omitempty"`
	AllowedCommands []string        `json:"allowedCommands,omitempty"`
	AllowedHosts    []string        `json:"allowedHosts,omitempty"`
	Tags            []string        `json:"tags,omitempty"`
	Inject          InjectConfig    `json:"inject"`
	ExpiresAt       *time.Time      `json:"expiresAt,omitempty"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
	// The protected secret. No plaintext, no single-holder-decryptable copy.
	Envelope *TwoPersonEnvelope `json:"envelope"`
}

// twoPersonDiskFormat is the top-level JSON structure.
type twoPersonDiskFormat struct {
	Version   string                     `json:"version"`
	UpdatedAt time.Time                  `json:"updatedAt"`
	Records   map[string]twoPersonRecord `json:"records"`
}

// TwoPersonStorage is an on-disk store for two-person credentials.
type TwoPersonStorage struct {
	path    string
	records map[string]twoPersonRecord
	mu      sync.RWMutex
}

// NewTwoPersonStorage creates or loads a two-person credential store.
func NewTwoPersonStorage(path string) (*TwoPersonStorage, error) {
	s := &TwoPersonStorage{
		path:    expandPath(path),
		records: make(map[string]twoPersonRecord),
	}
	if _, err := os.Stat(s.path); err == nil {
		if err := s.load(); err != nil {
			return nil, fmt.Errorf("failed to load two-person vault: %w", err)
		}
	}
	return s, nil
}

func (s *TwoPersonStorage) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var disk twoPersonDiskFormat
	if err := json.Unmarshal(data, &disk); err != nil {
		return fmt.Errorf("invalid two-person vault format: %w", err)
	}
	if disk.Version != twoPersonVersion {
		return fmt.Errorf("unsupported two-person vault version: %s", disk.Version)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = disk.Records
	if s.records == nil {
		s.records = make(map[string]twoPersonRecord)
	}
	return nil
}

func (s *TwoPersonStorage) save() error {
	disk := twoPersonDiskFormat{
		Version:   twoPersonVersion,
		UpdatedAt: time.Now(),
		Records:   s.records,
	}
	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// SealAndStore protects secret under a K-of-N policy and stores it. The
// plaintext secret is consumed here and never persisted in the clear.
func (s *TwoPersonStorage) SealAndStore(ctx context.Context, cred Credential, threshold int, recipients []recipientWithKey) error {
	env, err := twoPersonSeal(cred.ID, []byte(cred.Secret), threshold, recipients)
	if err != nil {
		return err
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := twoPersonRecord{
		Name:            cred.Name,
		Kind:            cred.Kind,
		Scope:           cred.Scope,
		ProjectID:       cred.ProjectID,
		AllowedTools:    cred.AllowedTools,
		AllowedCommands: cred.AllowedCommands,
		AllowedHosts:    cred.AllowedHosts,
		Tags:            cred.Tags,
		Inject:          cred.Inject,
		ExpiresAt:       cred.ExpiresAt,
		CreatedAt:       now,
		UpdatedAt:       now,
		Envelope:        env,
	}
	if existing, ok := s.records[cred.ID]; ok {
		rec.CreatedAt = existing.CreatedAt
	}
	s.records[cred.ID] = rec
	return s.save()
}

// GetEnvelope returns the protected envelope for a credential (no plaintext).
func (s *TwoPersonStorage) GetEnvelope(id string) (*TwoPersonEnvelope, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[id]
	if !ok {
		return nil, ErrNotFound
	}
	return rec.Envelope, nil
}

// Metadata returns the public credential metadata (Secret is always empty).
func (s *TwoPersonStorage) Metadata(id string) (*Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[id]
	if !ok {
		return nil, ErrNotFound
	}
	return recordToCredential(id, rec), nil
}

// Retrieve satisfies the Storage interface but intentionally returns the
// credential WITHOUT a secret. Two-person credentials cannot be resolved to
// plaintext by a single holder — the executor must run the approval flow.
func (s *TwoPersonStorage) Retrieve(ctx context.Context, id string) (*Credential, error) {
	return s.Metadata(id)
}

// RetrieveForExecution mirrors Retrieve: no single-holder secret.
func (s *TwoPersonStorage) RetrieveForExecution(ctx context.Context, id string, projectID string) (*Credential, error) {
	return s.Metadata(id)
}

// Store satisfies the Storage interface for non-secret metadata updates only.
// To (re)seal a secret use SealAndStore. If a Secret is present here it is
// ignored to avoid ever persisting plaintext in this store.
func (s *TwoPersonStorage) Store(ctx context.Context, cred Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[cred.ID]
	if !ok {
		return fmt.Errorf("two-person credential %q not found; use SealAndStore to create", cred.ID)
	}
	rec.Name = cred.Name
	rec.AllowedTools = cred.AllowedTools
	rec.AllowedCommands = cred.AllowedCommands
	rec.AllowedHosts = cred.AllowedHosts
	rec.Tags = cred.Tags
	rec.ExpiresAt = cred.ExpiresAt
	rec.UpdatedAt = time.Now()
	s.records[cred.ID] = rec
	return s.save()
}

// Delete removes a two-person credential.
func (s *TwoPersonStorage) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[id]; !ok {
		return ErrNotFound
	}
	delete(s.records, id)
	return s.save()
}

// List lists two-person credentials matching filter. Secrets are never present.
func (s *TwoPersonStorage) List(ctx context.Context, filter CredentialFilter) ([]Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Credential
	for id, rec := range s.records {
		if filter.Kind != "" && rec.Kind != filter.Kind {
			continue
		}
		if filter.Scope != "" && rec.Scope != filter.Scope {
			continue
		}
		if filter.ProjectID != "" && rec.ProjectID != filter.ProjectID {
			continue
		}
		c := recordToCredential(id, rec)
		if !filter.IncludeExpired && c.IsExpired() {
			continue
		}
		if len(filter.Tags) > 0 && !hasAllTags(c.Tags, filter.Tags) {
			continue
		}
		result = append(result, *c)
	}
	return result, nil
}

// UpdateLastUsed updates the last-used timestamp.
func (s *TwoPersonStorage) UpdateLastUsed(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return ErrNotFound
	}
	now := time.Now()
	rec.UpdatedAt = now
	s.records[id] = rec
	return s.save()
}

// IsTwoPerson reports whether a credential id is stored as two-person.
func (s *TwoPersonStorage) IsTwoPerson(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.records[id]
	return ok
}

// GetPath returns the storage file path.
func (s *TwoPersonStorage) GetPath() string { return s.path }

// recordToCredential builds a Credential (Secret empty) from a record.
func recordToCredential(id string, rec twoPersonRecord) *Credential {
	return &Credential{
		ID:              id,
		Name:            rec.Name,
		Kind:            rec.Kind,
		Scope:           rec.Scope,
		ProjectID:       rec.ProjectID,
		AllowedTools:    rec.AllowedTools,
		AllowedCommands: rec.AllowedCommands,
		AllowedHosts:    rec.AllowedHosts,
		Tags:            rec.Tags,
		Inject:          rec.Inject,
		ExpiresAt:       rec.ExpiresAt,
		CreatedAt:       rec.CreatedAt,
		UpdatedAt:       rec.UpdatedAt,
	}
}

// hasAllTags reports whether have contains all of want.
func hasAllTags(have, want []string) bool {
	set := make(map[string]bool, len(have))
	for _, t := range have {
		set[t] = true
	}
	for _, t := range want {
		if !set[t] {
			return false
		}
	}
	return true
}

// TwoPersonStorageExists reports whether a two-person vault file exists.
func TwoPersonStorageExists(path string) bool {
	_, err := os.Stat(expandPath(path))
	return err == nil
}

// Ensure TwoPersonStorage satisfies the Storage interface.
var _ Storage = (*TwoPersonStorage)(nil)
