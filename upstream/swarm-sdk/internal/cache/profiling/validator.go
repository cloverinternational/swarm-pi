package profiling

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Validator implements hash-based cache stability validation.
type Validator struct {
	mu              sync.RWMutex
	messageHashes   map[string][]MessageHashEntry // conversationID -> history
	cacheBreakEvent map[string][]*CacheBreakEvent // conversationID -> breaks
	metrics         map[string]*CacheMetrics      // conversationID -> metrics
}

// NewValidator creates a new cache validator.
func NewValidator() *Validator {
	return &Validator{
		messageHashes:   make(map[string][]MessageHashEntry),
		cacheBreakEvent: make(map[string][]*CacheBreakEvent),
		metrics:         make(map[string]*CacheMetrics),
	}
}

// ComputeMessageHash computes a stable SHA256 hash of messages.
func ComputeMessageHash(messages any) (string, error) {
	// Convert to JSON with stable key ordering
	data, err := json.Marshal(messages)
	if err != nil {
		return "", fmt.Errorf("failed to marshal messages: %w", err)
	}

	// Compute SHA256
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// BeforeTranslation computes hash of messages before translation.
func (v *Validator) BeforeTranslation(ctx context.Context, messages any) (string, error) {
	if messages == nil {
		return "", nil
	}

	hash, err := ComputeMessageHash(messages)
	if err != nil {
		// Don't block on hash error
		return "", nil
	}

	return hash, nil
}

// AfterTranslation validates that translated JSON is stable.
func (v *Validator) AfterTranslation(ctx context.Context, preHash string, translatedJSON any) error {
	if preHash == "" || translatedJSON == nil {
		return nil
	}

	// For now, just validate that marshaling works
	// Full diff detection will happen when we have both the pre and post state
	_, err := json.Marshal(translatedJSON)
	if err != nil {
		return fmt.Errorf("translated JSON is not marshable: %w", err)
	}

	return nil
}

// RecordMessageHash stores a message hash for later comparison.
func (v *Validator) RecordMessageHash(conversationID string, turnNumber int, messages any) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	hash, err := ComputeMessageHash(messages)
	if err != nil {
		return err
	}

	// Store hash entry
	entry := MessageHashEntry{
		TurnNumber:   turnNumber,
		Timestamp:    time.Now(),
		Hash:         hash,
		JSONByteSize: estimateJSONSize(messages),
	}

	v.messageHashes[conversationID] = append(v.messageHashes[conversationID], entry)
	return nil
}

// CompareTurns detects cache breaks between consecutive turns.
func (v *Validator) CompareTurns(conversationID string, turnNumber int, messages any) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Ensure metrics exist for this conversation
	if _, ok := v.metrics[conversationID]; !ok {
		v.metrics[conversationID] = &CacheMetrics{
			ConversationID:   conversationID,
			StartTime:        time.Now(),
			BreaksByType:     make(map[string]int),
			BreaksBySeverity: make(map[string]int),
		}
	}

	history, exists := v.messageHashes[conversationID]
	if !exists || len(history) == 0 {
		// First turn, record it
		hash, err := ComputeMessageHash(messages)
		if err != nil {
			return err
		}
		entry := MessageHashEntry{
			TurnNumber:   turnNumber,
			Timestamp:    time.Now(),
			Hash:         hash,
			JSONByteSize: estimateJSONSize(messages),
		}
		v.messageHashes[conversationID] = append(v.messageHashes[conversationID], entry)
		return nil
	}

	// Get previous turn's hash
	prevEntry := history[len(history)-1]
	if prevEntry.TurnNumber >= turnNumber {
		// Out of order or duplicate turn
		return nil
	}

	// Compute current hash
	currentHash, err := ComputeMessageHash(messages)
	if err != nil {
		return err
	}

	// Compare
	if currentHash != prevEntry.Hash {
		// Cache break detected
		break_event := &CacheBreakEvent{
			ID:             fmt.Sprintf("break-%d-%d", time.Now().UnixNano(), len(history)),
			Timestamp:      time.Now(),
			ConversationID: conversationID,
			TurnNumber:     turnNumber,
			EventType:      "CACHE_BREAK_DETECTED",
			BreakType:      string(BreakContentMutated),
			Severity:       string(SeverityMedium),
			Description:    "Message content changed between turns",
			BeforeHash:     prevEntry.Hash,
			AfterHash:      currentHash,
			DiffType:       string(DiffModified),
			DiffSize:       int64(prevEntry.JSONByteSize - estimateJSONSize(messages)),
		}

		v.cacheBreakEvent[conversationID] = append(v.cacheBreakEvent[conversationID], break_event)

		// Update metrics
		metrics := v.metrics[conversationID]
		metrics.CacheBreaks++
		metrics.BreaksByType[break_event.BreakType]++
		metrics.BreaksBySeverity[break_event.Severity]++
	}

	// Record current hash
	entry := MessageHashEntry{
		TurnNumber:   turnNumber,
		Timestamp:    time.Now(),
		Hash:         currentHash,
		JSONByteSize: estimateJSONSize(messages),
	}
	v.messageHashes[conversationID] = append(v.messageHashes[conversationID], entry)

	return nil
}

