package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/gateway"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/lifecycle"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
	"github.com/Swarm-Code/mono/swarm-sdk/serve"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/protocol"
	attserver "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/server"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version"
)

// runDaemonCLI implements `swarmos daemon [flags]`.
//
// A daemon is a persistent, headless swarmos process that:
//   - Registers itself in the swarm with an A2A HTTP endpoint
//   - Accepts inbound tasks from any peer via JSON-RPC
//   - Runs a full multi-turn agent loop for each task
//   - Exposes a Unix control socket (same protocol as TUI) for observability
//   - Reports canonical lifecycle status (ADR-005: "ready"/"working"/
//     "stopping", never legacy "idle") in its presence file on every
//     transition
//
// Usage:
//
//	swarmos daemon                                   # auto-handle, cwd workspace
//	swarmos daemon --a2a-handle backend-reviewer     # stable named handle
//	swarmos daemon --m claude-opus-4 --workspace /p  # model + workspace override
func runDaemonCLI(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	handle := fs.String("a2a-handle", "", "Peer handle (default: hostname-timestamp)")
	listen := fs.String("a2a-listen", "127.0.0.1:0", "A2A HTTP listen address")
	modelName := fs.String("m", "", "Model override (uses saved config if empty)")
	provider := fs.String("P", "", "Provider override (uses saved config if empty)")
	workspace := fs.String("workspace", "", "Workspace root (default: cwd)")
	approval := fs.String("approval", "headless", "Tool approval mode: 'headless' (auto-approve) or 'interactive' (route to attached UI via respondApproval)")
	noFallback := fs.Bool("no-fallback", false, "Disable fallback-chain cascading: only the pinned primary provider/model is attempted, so its real error surfaces instead of routing to other (possibly unregistered) providers")
	gatewayOn := fs.Bool("gateway", false, "Also serve the open LAN gateway")
	gatewayAddr := fs.String("gateway-addr", defaultDaemonGatewayAddr, "Gateway listen address. Only used with --gateway; defaults to all LAN interfaces")
	if err := fs.Parse(args); err != nil {
		return err
	}

	resolvedHandle := *handle
	if resolvedHandle == "" {
		resolvedHandle = computePeerHandle()
	}
	if err := validatePeerHandle(resolvedHandle); err != nil {
		return fmt.Errorf("invalid --a2a-handle: %w", err)
	}

	resolvedWorkspace := *workspace
	if resolvedWorkspace == "" {
		var err error
		resolvedWorkspace, err = os.Getwd()
		if err != nil {
			resolvedWorkspace = "."
		}
	}

	return runDaemon(daemonConfig{
		handle:      resolvedHandle,
		listen:      *listen,
		model:       *modelName,
		provider:    *provider,
		workspace:   resolvedWorkspace,
		approval:    *approval,
		noFallback:  *noFallback,
		gatewayOn:   *gatewayOn,
		gatewayAddr: *gatewayAddr,
	})
}

const defaultDaemonGatewayAddr = ":8787"

// daemonWriteTimeout backstops both daemon-hosted production HTTP servers
// (the loopback client-interface server and the LAN gateway server) with a
// positive write deadline, in addition to the per-handler response
// deadlines the serve package installs. Phase 01 CONTRACT.md R5 requires
// this on both daemon production http.Server constructions.
const daemonWriteTimeout = 10 * time.Second

// newDaemonClientServer constructs the loopback client-interface HTTP
// server (/rpc /sse /ws /healthz) that runDaemon binds and serves. Factored
// out as a small, deterministic constructor so its WriteTimeout backstop is
// independently testable without booting a full daemon.
func newDaemonClientServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:      handler,
		WriteTimeout: daemonWriteTimeout,
	}
}

// newDaemonGatewayServer constructs the LAN gateway HTTP server that
// serveDaemonGateway binds and serves. Factored out as a small,
// deterministic constructor so its WriteTimeout backstop is independently
// testable without booting a full daemon or a real gateway.
func newDaemonGatewayServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:      handler,
		WriteTimeout: daemonWriteTimeout,
	}
}

// lanRegistryEnabled implements the explicit opt-in policy for the LAN peer
// registry required by Phase 01 CONTRACT.md R5: only the exact value "1"
// enables production wiring. Unset, empty, "0", and any other value leave it
// disabled. This is a pure string->bool mapping (no environment access) so
// daemon, standalone gateway, and desktop backend entry points can each be
// covered by deterministic table tests without mutating process-global
// environment state.
func lanRegistryEnabled(raw string) bool {
	return raw == "1"
}

type daemonConfig struct {
	handle      string
	listen      string
	model       string
	provider    string
	workspace   string
	approval    string
	noFallback  bool
	gatewayOn   bool
	gatewayAddr string
}

// daemonState tracks mutable runtime state shared between the request handler,
// the control socket view, and the heartbeat goroutine. All fields accessed
// from multiple goroutines are protected by mu or use atomic ops.
type daemonState struct {
	mu            sync.RWMutex
	handle        string
	model         string
	workspace     string
	startedAt     time.Time
	status        lifecycle.State
	currentTask   string
	lastErr       string
	gateway       string // LAN gateway state: "", "listening <addr>", or a bind-failure note
	lastTaskAt    time.Time
	tasksOK       atomic.Int64
	tasksFailed   atomic.Int64
	instanceToken string
	processStart  string
	executable    string
}

