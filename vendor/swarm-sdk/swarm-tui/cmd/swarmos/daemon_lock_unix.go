//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

func lockDaemonFile(f *os.File, block bool) (bool, error) {
	how := syscall.LOCK_EX
	if !block {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		if err == syscall.EWOULDBLOCK {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func unlockDaemonFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// sameOwnerAsSelf reports whether path (a presence-advertised artifact such
// as a control socket) is owned by the same POSIX user id as this process.
// Used before any destructive removal of a daemon's artifacts: a presence
// record naming a path owned by a different user must never be trusted or
// deleted, no matter what its content claims.
func sameOwnerAsSelf(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("unsupported stat metadata for %s", path)
	}
	return int(st.Uid) == os.Getuid(), nil
}
