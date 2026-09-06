//go:build !windows

package bgprocess

import (
	"errors"
	"os/exec"
	"syscall"
)

// ErrNotSupported is returned when an operation is not supported on the platform.
// On Unix, all operations are supported, so this error is defined but not used.
var ErrNotSupported = errors.New("operation not supported on this platform")

// signalTerm returns the signal for graceful termination on Unix.
func signalTerm() syscall.Signal {
	return syscall.SIGTERM
}

// signalKill returns the signal for immediate termination on Unix.
func signalKill() syscall.Signal {
	return syscall.SIGKILL
}

// signalStop returns the signal for pausing a process on Unix.
func signalStop() syscall.Signal {
	return syscall.SIGSTOP
}

// signalCont returns the signal for resuming a paused process on Unix.
func signalCont() syscall.Signal {
	return syscall.SIGCONT
}

// canPauseResume reports whether the platform supports pause/resume operations.
// Unix systems support this via SIGSTOP/SIGCONT.
func canPauseResume() bool {
	return true
}

// setSysProcAttr configures the command to run in its own session,
// detached from the controlling terminal. This prevents interactive
// commands from capturing terminal input.
func setSysProcAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}
