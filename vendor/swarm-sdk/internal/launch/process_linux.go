//go:build linux

package launch

import (
	"os/exec"
	"syscall"
)

// configureProcess detaches the launched browser from the TUI's process group
// so it survives when the TUI exits or is interrupted.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Pgid:    0,
	}
}

// releaseProcess releases the process handle asynchronously after a successful
// start so the parent does not wait for the browser process.
func releaseProcess(cmd *exec.Cmd) {
	go cmd.Process.Release()
}
