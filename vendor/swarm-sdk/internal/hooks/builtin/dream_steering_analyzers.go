package builtin

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/findings"
)

// fileRefPattern matches plausible file references like path/to/file.ext or ./file.ext
var fileRefPattern = regexp.MustCompile(`(?:^|[\s"'(,])([./\w-]+\.\w{1,5})(?:[\s"'),:]|$)`)

// DreamAnalyzer extracts findings worth remembering as agent memories.
// This is the tagging layer that identifies Bronze findings with memory
// potential. Priority scoring is deferred to the LLM via FindingsAnalysisHook.
type DreamAnalyzer struct{}

func NewDreamAnalyzer() *DreamAnalyzer {
	return &DreamAnalyzer{}
}

func (a *DreamAnalyzer) Name() string {
	return "dream"
}

// Analyze decides what agents should remember. Tags findings and sets
// ShouldCapture but does NOT score priority -- the agent does that.
func (a *DreamAnalyzer) Analyze(ctx context.Context, toolName string, input, output map[string]any) (findings.AnalysisResult, error) {
	result := findings.AnalysisResult{
		ShouldCapture: false,
		Priority:      0, // Unscored -- LLM will assign real priority via EvaluateFindings
	}

	// Pattern 1: Repeated failures (agent should remember what NOT to do).
	// Skip "failures" that are just discovery commands returning non-zero —
	// `which foo`, `grep needle`, `find -name` all exit 1 or 127 on misses,
	// which is NORMAL exploration, not a system error. Treating them as
	// anti-patterns dragged the Bash avoidance score up on every discovery
	// turn and created "Avoid: Bash - error severity 0.27" system reminders
	// on healthy sessions (2026-04-27 survey).
	if !isDiscoveryNonZero(toolName, input, output) {
		if failureScore := a.calculateFailureScore(toolName, input, output); failureScore > 0 {
			result.ShouldCapture = true
			result.Tags = append(result.Tags, "anti-pattern", "memory")
			result.Insights = append(result.Insights,
				fmt.Sprintf("Avoid: %s - error severity %.2f", toolName, failureScore))
		}
	}

	// Pattern 2: Successful recipe (agent should remember what worked).
	// Threshold at 0.5 so only genuinely complex or slow operations produce
	// a "recipe" insight. The calculator gives a base 0.3 to any successful
	// tool — that base alone shouldn't generate a system-reminder for every
	// TaskCreate / Read / Bash call, which was the main source of per-tool
	// "succeeded with quality: solid" noise.
	if recipeStrength := a.calculateRecipeStrength(toolName, input, output); recipeStrength > 0.5 {
		result.ShouldCapture = true
		result.Tags = append(result.Tags, "recipe", "memory")
		result.Insights = append(result.Insights, a.extractRecipe(toolName, input, output, recipeStrength))
	}

	// Pattern 3: File relationship discovered. Suppressed entirely when no
	// real file references are detected in the content — the old behavior
	// emitted "relates to [] (connection: moderate, strength: 0.80)" on
	// plain directory listings and empty directories, which is noise.
	if relationshipStrength := a.calculateRelationshipStrength(toolName, input, output); relationshipStrength > 0 {
		if rel := a.extractRelationship(input, output, relationshipStrength); rel != "" {
			result.ShouldCapture = true
			result.Tags = append(result.Tags, "relationship", "memory")
			result.Insights = append(result.Insights, rel)
		}
	}

	// Pattern 4: Semantic insight (non-obvious connection)
	if insightQuality := a.calculateInsightQuality(toolName, input, output); insightQuality > 0 {
		result.ShouldCapture = true
		result.Tags = append(result.Tags, "insight", "memory")
		result.Insights = append(result.Insights, a.extractInsight(input, output, insightQuality))
	}

	return result, nil
}

