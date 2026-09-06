package runtime

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/chrome/protocol"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

const (
	StateVersion      = 1
	CredentialVersion = 1
	maxRecordString   = 512
	maxClaims         = 256
	maxStoreFileBytes = 4 << 20
)

var (
	ErrCorruptStore   = errors.New("chrome runtime: corrupt authority store")
	ErrEpochExhausted = errors.New("chrome runtime: manager epoch exhausted")
)

// Paths makes all process-wide authority locations explicit and injectable.
type Paths struct {
	Lock       string
	State      string
	Credential string
}

// DefaultPaths returns the dedicated user-only Chrome authority locations.
func DefaultPaths() Paths {
	return Paths{
		Lock:       paths.In("chrome", "authority.lock"),
		State:      paths.In("chrome", "authority.json"),
		Credential: paths.In("chrome", "installation.json"),
	}
}

// Claim is non-secret recovery metadata. It intentionally cannot represent a
// capability, request, page content, or browsing result.
type Claim struct {
	FamilyHash     string    `json:"family_hash"`
	Generation     uint64    `json:"generation"`
	ClaimID        string    `json:"claim_id"`
	InstallationID string    `json:"installation_id"`
	Lifecycle      string    `json:"lifecycle"`
	CloseEpoch     uint64    `json:"close_epoch,omitempty"`
	ClosePhase     string    `json:"close_phase,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type State struct {
	Version      int     `json:"version"`
	ManagerEpoch uint64  `json:"manager_epoch"`
	Claims       []Claim `json:"claims"`
}

// InstallationCredential is kept in a file separate from non-secret state.
type InstallationCredential struct {
	Version        int    `json:"version"`
	InstallationID string `json:"installation_id"`
	Secret         string `json:"secret"`
	LoopbackPort   uint16 `json:"loopback_port"`
}

type Store struct {
	paths Paths
	mu    sync.Mutex
}

func NewStore(paths Paths) (*Store, error) {
	var err error
	paths.Lock, err = cleanPath(paths.Lock)
	if err != nil {
		return nil, err
	}
	paths.State, err = cleanPath(paths.State)
	if err != nil {
		return nil, err
	}
	paths.Credential, err = cleanPath(paths.Credential)
	if err != nil {
		return nil, err
	}
	if paths.Lock == paths.State || paths.Lock == paths.Credential || paths.State == paths.Credential {
		return nil, fmt.Errorf("%w: lock, state, and credential paths must be distinct", ErrInsecureStorage)
	}
	return &Store{paths: paths}, nil
}

func (s *Store) Paths() Paths { return s.paths }

func (s *Store) Acquire() (*HostLock, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: nil store", ErrInsecureStorage)
	}
	return AcquireHostLock(s.paths.Lock)
}

// LoadState strictly loads non-secret state. A missing file returns a valid
// empty version-1 state.
func (s *Store) LoadState() (State, error) {
	var state State
	err := s.load(s.paths.State, &state)
	if errors.Is(err, os.ErrNotExist) {
		return State{Version: StateVersion, Claims: []Claim{}}, nil
	}
	if err != nil {
		return State{}, err
	}
	if err := state.validate(); err != nil {
		return State{}, err
	}
	return state, nil
}

// SaveState atomically persists state while preserving manager epoch
// monotonicity. The caller must hold this Store's lifetime host lock.
func (s *Store) SaveState(lock *HostLock, state State) error {
	if s == nil {
		return ErrLockNotHeld
	}
	return lock.withHeld(s.paths.Lock, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := state.validate(); err != nil {
			return err
		}
		current, err := s.LoadState()
		if err != nil {
			return err
		}
		if state.ManagerEpoch < current.ManagerEpoch {
			return fmt.Errorf("%w: manager epoch would decrease from %d to %d", ErrCorruptStore, current.ManagerEpoch, state.ManagerEpoch)
		}
		return s.write(s.paths.State, state)
	})
}

// AdvanceEpoch atomically loads, increments, and persists manager_epoch under
// the lifetime lock, returning the new epoch and complete state.
func (s *Store) AdvanceEpoch(lock *HostLock) (State, error) {
	if s == nil {
		return State{}, ErrLockNotHeld
	}
	var state State
	err := lock.withHeld(s.paths.Lock, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		var err error
		state, err = s.LoadState()
		if err != nil {
			return err
		}
		if state.ManagerEpoch >= protocol.MaxSafeInteger {
			return ErrEpochExhausted
		}
		state.ManagerEpoch++
		return s.write(s.paths.State, state)
	})
	if err != nil {
		return State{}, err
	}
	return state, nil
}

func (s *Store) LoadCredential() (InstallationCredential, error) {
	var credential InstallationCredential
	if err := s.load(s.paths.Credential, &credential); err != nil {
		return InstallationCredential{}, err
	}
	if err := credential.validate(); err != nil {
		return InstallationCredential{}, err
	}
	return credential, nil
}

func (s *Store) SaveCredential(lock *HostLock, credential InstallationCredential) error {
	if s == nil {
		return ErrLockNotHeld
	}
	return lock.withHeld(s.paths.Lock, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := credential.validate(); err != nil {
			return err
		}
		return s.write(s.paths.Credential, credential)
	})
}

func (s *Store) load(path string, dst any) error {
	if s == nil {
		return fmt.Errorf("%w: nil store", ErrInsecureStorage)
	}
	data, err := readPlatformFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("%w: decode %s: %v", ErrCorruptStore, filepath.Base(path), err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing JSON in %s", ErrCorruptStore, filepath.Base(path))
	}
	return nil
}

func (s *Store) write(path string, value any) error {
	if err := secureParent(path); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("chrome runtime: encode store: %w", err)
	}
	data = append(data, '\n')
	if err := atomicfile.Write(path, data); err != nil {
		return fmt.Errorf("chrome runtime: persist %s: %w", filepath.Base(path), err)
	}
	if err := protectPlatformFile(path); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func (s State) validate() error {
	if s.Version != StateVersion {
		return fmt.Errorf("%w: state version %d is unsupported", ErrCorruptStore, s.Version)
	}
	if s.ManagerEpoch > protocol.MaxSafeInteger {
		return fmt.Errorf("%w: manager epoch exceeds JavaScript-safe wire range", ErrCorruptStore)
	}
	if len(s.Claims) > maxClaims {
		return fmt.Errorf("%w: too many claims", ErrCorruptStore)
	}
	seen := make(map[string]struct{}, len(s.Claims))
	for i, claim := range s.Claims {
		if err := claim.validate(); err != nil {
			return fmt.Errorf("%w: claim %d: %v", ErrCorruptStore, i, err)
		}
		if _, exists := seen[claim.ClaimID]; exists {
			return fmt.Errorf("%w: duplicate claim_id", ErrCorruptStore)
		}
		seen[claim.ClaimID] = struct{}{}
	}
	return nil
}

func (c Claim) validate() error {
	if len(c.FamilyHash) != 64 || strings.ToLower(c.FamilyHash) != c.FamilyHash {
		return errors.New("family_hash must be lowercase SHA-256 hex")
	}
	if _, err := hex.DecodeString(c.FamilyHash); err != nil {
		return errors.New("family_hash must be lowercase SHA-256 hex")
	}
	if c.Generation == 0 {
		return errors.New("generation must be positive")
	}
	if !validToken(c.ClaimID) || !validToken(c.InstallationID) {
		return errors.New("invalid claim or installation identity")
	}
	switch c.Lifecycle {
	case "claiming", "open", "closed", "quarantined":
		if c.CloseEpoch != 0 || c.ClosePhase != "" {
			return errors.New("close metadata is only valid while closing")
		}
	case "closing":
		if c.CloseEpoch == 0 || (c.ClosePhase != "close_sent" && c.ClosePhase != "close_committed") {
			return errors.New("closing claim requires a valid close epoch and phase")
		}
	default:
		return errors.New("invalid lifecycle")
	}
	if c.UpdatedAt.IsZero() || c.UpdatedAt.Location() != time.UTC {
		return errors.New("updated_at must be a non-zero UTC timestamp")
	}
	return nil
}

func (c InstallationCredential) validate() error {
	if c.Version != CredentialVersion {
		return fmt.Errorf("%w: credential version %d is unsupported", ErrCorruptStore, c.Version)
	}
	if !validURLToken(c.InstallationID) || !validURLToken(c.Secret) || len(c.Secret) < 32 {
		return fmt.Errorf("%w: invalid installation credential", ErrCorruptStore)
	}
	if c.LoopbackPort == 0 {
		return fmt.Errorf("%w: invalid loopback port", ErrCorruptStore)
	}
	return nil
}

func validURLToken(value string) bool {
	if !validToken(value) {
		return false
	}
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			continue
		}
		return false
	}
	return true
}
func validToken(value string) bool {
	return value != "" && len(value) <= maxRecordString && value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, "\x00\r\n\t ")
}
