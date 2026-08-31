package agents

import (
	"sort"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/agent"
)

// Manager loads and serves agent.Definition objects from three sources,
// merged in ascending priority order:
//
//	embedded built-ins < user dir (~/.swarmos/agents/) < project dir (./.swarm/agents/)
//
// It is safe for concurrent use.
type Manager struct {
	mu         sync.RWMutex
	defs       map[string]*agent.Definition
	projectDir string // e.g. /path/to/project/.swarm/agents
	userDir    string // e.g. ~/.swarmos/agents
}

// New creates a Manager and performs the initial load.
//
// projectDir and userDir may be empty strings — missing or unreadable
// directories are silently skipped so the manager always works even in
// minimal environments.
//
// The embedded built-ins are always loaded regardless of the directories.
func New(projectDir, userDir string) (*Manager, error) {
	m := &Manager{
		projectDir: projectDir,
		userDir:    userDir,
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

// NewDefault creates a Manager that serves only the embedded built-ins.
// This is the zero-config path used when no directories are configured.
func NewDefault() *Manager {
	m := &Manager{}
	// Ignore error — embedded FS is always present.
	_ = m.load()
	return m
}

// Get returns the Definition for the given agent ID, or (nil, false) if not found.
func (m *Manager) Get(id string) (*agent.Definition, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	def, ok := m.defs[id]
	return def, ok
}

// List returns all known agent definitions, sorted by ID for stable output.
func (m *Manager) List() []*agent.Definition {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*agent.Definition, 0, len(m.defs))
	for _, def := range m.defs {
		out = append(out, def)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

// IDs returns a sorted slice of all known agent IDs.
// Useful for building tool schema enums.
func (m *Manager) IDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.defs))
	for id := range m.defs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Reload re-reads all agent definition files from disk.
// Embedded built-ins are always re-applied as the base layer.
// Safe to call concurrently — takes a write lock during the swap.
func (m *Manager) Reload() error {
	return m.load()
}

// load performs the full three-layer load and atomically swaps m.defs.
func (m *Manager) load() error {
	embedded, err := loadEmbedded()
	if err != nil {
		return err
	}

	user, err := loadDir(m.userDir)
	if err != nil {
		return err
	}

	project, err := loadDir(m.projectDir)
	if err != nil {
		return err
	}

	// Merge: project wins over user wins over embedded.
	merged := merge(embedded, user, project)

	m.mu.Lock()
	m.defs = merged
	m.mu.Unlock()
	return nil
}
