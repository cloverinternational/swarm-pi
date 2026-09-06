package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// Registry manages skill discovery, loading, and activation.
type Registry struct {
	mu sync.RWMutex

	// Loaded skills by name
	skills map[string]*Skill

	// Active skills for current context
	active map[string]bool

	// Search paths for skill discovery
	searchPaths []string

	// Remote registries for plugin discovery
	remotes []RemoteRegistry

	// Cache for search results
	searchCache map[string][]SkillSearchResult
	cacheExpiry time.Duration
}

// RemoteRegistry represents a remote skill/plugin registry.
type RemoteRegistry struct {
	// Name of the registry
	Name string `json:"name" yaml:"name"`

	// URL of the registry
	URL string `json:"url" yaml:"url"`

	// Type: "github", "http", "local"
	Type string `json:"type" yaml:"type"`

	// Enabled status
	Enabled bool `json:"enabled" yaml:"enabled"`

	// Priority for search ordering
	Priority int `json:"priority" yaml:"priority"`
}

// NewRegistry creates a new skill registry.
func NewRegistry() *Registry {
	return &Registry{
		skills:      make(map[string]*Skill),
		active:      make(map[string]bool),
		searchPaths: []string{},
		remotes:     []RemoteRegistry{},
		searchCache: make(map[string][]SkillSearchResult),
		cacheExpiry: 5 * time.Minute,
	}
}

// AddSearchPath adds a directory to search for skills.
func (r *Registry) AddSearchPath(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.searchPaths = append(r.searchPaths, path)
}

// AddRemoteRegistry adds a remote registry for plugin discovery.
func (r *Registry) AddRemoteRegistry(remote RemoteRegistry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.remotes = append(r.remotes, remote)
}

// LoadFromPath loads a single skill from a path.
func (r *Registry) LoadFromPath(path string) (*Skill, error) {
	skill, err := LoadSkill(path)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if skill.Metadata.Name == "" {
		skill.Metadata.Name = generateSkillName(path)
	}

	r.skills[skill.Metadata.Name] = skill
	skill.LoadedAt = time.Now()

	return skill, nil
}

// RegisterSkill registers a pre-built Skill directly, bypassing disk loading.
// This is used for built-in/embedded skills that don't live on the filesystem.
// If a skill with the same name already exists it is overwritten only when
// overwrite is true; otherwise the existing entry is left unchanged.
// Built-in skills registered via this method are tagged with Source="builtin"
// if their Source field is empty.
func (r *Registry) RegisterSkill(skill *Skill, overwrite bool) {
	if skill == nil || skill.Metadata.Name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.skills[skill.Metadata.Name]; exists && !overwrite {
		return
	}
	if skill.LoadedAt.IsZero() {
		skill.LoadedAt = time.Now()
	}
	// Tag built-in skills
	if skill.Source == "" {
		skill.Source = "builtin"
	}
	if skill.LoadedFrom == "" {
		skill.LoadedFrom = "builtin"
	}
	r.skills[skill.Metadata.Name] = skill
}

// DiscoverAll discovers and loads all skills from search paths.
// Sets the Source and LoadedFrom fields on each skill based on which
// search path it was discovered from, enabling UI grouping by source.
// Mirrors Claude Code's SkillsMenu.tsx grouping (policySettings, userSettings, etc).
func (r *Registry) DiscoverAll() ([]*Skill, error) {
	r.mu.RLock()
	paths := make([]string, len(r.searchPaths))
	copy(paths, r.searchPaths)
	r.mu.RUnlock()

	// Resolve home dir once for source classification
	homeDir, _ := os.UserHomeDir()

	var allSkills []*Skill

	for _, path := range paths {
		discovered, err := DiscoverSkills(path)
		if err != nil {
			continue
		}

		r.mu.Lock()
		for _, skill := range discovered {
			if skill.Metadata.Name == "" {
				skill.Metadata.Name = generateSkillName(skill.Path)
			}

			// Classify source based on the search path first (policy beats all),
			// then refine with the skill's actual directory path. This ensures:
			//   - Policy skills always keep their source (highest priority)
			//   - Autogen skills under ~/.swarm/skills/autogen/ get "autogen" even
			//     when the search path is ~/.swarm/skills (which maps to "user")
			pathSource := classifySource(path, homeDir)
			skillSource := classifySkillSource(skill.Path, homeDir)
			source := pathSource
			// Policy classification from the search path wins over everything.
			// Autogen classification from the skill path refines "user" or "local".
			if pathSource != "policy" && skillSource != "" {
				source = skillSource
			}
			if skill.Source == "" {
				skill.Source = source
			}
			if skill.LoadedFrom == "" {
				skill.LoadedFrom = source
			}

			r.skills[skill.Metadata.Name] = skill
			skill.LoadedAt = time.Now()
			allSkills = append(allSkills, skill)
		}
		r.mu.Unlock()
	}

	return allSkills, nil
}

