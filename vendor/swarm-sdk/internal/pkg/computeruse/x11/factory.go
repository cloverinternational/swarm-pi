package x11

import (
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/pkg/computeruse"
)

func init() {
	// Register X11 backend with auto-detect
	computeruse.AutoDetectBackend = func() (computeruse.InputBackend, error) {
		// Only use X11 if DISPLAY is set
		if os.Getenv("DISPLAY") == "" {
			return nil, computeruse.ErrNoDisplay
		}

		return NewBackend(DefaultOptions())
	}
}

// NewExecutor creates a Linux executor with the X11 backend.
// This is a convenience function for creating an X11-based computer executor.
func NewExecutor(opts ...Options) (*computeruse.LinuxExecutor, error) {
	backendOpts := DefaultOptions()
	if len(opts) > 0 {
		backendOpts = opts[0]
	}

	backend, err := NewBackend(backendOpts)
	if err != nil {
		return nil, err
	}

	executorOpts := computeruse.DefaultOptions()
	if backendOpts.MouseAnimation {
		executorOpts.MouseAnimation = true
	}

	return computeruse.NewLinuxExecutor(backend, executorOpts)
}
