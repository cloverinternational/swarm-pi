// Command swarm-gateway is a standalone LAN bridge that lets a phone (or any
// device on the same WiFi) control a running swarm daemon / TUI / headless peer.
//
// It reuses the shared gateway package for the PWA, LAN-IP ranking, discovery
// beacon and control routes, and injects a reverse-proxy handler for the data
// routes (/rpc, /sse, /ws) so it can drive ANY selected peer on the box.
//
// The daemon has its own in-process gateway (no proxy hop); this command is for
// bridging to peers that are not themselves LAN-exposed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/gateway"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/lan"
	"github.com/Swarm-Code/mono/swarm-sdk/serve"
)

// state holds the mutable selected-peer target for the standalone proxy.
type state struct {
	swarm    string
	mu       sync.RWMutex
	selected string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	if err := runGateway(ctx, cfg, defaultRuntimeDeps()); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("swarm-gateway: %v", err)
	}
}

type gatewayConfig struct {
	addr            string
	advertiseHost   string
	swarm           string
	credentialStore string
	peer            string
	insecure        bool
	tlsCert         string
	tlsKey          string
	allowedOrigins  []string
	allowedHosts    []string
}

func parseConfig(args []string) (gatewayConfig, error) {
	fs := flag.NewFlagSet("swarm-gateway", flag.ContinueOnError)
	var cfg gatewayConfig
	var origins, hosts string
	fs.StringVar(&cfg.addr, "addr", ":8787", "listen address")
	fs.StringVar(&cfg.advertiseHost, "advertise-host", "", "concrete TLS DNS name or IP advertised to LAN clients")
	fs.StringVar(&cfg.swarm, "swarm", a2a.DefaultSwarmName, "swarm name to discover peers in")
	fs.StringVar(&cfg.credentialStore, "credential-store", "", "absolute path to owner-only versioned gateway credential storage")
	fs.StringVar(&cfg.peer, "peer", "", "initial peer handle")
	fs.BoolVar(&cfg.insecure, "insecure-static-only", false, "explicit insecure static-only non-loopback listener")
	fs.StringVar(&cfg.tlsCert, "tls-cert", "", "TLS certificate file")
	fs.StringVar(&cfg.tlsKey, "tls-key", "", "owner-only TLS private key file")
	fs.StringVar(&origins, "allowed-origins", "", "comma-separated browser Origin allowlist")
	fs.StringVar(&hosts, "allowed-hosts", "", "comma-separated browser Host allowlist")
	if err := fs.Parse(args); err != nil {
		return gatewayConfig{}, err
	}
	cfg.allowedOrigins = splitNonEmpty(origins)
	cfg.allowedHosts = splitNonEmpty(hosts)
	return cfg, nil
}

type runtimeDeps struct {
	now           func() time.Time
	loadAuthority func(string, time.Time) (*lan.CredentialAuthority, error)
	loadTLS       func(string, string, string, time.Time) (*lan.ListenerTLSEvidence, error)
	listen        func(string, string) (net.Listener, error)
	startBeacon   func(context.Context, gateway.BeaconConfig)
	serve         func(*http.Server, net.Listener, *lan.ListenerTLSEvidence) error
}

func defaultRuntimeDeps() runtimeDeps {
	return runtimeDeps{
		now:           time.Now,
		loadAuthority: lan.LoadCredentialAuthority,
		loadTLS:       lan.LoadListenerTLSEvidence,
		listen:        net.Listen,
		startBeacon: func(ctx context.Context, cfg gateway.BeaconConfig) {
			if err := gateway.StartAuthenticatedBeacon(ctx, cfg); err != nil {
				log.Printf("swarm-gateway: discovery beacon unavailable: %v", err)
			}
		},
		serve: func(s *http.Server, listener net.Listener, evidence *lan.ListenerTLSEvidence) error {
			if evidence == nil {
				return s.Serve(listener)
			}
			tlsConfig, err := evidence.TLSConfig()
			if err != nil {
				return err
			}
			s.TLSConfig = tlsConfig
			return s.ServeTLS(listener, "", "")
		},
	}
}

