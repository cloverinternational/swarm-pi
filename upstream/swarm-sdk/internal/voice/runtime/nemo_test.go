package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/voice/runtimeassets"
)

type fakeRunner struct {
	mu       sync.Mutex
	commands []Command
	run      func(context.Context, Command) (CommandResult, error)
}

func (f *fakeRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	f.mu.Lock()
	f.commands = append(f.commands, command)
	f.mu.Unlock()
	if f.run != nil {
		return f.run(ctx, command)
	}
	return CommandResult{}, errors.New("not found")
}

func (f *fakeRunner) snapshot() []Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Command(nil), f.commands...)
}

type fixedPort bool

func (f fixedPort) InUse(context.Context, string, int) (bool, error) { return bool(f), nil }

type recordingPort struct {
	port  int
	inUse bool
}

func (p *recordingPort) InUse(_ context.Context, _ string, port int) (bool, error) {
	p.port = port
	return p.inUse, nil
}

type clientFunc func(*http.Request) (*http.Response, error)

func (f clientFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }

func healthyServer(t *testing.T) *httptest.Server {
	t.Helper()
	return healthyServerForModel(t, defaultNemoModel)
}

func healthyServerForModel(t *testing.T, model string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/models":
			_, _ = w.Write([]byte(`{"models":[{"id":"` + model + `","ready":true}]}`))
		case transcriptionRoute:
			if r.Method != http.MethodOptions {
				http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Allow", "OPTIONS, POST")
			w.Header().Set("X-Swarm-Transcription-Contract", transcriptionContract)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
}

func newTestManager(t *testing.T, runner Runner, endpoint string, port PortChecker) *NemoManager {
	t.Helper()
	root := t.TempDir()
	manager, err := NewNemoManager(NemoConfig{
		Endpoint:    endpoint,
		Paths:       Paths{Root: filepath.Join(root, "runtime"), Cache: filepath.Join(root, "cache"), ProcVersion: filepath.Join(root, "proc-version")},
		Runner:      runner,
		PortChecker: port,
		GOOS:        "linux", GOARCH: "amd64",
		Now: func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func capableRunner(containerJSON string) *fakeRunner {
	return &fakeRunner{run: func(_ context.Context, command Command) (CommandResult, error) {
		joined := command.Name + " " + strings.Join(command.Args, " ")
		switch {
		case joined == "docker version --format {{.Server.Version}}":
			return CommandResult{Stdout: "27.0"}, nil
		case joined == "docker compose version --short":
			return CommandResult{Stdout: "2.29"}, nil
		case joined == "nvidia-smi -L":
			return CommandResult{Stdout: "GPU 0"}, nil
		case joined == "docker info --format {{json .Runtimes}}":
			return CommandResult{Stdout: `{"nvidia":{}}`}, nil
		case joined == "docker inspect nemo-asr" && containerJSON != "":
			return CommandResult{Stdout: containerJSON}, nil
		case joined == "docker inspect nemo-asr":
			return CommandResult{}, errors.New("no such container")
		case strings.HasPrefix(joined, "docker compose "):
			return CommandResult{}, nil
		case joined == "docker start nemo-asr":
			return CommandResult{Stdout: "nemo-asr"}, nil
		default:
			return CommandResult{}, errors.New("unexpected command: " + joined)
		}
	}}
}

func readyAfterComposeRunner(containerJSON string, ready *atomic.Bool) *fakeRunner {
	base := capableRunner(containerJSON)
	return &fakeRunner{run: func(ctx context.Context, command Command) (CommandResult, error) {
		result, err := base.run(ctx, command)
		if err == nil && strings.Contains(strings.Join(command.Args, " "), "compose -f") && strings.Contains(strings.Join(command.Args, " "), " up ") {
			ready.Store(true)
		}
		return result, err
	}}
}

func readyAfterComposeServer(t *testing.T, ready *atomic.Bool, beforeModel, afterModel string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			status := "loading"
			if ready.Load() {
				status = "ok"
			}
			_, _ = w.Write([]byte(`{"status":"` + status + `"}`))
		case "/models":
			model := beforeModel
			if ready.Load() {
				model = afterModel
			}
			_, _ = w.Write([]byte(`{"models":[{"id":"` + model + `","ready":true}]}`))
		case transcriptionRoute:
			w.Header().Set("Allow", "OPTIONS, POST")
			w.Header().Set("X-Swarm-Transcription-Contract", transcriptionContract)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestUnsupportedHostIsConnectOnlyButUsesCompatibleEndpoint(t *testing.T) {
	server := healthyServer(t)
	defer server.Close()
	manager := newTestManager(t, &fakeRunner{}, server.URL, fixedPort(false))
	manager.goos = "darwin"
	inspection, err := manager.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Capabilities.Install || inspection.Capabilities.Start {
		t.Fatalf("unsupported host reported management: %+v", inspection.Capabilities)
	}
	if inspection.State.Mode != ModeExternal || inspection.State.Status != StatusRunning {
		t.Fatalf("compatible endpoint not detected: %+v", inspection.State)
	}
}

func TestInspectDetectsWSLAndManagedContainer(t *testing.T) {
	server := healthyServer(t)
	defer server.Close()
	container := `[{"Name":"/nemo-asr","Config":{"Image":"swarm-nemo-asr:nemo-asr-v2","Labels":{"com.swarm.voice.runtime":"nemo-asr-v2","com.docker.compose.service":"nemo-asr"}},"State":{"Running":true},"NetworkSettings":{"Ports":{"8001/tcp":[{"HostPort":"8001"}]}}}]`
	manager := newTestManager(t, capableRunner(container), server.URL, fixedPort(true))
	if err := os.WriteFile(manager.paths.ProcVersion, []byte("Linux microsoft-standard-WSL2"), 0o600); err != nil {
		t.Fatal(err)
	}
	inspection, err := manager.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Capabilities.WSL2 || !inspection.Container.Managed || inspection.State.Mode != ModeManaged {
		t.Fatalf("unexpected inspection: %+v", inspection)
	}
	if !inspection.Capabilities.Stop || !inspection.Capabilities.Update {
		t.Fatalf("managed lifecycle disabled: %+v", inspection.Capabilities)
	}
}

func TestInspectAdoptsRunningCompatibleContainerWithoutOwnership(t *testing.T) {
	server := healthyServer(t)
	defer server.Close()
	container := `[{"Name":"/nemo-asr","Config":{"Image":"custom/nemo:1","Labels":{}},"State":{"Running":true},"NetworkSettings":{"Ports":{"8001/tcp":[{"HostPort":"8001"}]}}}]`
	manager := newTestManager(t, capableRunner(container), server.URL, fixedPort(true))
	inspection, err := manager.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State.Mode != ModeAdopted || inspection.State.Status != StatusRunning {
		t.Fatalf("running compatible container was not adopted: %+v", inspection.State)
	}
	if inspection.Capabilities.Stop || inspection.Capabilities.Update {
		t.Fatalf("adopted container received ownership capabilities: %+v", inspection.Capabilities)
	}
}

func TestStartRefusesStoppedUnownedContainer(t *testing.T) {
	container := `[{"Name":"/nemo-asr","Config":{"Image":"custom/nemo:1","Labels":{}},"State":{"Running":false},"NetworkSettings":{"Ports":{"8001/tcp":[{"HostPort":"8001"}]}}}]`
	runner := capableRunner(container)
	manager := newTestManager(t, runner, "http://127.0.0.1:1", fixedPort(false))
	inspection, inspectErr := manager.Inspect(context.Background())
	if inspectErr != nil {
		t.Fatal(inspectErr)
	}
	if inspection.Capabilities.Start {
		t.Fatalf("stopped unowned container advertised Start: %+v", inspection.Capabilities)
	}
	if !strings.Contains(inspection.State.Message, "manual external startup is required") {
		t.Fatalf("stopped unowned message contradicts capabilities: %+v", inspection)
	}
	_, err := manager.Start(context.Background(), nil)
	if !errors.Is(err, ErrUnsafeContainer) {
		t.Fatalf("expected safe ownership refusal, got %v", err)
	}
	for _, command := range runner.snapshot() {
		joined := command.Name + " " + strings.Join(command.Args, " ")
		if joined == "docker start nemo-asr" || strings.Contains(joined, " compose -f ") {
			t.Fatalf("unowned container was mutated: %+v", runner.snapshot())
		}
	}
}

func TestInstallWritesPinnedBundleAndUsesEnvironmentForCache(t *testing.T) {
	runner := capableRunner("")
	manager := newTestManager(t, runner, "http://127.0.0.1:1", fixedPort(false))
	var progress []Progress
	state, err := manager.Install(context.Background(), func(p Progress) { progress = append(progress, p) })
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusStopped || len(progress) == 0 || progress[len(progress)-1].Percent != 100 {
		t.Fatalf("bad install result: %+v %+v", state, progress)
	}
	compose, err := os.ReadFile(filepath.Join(manager.bundleDir(), "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(compose), ":latest") || !strings.Contains(string(compose), runtimeassets.Image) {
		t.Fatalf("bundle image is not pinned: %s", compose)
	}
	if strings.Contains(strings.ToLower(string(compose)), "token") {
		t.Fatal("compose unexpectedly contains credential material")
	}
	commands := runner.snapshot()
	last := commands[len(commands)-1]
	if last.Env["NEMO_CACHE_DIR"] != manager.paths.Cache ||
		last.Env["NEMO_MODEL"] != defaultNemoModel || last.Env["NEMO_PORT"] != "8001" {
		t.Fatalf("runtime inputs not passed via environment: %+v", last)
	}
	for _, arg := range last.Args {
		if strings.Contains(arg, manager.paths.Cache) {
			t.Fatalf("cache path leaked into argv: %+v", last.Args)
		}
	}
}

func TestConfiguredPortAndModelDriveInspectionAndCompose(t *testing.T) {
	var ready atomic.Bool
	server := readyAfterComposeServer(t, &ready, defaultNemoModel, "nvidia/custom-asr")
	defer server.Close()
	container := `[{"Name":"/nemo-asr","Config":{"Image":"swarm-nemo-asr:nemo-asr-v2","Labels":{"com.swarm.voice.runtime":"nemo-asr-v2","com.docker.compose.service":"nemo-asr"}},"State":{"Running":false},"NetworkSettings":{"Ports":{"8001/tcp":[{"HostPort":"8001"}]}}}]`
	runner := readyAfterComposeRunner(container, &ready)
	ports := &recordingPort{}
	root := t.TempDir()
	manager, err := NewNemoManager(NemoConfig{
		Endpoint:    server.URL,
		Port:        9100,
		Model:       "nvidia/custom-asr",
		Paths:       Paths{Root: filepath.Join(root, "runtime"), Cache: filepath.Join(root, "cache"), ProcVersion: filepath.Join(root, "proc-version")},
		Runner:      runner,
		PortChecker: ports,
		GOOS:        "linux",
		GOARCH:      "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := manager.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ports.port != 9100 || !inspection.Container.Managed || inspection.Container.Port != 8001 {
		t.Fatalf("configured port not used while stale managed container remains owned: port=%d container=%+v", ports.port, inspection.Container)
	}
	if _, err := manager.Start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	commands := runner.snapshot()
	last := commands[len(commands)-1]
	if last.Env["NEMO_PORT"] != "9100" || last.Env["NEMO_MODEL"] != "nvidia/custom-asr" {
		t.Fatalf("configured runtime inputs not passed to Compose: %+v", last.Env)
	}
}

func TestInstallRejectsUnknownPortConflict(t *testing.T) {
	manager := newTestManager(t, capableRunner(""), "http://127.0.0.1:1", fixedPort(true))
	_, err := manager.Install(context.Background(), nil)
	if !errors.Is(err, ErrPortConflict) {
		t.Fatalf("expected port conflict, got %v", err)
	}
}

func TestStopRefusesUnownedContainer(t *testing.T) {
	container := `[{"Name":"/nemo-asr","Config":{"Image":"custom/nemo:1","Labels":{}},"State":{"Running":true},"NetworkSettings":{"Ports":{"8001/tcp":[{"HostPort":"8001"}]}}}]`
	manager := newTestManager(t, capableRunner(container), "http://127.0.0.1:1", fixedPort(true))
	_, err := manager.Stop(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("expected ownership refusal, got %v", err)
	}
}

func TestManagedLifecycleUsesComposeWithoutCredentials(t *testing.T) {
	container := `[{"Name":"/nemo-asr","Config":{"Image":"swarm-nemo-asr:nemo-asr-v2","Labels":{"com.swarm.voice.runtime":"nemo-asr-v2","com.docker.compose.service":"nemo-asr"}},"State":{"Running":false},"NetworkSettings":{"Ports":{"8001/tcp":[{"HostPort":"8001"}]}}}]`
	tests := []struct {
		name string
		run  func(*NemoManager) error
		want string
	}{
		{name: "start", run: func(manager *NemoManager) error { _, err := manager.Start(context.Background(), nil); return err }, want: "up -d --force-recreate --remove-orphans"},
		{name: "stop", run: func(manager *NemoManager) error { _, err := manager.Stop(context.Background(), nil); return err }, want: "stop"},
		{name: "update", run: func(manager *NemoManager) error { _, err := manager.Update(context.Background(), nil); return err }, want: "build --pull"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var ready atomic.Bool
			server := readyAfterComposeServer(t, &ready, defaultNemoModel, defaultNemoModel)
			defer server.Close()
			runner := readyAfterComposeRunner(container, &ready)
			manager := newTestManager(t, runner, server.URL, fixedPort(false))
			if err := test.run(manager); err != nil {
				t.Fatal(err)
			}
			var found bool
			for _, command := range runner.snapshot() {
				joined := strings.Join(command.Args, " ")
				found = found || strings.Contains(joined, test.want)
				if len(command.Env) > 0 {
					if command.Env["NEMO_CACHE_DIR"] != manager.paths.Cache ||
						command.Env["NEMO_MODEL"] != defaultNemoModel || command.Env["NEMO_PORT"] != "8001" {
						t.Fatalf("unexpected environment values: %+v", command.Env)
					}
				}
			}
			if !found {
				t.Fatalf("missing compose operation %q: %+v", test.want, runner.snapshot())
			}
		})
	}
}

func TestStartReconcilesRunningManagedContainerWithStaleModel(t *testing.T) {
	var ready atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/models":
			model := defaultNemoModel
			if ready.Load() {
				model = "nvidia/new-model"
			}
			_, _ = w.Write([]byte(`{"models":[{"id":"` + model + `","ready":true}]}`))
		case transcriptionRoute:
			w.Header().Set("Allow", "OPTIONS, POST")
			w.Header().Set("X-Swarm-Transcription-Contract", transcriptionContract)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	container := `[{"Name":"/nemo-asr","Config":{"Image":"swarm-nemo-asr:nemo-asr-v2","Labels":{"com.swarm.voice.runtime":"nemo-asr-v2","com.docker.compose.service":"nemo-asr"}},"State":{"Running":true},"NetworkSettings":{"Ports":{"8001/tcp":[{"HostPort":"8001"}]}}}]`
	runner := readyAfterComposeRunner(container, &ready)
	manager := newTestManager(t, runner, server.URL, fixedPort(true))
	manager.model = "nvidia/new-model"
	manager.readyBackoff = time.Millisecond

	inspection, err := manager.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State.Status != StatusUnhealthy || !strings.Contains(inspection.State.Message, "configured model") {
		t.Fatalf("stale managed model reported ready: %+v", inspection.State)
	}
	state, err := manager.Start(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusRunning {
		t.Fatalf("Start() state = %+v, want running", state)
	}
	commands := runner.snapshot()
	last := commands[len(commands)-1]
	if !strings.Contains(strings.Join(last.Args, " "), "up -d --force-recreate --remove-orphans") || last.Env["NEMO_MODEL"] != "nvidia/new-model" {
		t.Fatalf("stale model was not reconciled through Compose: %+v", last)
	}
}

func TestStartWaitsForDelayedEndpointAndConfiguredModelReadiness(t *testing.T) {
	var healthChecks int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			healthChecks++
			status := "loading"
			if healthChecks >= 3 {
				status = "ok"
			}
			_, _ = w.Write([]byte(`{"status":"` + status + `"}`))
		case "/models":
			_, _ = w.Write([]byte(`{"models":[{"id":"` + defaultNemoModel + `","ready":true}]}`))
		case transcriptionRoute:
			w.Header().Set("Allow", "OPTIONS, POST")
			w.Header().Set("X-Swarm-Transcription-Contract", transcriptionContract)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	container := `[{"Name":"/nemo-asr","Config":{"Image":"swarm-nemo-asr:nemo-asr-v2","Labels":{"com.swarm.voice.runtime":"nemo-asr-v2","com.docker.compose.service":"nemo-asr"}},"State":{"Running":false},"NetworkSettings":{"Ports":{"8001/tcp":[{"HostPort":"8001"}]}}}]`
	manager := newTestManager(t, capableRunner(container), server.URL, fixedPort(false))
	manager.readyTimeout = time.Second
	manager.readyBackoff = time.Millisecond

	state, err := manager.Start(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusRunning || healthChecks < 3 {
		t.Fatalf("Start() returned before delayed readiness: state=%+v health_checks=%d", state, healthChecks)
	}
}

func TestStartReadinessWaitHonorsContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"status":"loading"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	container := `[{"Name":"/nemo-asr","Config":{"Image":"swarm-nemo-asr:nemo-asr-v2","Labels":{"com.swarm.voice.runtime":"nemo-asr-v2","com.docker.compose.service":"nemo-asr"}},"State":{"Running":false},"NetworkSettings":{"Ports":{"8001/tcp":[{"HostPort":"8001"}]}}}]`
	manager := newTestManager(t, capableRunner(container), server.URL, fixedPort(false))
	manager.readyTimeout = time.Minute
	manager.readyBackoff = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := manager.Start(ctx, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start() error = %v, want context deadline exceeded", err)
	}
}

func TestHealthModelsAndEndpointRedaction(t *testing.T) {
	server := healthyServer(t)
	defer server.Close()
	manager := newTestManager(t, capableRunner(""), server.URL, fixedPort(false))
	health, err := manager.Health(context.Background())
	if err != nil || !health.Healthy {
		t.Fatalf("health: %+v %v", health, err)
	}
	models, err := manager.Models(context.Background())
	if err != nil || len(models) != 1 || !models[0].Ready {
		t.Fatalf("models: %+v %v", models, err)
	}

	secretManager, err := NewNemoManager(NemoConfig{Endpoint: "http://user:secret@example.test:8001?token=abc", Paths: manager.paths, Runner: capableRunner(""), HTTPClient: clientFunc(func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed for " + request.URL.String())
	}), PortChecker: fixedPort(false), GOOS: "darwin", GOARCH: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := secretManager.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	serialized := inspection.State.Endpoint
	if strings.Contains(serialized, "secret") || strings.Contains(serialized, "abc") || !strings.Contains(serialized, "REDACTED") {
		t.Fatalf("endpoint not redacted: %q", serialized)
	}
	_, healthErr := secretManager.Health(context.Background())
	if healthErr == nil || strings.Contains(healthErr.Error(), "secret") || strings.Contains(healthErr.Error(), "abc") {
		t.Fatalf("health error leaked endpoint credentials: %v", healthErr)
	}
}

func TestRuntimeSuccessResponsesAreBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","padding":"` + strings.Repeat("x", runtimeResponseLimit) + `"}`))
	}))
	defer server.Close()
	manager := newTestManager(t, capableRunner(""), server.URL, fixedPort(false))

	if _, err := manager.Health(context.Background()); err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("Health() error = %v, want bounded response error", err)
	}
	if _, err := manager.Models(context.Background()); err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("Models() error = %v, want bounded response error", err)
	}
}

func TestHealthOnlyEndpointIsNotCompatible(t *testing.T) {
	probes := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case transcriptionRoute:
			probes <- r.Method
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	manager := newTestManager(t, &fakeRunner{}, server.URL, fixedPort(false))
	manager.goos = "darwin"
	inspection, err := manager.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case method := <-probes:
		if method != http.MethodOptions {
			t.Fatalf("capability probe used %s, want OPTIONS", method)
		}
	default:
		t.Fatal("transcription capability route was not probed")
	}
	if inspection.State.Status == StatusRunning || inspection.State.Mode == ModeExternal {
		t.Fatalf("health-only endpoint reported compatible: %+v", inspection.State)
	}
}

func TestHealthOnlyContainerIsNotAdoptedAsReady(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	container := `[{"Name":"/nemo-asr","Config":{"Image":"custom/nemo:1","Labels":{}},"State":{"Running":true},"NetworkSettings":{"Ports":{"8001/tcp":[{"HostPort":"8001"}]}}}]`
	manager := newTestManager(t, capableRunner(container), server.URL, fixedPort(true))
	inspection, err := manager.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State.Mode != ModeAdopted || inspection.State.Status != StatusUnhealthy {
		t.Fatalf("route-incompatible container reported ready: %+v", inspection.State)
	}
	if inspection.Capabilities.Stop || inspection.Capabilities.Update {
		t.Fatalf("route-incompatible unowned container became mutable: %+v", inspection.Capabilities)
	}
}

func TestInspectDetectsOnlyWSL2(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{name: "wsl2", version: "Linux version 5.15.153.1-microsoft-standard-WSL2", want: true},
		{name: "wsl1", version: "Linux version 4.4.0-19041-Microsoft", want: false},
		{name: "ordinary linux", version: "Linux version 6.8.0-generic", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := newTestManager(t, &fakeRunner{}, "http://127.0.0.1:1", fixedPort(false))
			manager.goos = "darwin"
			if err := os.WriteFile(manager.paths.ProcVersion, []byte(test.version), 0o600); err != nil {
				t.Fatal(err)
			}
			inspection, err := manager.Inspect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if inspection.Capabilities.WSL2 != test.want {
				t.Fatalf("WSL2 = %v, want %v for %q", inspection.Capabilities.WSL2, test.want, test.version)
			}
		})
	}
}