// classifySkillSource classifies a skill's source based on its actual directory
// path (skill.Path), rather than the search path it was discovered from.
// This handles the case where autogen skills live under ~/.swarm/skills/autogen/
// but are discovered via the broader ~/.swarm/skills search path — the search
// path would classify them as "user", but their actual path reveals them as
// "autogen".
//
// Returns "" when the skill path doesn't match any known autogen pattern,
// signalling the caller to fall back to classifySource(searchPath, homeDir).
func classifySkillSource(skillPath, homeDir string) string {
	if skillPath == "" {
		return ""
	}
	normalized := filepath.Clean(skillPath)

	// Recognize the canonical directory and the legacy location while migration
	// is in progress. Include the explicit homeDir forms so classification stays
	// pure and testable when SWARM_HOME/HOME differs from the process default.
	autogenDirs := []string{paths.AutogenSkillsDir()}
	if homeDir != "" {
		autogenDirs = append(autogenDirs,
			filepath.Join(homeDir, ".swarm", "skills", "autogen"),
			filepath.Join(homeDir, ".swarmos", "skills", "autogen"),
		)
	}
	for _, dir := range autogenDirs {
		prefix := filepath.Clean(dir) + string(filepath.Separator)
		if strings.HasPrefix(normalized, prefix) {
			return "autogen"
		}
	}

	return ""
}

// classifySource determines the source classification for a skill
// based on the search path it was discovered from.
// Returns one of: "policy", "autogen", "user", "project", "builtin", or "local".
func classifySource(searchPath, homeDir string) string {
	// Normalize for comparison
	normalized := filepath.Clean(searchPath)

	// Managed/policy skills (SWARM_MANAGED_SKILLS_DIR)
	if os.Getenv("SWARM_MANAGED_SKILLS_DIR") != "" {
		if normalized == filepath.Clean(os.Getenv("SWARM_MANAGED_SKILLS_DIR")) {
			return "policy"
		}
	}

	// Autogen skills (~/.swarm/skills/autogen) — must be checked BEFORE the
	// user-level patterns below because ~/.swarm/skills is a user path and
	// ~/.swarm/skills/autogen is a suffix of it. Without this, autogen skills
	// discovered from the autogen dir get misclassified as "user" or "local",
	// making ListSkills() (which filters Source=="autogen") return nothing.
	autogenDirs := []string{paths.AutogenSkillsDir()}
	if homeDir != "" {
		autogenDirs = append(autogenDirs,
			filepath.Join(homeDir, ".swarm", "skills", "autogen"),
			filepath.Join(homeDir, ".swarmos", "skills", "autogen"),
		)
	}
	for _, dir := range autogenDirs {
		if normalized == filepath.Clean(dir) {
			return "autogen"
		}
	}

	// User-level Claude skills (~/.claude/skills, ~/.claude/commands).
	if homeDir != "" {
		userPatterns := []string{
			filepath.Join(homeDir, ".swarm", "skills"),
			filepath.Join(homeDir, ".swarmos", "skills"),
			filepath.Join(homeDir, ".claude", "skills"),
			filepath.Join(homeDir, ".claude", "commands"),
		}
		for _, p := range userPatterns {
			if normalized == filepath.Clean(p) {
				return "user"
			}
		}
	}
	// Canonical user-level Swarm skills (~/.swarm/skills == paths.SkillsDir()).
	if normalized == filepath.Clean(paths.SkillsDir()) {
		return "user"
	}

	// Project-level skills (.claude/skills, .swarm/skills within project)
	if strings.HasSuffix(normalized, filepath.Join(".claude", "skills")) ||
		strings.HasSuffix(normalized, filepath.Join(".claude", "commands")) ||
		strings.HasSuffix(normalized, filepath.Join(".swarm", "skills")) {
		return "project"
	}

	return "local"
}

// Get retrieves a loaded skill by name.
func (r *Registry) Get(name string) (*Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	skill, ok := r.skills[name]
	return skill, ok
}

// List returns all loaded skills.
func (r *Registry) List() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	skills := make([]*Skill, 0, len(r.skills))
	for _, skill := range r.skills {
		skills = append(skills, skill)
	}

	// Sort by priority then name
	sort.Slice(skills, func(i, j int) bool {
		if skills[i].Metadata.Priority != skills[j].Metadata.Priority {
			return skills[i].Metadata.Priority > skills[j].Metadata.Priority
		}
		return skills[i].Metadata.Name < skills[j].Metadata.Name
	})

	return skills
}

