package findings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// HTTPHTTPSyncEngine handles bidirectional synchronization of findings with a remote server
type HTTPHTTPSyncEngine struct {
	endpoint string
	apiKey   string
	client   *http.Client
	cache    Cache

	// Configuration
	interval  time.Duration
	batchSize int
	enabled   bool

	// State
	mu       sync.RWMutex
	status   SyncStatus
	stopChan chan struct{}
	wg       sync.WaitGroup
}

// NewHTTPHTTPSyncEngine creates a new sync engine
func NewHTTPHTTPSyncEngine(cache Cache, endpoint, apiKey string) *HTTPHTTPSyncEngine {
	return &HTTPHTTPSyncEngine{
		endpoint:  endpoint,
		apiKey:    apiKey,
		cache:     cache,
		client:    &http.Client{Timeout: 30 * time.Second},
		interval:  5 * time.Minute,
		batchSize: 100,
		enabled:   endpoint != "",
		status:    SyncStatus{},
		stopChan:  make(chan struct{}),
	}
}

// SetInterval sets the sync interval
func (se *HTTPHTTPSyncEngine) SetInterval(interval time.Duration) {
	se.interval = interval
}

// SetBatchSize sets the batch size for sync operations
func (se *HTTPHTTPSyncEngine) SetBatchSize(size int) {
	se.batchSize = size
}

// Enable enables or disables syncing
func (se *HTTPHTTPSyncEngine) Enable(enabled bool) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.enabled = enabled
}

// Start begins background syncing
func (se *HTTPHTTPSyncEngine) Start(ctx context.Context) {
	if !se.enabled {
		return
	}

	se.wg.Add(1)
	go se.syncLoop(ctx)
}

// Stop stops background syncing
func (se *HTTPHTTPSyncEngine) Stop() {
	close(se.stopChan)
	se.wg.Wait()
}

// syncLoop runs the periodic sync
func (se *HTTPHTTPSyncEngine) syncLoop(ctx context.Context) {
	defer se.wg.Done()

	ticker := time.NewTicker(se.interval)
	defer ticker.Stop()

	// Sync immediately on start
	se.performSync(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-se.stopChan:
			return
		case <-ticker.C:
			se.performSync(ctx)
		}
	}
}

// performSync does a full bidirectional sync
func (se *HTTPHTTPSyncEngine) performSync(ctx context.Context) {
	se.mu.Lock()
	se.status.LastSync = time.Now()
	se.mu.Unlock()

	// Step 1: Push local findings to remote
	if err := se.push(ctx); err != nil {
		se.updateStatus(func(s *SyncStatus) {
			s.Error = err
		})
		return
	}

	// Step 2: Pull remote findings to local
	if err := se.pull(ctx); err != nil {
		se.updateStatus(func(s *SyncStatus) {
			s.Error = err
		})
		return
	}

	// Clear error on success
	se.updateStatus(func(s *SyncStatus) {
		s.Error = nil
		s.Connected = true
	})
}

// Push uploads local findings to remote
func (se *HTTPHTTPSyncEngine) push(ctx context.Context) error {
	// Query for findings that haven't been synced yet
	// In a real implementation, we'd track sync state per finding
	// For now, push recent findings
	query := FindingQuery{
		TimeRange: &TimeRange{
			Start: time.Now().Add(-se.interval),
			End:   time.Now().Add(time.Second),
		},
		Limit: se.batchSize,
	}

	results, err := se.cache.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to query local findings: %w", err)
	}

	if len(results) == 0 {
		return nil // Nothing to push
	}

	// Prepare batch
	findings := make([]Finding, len(results))
	for i, r := range results {
		findings[i] = r.Finding
	}

	// Build request
	payload := struct {
		Findings []Finding `json:"findings"`
		DeviceID string    `json:"device_id"`
	}{
		Findings: findings,
		DeviceID: "swarm-agent-001", // Should be configurable
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal findings: %w", err)
	}

	// Send to remote
	url := se.endpoint + "/api/v1/findings/push"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+se.apiKey)

	resp, err := se.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to push findings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("push failed with status: %d", resp.StatusCode)
	}

	// Update status
	se.updateStatus(func(s *SyncStatus) {
		s.PendingLocal = 0
	})

	return nil
}

