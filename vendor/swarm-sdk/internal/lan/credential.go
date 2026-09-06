package lan

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

const (
	credentialFileVersion  = 1
	maxCredentialFileBytes = 1 << 20
)

var (
	ErrCredentialStorage = errors.New("lan: credential storage is not secure")
	ErrCredentialSchema  = errors.New("lan: unsupported credential storage schema")
	ErrNoValidCredential = errors.New("lan: no valid provisioned credential")
)

// CredentialState is the closed result vocabulary returned by an authority.
type CredentialState string

const (
	CredentialAbsent  CredentialState = "absent"
	CredentialInvalid CredentialState = "invalid"
	CredentialExpired CredentialState = "expired"
	CredentialRevoked CredentialState = "revoked"
	CredentialValid   CredentialState = "valid"
)

// CredentialBinding is non-secret evidence attached to an authenticated
// request. It deliberately contains no reusable credential material.
type CredentialBinding struct {
	ID        string
	Revision  uint64
	ExpiresAt time.Time
}

// CredentialDecision is the result of authenticating one presented secret.
type CredentialDecision struct {
	State   CredentialState
	Binding CredentialBinding
}

// CredentialRecord is one provisioned revision. OverlapUntil is meaningful
// only after a newer revision with the same ID exists.
type CredentialRecord struct {
	ID           string    `json:"id"`
	Secret       string    `json:"secret"`
	Revision     uint64    `json:"revision"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
	Revoked      bool      `json:"revoked,omitempty"`
	OverlapUntil time.Time `json:"overlapUntil,omitempty"`
}

type credentialFile struct {
	Version     int                `json:"version"`
	Credentials []CredentialRecord `json:"credentials"`
}

// CredentialAuthority is an opaque, concurrency-safe provisioned credential
// owner. Production instances come from LoadCredentialAuthority. Tests and
// embedding composition roots may use NewMemoryCredentialAuthority; callers
// cannot manufacture a valid authority by setting a boolean or raw token in
// gateway options.
type CredentialAuthority struct {
	mu      sync.RWMutex
	records []CredentialRecord
	loadErr error
	path    string
	changed chan struct{}
}

// LoadCredentialAuthority securely opens and validates an owner-only,
// no-symlink, versioned credential file. No credential is returned on any
// storage, parse, schema, or provisioned-state failure.
func LoadCredentialAuthority(path string, now time.Time) (*CredentialAuthority, error) {
	records, err := loadCredentialRecords(path)
	if err != nil {
		return nil, err
	}
	a := newCredentialAuthority(records)
	a.path = path
	if err := a.Ready(now); err != nil {
		return nil, err
	}
	return a, nil
}

// NewMemoryCredentialAuthority creates an authority without filesystem I/O.
// It exists as an injected test/composition seam; records receive the same
// schema validation as securely loaded records.
func NewMemoryCredentialAuthority(records []CredentialRecord) (*CredentialAuthority, error) {
	if err := validateCredentialRecords(records); err != nil {
		return nil, err
	}
	return newCredentialAuthority(records), nil
}

func newCredentialAuthority(records []CredentialRecord) *CredentialAuthority {
	copied := append([]CredentialRecord(nil), records...)
	sort.Slice(copied, func(i, j int) bool {
		if copied[i].ID == copied[j].ID {
			return copied[i].Revision > copied[j].Revision
		}
		return copied[i].ID < copied[j].ID
	})
	return &CredentialAuthority{records: copied, changed: make(chan struct{})}
}

// Ready proves that the authority was loaded successfully and currently has
// at least one unrevoked, unexpired newest credential revision.
func (a *CredentialAuthority) Ready(now time.Time) error {
	if a == nil {
		return ErrNoValidCredential
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.loadErr != nil {
		return a.loadErr
	}
	for _, r := range newestRecords(a.records) {
		if !r.Revoked && (r.ExpiresAt.IsZero() || now.Before(r.ExpiresAt)) {
			return nil
		}
	}
	return ErrNoValidCredential
}

// Authenticate compares the presented secret in constant time against every
// provisioned revision. A matching old revision remains valid only during its
// explicit bounded overlap.
func (a *CredentialAuthority) Authenticate(presented string, now time.Time) CredentialDecision {
	if presented == "" {
		return CredentialDecision{State: CredentialAbsent}
	}
	if a == nil {
		return CredentialDecision{State: CredentialInvalid}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.loadErr != nil {
		return CredentialDecision{State: CredentialInvalid}
	}
	var match *CredentialRecord
	for i := range a.records {
		r := &a.records[i]
		equal := len(presented) == len(r.Secret) &&
			subtle.ConstantTimeCompare([]byte(presented), []byte(r.Secret)) == 1
		if equal && match == nil {
			copy := *r
			match = &copy
		}
	}
	if match == nil {
		return CredentialDecision{State: CredentialInvalid}
	}
	return decisionForRecord(a.records, *match, now)
}

// ValidateBinding revalidates an already-open request/stream against current
// authority state. Revocation, expiry, removal, or rotation beyond overlap
// invalidates the binding.
func (a *CredentialAuthority) ValidateBinding(binding CredentialBinding, now time.Time) CredentialState {
	return a.BindingDecision(binding, now).State
}

// BindingDecision is ValidateBinding plus the binding's newly effective
// expiry. Rotation can shorten an old revision's authority to OverlapUntil;
// stream owners use this result to re-arm their exact cancellation deadline.
func (a *CredentialAuthority) BindingDecision(binding CredentialBinding, now time.Time) CredentialDecision {
	if a == nil || binding.ID == "" || binding.Revision == 0 {
		return CredentialDecision{State: CredentialInvalid}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.loadErr != nil {
		return CredentialDecision{State: CredentialInvalid}
	}
	for _, r := range a.records {
		if r.ID == binding.ID && r.Revision == binding.Revision {
			return decisionForRecord(a.records, r, now)
		}
	}
	return CredentialDecision{State: CredentialRevoked, Binding: binding}
}

// Changes returns a generation channel that is closed whenever authority
// state changes. Callers fetch Changes again after a non-invalidating update.
func (a *CredentialAuthority) Changes() <-chan struct{} {
	if a == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.changed
}

// Replace atomically updates an in-memory authority and notifies active
// request bindings. It is primarily the deterministic rotation/revocation
// seam used by tests and provisioning owners.
func (a *CredentialAuthority) Replace(records []CredentialRecord) error {
	if a == nil {
		return ErrNoValidCredential
	}
	if err := validateCredentialRecords(records); err != nil {
		return err
	}
	a.replace(records, nil)
	return nil
}

func (a *CredentialAuthority) replace(records []CredentialRecord, loadErr error) {
	copied := append([]CredentialRecord(nil), records...)
	sort.Slice(copied, func(i, j int) bool {
		if copied[i].ID == copied[j].ID {
			return copied[i].Revision > copied[j].Revision
		}
		return copied[i].ID < copied[j].ID
	})
	a.mu.Lock()
	a.records = copied
	a.loadErr = loadErr
	close(a.changed)
	a.changed = make(chan struct{})
	a.mu.Unlock()
}

// Watch reloads the securely opened file at interval. Any reload failure
// immediately makes the authority unusable and notifies active streams; a
// later valid reload can recover it without replacing the listener process.
func (a *CredentialAuthority) Watch(ctxDone <-chan struct{}, interval time.Duration) {
	if a == nil || a.path == "" || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctxDone:
			return
		case <-ticker.C:
			records, err := loadCredentialRecords(a.path)
			if err != nil {
				a.replace(nil, err)
				continue
			}
			a.replace(records, nil)
		}
	}
}

func decisionForRecord(records []CredentialRecord, r CredentialRecord, now time.Time) CredentialDecision {
	binding := CredentialBinding{ID: r.ID, Revision: r.Revision, ExpiresAt: r.ExpiresAt}
	if r.Revoked {
		return CredentialDecision{State: CredentialRevoked, Binding: binding}
	}
	if !r.ExpiresAt.IsZero() && !now.Before(r.ExpiresAt) {
		return CredentialDecision{State: CredentialExpired, Binding: binding}
	}
	var newest uint64
	for _, candidate := range records {
		if candidate.ID == r.ID && candidate.Revision > newest {
			newest = candidate.Revision
		}
	}
	if r.Revision != newest {
		if r.OverlapUntil.IsZero() || !now.Before(r.OverlapUntil) {
			return CredentialDecision{State: CredentialRevoked, Binding: binding}
		}
		if binding.ExpiresAt.IsZero() || r.OverlapUntil.Before(binding.ExpiresAt) {
			binding.ExpiresAt = r.OverlapUntil
		}
	}
	return CredentialDecision{State: CredentialValid, Binding: binding}
}

func newestRecords(records []CredentialRecord) []CredentialRecord {
	seen := make(map[string]bool)
	out := make([]CredentialRecord, 0, len(records))
	for _, r := range records {
		if !seen[r.ID] {
			seen[r.ID] = true
			out = append(out, r)
		}
	}
	return out
}

func validateCredentialRecords(records []CredentialRecord) error {
	if len(records) == 0 {
		return fmt.Errorf("%w: empty credential set", ErrCredentialSchema)
	}
	seen := make(map[string]struct{}, len(records))
	for _, r := range records {
		if r.ID == "" || r.Secret == "" || r.Revision == 0 {
			return fmt.Errorf("%w: credential id, secret, and revision are required", ErrCredentialSchema)
		}
		key := fmt.Sprintf("%s\x00%d", r.ID, r.Revision)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("%w: duplicate credential revision", ErrCredentialSchema)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func loadCredentialRecords(path string) ([]CredentialRecord, error) {
	f, err := openSecureCredentialFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, maxCredentialFileBytes+1))
	dec.DisallowUnknownFields()
	var stored credentialFile
	if err := dec.Decode(&stored); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON", ErrCredentialSchema)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return nil, err
	}
	if stored.Version != credentialFileVersion {
		return nil, fmt.Errorf("%w: version %d", ErrCredentialSchema, stored.Version)
	}
	if err := validateCredentialRecords(stored.Credentials); err != nil {
		return nil, err
	}
	return stored.Credentials, nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	err := dec.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return fmt.Errorf("%w: trailing data", ErrCredentialSchema)
}