func newDaemonState(handle, model, workspace string) *daemonState {
	return &daemonState{
		handle:    handle,
		model:     model,
		workspace: workspace,
		startedAt: time.Now(),
		// ADR-005 "Readiness, publication, and adapters": a daemon must
		// never be observed as `ready` before it has itself self-probed —
		// "Process existence, a held lock, presence publication, and a
		// successful TCP handshake are liveness signals, not readiness."
		// A freshly constructed daemonState therefore starts in the one
		// legal pre-readiness state (`starting`, ADR-005's `absent ->
		// starting` row's destination) and only reaches `ready` via
		// selfProbeReady's legal `starting -> ready` transition
		// (daemon_health.go), invoked from runDaemon strictly before
		// publishDaemonAttachEndpoints.
		status: lifecycle.StateStarting,
	}
}

// lifecycleCandidate is one (to, intent, reason) transition daemonState's
// tryTransitionLocked may attempt from its current status; see below.
type lifecycleCandidate struct {
	To     lifecycle.State
	Intent lifecycle.Intent
	Reason lifecycle.Reason
}

// tryTransitionLocked applies the first candidate lifecycle.ValidateTransition
// accepts from s.status (caller must hold s.mu), mutating s.status only on
// success, or returns an error if none of the candidates validate. This is
// the single choke point every daemonState status mutation goes through
// after construction — direct `s.status = ...` field assignment is never
// used again, per ADR-005's requirement that every state change be a
// legally validated transition (lifecycle.ValidateTransition), never a
// bare field write.
func (s *daemonState) tryTransitionLocked(candidates ...lifecycleCandidate) error {
	for _, c := range candidates {
		if err := lifecycle.ValidateTransition(s.status, c.To, c.Intent, c.Reason); err == nil {
			s.status = c.To
			return nil
		}
	}
	if len(candidates) == 1 {
		return lifecycle.ValidateTransition(s.status, candidates[0].To, candidates[0].Intent, candidates[0].Reason)
	}
	return fmt.Errorf("lifecycle: no legal transition from %s among %d candidates", s.status, len(candidates))
}

// transition is the single-candidate convenience wrapper around
// tryTransitionLocked, acquiring s.mu itself. Used by the self-probe
// readiness path (daemon_health.go's selfProbeReady) and anywhere else
// exactly one legal source state is expected.
func (s *daemonState) transition(to lifecycle.State, intent lifecycle.Intent, reason lifecycle.Reason) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tryTransitionLocked(lifecycleCandidate{To: to, Intent: intent, Reason: reason})
}

// setWorking transitions the daemon's canonical lifecycle status to
// `working` (ADR-005: a ready daemon has at least one accepted active
// execution) and records the accepted task text. Tries the ordinary
// `ready -> working` row first (the expected precondition for every
// current call site) and falls back to the `degraded -> working` recovery
// rows so a daemon that regains readiness right as it accepts work is not
// spuriously refused; if s.status is neither (for example still
// `starting`), the transition is illegally refused and s.status is left
// unchanged — the accepted task text is still recorded either way since it
// is bookkeeping, not itself part of the closed lifecycle vocabulary.
func (s *daemonState) setWorking(task string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.tryTransitionLocked(
		lifecycleCandidate{lifecycle.StateWorking, lifecycle.IntentAcceptWork, lifecycle.ReasonWorkAccepted},
		lifecycleCandidate{lifecycle.StateWorking, lifecycle.IntentRecover, lifecycle.ReasonDependencyRecovered},
		lifecycleCandidate{lifecycle.StateWorking, lifecycle.IntentRecover, lifecycle.ReasonReadinessProven},
	)
	s.currentTask = task
}

// setIdle transitions the daemon's canonical lifecycle status back to
// `ready` once its active execution completes (or fails). ADR-005: "ready"
// already means idle and able to accept work — "idle" is a legacy/derived
// display label, never the current writer's canonical output, so this
// writes lifecycle.StateReady rather than a literal "idle" string, via the
// legal `working -> ready` transition row (`complete_work`,
// `work_completed`/`work_failed`) rather than a direct field assignment.
func (s *daemonState) setIdle(execErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reason := lifecycle.ReasonWorkCompleted
	if execErr != nil {
		reason = lifecycle.ReasonWorkFailed
	}
	_ = s.tryTransitionLocked(lifecycleCandidate{lifecycle.StateReady, lifecycle.IntentCompleteWork, reason})
	s.lastTaskAt = time.Now()
	if execErr != nil {
		s.lastErr = execErr.Error()
		s.tasksFailed.Add(1)
	} else {
		s.lastErr = ""
		s.tasksOK.Add(1)
	}
	s.currentTask = ""
}

func (s *daemonState) snapshot() (status lifecycle.State, task, lastErr string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status, s.currentTask, s.lastErr
}

// setGateway records the LAN gateway's current state for healthz/frame display.
func (s *daemonState) setGateway(state string) {
	s.mu.Lock()
	s.gateway = state
	s.mu.Unlock()
}

func (s *daemonState) gatewayState() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.gateway
}

// renderFrame builds the synthesized status pane returned by `swarmos swarm attach <h> frame`.
func (s *daemonState) renderFrame() string {
	status, task, lastErr := s.snapshot()
	uptime := time.Since(s.startedAt).Truncate(time.Second)
	ok := s.tasksOK.Load()
	failed := s.tasksFailed.Load()

	frame := fmt.Sprintf("daemon:    %s\n", s.handle)
	frame += fmt.Sprintf("status:    %s\n", status.String())
	frame += fmt.Sprintf("model:     %s\n", s.model)
	frame += fmt.Sprintf("workspace: %s\n", s.workspace)
	frame += fmt.Sprintf("uptime:    %s\n", uptime)
	frame += fmt.Sprintf("tasks:     %d ok  %d failed\n", ok, failed)
	if gw := s.gatewayState(); gw != "" {
		frame += fmt.Sprintf("gateway:   %s\n", gw)
	}

	if task != "" {
		frame += fmt.Sprintf("\ncurrent task:\n  %s\n", task)
	} else if !s.lastTaskAt.IsZero() {
		frame += fmt.Sprintf("\nlast task completed: %s ago\n", time.Since(s.lastTaskAt).Truncate(time.Second))
	} else {
		frame += "\nno tasks yet — waiting for work\n"
	}

	if lastErr != "" {
		frame += fmt.Sprintf("\nlast error:\n  %s\n", lastErr)
	}
	return frame
}

