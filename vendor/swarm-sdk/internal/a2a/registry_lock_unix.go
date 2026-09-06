//go:build !windows

package a2a

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func acquireRegistryHandleLock(path string) (func() error, error) {
	dirFD, err := openRegistryDir(filepath.Dir(path), true)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dirFD)

	lockPath := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".lock")
	fd, err := unix.Openat(
		dirFD,
		filepath.Base(lockPath),
		unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		uint32(filePerm),
	)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, &unsafeEntryError{path: lockPath, reason: "symlink"}
		}
		return nil, fmt.Errorf("open registry handle lock %s: %w", lockPath, err)
	}

	f := os.NewFile(uintptr(fd), lockPath)
	if f == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open registry handle lock %s: invalid file descriptor", lockPath)
	}
	if err := verifyRegistryFileFD(fd, lockPath); err != nil {
		_ = f.Close()
		return nil, err
	}
	for {
		err = unix.Flock(fd, unix.LOCK_EX)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock registry handle %s: %w", lockPath, err)
	}

	return func() error {
		unlockErr := unix.Flock(fd, unix.LOCK_UN)
		closeErr := f.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, nil
}
