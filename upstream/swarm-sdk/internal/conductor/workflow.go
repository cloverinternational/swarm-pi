package conductor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// WorkflowDef is the parsed content of a WORKFLOW.md file: typed configuration
// from YAML front-matter plus a Go template string from the Markdown body.
type WorkflowDef struct {
	Config         WorkflowConfig
	PromptTemplate string // raw Go template source from the Markdown body
	FilePath       string // absolute path of the source file (for reload)
}

// WorkflowConfig holds the typed runtime values derived from WORKFLOW.md
// front-matter.  It mirrors Symphony SPEC §5.3 with the tracker.kind field
// extended to support "forgejo" and "github" in addition to "linear".
type WorkflowConfig struct {
	Tracker TrackerConfig `yaml:"tracker"`
	Polling struct {
		IntervalMs int `yaml:"interval_ms"`
	} `yaml:"polling"`
	Workspace struct {
		Root string `yaml:"root"`
	} `yaml:"workspace"`
	Hooks struct {
		AfterCreate  string `yaml:"after_create"`
		BeforeRun    string `yaml:"before_run"`
		AfterRun     string `yaml:"after_run"`
		BeforeRemove string `yaml:"before_remove"`
		TimeoutMs    int    `yaml:"timeout_ms"`
	} `yaml:"hooks"`
	Agent struct {
		MaxConcurrentAgents int `yaml:"max_concurrent_agents"`
		MaxTurns            int `yaml:"max_turns"`
		MaxRetryBackoffMs   int `yaml:"max_retry_backoff_ms"`
	} `yaml:"agent"`
}

// TrackerConfig holds the issue-tracker connection settings.
type TrackerConfig struct {
	Kind           string   `yaml:"kind"`     // "forgejo" | "github" | "linear"
	Endpoint       string   `yaml:"endpoint"` // base URL for forgejo; omit for github
	APIKey         string   `yaml:"api_key"`  // literal or "$VAR"
	Repo           string   `yaml:"repo"`     // "owner/repo"
	ActiveLabels   []string `yaml:"active_labels"`
	TerminalLabels []string `yaml:"terminal_labels"`
}

// LoadWorkflow reads and parses a WORKFLOW.md file from path.
// It resolves $VAR references in api_key and workspace.root.
// Returns an error if the file is missing, the YAML is malformed, or required
// fields (tracker.kind, tracker.api_key) are absent after resolution.
func LoadWorkflow(path string) (*WorkflowDef, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("workflow: resolve path %q: %w", path, err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("workflow: read %q: %w", abs, err)
	}
	return parseWorkflow(data, abs)
}

// parseWorkflow is the core parser; exposed separately for testing.
func parseWorkflow(data []byte, absPath string) (*WorkflowDef, error) {
	cfg, body, err := splitFrontMatter(data)
	if err != nil {
		return nil, fmt.Errorf("workflow: parse front-matter: %w", err)
	}

	// Apply defaults.
	applyDefaults(&cfg)

	// Resolve environment variable references.
	cfg.Tracker.APIKey = resolveEnvVar(cfg.Tracker.APIKey)
	cfg.Workspace.Root = resolveEnvPath(cfg.Workspace.Root, filepath.Dir(absPath))

	def := &WorkflowDef{
		Config:         cfg,
		PromptTemplate: strings.TrimSpace(string(body)),
		FilePath:       absPath,
	}
	return def, nil
}

// BuildPrompt renders the prompt template for a specific issue and attempt.
// issue is passed as the {{.Issue}} template variable.
// attempt is nil on the first run and an integer on retries.
func (d *WorkflowDef) BuildPrompt(issue Issue, attempt *int) (string, error) {
	src := d.PromptTemplate
	if src == "" {
		// Minimal default prompt when body is empty.
		src = "You are working on {{.Issue.Identifier}}: {{.Issue.Title}}"
	}
	t, err := template.New("workflow").Parse(src)
	if err != nil {
		return "", fmt.Errorf("workflow: parse template: %w", err)
	}
	data := map[string]any{
		"Issue":   issue,
		"Attempt": attempt,
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("workflow: render template: %w", err)
	}
	return buf.String(), nil
}

// ── front-matter parsing ──────────────────────────────────────────────────────

// splitFrontMatter splits a Markdown file into a WorkflowConfig (from YAML
// front-matter between --- delimiters) and the remaining body bytes.
func splitFrontMatter(data []byte) (WorkflowConfig, []byte, error) {
	var cfg WorkflowConfig
	s := string(data)
	if !strings.HasPrefix(s, "---") {
		// No front-matter — treat entire file as body.
		return cfg, data, nil
	}
	// Find the closing ---
	rest := s[3:]
	before, after, ok := strings.Cut(rest, "\n---")
	if !ok {
		return cfg, nil, fmt.Errorf("missing closing '---' for front-matter")
	}
	yamlPart := before
	body := after // skip "\n---"
	if err := yaml.Unmarshal([]byte(yamlPart), &cfg); err != nil {
		return cfg, nil, fmt.Errorf("YAML: %w", err)
	}
	return cfg, []byte(body), nil
}

// applyDefaults fills in default values for omitted fields.
func applyDefaults(cfg *WorkflowConfig) {
	if cfg.Polling.IntervalMs == 0 {
		cfg.Polling.IntervalMs = 30_000
	}
	if cfg.Hooks.TimeoutMs == 0 {
		cfg.Hooks.TimeoutMs = 60_000
	}
	if cfg.Agent.MaxConcurrentAgents == 0 {
		cfg.Agent.MaxConcurrentAgents = 10
	}
	if cfg.Agent.MaxTurns == 0 {
		cfg.Agent.MaxTurns = 20
	}
	if cfg.Agent.MaxRetryBackoffMs == 0 {
		cfg.Agent.MaxRetryBackoffMs = 300_000
	}
	if len(cfg.Tracker.ActiveLabels) == 0 {
		cfg.Tracker.ActiveLabels = DefaultActiveLabels
	}
	if len(cfg.Tracker.TerminalLabels) == 0 {
		cfg.Tracker.TerminalLabels = DefaultTerminalLabels
	}
}

// resolveEnvVar expands a "$VAR" reference to the corresponding environment
// variable.  Returns the original string if it does not start with "$".
func resolveEnvVar(s string) string {
	if strings.HasPrefix(s, "$") {
		return os.Getenv(s[1:])
	}
	return s
}

// resolveEnvPath expands "$VAR" and "~" in a path value, then resolves
// relative paths against base.
func resolveEnvPath(s, base string) string {
	s = resolveEnvVar(s)
	if strings.HasPrefix(s, "~/") {
		home, _ := os.UserHomeDir()
		s = filepath.Join(home, s[2:])
	}
	if s == "" {
		return s
	}
	if !filepath.IsAbs(s) {
		s = filepath.Join(base, s)
	}
	return filepath.Clean(s)
}