// daemonModel implements tea.Model + A2ADebugger so it can be passed to
// AttachedServer (which expects a tea.Model for View/State and optionally
// an A2ADebugger for the debug command). No bubbletea event loop runs;
// this is purely a state-rendering shim.
type daemonModel struct {
	state *daemonState
	sdk   *chat.SDKIntegration
}

func (m *daemonModel) Init() tea.Cmd                           { return nil }
func (m *daemonModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (m *daemonModel) View() tea.View {
	return tea.NewView(m.state.renderFrame())
}

func (m *daemonModel) GetA2ADebug() protocol.A2ADebugResponse {
	status, task, lastErr := m.state.snapshot()
	uptime := time.Since(m.state.startedAt).Truncate(time.Second)

	notes := []string{
		fmt.Sprintf("type: daemon"),
		fmt.Sprintf("uptime: %s", uptime),
		fmt.Sprintf("tasks_ok: %d", m.state.tasksOK.Load()),
		fmt.Sprintf("tasks_failed: %d", m.state.tasksFailed.Load()),
	}
	if lastErr != "" {
		notes = append(notes, fmt.Sprintf("last_error: %s", lastErr))
	}

	resp := protocol.A2ADebugResponse{
		Timestamp:        time.Now().UTC().Format(time.RFC3339Nano),
		AgentState:       status.String(),
		SwarmCurrentTask: task,
		Notes:            notes,
	}

	// Augment with live A2A runtime data if available.
	if m.sdk != nil && m.sdk.IsA2AEnabled() {
		// The runtime exposes task queue depth via the debug surface of the TUI;
		// here we surface what we can from the SDK's public API.
		resp.Notes = append(resp.Notes,
			fmt.Sprintf("endpoint: %s", m.sdk.GetA2AAddress()),
			fmt.Sprintf("handle: %s", m.sdk.GetA2AHandle()),
		)
	}
	return resp
}

func runDaemon(cfg daemonConfig) error {
	// Single-instance enforcement: hold an advisory lock for this handle for
	// the whole process lifetime. A second daemon with the same handle would
	// otherwise fight over the presence file and the gateway port. flock dies
	// with the process, so a crashed daemon never leaves a stale lock.
	releaseLock, lockIdentity, lockOK, lockErr := acquireDaemonLockWithIdentity(daemonInstanceLockName(cfg.handle), false)
	if lockErr != nil {
		return fmt.Errorf("daemon instance lock unavailable: %w", lockErr)
	} else if !lockOK {
		return fmt.Errorf("daemon %q is already running on this machine (instance lock held) — attach with `swarmos swarm attach %s` or stop it with `swarmos swarm stop`",
			cfg.handle, cfg.handle)
	}
	defer releaseLock()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── 1. Build SDK ─────────────────────────────────────────────────────────
	providerName := cfg.provider
	modelName := cfg.model

	// Fall back to saved config when flags are empty (mirrors runHeadless behaviour).
	if cfgMgr, err := commands.NewConfigManager(); err == nil {
		if saved, err := cfgMgr.LoadConfig(); err == nil && saved != nil {
			if providerName == "" {
				providerName = saved.CurrentProvider
			}
			if modelName == "" {
				modelName = saved.CurrentModel
			}
		}
	}
	if providerName == "" {
		providerName = "claudecode"
	}
	if modelName == "" {
		modelName = "claude-sonnet-4-20250514"
	}

	permConfig, _ := chat.LoadPermissionConfig()
	if permConfig == nil {
		permConfig = chat.NewPermissionConfigWithDefaults()
	}

	// Interactive approval routes each tool-call approval to the attached UI over
	// the daemon event stream (EventApprovalRequested + respondApproval); headless
	// auto-approves. The interactive broker needs the client, wired post-build.
	var approvalBroker tools.ApprovalBroker = chat.NewHeadlessApprovalBroker()
	var interactiveBroker *daemonApprovalBroker
	if cfg.approval == "interactive" {
		interactiveBroker = &daemonApprovalBroker{}
		approvalBroker = interactiveBroker
		// Force the dangerous permissions to PolicyAsk so the broker is actually
		// consulted (a loaded config may auto-allow them, and PolicyDeny denies
		// outright without asking). Without this the interactive broker never fires.
		pc := permConfig.Config()
		pc.Level = tools.LevelAlwaysAsk
		if pc.Defaults.Policies == nil {
			pc.Defaults.Policies = map[tools.Permission]tools.PermissionPolicy{}
		}
		for _, p := range []tools.Permission{
			tools.PermissionBashExecute, tools.PermissionFileWrite,
			tools.PermissionFileDelete, tools.PermissionNetworkAccess,
		} {
			pc.Defaults.Policies[p] = tools.PolicyAsk
		}
		permConfig.SetConfig(pc)
	}

	sdk, err := chat.NewSDKIntegrationWithOptions(providerName, modelName, chat.SDKIntegrationOptions{
		ApprovalBroker:   approvalBroker,
		PermissionConfig: permConfig,
	})
	if err != nil {
		return fmt.Errorf("SDK init: %w", err)
	}

	// Non-interactive vault auto-load (same as headless -p): make team /
	// two-person credentials available to the daemon's agent without prompting.
	if home, herr := os.UserHomeDir(); herr == nil {
		if provider, ok := vault.AutoLoadProvider(vault.DefaultAutoLoadPaths(home, cfg.workspace, cfg.workspace)); ok {
			sdk.SetVaultProvider(provider)
		}
	}
	if interactiveBroker != nil {
		interactiveBroker.SetClient(sdk.SDKClient())
	}

	// Start the client's agent bridge so interactive turns dispatched over the
	// serve interface (client.SendMessage) actually EXECUTE. Without Start() the
	// agent bridge that drives ChatCtx is unwired and turns silently no-op
	// (0 tokens, nothing persisted). The TUI starts its client the same way.
	if cl := sdk.SDKClient(); cl != nil {
		if serr := cl.Start(ctx); serr != nil {
			fmt.Fprintf(os.Stderr, "daemon: client start: %v\n", serr)
		}
		// Honour --no-fallback: collapse the agent's chain to its primary so a
		// pinned provider/model failure surfaces verbatim instead of cascading
		// into other (possibly unregistered) providers.
		if cfg.noFallback {
			if ferr := cl.SetNoFallback(ctx, true); ferr != nil {
				fmt.Fprintf(os.Stderr, "daemon: set no-fallback: %v\n", ferr)
			} else {
				fmt.Println("daemon: fallback chain disabled (--no-fallback)")
			}
		}
	}

	// ── 2. Enable A2A (starts HTTP server + filesystem JoinSwarm) ─────────────
	// sdk.JoinSwarm starts the A2A HTTP runtime and performs its own initial
	// filesystem JoinSwarm — this is prerequisite SDK/network initialization
	// and, per Phase 01 CONTRACT.md, must run OUTSIDE the per-handle registry
	// transaction claimDaemonControlSocket opens below (its callback must
	// never call a lock-reacquiring A2A operation, and JoinSwarm's own
	// internal write already reacquires this handle's registry lock).
	state := newDaemonState(cfg.handle, modelName, cfg.workspace)
	state.instanceToken = lockIdentity.Token
	state.processStart = lockIdentity.ProcessStart
	state.executable = lockIdentity.Executable
	ctrlSockDir := filepath.Join(a2a.SwarmPath(a2a.DefaultSwarmName), "peers")
	ctrlSock := filepath.Join(ctrlSockDir, cfg.handle+".ctrl")
	model := &daemonModel{state: state, sdk: sdk}
	ctrlSrv := attserver.NewAttached(nil, model, 220, 50, nil, ctrlSock)
	if err := sdk.JoinSwarm(ctx, cfg.handle, cfg.handle); err != nil {
		return fmt.Errorf("join swarm: %w", err)
	}
	// Claim the handle-specific control socket: stale-socket inspection and
	// removal, bind, chmod, and socket identity capture (all inside
	// AttachedServer.Listen) plus the instance-evidence presence publication
	// join ONE a2a.WithPeerHandleTransaction on this handle — the same shared
	// per-handle transaction conditional cleanup (RemovePeerIfInstance) and
	// any concurrent replacement startup use. A duplicate live handle must
	// fail without overwriting the incumbent daemon; any error after a
	// successful bind calls the identity-checked Stop before this function
	// returns (and therefore before WithPeerHandleTransaction releases the
	// per-handle lock), so a concurrent same-handle transaction can never
	// observe a bound-but-abandoned socket while believing the lock is free.
	if err := claimDaemonControlSocket(ctrlSrv, cfg.handle, *lockIdentity); err != nil {
		return err
	}
	defer func() {
		removed, _ := a2a.RemovePeerIfInstance(a2a.DefaultSwarmName, cfg.handle, lockIdentity.Token)
		if removed {
			fmt.Printf("daemon %s left swarm\n", cfg.handle)
		}
	}()
	defer ctrlSrv.Stop()
	_ = cfg.listen // SDK uses its own default listen; flag reserved for future use

	endpointURL := sdk.GetA2AAddress()
	fmt.Printf("daemon %s ready at %s\n", cfg.handle, endpointURL)

	// ── 3. State tracker ─────────────────────────────────────────────────────

	// ── 4. Install inbound request handler ──────────────────────────────────
	sdk.InstallDaemonA2ARequestHandler(
		func(peerHandle, taskText string) {
			state.setWorking(taskText)
			_ = a2a.UpdateStatus(a2a.DefaultSwarmName, cfg.handle, lifecycle.StateWorking.String(), taskText)
			fmt.Printf("[daemon] task from %s: %s\n", peerHandle, truncate(taskText, 80))
		},
		func(peerHandle string, execErr error) {
			state.setIdle(execErr)
			// ADR-005: "ready" is the canonical wire value for an idle daemon
			// that can accept work — "idle" is a legacy/derived display label
			// and must never be the current writer's canonical output.
			_ = a2a.UpdateStatus(a2a.DefaultSwarmName, cfg.handle, lifecycle.StateReady.String(), "")
			if execErr != nil {
				fmt.Printf("[daemon] task from %s failed: %v\n", peerHandle, execErr)
			} else {
				fmt.Printf("[daemon] task from %s complete\n", peerHandle)
			}
			// Skill maintenance: when the daemon goes idle after a task, run the
			// curator if the daily cadence gate (Curator.MinRunGap) has elapsed.
			// This keeps the autogen skills list pruned so the system prompt
			// doesn't grow unbounded. No-op if not configured or not yet due.
			if hm := sdk.GetHooksManager(); hm != nil {
				if hm.RunCuratorIfDue() {
					fmt.Printf("[daemon] curator: running skill maintenance pass\n")
				}
			}
		},
	)

	// ── 5. Bind attach surfaces BEFORE advertising them ──────────────────────
	// LISTEN-THEN-ADVERTISE: the presence file is how clients discover this
	// daemon, so every endpoint written into it must already be accepting
	// connections. The old order (advertise control socket + type, then bind
	// listeners in goroutines) created a window where `swarm attach` right
	// after `swarm up` found a peer whose socket was not bound yet ("connection
	// refused") or whose serve_url was still empty ("exposes no serve
	// endpoint") — the classic "attach sometimes doesn't work".
	// 5b. Serve interface (/rpc /sse /ws /healthz /readyz): bind + start serving.
	var serveURL string
	var serveMux *serve.Mux
	if cl := sdk.SDKClient(); cl != nil {
		serveMux = serve.NewMux(cl)
		httpMux := http.NewServeMux()
		httpMux.Handle("/rpc", serveMux.HTTPHandler())
		httpMux.Handle("/sse", serveMux.SSEHandler())
		httpMux.Handle("/ws", serveMux.WebSocketHandler())
		// Real health check: proves the HTTP serve loop is alive (a TCP
		// connect can succeed against a wedged process). LIVENESS ONLY
		// (ADR-005) — ensureGlobalDaemon's liveness-only probe
		// (daemonHealthy, global_daemon.go) still uses this for the
		// crashed-vs-wedged distinction, but never as readiness or
		// identity-only proof by itself.
		httpMux.Handle("/healthz", daemonHealthzHandler(state))
		// Readiness (ADR-005 "Readiness, publication, and adapters"),
		// distinct from /healthz above: true only once this process has
		// itself self-probed (selfProbeReady, invoked below) and proven
		// its own identity-bearing /healthz response internally
		// consistent. ensureGlobalDaemon prefers a positive,
		// identity-matched /readyz over the old liveness-only /healthz
		// 200/404 check before treating an existing candidate as
		// usable-ready (see global_daemon.go's daemonReady).
		httpMux.Handle("/readyz", daemonReadyzHandler(state))
		ln, lerr := net.Listen("tcp", cfg.listen)
		if lerr != nil {
			fmt.Fprintf(os.Stderr, "daemon: client-interface listen %s: %v\n", cfg.listen, lerr)
		} else {
			serveURL = "http://" + dialableAddr(ln.Addr().String())
			srv := newDaemonClientServer(httpMux)
			go func() {
				if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
					fmt.Fprintf(os.Stderr, "daemon: client-interface error: %v\n", err)
				}
			}()
			// Graceful drain on shutdown: let in-flight /rpc requests finish
			// (bounded) instead of dropping them mid-response.
			defer func() {
				shCtx, shCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer shCancel()
				_ = srv.Shutdown(shCtx)
			}()
		}
	}

	// 5b-2. Self-probe BEFORE advertising (ADR-005): "The daemon binds its
	// handle-specific control socket and HTTP listener, starts serving, and
	// self-probes required endpoints before publishing `ready`." This is
	// the fix for the historical bug where in-memory status started as
	// `ready` (and presence could in principle advertise readiness) before
	// any self-probe had run at all — daemonState now starts in `starting`
	// (see newDaemonState) and only reaches `ready` here, via
	// selfProbeReady's legal `starting -> ready` transition, strictly
	// before publishDaemonAttachEndpoints below.
	//
	// A daemon whose client interface never bound (serveURL == "") has no
	// endpoint to self-probe at all — that is itself a lost required
	// dependency, so it goes straight to `degraded`/dependency_lost rather
	// than hanging out the full self-probe window waiting on a probe that
	// can never succeed.
	if serveURL != "" {
		probeCtx, probeCancel := context.WithTimeout(ctx, readinessSelfProbeTimeout)
		probe := func() (daemonHealthEvidence, error) { return daemonHealthEvidenceFunc(serveURL) }
		if perr := selfProbeReady(probeCtx, state, probe, func() bool { return true }); perr != nil {
			fmt.Fprintf(os.Stderr, "daemon: readiness self-probe: %v\n", perr)
		}
		probeCancel()
	} else if terr := state.transition(lifecycle.StateDegraded, lifecycle.IntentObserve, lifecycle.ReasonDependencyLost); terr != nil {
		fmt.Fprintf(os.Stderr, "daemon: mark degraded (no client interface bound): %v\n", terr)
	}
	// Publish the self-probed status (ready, or degraded on timeout/missing
	// dependency) into presence BEFORE the endpoint publication below, so
	// no reader can ever observe endpoints advertised alongside a status
	// that predates this self-probe.
	probedStatus, _, _ := state.snapshot()
	_ = a2a.UpdateStatus(a2a.DefaultSwarmName, cfg.handle, probedStatus.String(), "")

	// 5c. NOW advertise: one consolidated presence write with every endpoint
	// already live (control socket bound, serve listener serving).
	// This final publication reuses the SAME per-handle transaction contract:
	// it reads the existing instance evidence and ControlSocket claimed above
	// via tx.Get(), layers on Type/Workspace/ServeURL/Version/BinaryModTime,
	// and writes with tx.Publish() — never JoinSwarm — so it can never
	// deadlock against the still-open lock domain. Any error here (after the
	// control socket was already successfully bound above) also calls the
	// identity-checked Stop before the transaction releases the lock.
	if err := publishDaemonAttachEndpoints(ctrlSrv, cfg.handle, cfg.workspace, serveURL); err != nil {
		return err
	}

	// ── 6. Run the control socket accept loop (already bound in 5a) ──────────
	ctrlCtx, ctrlCancel := context.WithCancel(ctx)
	defer ctrlCancel()
	go func() {
		if err := ctrlSrv.Start(ctrlCtx); err != nil && ctrlCtx.Err() == nil {
			fmt.Fprintf(os.Stderr, "daemon: control socket error: %v\n", err)
		}
	}()

	// ── 6b. LAN GATEWAY (in-process): bind 0.0.0.0 and serve the mobile
	// PWA + control API on the SAME serve mux, so phones and Swarm Desktop
	// on the WiFi reach this daemon directly (no proxy hop — the daemon IS
	// the peer). Best-effort: a bind failure is logged and never fatal, so
	// the loopback serve interface above keeps working regardless.
	if cfg.gatewayOn && serveMux != nil {
		startDaemonGateway(ctx, cfg, serveMux, state)
	}

	// ── 6c. LAN peer registry: advertise this machine's peers over UDP gossip
	// and ingest other machines' peers, so phones/desktops attached to ANY
	// gateway on the LAN can see and reach agents on every machine. This was
	// previously wired only into the swarm-ic backend, which made
	// cross-machine attach dead code for plain daemons. Explicit opt-in only:
	// production wiring requires SWARM_LAN_REGISTRY=1 (Phase 01 CONTRACT.md
	// R5) — unset, empty, "0", and any other value leave it disabled.
	if lanRegistryEnabled(os.Getenv("SWARM_LAN_REGISTRY")) {
		if stopLAN, lerr := a2a.StartLANRegistry(ctx, a2a.DefaultSwarmName); lerr != nil {
			fmt.Fprintf(os.Stderr, "daemon: LAN peer registry unavailable (non-fatal): %v\n", lerr)
		} else {
			defer stopLAN()
			fmt.Println("daemon: LAN peer registry active (UDP gossip on :8789)")
		}
	}

	// ── 7. Heartbeat: keep LastSeenAt fresh and correct status ────────────────
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s, task, _ := state.snapshot()
				_ = a2a.UpdateStatus(a2a.DefaultSwarmName, cfg.handle, s.String(), task)
			}
		}
	}()

	// ── 7b. Daily curator floor: guarantees skill maintenance runs even if the
	// daemon never returns to idle via the request path (e.g. always-idle with
	// no tasks, or long-lived). The curator's own MinRunGap gate (default 24h)
	// makes this a no-op until actually due, so a 1h poll is cheap and safe.
	go func() {
		t := time.NewTicker(1 * time.Hour)
		defer t.Stop()
		// Kick once shortly after startup so a daemon that's been down past the
		// cadence window catches up without waiting a full hour.
		startup := time.NewTimer(2 * time.Minute)
		defer startup.Stop()
		runIfDue := func() {
			if hm := sdk.GetHooksManager(); hm != nil {
				if hm.RunCuratorIfDue() {
					fmt.Printf("[daemon] curator: daily skill maintenance pass\n")
				}
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-startup.C:
				runIfDue()
			case <-t.C:
				runIfDue()
			}
		}
	}()

	fmt.Printf("daemon %s listening — press Ctrl-C to stop\n", cfg.handle)
	if serveURL != "" {
		fmt.Printf("  client:  %s/rpc   (events: %s/sse)\n", serveURL, serveURL)
	}
	fmt.Printf("  attach:  swarmos swarm attach %s\n", cfg.handle)
	fmt.Printf("  task:    swarmos swarm task %s \"your prompt\"\n", cfg.handle)

	<-ctx.Done()
	// Signal received: FIRST transition the in-process lifecycle authority
	// through the already-existing `draining` state (ADR-005 "Drain and
	// stop": "Graceful shutdown first enters draining, atomically refuses
	// new tasks, and allows accepted tasks and HTTP requests to finish. It
	// then enters stopping before releasing resources.") via the ONE legal
	// choke point, daemonState.transition/tryTransitionLocked
	// (CONTRACT.md section 3) — never a bare `state.status = ...` write.
	// beginDaemonDrain tries the drain intent/reason from every state the
	// table's ready/working/degraded -> draining rows accept; a daemon
	// still `starting` (no starting->draining row exists) logs and
	// continues, since there is no accepted work to drain in that case.
	if terr := beginDaemonDrain(state); terr != nil {
		fmt.Fprintf(os.Stderr, "daemon: drain transition: %v\n", terr)
	}
	// Advertise "stopping" so pollers and attach clients see
	// an intentional shutdown (not a crash) while the deferred drain runs.
	_ = a2a.UpdateStatus(a2a.DefaultSwarmName, cfg.handle, lifecycle.StateStopping.String(), "")
	fmt.Printf("daemon %s stopping (draining connections)\n", cfg.handle)
	return nil
}