func TestTranscriptionCapabilityAcceptsStandardAllowHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == transcriptionRoute && r.Method == http.MethodOptions {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	manager := newTestManager(t, &fakeRunner{}, server.URL, fixedPort(false))
	if !manager.transcriptionCapable(context.Background()) {
		t.Fatal("standard 405 Allow: POST route was not recognized")
	}
}

func TestInspectReportsStoppedManagedContainerDespiteCompatibleEndpoint(t *testing.T) {
	server := healthyServer(t)
	defer server.Close()
	// Managed container is owned and bound to the configured port, but stopped.
	// A compatible endpoint answers on that port; it must NOT be attributed to
	// the stopped managed container.
	container := `[{"Name":"/nemo-asr","Config":{"Image":"swarm-nemo-asr:nemo-asr-v2","Labels":{"com.swarm.voice.runtime":"nemo-asr-v2","com.docker.compose.service":"nemo-asr"}},"State":{"Running":false},"NetworkSettings":{"Ports":{"8001/tcp":[{"HostPort":"8001"}]}}}]`
	runner := capableRunner(container)
	manager := newTestManager(t, runner, server.URL, fixedPort(false))

	inspection, err := manager.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State.Mode != ModeManaged {
		t.Fatalf("owned container not reported as managed: %+v", inspection.State)
	}
	if inspection.State.Status != StatusStopped {
		t.Fatalf("stopped managed container misreported via compatible endpoint: %+v", inspection.State)
	}
	if !strings.Contains(inspection.State.Message, "stopped") {
		t.Fatalf("stopped managed message unexpected: %+v", inspection.State)
	}

	// Start must reconcile through Compose --force-recreate rather than
	// early-returning because a compatible endpoint happened to answer.
	state, err := manager.Start(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusRunning {
		t.Fatalf("Start() state = %+v, want running", state)
	}
	if !composeForceRecreated(runner.snapshot()) {
		t.Fatalf("stopped managed container was not recreated through Compose: %+v", runner.snapshot())
	}
}

func TestInspectReportsStaleManagedPortDespiteCompatibleEndpoint(t *testing.T) {
	server := healthyServer(t)
	defer server.Close()
	// Managed container is running but bound to a stale host port (8999) while
	// the configured port is 8001. A compatible endpoint answers, but readiness
	// must not be attributed to a container bound to the wrong port.
	container := `[{"Name":"/nemo-asr","Config":{"Image":"swarm-nemo-asr:nemo-asr-v2","Labels":{"com.swarm.voice.runtime":"nemo-asr-v2","com.docker.compose.service":"nemo-asr"}},"State":{"Running":true},"NetworkSettings":{"Ports":{"8001/tcp":[{"HostPort":"8999"}]}}}]`
	runner := capableRunner(container)
	manager := newTestManager(t, runner, server.URL, fixedPort(false))

	inspection, err := manager.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Container.Port != 8999 {
		t.Fatalf("stale container port not captured: %+v", inspection.Container)
	}
	if inspection.State.Mode != ModeManaged || inspection.State.Status != StatusUnhealthy {
		t.Fatalf("stale-port managed container misreported as ready: %+v", inspection.State)
	}
	if !strings.Contains(inspection.State.Message, "stale") {
		t.Fatalf("stale-port managed message unexpected: %+v", inspection.State)
	}

	// Start must reconcile through Compose --force-recreate to rebind the
	// container onto the configured host port.
	state, err := manager.Start(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusRunning {
		t.Fatalf("Start() state = %+v, want running", state)
	}
	if !composeForceRecreated(runner.snapshot()) {
		t.Fatalf("stale-port managed container was not recreated through Compose: %+v", runner.snapshot())
	}
}

func composeForceRecreated(commands []Command) bool {
	for _, command := range commands {
		joined := strings.Join(command.Args, " ")
		if strings.Contains(joined, "compose -f") && strings.Contains(joined, "up -d --force-recreate") {
			return true
		}
	}
	return false
}

func TestExecRunnerHonorsCancellation(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("requires /bin/sh")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (ExecRunner{}).Run(ctx, Command{Name: "/bin/sh", Args: []string{"-c", "sleep 10"}})
	if err == nil {
		t.Fatal("expected canceled command")
	}
}
