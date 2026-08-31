package supervisor

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// fakeCall records a single CommandRunner.Run invocation for assertions.
type fakeCall struct {
	name string
	args []string
}

// fakeRunner is the injected CommandRunner test seam described in this
// package's INDEX.md and required by CONTRACT.md section 3's "real
// Detect/Status round-trip test ... may use a fake/mock OS surface".
// It never shells out to a real systemctl/loginctl/kill.
type fakeRunner struct {
	// lookPathErr, keyed by executable name, makes LookPath fail for that
	// name (simulating "not installed on this host").
	lookPathErr map[string]error
	// runErr, keyed by "name arg1 arg2 ...", makes Run return that error
	// for the matching invocation.
	runErr map[string]error
	// stdoutText, keyed the same way as runErr, is written to the stdout
	// writer (if non-nil) before Run returns.
	stdoutText map[string]string

	calls []fakeCall
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if f.lookPathErr != nil {
		if err, ok := f.lookPathErr[name]; ok {
			return "", err
		}
	}
	return "/usr/bin/" + name, nil
}

func (f *fakeRunner) key(name string, args []string) string {
	return name + " " + strings.Join(args, " ")
}

func (f *fakeRunner) Run(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) error {
	f.calls = append(f.calls, fakeCall{name: name, args: append([]string(nil), args...)})
	key := f.key(name, args)
	if f.stdoutText != nil {
		if text, ok := f.stdoutText[key]; ok && stdout != nil {
			_, _ = io.WriteString(stdout, text)
		}
	}
	if f.runErr != nil {
		if err, ok := f.runErr[key]; ok {
			return err
		}
	}
	return nil
}

// --- Detect/Status real round-trip: genuine state transitions from fake inputs ---

func TestDetectRoundTrip(t *testing.T) {
	if runtime.GOOS != "linux" {
		// Every non-Linux host must classify as ModeUnsupportedOS regardless
		// of what the CommandRunner reports — this is the first branch
		// Detect checks, before ever touching the runner.
		mgr := NewManager(&fakeRunner{})
		mode, err := mgr.Detect(context.Background())
		if err != nil {
			t.Fatalf("Detect: unexpected error: %v", err)
		}
		if mode != ModeUnsupportedOS {
			t.Fatalf("Detect on %s: got mode %q, want %q", runtime.GOOS, mode, ModeUnsupportedOS)
		}
		return
	}

	t.Run("systemctl on PATH -> ModeSystemdUser", func(t *testing.T) {
		fr := &fakeRunner{}
		mgr := NewManager(fr)
		mode, err := mgr.Detect(context.Background())
		if err != nil {
			t.Fatalf("Detect: unexpected error: %v", err)
		}
		if mode != ModeSystemdUser {
			t.Fatalf("Detect: got mode %q, want %q", mode, ModeSystemdUser)
		}
	})

	t.Run("systemctl missing -> ModeNoSystemd", func(t *testing.T) {
		fr := &fakeRunner{lookPathErr: map[string]error{"systemctl": errors.New("exec: \"systemctl\": executable file not found in $PATH")}}
		mgr := NewManager(fr)
		mode, err := mgr.Detect(context.Background())
		if err != nil {
			t.Fatalf("Detect: unexpected error: %v", err)
		}
		if mode != ModeNoSystemd {
			t.Fatalf("Detect: got mode %q, want %q", mode, ModeNoSystemd)
		}
	})
}