// beginDaemonDrain transitions state into lifecycle.StateDraining through
// the existing daemonState.transition choke point (daemon_cli.go's
// tryTransitionLocked, CONTRACT.md section 3: "Worker C MUST call the
// existing daemonState.transition ... method -- do NOT invent a second
// transition helper or bypass it with a bare field write").
//
// ADR-005's normative table permits `ready -> draining` and
// `working -> draining` (and, via the degraded recovery rows,
// `degraded -> draining`) all under the SAME (intent, reason) pair —
// IntentDrain/ReasonOperatorDrain — so a single ValidateTransition
// candidate covers every live pre-drain source state; the row lookup keys
// on state.status (the actual "from"), not on which candidate is passed.
// A daemon still `starting` has no `starting -> draining` row in the table
// (ADR-005 deliberately has none: intake was never opened, so there is
// nothing to drain) — that in-progress candidate instead only has a legal
// path to `stopping`/`stopped`, so this call legally fails closed for it
// and the caller (runDaemon) logs and proceeds with shutdown regardless,
// exactly as it already does for every other beginDaemonDrain error.
//
// Factored out of runDaemon's inline shutdown sequence so
// drain_test.go can exercise it directly against a constructed
// *daemonState without booting a full daemon process.
func beginDaemonDrain(state *daemonState) error {
	if state == nil {
		return fmt.Errorf("daemon_cli: beginDaemonDrain called with nil state")
	}
	return state.transition(lifecycle.StateDraining, lifecycle.IntentDrain, lifecycle.ReasonOperatorDrain)
}

