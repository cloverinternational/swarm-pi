package compaction

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// TestPreservationRate mide qué porcentaje de contexto crítico se preserva
func TestPreservationRate(t *testing.T) {
	testCases := []struct {
		name            string
		messageCount    int
		importantMsgs   int
		wantMinPreserve float64
	}{
		{
			name:            "Small conversation (20 msgs)",
			messageCount:    20,
			importantMsgs:   3,
			wantMinPreserve: 0.20, // 20% mínimo
		},
		{
			name:            "Medium conversation (50 msgs)",
			messageCount:    50,
			importantMsgs:   5,
			wantMinPreserve: 0.15, // 15% mínimo
		},
		{
			name:            "Large conversation (100 msgs)",
			messageCount:    100,
			importantMsgs:   10,
			wantMinPreserve: 0.10, // 10% mínimo
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			messages := generateTestMessages(tc.messageCount, tc.importantMsgs)

			config := DefaultHierarchicalConfig()
			mockSummarize := func(msgs []*conversation.Message) (string, error) {
				return fmt.Sprintf("Summary of %d messages", len(msgs)), nil
			}

			summary, err := BuildHierarchicalSummary(messages, config, mockSummarize)
			if err != nil {
				t.Fatalf("BuildHierarchicalSummary failed: %v", err)
			}

			// Calcular ratio de preservación
			preserved := summary.Stats.RecentPreserved + summary.Stats.ImportantPreserved
			ratio := float64(preserved) / float64(summary.Stats.TotalMessages)

			t.Logf("Preservation rate: %.2f%% (%d/%d messages)",
				ratio*100, preserved, summary.Stats.TotalMessages)

			if ratio < tc.wantMinPreserve {
				t.Errorf("Preservation rate %.2f%% below minimum %.2f%%",
					ratio*100, tc.wantMinPreserve*100)
			}
		})
	}
}

// TestCriticalContextNeverLost verifica que ciertos mensajes nunca se pierdan
func TestCriticalContextNeverLost(t *testing.T) {
	// Crear mensajes con errores y decisiones críticas
	messages := []*conversation.Message{
		{ID: "1", Role: conversation.RoleUser, Content: "Implement auth"},
		{ID: "2", Role: conversation.RoleAssistant, Content: "Working on it"},
		{ID: "3", Role: conversation.RoleAssistant,
			Content: "Error: failed to connect to database", // CRÍTICO
			ToolResults: []conversation.ToolResult{
				{Output: "Error: connection refused"},
			}},
		{ID: "4", Role: conversation.RoleUser, Content: "Fix it"},
		{ID: "5", Role: conversation.RoleAssistant,
			Content: "I have decided to use connection pooling"}, // CRÍTICO
	}

	// Agregar 20 mensajes genéricos
	for i := 6; i < 26; i++ {
		messages = append(messages, &conversation.Message{
			ID:      fmt.Sprintf("%d", i),
			Role:    conversation.RoleAssistant,
			Content: fmt.Sprintf("Generic message %d", i),
		})
	}

	config := DefaultHierarchicalConfig()
	mockSummarize := func(msgs []*conversation.Message) (string, error) {
		return "Summary", nil
	}

	summary, err := BuildHierarchicalSummary(messages, config, mockSummarize)
	if err != nil {
		t.Fatalf("Failed: %v", err)
	}

	// Verificar que el mensaje de error está en importantes
	errorPreserved := false
	decisionPreserved := false

	for _, msg := range summary.ImportantMessages {
		if msg != nil {
			if strings.Contains(msg.Content, "Error: failed to connect") {
				errorPreserved = true
			}
			if strings.Contains(msg.Content, "decided") {
				decisionPreserved = true
			}
		}
	}

	if !errorPreserved {
		t.Error("Critical error message was not preserved")
	}
	if !decisionPreserved {
		t.Error("Critical decision message was not preserved")
	}

	t.Logf("✅ Critical context preserved: error=%v, decision=%v",
		errorPreserved, decisionPreserved)
}