// calculateFailureScore returns 0.0-1.0 severity based on error characteristics
func (a *DreamAnalyzer) calculateFailureScore(toolName string, input, output map[string]any) float64 {
	success, hasSuccess := output["success"].(bool)
	if !hasSuccess || success {
		return 0
	}

	errMsg, _ := output["error"].(string)
	if errMsg == "" {
		errMsg, _ = output["error_message"].(string)
	}
	if errMsg == "" {
		errMsg, _ = output["stderr"].(string)
	}

	lowerErr := strings.ToLower(errMsg)

	// Critical errors (0.8-1.0)
	criticalPatterns := []string{"permission denied", "access denied", "unauthorized"}
	for _, pattern := range criticalPatterns {
		if strings.Contains(lowerErr, pattern) {
			return 0.85 + (float64(len(errMsg)%10) / 100.0) // 0.85-0.95
		}
	}

	// Medium severity (0.5-0.7)
	mediumPatterns := []string{"no such file", "not found", "does not exist", "invalid"}
	for _, pattern := range mediumPatterns {
		if strings.Contains(lowerErr, pattern) {
			return 0.55 + (float64(len(errMsg)%15) / 100.0) // 0.55-0.70
		}
	}

	// Lower severity (0.3-0.5)
	lowPatterns := []string{"syntax error", "warning", "deprecated"}
	for _, pattern := range lowPatterns {
		if strings.Contains(lowerErr, pattern) {
			return 0.35 + (float64(len(errMsg)%20) / 100.0) // 0.35-0.55
		}
	}

	// Generic failure (0.2-0.4)
	if errMsg != "" {
		return 0.25 + (float64(len(errMsg)%15) / 100.0)
	}

	return 0.15 // Unknown failure
}

// calculateRecipeStrength returns 0.0-1.0 based on operation complexity and success
func (a *DreamAnalyzer) calculateRecipeStrength(toolName string, input, output map[string]any) float64 {
	success, hasSuccess := output["success"].(bool)
	if !hasSuccess || !success {
		return 0
	}

	// Complex multi-step operations score higher
	complexityScore := 0.0

	complexTools := map[string]float64{
		"semantic_grep": 0.9,
		"FSRead":        0.7,
		"multi_tool":    0.95,
		"evaluate":      0.85,
		"Edit":          0.6,
		"Write":         0.55,
	}

	if score, ok := complexTools[toolName]; ok {
		complexityScore = score
	} else {
		complexityScore = 0.3 // Base for other tools
	}

	// Check duration - longer successful operations are more valuable
	durationMs, hasDuration := output["duration_ms"].(float64)
	if hasDuration {
		// Bonus for taking significant time (2-10 seconds optimal)
		timeBonus := 0.0
		if durationMs > 2000 && durationMs < 10000 {
			timeBonus = 0.15
		} else if durationMs >= 10000 {
			timeBonus = 0.10 // Diminishing returns for very long ops
		}
		complexityScore += timeBonus
	}

	// Check for quality indicators in output
	if stdout, ok := output["stdout"].(string); ok && len(stdout) > 100 {
		complexityScore += 0.05
	}

	// Add some variance based on input complexity
	inputComplexity := 0.0
	for k, v := range input {
		if len(k) > 5 {
			inputComplexity += 0.01
		}
		if s, ok := v.(string); ok && len(s) > 20 {
			inputComplexity += 0.02
		}
	}
	complexityScore += math.Min(inputComplexity, 0.1)

	return math.Min(complexityScore, 1.0)
}