// daemonHealthzHandler serves GET /healthz with a JSON snapshot of daemon
// health. 200 means the serve loop is alive and dispatching; the payload
// carries identity so operators can spot a stale binary at a glance.
func daemonHealthzHandler(state *daemonState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, task, lastErr := state.snapshot()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":             true,
			"handle":         state.handle,
			"status":         status.String(),
			"current_task":   task,
			"last_error":     lastErr,
			"gateway":        state.gatewayState(),
			"uptime_s":       int(time.Since(state.startedAt).Seconds()),
			"tasks_ok":       state.tasksOK.Load(),
			"tasks_failed":   state.tasksFailed.Load(),
			"version":        version.Version,
			"pid":            os.Getpid(),
			"instance_token": state.instanceToken,
			"process_start":  state.processStart,
			"executable":     state.executable,
		})
	}
}

// dialableAddr converts a listener address with an unspecified host
// ("[::]:7000", "0.0.0.0:7000") into a locally dialable one
// ("127.0.0.1:7000"). Presence files must never advertise an undialable
// wildcard host — local clients dial it verbatim, and the LAN gossip rewrites
// loopback (not wildcard-in-brackets) to the machine's LAN IP.
func dialableAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "::" || host == "0.0.0.0" {
		return net.JoinHostPort("127.0.0.1", port)
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return net.JoinHostPort("127.0.0.1", port)
	}
	return addr
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// claimDaemonControlSocket performs the whole "claim this handle's control
// socket" sequence — stale-socket inspection/removal, bind, chmod, and
// socket identity capture (all inside AttachedServer.Listen), followed by
// the instance-evidence presence publication — inside ONE
// a2a.WithPeerHandleTransaction for handle. This is the shared per-handle
// cross-process transaction Phase 01 CONTRACT.md requires replacement
// control-socket bind/publication to join with conditional cleanup
// (RemovePeerIfInstance) and any other replacement instance's own startup:
// they all serialize on the exact same registry handle lock.
//
// The callback below touches the registry ONLY through tx.Get()/tx.Publish()
// — never JoinSwarm, LeaveSwarm, UpdateStatus, or RemovePeerIfInstance, all
// of which would try to reacquire the same non-reentrant per-handle lock
// this transaction already holds and deadlock the calling goroutine against
// itself. ctrlSrv.Listen()/Stop() only ever touch AttachedServer's own
// mutex, never the registry lock, so this preserves the required lock order
// (registry handle lock, then AttachedServer.mu — never the reverse).
//
// Every error path after a successful Listen() calls the identity-checked
// ctrlSrv.Stop() BEFORE returning from the callback — and therefore before
// WithPeerHandleTransaction releases the per-handle lock — so a concurrent
// same-handle transaction (conditional cleanup, or a replacement instance
// racing this one) can never observe a bound-but-abandoned socket while
// believing the lock is already free. A Listen() failure itself calls Stop
// only when Listen partially bound before failing; AttachedServer.Listen is
// written so a failed call never leaves s.listener set, so no extra Stop is
// required on that path, and no presence mutation is attempted.
func claimDaemonControlSocket(ctrlSrv *attserver.AttachedServer, handle string, identity daemonLockIdentity) error {
	return a2a.WithPeerHandleTransaction(a2a.DefaultSwarmName, handle, func(tx *a2a.PeerHandleTransaction) error {
		if err := ctrlSrv.Listen(); err != nil {
			return fmt.Errorf("bind daemon control socket: %w", err)
		}
		existing, err := tx.Get()
		if err != nil {
			ctrlSrv.Stop()
			return fmt.Errorf("load daemon presence for instance evidence: %w", err)
		}
		if existing == nil {
			ctrlSrv.Stop()
			return fmt.Errorf("daemon presence missing before instance evidence publication")
		}
		existing.InstanceToken = identity.Token
		existing.ProcessStart = identity.ProcessStart
		existing.Executable = identity.Executable
		existing.ControlSocket = ctrlSrv.SocketPath()
		if err := tx.Publish(*existing); err != nil {
			ctrlSrv.Stop()
			return fmt.Errorf("publish daemon instance evidence: %w", err)
		}
		return nil
	})
}