// TrackMutation records a message mutation event.
func (v *Validator) TrackMutation(ctx context.Context, event *MutationEvent) error {
	// For Phase 1, we just acknowledge the interface
	// Phase 3 will implement full mutation tracking
	return nil
}

// RecordAPIResponse records cache metrics from an API response.
func (v *Validator) RecordAPIResponse(ctx context.Context, metrics *APIMetrics) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if _, ok := v.metrics[metrics.Provider]; !ok {
		v.metrics[metrics.Provider] = &CacheMetrics{
			StartTime: time.Now(),
		}
	}

	m := v.metrics[metrics.Provider]
	m.TotalAPICalls++
	m.TotalInputTokens += metrics.RequestTokens
	m.TotalOutputTokens += metrics.OutputTokens
	m.CacheCreatedTokens += metrics.CacheCreationTokens
	m.CacheReadTokens += metrics.CacheReadTokens

	if metrics.CacheHitDetected {
		m.CacheHits++
	} else {
		m.CacheMisses++
	}

	if m.TotalAPICalls > 0 {
		m.CacheHitPercent = float64(m.CacheHits) / float64(m.TotalAPICalls) * 100.0
	}

	if m.TotalInputTokens > 0 {
		m.CacheSavingsPercent = float64(m.CacheReadTokens) / float64(m.TotalInputTokens) * 100.0
	}

	m.EndTime = time.Now()

	return nil
}

// GetMetrics returns aggregated metrics for a conversation.
func (v *Validator) Metrics(conversationID string) *CacheMetrics {
	v.mu.RLock()
	defer v.mu.RUnlock()

	metrics, ok := v.metrics[conversationID]
	if !ok {
		return nil
	}

	// Return a copy to avoid external modification
	copy := *metrics
	return &copy
}

// GenerateReport creates a comprehensive analysis report.
func (v *Validator) GenerateReport(conversationID string) *CacheBreakReport {
	v.mu.RLock()
	defer v.mu.RUnlock()

	metrics, ok := v.metrics[conversationID]
	if !ok {
		return &CacheBreakReport{
			Summary:     &CacheMetrics{ConversationID: conversationID},
			GeneratedAt: time.Now(),
		}
	}

	breaks, _ := v.cacheBreakEvent[conversationID]

	// Group breaks by type
	breaksByType := make(map[string][]CacheBreakEvent)
	for _, b := range breaks {
		breaksByType[b.BreakType] = append(breaksByType[b.BreakType], *b)
	}

	clusters := make([]BreakCluster, 0)
	for typ, events := range breaksByType {
		cluster := BreakCluster{
			Type:   typ,
			Count:  len(events),
			Events: events,
		}
		clusters = append(clusters, cluster)
	}

	// Analyze hotspots
	locationBreaks := make(map[string]int)
	for _, b := range breaks {
		if b.Location != nil {
			key := fmt.Sprintf("%s:%d", b.Location.File, b.Location.Line)
			locationBreaks[key]++
		}
	}

	hotspots := make([]HotspotAnalysis, 0)
	for location, count := range locationBreaks {
		severity := string(SeverityLow)
		if count >= 3 {
			severity = string(SeverityHigh)
		} else if count >= 2 {
			severity = string(SeverityMedium)
		}

		hotspots = append(hotspots, HotspotAnalysis{
			Location:   location,
			BreakCount: count,
			Severity:   severity,
			Confidence: float64(count) / float64(len(breaks)),
		})
	}

	// Calculate efficiency
	efficiency := &EfficiencyMetrics{
		CacheHitRate:   metrics.CacheHitPercent / 100.0,
		CacheSavings:   metrics.CacheSavingsPercent / 100.0,
		BreakFrequency: float64(metrics.CacheBreaks) / float64(metrics.TotalTurns),
	}

	if efficiency.BreakFrequency < 0.1 {
		efficiency.BreakImpact = "NONE"
	} else if efficiency.BreakFrequency < 0.3 {
		efficiency.BreakImpact = "LOW"
	} else if efficiency.BreakFrequency < 0.7 {
		efficiency.BreakImpact = "MEDIUM"
	} else {
		efficiency.BreakImpact = "HIGH"
	}

	// Generate recommendations
	recommendations := generateRecommendations(metrics, breaks, locationBreaks)

	return &CacheBreakReport{
		Summary:         metrics,
		BreakClusters:   clusters,
		Hotspots:        hotspots,
		Efficiency:      efficiency,
		Recommendations: recommendations,
		GeneratedAt:     time.Now(),
	}
}

