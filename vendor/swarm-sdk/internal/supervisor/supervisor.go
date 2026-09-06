// Package supervisor reconciles the requested swarm daemon lifecycle with an
// OS service supervisor. Per package-boundaries.md's "supervisor" section
// (docs/architecture/swarm-attach/package-boundaries.md) this package
// performs process/service actions but never decides application readiness,
// and it must never signal a process from a bare PID alone.
//
// This phase (P08.2, see
// .swarmflow/swarm-attach-architecture/p08-package-decomposition/CONTRACT.md)
// extracts ONLY the systemd-user-unit behavior that already existed in
// swarm-tui/cmd/swarmos/service_cli.go (a behavior-preserving move, not a
// redesign). OS-specific command invocation is hidden behind the
// CommandRunner interface below so a future non-Linux implementation can be
// added later without changing the Manager interface — but no such
// implementation is attempted this phase; see this package's INDEX.md
// "Deferred" section.
package supervisor

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Mode is the detected OS-service supervision mode for the swarm daemon.
//
// package-boundaries.md's "supervisor" section calls out reconciliation
// tables for "absent/manual/systemd" states as a focused-test requirement.
// This phase's extraction only preserves the two states the original
// service_cli.go code already distinguished (a Linux host with `systemctl`
// on PATH, versus everything else); a "manual" (non-systemd, hand-managed
// process) mode is not detected by any code this phase extracts and is left
// for a later phase's daemon-composition-root work.
type Mode string

const (
	// ModeSystemdUser means the current host is Linux and `systemctl` is on
	// PATH — systemd user-unit supervision is available and authoritative
	// when it owns the service, per package-boundaries.md's invariant "If
	// systemd owns the unit, lifecycle actions go through systemd."
	ModeSystemdUser Mode = "systemd-user"
	// ModeNoSystemd means the current host is Linux but `systemctl` could
	// not be found on PATH.
	ModeNoSystemd Mode = "no-systemd"
	// ModeUnsupportedOS means runtime.GOOS is not "linux". The original
	// service_cli.go code only ever implemented Linux/systemd-user-unit
	// supervision; every other platform got a guidance error.
	ModeUnsupportedOS Mode = "unsupported-os"
)

// UnitName is the systemd user-unit name the swarm daemon is installed
// under. It is exported because swarm-tui/cmd/swarmos/global_daemon.go
// (explicitly out of scope for this extraction — daemon composition-root
// extraction is deferred) references the identical value directly today via
// service_cli.go's package-main `serviceUnitName` constant, which now aliases
// this one.
const UnitName = "swarm-daemon.service"

// Spec is the desired service specification: the binary path and unit
// parameters `Install` bakes into the generated systemd user unit, and
// `Start` uses to know what to launch.
type Spec struct {
	// BinaryPath is the absolute path to the swarm binary that becomes the
	// unit's ExecStart target (originally `service_cli.go`'s `self`, from
	// `os.Executable()`).
	BinaryPath string
	// A2AHandle is the `--a2a-handle` value baked into ExecStart.
	A2AHandle string
	// A2AListen is the `--a2a-listen` value baked into ExecStart.
	A2AListen string
	// GatewayAddr is the `--gateway-addr` value baked into ExecStart.
	GatewayAddr string
}

// Identity is a verified process/service identity tuple used to target
// Stop/Restart at a specific supervised unit. The empty value targets the
// default swarm daemon unit (UnitName). Full process-identity verification
// (peer type, daemon instance token, process start identity, user
// ownership) is owned by swarm-tui/cmd/swarmos/daemon_lock.go, which is
// explicitly out of scope for this extraction — this type intentionally
// stays a thin unit-name selector, not a reimplementation of that logic.
type Identity struct {
	// UnitName optionally overrides the default swarm-daemon systemd
	// user-unit name. Empty means "the default unit" (UnitName).
	UnitName string
}

// Status is the result of a Status/Detect operation: the detected
// supervision Mode plus whether the unit is currently active. It is an
// operation result, not cached daemon truth — package-boundaries.md's
// supervisor section: "It does not cache daemon status as truth."
type Status struct {
	// Mode is the supervision mode Detect classified this host as.
	Mode Mode
	// Active reports whether `systemctl --user status` (or equivalent)
	// reported the unit active at the moment of the call. Only meaningful
	// when Mode == ModeSystemdUser.
	Active bool
}