// publishDaemonAttachEndpoints performs the final replacement presence
// publication — Type, Workspace, ControlSocket, ServeURL, Version, and
// BinaryModTime layered on top of the instance evidence claimDaemonControlSocket
// already published — inside the SAME per-handle a2a transaction contract.
// serveURL may be empty when the optional client interface failed to bind;
// ControlSocket is always ctrlSrv.SocketPath() since Listen already
// succeeded by the time this runs. Exactly like claimDaemonControlSocket,
// any error here calls the identity-checked Stop before this transaction's
// per-handle lock is released, and the callback never calls a
// lock-reacquiring A2A operation.
func publishDaemonAttachEndpoints(ctrlSrv *attserver.AttachedServer, handle, workspace, serveURL string) error {
	return a2a.WithPeerHandleTransaction(a2a.DefaultSwarmName, handle, func(tx *a2a.PeerHandleTransaction) error {
		existing, err := tx.Get()
		if err != nil {
			ctrlSrv.Stop()
			return fmt.Errorf("load daemon presence for endpoint publication: %w", err)
		}
		if existing == nil {
			ctrlSrv.Stop()
			return fmt.Errorf("daemon presence missing before endpoint publication")
		}
		existing.Type = a2a.PeerTypeDaemon
		existing.Workspace = workspace
		existing.ControlSocket = ctrlSrv.SocketPath()
		existing.ServeURL = serveURL
		// Record binary identity so ensureGlobalDaemon can detect a daemon
		// left running outdated code after a swarm update and restart it
		// when idle.
		existing.Version = version.Version
		existing.BinaryModTime = binaryModTime()
		if err := tx.Publish(*existing); err != nil {
			ctrlSrv.Stop()
			return fmt.Errorf("publish daemon attach endpoints: %w", err)
		}
		return nil
	})
}

