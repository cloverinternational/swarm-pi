package chat

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// MentionType categorizes mention entries
type MentionType int

const (
	MentionFile  MentionType = iota // File/directory mention
	MentionAgent                    // Agent mention (plugin or custom)
	MentionTmux                     // Tmux session mention
)

// MentionEntry represents a single item in the unified @ mention dropdown
type MentionEntry struct {
	Type        MentionType
	Name        string // display name
	Description string // agent desc / tmux state / file type
	Icon        string // nerd font icon
	IconColor   string // icon color

	// File-specific fields
	Path    string // for files: relative path
	IsDir   bool
	RelPath string

	// Agent-specific fields
	AgentSource string // "plugin" or "custom"
	AgentNS     string // plugin namespace (for plugin agents)
	AgentID     string // agent ID (for custom agents: "general-assistant", for plugins: agent name)

	// Tmux-specific fields
	SessionName string // tmux session name
	State       string // "idle" / "busy"
}

// AgentMentionProvider supplies agent entries for the mention dropdown
type AgentMentionProvider interface {
	GetAgentMentions() []MentionEntry
}

// TmuxMentionProvider supplies tmux session entries for the mention dropdown
type TmuxMentionProvider interface {
	GetTmuxSessions() []MentionEntry
}

type A2APeerLister interface {
	ListA2APeersContext(ctx context.Context) ([]a2a.PeerIdentity, error)
}

// Nerd font icons for mention types
const (
	iconAgent = "\uf544" // nf-md-robot (robot face)
	iconTmux  = "\ue795" // nf-dev-terminal (same glyph as iconShell, different semantic)
)

// Icon colors for mention types
const (
	agentIconColor = "#a78bfa" // purple for agents
	tmuxIconColor  = "#34d399" // green for tmux sessions
)

// PluginAgentMentionProvider aggregates agents from plugins and custom settings
type PluginAgentMentionProvider struct {
	pluginsManager *PluginsManager
	agentsSettings *settings.AgentsSettings
	peerLister     A2APeerLister
}

// NewPluginAgentMentionProvider creates a provider that aggregates plugin and custom agents
func NewPluginAgentMentionProvider(pm *PluginsManager, as *settings.AgentsSettings, peerLister A2APeerLister) *PluginAgentMentionProvider {
	return &PluginAgentMentionProvider{
		pluginsManager: pm,
		agentsSettings: as,
		peerLister:     peerLister,
	}
}

// GetAgentMentions returns all available agents as mention entries
func (p *PluginAgentMentionProvider) GetAgentMentions() []MentionEntry {
	var entries []MentionEntry

	// 1. Plugin agents
	if p.pluginsManager != nil {
		for _, plugin := range p.pluginsManager.GetEnabledPlugins() {
			ns := plugin.Namespace()
			for _, ag := range plugin.Agents {
				entries = append(entries, MentionEntry{
					Type:        MentionAgent,
					Name:        ag.Name,
					Description: ag.Description,
					Icon:        iconAgent,
					IconColor:   agentIconColor,
					AgentSource: "plugin",
					AgentNS:     ns,
					AgentID:     ag.Name, // Plugin agents use name as ID
				})
			}
		}
	}

	// 2. Custom agents from settings
	if p.agentsSettings != nil {
		for _, ag := range p.agentsSettings.GetAgents() {
			entries = append(entries, MentionEntry{
				Type:        MentionAgent,
				Name:        ag.Name,
				Description: ag.Description,
				Icon:        iconAgent,
				IconColor:   agentIconColor,
				AgentSource: "custom",
				AgentID:     ag.ID, // Use actual ID like "general-assistant"
			})
		}
	}

	if p.peerLister != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		if peers, err := p.peerLister.ListA2APeersContext(ctx); err == nil {
			for _, peer := range peers {
				entries = append(entries, MentionEntry{
					Type:        MentionAgent,
					Name:        peer.Handle,
					Description: i18n.T("classic_chat_2.mention.a2a_peer"),
					Icon:        iconAgent,
					IconColor:   agentIconColor,
					AgentSource: "peer",
					AgentNS:     "peer",
					AgentID:     peer.Handle,
				})
			}
		}
	}

	return entries
}

// TmuxSessionMentionProvider queries tmux directly for active sessions
type TmuxSessionMentionProvider struct{}

// NewTmuxSessionMentionProvider creates a provider that queries tmux directly
func NewTmuxSessionMentionProvider() *TmuxSessionMentionProvider {
	return &TmuxSessionMentionProvider{}
}

// GetTmuxSessions returns all active tmux sessions as mention entries
func (p *TmuxSessionMentionProvider) GetTmuxSessions() []MentionEntry {
	// Query tmux for all sessions
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	entries := make([]MentionEntry, 0, len(lines))

	for _, name := range lines {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		entries = append(entries, MentionEntry{
			Type:        MentionTmux,
			Name:        name,
			Icon:        iconTmux,
			IconColor:   tmuxIconColor,
			SessionName: name,
		})
	}

	return entries
}