// ListByCategory returns skills in a specific category.
func (r *Registry) ListByCategory(category string) []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var skills []*Skill
	for _, skill := range r.skills {
		if strings.EqualFold(skill.Metadata.Category, category) {
			skills = append(skills, skill)
		}
	}
	return skills
}

// Search finds skills matching a query.
func (r *Registry) Search(query string) []SkillSearchResult {
	r.mu.RLock()
	defer r.mu.RUnlock()

	query = strings.ToLower(query)
	var results []SkillSearchResult

	for _, skill := range r.skills {
		score := calculateMatchScore(skill, query)
		if score > 0 {
			results = append(results, SkillSearchResult{
				Name:        skill.Metadata.Name,
				Description: skill.Metadata.Description,
				Version:     skill.Metadata.Version,
				Author:      skill.Metadata.Author,
				Category:    skill.Metadata.Category,
				Tags:        skill.Metadata.Tags,
				Source:      "local",
				Installed:   true,
			})
		}
	}

	// Sort by relevance (name matches first)
	sort.Slice(results, func(i, j int) bool {
		iExact := strings.EqualFold(results[i].Name, query)
		jExact := strings.EqualFold(results[j].Name, query)
		if iExact != jExact {
			return iExact
		}
		return results[i].Name < results[j].Name
	})

	return results
}

// SearchRemote searches remote registries for skills.
func (r *Registry) SearchRemote(ctx context.Context, query string) ([]SkillSearchResult, error) {
	r.mu.RLock()
	remotes := make([]RemoteRegistry, len(r.remotes))
	copy(remotes, r.remotes)
	r.mu.RUnlock()

	var allResults []SkillSearchResult

	for _, remote := range remotes {
		if !remote.Enabled {
			continue
		}

		results, err := searchRemoteRegistry(ctx, remote, query)
		if err != nil {
			continue // Skip failed registries
		}

		for _, result := range results {
			result.Source = remote.Name
			allResults = append(allResults, result)
		}
	}

	return allResults, nil
}

// Activate is a no-op in the Claude-style skill model.
//
// Deprecated: Skills are invoked on-demand via the Skill tool. Persistent
// activation is not supported. This method is preserved for backward
// compatibility with the TUI and will be removed in a future release.
func (r *Registry) Activate(name string) error {
	// Verify skill exists so callers that check errors still get feedback.
	r.mu.RLock()
	_, ok := r.skills[name]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("skill %q not found", name)
	}
	return nil
}

// Deactivate is a no-op in the Claude-style skill model.
//
// Deprecated: See Activate.
func (r *Registry) Deactivate(name string) error {
	return nil
}

// IsActive always returns false in the Claude-style skill model.
//
// Deprecated: See Activate.
func (r *Registry) IsActive(name string) bool {
	return false
}

// GetActive always returns nil in the Claude-style skill model.
//
// Deprecated: See Activate.
func (r *Registry) GetActive() []*Skill {
	return nil
}

// GetInstructions always returns "" in the Claude-style skill model.
//
// Deprecated: See Activate.
func (r *Registry) GetInstructions() string {
	return ""
}

// Unload removes a skill from the registry.
func (r *Registry) Unload(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.skills, name)
	delete(r.active, name)
}

// Clear removes all skills.
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills = make(map[string]*Skill)
	r.active = make(map[string]bool)
}

func generateSkillName(path string) string {
	// Use the directory name as skill name
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return path
}

func calculateMatchScore(skill *Skill, query string) int {
	score := 0

	// Name match
	if strings.Contains(strings.ToLower(skill.Metadata.Name), query) {
		score += 10
		if strings.EqualFold(skill.Metadata.Name, query) {
			score += 20
		}
	}

	// Description match
	if strings.Contains(strings.ToLower(skill.Metadata.Description), query) {
		score += 5
	}

	// Tag match
	for _, tag := range skill.Metadata.Tags {
		if strings.Contains(strings.ToLower(tag), query) {
			score += 3
		}
	}

	// Category match
	if strings.Contains(strings.ToLower(skill.Metadata.Category), query) {
		score += 2
	}

	return score
}

func searchRemoteRegistry(ctx context.Context, remote RemoteRegistry, query string) ([]SkillSearchResult, error) {
	// Placeholder for remote registry search implementation
	// This would make HTTP requests to remote registries
	return nil, nil
}
