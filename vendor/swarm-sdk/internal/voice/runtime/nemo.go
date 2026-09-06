package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/voice/runtimeassets"
)

const (
	nemoProvider          = "nemo"
	defaultNemoPort       = 8001
	defaultNemoModel      = "nvidia/parakeet-tdt-0.6b-v3"
	runtimeResponseLimit  = 1 << 20
	defaultReadyTimeout   = 5 * time.Minute
	defaultReadyBackoff   = 250 * time.Millisecond
	maxReadyBackoff       = 5 * time.Second
	transcriptionRoute    = "/v1/audio/transcriptions"
	transcriptionContract = "openai-multipart-v1"
)

var (
	ErrConnectOnly     = errors.New("nemo runtime is connect-only on this host")
	ErrPortConflict    = errors.New("configured NeMo port is occupied by an incompatible service")
	ErrUnsafeContainer = errors.New("existing nemo-asr container is incompatible and will not be replaced")
)

type NemoConfig struct {
	Endpoint     string
	Port         int
	Model        string
	Paths        Paths
	Runner       Runner
	HTTPClient   HTTPClient
	Files        FileOps
	PortChecker  PortChecker
	GOOS         string
	GOARCH       string
	Now          func() time.Time
	ReadyTimeout time.Duration
	ReadyBackoff time.Duration
}

type NemoManager struct {
	endpoint     string
	port         int
	model        string
	paths        Paths
	runner       Runner
	http         HTTPClient
	files        FileOps
	ports        PortChecker
	goos         string
	goarch       string
	now          func() time.Time
	readyTimeout time.Duration
	readyBackoff time.Duration
	tempID       atomic.Uint64
}