// Manager is the supervisor package's sole public entry point. Signature
// fixed verbatim by package-boundaries.md's "supervisor" section and
// CONTRACT.md section 3 — do not change without updating both documents and
// this package's INDEX.md in the same change.
type Manager interface {
	Detect(context.Context) (Mode, error)
	Status(context.Context) (Status, error)
	Start(context.Context, Spec) error
	Stop(context.Context, Identity) error
	Restart(context.Context, Identity) error
	Install(context.Context, Spec) error
	Uninstall(context.Context) error
}

// CommandRunner abstracts external process invocation. Production code uses
// execCommandRunner (os/exec-backed); tests inject a fake so this package's
// systemd-user-unit logic is exercised without shelling out to a real
// systemctl/loginctl. This is the "clean internal boundary" mentioned in
// this package's INDEX.md that lets a future non-Linux implementation swap
// in without changing the Manager interface.
type CommandRunner interface {
	// LookPath reports whether the named executable is present on PATH,
	// mirroring exec.LookPath.
	LookPath(name string) (string, error)
	// Run executes name with args, connecting stdout/stderr to the given
	// writers (either may be nil, discarding that stream, matching
	// (*exec.Cmd).Run's behavior when Stdout/Stderr are left unset), and
	// returns the command's error verbatim.
	Run(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) error
}

// execCommandRunner is the production CommandRunner, backed by os/exec.
type execCommandRunner struct{}

func (execCommandRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (execCommandRunner) Run(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// systemdUserManager is the concrete Manager implementation for the current
// (Linux/systemd-user-unit) OS target. Its runtime.GOOS checks make every
// method a safe no-op-with-guidance-error on non-Linux hosts, matching the
// original per-function service_cli.go behavior (Install's checks were NOT
// identical to Uninstall's/Status's in the original code — preserved
// faithfully below, see each method's doc comment).
type systemdUserManager struct {
	runner CommandRunner
	stdout io.Writer
	stderr io.Writer
}

// NewManager constructs a Manager backed by the given CommandRunner, with
// stdout/stderr streamed to os.Stdout/os.Stderr (matching the original
// service_cli.go code, which always inherited the process's own stdio for
// the systemctl invocations it streamed).
func NewManager(runner CommandRunner) Manager {
	return newManagerWithIO(runner, os.Stdout, os.Stderr)
}

// NewDefaultManager constructs the production Manager: a real os/exec-backed
// CommandRunner streaming to the process's real stdout/stderr.
func NewDefaultManager() Manager {
	return NewManager(execCommandRunner{})
}

// newManagerWithIO is the test seam: it lets supervisor_test.go inject
// buffers/io.Discard in place of the process's real stdout/stderr, so a
// fake CommandRunner's captured output can be asserted without touching the
// real terminal.
func newManagerWithIO(runner CommandRunner, stdout, stderr io.Writer) *systemdUserManager {
	return &systemdUserManager{runner: runner, stdout: stdout, stderr: stderr}
}

// UserUnitPath returns ~/.config/systemd/user/swarm-daemon.service. Extracted
// verbatim from service_cli.go's systemdUserUnitPath.
func UserUnitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", UnitName), nil
}

// CurrentUser returns the login name for the linger hint, best-effort.
// Extracted verbatim from service_cli.go's currentUser.
func CurrentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "$USER"
}