// startDaemonGateway binds the LAN gateway on cfg.gatewayAddr (0.0.0.0) and
// serves the mobile PWA + control API over the daemon's OWN serve mux, so
// phones and Swarm Desktop on the WiFi drive this daemon directly. It is
// best-effort: a bind error is logged and never fatal. The daemon's default is
// intentionally open LAN control; SWARM_GATEWAY_TOKEN remains advertised for
// clients that still want to send a token.
func startDaemonGateway(ctx context.Context, cfg daemonConfig, mux *serve.Mux, state *daemonState) {
	token := os.Getenv("SWARM_GATEWAY_TOKEN")
	opts := gateway.Options{
		Swarm:                   a2a.DefaultSwarmName,
		Token:                   token,
		ListenAddr:              cfg.gatewayAddr,
		AllowUnauthenticatedLAN: true,
		RPCHandler:              mux.HTTPHandler(),
		SSEHandler:              mux.SSEHandler(),
		WSHandler:               nativeGatewayWSHandler(mux.WebSocketHandler()),
		SelfHandle:              cfg.handle,
	}
	if _, err := gateway.AuthorizeListener(cfg.gatewayAddr, opts); err != nil {
		// Listener authorization is a pre-bind policy gate. A denied gateway
		// must not open a socket, retry, or affect the loopback engine.
		fmt.Fprintf(os.Stderr, "daemon: gateway disabled for %s: %v\n", cfg.gatewayAddr, err)
		if state != nil {
			state.setGateway(fmt.Sprintf("disabled for %s (%v)", cfg.gatewayAddr, err))
		}
		return
	}
	gw := gateway.New(opts)

	ln, err := net.Listen("tcp", cfg.gatewayAddr)
	if err != nil {
		// Usually another process holds the port. Keep retrying so the
		// gateway comes up as soon as the port frees, instead of silently
		// leaving phones with no endpoint until the next manual restart —
		// and record the failure where healthz/frame can show it.
		fmt.Fprintf(os.Stderr, "daemon: LAN gateway bind %s failed (%v) — retrying every 15s\n", cfg.gatewayAddr, err)
		if state != nil {
			state.setGateway(fmt.Sprintf("bind %s failed (%v) — retrying every 15s", cfg.gatewayAddr, err))
		}
		go func() {
			t := time.NewTicker(15 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					ln, err := net.Listen("tcp", cfg.gatewayAddr)
					if err != nil {
						continue
					}
					fmt.Fprintf(os.Stderr, "daemon: LAN gateway bound %s after retry\n", cfg.gatewayAddr)
					serveDaemonGateway(ctx, cfg, gw, ln, token, state)
					return
				}
			}
		}()
		return
	}
	serveDaemonGateway(ctx, cfg, gw, ln, token, state)
}

