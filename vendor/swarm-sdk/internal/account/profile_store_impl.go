package account

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ProfileStoreImpl is the concrete implementation of ProfileStore
type ProfileStoreImpl struct {
	mu       sync.RWMutex
	config   ProfileStoreConfig
	profiles map[string]*StoredProfile // name -> profile
	default_ string                    // default profile name
	registry AccountRegistry           // for resolving accounts
	storage  ProfileStorage
}

// ProfileStorage handles file persistence for profiles
type ProfileStorage interface {
	// LoadProfiles loads profiles from storage
	LoadProfiles() (map[string]*StoredProfile, string, error)

	// SaveProfiles persists profiles to storage
	SaveProfiles(profiles map[string]*StoredProfile, defaultName string) error

	// DeleteProfile removes a profile from storage
	DeleteProfile(name string) error
}

// FileProfileStorage implements ProfileStorage with file persistence
type FileProfileStorage struct {
	filePath string
	mu       sync.RWMutex
}

// NewFileProfileStorage creates a new file-based profile storage
func NewFileProfileStorage(baseDir string) *FileProfileStorage {
	return &FileProfileStorage{
		filePath: filepath.Join(baseDir, "profiles.json"),
	}
}

// ProfilesData is the on-disk format
type ProfilesData struct {
	Version  string                    `json:"version"`
	Profiles map[string]*StoredProfile `json:"profiles"`
	Default  string                    `json:"default"`
	SavedAt  time.Time                 `json:"saved_at"`
}

// LoadProfiles loads profiles from disk
func (fps *FileProfileStorage) LoadProfiles() (map[string]*StoredProfile, string, error) {
	fps.mu.RLock()
	defer fps.mu.RUnlock()

	// If file doesn't exist, return empty
	if _, err := os.Stat(fps.filePath); os.IsNotExist(err) {
		return make(map[string]*StoredProfile), "", nil
	}

	data, err := os.ReadFile(fps.filePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read profiles file: %w", err)
	}

	var stored ProfilesData
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, "", fmt.Errorf("failed to parse profiles file: %w", err)
	}

	return stored.Profiles, stored.Default, nil
}

// SaveProfiles saves profiles to disk
func (fps *FileProfileStorage) SaveProfiles(profiles map[string]*StoredProfile, defaultName string) error {
	fps.mu.Lock()
	defer fps.mu.Unlock()

	stored := ProfilesData{
		Version:  "1.0",
		Profiles: profiles,
		Default:  defaultName,
		SavedAt:  time.Now(),
	}

	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal profiles: %w", err)
	}

	// Write atomically
	tmpPath := fps.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp profiles file: %w", err)
	}

	if err := os.Rename(tmpPath, fps.filePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename profiles file: %w", err)
	}

	return nil
}

// DeleteProfile removes a profile from disk
func (fps *FileProfileStorage) DeleteProfile(name string) error {
	fps.mu.Lock()
	defer fps.mu.Unlock()

	// For file storage, deletion is handled by SaveProfiles
	// This is just a marker for cleanup if needed
	return nil
}

// NewProfileStoreImpl creates a new profile store implementation
func NewProfileStoreImpl(config ProfileStoreConfig, registry AccountRegistry) (*ProfileStoreImpl, error) {
	if config.PersistenceDir == "" {
		return nil, fmt.Errorf("persistence directory required")
	}

	// Create directory if needed
	if err := os.MkdirAll(config.PersistenceDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create persistence directory: %w", err)
	}

	storage := NewFileProfileStorage(config.PersistenceDir)

	store := &ProfileStoreImpl{
		config:   config,
		profiles: make(map[string]*StoredProfile),
		registry: registry,
		storage:  storage,
	}

	// Load existing profiles
	if err := store.load(); err != nil {
		return nil, fmt.Errorf("failed to load profiles: %w", err)
	}

	// Create default profile if empty
	if len(store.profiles) == 0 {
		if err := store.initializeDefaults(); err != nil {
			return nil, fmt.Errorf("failed to initialize default profiles: %w", err)
		}
	}

	return store, nil
}

