package chat

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// GetSkillsManager returns the skills manager (for headless mode)
func (sdk *SDKIntegration) GetSkillsManager() *SkillsManager {
	return sdk.skillsManager
}

// SetSkillsManager sets the skills manager
func (sdk *SDKIntegration) SetSkillsManager(sm *SkillsManager) {
	if sdk == nil || sdk.harnessGoverned() {
		return
	}
	sdk.skillsManager = sm
}

// GetPluginsManager returns the plugins manager (for headless mode)
func (sdk *SDKIntegration) GetPluginsManager() *PluginsManager {
	return sdk.pluginsManager
}

// SetPluginsManager sets the plugins manager
func (sdk *SDKIntegration) SetPluginsManager(pm *PluginsManager) {
	if sdk == nil || sdk.harnessGoverned() {
		return
	}
	sdk.pluginsManager = pm
}

// GetSkillsPromptContext returns skill context XML for system prompt injection.
// This includes available skills, active skills, and active skill instructions.
// Returns empty string if skills manager is not initialized.
func (sdk *SDKIntegration) GetSkillsPromptContext() string {
	return sdk.GetSkillsPromptContextForQuery("")
}

// GetSkillsPromptContextForQuery ranks available skills for the current request
// while preserving active-skill instructions and prompt-size bounds.
func (sdk *SDKIntegration) GetSkillsPromptContextForQuery(query string) string {
	if sdk == nil || sdk.harnessGoverned() || sdk.skillsManager == nil || !sdk.skillsManager.IsInitialized() {
		return ""
	}
	return sdk.skillsManager.GetPromptContextForQuery(query).String()
}

// buildActivationContext creates a skill activation context from current state.
// This is used for auto-activating skills based on context triggers.
func (sdk *SDKIntegration) buildActivationContext(userMessage string) skills.ActivationContext {
	return skills.ActivationContext{
		CurrentFile: sdk.getCurrentFile(),
		CurrentTool: "",
		CurrentMode: sdk.GetOperatingMode(),
		Keywords:    extractKeywords(userMessage),
		Message:     strings.ToLower(userMessage),
		ProjectType: sdk.detectProjectType(),
	}
}

// getCurrentFile returns the current file being edited if available.
// This is used for skill activation based on file patterns.
func (sdk *SDKIntegration) getCurrentFile() string {
	// Try to get from workspace root context
	if sdk.workspaceRoot != "" {
		// For now, return empty - in future could track active file from tools
		return ""
	}
	return ""
}

// detectProjectType attempts to detect the project type from the workspace.
// Returns a string like "go", "typescript", "python", etc.
func (sdk *SDKIntegration) detectProjectType() string {
	if sdk.workspaceRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return ""
		}
		sdk.workspaceRoot = cwd
	}

	// Check for common project markers
	projectMarkers := map[string]string{
		"go.mod":           "go",
		"package.json":     "javascript",
		"tsconfig.json":    "typescript",
		"Cargo.toml":       "rust",
		"requirements.txt": "python",
		"pyproject.toml":   "python",
		"setup.py":         "python",
		"pom.xml":          "java",
		"build.gradle":     "java",
		"Gemfile":          "ruby",
		"composer.json":    "php",
	}

	for file, projectType := range projectMarkers {
		if _, err := os.Stat(filepath.Join(sdk.workspaceRoot, file)); err == nil {
			return projectType
		}
	}

	return ""
}

