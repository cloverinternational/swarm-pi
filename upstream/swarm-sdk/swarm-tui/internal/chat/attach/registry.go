package attach

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/state"
)

type sdkSource interface {
	GetBackgroundAgents() []builtin.BackgroundAgentInfo
	GetA2AHandle() string
}

type registrySources struct {
	agents     func() []builtin.BackgroundAgentInfo
	peers      func(string) ([]a2a.PeerPresence, error)
	selfHandle func() string
}

// ListTargets returns the local background agents and discovered swarm peers
// as one stable, display-ready list.
func ListTargets(sdk sdkSource, swarmName string) ([]Target, error) {
	sources := registrySources{
		agents: func() []builtin.BackgroundAgentInfo {
			if sdk == nil {
				return nil
			}
			return sdk.GetBackgroundAgents()
		},
		peers: listLivePeers,
		selfHandle: func() string {
			if sdk == nil {
				return ""
			}
			return sdk.GetA2AHandle()
		},
	}
	return listTargets(sources, swarmName)
}

// listLivePeers removes local/daemon records whose control socket is no
// longer accepting connections. Process liveness alone is insufficient here:
// a reused PID or a crashed automation server can leave a peer record behind
// while its socket is dead, producing a broken pipe when selected.
func listLivePeers(swarmName string) ([]a2a.PeerPresence, error) {
	peers, err := a2a.ListPeers(swarmName)
	if err != nil {
		return nil, err
	}
	live := peers[:0]
	for _, peer := range peers {
		if peer.ControlSocket != "" &&
			(peer.Type == a2a.PeerTypeLocal || peer.Type == a2a.PeerTypeDaemon) &&
			!controlSocketAccepting(peer.ControlSocket) {
			continue
		}
		live = append(live, peer)
	}
	return live, nil
}

func controlSocketAccepting(path string) bool {
	conn, err := net.DialTimeout("unix", path, 150*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func listTargets(sources registrySources, swarmName string) ([]Target, error) {
	targets := make([]Target, 0)
	for _, info := range sources.agents() {
		name := strings.SplitN(info.Task, "\n", 2)[0]
		status := string(info.Status)
		if info.Result != nil {
			status = "done"
		} else if status == "" {
			status = "running"
		}
		summary := info.Summary
		if summary == "" && info.Result != nil {
			summary = info.Result.Result
			if summary == "" && info.Result.Error != nil {
				summary = info.Result.Error.Error()
			}
		}
		snapshot := StateSnapshot{
			Summary:   summary,
			Preview:   summary,
			Streaming: status == "running" || status == "pending",
			UpdatedAt: info.StartTime,
		}
		if summary != "" {
			snapshot.Messages = []state.MessageState{{Role: "assistant", Content: summary}}
		}
		targets = append(targets, Target{
			Kind:      TargetKindSubAgent,
			ID:        info.AgentID,
			ParentID:  info.ParentID,
			Name:      name,
			Status:    status,
			Progress:  info.Progress,
			Summary:   info.Summary,
			LastTool:  info.LastTool,
			ToolCount: info.ToolCount,
			Snapshot:  snapshot,
			// TODO(WS4/feat/tui-attach-steer): set this once sub-agent
			// interjection plumbing is available.
			Steerable: false,
			AgentID:   info.AgentID,
		})
	}

	peers, err := sources.peers(swarmName)
	if err != nil {
		return nil, fmt.Errorf("list attach peers: %w", err)
	}
	selfHandle := strings.TrimSpace(sources.selfHandle())
	for _, peer := range peers {
		if selfHandle != "" && peer.Handle == selfHandle {
			continue
		}
		name := peer.Name
		if name == "" {
			name = peer.Handle
		}
		peerSnapshot := StateSnapshot{
			ConversationID: peer.ConversationID,
			Summary:        peer.CurrentTask,
			Preview:        peer.CurrentTask,
			UpdatedAt:      peer.LastSeenAt,
		}
		if peer.CurrentTask != "" {
			peerSnapshot.Messages = []state.MessageState{{Role: "assistant", Content: peer.CurrentTask}}
		}
		targets = append(targets, Target{
			Kind:           TargetKindPeer,
			ID:             peer.Handle,
			Name:           name,
			Status:         peer.Status,
			Steerable:      peer.ControlSocket != "",
			Machine:        peerMachine(peer),
			Workspace:      peer.Workspace,
			Branch:         peer.Branch,
			ConversationID: peer.ConversationID,
			ControlSocket:  peer.ControlSocket,
			ServeURL:       peer.ServeURL,
			Snapshot:       peerSnapshot,
		})
	}

	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].Kind != targets[j].Kind {
			return targets[i].Kind == TargetKindSubAgent
		}
		if targets[i].Name != targets[j].Name {
			return targets[i].Name < targets[j].Name
		}
		return targets[i].ID < targets[j].ID
	})
	return targets, nil
}

func peerMachine(peer a2a.PeerPresence) string {
	if i := strings.IndexByte(peer.Handle, '~'); i >= 0 {
		return peer.Handle[:i]
	}
	if peer.Type == a2a.PeerTypeRemote {
		return peer.AddedBy
	}
	return ""
}