func runGateway(ctx context.Context, cfg gatewayConfig, deps runtimeDeps) error {
	now := deps.now()
	advertised, err := advertisedListener(cfg.addr, cfg.advertiseHost)
	if err != nil && !cfg.insecure {
		return err
	}
	var authority *lan.CredentialAuthority
	if cfg.credentialStore != "" {
		authority, err = deps.loadAuthority(cfg.credentialStore, now)
		if err != nil {
			return fmt.Errorf("credential preflight: %w", err)
		}
		if err := authority.Ready(now); err != nil {
			return fmt.Errorf("credential preflight: %w", err)
		}
	}
	var evidence *lan.ListenerTLSEvidence
	if cfg.tlsCert != "" || cfg.tlsKey != "" {
		if cfg.tlsCert == "" || cfg.tlsKey == "" {
			return errors.New("TLS preflight: both certificate and key are required")
		}
		evidence, err = deps.loadTLS(cfg.tlsCert, cfg.tlsKey, advertised, now)
		if err != nil {
			return fmt.Errorf("TLS preflight: %w", err)
		}
	}
	opts := gateway.Options{
		Swarm:              cfg.swarm,
		ListenAddr:         cfg.addr,
		AdvertisedListener: advertised,
		Insecure:           cfg.insecure,
		Authority:          authority,
		TLSEvidence:        evidence,
		AllowedOrigins:     cfg.allowedOrigins,
		AllowedHosts:       cfg.allowedHosts,
	}
	grant, err := gateway.AuthorizeListener(cfg.addr, opts)
	if err != nil {
		return fmt.Errorf("refusing to bind %s: %w", cfg.addr, err)
	}

	st := &state{swarm: cfg.swarm, selected: cfg.peer}
	proxy := http.HandlerFunc(st.handleProxy)
	opts.RPCHandler, opts.SSEHandler, opts.WSHandler = proxy, proxy, proxy
	opts.Selected = func() string { st.mu.RLock(); defer st.mu.RUnlock(); return st.selected }
	opts.Select = func(h string) { st.mu.Lock(); st.selected = h; st.mu.Unlock() }
	// gateway.Server.Handler() already wraps its own composed mux with
	// serve.WithResponseWriteBounds (see gateway/server.go); wrapping it
	// again here bounds the standalone command's own top-level
	// http.Server.Handler assignment directly, independent of gateway
	// package internals, exactly like the shared response-bound contract
	// requires for "the complete embedded gateway handler and standalone
	// command mux". The wrap is idempotent: a supported writer simply has
	// its write deadline (re)installed before dispatch.
	httpSrv := &http.Server{Addr: cfg.addr, Handler: serve.WithResponseWriteBounds(gateway.New(opts).Handler())}

	// This is the first network side effect. All credential, TLS, listener
	// identity, and grant validation above has already completed.
	listener, err := deps.listen("tcp", cfg.addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()
	if authority != nil {
		go authority.Watch(ctx.Done(), time.Second)
	}
	if !grant.Loopback && !grant.Insecure {
		host, port, _ := net.SplitHostPort(advertised)
		go deps.startBeacon(ctx, gateway.BeaconConfig{
			Host: host, Port: port, Swarm: cfg.swarm, TLSEvidence: evidence,
		})
	}
	// Explicit opt-in only: production wiring requires SWARM_LAN_REGISTRY=1
	// (Phase 01 CONTRACT.md R5) — unset, empty, "0", and any other value
	// leave it disabled.
	if lanRegistryEnabled(os.Getenv("SWARM_LAN_REGISTRY")) {
		if stopLAN, lerr := a2a.StartLANRegistry(ctx, cfg.swarm); lerr != nil {
			log.Printf("swarm-gateway: LAN peer registry unavailable (non-fatal): %v", lerr)
		} else {
			defer stopLAN()
		}
	}
	return deps.serve(httpSrv, listener, evidence)
}

// lanRegistryEnabled implements the explicit opt-in policy for the LAN peer
// registry required by Phase 01 CONTRACT.md R5: only the exact value "1"
// enables production wiring. Unset, empty, "0", and any other value leave it
// disabled. This is a pure string->bool mapping (no environment access) so
// it can be covered by deterministic table tests without mutating
// process-global environment state.
func lanRegistryEnabled(raw string) bool {
	return raw == "1"
}

func advertisedListener(addr, configuredHost string) (string, error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return "", fmt.Errorf("invalid listen address %q", addr)
	}
	host := strings.TrimSpace(configuredHost)
	if host == "" {
		host, _, _ = net.SplitHostPort(addr)
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
		if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
			ips := gateway.LocalIPv4s()
			if len(ips) == 0 {
				return "", errors.New("no concrete advertised listener identity available")
			}
			host = ips[0]
		}
	}
	return net.JoinHostPort(host, port), nil
}

// splitNonEmpty splits a comma-separated flag value into a trimmed,
// non-empty-entry slice. An all-empty input yields a nil slice, which keeps
// gateway.Options' AllowedOrigins/AllowedHosts fail-closed default (an empty
// allowlist denies every cookie/browser route) intact.
func splitNonEmpty(csv string) []string {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
