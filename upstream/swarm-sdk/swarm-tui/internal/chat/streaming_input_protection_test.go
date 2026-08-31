package chat

import (
	"testing"
	"time"
)

// TestInputProtectionDebouncing verifies that viewport updates are debounced during streaming
func TestInputProtectionDebouncing(t *testing.T) {
	app := NewApp()

	// Initialize input protection
	app.inputProtection.debounceDelay = 50 * time.Millisecond
	app.inputProtection.lastUpdateTime = time.Time{}
	app.inputProtection.pendingUpdate = false

	// Simulate streaming start
	app.streamingInProgress = true

	// Add a test message to the conversation
	app.messages = []Message{
		{
			Role:    "assistant",
			Content: "Test streaming message",
		},
	}

	// First update should go through immediately (no previous update)
	beforeUpdate := time.Now()
	app.updateStreamingMessageIncremental()
	afterUpdate := time.Now()

	if app.inputProtection.lastUpdateTime.IsZero() {
		t.Error("Expected lastUpdateTime to be set after first update")
	}

	if app.inputProtection.pendingUpdate {
		t.Error("Expected pendingUpdate to be false after successful update")
	}

	// Immediate second update should be debounced
	time.Sleep(10 * time.Millisecond) // Wait less than debounceDelay
	beforeSecondUpdate := time.Now()
	app.updateStreamingMessageIncremental()

	if !app.inputProtection.pendingUpdate {
		t.Error("Expected pendingUpdate to be true when update is debounced")
	}

	// Wait for debounce delay to pass
	time.Sleep(60 * time.Millisecond) // Wait more than debounceDelay

	// Third update should go through after debounce delay
	beforeThirdUpdate := time.Now()
	app.updateStreamingMessageIncremental()
	afterThirdUpdate := time.Now()

	if app.inputProtection.pendingUpdate {
		t.Error("Expected pendingUpdate to be false after debounce delay has passed")
	}

	timeSinceSecond := beforeThirdUpdate.Sub(beforeSecondUpdate)
	if timeSinceSecond < app.inputProtection.debounceDelay {
		t.Errorf("Expected at least %v to pass between second and third update, got %v",
			app.inputProtection.debounceDelay, timeSinceSecond)
	}

	t.Logf("First update: %v", afterUpdate.Sub(beforeUpdate))
	t.Logf("Second update (debounced): %v", time.Since(beforeSecondUpdate))
	t.Logf("Third update (after delay): %v", afterThirdUpdate.Sub(beforeThirdUpdate))
}

// TestInputProtectionFlushOnStreamEnd verifies that pending updates are flushed when streaming ends
func TestInputProtectionFlushOnStreamEnd(t *testing.T) {
	app := NewApp()

	// Initialize input protection
	app.inputProtection.debounceDelay = 50 * time.Millisecond
	app.inputProtection.lastUpdateTime = time.Now()
	app.inputProtection.pendingUpdate = true

	// Simulate streaming
	app.streamingInProgress = true
	app.streamingMessage = true

	// Add a test message
	app.messages = []Message{
		{
			Role:    "assistant",
			Content: "Test streaming message",
		},
	}

	// Trigger streamDoneMsg which should flush pending updates
	msg := streamDoneMsg{
		totalTokens:  100,
		inputTokens:  50,
		outputTokens: 50,
	}

	// Process the message
	app.Update(msg)

	// Verify that streaming state is reset
	if app.streamingInProgress {
		t.Error("Expected streamingInProgress to be false after streamDoneMsg")
	}

	if app.inputProtection.pendingUpdate {
		t.Error("Expected pendingUpdate to be false after streamDoneMsg (should be flushed)")
	}
}

// TestInputProtectionUpdateFrequency verifies that updates are limited to the specified rate
func TestInputProtectionUpdateFrequency(t *testing.T) {
	app := NewApp()

	// Set a longer debounce delay for clearer testing
	debounceDelay := 100 * time.Millisecond
	app.inputProtection.debounceDelay = debounceDelay
	app.inputProtection.lastUpdateTime = time.Time{}
	app.inputProtection.pendingUpdate = false

	// Simulate streaming
	app.streamingInProgress = true

	// Add a test message
	app.messages = []Message{
		{
			Role:    "assistant",
			Content: "Test streaming message",
		},
	}

	// Track number of actual updates vs attempted updates
	attemptedUpdates := 0
	actualUpdates := 0

	// Try to update rapidly (every 10ms) for a fixed number of attempts
	startTime := time.Now()
	const attemptsTarget = 25
	for range attemptsTarget {
		attemptedUpdates++

		// Check if update would be debounced
		timeSinceLastUpdate := time.Since(app.inputProtection.lastUpdateTime)
		wasDebounced := timeSinceLastUpdate < app.inputProtection.debounceDelay && !app.inputProtection.lastUpdateTime.IsZero()

		app.updateStreamingMessageIncremental()

		if !wasDebounced {
			actualUpdates++
		}

		time.Sleep(10 * time.Millisecond)
	}
	elapsed := time.Since(startTime)

	// We should have attempted many updates but only performed a few
	expectedMaxUpdates := int(elapsed/debounceDelay) + 2 // ~2-4 updates in ~250ms with 100ms debounce

	t.Logf("Attempted updates: %d", attemptedUpdates)
	t.Logf("Actual updates: %d", actualUpdates)
	t.Logf("Expected max updates: %d", expectedMaxUpdates)

	if attemptedUpdates != attemptsTarget {
		t.Errorf("Expected %d attempted updates, got %d", attemptsTarget, attemptedUpdates)
	}

	if actualUpdates > expectedMaxUpdates {
		t.Errorf("Expected at most %d actual updates (debounced), got %d", expectedMaxUpdates, actualUpdates)
	}

	// Verify that debouncing reduced the update frequency significantly
	if actualUpdates == 0 {
		t.Fatal("Expected at least one actual update")
	}
	reductionRatio := float64(attemptedUpdates) / float64(actualUpdates)
	if reductionRatio < 5.0 {
		t.Errorf("Expected debouncing to reduce updates by at least 5x, got %.2fx reduction", reductionRatio)
	}

	t.Logf("Update frequency reduction: %.2fx", reductionRatio)
}
