package envelope

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// EnvelopeStore provides persistent storage for envelopes with state preservation.
// Principle 3: Non-Destructive Transformation
type EnvelopeStore struct {
	mu        sync.RWMutex
	envelopes []*Envelope
	index     map[string]*Envelope
	sessionID string
	traceDir  string
	sequence  int
}

// NewEnvelopeStore creates a new store for a session
func NewEnvelopeStore(sessionID string) *EnvelopeStore {
	// Sanitize sessionID to prevent path traversal attacks
	// Replace path separators and other dangerous characters with safe alternatives
	safeSessionID := sanitizeSessionID(sessionID)

	traceDir := filepath.Join(os.TempDir(), "envelope-trace", safeSessionID)
	os.MkdirAll(traceDir, 0755)

	return &EnvelopeStore{
		envelopes: make([]*Envelope, 0),
		index:     make(map[string]*Envelope),
		sessionID: sessionID, // Keep original for reference
		traceDir:  traceDir,
	}
}

// sanitizeSessionID replaces dangerous characters to prevent path traversal
func sanitizeSessionID(id string) string {
	result := make([]rune, 0, len(id))
	for _, r := range id {
		switch r {
		case '/', '\\', '.', ':', '*', '?', '"', '<', '>', '|':
			result = append(result, '_')
		default:
			result = append(result, r)
		}
	}
	return string(result)
}

// Store adds an envelope to the store
func (s *EnvelopeStore) Store(env *Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sequence++
	env.Sequence = s.sequence
	env.SessionID = s.sessionID

	s.envelopes = append(s.envelopes, env)
	s.index[env.ID] = env

	if err := s.writeToDisk(env); err != nil {
		return fmt.Errorf("write to disk: %w", err)
	}
	return nil
}

// Get retrieves an envelope by ID
func (s *EnvelopeStore) Get(id string) (*Envelope, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	env, ok := s.index[id]
	if !ok {
		return nil, fmt.Errorf("envelope not found: %s", id)
	}
	return env, nil
}

// GetBySequence retrieves envelopes by sequence range
func (s *EnvelopeStore) BySequence(from, to int) []*Envelope {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Envelope
	for _, env := range s.envelopes {
		if env.Sequence >= from && env.Sequence <= to {
			result = append(result, env)
		}
	}
	return result
}

// GetAll returns all envelopes
func (s *EnvelopeStore) GetAll() []*Envelope {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Envelope, len(s.envelopes))
	copy(result, s.envelopes)
	return result
}

// GetRawJSON retrieves the original JSON for an envelope
func (s *EnvelopeStore) RawJSON(id string) (json.RawMessage, error) {
	env, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	return env.RawJSON, nil
}

// GetCanonical retrieves the canonical payload
func (s *EnvelopeStore) Canonical(id string) (*CanonicalPayload, error) {
	env, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	return env.Canonical, nil
}

// ReconstructProviderJSON reconstructs original provider JSON
func (s *EnvelopeStore) ReconstructProviderJSON(ids ...string) ([]json.RawMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]json.RawMessage, 0, len(ids))
	for _, id := range ids {
		env, ok := s.index[id]
		if !ok {
			return nil, fmt.Errorf("envelope not found: %s", id)
		}
		result = append(result, env.RawJSON)
	}
	return result, nil
}

// QueryByProvider returns all envelopes from a provider
func (s *EnvelopeStore) QueryByProvider(providerID ProviderID) []*Envelope {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Envelope
	for _, env := range s.envelopes {
		if env.ProviderID == providerID {
			result = append(result, env)
		}
	}
	return result
}

// QueryByEventType returns all envelopes of an event type
func (s *EnvelopeStore) QueryByEventType(eventType EventType) []*Envelope {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Envelope
	for _, env := range s.envelopes {
		if env.EventType == eventType {
			result = append(result, env)
		}
	}
	return result
}

func (s *EnvelopeStore) writeToDisk(env *Envelope) error {
	filename := filepath.Join(s.traceDir, fmt.Sprintf("%04d_%s_%s.json",
		env.Sequence, env.ProviderID, env.EventType))

	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("write file %s: %w", filename, err)
	}
	return nil
}

// WriteNDJSON writes all envelopes as NDJSON
func (s *EnvelopeStore) WriteNDJSON() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filename := filepath.Join(s.traceDir, fmt.Sprintf("complete_trace_%s.ndjson",
		time.Now().Format("20060102_150405")))

	file, err := os.Create(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()

	for _, env := range s.envelopes {
		data, err := json.Marshal(env)
		if err != nil {
			return "", fmt.Errorf("marshal envelope: %w", err)
		}
		if _, err := file.WriteString(string(data) + "\n"); err != nil {
			return "", fmt.Errorf("write to file: %w", err)
		}
	}

	return filename, nil
}

// GetTraceDir returns the trace directory
func (s *EnvelopeStore) TraceDir() string {
	return s.traceDir
}

// Count returns the number of stored envelopes
func (s *EnvelopeStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.envelopes)
}

// Summary returns a summary
func (s *EnvelopeStore) Summary() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	providerCounts := make(map[ProviderID]int)
	eventCounts := make(map[EventType]int)

	for _, env := range s.envelopes {
		providerCounts[env.ProviderID]++
		eventCounts[env.EventType]++
	}

	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("Session: %s\nTotal: %d\nTrace: %s\n\nBy Provider:\n",
		s.sessionID, len(s.envelopes), s.traceDir))

	for p, c := range providerCounts {
		summary.WriteString(fmt.Sprintf("  %s: %d\n", p, c))
	}

	summary.WriteString("\nBy Event:\n")
	for e, c := range eventCounts {
		summary.WriteString(fmt.Sprintf("  %s: %d\n", e, c))
	}

	return summary.String()
}
