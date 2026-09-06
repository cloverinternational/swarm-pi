package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/lan"
)

// DiscoveryPort is the fixed UDP port the gateway broadcasts presence on and
// that native clients (the Tauri desktop/mobile app) listen on to auto-find a
// gateway without the user typing an IP. It is intentionally distinct from the
// HTTP -addr port so discovery works regardless of which port the gateway
// serves on.
//
// A browser PWA cannot open raw UDP sockets, so this beacon targets the NATIVE
// layer (Rust/Go). PWA clients are same-origin with their gateway and never
// need discovery; native apps use this to populate a "Found on your network"
// list.
const DiscoveryPort = 8788

// beaconVersion is bumped when the beacon JSON shape changes so listeners can
// reject frames they don't understand.
const beaconVersion = 1

// beacon is the JSON payload broadcast on the LAN. Keep field names stable —
// they are a wire contract consumed by native discovery listeners.
type beacon struct {
	Service       string `json:"service"`       // always "swarm-gateway"
	Version       int    `json:"version"`       // beaconVersion
	Name          string `json:"name"`          // human label
	URL           string `json:"url"`           // https://<validated-host>:<port>
	WSURL         string `json:"wsURL"`         // wss://<validated-host>:<port>/ws
	Swarm         string `json:"swarm"`         // swarm name being served
	TokenRequired bool   `json:"tokenRequired"` // whether data routes need a token
	Sent          int64  `json:"sent"`          // unix seconds, for freshness/decay
}

// BeaconConfig is the non-secret input to authenticated LAN advertisement.
// Host is the concrete DNS name or IP already validated by TLSEvidence.
type BeaconConfig struct {
	Host        string
	Port        string
	Swarm       string
	TLSEvidence *lan.ListenerTLSEvidence
}

var (
	errBeaconTLS = errors.New("gateway: authenticated beacon requires valid listener TLS evidence")
	beaconDial   = func(dst *net.UDPAddr) (net.Conn, error) {
		return net.DialUDP("udp4", nil, dst)
	}
)

func plaintextBeaconPayload(host, port, swarm string, tokenRequired bool, now time.Time) ([]byte, error) {
	hostPort := net.JoinHostPort(host, port)
	return json.Marshal(beacon{
		Service:       "swarm-gateway",
		Version:       beaconVersion,
		Name:          "Swarm Gateway",
		URL:           "http://" + hostPort,
		WSURL:         "ws://" + hostPort + "/ws",
		Swarm:         swarm,
		TokenRequired: tokenRequired,
		Sent:          now.Unix(),
	})
}

// authenticatedBeaconPayload validates TLS evidence before constructing an
// advertisement. Its URL schemes are always HTTPS/WSS.
func authenticatedBeaconPayload(cfg BeaconConfig, now time.Time) ([]byte, error) {
	listener := net.JoinHostPort(cfg.Host, cfg.Port)
	if cfg.TLSEvidence == nil || cfg.TLSEvidence.ValidFor(listener, now) != nil {
		return nil, errBeaconTLS
	}
	hostPort := net.JoinHostPort(cfg.Host, cfg.Port)
	return json.Marshal(beacon{
		Service:       "swarm-gateway",
		Version:       beaconVersion,
		Name:          "Swarm Gateway",
		URL:           "https://" + hostPort,
		WSURL:         "wss://" + hostPort + "/ws",
		Swarm:         cfg.Swarm,
		TokenRequired: true,
		Sent:          now.Unix(),
	})
}

// StartAuthenticatedBeacon validates the exact TLS listener identity before
// opening the UDP socket or writing a beacon. It broadcasts HTTPS/WSS only.
func StartAuthenticatedBeacon(ctx context.Context, cfg BeaconConfig) error {
	if _, err := authenticatedBeaconPayload(cfg, time.Now()); err != nil {
		return err
	}
	dst := &net.UDPAddr{IP: net.IPv4bcast, Port: DiscoveryPort}
	conn, err := beaconDial(dst)
	if err != nil {
		return err
	}
	defer conn.Close()

	const interval = 3 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var loggedErr bool
	send := func() {
		payload, err := authenticatedBeaconPayload(cfg, time.Now())
		if err != nil {
			if !loggedErr {
				log.Printf("swarm-gateway: authenticated beacon disabled: %v", err)
				loggedErr = true
			}
			return
		}
		if _, err := conn.Write(payload); err != nil {
			if !loggedErr {
				log.Printf("swarm-gateway: beacon broadcast error (will retry): %v", err)
				loggedErr = true
			}
			return
		}
		loggedErr = false
	}
	send()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			send()
		}
	}
}

// StartBeacon emits the legacy plaintext discovery payload for the explicitly
// open LAN daemon mode. Callers that expose authenticated control must use
// StartAuthenticatedBeacon instead.
func StartBeacon(ctx context.Context, httpPort, swarm string, tokenRequired bool) {
	ips := LocalIPv4s()
	if len(ips) == 0 {
		log.Printf("swarm-gateway: plaintext beacon disabled: no local IPv4 address")
		return
	}
	if _, err := plaintextBeaconPayload(ips[0], httpPort, swarm, tokenRequired, time.Now()); err != nil {
		log.Printf("swarm-gateway: plaintext beacon disabled: %v", err)
		return
	}
	dst := &net.UDPAddr{IP: net.IPv4bcast, Port: DiscoveryPort}
	conn, err := beaconDial(dst)
	if err != nil {
		log.Printf("swarm-gateway: plaintext beacon unavailable: %v", err)
		return
	}
	defer conn.Close()

	const interval = 3 * time.Second
	send := func() {
		payload, err := plaintextBeaconPayload(ips[0], httpPort, swarm, tokenRequired, time.Now())
		if err == nil {
			_, _ = conn.Write(payload)
		}
	}
	send()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			send()
		}
	}
}
