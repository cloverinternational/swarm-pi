//go:build windows

package chat

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// detachPeerProcess configures the command to run as an independent process
// detached from the parent (Windows version).
// This prevents the peer from being killed when the parent (TUI) exits.
// stdout/stderr are redirected to %TEMP%\swarm-peer-{handle}.log to prevent
// terminal breakage from peer process output.
func detachPeerProcess(cmd *exec.Cmd) *exec.Cmd {
	// On Windows, we use CREATE_NEW_PROCESS_GROUP to detach the process
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}

	// Redirect stdout and stderr to a log file in %TEMP%
	// This prevents peer process output from breaking the TUI terminal
	// Extract handle from args (format: peer -handle <handle> ...)
	handle := "unknown"
	for i, arg := range cmd.Args {
		if arg == "-handle" && i+1 < len(cmd.Args) {
			handle = cmd.Args[i+1]
			break
		}
	}

	// Use %TEMP% on Windows
	tmpDir := os.Getenv("TEMP")
	if tmpDir == "" {
		tmpDir = os.Getenv("TMP")
	}
	if tmpDir == "" {
		tmpDir = "C:\\Temp"
	}

	logPath := filepath.Join(tmpDir, "swarm-peer-"+handle+".log")
	if logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		// Note: logFile is closed by the OS when the process exits
	}

	return cmd
}