// calculateRelationshipStrength returns 0.0-1.0 based on file connectivity
func (a *DreamAnalyzer) calculateRelationshipStrength(toolName string, input, output map[string]any) float64 {
	if toolName != "FSRead" && toolName != "Read" && toolName != "semantic_grep" {
		return 0
	}

	content := ""
	if c, ok := output["content"].(string); ok {
		content = c
	} else if c, ok := output["stdout"].(string); ok {
		content = c
	}

	if content == "" {
		return 0
	}

	// Count actual file references using a regex that matches path-like patterns
	// (e.g., "path/to/file.go", "./config.yaml") rather than naive substring
	// counting which inflates scores on documentation and prose.
	codeExts := map[string]float64{
		".go": 1.2, ".ts": 1.2, ".tsx": 1.0, ".js": 1.0, ".jsx": 1.0,
		".json": 0.8, ".yaml": 0.8, ".yml": 0.8, ".md": 0.5,
	}

	matches := fileRefPattern.FindAllStringSubmatch(content, -1)
	fileRefs := 0.0
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		ref := m[1]
		for ext, weight := range codeExts {
			if strings.HasSuffix(ref, ext) {
				fileRefs += weight
				break
			}
		}
	}

	// Count import/require statements (only at line start for precision)
	importPatterns := []string{"import ", "require(", "from \"", "from '", "#include"}
	importCount := 0
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		for _, pattern := range importPatterns {
			if strings.HasPrefix(trimmed, pattern) {
				importCount++
				break
			}
		}
	}

	// Calculate relationship score
	score := 0.0
	if fileRefs > 3 {
		score = 0.4 + math.Min((fileRefs-3)*0.08, 0.4) // 0.4 to 0.8
	}
	if importCount > 2 {
		score += math.Min(float64(importCount)*0.05, 0.2) // Up to +0.2
	}

	return math.Min(score, 1.0)
}

// calculateInsightQuality returns 0.0-1.0 based on metric quality
func (a *DreamAnalyzer) calculateInsightQuality(toolName string, input, output map[string]any) float64 {
	insightTools := map[string]bool{
		"evaluate":      true,
		"benchmark":     true,
		"experiment":    true,
		"semantic_grep": true,
	}

	if !insightTools[toolName] {
		return 0
	}

	score := 0.0

	// PGR (Performance Gap Recovery) is highly valuable
	if pgr, ok := output["pgr"].(float64); ok {
		if pgr > 0.5 {
			score = 0.7 + (pgr-0.5)*0.6 // 0.7 to 1.0
		} else if pgr > 0.2 {
			score = 0.4 + pgr*0.6 // 0.4 to 0.7
		} else {
			score = 0.2 + pgr // 0.2 to 0.4
		}
	}

	// Accuracy metrics
	if accuracy, ok := output["accuracy"].(float64); ok && accuracy > 0.7 {
		score = math.Max(score, 0.5+(accuracy-0.7)*1.67) // 0.5 to 1.0 for 0.7-1.0 accuracy
	}

	// F1 score
	if f1, ok := output["f1"].(float64); ok && f1 > 0.6 {
		score = math.Max(score, 0.45+(f1-0.6)*1.375)
	}

	// Precision/Recall
	if precision, ok := output["precision"].(float64); ok && precision > 0.7 {
		score = math.Max(score, 0.4+(precision-0.7)*2.0)
	}

	return math.Min(score, 1.0)
}

func (a *DreamAnalyzer) extractRecipe(toolName string, input, output map[string]any, strength float64) string {
	quality := ""
	if strength > 0.8 {
		quality = "excellent"
	} else if strength > 0.6 {
		quality = "good"
	} else {
		quality = "solid"
	}

	if query, ok := input["query"].(string); ok {
		return fmt.Sprintf("%s: '%s' pattern works (%s, strength %.2f)", toolName, query, quality, strength)
	}
	if pattern, ok := input["pattern"].(string); ok {
		return fmt.Sprintf("%s: '%s' search pattern succeeded (%s, strength %.2f)", toolName, pattern, quality, strength)
	}
	return fmt.Sprintf("%s succeeded with these parameters (quality: %s, strength: %.2f)", toolName, quality, strength)
}

func (a *DreamAnalyzer) extractRelationship(input, output map[string]any, strength float64) string {
	filePath, _ := input["file_path"].(string)
	if filePath == "" {
		filePath, _ = input["path"].(string)
	}

	content, _ := output["content"].(string)

	// Extract mentioned file types
	var mentioned []string
	extMap := map[string]string{
		".go": "Go", ".ts": "TypeScript", ".js": "JavaScript",
		".tsx": "TSX", ".json": "JSON", ".yaml": "YAML", ".md": "Markdown",
	}

	for ext, name := range extMap {
		if strings.Contains(content, ext) {
			mentioned = append(mentioned, name)
		}
	}

	// No detected file types = no real relationship to report. Suppress
	// rather than emit "relates to []" noise.
	if len(mentioned) == 0 {
		return ""
	}

	strengthDesc := ""
	if strength > 0.8 {
		strengthDesc = "strong"
	} else if strength > 0.5 {
		strengthDesc = "moderate"
	} else {
		strengthDesc = "weak"
	}

	return fmt.Sprintf("Reading %s relates to %v (connection: %s, strength: %.2f)",
		filePath, mentioned, strengthDesc, strength)
}

