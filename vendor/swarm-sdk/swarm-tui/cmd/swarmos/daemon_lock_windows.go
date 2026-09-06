//go:build windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func lockDaemonFile(f *os.File, block bool) (bool, error) {
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if !block {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		flags,
		0,
		1,
		0,
		new(windows.Overlapped),
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func unlockDaemonFile(f *os.File) error {
	return windows.UnlockFileEx(
		windows.Handle(f.Fd()),
		0,
		1,
		0,
		new(windows.Overlapped),
	)
}

// sameOwnerAsSelf reports whether path (a presence-advertised artifact such
// as a control socket) is owned by the same Windows security principal as
// this process. Used before any destructive removal of a daemon's artifacts:
// a presence record naming a path owned by a different principal must never
// be trusted or deleted, no matter what its content claims.
func sameOwnerAsSelf(path string) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return false, err
	}

	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return false, err
	}
	defer token.Close()
	tokenUser, err := token.GetTokenUser()
	if err != nil {
		return false, err
	}

	return owner.Equals(tokenUser.User.Sid), nil
}