// extractKeywords extracts significant keywords from a user message.
// These keywords are used for skill activation based on keyword triggers.
func extractKeywords(message string) []string {
	// Normalize the message
	message = strings.ToLower(message)

	// Define significant keywords that might trigger skills
	significantKeywords := []string{
		// Development tasks
		"test", "tests", "testing", "debug", "debugging", "build",
		"deploy", "deployment", "ci", "cd", "pipeline",
		// Languages/frameworks
		"go", "golang", "rust", "python", "javascript", "typescript",
		"react", "vue", "angular", "node", "express",
		// Tools
		"docker", "kubernetes", "k8s", "git", "github", "gitlab",
		"terraform", "aws", "azure", "gcp", "cloud",
		// Actions
		"refactor", "review", "optimize", "fix", "implement", "create",
		"update", "delete", "migrate", "upgrade",
		// Documentation
		"document", "documentation", "readme", "api", "spec",
		// Data
		"database", "sql", "nosql", "redis", "postgres", "mongodb",
		// Security
		"security", "auth", "authentication", "authorization", "oauth",
		// File types
		"pdf", "csv", "json", "yaml", "xml", "markdown",
		// Swarm / agent concepts
		"workflow", "workflows", "swarm", "agent", "agents",
		"skill", "skills", "orchestrat", "conduct", "parallel",
	}

	var found []string
	for _, keyword := range significantKeywords {
		if strings.Contains(message, keyword) {
			found = append(found, keyword)
		}
	}

	return found
}

// AutoActivateSkills triggers skill auto-activation based on context.
// Should be called before building the prompt for each request.
func (sdk *SDKIntegration) AutoActivateSkills(userMessage string) []*skills.Skill {
	if sdk == nil || sdk.harnessGoverned() || sdk.skillsManager == nil || !sdk.skillsManager.IsInitialized() {
		return nil
	}

	ctx := sdk.buildActivationContext(userMessage)
	activated := sdk.skillsManager.AutoActivate(ctx)

	if len(activated) > 0 {
		names := make([]string, len(activated))
		for i, s := range activated {
			names[i] = s.Metadata.Name
		}
		logDebug("[SDK] Auto-activated skills: %v", names)
	}

	return activated
}

// InjectSkillsContext injects skill context into the current system prompt.
// Returns true if skills were injected, false otherwise.
func (sdk *SDKIntegration) InjectSkillsContext() bool {
	if sdk == nil || sdk.harnessGoverned() || sdk.skillsManager == nil || !sdk.skillsManager.IsInitialized() {
		return false
	}
	if sdk.IsCodexBacked() {
		return false
	}

	skillsContext := sdk.GetSkillsPromptContext()
	if skillsContext == "" {
		return false
	}

	// Get current prompt and prepend skills context
	currentPrompt := sdk.activeAgent().SystemPrompt()
	enhancedPrompt := skillsContext + currentPrompt
	sdk.activeAgent().SetSystemPrompt(enhancedPrompt)
	// Provenance: this is one of the bugs that motivated tracing — the
	// "available_skills" block gets prepended without a separator before the
	// base prompt. The banner will now show this exact contribution and the
	// caller's file:line so operators can correlate the malformed boundary
	// with the responsible site.
	sdk.recordStartupPromptContribution("available_skills", len(skillsContext))

	logDebug("[SDK] Injected skills context: %d chars", len(skillsContext))
	return true
}

// getActiveSkillKeywords returns deduplicated keyword strings from keyword-type
// triggers across all currently active skills.  Used to drive real-time input
// highlighting so the user sees which words in their message will fire
// skill auto-activation.  Multi-word patterns are split on spaces so each
// word is highlighted independently; words shorter than 3 runes are skipped.
func getActiveSkillKeywords(mgr *SkillsManager) []string {
	if mgr == nil || !mgr.IsInitialized() {
		return nil
	}
	activeSkills := mgr.GetActiveSkills()
	seen := make(map[string]struct{})
	for _, skill := range activeSkills {
		for _, trigger := range skill.Metadata.Triggers {
			if trigger.Type != "keyword" {
				continue
			}
			for word := range strings.FieldsSeq(trigger.Pattern) {
				w := strings.ToLower(word)
				if len([]rune(w)) >= 3 {
					seen[w] = struct{}{}
				}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for w := range seen {
		result = append(result, w)
	}
	return result
}