// serveDaemonGateway serves the gateway on an established listener and starts
// the discovery beacon + banner.
func serveDaemonGateway(ctx context.Context, cfg daemonConfig, gw *gateway.Server, ln net.Listener, token string, state *daemonState) {
	if state != nil {
		state.setGateway("listening " + ln.Addr().String())
	}
	srv := newDaemonGatewayServer(gw.Handler())
	go func() {
		if serr := srv.Serve(ln); serr != nil && serr != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "daemon: LAN gateway error: %v\n", serr)
		}
	}()
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	// A loopback listener is deliberately not advertised as a LAN endpoint.
	// The daemon's explicit open-LAN mode uses the plaintext discovery beacon.
	if !gateway.IsLoopbackAddr(cfg.gatewayAddr) {
		go gateway.StartBeacon(ctx, gateway.PortOf(cfg.gatewayAddr), a2a.DefaultSwarmName, token != "")
	}
	gateway.PrintBanner("swarm daemon gateway", cfg.gatewayAddr, token)
}

// nativeGatewayWSHandler adapts the Tauri WebView's app origin to the serve
// WebSocket handler. Tauri Android sends Origin: http://tauri.localhost (some
// versions use localhost), while serve's browser CSRF defense intentionally
// requires a browser Origin to match the network Host exactly. The LAN daemon
// gateway is the explicitly open native-app surface, so only these exact
// native origins are admitted; arbitrary cross-origin browser requests still
// reach serve's normal same-origin rejection.
func nativeGatewayWSHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || !isNativeGatewayOrigin(origin) {
			next.ServeHTTP(w, r)
			return
		}
		clone := r.Clone(r.Context())
		clone.Header = r.Header.Clone()
		clone.Header.Del("Origin")
		next.ServeHTTP(w, clone)
	})
}

func isNativeGatewayOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return false
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "tauri.localhost" || host == "localhost"
}
