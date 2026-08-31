package compaction

import (
	"fmt"
	"strings"
	"time"
)

// VerificationResult contiene el resultado de la verificación
type VerificationResult struct {
	Valid            bool
	MissingSections  []string
	PresentSections  []string
	Score            float64
	RetryRecommended bool
}

// RequiredSections lista las secciones obligatorias en el resumen.
// These must match the section names the CompressionPrompt actually asks the
// model to produce (see prompt.go). Names are matched as substrings/keywords by
// hasSection, so "Primary Request" matches "Primary Request and Intent".
var RequiredSections = []string{
	"Primary Request",        // 1. Primary Request and Intent
	"Current Work",           // 8. Current Work
	"Files and Code",         // 3. Files and Code Sections
	"Key Technical Concepts", // 2. Key Technical Concepts
	"Pending Tasks",          // 7. Pending Tasks
}

// OptionalSections lista secciones opcionales pero deseables
var OptionalSections = []string{
	"Errors and Fixes",
	"MCP Context",
	"Decisions Made",
}

// VerifySummary verifica que el resumen cumpla con los requisitos mínimos
// siguiendo el modelo de Claude Code (CC's verifyPostCompactionState)
func VerifySummary(summary string) VerificationResult {
	result := VerificationResult{
		Valid:            true,
		PresentSections:  []string{},
		MissingSections:  []string{},
		Score:            0,
		RetryRecommended: false,
	}

	if len(summary) == 0 {
		result.Valid = false
		result.RetryRecommended = true
		return result
	}

	// Verificar secciones requeridas
	requiredPresent := 0
	for _, section := range RequiredSections {
		if hasSection(summary, section) {
			result.PresentSections = append(result.PresentSections, section)
			requiredPresent++
		} else {
			result.MissingSections = append(result.MissingSections, section)
			result.Valid = false
		}
	}

	// Verificar secciones opcionales (bonus)
	optionalPresent := 0
	for _, section := range OptionalSections {
		if hasSection(summary, section) {
			result.PresentSections = append(result.PresentSections, section)
			optionalPresent++
		}
	}

	// Calcular score (0-100)
	requiredScore := float64(requiredPresent) / float64(len(RequiredSections)) * 70.0
	optionalScore := float64(optionalPresent) / float64(len(OptionalSections)) * 30.0
	result.Score = requiredScore + optionalScore

	// Validaciones adicionales
	// 1. Longitud mínima — a degenerate/truncated summary is too short to be
	//    useful. But a structurally complete summary (all required sections
	//    present, none empty) is valid even if concise, so only the length
	//    floor invalidates incomplete-AND-short output.
	if len(summary) < 200 {
		if requiredPresent < len(RequiredSections) {
			result.Valid = false
			result.RetryRecommended = true
		}
	}

	// 2. Longitud máxima razonable
	if len(summary) > 8000 {
		// Resumen demasiado largo puede indicar que no está condensando bien
		result.Score -= 10
	}

	// 3. Verificar que no sea solo headers sin contenido
	if hasEmptySections(summary) {
		result.Valid = false
		result.RetryRecommended = true
		result.Score -= 20
	}

	// 4. Verificar que mencione archivos si se recuperaron archivos
	if strings.Contains(summary, "Restored Files") && !hasSection(summary, "Files and Code") {
		result.Score -= 15
	}

	// Determinar si se recomienda reintento
	if result.Score < 60 || len(result.MissingSections) >= 2 {
		result.RetryRecommended = true
	}

	return result
}

// sectionSynonyms maps a canonical required-section name to alternative
// headings that older/variant summary formats use. A summary satisfies a
// required section if it contains the canonical name OR any synonym. This keeps
// verification robust across the 9-section prompt and legacy 7-section outputs.
var sectionSynonyms = map[string][]string{
	"Current Work":           {"Current State"},
	"Pending Tasks":          {"Next Steps", "Optional Next Step", "Current Tasks"},
	"Key Technical Concepts": {"Technical Details", "Technical Concepts"},
}

// hasSection verifica si una sección existe en el resumen
func hasSection(summary, section string) bool {
	if matchSectionName(summary, section) {
		return true
	}
	// Accept known synonyms for the canonical section name.
	for _, syn := range sectionSynonyms[section] {
		if matchSectionName(summary, syn) {
			return true
		}
	}
	return false
}

// matchSectionName reports whether a single heading name appears in the summary
// as a markdown header, bold label, "Name:" prefix, or via keyword fallback.
func matchSectionName(summary, section string) bool {
	// Buscar como header markdown
	patterns := []string{
		"## " + section,
		"### " + section,
		"**" + section + "**",
		section + ":",
	}

	lowerSummary := strings.ToLower(summary)
	lowerSection := strings.ToLower(section)

	for _, pattern := range patterns {
		if strings.Contains(lowerSummary, strings.ToLower(pattern)) {
			return true
		}
	}

	// Búsqueda flexible (solo las palabras clave)
	keyWords := strings.Fields(lowerSection)
	if len(keyWords) >= 2 {
		if strings.Contains(lowerSummary, keyWords[0]) && strings.Contains(lowerSummary, keyWords[1]) {
			return true
		}
	}

	return false
}

