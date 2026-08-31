package a2a

// LAN peer registry — makes peer discovery network-aware.
//
// Problem: ListPeers reads ~/.swarm/swarms/<swarm>/peers/*.json on the LOCAL
// host only, so a client attached to one daemon can never see swarm processes
// running on OTHER machines. This file adds a best-effort UDP LAN gossip that
// (a) broadcasts this host's locally-registered, remotely-reachable peers, and
// (b) ingests peers advertised by other hosts, persisting them into the SAME
// on-disk registry as PeerTypeRemote entries with a short TTL.
//
// Because ListPeers already reads that directory and already trusts remote
// peers as alive (skipping the PID check), no consumer needs to change — the
// only companion change is a TTL prune of stale remote peers in ListPeers.
//
// Transport mirrors the proven swarm-gateway beacon (cmd/swarm-gateway/beacon.go
// + the Tauri discovery.rs listener): a small JSON frame broadcast to
// 255.255.255.255 on a fixed UDP port, on a 3s ticker. It is LAN-scoped by
// design; on networks that block broadcast it simply degrades to today's
// host-local behavior.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// PeerSyncPort is the fixed UDP port peer-sync frames are broadcast on.
	// Intentionally distinct from the gateway's DiscoveryPort (8788) so the two
	// gossip channels never cross-talk.
	PeerSyncPort = 8789

	// peerSyncVersion is bumped when the frame shape changes so listeners can
	// reject frames they don't understand.
	peerSyncVersion = 1

	// peerSyncService tags our frames so a listener can ignore unrelated UDP.
	peerSyncService = "swarm-peer-sync"

	// peerSyncInterval is how often we advertise our local peers.
	peerSyncInterval = 3 * time.Second

	// RemotePeerTTL bounds how long a remote peer persists without a refresh.
	// 5 missed beacons (5 * 3s) → the peer is considered gone and pruned by
	// ListPeers. Keeps the cross-device list self-healing when a machine drops
	// off the LAN.
	RemotePeerTTL = 15 * time.Second

	// maxAdvertisedPeers caps how many peers we put in one UDP frame so the
	// datagram stays well under the practical ~64KB UDP limit.
	maxAdvertisedPeers = 64

	// originIDBytes gives each running registry an opaque process-lifetime
	// namespace without exposing machine or process evidence. Its fixed encoded
	// size also lets ingest reject untrusted origin values before any write.
	originIDBytes = 16
	originIDLen   = originIDBytes * 2

	aliasIDBytes = 16
	aliasIDLen   = aliasIDBytes * 2

	// This is the retained unauthenticated-record bound, independent of the
	// per-frame cardinality limit above.
	maxRetainedRemotePeers = 128
)

// peerSyncFrame is the JSON payload broadcast on the LAN. Field names are a wire
// contract — keep them stable.
type peerSyncFrame struct {
	Service string         `json:"service"` // always peerSyncService
	Version int            `json:"version"` // peerSyncVersion
	Swarm   string         `json:"swarm"`   // swarm name
	Origin  string         `json:"origin"`  // opaque process-lifetime origin ID
	Sent    int64          `json:"sent"`    // unix seconds, for freshness
	Peers   []PeerPresence `json:"peers"`   // remotely-reachable local peers
}

type rawPeerSyncFrame struct {
	Service string            `json:"service"`
	Version int               `json:"version"`
	Swarm   string            `json:"swarm"`
	Origin  string            `json:"origin"`
	Sent    int64             `json:"sent"`
	Peers   []json.RawMessage `json:"peers"`
}