func NewNemoManager(config NemoConfig) (*NemoManager, error) {
	if config.Port == 0 {
		config.Port = defaultNemoPort
	}
	if config.Port < 1 || config.Port > 65535 {
		return nil, fmt.Errorf("runtime: invalid NeMo port %d", config.Port)
	}
	if strings.TrimSpace(config.Model) == "" {
		config.Model = defaultNemoModel
	}
	if config.Endpoint == "" {
		config.Endpoint = fmt.Sprintf("http://127.0.0.1:%d", config.Port)
	}
	parsed, err := url.Parse(config.Endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("runtime: invalid NeMo endpoint")
	}
	if config.Runner == nil {
		config.Runner = ExecRunner{}
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	if config.Files == nil {
		config.Files = OSFileOps{}
	}
	if config.PortChecker == nil {
		config.PortChecker = NetPortChecker{}
	}
	if config.GOOS == "" {
		config.GOOS = goruntime.GOOS
	}
	if config.GOARCH == "" {
		config.GOARCH = goruntime.GOARCH
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.ReadyTimeout <= 0 {
		config.ReadyTimeout = defaultReadyTimeout
	}
	if config.ReadyBackoff <= 0 {
		config.ReadyBackoff = defaultReadyBackoff
	}
	if config.Paths.Root == "" {
		// Canonical managed-runtime root under ~/.swarm (paths.In ensures parents).
		config.Paths.Root = paths.In("voice", "runtimes", "nemo")
	}
	if config.Paths.Cache == "" {
		// Model cache stays under the XDG cache root (~/.cache/swarm), separate
		// from the ~/.swarm config/state root.
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return nil, fmt.Errorf("runtime: locate home: %w", homeErr)
		}
		config.Paths.Cache = filepath.Join(home, ".cache", "swarm", "voice", "nemo")
	}
	if config.Paths.ProcVersion == "" {
		config.Paths.ProcVersion = "/proc/version"
	}
	return &NemoManager{endpoint: strings.TrimRight(config.Endpoint, "/"), port: config.Port, model: config.Model, paths: config.Paths, runner: config.Runner, http: config.HTTPClient, files: config.Files, ports: config.PortChecker, goos: config.GOOS, goarch: config.GOARCH, now: config.Now, readyTimeout: config.ReadyTimeout, readyBackoff: config.ReadyBackoff}, nil
}

func (m *NemoManager) Capabilities(ctx context.Context) (Capabilities, error) {
	inspection, err := m.Inspect(ctx)
	return inspection.Capabilities, err
}

func (m *NemoManager) Inspect(ctx context.Context) (Inspection, error) {
	inspection := Inspection{OS: m.goos, Arch: m.goarch}
	inspection.Capabilities = Capabilities{Connect: true, Mode: ModeConnectOnly}
	inspection.State = m.state(StatusMissing, ModeConnectOnly, "runtime is not installed")

	if data, err := m.files.ReadFile(m.paths.ProcVersion); err == nil {
		inspection.Capabilities.WSL2 = strings.Contains(strings.ToLower(string(data)), "microsoft-standard-wsl2")
	}
	if m.goos != "linux" || (m.goarch != "amd64" && m.goarch != "arm64") {
		inspection.Capabilities.Reason = "managed NeMo requires Linux amd64 or arm64; an existing endpoint can still be used"
		if m.endpointCompatible(ctx) {
			inspection.State = m.state(StatusRunning, ModeExternal, "compatible endpoint detected")
			inspection.Capabilities.Mode = ModeExternal
		}
		return inspection, nil
	}

	inspection.Docker = m.commandOK(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	inspection.Compose = inspection.Docker && m.commandOK(ctx, "docker", "compose", "version", "--short")
	gpuCLI := m.commandOK(ctx, "nvidia-smi", "-L")
	runtimeResult, _ := m.runner.Run(ctx, Command{Name: "docker", Args: []string{"info", "--format", "{{json .Runtimes}}"}})
	inspection.NVIDIA = gpuCLI && strings.Contains(strings.ToLower(runtimeResult.Stdout), "nvidia")
	inspection.Capabilities.GPU = inspection.NVIDIA
	if err := ctx.Err(); err != nil {
		return inspection, err
	}
	inspection.Container = m.inspectContainer(ctx)
	var portErr error
	inspection.PortInUse, portErr = m.ports.InUse(ctx, "127.0.0.1", m.port)
	if portErr != nil {
		return inspection, fmt.Errorf("runtime: inspect port: %w", portErr)
	}
	health, _ := m.Health(ctx)
	compatible := health.Healthy && m.transcriptionCapable(ctx)
	// A compatible endpoint on the configured port may belong to an unrelated
	// service. Managed readiness must be attributable to the owned container
	// itself: it must be running AND bound to the configured host port.
	// Without this gate, a stopped managed container (or one bound to a stale
	// port) would be misreported as a healthy managed runtime whenever any
	// compatible endpoint happened to answer, and Start would early-return
	// instead of recreating through Compose.
	managedBound := inspection.Container.Running && inspection.Container.Port == m.port
	managedCompatible := compatible
	if inspection.Container.Managed {
		managedCompatible = compatible && managedBound && m.modelReady(ctx)
	}
	if err := ctx.Err(); err != nil {
		return inspection, err
	}

	manageable := inspection.Docker && inspection.Compose && inspection.NVIDIA
	inspection.Capabilities.Install = manageable
	inspection.Capabilities.Start = manageable
	inspection.Capabilities.Update = manageable && inspection.Container.Managed
	inspection.Capabilities.Stop = manageable && inspection.Container.Managed
	if inspection.Container.Exists && !inspection.Container.Managed {
		inspection.Capabilities.Start = false
	}
	if !manageable {
		inspection.Capabilities.Reason = missingRequirement(inspection)
	}

	switch {
	case inspection.Container.Managed:
		inspection.Capabilities.Mode = ModeManaged
		inspection.Capabilities.Stop = manageable
		inspection.Capabilities.Update = manageable
		switch {
		case managedCompatible:
			inspection.State = m.state(StatusRunning, ModeManaged, "managed endpoint is healthy")
		case inspection.Container.Running && inspection.Container.Port != m.port:
			inspection.State = m.state(StatusUnhealthy, ModeManaged, "managed container is bound to a stale host port; recreate is required")
		case inspection.Container.Running:
			message := "managed container is running but unhealthy"
			if compatible {
				message = "managed container is running without the configured model"
			}
			inspection.State = m.state(StatusUnhealthy, ModeManaged, message)
		default:
			inspection.State = m.state(StatusStopped, ModeManaged, "managed container is stopped")
		}
	case inspection.Container.Exists && inspection.Container.Port == m.port:
		inspection.Capabilities.Mode = ModeAdopted
		inspection.Capabilities.Stop = false
		inspection.Capabilities.Update = false
		inspection.Capabilities.Start = false
		if compatible {
			inspection.State = m.state(StatusRunning, ModeAdopted, "compatible existing nemo-asr container adopted")
		} else if inspection.Container.Running {
			inspection.State = m.state(StatusUnhealthy, ModeAdopted, "adopted container is not healthy")
		} else {
			inspection.State = m.state(StatusStopped, ModeAdopted, "unowned container is stopped; manual external startup is required")
		}
	case compatible:
		inspection.Capabilities.Mode = ModeExternal
		inspection.Capabilities.Stop = false
		inspection.Capabilities.Update = false
		inspection.State = m.state(StatusRunning, ModeExternal, "compatible endpoint detected")
	case inspection.Container.Exists:
		inspection.State = m.state(StatusConflict, ModeConnectOnly, ErrUnsafeContainer.Error())
	case inspection.PortInUse:
		inspection.State = m.state(StatusConflict, ModeConnectOnly, ErrPortConflict.Error())
	case manageable:
		inspection.Capabilities.Mode = ModeManaged
		inspection.State = m.state(StatusMissing, ModeManaged, "managed runtime is available to install")
	}
	return inspection, nil
}

func (m *NemoManager) Install(ctx context.Context, progress ProgressFunc) (State, error) {
	inspection, err := m.Inspect(ctx)
	if err != nil {
		return State{}, err
	}
	if inspection.State.Status == StatusRunning {
		return inspection.State, nil
	}
	if !inspection.Capabilities.Install {
		return inspection.State, fmt.Errorf("%w: %s", ErrConnectOnly, inspection.Capabilities.Reason)
	}
	if inspection.Container.Exists && !inspection.Container.Managed {
		return inspection.State, ErrUnsafeContainer
	}
	if inspection.PortInUse {
		return inspection.State, ErrPortConflict
	}
	emit(progress, "bundle", "Installing pinned NeMo runtime bundle", 15)
	if err := m.installBundle(); err != nil {
		return State{}, err
	}
	emit(progress, "image", "Building pinned NeMo image", 55)
	if _, err := m.compose(ctx, "build", "--pull"); err != nil {
		return State{}, fmt.Errorf("runtime: build pinned image: %w", err)
	}
	emit(progress, "complete", "Pinned NeMo runtime installed", 100)
	return m.state(StatusStopped, ModeManaged, "runtime installed"), nil
}

func (m *NemoManager) Start(ctx context.Context, progress ProgressFunc) (State, error) {
	inspection, err := m.Inspect(ctx)
	if err != nil {
		return State{}, err
	}
	if inspection.State.Status == StatusRunning {
		return inspection.State, nil
	}
	if inspection.Container.Exists && !inspection.Container.Managed {
		return inspection.State, fmt.Errorf("%w: unowned stopped containers require an explicit manual start", ErrUnsafeContainer)
	}
	if !inspection.Capabilities.Start {
		return inspection.State, fmt.Errorf("%w: %s", ErrConnectOnly, inspection.Capabilities.Reason)
	}
	if inspection.State.Status == StatusMissing {
		if _, err := m.Install(ctx, progress); err != nil {
			return State{}, err
		}
	}
	emit(progress, "start", "Starting managed NeMo runtime", 75)
	if _, err := m.compose(ctx, "up", "-d", "--force-recreate", "--remove-orphans"); err != nil {
		return State{}, fmt.Errorf("runtime: start: %w", err)
	}
	emit(progress, "ready", "Waiting for configured NeMo model", 90)
	if err := m.waitUntilReady(ctx); err != nil {
		return m.state(StatusUnhealthy, ModeManaged, "managed runtime did not become ready"), err
	}
	emit(progress, "complete", "Managed NeMo runtime is ready", 100)
	return m.state(StatusRunning, ModeManaged, "managed endpoint and configured model are ready"), nil
}

func (m *NemoManager) Stop(ctx context.Context, progress ProgressFunc) (State, error) {
	inspection, err := m.Inspect(ctx)
	if err != nil {
		return State{}, err
	}
	if !inspection.Container.Managed {
		return inspection.State, fmt.Errorf("runtime: refusing to stop unowned NeMo runtime")
	}
	emit(progress, "stop", "Stopping managed NeMo runtime", 50)
	if _, err := m.compose(ctx, "stop"); err != nil {
		return State{}, fmt.Errorf("runtime: stop: %w", err)
	}
	emit(progress, "complete", "Managed NeMo runtime stopped", 100)
	return m.state(StatusStopped, ModeManaged, "managed runtime stopped"), nil
}

func (m *NemoManager) Update(ctx context.Context, progress ProgressFunc) (State, error) {
	inspection, err := m.Inspect(ctx)
	if err != nil {
		return State{}, err
	}
	if !inspection.Container.Managed {
		return inspection.State, fmt.Errorf("runtime: refusing to update unowned NeMo runtime")
	}
	emit(progress, "bundle", "Refreshing pinned runtime bundle", 20)
	if err := m.installBundle(); err != nil {
		return State{}, err
	}
	emit(progress, "image", "Rebuilding pinned NeMo image", 55)
	if _, err := m.compose(ctx, "build", "--pull"); err != nil {
		return State{}, fmt.Errorf("runtime: rebuild pinned image: %w", err)
	}
	emit(progress, "restart", "Applying pinned runtime update", 80)
	if _, err := m.compose(ctx, "up", "-d", "--remove-orphans"); err != nil {
		return State{}, fmt.Errorf("runtime: update: %w", err)
	}
	emit(progress, "complete", "Managed NeMo runtime updated", 100)
	return m.state(StatusStarting, ModeManaged, "managed runtime updated"), nil
}

func (m *NemoManager) Health(ctx context.Context) (Health, error) {
	started := m.now()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.endpoint+"/health", nil)
	if err != nil {
		return Health{}, err
	}
	response, err := m.http.Do(request)
	health := Health{Endpoint: redactEndpoint(m.endpoint), CheckedAt: m.now(), Latency: m.now().Sub(started)}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return health, ctxErr
		}
		return health, errors.New("runtime: health request failed")
	}
	defer response.Body.Close()
	var payload struct {
		Status string `json:"status"`
	}
	if err := decodeLimited(response.Body, &payload); err != nil {
		return health, err
	}
	health.Status = payload.Status
	health.Healthy = response.StatusCode >= 200 && response.StatusCode < 300 && (payload.Status == "ok" || payload.Status == "healthy" || payload.Status == "ready")
	return health, nil
}