// hasEmptySections detecta secciones que solo tienen headers sin contenido
func hasEmptySections(summary string) bool {
	lines := strings.Split(summary, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Si es un header
		if strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ") {
			// Verificar siguiente línea no vacía
			if i+1 < len(lines) {
				nextLine := strings.TrimSpace(lines[i+1])

				// Si es otro header o está vacío, la sección está vacía
				if nextLine == "" || strings.HasPrefix(nextLine, "##") {
					// Buscar más abajo contenido real
					foundContent := false
					for j := i + 1; j < len(lines) && j < i+5; j++ {
						if strings.TrimSpace(lines[j]) != "" && !strings.HasPrefix(strings.TrimSpace(lines[j]), "##") {
							foundContent = true
							break
						}
					}
					if !foundContent {
						return true
					}
				}
			}
		}
	}

	return false
}

// VerificationConfig configura la verificación y reintentos
type VerificationConfig struct {
	MaxRetries int
	RetryDelay time.Duration
	StrictMode bool
}

// DefaultVerificationConfig retorna configuración por defecto
func DefaultVerificationConfig() VerificationConfig {
	return VerificationConfig{
		MaxRetries: 3,
		RetryDelay: 500 * time.Millisecond,
		StrictMode: true,
	}
}

// CompactWithVerification realiza compactación con verificación y reintento
func (s *Service) CompactWithVerification(
	summary string,
	verifyFunc func(string) VerificationResult,
	retryFunc func() (string, error),
	config VerificationConfig,
) (string, *VerificationResult, error) {
	attempt := 0
	var lastResult VerificationResult

	for attempt <= config.MaxRetries {
		// Verificar resultado actual
		lastResult = verifyFunc(summary)

		if lastResult.Valid {
			return summary, &lastResult, nil
		}

		// No hay más reintentos disponibles
		if attempt >= config.MaxRetries {
			break
		}

		// Intentar de nuevo
		attempt++

		// Esperar antes de reintentar
		if config.RetryDelay > 0 {
			time.Sleep(config.RetryDelay)
		}

		// Llamar función de reintento
		newSummary, err := retryFunc()
		if err != nil {
			return summary, &lastResult, fmt.Errorf("retry %d failed: %w", attempt, err)
		}

		summary = newSummary
	}

	// Todos los reintentos agotados
	return summary, &lastResult, fmt.Errorf(
		"verification failed after %d attempts, score %.1f%%, missing: %v",
		attempt, lastResult.Score, lastResult.MissingSections,
	)
}

// GenerateRetryPrompt genera un prompt más específico para el reintento
func GenerateRetryPrompt(originalPrompt string, result VerificationResult) string {
	builder := strings.Builder{}

	builder.WriteString(originalPrompt)
	builder.WriteString("\n\n")
	builder.WriteString("=== CORRECCIONES REQUERIDAS ===\n\n")

	if len(result.MissingSections) > 0 {
		builder.WriteString("Secciones OBLIGATORIAS que faltan:\n")
		for _, section := range result.MissingSections {
			builder.WriteString(fmt.Sprintf("  - %s\n", section))
		}
		builder.WriteString("\n")
	}

	if result.Score < 60 {
		builder.WriteString(fmt.Sprintf(
			"Calidad actual: %.1f%%. Se requiere al menos 60%%.\n\n",
			result.Score,
		))
	}

	builder.WriteString("Por favor, regenera el resumen asegurándote de:\n")
	builder.WriteString("1. Incluir TODAS las secciones listadas arriba\n")
	builder.WriteString("2. Cada sección debe tener contenido sustancial (no solo headers)\n")
	builder.WriteString("3. El total debe tener al menos 200 caracteres\n")

	return builder.String()
}

// CompactionMetrics contiene métricas para observabilidad
type CompactionMetrics struct {
	TotalCompactions      int
	SuccessfulCompactions int
	FailedCompactions     int
	RetryCount            int
	AverageScore          float64
	MissingSections       map[string]int
}

// NewCompactionMetrics crea un nuevo tracker de métricas
func NewCompactionMetrics() *CompactionMetrics {
	return &CompactionMetrics{
		MissingSections: make(map[string]int),
	}
}

// RecordResult registra un resultado de verificación
func (m *CompactionMetrics) RecordResult(result VerificationResult) {
	m.TotalCompactions++

	if result.Valid {
		m.SuccessfulCompactions++
	} else {
		m.FailedCompactions++
	}

	m.AverageScore = (m.AverageScore*float64(m.TotalCompactions-1) + result.Score) / float64(m.TotalCompactions)

	// Track secciones faltantes
	for _, section := range result.MissingSections {
		m.MissingSections[section]++
	}
}

// RecordRetry registra un reintento
func (m *CompactionMetrics) RecordRetry() {
	m.RetryCount++
}

// FormatMetrics formatea las métricas para logging
func (m *CompactionMetrics) FormatMetrics() string {
	builder := strings.Builder{}

	builder.WriteString(fmt.Sprintf(
		"Compaction Metrics:\n"+
			"  Total: %d\n"+
			"  Successful: %d (%.1f%%)\n"+
			"  Failed: %d (%.1f%%)\n"+
			"  Retries: %d\n"+
			"  Avg Score: %.1f%%\n",
		m.TotalCompactions,
		m.SuccessfulCompactions,
		float64(m.SuccessfulCompactions)/float64(m.TotalCompactions)*100,
		m.FailedCompactions,
		float64(m.FailedCompactions)/float64(m.TotalCompactions)*100,
		m.RetryCount,
		m.AverageScore,
	))

	if len(m.MissingSections) > 0 {
		builder.WriteString("  Most missing sections:\n")
		for section, count := range m.MissingSections {
			if count > 0 {
				builder.WriteString(fmt.Sprintf("    - %s: %d times\n", section, count))
			}
		}
	}

	return builder.String()
}
