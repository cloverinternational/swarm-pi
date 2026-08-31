// Package runtime defines lifecycle management for locally managed voice runtimes.
package runtime

import (
	"context"
	"io/fs"
	"net/http"
	"time"
)

// Manager is the provider-neutral lifecycle contract for a voice runtime.
type Manager interface {
	Capabilities(context.Context) (Capabilities, error)
	Inspect(context.Context) (Inspection, error)
	Install(context.Context, ProgressFunc) (State, error)
	Start(context.Context, ProgressFunc) (State, error)
	Stop(context.Context, ProgressFunc) (State, error)
	Update(context.Context, ProgressFunc) (State, error)
	Health(context.Context) (Health, error)
	Models(context.Context) ([]Model, error)
}

type Mode string

const (
	ModeManaged     Mode = "managed"
	ModeAdopted     Mode = "adopted"
	ModeExternal    Mode = "external"
	ModeConnectOnly Mode = "connect-only"
)

type Status string

const (
	StatusUnknown    Status = "unknown"
	StatusMissing    Status = "missing"
	StatusInstalling Status = "installing"
	StatusStopped    Status = "stopped"
	StatusStarting   Status = "starting"
	StatusRunning    Status = "running"
	StatusUnhealthy  Status = "unhealthy"
	StatusConflict   Status = "conflict"
	StatusUpdating   Status = "updating"
)

// Capabilities describes what the current host can safely manage. Connect is
// always true because a user may provide a remote endpoint.
type Capabilities struct {
	Connect bool   `json:"connect"`
	Install bool   `json:"install"`
	Start   bool   `json:"start"`
	Stop    bool   `json:"stop"`
	Update  bool   `json:"update"`
	GPU     bool   `json:"gpu"`
	WSL2    bool   `json:"wsl2"`
	Mode    Mode   `json:"mode"`
	Reason  string `json:"reason,omitempty"`
}

// Inspection is a side-effect-free snapshot of host and runtime readiness.
type Inspection struct {
	Capabilities Capabilities `json:"capabilities"`
	State        State        `json:"state"`
	OS           string       `json:"os"`
	Arch         string       `json:"arch"`
	Docker       bool         `json:"docker"`
	Compose      bool         `json:"compose"`
	NVIDIA       bool         `json:"nvidia"`
	PortInUse    bool         `json:"port_in_use"`
	Container    Container    `json:"container,omitempty"`
}

type Container struct {
	Exists  bool   `json:"exists"`
	Running bool   `json:"running"`
	Managed bool   `json:"managed"`
	Name    string `json:"name,omitempty"`
	Image   string `json:"image,omitempty"`
	Port    int    `json:"port,omitempty"`
}

type State struct {
	Provider  string    `json:"provider"`
	Status    Status    `json:"status"`
	Mode      Mode      `json:"mode"`
	Endpoint  string    `json:"endpoint"`
	Version   string    `json:"version,omitempty"`
	Message   string    `json:"message,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Progress struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
	Percent int    `json:"percent"`
}

type ProgressFunc func(Progress)

type Health struct {
	Healthy   bool          `json:"healthy"`
	Status    string        `json:"status,omitempty"`
	Endpoint  string        `json:"endpoint"`
	CheckedAt time.Time     `json:"checked_at"`
	Latency   time.Duration `json:"latency"`
}

type Model struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Language string `json:"language,omitempty"`
	Ready    bool   `json:"ready"`
}

// Command contains only non-secret command arguments. Secrets, when a future
// runtime needs them, belong in Env and must never be rendered or logged.
type Command struct {
	Name string
	Args []string
	Dir  string
	Env  map[string]string
}

type CommandResult struct {
	Stdout string
	Stderr string
	Code   int
}

type Runner interface {
	Run(context.Context, Command) (CommandResult, error)
}

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type PortChecker interface {
	InUse(context.Context, string, int) (bool, error)
}

// Paths makes every user-state location injectable for tests and embedders.
type Paths struct {
	Root        string
	Cache       string
	ProcVersion string
}

// FileOps is intentionally small and supports isolated or in-memory tests.
type FileOps interface {
	MkdirAll(string, fs.FileMode) error
	ReadFile(string) ([]byte, error)
	WriteFile(string, []byte, fs.FileMode) error
	Rename(string, string) error
}