// BuildUnitFile returns the systemd user unit text for the daemon+gateway.
// Extracted verbatim (template text byte-for-byte identical) from
// service_cli.go's buildUnitFile, generalized from a single binPath
// parameter to the full Spec so callers can vary the a2a/gateway parameters
// that were previously hardcoded literals in the template.
// swarm-tui/cmd/swarmos/service_cli.go's package-main buildUnitFile(binPath
// string) string wrapper (kept for global_daemon_test.go's existing direct
// call) always supplies the exact literals the original template hardcoded
// (A2AHandle "swarmos-daemon", A2AListen "127.0.0.1:0", GatewayAddr
// defaultDaemonGatewayAddr), so its output is byte-for-byte identical to the
// pre-extraction template — regression-proofed by
// TestBuildUnitFileMatchesOriginalTemplate in supervisor_test.go.
func BuildUnitFile(spec Spec) string {
	return `[Unit]
Description=Swarm background daemon (LAN gateway for phones / Swarm Desktop)
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=simple
ExecStart=` + spec.BinaryPath + ` daemon --a2a-handle ` + spec.A2AHandle + ` --a2a-listen ` + spec.A2AListen + ` --gateway --gateway-addr ` + spec.GatewayAddr + `
# Always restart: a daemon that exits — cleanly or not — should come back.
# Explicit stops go through systemctl (swarm swarm stop delegates to it when
# the unit is active), which does not trigger a restart.
Restart=always
RestartSec=3
# Give the graceful drain (presence "stopping" + HTTP shutdown) time to run.
TimeoutStopSec=15
# Honor SWARM_GATEWAY_TOKEN if present in the user environment.
Environment=SWARM_NO_AUTO_DAEMON=1

[Install]
WantedBy=default.target
`
}

// Detect classifies the current host's supervision Mode. Extracted from the
// two checks service_cli.go's serviceInstall performed inline
// (runtime.GOOS != "linux", then exec.LookPath("systemctl")) — Install below
// calls this same logic inline and preserves each check's exact original
// error text. Status/Uninstall intentionally do NOT route their own
// runtime.GOOS/PATH checks through Detect's control flow: the original
// serviceStatus and serviceUninstall functions never performed a
// `systemctl` PATH check (a missing systemctl there surfaced as whatever
// error `exec.Command(...).Run()` produced), and this extraction preserves
// that exactly rather than "improving" it into a new behavior.
func (m *systemdUserManager) Detect(ctx context.Context) (Mode, error) {
	if runtime.GOOS != "linux" {
		return ModeUnsupportedOS, nil
	}
	if _, err := m.runner.LookPath("systemctl"); err != nil {
		return ModeNoSystemd, nil
	}
	return ModeSystemdUser, nil
}

// Status runs `systemctl --user status swarm-daemon.service --no-pager`,
// streaming its output to the configured stdout/stderr exactly as
// service_cli.go's serviceStatus did (cmd.Stdout, cmd.Stderr = os.Stdout,
// os.Stderr; return cmd.Run()). The only non-Linux guard is the original
// `runtime.GOOS != "linux"` check; a missing `systemctl` binary is NOT
// pre-checked here (matching the original, which let exec.Command's own
// "executable file not found in $PATH" error surface unmodified).
func (m *systemdUserManager) Status(ctx context.Context) (Status, error) {
	if runtime.GOOS != "linux" {
		return Status{Mode: ModeUnsupportedOS}, fmt.Errorf("not applicable on %s", runtime.GOOS)
	}
	// Best-effort classification for the returned Status.Mode field; a
	// missing systemctl is not treated as fatal here (see doc comment
	// above) — it surfaces via the Run call below instead, exactly as the
	// original code allowed.
	mode, _ := m.Detect(ctx)
	runErr := m.runner.Run(ctx, m.stdout, m.stderr, "systemctl", "--user", "status", UnitName, "--no-pager")
	return Status{Mode: mode, Active: runErr == nil}, runErr
}

// Start runs `systemctl --user start swarm-daemon.service`. This method
// (along with Stop/Restart) was NOT present in the original service_cli.go —
// no `swarm service start/stop/restart` CLI subcommand exists today — but
// package-boundaries.md's Manager interface (CONTRACT.md section 3) fixes
// this exact 7-method shape, so it is implemented here for future callers
// (e.g. a later daemon composition root) against the same systemd-user-unit
// primitives Install/Uninstall/Status already use.
func (m *systemdUserManager) Start(ctx context.Context, spec Spec) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("not applicable on %s", runtime.GOOS)
	}
	return m.runner.Run(ctx, m.stdout, m.stderr, "systemctl", "--user", "start", UnitName)
}

// Stop runs `systemctl --user stop <unit>`. See Start's doc comment: no CLI
// subcommand calls this today, it exists to satisfy the fixed Manager
// interface shape for future callers.
func (m *systemdUserManager) Stop(ctx context.Context, id Identity) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("not applicable on %s", runtime.GOOS)
	}
	return m.runner.Run(ctx, m.stdout, m.stderr, "systemctl", "--user", "stop", unitOrDefault(id))
}

