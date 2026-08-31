// Package a2a is the public surface for swarm's agent-to-agent (A2A) peer
// discovery and presence. It is a thin re-export of the internal
// implementation so external consumers — IDE/desktop backends, custom
// daemons — can list, join, leave, and inspect swarm peers without depending
// on internal packages.
//
// Peers register filesystem presence under
// ~/.swarm/swarms/<swarmName>/peers/<handle>.json and may expose:
//   - ServeURL: the SDK client serve interface — HTTP /rpc (JSON-RPC) + /sse +
//     /ws, i.e. the serve.Mux with the client.* methods (client.snapshot,
//     getMessages, sendMessage, …). This is what an attaching TUI/webapp/mobile
//     PWA dials for client RPC. PREFER THIS for client calls.
//   - EndpointURL: the A2A agent endpoint (automation/task-dispatch). It is NOT
//     guaranteed to be the client serve.Mux — on a daemon it is a distinct
//     endpoint that answers client.* with "method not found". Only fall back to
//     it for client RPC when ServeURL is empty AND you know it points at a serve
//     bridge; otherwise resolve via ServeURL. (See resolveServeBase / proxy
//     ServeBase, which both prefer ServeURL for exactly this reason.)
//   - ControlSocket: a Unix socket speaking the automation control protocol
package a2a

import (
	"context"

	internala2a "github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
)

// DefaultSwarmName is the swarm every peer joins unless told otherwise.
const DefaultSwarmName = internala2a.DefaultSwarmName

// PeerSyncPort is the UDP port the LAN peer registry broadcasts on.
const PeerSyncPort = internala2a.PeerSyncPort

// PeerType indicates whether a peer is local, remote, or a headless daemon.
type PeerType = internala2a.PeerType

// PeerType values.
const (
	PeerTypeLocal  = internala2a.PeerTypeLocal
	PeerTypeRemote = internala2a.PeerTypeRemote
	PeerTypeDaemon = internala2a.PeerTypeDaemon
)

// PeerPresence is a peer's published presence record.
type PeerPresence = internala2a.PeerPresence

// ListPeers returns all live peers in the swarm, pruning dead local processes.
// Remote peers contain display/liveness metadata only; unauthenticated
// transport capabilities and process-instance authority are omitted.
func ListPeers(swarmName string) ([]PeerPresence, error) {
	return internala2a.ListPeers(swarmName)
}

// GetPeer returns a single peer by handle, or an error if not found. Legacy
// remote records are projected to capability-free display/liveness metadata.
func GetPeer(swarmName, handle string) (*PeerPresence, error) {
	return internala2a.GetPeer(swarmName, handle)
}

// JoinSwarm registers (or refreshes) a peer's presence. PeerTypeRemote records
// are persisted without transport capabilities or process-instance authority:
// they are routed through the internal opaque-handle, retention-lock, and
// cap-enforcing join path, which rejects any non-opaque remote handle outright
// rather than persisting or displaying it.
func JoinSwarm(swarmName string, peer PeerPresence) error {
	return internala2a.JoinSwarm(swarmName, peer)
}

// LeaveSwarm removes a peer's presence.
func LeaveSwarm(swarmName, handle string) error {
	return internala2a.LeaveSwarm(swarmName, handle)
}

// UpdateStatus updates a peer's status/current-task and heartbeat timestamp.
func UpdateStatus(swarmName, handle, status, currentTask string) error {
	return internala2a.UpdateStatus(swarmName, handle, status, currentTask)
}

// StartLANRegistry starts the network-aware LAN peer gossip for the given swarm
// and returns a stop function. It broadcasts this host's remotely-reachable
// peers over UDP and ingests peers advertised by other machines on the LAN,
// persisting them as remote peers so ListPeers surfaces cross-device instances.
// Best-effort and LAN-scoped; returns an error if the sockets can't be opened
// (callers should treat that as non-fatal).
func StartLANRegistry(ctx context.Context, swarmName string) (stop func(), err error) {
	return internala2a.StartLANRegistry(ctx, swarmName)
}
