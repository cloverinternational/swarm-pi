# `internal/supervisor`

## Responsibility

Reconcile the requested swarm daemon lifecycle with an OS service
supervisor. Today's `Manager` implementation targets exactly one supervision
surface — a Linux `systemd --user` unit (`swarm-daemon.service`) — because
that is the only OS-service supervision that existed in
`swarm-tui/cmd/swarmos/service_cli.go` before this extraction (P08.2, see
`.swarmflow/swarm-attach-architecture/p08-package-decomposition/CONTRACT.md`).
This package performs process/service actions; it does **not** decide
application readiness (that remains the daemon's/lifecycle package's job),
and it never signals a process from a bare PID alone (see "Invariants"
below — full PID/identity-corroborated signaling is
`swarm-tui/cmd/swarmos/daemon_lock.go`'s job, explicitly out of scope here).

## Owned state

- Detected supervision `Mode` (`ModeSystemdUser`, `ModeNoSystemd`,
  `ModeUnsupportedOS`).
- Desired service specification (`Spec`: binary path, `--a2a-handle`,
  `--a2a-listen`, `--gateway-addr` values baked into the generated unit).
- Operation result (`Status`: the `Mode` a `Status`/`Detect` call classified,
  plus whether the unit was reported active).
- A verified process/service identity selector (`Identity`: an optional
  unit-name override for `Stop`/`Restart`; it does **not** cache daemon
  status as truth).

This package owns the generated `~/.config/systemd/user/swarm-daemon.service`
unit file and its content. It does not persist presence files, PID-only
handles, daemon journal data, HTTP health payloads, or RPC state.

## Public entry points

```go
type Manager interface {
    Detect(context.Context) (Mode, error)
    Status(context.Context) (Status, error)
    Start(context.Context, Spec) error
    Stop(context.Context, Identity) error
    Restart(context.Context, Identity) error
    Install(context.Context, Spec) error
    Uninstall(context.Context) error
}
```

- `NewDefaultManager() Manager` — the production constructor: a real
  `os/exec`-backed `CommandRunner` streaming to the process's real
  stdout/stderr. This is what `swarm-tui/cmd/swarmos/service_cli.go`'s
  `serviceInstall`/`serviceUninstall`/`serviceStatus` call.
- `NewManager(CommandRunner) Manager` — constructs a `Manager` over an
  injected `CommandRunner`, for tests or a future alternate OS-command
  backend.
- `BuildUnitFile(Spec) string` — the systemd unit template, exported so
  `service_cli.go`'s `buildUnitFile(binPath string) string` (kept for
  `global_daemon_test.go`'s existing direct call — out of scope for this
  extraction) can forward to it with byte-for-byte identical output.