// load loads profiles from storage
func (ps *ProfileStoreImpl) load() error {
	profiles, defaultName, err := ps.storage.LoadProfiles()
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	ps.profiles = profiles
	ps.default_ = defaultName

	return nil
}

// initializeDefaults creates default profiles
func (ps *ProfileStoreImpl) initializeDefaults() error {
	// Create a basic default profile
	defaultProfile := &StoredProfile{
		Name:      "default",
		Provider:  "anthropic",
		AccountID: "", // Will be set by user during first login
		Model:     "claude-opus-4-20250514",
		IsDefault: true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Metadata:  make(map[string]any),
	}

	ps.profiles["default"] = defaultProfile
	ps.default_ = "default"

	return ps.storage.SaveProfiles(ps.profiles, ps.default_)
}

// CreateProfile creates a new profile
func (ps *ProfileStoreImpl) CreateProfile(name string, account AccountIdentity, model string) (AccountProfile, error) {
	if err := ValidateProfileName(name); err != nil {
		return nil, NewProfileStoreError(ErrorCodeInvalidProfileName, "invalid profile name", err)
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()

	// Check if already exists
	if _, exists := ps.profiles[name]; exists {
		return nil, NewProfileStoreError(ErrorCodeProfileAlreadyExists,
			fmt.Sprintf("profile already exists: %s", name), nil)
	}

	// Check duplicate account if not allowed
	if !ps.config.AllowDuplicateAccounts {
		for _, profile := range ps.profiles {
			if profile.Provider == account.Provider() && profile.AccountID == account.ID() {
				return nil, NewProfileStoreError(ErrorCodeDuplicateAccount,
					fmt.Sprintf("account already used in profile: %s", profile.Name), nil)
			}
		}
	}

	// Create profile
	now := time.Now()
	profile := &StoredProfile{
		Name:      name,
		Provider:  account.Provider(),
		AccountID: account.ID(),
		Model:     model,
		IsDefault: false,
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  make(map[string]any),
	}

	ps.profiles[name] = profile

	// Save
	if err := ps.storage.SaveProfiles(ps.profiles, ps.default_); err != nil {
		delete(ps.profiles, name)
		return nil, NewProfileStoreError(ErrorCodeStorageFailed, "failed to save profile", err)
	}

	// Convert to AccountProfile
	return profile.ToAccountProfile(ps.registry)
}

// GetProfile gets a profile
func (ps *ProfileStoreImpl) GetProfile(name string) (AccountProfile, error) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	profile, exists := ps.profiles[name]
	if !exists {
		return nil, NewProfileStoreError(ErrorCodeProfileNotFound,
			fmt.Sprintf("profile not found: %s", name), nil)
	}

	return profile.ToAccountProfile(ps.registry)
}

// ListProfiles lists all profiles
func (ps *ProfileStoreImpl) ListProfiles() ([]AccountProfile, error) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	// Sort by creation time
	var profiles []*StoredProfile
	for _, p := range ps.profiles {
		profiles = append(profiles, p)
	}

	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].CreatedAt.Before(profiles[j].CreatedAt)
	})

	// Convert to AccountProfiles
	var result []AccountProfile
	for _, sp := range profiles {
		if ap, err := sp.ToAccountProfile(ps.registry); err == nil {
			result = append(result, ap)
		}
	}

	return result, nil
}

// ListProfilesByProvider lists profiles for a provider
func (ps *ProfileStoreImpl) ListProfilesByProvider(provider string) ([]AccountProfile, error) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	var profiles []*StoredProfile
	for _, p := range ps.profiles {
		if p.Provider == provider {
			profiles = append(profiles, p)
		}
	}

	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].CreatedAt.Before(profiles[j].CreatedAt)
	})

	var result []AccountProfile
	for _, sp := range profiles {
		if ap, err := sp.ToAccountProfile(ps.registry); err == nil {
			result = append(result, ap)
		}
	}

	return result, nil
}

// UpdateProfile updates a profile
func (ps *ProfileStoreImpl) UpdateProfile(name string, account AccountIdentity, model string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	profile, exists := ps.profiles[name]
	if !exists {
		return NewProfileStoreError(ErrorCodeProfileNotFound,
			fmt.Sprintf("profile not found: %s", name), nil)
	}

	profile.Provider = account.Provider()
	profile.AccountID = account.ID()
	profile.Model = model
	profile.UpdatedAt = time.Now()

	if err := ps.storage.SaveProfiles(ps.profiles, ps.default_); err != nil {
		return NewProfileStoreError(ErrorCodeStorageFailed, "failed to save profile", err)
	}

	return nil
}

