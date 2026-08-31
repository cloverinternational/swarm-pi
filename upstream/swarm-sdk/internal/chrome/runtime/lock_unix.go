//go:build unix

package runtime

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

func acquirePlatformLock(path string) (*os.File, func(*os.File) error, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("chrome runtime: open host lock: %w", err)
	}
	f := os.NewFile(uintptr(fd), path)
	if err := validateUnixFile(f); err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, nil, ErrHostLocked
		}
		return nil, nil, fmt.Errorf("chrome runtime: lock host authority: %w", err)
	}
	return f, func(file *os.File) error {
		return unix.Flock(int(file.Fd()), unix.LOCK_UN)
	}, nil
}

func validatePlatformDirectory(path string, info fs.FileInfo) error {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return fmt.Errorf("chrome runtime: inspect storage directory: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || !info.IsDir() {
		return fmt.Errorf("%w: storage parent is not a real directory", ErrInsecureStorage)
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%w: directory %s is not owned by the current user", ErrInsecureStorage, path)
	}
	if fs.FileMode(stat.Mode).Perm() != 0o700 {
		return fmt.Errorf("%w: directory %s permissions are %04o, require 0700", ErrInsecureStorage, path, fs.FileMode(stat.Mode).Perm())
	}
	return nil
}

func validatePlatformFile(path string) error {
	_, err := readPlatformFile(path)
	return err
}

func readPlatformFile(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	if err := validateUnixFile(f); err != nil {
		return nil, err
	}
	return readBoundedStoreFile(f)
}

func validateUnixFile(f *os.File) error {
	var stat unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &stat); err != nil {
		return fmt.Errorf("chrome runtime: inspect secure file: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("%w: %s is not a regular file", ErrInsecureStorage, f.Name())
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%w: %s is not owned by the current user", ErrInsecureStorage, f.Name())
	}
	if fs.FileMode(stat.Mode).Perm() != 0o600 {
		return fmt.Errorf("%w: %s permissions are %04o, require 0600", ErrInsecureStorage, f.Name(), fs.FileMode(stat.Mode).Perm())
	}
	return nil
}

func protectPlatformFile(path string) error {
	return validatePlatformFile(path)
}