- `UserUnitPath() (string, error)` — `~/.config/systemd/user/swarm-daemon.service`.
- `CurrentUser() string` — best-effort login-name lookup for the lingering
  guidance message (moved here from `service_cli.go`'s `currentUser`, since
  it is only ever used inside `Install`'s systemd-lingering guidance text).
- `const UnitName = "swarm-daemon.service"`.

**Normal call sequence** (CLI-argument translation lives in
`service_cli.go`, orchestration lives here):
`serviceInstall()` resolves `os.Executable()` for `Spec.BinaryPath`, then
calls `Manager.Install(ctx, spec)`, which internally: checks
`runtime.GOOS`/`systemctl` availability -> writes the unit file -> runs
`systemctl --user daemon-reload` then `--user enable --now` -> best-effort
`loginctl enable-linger` -> prints the install summary.
`serviceUninstall()`/`serviceStatus()` call `Manager.Uninstall`/`Status`
directly with no CLI-only inputs to gather.

## Allowed dependencies

Standard library only (`context`, `fmt`, `io`, `os`, `os/exec`,
`path/filepath`, `runtime`, `strings`). No neutral lifecycle-intent or
process-identity value packages are imported yet because this phase's
extraction did not need them — `service_cli.go`'s original logic never
consumed lifecycle intent types. A future phase wiring `Start`/`Stop`/
`Restart` into a real caller may need to add one; that is a Manager-signature-
compatible addition, not a redesign.

## Forbidden dependencies

`daemon`, `attachclient`, `presence` implementation, `presentationcontrol`,
`lan`, engine, gateway, and CLI/TUI packages (package-boundaries.md,
verbatim, "supervisor" section). This package imports **zero**
`swarm-tui/cmd/swarmos` symbols — the dependency direction is
`cmd/swarmos` -> `internal/supervisor`, never the reverse.

## Persistence/protocol ownership

Owns the generated systemd user-service unit file
(`~/.config/systemd/user/swarm-daemon.service`) and its content/template.
Does not own presence files, PID-only handles, daemon journal data, HTTP
health payloads, or RPC. `daemon_lock.go`'s PID-corroborated stop-by-signal
logic is a distinct, out-of-scope concern (see "Invariants").

## Invariants

1. **Never signal from PID alone.** This package never sends a process
   signal by PID; all lifecycle actions this phase implements go through
   `systemctl --user <verb> <unit>`, letting systemd resolve the managed
   process. (Full PID/instance-token/user-ownership corroborated manual
   signaling is `daemon_lock.go`'s job, out of scope for this extraction.)
2. **If systemd owns the unit, lifecycle actions go through systemd.**
   `Start`/`Stop`/`Restart` are `systemctl --user start|stop|restart <unit>`
   calls, never a direct process operation.
3. **Ambiguous ownership returns an actionable error and performs no
   mutation.** `Install` checks `runtime.GOOS` then `systemctl` presence
   before any filesystem write; both checks return before `os.MkdirAll`/
   `os.WriteFile` run.
4. **Uninstall does not imply stop unless explicitly requested.**
   `Uninstall` disables the systemd unit (`systemctl --user disable --now`,
   existing/documented behavior) but never issues a separate direct
   process-kill/signal call — regression-proofed by
   `supervisor_test.go`'s `TestUninstallNeverSignalsDaemonProcess`, which
   asserts the exact `CommandRunner` call sequence `Uninstall` makes.
5. **`Status`/`Detect` do not cache daemon status as truth.** Each call
   re-derives `Mode`/`Active` from a fresh `CommandRunner` invocation; no
   field is memoized across calls.

## Concurrency model

`Manager` implementations are stateless beyond their injected
`CommandRunner`/stdout/stderr writers and hold no goroutines, background
timers, or mutable shared maps. Every method call is a synchronous,
independently-safe sequence of `CommandRunner.Run`/`LookPath` calls plus
plain filesystem operations (`os.MkdirAll`, `os.WriteFile`, `os.Remove`,
`os.ReadFile`) — there is no queue, no background worker, and no shutdown
ordering to document.

## Error categories and security/privacy boundary

- **Unsupported platform** (`runtime.GOOS != "linux"`) — every method
  returns a `not applicable on %s` (or, for `Install`, a longer guidance)
  error and performs no mutation.
- **Missing `systemctl`** — `Install` classifies this explicitly
  (`systemctl not found — this system does not use systemd...`) before any
  write; `Status`/`Uninstall` intentionally let the underlying
  `exec.Command` error surface unmodified, matching the original
  `service_cli.go` code exactly (see `Detect`'s doc comment in
  `supervisor.go`).
- **Filesystem errors** (`create unit dir`, `write unit`, `remove unit`) are
  wrapped with `%w` and a short label.
- No secrets, credentials, or engine/task content pass through this
  package — its only privacy-relevant surface is the swarm binary's own
  absolute path, written into a user-owned config file under the invoking
  user's `$HOME`.

## Focused test files and verification commands

- `internal/supervisor/supervisor_test.go`:
  - `TestDetectRoundTrip` — real state-transition assertions for
    `ModeSystemdUser`/`ModeNoSystemd`/`ModeUnsupportedOS` against a fake
    `CommandRunner` (no real `systemctl` invoked).
  - `TestStatusRoundTrip` — active/inactive/unsupported-OS round trips
    against a fake `CommandRunner`.
  - `TestBuildUnitFileMatchesOriginalTemplate` — byte-for-byte regression
    of the generated unit file text against the pre-extraction
    `service_cli.go` template.
  - `TestInstallUninstallRoundTrip` — a real `Install` then `Uninstall`
    against a temp `$HOME`, asserting the unit file is written with exactly
    `BuildUnitFile`'s content and then removed.
  - `TestUninstallNeverSignalsDaemonProcess` — asserts `Uninstall`'s exact
    `CommandRunner` call sequence, proving no daemon-stop/kill primitive is
    ever invoked (Invariant 4).
  - `TestManagerInterfaceSatisfied` — compile-time-adjacent check that both
    constructors satisfy the exact fixed `Manager` shape.
- Verification: `go test -race ./internal/supervisor/...`,
  `go vet ./internal/supervisor/...`, `gofmt -l internal/supervisor/`.
- `swarm-tui/cmd/swarmos/global_daemon_test.go`'s
  `TestDaemonGatewayDefaultsAreExplicitLoopback` (out of scope for this
  extraction, unmodified) exercises `service_cli.go`'s `buildUnitFile`
  wrapper end-to-end and must keep passing:
  `go test -race ./swarm-tui/cmd/swarmos/... -run TestDaemonGatewayDefaultsAreExplicitLoopback`.

## Compatibility adapters

None. This is a new package; there is no legacy caller to bridge except
`swarm-tui/cmd/swarmos/service_cli.go`'s thin wrapper functions
(`serviceInstall`, `serviceUninstall`, `serviceStatus`, `buildUnitFile`,
`systemdUserUnitPath`, `serviceUnitName`), which are permanent CLI-argument-
translation call sites, not a removal-gated adapter.

## Deferred

- `internal/daemon` composition-root extraction is deferred (structurally
  last in package-boundaries.md's extraction order); cross-platform
  (non-systemd) supervisor implementations are deferred to a later phase,
  only the current systemd-user-unit behavior was extracted.
- Full process-identity-corroborated manual signaling (peer type, daemon
  instance token, process start identity, user ownership) remains owned by
  `swarm-tui/cmd/swarmos/daemon_lock.go`, explicitly out of scope for this
  extraction; `Identity` in this package intentionally stays a thin
  unit-name selector rather than a reimplementation of that logic.
- A `Mode` value for a hand-managed "manual" (non-systemd) supervised
  process — package-boundaries.md's focused-test list calls out
  "absent/manual/systemd" reconciliation tables, but no code this phase
  extracted from `service_cli.go` ever detected a manual-process mode; only
  `ModeSystemdUser`/`ModeNoSystemd`/`ModeUnsupportedOS` are implemented.
- `Start`/`Stop`/`Restart` have no CLI subcommand caller today (there is no
  `swarm service start|stop|restart`); they exist only to satisfy
  CONTRACT.md section 3's fixed `Manager` interface shape for a future
  daemon composition root.