// DeleteProfile deletes a profile
func (ps *ProfileStoreImpl) DeleteProfile(name string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	profile, exists := ps.profiles[name]
	if !exists {
		return NewProfileStoreError(ErrorCodeProfileNotFound,
			fmt.Sprintf("profile not found: %s", name), nil)
	}

	// Cannot delete default profile
	if profile.IsDefault {
		return NewProfileStoreError(ErrorCodeCannotDeleteDefault,
			fmt.Sprintf("cannot delete default profile: %s", name), nil)
	}

	delete(ps.profiles, name)

	if err := ps.storage.SaveProfiles(ps.profiles, ps.default_); err != nil {
		ps.profiles[name] = profile
		return NewProfileStoreError(ErrorCodeStorageFailed, "failed to save profiles", err)
	}

	if err := ps.storage.DeleteProfile(name); err != nil {
		return NewProfileStoreError(ErrorCodeStorageFailed, "failed to delete profile", err)
	}

	return nil
}

// RenameProfile renames a profile
func (ps *ProfileStoreImpl) RenameProfile(oldName, newName string) error {
	if err := ValidateProfileName(newName); err != nil {
		return NewProfileStoreError(ErrorCodeInvalidProfileName, "invalid new name", err)
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()

	oldProfile, exists := ps.profiles[oldName]
	if !exists {
		return NewProfileStoreError(ErrorCodeProfileNotFound,
			fmt.Sprintf("profile not found: %s", oldName), nil)
	}

	// Check if new name exists
	if _, exists := ps.profiles[newName]; exists {
		return NewProfileStoreError(ErrorCodeProfileAlreadyExists,
			fmt.Sprintf("profile already exists: %s", newName), nil)
	}

	// Update
	oldProfile.Name = newName
	ps.profiles[newName] = oldProfile
	delete(ps.profiles, oldName)

	// Update default if it was the default
	if ps.default_ == oldName {
		ps.default_ = newName
	}

	if err := ps.storage.SaveProfiles(ps.profiles, ps.default_); err != nil {
		// Revert
		ps.profiles[oldName] = oldProfile
		delete(ps.profiles, newName)
		if ps.default_ == newName {
			ps.default_ = oldName
		}
		return NewProfileStoreError(ErrorCodeStorageFailed, "failed to save profiles", err)
	}

	return nil
}

// SetDefaultProfile sets the default profile
func (ps *ProfileStoreImpl) SetDefaultProfile(name string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	profile, exists := ps.profiles[name]
	if !exists {
		return NewProfileStoreError(ErrorCodeProfileNotFound,
			fmt.Sprintf("profile not found: %s", name), nil)
	}

	// Update is_default flags
	for _, p := range ps.profiles {
		p.IsDefault = false
	}
	profile.IsDefault = true

	ps.default_ = name

	if err := ps.storage.SaveProfiles(ps.profiles, ps.default_); err != nil {
		return NewProfileStoreError(ErrorCodeStorageFailed, "failed to save profiles", err)
	}

	return nil
}

// GetDefaultProfile gets the default profile
func (ps *ProfileStoreImpl) GetDefaultProfile() (AccountProfile, error) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	if ps.default_ == "" {
		return nil, NewProfileStoreError(ErrorCodeProfileNotFound,
			"no default profile set", nil)
	}

	profile, exists := ps.profiles[ps.default_]
	if !exists {
		return nil, NewProfileStoreError(ErrorCodeProfileNotFound,
			fmt.Sprintf("default profile not found: %s", ps.default_), nil)
	}

	return profile.ToAccountProfile(ps.registry)
}

// HasProfile checks if profile exists
func (ps *ProfileStoreImpl) HasProfile(name string) bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	_, exists := ps.profiles[name]
	return exists
}

// GetProfileCount returns profile count
func (ps *ProfileStoreImpl) GetProfileCount() int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	return len(ps.profiles)
}
