// Package runtime provides process-wide authority persistence primitives for
// the built-in Chrome runtime.
package runtime

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

var (
	ErrHostLocked      = errors.New("chrome runtime: host authority is already locked")
	ErrLockNotHeld     = errors.New("chrome runtime: host authority lock is not held")
	ErrInsecureStorage = errors.New("chrome runtime: insecure storage")
)

// HostLock is a lifetime lock for one Chrome runtime authority. Call Close
// only after every service using the authority has stopped.
type HostLock struct {
	mu     sync.Mutex
	file   *os.File
	path   string
	unlock func(*os.File) error
}

// AcquireHostLock acquires a non-blocking, process-wide exclusive lock.
func AcquireHostLock(path string) (*HostLock, error) {
	clean, err := cleanPath(path)
	if err != nil {
		return nil, err
	}
	if err := secureParent(clean); err != nil {
		return nil, err
	}
	f, unlock, err := acquirePlatformLock(clean)
	if err != nil {
		return nil, err
	}
	return &HostLock{file: f, path: clean, unlock: unlock}, nil
}

func (l *HostLock) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	unlockErr := l.unlock(f)
	closeErr := f.Close()
	return errors.Join(unlockErr, closeErr)
}

func (l *HostLock) holds(path string) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file != nil && l.path == path
}

func (l *HostLock) withHeld(path string, fn func() error) error {
	if l == nil {
		return ErrLockNotHeld
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil || l.path != path {
		return ErrLockNotHeld
	}
	return fn()
}

func cleanPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%w: empty path", ErrInsecureStorage)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("chrome runtime: resolve path: %w", err)
	}
	return filepath.Clean(abs), nil
}

func secureParent(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("chrome runtime: create storage directory: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("chrome runtime: inspect storage directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: storage parent is not a directory", ErrInsecureStorage)
	}
	if err := validatePlatformDirectory(dir, info); err != nil {
		return err
	}
	return nil
}

func readBoundedStoreFile(f *os.File) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(f, maxStoreFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("chrome runtime: read secure file: %w", err)
	}
	if len(data) > maxStoreFileBytes {
		return nil, fmt.Errorf("%w: store file exceeds %d bytes", ErrCorruptStore, maxStoreFileBytes)
	}
	return data, nil
}
