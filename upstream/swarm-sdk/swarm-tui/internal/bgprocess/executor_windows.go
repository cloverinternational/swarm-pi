//go:build windows

package bgprocess

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// ErrNotSupported is returned when an operation is not supported on Windows.
var ErrNotSupported = errors.New("operation not supported on Windows")

// signalTerm returns the signal for graceful termination on Windows.
// Windows doesn't have SIGTERM, so we use os.Interrupt (CTRL+C signal).
func signalTerm() os.Signal {
	return os.Interrupt
}

// signalKill returns the signal for immediate termination on Windows.
// os.Kill maps to TerminateProcess on Windows.
func signalKill() os.Signal {
	return os.Kill
}

// signalStop returns nil on Windows because SIGSTOP is not available.
// Windows does not support pausing processes via signals.
// Use canPauseResume() to check before calling.
func signalStop() os.Signal {
	return nil
}

// signalCont returns nil on Windows because SIGCONT is not available.
// Windows does not support resuming paused processes via signals.
// Use canPauseResume() to check before calling.
func signalCont() os.Signal {
	return nil
}

// canPauseResume reports whether the platform supports pause/resume operations.
// Windows does not support this via signals (requires DebugActiveProcessStop
// and other complex Win32 API calls that are not portable).
func canPauseResume() bool {
	return false
}

// setSysProcAttr configures the command to run in its own process group,
// detached from the parent's console. This prevents interactive commands
// from capturing terminal input.
func setSysProcAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags = syscall.CREATE_NEW_PROCESS_GROUP
}