// ExportMetrics exports metrics in specified format.
func (v *Validator) ExportMetrics(ctx context.Context, conversationID string, format string) (any, error) {
	report := v.GenerateReport(conversationID)

	switch strings.ToLower(format) {
	case "json", "jsonl":
		return report, nil
	default:
		return report, fmt.Errorf("unknown export format: %s", format)
	}
}

// Reset clears all data for a conversation.
func (v *Validator) Reset(conversationID string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	delete(v.messageHashes, conversationID)
	delete(v.cacheBreakEvent, conversationID)
	delete(v.metrics, conversationID)

	return nil
}

// Helper functions

func estimateJSONSize(v any) int {
	data, _ := json.Marshal(v)
	return len(data)
}

func generateRecommendations(metrics *CacheMetrics, breaks []*CacheBreakEvent, locationBreaks map[string]int) []Recommendation {
	recommendations := make([]Recommendation, 0)

	// If too many breaks, recommend investigation
	if metrics.CacheBreaks > 5 {
		recommendations = append(recommendations, Recommendation{
			Priority:    "HIGH",
			Category:    "INVESTIGATION",
			Title:       "High frequency of cache breaks detected",
			Description: fmt.Sprintf("Detected %d cache breaks in %d turns. This may indicate systematic mutation of messages.", metrics.CacheBreaks, metrics.TotalTurns),
			Evidence:    "cache_break_frequency",
		})
	}

	// If cache hit rate is low, recommend investigation
	if metrics.CacheHitPercent < 50 {
		recommendations = append(recommendations, Recommendation{
			Priority:    "MEDIUM",
			Category:    "INVESTIGATION",
			Title:       "Cache hit rate is lower than expected",
			Description: "Cache is being invalidated frequently. Check for message mutations or system prompt changes between turns.",
			Evidence:    "cache_hit_rate",
		})
	}

	// Find hotspot with most breaks
	maxBreaks := 0
	hotspotLoc := ""
	for loc, count := range locationBreaks {
		if count > maxBreaks {
			maxBreaks = count
			hotspotLoc = loc
		}
	}

	if maxBreaks > 2 {
		recommendations = append(recommendations, Recommendation{
			Priority:    "HIGH",
			Category:    "CODE_CHANGE",
			Title:       "Fix cache breaks in critical code path",
			Description: fmt.Sprintf("Code location %s is causing %d cache breaks. Review mutations at this location.", hotspotLoc, maxBreaks),
			Location:    hotspotLoc,
			Evidence:    "location_break_count",
		})
	}

	// If system prompt changes detected
	if metrics.SystemPromptChanges > 0 {
		recommendations = append(recommendations, Recommendation{
			Priority:    "HIGH",
			Category:    "CODE_CHANGE",
			Title:       "System prompt should not change between turns",
			Description: fmt.Sprintf("System prompt changed %d times during conversation. This invalidates cache prefix. Consider moving dynamic instructions to non-cached blocks.", metrics.SystemPromptChanges),
			Evidence:    "system_prompt_changes",
		})
	}

	return recommendations
}
