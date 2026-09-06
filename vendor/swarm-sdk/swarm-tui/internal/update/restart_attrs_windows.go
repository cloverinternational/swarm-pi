//go:build windows

package update

import "syscall"

func restartSysProcAttr() *syscall.SysProcAttr {
	// Windows replacement uses a batch-script relaunch path, so there is no
	// Unix-style session detachment to configure here.
	return nil
}