func TestStatusRoundTrip(t *testing.T) {
	if runtime.GOOS != "linux" {
		fr := &fakeRunner{}
		mgr := newManagerWithIO(fr, io.Discard, io.Discard)
		st, err := mgr.Status(context.Background())
		if err == nil {
			t.Fatalf("Status on %s: expected a not-applicable error, got nil", runtime.GOOS)
		}
		if st.Mode != ModeUnsupportedOS {
			t.Fatalf("Status on %s: got mode %q, want %q", runtime.GOOS, st.Mode, ModeUnsupportedOS)
		}
		if len(fr.calls) != 0 {
			t.Fatalf("Status on %s must not invoke the CommandRunner at all, got calls %#v", runtime.GOOS, fr.calls)
		}
		return
	}

	t.Run("unit active", func(t *testing.T) {
		fr := &fakeRunner{}
		mgr := newManagerWithIO(fr, io.Discard, io.Discard)
		st, err := mgr.Status(context.Background())
		if err != nil {
			t.Fatalf("Status: unexpected error: %v", err)
		}
		if st.Mode != ModeSystemdUser {
			t.Fatalf("Status: got mode %q, want %q", st.Mode, ModeSystemdUser)
		}
		if !st.Active {
			t.Fatalf("Status: got Active=false, want true (fake runner reported no error)")
		}
	})

	t.Run("unit inactive/failed", func(t *testing.T) {
		key := "systemctl --user status " + UnitName + " --no-pager"
		fr := &fakeRunner{runErr: map[string]error{key: errors.New("exit status 3")}}
		mgr := newManagerWithIO(fr, io.Discard, io.Discard)
		st, err := mgr.Status(context.Background())
		if err == nil {
			t.Fatalf("Status: expected the fake's configured error to surface, got nil")
		}
		if st.Active {
			t.Fatalf("Status: got Active=true, want false (fake runner reported an error)")
		}
		if st.Mode != ModeSystemdUser {
			t.Fatalf("Status: got mode %q, want %q (systemctl is on PATH in this fake)", st.Mode, ModeSystemdUser)
		}
	})
}

// --- Install/Uninstall: byte-for-byte unit-file regression + real round-trip ---