// Restart runs `systemctl --user restart <unit>`. See Start's doc comment:
// no CLI subcommand calls this today, it exists to satisfy the fixed
// Manager interface shape for future callers.
func (m *systemdUserManager) Restart(ctx context.Context, id Identity) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("not applicable on %s", runtime.GOOS)
	}
	return m.runner.Run(ctx, m.stdout, m.stderr, "systemctl", "--user", "restart", unitOrDefault(id))
}

func unitOrDefault(id Identity) string {
	if id.UnitName != "" {
		return id.UnitName
	}
	return UnitName
}

// Install writes the systemd user unit and enables+starts it. Extracted
// verbatim (byte-for-byte identical logic and user-visible messages) from
// service_cli.go's serviceInstall. package-boundaries.md's invariant
// "Ambiguous ownership returns an actionable error and performs no
// mutation" is preserved: both the OS check and the systemctl-PATH check
// return before any filesystem write.
func (m *systemdUserManager) Install(ctx context.Context, spec Spec) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("boot persistence via `swarm service install` is currently Linux/systemd only "+
			"(detected %s). On this platform, run `swarm swarm up` from your login items instead", runtime.GOOS)
	}
	if _, err := m.runner.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemctl not found — this system does not use systemd. " +
			"Add `swarm swarm up` to your session autostart instead")
	}

	unitPath, err := UserUnitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return fmt.Errorf("create unit dir: %w", err)
	}

	unit := BuildUnitFile(spec)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}
	fmt.Printf("wrote %s\n", unitPath)

	// Reload, enable (start at boot) and start now.
	for _, argv := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", "--now", UnitName},
	} {
		if err := m.runner.Run(ctx, m.stdout, m.stderr, "systemctl", argv...); err != nil {
			return fmt.Errorf("systemctl %s: %w", strings.Join(argv, " "), err)
		}
	}

	// Lingering lets the user service start at boot without an interactive
	// login. Best-effort: it needs privileges and may prompt; failure is not
	// fatal (the service still starts on login).
	if err := m.runner.Run(ctx, nil, nil, "loginctl", "enable-linger"); err != nil {
		fmt.Println()
		fmt.Println("note: could not enable user lingering automatically.")
		fmt.Println("      to start at boot WITHOUT logging in, run once:")
		fmt.Printf("        sudo loginctl enable-linger %s\n", CurrentUser())
	}

	fmt.Println()
	fmt.Println("swarm daemon service installed.")
	fmt.Println("  it starts on boot and serves the LAN gateway on :8787")
	fmt.Println("  status:    swarm service status")
	fmt.Println("  logs:      journalctl --user -u " + UnitName + " -f")
	fmt.Println("  remove:    swarm service uninstall")
	return nil
}

// Uninstall stops, disables and removes the unit. Extracted verbatim from
// service_cli.go's serviceUninstall. Per package-boundaries.md's invariant
// "Uninstall does not imply stop unless explicitly requested": this method
// disables the systemd unit (which is systemd's own mechanism for a unit it
// owns) but deliberately never signals the daemon process directly — no
// call here is the "stop the daemon process" primitive Stop/kill would be.
// TestUninstallNeverSignalsDaemonProcess in supervisor_test.go regression-
// proofs the exact call sequence below never grows a direct process-kill
// call.
func (m *systemdUserManager) Uninstall(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("not applicable on %s", runtime.GOOS)
	}
	unitPath, err := UserUnitPath()
	if err != nil {
		return err
	}
	// Best-effort disable+stop; ignore errors so a partially-installed unit
	// can still be cleaned up.
	_ = m.runner.Run(ctx, nil, nil, "systemctl", "--user", "disable", "--now", UnitName)
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove unit: %w", err)
	}
	_ = m.runner.Run(ctx, nil, nil, "systemctl", "--user", "daemon-reload")
	// ADR-002 canonical spelling: legacy `swarm swarm stop` -> `swarm daemon
	// stop` (Compatibility-matrix row 4). Uninstall's own logic is
	// unchanged — it still never stops the daemon; only this guidance text
	// now points at the canonical command.
	fmt.Printf("removed %s (daemon left running; stop with `swarm daemon stop`)\n", unitPath)
	return nil
}