func (m *NemoManager) Models(ctx context.Context) ([]Model, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.endpoint+"/models", nil)
	if err != nil {
		return nil, err
	}
	response, err := m.http.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, errors.New("runtime: models request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("runtime: models endpoint returned %s", response.Status)
	}
	var envelope struct {
		Models []Model `json:"models"`
		Data   []Model `json:"data"`
	}
	if err := decodeLimited(response.Body, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Models) > 0 {
		return envelope.Models, nil
	}
	return envelope.Data, nil
}

func (m *NemoManager) modelReady(ctx context.Context) bool {
	models, err := m.Models(ctx)
	if err != nil {
		return false
	}
	for _, model := range models {
		if model.ID == m.model && model.Ready {
			return true
		}
	}
	return false
}

func (m *NemoManager) managedEndpointReady(ctx context.Context) bool {
	health, err := m.Health(ctx)
	return err == nil && health.Healthy && m.transcriptionCapable(ctx) && m.modelReady(ctx)
}

func (m *NemoManager) waitUntilReady(ctx context.Context) error {
	readyCtx, cancel := context.WithTimeout(ctx, m.readyTimeout)
	defer cancel()

	backoff := m.readyBackoff
	for {
		if m.managedEndpointReady(readyCtx) {
			return nil
		}
		if err := readyCtx.Err(); err != nil {
			if err == context.DeadlineExceeded && ctx.Err() == nil {
				return fmt.Errorf("runtime: NeMo endpoint and configured model %q were not ready within %s: %w", m.model, m.readyTimeout, err)
			}
			return fmt.Errorf("runtime: wait for NeMo readiness: %w", err)
		}

		timer := time.NewTimer(backoff)
		select {
		case <-readyCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
		if backoff < maxReadyBackoff {
			backoff *= 2
			if backoff > maxReadyBackoff {
				backoff = maxReadyBackoff
			}
		}
	}
}

func (m *NemoManager) endpointCompatible(ctx context.Context) bool {
	health, err := m.Health(ctx)
	return err == nil && health.Healthy && m.transcriptionCapable(ctx)
}

// transcriptionCapable verifies the route and wire contract without uploading
// audio or invoking model inference. The bundled server advertises this contract
// on OPTIONS, and compatible third-party servers may advertise POST via Allow.
func (m *NemoManager) transcriptionCapable(ctx context.Context) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodOptions, m.endpoint+transcriptionRoute, nil)
	if err != nil {
		return false
	}
	response, err := m.http.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))

	if response.Header.Get("X-Swarm-Transcription-Contract") == transcriptionContract {
		return response.StatusCode >= 200 && response.StatusCode < 300
	}
	for _, method := range strings.Split(response.Header.Get("Allow"), ",") {
		if strings.EqualFold(strings.TrimSpace(method), http.MethodPost) {
			return response.StatusCode >= 200 && response.StatusCode < 300 || response.StatusCode == http.StatusMethodNotAllowed
		}
	}
	return false
}