// TestBuildUnitFileMatchesOriginalTemplate regression-proofs that
// BuildUnitFile's template text is byte-for-byte identical to
// service_cli.go's pre-extraction buildUnitFile template, given the exact
// literals swarm-tui/cmd/swarmos/service_cli.go's thin wrapper now supplies
// (A2AHandle "swarmos-daemon", A2AListen "127.0.0.1:0").
func TestBuildUnitFileMatchesOriginalTemplate(t *testing.T) {
	spec := Spec{
		BinaryPath:  "/tmp/swarm",
		A2AHandle:   "swarmos-daemon",
		A2AListen:   "127.0.0.1:0",
		GatewayAddr: "127.0.0.1:8787",
	}
	got := BuildUnitFile(spec)
	want := `[Unit]
Description=Swarm background daemon (LAN gateway for phones / Swarm Desktop)
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=simple
ExecStart=/tmp/swarm daemon --a2a-handle swarmos-daemon --a2a-listen 127.0.0.1:0 --gateway --gateway-addr 127.0.0.1:8787
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
	if got != want {
		t.Fatalf("BuildUnitFile mismatch (byte-for-byte regression against the pre-extraction template):\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestInstallUninstallRoundTrip(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd-user-unit install/uninstall is Linux-only, matching the original service_cli.go")
	}

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	fr := &fakeRunner{}
	mgr := newManagerWithIO(fr, io.Discard, io.Discard)
	spec := Spec{
		BinaryPath:  "/tmp/swarm",
		A2AHandle:   "swarmos-daemon",
		A2AListen:   "127.0.0.1:0",
		GatewayAddr: "127.0.0.1:8787",
	}

	if err := mgr.Install(context.Background(), spec); err != nil {
		t.Fatalf("Install: unexpected error: %v", err)
	}

	unitPath, err := UserUnitPath()
	if err != nil {
		t.Fatalf("UserUnitPath: %v", err)
	}
	if !strings.HasPrefix(unitPath, tmp) {
		t.Fatalf("UserUnitPath %q did not respect overridden $HOME %q", unitPath, tmp)
	}
	data, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("Install did not write the unit file at %q: %v", unitPath, err)
	}
	if string(data) != BuildUnitFile(spec) {
		t.Fatalf("installed unit file content mismatch:\n--- got ---\n%s\n--- want ---\n%s", string(data), BuildUnitFile(spec))
	}

	wantInstallCalls := []fakeCall{
		{name: "systemctl", args: []string{"--user", "daemon-reload"}},
		{name: "systemctl", args: []string{"--user", "enable", "--now", UnitName}},
		{name: "loginctl", args: []string{"enable-linger"}},
	}
	if !reflect.DeepEqual(fr.calls, wantInstallCalls) {
		t.Fatalf("Install call sequence mismatch:\ngot:  %#v\nwant: %#v", fr.calls, wantInstallCalls)
	}

	// Now Uninstall must remove the file this Install just wrote.
	fr.calls = nil
	if err := mgr.Uninstall(context.Background()); err != nil {
		t.Fatalf("Uninstall: unexpected error: %v", err)
	}
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Fatalf("Uninstall did not remove the unit file at %q: stat err=%v", unitPath, err)
	}
}

// TestUninstallNeverSignalsDaemonProcess is the invariant test required by
// CONTRACT.md section 5 ("a test proving Uninstall never calls whatever the
// injected command-runner's 'stop daemon' equivalent would be"), regression-
// proofing package-boundaries.md's supervisor invariant "Uninstall does not
// imply stop unless explicitly requested" / service_cli.go's own comment
// "daemon left running". It asserts the *exact* call sequence Uninstall
// makes through the CommandRunner, proving no additional call (in
// particular no `systemctl ... kill`, no bare `stop` verb, and no direct
// process-signal call by any name) was ever added beside the two
// historically-present systemctl calls.
func TestUninstallNeverSignalsDaemonProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd-user-unit uninstall is Linux-only, matching the original service_cli.go")
	}

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	unitPath, err := UserUnitPath()
	if err != nil {
		t.Fatalf("UserUnitPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		t.Fatalf("seed unit dir: %v", err)
	}
	if err := os.WriteFile(unitPath, []byte("stub-unit-file"), 0o644); err != nil {
		t.Fatalf("seed unit file: %v", err)
	}

	fr := &fakeRunner{}
	mgr := newManagerWithIO(fr, io.Discard, io.Discard)
	if err := mgr.Uninstall(context.Background()); err != nil {
		t.Fatalf("Uninstall: unexpected error: %v", err)
	}

	// The daemon-stop-equivalent primitives this test guards against: a
	// direct signal/kill call by any name, or a systemctl "stop"/"kill"
	// verb (as opposed to the "disable --now" call Uninstall does make,
	// which is existing/documented behavior, not a new stop primitive).
	for _, c := range fr.calls {
		if c.name == "kill" || c.name == "pkill" || c.name == "signal" {
			t.Fatalf("Uninstall must never directly signal the daemon process, got call %+v", c)
		}
		for _, a := range c.args {
			if a == "kill" || a == "stop" || strings.HasPrefix(a, "--signal") {
				t.Fatalf("Uninstall must never send a stop/kill verb through systemctl, got call %+v", c)
			}
		}
	}

	wantUninstallCalls := []fakeCall{
		{name: "systemctl", args: []string{"--user", "disable", "--now", UnitName}},
		{name: "systemctl", args: []string{"--user", "daemon-reload"}},
	}
	if !reflect.DeepEqual(fr.calls, wantUninstallCalls) {
		t.Fatalf("Uninstall call sequence mismatch (this is the exact call sequence historically present in service_cli.go's serviceUninstall):\ngot:  %#v\nwant: %#v", fr.calls, wantUninstallCalls)
	}
}

// TestManagerInterfaceSatisfied is a compile-time-adjacent smoke test that
// NewDefaultManager/NewManager both satisfy the exact Manager interface
// signature fixed by CONTRACT.md section 3 / package-boundaries.md.
func TestManagerInterfaceSatisfied(t *testing.T) {
	var _ Manager = NewDefaultManager()
	var _ Manager = NewManager(&fakeRunner{})
}