func (a *DreamAnalyzer) extractInsight(input, output map[string]any, quality float64) string {
	qualityDesc := ""
	if quality > 0.9 {
		qualityDesc = "exceptional"
	} else if quality > 0.75 {
		qualityDesc = "high-quality"
	} else if quality > 0.6 {
		qualityDesc = "notable"
	} else {
		qualityDesc = "moderate"
	}

	if pgr, ok := output["pgr"].(float64); ok {
		return fmt.Sprintf("%s insight: PGR discovery at %.2f%% (quality: %.2f)", qualityDesc, pgr*100, quality)
	}
	if accuracy, ok := output["accuracy"].(float64); ok {
		return fmt.Sprintf("%s insight: High accuracy pattern at %.2f%% (quality: %.2f)", qualityDesc, accuracy*100, quality)
	}
	return fmt.Sprintf("%s insight from analysis (quality: %.2f)", qualityDesc, quality)
}

// SteeringAnalyzer extracts findings that should influence immediate decisions.
// These are fed to the steering system, not stored as long-term memories.
// Priority scoring is deferred to the LLM via FindingsAnalysisHook.
type SteeringAnalyzer struct{}

func NewSteeringAnalyzer() *SteeringAnalyzer {
	return &SteeringAnalyzer{}
}

func (a *SteeringAnalyzer) Name() string {
	return "steering"
}

// Analyze decides what should influence current decision-making.
// Tags findings but does NOT score priority -- the agent does that.
func (a *SteeringAnalyzer) Analyze(ctx context.Context, toolName string, input, output map[string]any) (findings.AnalysisResult, error) {
	result := findings.AnalysisResult{
		ShouldCapture: false,
		Priority:      0, // Unscored -- LLM will assign real priority
	}

	// Pattern 1: Tool inefficiency detected
	if inefficiencyScore := a.calculateInefficiency(toolName, input, output); inefficiencyScore > 0 {
		result.ShouldCapture = true
		result.Tags = append(result.Tags, "inefficiency", "steering")
		result.Insights = append(result.Insights, a.suggestBetterApproach(toolName, input, inefficiencyScore))
	}

	// Pattern 2: Context window pressure
	if pressureScore := a.calculateContextPressure(toolName, input, output); pressureScore > 0 {
		result.ShouldCapture = true
		result.Tags = append(result.Tags, "context-pressure", "steering")
		result.Insights = append(result.Insights,
			fmt.Sprintf("Context pressure at %.0f%% - consider file-specific queries instead of broad reads", pressureScore*100))
	}

	// Pattern 3: Repeated similar operations (potential batching opportunity).
	// Threshold raised from >0 to >=0.6 after the 2026-04-27 survey observed
	// "Batching opportunity (50% confidence)" on innocuous 3-op discovery
	// chains (echo "===" ; ls ; cat). At that confidence the hook is almost
	// certainly wrong; only fire when we're meaningfully sure (0.6+).
	if batchDetail := a.calculateBatchDetail(toolName, input, output); batchDetail.score >= 0.6 {
		result.ShouldCapture = true
		result.Tags = append(result.Tags, "batch-opportunity", "steering")
		result.Insights = append(result.Insights,
			fmt.Sprintf("Batching opportunity (%.0f%% confidence): %s. Consider combining into a single operation or script.",
				batchDetail.score*100, batchDetail.reason))
	}

	return result, nil
}

