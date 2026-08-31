package vault

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TransparentCredential is the on-disk format for the transparent vault.
// Values are stored in clear text for the trusted local vault.
type TransparentCredential struct {
	Kind            CredentialKind  `json:"kind"`
	Value           string          `json:"value"`
	AllowedTools    []string        `json:"allowedTools,omitempty"`
	AllowedCommands []string        `json:"allowedCommands,omitempty"`
	AllowedHosts    []string        `json:"allowedHosts,omitempty"`
	Tags            []string        `json:"tags,omitempty"`
	InjectMethod    InjectMethod    `json:"injectMethod,omitempty"`
	InjectTarget    string          `json:"injectTarget,omitempty"`
	Scope           CredentialScope `json:"scope,omitempty"`
	ProjectID       string          `json:"projectId,omitempty"`
	ExpiresAt       *time.Time      `json:"expiresAt,omitempty"`
}

// transparentDiskFormat is the top-level JSON structure.
type transparentDiskFormat struct {
	Version     string                           `json:"version"`
	UpdatedAt   time.Time                        `json:"updatedAt"`
	Credentials map[string]TransparentCredential `json:"credentials"`
}

// TransparentStorage provides auto-load credential storage without passwords.
// Values are intentionally stored in clear text for trusted local environments.
type TransparentStorage struct {
	path  string
	creds map[string]*Credential
	mu    sync.RWMutex
}

const transparentVersion = "2"

// NewTransparentStorage creates or loads a transparent credential store.
func NewTransparentStorage(path string) (*TransparentStorage, error) {
	s := &TransparentStorage{
		path:  expandPath(path),
		creds: make(map[string]*Credential),
	}

	if _, err := os.Stat(s.path); err == nil {
		if err := s.load(); err != nil {
			return nil, fmt.Errorf("failed to load transparent vault: %w", err)
		}
	}

	return s, nil
}

// load reads both the legacy base64 format and the current cleartext format.
func (s *TransparentStorage) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	var disk transparentDiskFormat
	if err := json.Unmarshal(data, &disk); err != nil {
		return fmt.Errorf("invalid transparent vault format: %w", err)
	}

	if disk.Version != "1" && disk.Version != transparentVersion {
		return fmt.Errorf("unsupported transparent vault version: %s", disk.Version)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for id, tc := range disk.Credentials {
		secret := []byte(tc.Value)
		if disk.Version == "1" {
			decoded, err := base64.StdEncoding.DecodeString(tc.Value)
			if err != nil {
				return fmt.Errorf("credential %s: invalid legacy base64 value: %w", id, err)
			}
			secret = decoded
		}

		inject := InjectConfig{
			Method: tc.InjectMethod,
			Target: tc.InjectTarget,
		}
		if inject.Method == "" {
			inject.Method = InjectEnv
		}
		if tc.Kind == CredentialKindSSHKey {
			inject.Method = InjectFile
		}

		cred := &Credential{
			ID:              id,
			Kind:            tc.Kind,
			Secret:          string(secret),
			Scope:           tc.Scope,
			ProjectID:       tc.ProjectID,
			AllowedTools:    tc.AllowedTools,
			AllowedCommands: tc.AllowedCommands,
			AllowedHosts:    tc.AllowedHosts,
			Tags:            tc.Tags,
			Inject:          inject,
			ExpiresAt:       tc.ExpiresAt,
			CreatedAt:       disk.UpdatedAt,
			UpdatedAt:       disk.UpdatedAt,
		}
		s.creds[id] = cred
	}

	return nil
}

// saveLocked writes credentials to disk with base64 obfuscation.
// The caller must hold s.mu for writing so the snapshot and atomic replacement
// are serialized with mutations. Taking a nested read lock here would deadlock
// because sync.RWMutex is not reentrant.
func (s *TransparentStorage) saveLocked() error {
	disk := transparentDiskFormat{
		Version:     transparentVersion,
		UpdatedAt:   time.Now(),
		Credentials: make(map[string]TransparentCredential),
	}

	for id, c := range s.creds {
		disk.Credentials[id] = TransparentCredential{
			Kind:            c.Kind,
			Value:           c.Secret,
			AllowedTools:    c.AllowedTools,
			AllowedCommands: c.AllowedCommands,
			AllowedHosts:    c.AllowedHosts,
			Tags:            c.Tags,
			InjectMethod:    c.Inject.Method,
			InjectTarget:    c.Inject.Target,
			Scope:           c.Scope,
			ProjectID:       c.ProjectID,
			ExpiresAt:       c.ExpiresAt,
		}
	}

	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	// Write atomically
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}

	return os.Rename(tmpPath, s.path)
}

// Store stores a credential.
func (s *TransparentStorage) Store(ctx context.Context, cred Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	cred.CreatedAt = now
	cred.UpdatedAt = now

	c := cred
	s.creds[cred.ID] = &c

	return s.saveLocked()
}

// Retrieve retrieves a credential by ID.
func (s *TransparentStorage) Retrieve(ctx context.Context, id string) (*Credential, error) {
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
func (s *TransparentStorage) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.creds[id]; !ok {
		return ErrNotFound
	}

	delete(s.creds, id)
	return s.saveLocked()
}

// List lists credentials matching filter.
func (s *TransparentStorage) List(ctx context.Context, filter CredentialFilter) ([]Credential, error) {
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
		if filter.ProjectID != "" && cred.ProjectID != filter.ProjectID {
			continue
		}
		if !filter.IncludeExpired && cred.IsExpired() {
			continue
		}
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
		if !filter.IncludeSecrets {
			c.Secret = ""
		}
		result = append(result, c)
	}

	return result, nil
}

// RetrieveForExecution retrieves a credential for execution.
func (s *TransparentStorage) RetrieveForExecution(ctx context.Context, id string, projectID string) (*Credential, error) {
	return s.Retrieve(ctx, id)
}

// UpdateLastUsed updates the last used timestamp.
func (s *TransparentStorage) UpdateLastUsed(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cred, ok := s.creds[id]
	if !ok {
		return ErrNotFound
	}
	now := time.Now()
	cred.LastUsedAt = &now

	return s.saveLocked()
}

// GetPath returns the storage file path.
func (s *TransparentStorage) GetPath() string {
	return s.path
}

// TransparentStorageExists returns true if a transparent vault file exists at the given path.
func TransparentStorageExists(path string) bool {
	_, err := os.Stat(expandPath(path))
	return err == nil
}
