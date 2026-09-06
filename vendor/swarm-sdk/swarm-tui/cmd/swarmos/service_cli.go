package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/supervisor"
)

// runServiceCLI implements `swarmos service <install|uninstall|status>`.
//
// It registers the global background daemon (with its LAN gateway) as a
// systemd USER service so it auto-starts whenever the computer is on — the
// "turn on when the PC is on" behavior — and survives logout when lingering is
// enabled. Linux/systemd only; other platforms print guidance.
func runServiceCLI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: swarmos service <install|uninstall|status>")
	}
	switch args[0] {
	case "install":
		return serviceInstall()
	case "uninstall":
		return serviceUninstall()
	case "status":
		return serviceStatus()
	default:
		return fmt.Errorf("unknown service subcommand %q — valid: install, uninstall, status", args[0])
	}
}

// serviceUnitName mirrors supervisor.UnitName. Kept as a package-level
// constant (rather than replacing every reference with the qualified name)
// because global_daemon.go — explicitly out of scope for this extraction,
// see .swarmflow/swarm-attach-architecture/p08-package-decomposition/CONTRACT.md
// — references this identifier directly today.
const serviceUnitName = supervisor.UnitName

// systemdUserUnitPath returns ~/.config/systemd/user/swarm-daemon.service.
// Thin forwarding wrapper kept because global_daemon.go (out of scope for
// this extraction) calls this package-main function directly.
func systemdUserUnitPath() (string, error) {
	return supervisor.UserUnitPath()
}

// serviceInstall is the `swarmos service install` CLI-argument translation:
// it resolves the running binary's own path (the one piece of information
// only the CLI process itself can supply) and delegates every OS-service
// action to internal/supervisor's Manager. The user-visible output (wrote
// path, systemctl reload/enable, lingering guidance, install summary) is
// produced by supervisor.Manager.Install itself, unchanged byte-for-byte
// from the original serviceInstall body — see internal/supervisor's
// INDEX.md and supervisor.go's Install doc comment.
func serviceInstall() error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate swarm binary: %w", err)
	}
	return supervisor.NewDefaultManager().Install(context.Background(), supervisor.Spec{
		BinaryPath:  self,
		A2AHandle:   "swarmos-daemon",
		A2AListen:   "127.0.0.1:0",
		GatewayAddr: defaultDaemonGatewayAddr,
	})
}

// buildUnitFile returns the systemd user unit text for the daemon+gateway.
// Kept as a package-main function (rather than removed) because
// global_daemon_test.go — out of scope for this extraction — calls it
// directly with this exact `func(string) string` signature. It now forwards
// to internal/supervisor.BuildUnitFile with the same literal a2a/gateway
// parameters the original template hardcoded, so its output remains
// byte-for-byte identical (see internal/supervisor/supervisor_test.go's
// TestBuildUnitFileMatchesOriginalTemplate for the regression proof of the
// underlying template text).
func buildUnitFile(binPath string) string {
	return supervisor.BuildUnitFile(supervisor.Spec{
		BinaryPath:  binPath,
		A2AHandle:   "swarmos-daemon",
		A2AListen:   "127.0.0.1:0",
		GatewayAddr: defaultDaemonGatewayAddr,
	})
}

// serviceUninstall is the `swarmos service uninstall` CLI-argument
// translation: it has no CLI-only inputs to gather, so it delegates
// directly to internal/supervisor's Manager, which preserves the original
// serviceUninstall body's exact behavior (best-effort disable+stop, remove
// unit file, best-effort daemon-reload, "daemon left running" message).
func serviceUninstall() error {
	return supervisor.NewDefaultManager().Uninstall(context.Background())
}

// serviceStatus is the `swarmos service status` CLI-argument translation:
// it delegates to internal/supervisor's Manager and discards the returned
// structured Status, since the user-visible output today is systemctl's own
// streamed stdout/stderr (which supervisor.Manager.Status still streams to
// this process's real stdio via NewDefaultManager) — the error is what
// determines this command's exit status, exactly as before.
func serviceStatus() error {
	_, err := supervisor.NewDefaultManager().Status(context.Background())
	return err
}
