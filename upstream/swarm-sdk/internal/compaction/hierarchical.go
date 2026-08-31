package compaction

import (
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// MessageImportance indica la importancia de un mensaje para preservación
type MessageImportance int

const (
	// ImportanceLow: mensaje normal, se resumirá
	ImportanceLow MessageImportance = iota
	// ImportanceMedium: mensaje con contexto relevante
	ImportanceMedium
	// ImportanceHigh: mensaje importante (error, decisión, cambio de modo)
	ImportanceHigh
	// ImportanceCritical: mensaje crítico (debe preservarse completo)
	ImportanceCritical
)

// ScoredMessage es un mensaje con su puntuación de importancia
type ScoredMessage struct {
	Message    *conversation.Message
	Importance MessageImportance
	Score      float64
	Reason     string
}

// HierarchicalSummary contiene el resultado de la compactación jerárquica
type HierarchicalSummary struct {
	// Nivel 1: Mensajes recientes (sin modificar)
	RecentMessages []*conversation.Message
	// Nivel 2: Mensajes importantes (preservados completos)
	ImportantMessages []*conversation.Message
	// Nivel 3: Chunks resumidos
	SummaryChunks []string
	// Estadísticas
	Stats HierarchicalStats
}

// HierarchicalStats contiene estadísticas del proceso jerárquico
type HierarchicalStats struct {
	TotalMessages      int
	RecentPreserved    int
	ImportantPreserved int
	Summarized         int
	ChunksGenerated    int
	CompressionRatio   float64
}

// HierarchicalConfig configura el comportamiento del resumen jerárquico
type HierarchicalConfig struct {
	// RecentMessagesCount: cuántos mensajes recientes preservar (default 5)
	RecentMessagesCount int
	// MaxImportantMessages: límite de mensajes importantes (default 10)
	MaxImportantMessages int
	// ChunkSize: tamaño de cada chunk para resumen (default 20 mensajes)
	ChunkSize int
	// MaxChunkTokens: máximo tokens por chunk resumido
	MaxChunkTokens int
}

// DefaultHierarchicalConfig retorna configuración por defecto
func DefaultHierarchicalConfig() HierarchicalConfig {
	return HierarchicalConfig{
		RecentMessagesCount:  5,
		MaxImportantMessages: 10,
		ChunkSize:            20,
		MaxChunkTokens:       4000,
	}
}

// ScoreMessage evalúa la importancia de un mensaje
func ScoreMessage(msg *conversation.Message) ScoredMessage {
	if msg == nil {
		return ScoredMessage{Message: msg, Importance: ImportanceLow, Score: 0}
	}

	content := strings.ToLower(msg.Content)
	score := 0.0
	importance := ImportanceLow
	reason := ""

	// 1. Errores son críticos
	if containsAny(content, []string{"error", "failed", "exception", "panic", "crash"}) {
		score += 100.0
		importance = ImportanceCritical
		reason = "Contains error indicator"
	}

	// 2. Decisiones de diseño son altamente importantes
	if containsAny(content, []string{"decided", "agreed", "conclusion", "consensus", "approved"}) {
		score += 80.0
		if importance < ImportanceHigh {
			importance = ImportanceHigh
			reason = "Contains decision indicator"
		}
	}

	// 3. Cambios de modo son importantes
	if containsAny(content, []string{"plan mode", "act mode", "debug mode", "switch mode"}) {
		score += 60.0
		if importance < ImportanceHigh {
			importance = ImportanceHigh
			reason = "Mode transition detected"
		}
	}

	// 4. Tool calls con resultados importantes
	if len(msg.ToolCalls) > 0 {
		for _, tc := range msg.ToolCalls {
			// Tools de archivo son importantes
			if containsAny(strings.ToLower(tc.Name), []string{"read", "write", "edit", "apply"}) {
				score += 40.0
				if importance < ImportanceMedium {
					importance = ImportanceMedium
					reason = "File operation"
				}
			}
		}
	}

	// 5. Tool results con errores o resultados significativos
	if len(msg.ToolResults) > 0 {
		for _, tr := range msg.ToolResults {
			output := strings.ToLower(tr.Output)
			if containsAny(output, []string{"error", "failed"}) {
				score += 50.0
				if importance < ImportanceHigh {
					importance = ImportanceHigh
					reason = "Tool result with error"
				}
			}
		}
	}

	// 6. Mensajes del usuario son siempre importantes (requests)
	if msg.Role == conversation.RoleUser {
		score += 30.0
		if importance < ImportanceMedium {
			importance = ImportanceMedium
			reason = "User request"
		}
	}

	// 7. Mensajes de sistema con cambios importantes
	if msg.Role == conversation.RoleSystem {
		score += 20.0
		if importance < ImportanceMedium {
			importance = ImportanceMedium
			reason = "System message"
		}
	}

	// 8. Longitud como indicador de contenido relevante
	contentLen := len(msg.Content)
	if contentLen > 500 {
		score += 10.0
	}
	if contentLen > 1000 {
		score += 10.0
	}

	return ScoredMessage{
		Message:    msg,
		Importance: importance,
		Score:      score,
		Reason:     reason,
	}
}

// ScoreMessages evalúa todos los mensajes y retorna puntuaciones
func ScoreMessages(messages []*conversation.Message) []ScoredMessage {
	scored := make([]ScoredMessage, 0, len(messages))
	for _, msg := range messages {
		scored = append(scored, ScoreMessage(msg))
	}
	return scored
}

// containsAny verifica si el string contiene alguno de los patrones
func containsAny(s string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// BuildHierarchicalSummary crea un resumen jerárquico de los mensajes
//
// Nivel 1: Mensajes recientes (últimos N, sin modificar)
// Nivel 2: Mensajes importantes (preservados completos)
// Nivel 3: Resto resumido por chunks
func BuildHierarchicalSummary(
	messages []*conversation.Message,
	config HierarchicalConfig,
	summarizeFunc func([]*conversation.Message) (string, error),
) (*HierarchicalSummary, error) {
	if len(messages) == 0 {
		return &HierarchicalSummary{
			RecentMessages:    []*conversation.Message{},
			ImportantMessages: []*conversation.Message{},
			SummaryChunks:     []string{},
			Stats:             HierarchicalStats{},
		}, nil
	}

	// Puntuar todos los mensajes
	scored := ScoreMessages(messages)

	// Separar mensajes por importancia
	recentCount := min(config.RecentMessagesCount, len(scored))

	// Nivel 1: Mensajes recientes (últimos N)
	recentStart := max(len(scored)-recentCount, 0)
	recentMessages := make([]*conversation.Message, 0, recentCount)
	for i := recentStart; i < len(scored); i++ {
		recentMessages = append(recentMessages, scored[i].Message)
	}

	// Nivel 2: Mensajes importantes (excluyendo los recientes)
	importantMap := make(map[string]bool)
	for _, m := range recentMessages {
		if m != nil {
			importantMap[m.ID] = true
		}
	}

	importantMessages := make([]*conversation.Message, 0)
	for i, sm := range scored {
		// Saltar mensajes recientes (ya incluidos)
		if i >= recentStart {
			continue
		}
		// Saltar si ya está en recientes
		if sm.Message != nil && importantMap[sm.Message.ID] {
			continue
		}
		// Incluir si es importante
		if sm.Importance >= ImportanceHigh {
			importantMessages = append(importantMessages, sm.Message)
			importantMap[sm.Message.ID] = true
		}
	}

	// Limitar mensajes importantes
	if len(importantMessages) > config.MaxImportantMessages {
		start := len(importantMessages) - config.MaxImportantMessages
		importantMessages = importantMessages[start:]
	}

	// Nivel 3: Resto de mensajes para resumir por chunks
	toSummarize := make([]*conversation.Message, 0)
	for i, sm := range scored {
		// Saltar recientes
		if i >= recentStart {
			continue
		}
		// Saltar importantes
		if sm.Message != nil && importantMap[sm.Message.ID] {
			continue
		}
		// Incluir para resumen
		toSummarize = append(toSummarize, sm.Message)
	}

	// Generar chunks resumidos
	var chunks []string
	if len(toSummarize) > 0 {
		chunks = splitAndSummarize(toSummarize, config, summarizeFunc)
	}

	// Calcular estadísticas
	totalPreserved := len(recentMessages) + len(importantMessages)
	stats := HierarchicalStats{
		TotalMessages:      len(messages),
		RecentPreserved:    len(recentMessages),
		ImportantPreserved: len(importantMessages),
		Summarized:         len(toSummarize),
		ChunksGenerated:    len(chunks),
		CompressionRatio:   float64(totalPreserved) / float64(len(messages)),
	}

	return &HierarchicalSummary{
		RecentMessages:    recentMessages,
		ImportantMessages: importantMessages,
		SummaryChunks:     chunks,
		Stats:             stats,
	}, nil
}

// splitAndSummarize divide mensajes en chunks y los resume
func splitAndSummarize(
	messages []*conversation.Message,
	config HierarchicalConfig,
	summarizeFunc func([]*conversation.Message) (string, error),
) []string {
	if len(messages) == 0 {
		return []string{}
	}

	var chunks []string
	chunkSize := config.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 20
	}

	// Dividir en chunks
	for start := 0; start < len(messages); start += chunkSize {
		end := min(start+chunkSize, len(messages))

		chunk := messages[start:end]
		if len(chunk) == 0 {
			continue
		}

		// Resumir el chunk
		if summarizeFunc != nil {
			summary, err := summarizeFunc(chunk)
			if err == nil && summary != "" {
				chunks = append(chunks, summary)
			}
		}
	}

	return chunks
}

// FormatHierarchicalContext formatea el resumen jerárquico para el prompt
func FormatHierarchicalContext(summary *HierarchicalSummary) string {
	var builder strings.Builder

	// Estadísticas
	stats := summary.Stats
	builder.WriteString(fmt.Sprintf(
		"## Conversation Summary (Hierarchical)\n\n"+
			"**Total Messages**: %d \n"+
			"**Recent Preserved**: %d \n"+
			"**Important Preserved**: %d \n"+
			"**Summarized**: %d (in %d chunks)\n\n",
		stats.TotalMessages,
		stats.RecentPreserved,
		stats.ImportantPreserved,
		stats.Summarized,
		stats.ChunksGenerated,
	))

	// Mensajes importantes
	if len(summary.ImportantMessages) > 0 {
		builder.WriteString("### Important Messages (Preserved Verbatim)\n\n")
		for _, msg := range summary.ImportantMessages {
			if msg != nil {
				builder.WriteString(fmt.Sprintf("**[%s]**: %s\n\n", msg.Role, truncate(msg.Content, 200)))
			}
		}
	}

	// Chunks resumidos
	if len(summary.SummaryChunks) > 0 {
		builder.WriteString("### Earlier Conversation (Summarized)\n\n")
		for i, chunk := range summary.SummaryChunks {
			builder.WriteString(fmt.Sprintf("**Chunk %d**: %s\n\n", i+1, chunk))
		}
	}

	return builder.String()
}

// truncate trunca un string a la longitud máxima
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// IsHierarchicalEnabled verifica si la compactación jerárquica está habilitada
func IsHierarchicalEnabled() bool {
	// Por defecto habilitado, puede ser controlado por feature flag
	return true
}

// HierarchicalCompactResult extiende CompactionResult con información jerárquica
type HierarchicalCompactResult struct {
	CompactionResult
	HierarchicalSummary
	PreservedMessages []*conversation.Message
}
