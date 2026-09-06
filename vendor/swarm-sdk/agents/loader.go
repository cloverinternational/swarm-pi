// Package agents loads and resolves agent definitions from embedded defaults,
// user-scoped files (~/.swarmos/agents/*.md), and project-scoped files
// (./.swarm/agents/*.md).
//
// Resolution precedence (highest wins on ID collision):
//
//  1. Project:  <projectDir>/agents/*.md
//  2. User:     <userDir>/agents/*.md   (typically ~/.swarmos/agents)
//  3. Embedded: binary-embedded defaults
package agents

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Swarm-Code/mono/swarm-sdk/agent"
)

//go:embed embedded/*.md
var embeddedFS embed.FS

// loadEmbedded returns all agent definitions from the embedded FS.
func loadEmbedded() ([]*agent.Definition, error) {
	entries, err := embeddedFS.ReadDir("embedded")
	if err != nil {
		return nil, fmt.Errorf("agents: read embedded dir: %w", err)
	}

	var defs []*agent.Definition
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := embeddedFS.ReadFile("embedded/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("agents: read embedded %s: %w", e.Name(), err)
		}
		def, err := parseFrontmatter(data)
		if err != nil {
			return nil, fmt.Errorf("agents: parse embedded %s: %w", e.Name(), err)
		}
		defs = append(defs, def)
	}
	return defs, nil
}

// loadDir reads all *.md agent definition files from dir.
// Missing or unreadable directories are silently skipped (returns empty slice).
func loadDir(dir string) ([]*agent.Definition, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // directory doesn't exist — not an error
		}
		return nil, fmt.Errorf("agents: read dir %s: %w", dir, err)
	}

	var defs []*agent.Definition
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			// Skip unreadable files rather than aborting the whole load.
			continue
		}
		def, err := parseFrontmatter(data)
		if err != nil || def == nil || def.ID == "" {
			// Skip malformed files silently.
			continue
		}
		defs = append(defs, def)
	}
	return defs, nil
}

// merge builds a map[id]*Definition applying precedence: later slices win.
// Call order: merge(embedded, user, project) so project has highest priority.
func merge(layers ...[]*agent.Definition) map[string]*agent.Definition {
	out := make(map[string]*agent.Definition)
	for _, layer := range layers {
		for _, def := range layer {
			if def != nil && def.ID != "" {
				out[def.ID] = def
			}
		}
	}
	return out
}

// parseFrontmatter splits a markdown file into YAML front matter and body,
// then unmarshals the front matter into an agent.Definition and sets
// SystemPrompt to the body text (trimmed).
//
// Front matter must be delimited by lines containing only "---".
// Files without a closing delimiter are treated as body-only (no front matter).
func parseFrontmatter(data []byte) (*agent.Definition, error) {
	const delim = "---"

	// Normalise line endings.
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))

	// Must start with "---\n"
	if !bytes.HasPrefix(data, []byte(delim+"\n")) {
		// No front matter — treat whole file as system prompt with empty def.
		def := &agent.Definition{
			SystemPrompt: strings.TrimSpace(string(data)),
		}
		return def, nil
	}

	// Find closing "---"
	rest := data[len(delim)+1:] // strip opening "---\n"
	idx := bytes.Index(rest, []byte("\n"+delim))
	if idx < 0 {
		// No closing delimiter — treat whole content as system prompt.
		def := &agent.Definition{
			SystemPrompt: strings.TrimSpace(string(data)),
		}
		return def, nil
	}

	yamlBytes := rest[:idx]
	body := rest[idx+len("\n"+delim):]
	// Skip the newline immediately after the closing "---"
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	}

	var def agent.Definition
	if err := yaml.Unmarshal(yamlBytes, &def); err != nil {
		return nil, fmt.Errorf("yaml unmarshal: %w", err)
	}

	// Body becomes the system prompt (trim trailing whitespace).
	def.SystemPrompt = strings.TrimSpace(string(body))

	// Normalise the "inherit" sentinel — treat it as empty so the tool's
	// getProviderConfig falls through to the roleModelSelector / profile chain.
	if strings.EqualFold(strings.TrimSpace(def.Model), "inherit") {
		def.Model = ""
	}

	// Ensure sub_agent metadata so factory Step 8.5 configures auto-compaction.
	if def.Metadata == nil {
		def.Metadata = make(map[string]any)
	}
	if _, ok := def.Metadata["type"]; !ok {
		def.Metadata["type"] = "sub_agent"
	}

	return &def, nil
}