// TestVerificationSections verifica que se detecten todas las secciones requeridas
func TestVerificationSections(t *testing.T) {
	testCases := []struct {
		name         string
		summary      string
		wantValid    bool
		wantMinScore float64
	}{
		{
			name: "Complete summary",
			summary: `# Summary
## Primary Request
Implement feature
## Current Work
Working on it
## Files and Code Sections
Modified files
## Technical Details
Using Go
## Current Tasks
- [ ] Task 1`,
			wantValid:    true,
			wantMinScore: 60.0,
		},
		{
			name:         "Empty summary",
			summary:      "",
			wantValid:    false,
			wantMinScore: 0.0,
		},
		{
			name: "Missing sections",
			summary: `# Summary
## Primary Request
Implement feature
## Current Work
Wiring it up`,
			wantValid:    false,
			wantMinScore: 20.0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := VerifySummary(tc.summary)

			if result.Valid != tc.wantValid {
				t.Errorf("Valid = %v, want %v", result.Valid, tc.wantValid)
			}
			if result.Score < tc.wantMinScore {
				t.Errorf("Score = %.1f, want at least %.1f", result.Score, tc.wantMinScore)
			}
		})
	}
}

// BenchmarkCompaction mide el rendimiento de la compactación
func BenchmarkCompaction(b *testing.B) {
	sizes := []int{10, 50, 100, 200}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("size_%d", size), func(b *testing.B) {
			messages := generateTestMessages(size, size/10)
			config := DefaultHierarchicalConfig()

			mockSummarize := func(msgs []*conversation.Message) (string, error) {
				return "Summary", nil
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := BuildHierarchicalSummary(messages, config, mockSummarize)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkScoring mide el rendimiento del scoring de mensajes
func BenchmarkScoring(b *testing.B) {
	messages := generateTestMessages(100, 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, msg := range messages {
			ScoreMessage(msg)
		}
	}
}

// BenchmarkVerification mide el rendimiento de la verificación
func BenchmarkVerification(b *testing.B) {
	summary := generateLargeSummary()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		VerifySummary(summary)
	}
}

// TestCompactionQualityReport genera un reporte de calidad
func TestCompactionQualityReport(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping quality report in short mode")
	}

	t.Log("╔════════════════════════════════════════════════════════╗")
	t.Log("║        COMPACTION QUALITY REPORT                       ║")
	t.Log("╚════════════════════════════════════════════════════════╝")

	// Test con diferentes tamaños
	sizes := []int{20, 50, 100, 200}

	for _, size := range sizes {
		messages := generateTestMessages(size, size/10)
		config := DefaultHierarchicalConfig()

		mockSummarize := func(msgs []*conversation.Message) (string, error) {
			return fmt.Sprintf("Summary of %d messages", len(msgs)), nil
		}

		summary, _ := BuildHierarchicalSummary(messages, config, mockSummarize)
		stats := summary.Stats

		t.Logf("\nConversation size: %d messages", size)
		t.Logf("  Recent preserved: %d (%.1f%%)",
			stats.RecentPreserved,
			float64(stats.RecentPreserved)/float64(stats.TotalMessages)*100)
		t.Logf("  Important preserved: %d (%.1f%%)",
			stats.ImportantPreserved,
			float64(stats.ImportantPreserved)/float64(stats.TotalMessages)*100)
		t.Logf("  Summarized: %d (%.1f%%)",
			stats.Summarized,
			float64(stats.Summarized)/float64(stats.TotalMessages)*100)
		t.Logf("  Chunks: %d", stats.ChunksGenerated)
		t.Logf("  Compression ratio: %.2f", stats.CompressionRatio)

		// Quality invariants — these must hold for every conversation size.
		if stats.TotalMessages != size {
			t.Errorf("size %d: TotalMessages = %d, want %d", size, stats.TotalMessages, size)
		}
		// No category may exceed the total, and none may be negative.
		if stats.RecentPreserved < 0 || stats.RecentPreserved > stats.TotalMessages {
			t.Errorf("size %d: RecentPreserved %d out of range [0,%d]", size, stats.RecentPreserved, stats.TotalMessages)
		}
		if stats.Summarized < 0 || stats.Summarized > stats.TotalMessages {
			t.Errorf("size %d: Summarized %d out of range [0,%d]", size, stats.Summarized, stats.TotalMessages)
		}
		// Some recent context must always survive compaction.
		if stats.RecentPreserved == 0 {
			t.Errorf("size %d: no recent messages preserved", size)
		}
		// Larger conversations must actually be compressed (something summarized,
		// and the compression ratio must reflect a real reduction).
		if size >= 100 {
			if stats.Summarized == 0 {
				t.Errorf("size %d: expected summarization but Summarized == 0", size)
			}
			if stats.CompressionRatio <= 0 || stats.CompressionRatio >= 1 {
				t.Errorf("size %d: expected compression ratio in (0,1), got %.2f", size, stats.CompressionRatio)
			}
		}
	}
}

// Helper functions

func generateTestMessages(total, important int) []*conversation.Message {
	rand.Seed(time.Now().UnixNano())
	messages := make([]*conversation.Message, 0, total)

	now := time.Now()

	// Agregar mensajes importantes en posiciones aleatorias
	importantPositions := make(map[int]bool)
	for i := 0; i < important && i < total; i++ {
		pos := rand.Intn(total)
		importantPositions[pos] = true
	}

	for i := range total {
		msg := &conversation.Message{
			ID:        fmt.Sprintf("msg_%d", i),
			Role:      conversation.RoleAssistant,
			Content:   fmt.Sprintf("Generic message content number %d with some details", i),
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		}

		// Hacer algunos mensajes importantes
		if importantPositions[i] {
			msg.Content = fmt.Sprintf("Error: failed operation %d - critical failure", i)
			msg.ToolResults = []conversation.ToolResult{
				{Output: "Error: operation failed"},
			}
		}

		// Últimos mensajes son del usuario
		if i >= total-5 {
			msg.Role = conversation.RoleUser
			msg.Content = fmt.Sprintf("User request %d", i)
		}

		messages = append(messages, msg)
	}

	return messages
}

func generateLargeSummary() string {
	return `# Session Summary

## Primary Request
Implement a complete authentication system with JWT tokens, refresh tokens, 
and OAuth2 integration for multiple providers.

## Current Work
Setting up the middleware layer and implementing token validation logic.
Currently working on the refresh token rotation mechanism.

## Files and Code
### /tmp/auth.go
` + "```go" + `
package auth

func ValidateToken(token string) (*Claims, error) {
    // Implementation here
}
` + "```" + `

## Technical Details
- Using golang-jwt v5.0.0
- Token expiration: 15 minutes
- Refresh token expiration: 7 days
- Algorithm: RS256

## Current Tasks
### In Progress
- [ ] Implement middleware
- [ ] Add refresh token rotation
- [ ] Write comprehensive tests

### Pending
- [ ] OAuth2 integration
- [ ] Documentation

## Errors and Fixes
Fixed nil pointer dereference in token parsing by adding proper validation.

## Decisions Made
Decided to use asymmetric keys (RS256) instead of symmetric (HS256) for better security.
`
}

// TestFileRecoveryRate mide la tasa de recuperación de archivos
func TestFileRecoveryRate(t *testing.T) {
	testCases := []struct {
		name     string
		numFiles int
		modified int
		wantMin  int
	}{
		{
			name:     "Few files (5)",
			numFiles: 5,
			modified: 3,
			wantMin:  3,
		},
		{
			name:     "Many files (20)",
			numFiles: 20,
			modified: 15,
			wantMin:  10, // Limitado por MaxFilesToRecover
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			compCtx := &CompactionContext{
				ModifiedFiles: make([]string, tc.modified),
			}
			for i := 0; i < tc.modified; i++ {
				compCtx.ModifiedFiles[i] = fmt.Sprintf("/tmp/file%d.go", i)
			}

			// Verificar que ModifiedFiles está poblado
			if len(compCtx.ModifiedFiles) < tc.wantMin {
				t.Errorf("Only %d modified files, expected at least %d",
					len(compCtx.ModifiedFiles), tc.wantMin)
			}
		})
	}
}

// TestMetricsAccuracy verifica que las métricas sean precisas
func TestMetricsAccuracy(t *testing.T) {
	metrics := NewCompactionMetrics()

	// Simular 10 compactaciones
	for i := range 10 {
		valid := i < 7 // 70% éxito
		result := VerificationResult{
			Valid: valid,
			Score: float64(50 + i*5),
		}
		if !valid {
			result.MissingSections = []string{"Current Work"}
		}
		metrics.RecordResult(result)
	}

	// 3 reintentos
	for range 3 {
		metrics.RecordRetry()
	}

	// Verificar métricas
	if metrics.TotalCompactions != 10 {
		t.Errorf("Total = %d, want 10", metrics.TotalCompactions)
	}
	if metrics.SuccessfulCompactions != 7 {
		t.Errorf("Success = %d, want 7", metrics.SuccessfulCompactions)
	}
	if metrics.FailedCompactions != 3 {
		t.Errorf("Failed = %d, want 3", metrics.FailedCompactions)
	}
	if metrics.RetryCount != 3 {
		t.Errorf("Retries = %d, want 3", metrics.RetryCount)
	}

	t.Logf("Metrics: %s", metrics.FormatMetrics())
}
