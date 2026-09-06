//go:build !windows

package chat

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// detachPeerProcess configures the command to run as an independent process
// detached from the parent (Unix/Linux/macOS version).
// This prevents the peer from being killed when the parent (TUI) exits.
// stdout/stderr are redirected to /tmp/swarm-peer-{handle}.log to prevent
// terminal breakage from peer process output.
func detachPeerProcess(cmd *exec.Cmd) *exec.Cmd {
	// Set process group ID to a new group (negative PID = new group)
	// This prevents signals to the parent from propagating to children
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // Create new process group
		Pgid:    0,    // 0 means use the process PID as PGID
	}

	// Redirect stdout and stderr to a log file in /tmp/
	// This prevents peer process output from breaking the TUI terminal
	// Extract handle from args (format: peer -handle <handle> ...)
	handle := "unknown"
	for i, arg := range cmd.Args {
		if arg == "-handle" && i+1 < len(cmd.Args) {
			handle = cmd.Args[i+1]
			break
		}
	}

	logPath := filepath.Join("/tmp", "swarm-peer-"+handle+".log")
	if logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		// Note: logFile is closed by the OS when the process exits
	}

	return cmd
}