func (m *NemoManager) inspectContainer(ctx context.Context) Container {
	result, err := m.runner.Run(ctx, Command{Name: "docker", Args: []string{"inspect", runtimeassets.ContainerName}})
	if err != nil {
		return Container{}
	}
	var records []struct {
		Name   string `json:"Name"`
		Config struct {
			Image  string            `json:"Image"`
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		State struct {
			Running bool `json:"Running"`
		} `json:"State"`
		NetworkSettings struct {
			Ports map[string][]struct {
				HostPort string `json:"HostPort"`
			} `json:"Ports"`
		} `json:"NetworkSettings"`
	}
	if json.Unmarshal([]byte(result.Stdout), &records) != nil || len(records) == 0 {
		return Container{}
	}
	record := records[0]
	container := Container{Exists: true, Running: record.State.Running, Name: strings.TrimPrefix(record.Name, "/"), Image: record.Config.Image}
	if bindings := record.NetworkSettings.Ports["8001/tcp"]; len(bindings) > 0 {
		container.Port, _ = strconv.Atoi(bindings[0].HostPort)
	}
	container.Managed = record.Config.Labels["com.swarm.voice.runtime"] == runtimeassets.BundleVersion &&
		record.Config.Labels["com.docker.compose.service"] == "nemo-asr" &&
		container.Image == runtimeassets.Image
	return container
}

func (m *NemoManager) commandOK(ctx context.Context, name string, args ...string) bool {
	_, err := m.runner.Run(ctx, Command{Name: name, Args: args})
	return err == nil
}

func (m *NemoManager) compose(ctx context.Context, args ...string) (CommandResult, error) {
	commandArgs := append([]string{"compose", "-f", filepath.Join(m.bundleDir(), "compose.yaml")}, args...)
	return m.runner.Run(ctx, Command{Name: "docker", Args: commandArgs, Dir: m.bundleDir(), Env: map[string]string{
		"NEMO_CACHE_DIR": m.paths.Cache,
		"NEMO_MODEL":     m.model,
		"NEMO_PORT":      strconv.Itoa(m.port),
	}})
}

func (m *NemoManager) bundleDir() string {
	return filepath.Join(m.paths.Root, runtimeassets.BundleVersion)
}

func (m *NemoManager) installBundle() error {
	bundleDir := m.bundleDir()
	if err := m.files.MkdirAll(bundleDir, 0o700); err != nil {
		return fmt.Errorf("runtime: create bundle: %w", err)
	}
	if err := m.files.MkdirAll(m.paths.Cache, 0o700); err != nil {
		return fmt.Errorf("runtime: create cache: %w", err)
	}
	names := make([]string, 0, len(runtimeassets.Files))
	for name := range runtimeassets.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := m.writeAtomic(filepath.Join(bundleDir, name), runtimeassets.Files[name], 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (m *NemoManager) writeAtomic(path string, data []byte, mode os.FileMode) error {
	temporary := fmt.Sprintf("%s.tmp-%d", path, m.tempID.Add(1))
	if err := m.files.WriteFile(temporary, data, mode); err != nil {
		return fmt.Errorf("runtime: write bundle: %w", err)
	}
	if err := m.files.Rename(temporary, path); err != nil {
		return fmt.Errorf("runtime: publish bundle: %w", err)
	}
	return nil
}

func (m *NemoManager) state(status Status, mode Mode, message string) State {
	return State{Provider: nemoProvider, Status: status, Mode: mode, Endpoint: redactEndpoint(m.endpoint), Version: runtimeassets.BundleVersion, Message: message, UpdatedAt: m.now()}
}

func missingRequirement(inspection Inspection) string {
	var missing []string
	if !inspection.Docker {
		missing = append(missing, "Docker")
	}
	if !inspection.Compose {
		missing = append(missing, "Docker Compose")
	}
	if !inspection.NVIDIA {
		missing = append(missing, "NVIDIA GPU/runtime")
	}
	return "managed NeMo unavailable: missing " + strings.Join(missing, ", ")
}

func emit(callback ProgressFunc, stage, message string, percent int) {
	if callback != nil {
		callback(Progress{Stage: stage, Message: message, Percent: percent})
	}
}

func redactEndpoint(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "[redacted endpoint]"
	}
	parsed.User = nil
	if parsed.RawQuery != "" {
		query := parsed.Query()
		for key := range query {
			query.Set(key, "REDACTED")
		}
		parsed.RawQuery = query.Encode()
	}
	parsed.Fragment = ""
	return parsed.String()
}

func decodeLimited(reader io.Reader, value any) error {
	data, err := io.ReadAll(io.LimitReader(reader, runtimeResponseLimit+1))
	if err != nil {
		return fmt.Errorf("runtime: read response: %w", err)
	}
	if len(data) > runtimeResponseLimit {
		return fmt.Errorf("runtime: response exceeds %d bytes", runtimeResponseLimit)
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("runtime: decode response: %w", err)
	}
	return nil
}