// calculateInefficiency returns 0.0-1.0 based on how inefficient the operation was
func (a *SteeringAnalyzer) calculateInefficiency(toolName string, input, output map[string]any) float64 {
	// Long duration with simple result
	durationMs, hasDuration := output["duration_ms"].(float64)
	if !hasDuration || durationMs < 1000 {
		return 0
	}

	inefficiencyScore := 0.0

	// Complex bash commands that could be semantic_grep
	if toolName == "Bash" || toolName == "bash" {
		cmd, _ := input["command"].(string)
		lowerCmd := strings.ToLower(cmd)

		// Recursive grep is highly inefficient
		if strings.Contains(lowerCmd, "grep -r") || strings.Contains(lowerCmd, "grep -R") {
			inefficiencyScore = 0.8 + math.Min((durationMs-1000)/10000.0, 0.15) // 0.8-0.95
		} else if strings.Contains(lowerCmd, "grep") {
			inefficiencyScore = 0.5 + math.Min((durationMs-1000)/8000.0, 0.3) // 0.5-0.8
		}

		// Find commands
		if strings.Contains(lowerCmd, "find") && strings.Contains(lowerCmd, "-name") {
			inefficiencyScore = 0.6 + math.Min((durationMs-1000)/9000.0, 0.25) // 0.6-0.85
		}
	}

	return math.Min(inefficiencyScore, 1.0)
}

func (a *SteeringAnalyzer) suggestBetterApproach(toolName string, input map[string]any, inefficiency float64) string {
	severity := ""
	if inefficiency > 0.8 {
		severity = "Critical"
	} else if inefficiency > 0.6 {
		severity = "Significant"
	} else if inefficiency > 0.4 {
		severity = "Moderate"
	} else {
		severity = "Minor"
	}

	if toolName == "Bash" || toolName == "bash" {
		cmd, _ := input["command"].(string)
		if strings.Contains(cmd, "grep -r") || strings.Contains(cmd, "grep -R") {
			return fmt.Sprintf("%s inefficiency (%.2f): Consider semantic_grep instead of recursive bash grep", severity, inefficiency)
		}
		if strings.Contains(cmd, "find") && strings.Contains(cmd, "-name") {
			return fmt.Sprintf("%s inefficiency (%.2f): Consider FSRead with file discovery instead of bash find", severity, inefficiency)
		}
	}
	return fmt.Sprintf("%s inefficiency detected (%.2f) - consider alternatives", severity, inefficiency)
}

// calculateContextPressure returns 0.0-1.0 based on token usage pressure
func (a *SteeringAnalyzer) calculateContextPressure(toolName string, input, output map[string]any) float64 {
	tokens, hasTokens := output["tokens_used"].(float64)
	if !hasTokens {
		// Try other token fields
		if t, ok := output["token_count"].(float64); ok {
			tokens = t
		} else if t, ok := output["tokens"].(float64); ok {
			tokens = t
		} else {
			return 0
		}
	}

	// Large file reads cause context pressure
	if toolName == "FSRead" || toolName == "Read" || toolName == "FSList" {
		// Scale: 5000 tokens = 0.5, 10000 tokens = 0.9
		if tokens > 5000 {
			pressure := 0.5 + math.Min((tokens-5000)/11111.0, 0.45) // 0.5 to ~0.95
			return math.Min(pressure, 1.0)
		}
	}

	// Other tools with high token usage
	if tokens > 3000 {
		return 0.4 + math.Min((tokens-3000)/14000.0, 0.5) // 0.4 to ~0.9
	}

	return 0
}

// batchDetail holds the score and human-readable reason for a batching opportunity.
type batchDetail struct {
	score  float64
	reason string
}