// Pull downloads findings from remote
func (se *HTTPHTTPSyncEngine) pull(ctx context.Context) error {
	// Build request
	url := se.endpoint + "/api/v1/findings/pull"

	reqBody := struct {
		Since    time.Time `json:"since"`
		DeviceID string    `json:"device_id"`
	}{
		Since:    se.status.LastSync,
		DeviceID: "swarm-agent-001",
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+se.apiKey)

	resp, err := se.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to pull findings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pull failed with status: %d", resp.StatusCode)
	}

	// Parse response
	var response struct {
		Findings []Finding `json:"findings"`
		HasMore  bool      `json:"has_more"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	// Store pulled findings
	for _, finding := range response.Findings {
		// Mark as synced from remote
		finding.Metadata.Source = "synced"

		if err := se.cache.Write(ctx, finding); err != nil {
			// Log but continue - partial sync is better than no sync
			fmt.Printf("[sync] Failed to write pulled finding %s: %v\n", finding.FindingID, err)
		}
	}

	// Update status
	se.updateStatus(func(s *SyncStatus) {
		s.PendingRemote = 0
	})

	return nil
}

// Sync performs an immediate bidirectional sync
func (se *HTTPHTTPSyncEngine) Sync(ctx context.Context) error {
	if !se.isEnabled() {
		return fmt.Errorf("sync is disabled")
	}

	if err := se.push(ctx); err != nil {
		return fmt.Errorf("push failed: %w", err)
	}

	if err := se.pull(ctx); err != nil {
		return fmt.Errorf("pull failed: %w", err)
	}

	return nil
}

// GetStatus returns current sync status
func (se *HTTPHTTPSyncEngine) GetStatus() SyncStatus {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.status
}

// isEnabled returns whether sync is enabled
func (se *HTTPHTTPSyncEngine) isEnabled() bool {
	se.mu.RLock()
	defer se.mu.RUnlock()
	return se.enabled
}

// updateStatus updates the sync status
func (se *HTTPHTTPSyncEngine) updateStatus(fn func(*SyncStatus)) {
	se.mu.Lock()
	defer se.mu.Unlock()
	fn(&se.status)
}

// MockSyncServer is a mock server for testing sync
type MockSyncServer struct {
	findings map[string]Finding
	mu       sync.RWMutex
}

// NewMockSyncServer creates a mock sync server
func NewMockSyncServer() *MockSyncServer {
	return &MockSyncServer{
		findings: make(map[string]Finding),
	}
}

// HandlePush handles push requests
func (ms *MockSyncServer) HandlePush(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Findings []Finding `json:"findings"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ms.mu.Lock()
	for _, f := range req.Findings {
		ms.findings[f.FindingID] = f
	}
	ms.mu.Unlock()

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandlePull handles pull requests
func (ms *MockSyncServer) HandlePull(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Since time.Time `json:"since"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ms.mu.RLock()
	var findings []Finding
	for _, f := range ms.findings {
		if f.Timestamp.After(req.Since) {
			findings = append(findings, f)
		}
	}
	ms.mu.RUnlock()

	resp := struct {
		Findings []Finding `json:"findings"`
		HasMore  bool      `json:"has_more"`
	}{
		Findings: findings,
		HasMore:  false,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// SyncConfig provides configuration for sync
type SyncConfig struct {
	Endpoint  string
	APIKey    string
	Interval  time.Duration
	BatchSize int
	Enabled   bool
	AutoStart bool
}

// DefaultSyncConfig returns default sync configuration
func DefaultSyncConfig() SyncConfig {
	return SyncConfig{
		Interval:  5 * time.Minute,
		BatchSize: 100,
		Enabled:   false,
		AutoStart: false,
	}
}