// outboundPeerPresence is the unauthenticated LAN wire representation. Keep
// PeerPresence's persisted JSON contract independent from this deliberately
// capability-free display/liveness payload.
type outboundPeerPresence struct {
	Handle     string    `json:"handle"`
	Status     string    `json:"status"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Type       PeerType  `json:"type"`
}

// marshalPeerSyncFrame serializes an outbound frame without host-local
// capabilities. advertisablePeers still performs eligibility selection first.
func marshalPeerSyncFrame(frame peerSyncFrame) ([]byte, error) {
	peers := make([]outboundPeerPresence, len(frame.Peers))
	for i, p := range frame.Peers {
		peers[i] = outboundPeerPresence{
			Handle:     p.Handle,
			Status:     p.Status,
			LastSeenAt: p.LastSeenAt,
			Type:       PeerTypeRemote,
		}
	}
	return json.Marshal(struct {
		Service string                 `json:"service"`
		Version int                    `json:"version"`
		Swarm   string                 `json:"swarm"`
		Origin  string                 `json:"origin"`
		Sent    int64                  `json:"sent"`
		Peers   []outboundPeerPresence `json:"peers"`
	}{
		Service: frame.Service,
		Version: frame.Version,
		Swarm:   frame.Swarm,
		Origin:  frame.Origin,
		Sent:    frame.Sent,
		Peers:   peers,
	})
}

func newOriginID() (string, error) {
	var raw [originIDBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate peer-sync origin: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func validOriginID(origin string) bool {
	if len(origin) != originIDLen {
		return false
	}
	decoded, err := hex.DecodeString(origin)
	return err == nil && len(decoded) == originIDBytes && origin == strings.ToLower(origin)
}

func newAliasID() (string, error) {
	var raw [aliasIDBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate peer-sync alias: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func validAliasID(alias string) bool {
	if len(alias) != aliasIDLen {
		return false
	}
	decoded, err := hex.DecodeString(alias)
	return err == nil && len(decoded) == aliasIDBytes && alias == strings.ToLower(alias)
}

// opaqueRemoteHandle rejects legacy and caller-supplied identifiers rather
// than projecting them through unauthenticated persistence or SDK reads.
func opaqueRemoteHandle(handle string) string {
	origin, alias, ok := strings.Cut(handle, "~")
	if !ok || !validOriginID(origin) || !validAliasID(alias) {
		return ""
	}
	return origin + "~" + alias
}

// packetConn is the minimal UDP surface the registry needs. net.UDPConn
// satisfies it; tests inject an in-memory pair so gossip is deterministic and
// never depends on real broadcast (which is unreliable in CI).
type packetConn interface {
	WriteTo(p []byte, addr net.Addr) (int, error)
	ReadFrom(p []byte) (int, net.Addr, error)
	SetReadDeadline(t time.Time) error
	Close() error
}

// lanRegistry holds the running gossip loops' shared config.
type lanRegistry struct {
	swarm  string
	origin string
	// send is where advertise frames go (a broadcast UDPAddr in production).
	sendTo net.Addr
	send   packetConn
	recv   packetConn
	// advertise is an optional deterministic test seam. Production uses
	// advertiseOnce directly.
	advertise func(context.Context)

	aliasMu sync.Mutex
	aliases map[string]string
}

// aliasFor returns a cryptographically random, non-derived alias stable for
// this registry process. The state cannot exceed one maximum-size frame.
func (r *lanRegistry) aliasFor(handle string) string {
	r.aliasMu.Lock()
	defer r.aliasMu.Unlock()
	if alias := r.aliases[handle]; alias != "" {
		return alias
	}
	if len(r.aliases) >= maxAdvertisedPeers {
		return ""
	}
	if r.aliases == nil {
		r.aliases = make(map[string]string, maxAdvertisedPeers)
	}
	for {
		alias, err := newAliasID()
		if err != nil {
			return ""
		}
		distinct := true
		for _, existing := range r.aliases {
			if existing == alias {
				distinct = false
				break
			}
		}
		if distinct {
			r.aliases[handle] = alias
			return alias
		}
	}
}

// StartLANRegistry starts the LAN peer gossip for the given swarm and returns a
// stop function. Best-effort: if sockets can't be opened it logs via the
// returned error path by returning a no-op stop and a nil error is NOT used —
// callers treat failures as non-fatal. Safe to call once per process.
//
// Production wiring binds a broadcast sender and a 0.0.0.0:PeerSyncPort
// listener. On success both an advertise loop and an ingest loop run until the
// returned stop func is called (or ctx is cancelled).
func StartLANRegistry(ctx context.Context, swarm string) (stop func(), err error) {
	if swarm == "" {
		swarm = DefaultSwarmName
	}
	origin, err := newOriginID()
	if err != nil {
		return nil, err
	}

	// Listener: bind the well-known port on all interfaces.
	recv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: PeerSyncPort})
	if err != nil {
		return nil, fmt.Errorf("peer-sync listen: %w", err)
	}

	// Sender: dial the broadcast address. A separate socket keeps the write
	// path simple (Write to a connected broadcast addr).
	sendConn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4bcast, Port: PeerSyncPort})
	if err != nil {
		_ = recv.Close()
		return nil, fmt.Errorf("peer-sync dial: %w", err)
	}

	reg := &lanRegistry{
		swarm:  swarm,
		origin: origin,
		sendTo: &net.UDPAddr{IP: net.IPv4bcast, Port: PeerSyncPort},
		send:   &connectedPacketConn{UDPConn: sendConn},
		recv:   recv,
	}

	return startLANRegistryLoops(ctx, reg), nil
}

// startLANRegistryLoops owns both goroutines and all shutdown actions. sync.Once
// serializes concurrent callers through the complete shutdown (including join),
// so every stop call returns only after both loops have signaled completion.
func startLANRegistryLoops(parent context.Context, reg *lanRegistry) func() {
	ctx, cancel := context.WithCancel(parent)
	var stopOnce sync.Once
	var loops sync.WaitGroup
	loops.Add(2)
	go func() {
		defer loops.Done()
		reg.advertiseLoop(ctx)
	}()
	go func() {
		defer loops.Done()
		reg.ingestLoop(ctx)
	}()
	return func() {
		stopOnce.Do(func() {
			cancel()
			_ = reg.recv.Close()
			_ = reg.send.Close()
			loops.Wait()
		})
	}
}

// connectedPacketConn adapts a DialUDP (connected) socket to packetConn by
// ignoring the WriteTo addr (the socket already has a fixed broadcast peer).
type connectedPacketConn struct {
	*net.UDPConn
}

func (c *connectedPacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	return c.UDPConn.Write(p)
}

// advertiseLoop broadcasts our advertisable local peers every peerSyncInterval.
func (r *lanRegistry) advertiseLoop(ctx context.Context) {
	ticker := time.NewTicker(peerSyncInterval)
	defer ticker.Stop()
	r.runAdvertise(ctx) // announce immediately
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runAdvertise(ctx)
		}
	}
}

func (r *lanRegistry) runAdvertise(ctx context.Context) {
	if r.advertise != nil {
		r.advertise(ctx)
		return
	}
	r.advertiseOnce()
}

// advertiseOnce builds and sends one frame of this host's advertisable peers.
func (r *lanRegistry) advertiseOnce() {
	peers := r.advertisablePeers()
	if len(peers) == 0 {
		return // nothing routable to share yet
	}
	frame := peerSyncFrame{
		Service: peerSyncService,
		Version: peerSyncVersion,
		Swarm:   r.swarm,
		Origin:  r.origin,
		Sent:    time.Now().Unix(),
		Peers:   peers,
	}
	payload, err := marshalPeerSyncFrame(frame)
	if err != nil {
		return
	}
	_, _ = r.send.WriteTo(payload, r.sendTo)
}

// advertisablePeers returns opaque liveness aliases for local/daemon peers
// that have a remotely reachable endpoint. The endpoint is used only for
// eligibility and is never copied into the unauthenticated advertisement.
func (r *lanRegistry) advertisablePeers() []PeerPresence {
	local, err := ListPeers(r.swarm)
	if err != nil {
		return nil
	}
	lanIP := firstLANIPv4()
	if lanIP == "" {
		return nil // can't advertise a reachable address
	}
	out := make([]PeerPresence, 0, len(local))
	for _, p := range local {
		// Only advertise peers that originate HERE (local or daemon). Never
		// re-advertise peers we ourselves learned remotely (avoids gossip
		// loops / amplification across the LAN).
		if p.Type == PeerTypeRemote {
			continue
		}
		serve := rewriteLoopbackHost(p.ServeURL, lanIP)
		endpoint := rewriteLoopbackHost(p.EndpointURL, lanIP)
		if serve == "" && endpoint == "" {
			continue // not remotely attachable
		}
		alias := r.aliasFor(p.Handle)
		if alias == "" {
			continue
		}
		adv := PeerPresence{
			Handle:     alias,
			Status:     p.Status,
			Type:       PeerTypeRemote,
			LastSeenAt: time.Now().UTC(),
		}
		out = append(out, adv)
		if len(out) >= maxAdvertisedPeers {
			break
		}
	}
	return out
}

// ingestLoop reads advertised frames and upserts remote peers into the local
// registry until ctx is cancelled.
func (r *lanRegistry) ingestLoop(ctx context.Context) {
	buf := make([]byte, 65536)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = r.recv.SetReadDeadline(time.Now().Add(time.Second))
		n, _, err := r.recv.ReadFrom(buf)
		if err != nil {
			// Timeout is expected (lets us re-check ctx); anything else we
			// also just retry on the next loop.
			continue
		}
		r.ingestFrame(buf[:n])
	}
}

// ingestFrame parses one frame and persists its peers as remote entries.
func (r *lanRegistry) ingestFrame(data []byte) {
	var frame rawPeerSyncFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return
	}
	if frame.Service != peerSyncService || frame.Version != peerSyncVersion {
		return
	}
	if frame.Swarm != r.swarm {
		return // different swarm
	}
	if !validOriginID(frame.Origin) {
		return
	}
	if frame.Origin == r.origin {
		return // our own broadcast echoed back
	}
	if len(frame.Peers) > maxAdvertisedPeers {
		return
	}
	peers := make([]PeerPresence, len(frame.Peers))
	for i, raw := range frame.Peers {
		if err := json.Unmarshal(raw, &peers[i]); err != nil {
			return
		}
	}
	now := time.Now().UTC()
	for _, p := range peers {
		if p.Handle == "" {
			continue
		}
		if !validAliasID(p.Handle) {
			continue
		}
		// Namespace the handle by opaque origin so two registries advertising a
		// peer with the same handle don't clobber each other on disk.
		p.Handle = frame.Origin + "~" + p.Handle
		p.Type = PeerTypeRemote
		p.LastSeenAt = now
		p = remotePresenceProjection(p)
		_ = upsertRemotePeer(r.swarm, p)
	}
}

// upsertRemotePeer writes a remote peer's presence file, but ONLY when its
// meaningful content changed (ignoring LastSeenAt), to avoid rewriting files
// every 3s and churning the disk. LastSeenAt is always refreshed on the
// in-memory value we persist so the TTL prune sees a fresh timestamp.
//
// The locking/read/write foundation is the SAME registry-ops primitive the
// field-scoped operations in registry_ops.go use — WithPeerHandleTransaction
// (itself withRegistryHandle under the hood) for the per-handle lock and
// tx.publishRaw for the already-locked atomic write — wrapped by
// withRemoteRetentionLock exactly as before. The retention-lock acquisition
// order (retention lock, THEN handle lock), the maxRetainedRemotePeers cap
// check, and retainedRemotePeerCount's accounting are unchanged: they still
// run, in the same order, only on the "no existing file" branch below, so a
// refresh of an already-retained entry never re-checks (and can never trip)
// the cap, exactly as before. The existing-record inspection deliberately
// keeps its OWN registryReadFile/unmarshalPeerTolerant call — rather than
// tx.Get(), which treats an unsafe (symlink/wrong-owner) entry the same as
// "does not exist" — so an unsafe existing entry still fails closed with an
// error here instead of silently falling through to the create-new-file/cap
// path, matching the pre-refactor behavior exactly.
func upsertRemotePeer(swarm string, peer PeerPresence) error {
	if opaqueRemoteHandle(peer.Handle) == "" {
		return fmt.Errorf("remote peer handle is not opaque")
	}
	peer = remotePresenceProjection(peer)
	peersDir := filepath.Join(SwarmPath(swarm), "peers")
	if err := validateRegistryName(peer.Handle); err != nil {
		return err
	}
	if err := secureDir(peersDir); err != nil {
		return err
	}

	return withRemoteRetentionLock(peersDir, func() error {
		return WithPeerHandleTransaction(swarm, peer.Handle, func(tx *PeerHandleTransaction) error {
			existing, readErr := registryReadFile(tx.path)
			if readErr == nil {
				var prev PeerPresence
				if err := unmarshalPeerTolerant(existing, &prev); err != nil {
					return fmt.Errorf("inspect existing remote peer: %w", err)
				}
				if prev.Type != PeerTypeRemote {
					return fmt.Errorf("refuse to replace non-remote peer")
				}
				if peerContentEqual(prev, peer) &&
					time.Since(prev.LastSeenAt) < peerSyncInterval {
					return nil
				}
				return tx.publishRaw(peer)
			}
			if !os.IsNotExist(readErr) {
				return fmt.Errorf("inspect existing remote peer: %w", readErr)
			}
			count, err := retainedRemotePeerCount(peersDir)
			if err != nil {
				return err
			}
			if count >= maxRetainedRemotePeers {
				return fmt.Errorf("remote peer retention limit reached")
			}
			return tx.publishRaw(peer)
		})
	})
}

func remoteRetentionLockPath(peersDir string) string {
	return filepath.Join(peersDir, ".remote-retention")
}

func withRemoteRetentionLock(peersDir string, fn func() error) error {
	return withRegistryHandle(remoteRetentionLockPath(peersDir), fn)
}

func retainedRemotePeerCount(peersDir string) (int, error) {
	entries, err := registryReadDir(peersDir)
	if err != nil {
		return 0, fmt.Errorf("inspect retained remote peers: %w", err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(peersDir, entry.Name())
		data, err := registryReadFile(path)
		if err != nil {
			return 0, fmt.Errorf("inspect retained remote peer %q: %w", entry.Name(), err)
		}
		var peer PeerPresence
		if err := unmarshalPeerTolerant(data, &peer); err != nil {
			return 0, fmt.Errorf("inspect retained remote peer %q: %w", entry.Name(), err)
		}
		if peer.Type == PeerTypeRemote {
			count++
		}
	}
	return count, nil
}

// removeStaleRemotePeer compare-deletes only the exact stale remote
// publication observed by ListPeers. It removes no socket and exercises no
// process authority.
func removeStaleRemotePeer(swarm string, expected PeerPresence) (bool, error) {
	if expected.Handle == "" {
		return false, nil
	}
	peersDir := filepath.Join(SwarmPath(swarm), "peers")
	path, err := resolveRegistryPath(peersDir, expected.Handle, ".json")
	if err != nil {
		return false, err
	}
	removed := false
	err = withRemoteRetentionLock(peersDir, func() error {
		return withRegistryHandle(path, func() error {
			data, err := registryReadFile(path)
			if err != nil {
				if os.IsNotExist(err) || isUnsafeEntryError(err) {
					return nil
				}
				return err
			}
			var current PeerPresence
			if err := unmarshalPeerTolerant(data, &current); err != nil {
				return nil
			}
			registryConditionalCleanupAfterRead()
			data, err = registryReadFile(path)
			if err != nil {
				if os.IsNotExist(err) || isUnsafeEntryError(err) {
					return nil
				}
				return err
			}
			if err := unmarshalPeerTolerant(data, &current); err != nil {
				return nil
			}
			if current.Type != PeerTypeRemote ||
				current.Handle != expected.Handle ||
				!current.LastSeenAt.Equal(expected.LastSeenAt) ||
				time.Since(current.LastSeenAt) <= RemotePeerTTL {
				return nil
			}
			if err := registryRemove(path); err != nil {
				return err
			}
			removed = true
			return nil
		})
	})
	return removed, err
}

// peerContentEqual compares two peers ignoring the volatile LastSeenAt field.
func peerContentEqual(a, b PeerPresence) bool {
	a.LastSeenAt = time.Time{}
	b.LastSeenAt = time.Time{}
	an, _ := json.Marshal(a)
	bn, _ := json.Marshal(b)
	return string(an) == string(bn)
}

// rewriteLoopbackHost rewrites a loopback/wildcard host in rawURL to lanIP so a
// remote machine can dial it. Returns "" for an empty input. On a parse failure
// it returns the input unchanged (best-effort — better a maybe-unreachable URL
// than dropping the peer silently), except empties stay empty.
func rewriteLoopbackHost(rawURL, lanIP string) string {
	if strings.TrimSpace(rawURL) == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	host := u.Hostname()
	if isLoopbackOrWildcard(host) {
		port := u.Port()
		if port != "" {
			u.Host = net.JoinHostPort(lanIP, port)
		} else {
			u.Host = lanIP
		}
	}
	return u.String()
}

func isLoopbackOrWildcard(host string) bool {
	switch host {
	case "127.0.0.1", "localhost", "0.0.0.0", "::1", "::":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return false
}

// lanVirtualIfacePrefixes are interface-name prefixes that are NOT real LAN
// NICs (container bridges, VPN tunnels). Advertising one of their IPs gives
// other machines an address they can't route, so peers look online but 502 on
// attach. Mirrors gateway/server.go virtualIfacePrefixes (import cycle keeps
// them separate).
var lanVirtualIfacePrefixes = []string{
	"docker", "br-", "veth", "virbr", "vmnet", "tailscale", "wg", "tun", "utun", "zt",
}

func lanIfaceIsVirtual(name string) bool {
	for _, p := range lanVirtualIfacePrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// lanIsCGNAT reports whether ip is in the 100.64.0.0/10 carrier-grade NAT
// range (Tailscale et al) — reachable only inside that overlay, not the LAN.
func lanIsCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

// firstLANIPv4 returns this host's best non-loopback IPv4 address, or "" when
// none is routable. Real-NIC addresses are preferred over container-bridge /
// VPN / CGNAT addresses, which are used only as a last resort — advertising a
// docker/tailscale IP to the physical LAN is how "peer shows online but
// attach 502s" happens.
func firstLANIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	fallback := ""
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		virtual := lanIfaceIsVirtual(iface.Name)
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			v4 := ip.To4()
			if v4 == nil {
				continue
			}
			if virtual || lanIsCGNAT(v4) {
				if fallback == "" {
					fallback = v4.String()
				}
				continue
			}
			return v4.String()
		}
	}
	return fallback
}