// calculateBatchDetail returns a batchDetail with both score and specific reason.
// It supersedes calculateBatchOpportunity by threading context into the message
// so the agent knows *what* to batch, not just that something looks similar.
func (a *SteeringAnalyzer) calculateBatchDetail(toolName string, input, output map[string]any) batchDetail {
	best := batchDetail{}

	// Pattern 1: Tool output lists multiple files → could batch file operations
	if files, ok := output["files"].([]any); ok && len(files) > 3 {
		score := 0.3 + math.Min(float64(len(files)-3)*0.1, 0.5)
		if score > best.score {
			best = batchDetail{
				score:  score,
				reason: fmt.Sprintf("tool returned %d files — consider processing them in a single operation", len(files)),
			}
		}
	}

	// Pattern 2: Bash command with many chained operations on similar targets
	if toolName == "Bash" || toolName == "bash" {
		if cmd, ok := input["command"].(string); ok {
			separators := strings.Count(cmd, "&&") + strings.Count(cmd, ";")
			if separators > 2 {
				score := 0.4 + math.Min(float64(separators-2)*0.1, 0.4)
				if score > best.score {
					best = batchDetail{
						score:  score,
						reason: fmt.Sprintf("Bash command chains %d operations (%s) — consider a script or single compound command", separators, truncateForDetail(cmd, 60)),
					}
				}
			}
			if strings.Contains(cmd, "for ") && strings.Contains(cmd, "do ") {
				score := 0.5
				if score > best.score {
					best = batchDetail{
						score:  score,
						reason: fmt.Sprintf("shell for-loop detected (%s) — already iterative; consider xargs/parallel for efficiency", truncateForDetail(cmd, 60)),
					}
				}
			}
		}
	}

	// Pattern 3: Read/Grep results reference many files — could batch
	if toolName == "Read" || toolName == "FSRead" || toolName == "semantic_grep" {
		if results, ok := output["results"].([]any); ok && len(results) > 5 {
			score := 0.3 + math.Min(float64(len(results)-5)*0.05, 0.4)
			if score > best.score {
				best = batchDetail{
					score:  score,
					reason: fmt.Sprintf("%s returned %d results — consider narrowing the query or batching reads", toolName, len(results)),
				}
			}
		}
	}

	best.score = math.Min(best.score, 1.0)
	return best
}

// truncateForDetail shortens a string for inclusion in insight messages.
func truncateForDetail(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// isDiscoveryNonZero detects the "command returned non-zero because it didn't
// find what it was looking for" case. These are NOT errors in the system-health
// sense — they are the normal exit semantics of which/grep/find/test:
//
//   - `which foo` exits 1 or 127 when foo is not on PATH.
//   - `grep needle haystack` exits 1 when there's no match.
//   - `find -name foo` exits 0 but prints nothing on misses; `find ... -true`
//     patterns can exit 1.
//   - `command -v foo` exits 1 on miss.
//   - `test -f foo` exits 1 when the file is absent.
//
// Treating these as anti-pattern tagged findings dragged the Bash avoidance
// score up on every healthy discovery turn and created "Avoid: Bash - error
// severity 0.27" system reminders that trained agents to avoid perfectly valid
// exploration idioms (2026-04-27 survey). Stay silent on these.
func isDiscoveryNonZero(toolName string, input, output map[string]any) bool {
	if toolName != "Bash" && toolName != "bash" && toolName != "shell" {
		return false
	}
	cmd, _ := input["command"].(string)
	if cmd == "" {
		return false
	}

	// Look at the first token of the command only. If the user is running
	// a composite `X || Y` we only suppress when the primary command is a
	// discovery verb — we don't want to suppress real failures that happen
	// to be preceded by a which.
	trimmed := strings.TrimSpace(cmd)
	first := trimmed
	if idx := strings.IndexAny(trimmed, " \t"); idx > 0 {
		first = trimmed[:idx]
	}
	// Strip path prefix (/usr/bin/which → which).
	if slash := strings.LastIndex(first, "/"); slash >= 0 {
		first = first[slash+1:]
	}

	discoveryVerbs := map[string]bool{
		"which": true, "command": true, "type": true, "hash": true,
		"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true,
		"find": true, "locate": true, "fd": true,
		"test": true, "[": true,
		"ls": true,
	}
	if !discoveryVerbs[first] {
		return false
	}

	// Sanity check: if the output carries a real error string (permission
	// denied, panic, segfault), don't suppress even if the verb is discovery.
	if errStr, _ := output["error"].(string); errStr != "" {
		low := strings.ToLower(errStr)
		for _, hard := range []string{"permission denied", "access denied", "segmentation fault", "panic:", "killed"} {
			if strings.Contains(low, hard) {
				return false
			}
		}
	}
	return true
}
