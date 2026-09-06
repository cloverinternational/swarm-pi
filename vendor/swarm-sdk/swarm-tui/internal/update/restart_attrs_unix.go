//go:build !windows

package update

import "syscall"

func restartSysProcAttr() *syscall.SysProcAttr {
	// Detach the restarted process from the current session so the replacement
	// survives after the updater exits.
	return &syscall.SysProcAttr{
		Setsid: true,
	}
}
