//go:build aix || android || darwin || dragonfly || freebsd || hurd || illumos || ios || linux || netbsd || openbsd || solaris

package lan

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func openSecureCredentialFile(path string) (*os.File, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("%w: path must be absolute and clean", ErrCredentialStorage)
	}
	parent := filepath.Dir(path)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return nil, fmt.Errorf("%w: parent unavailable", ErrCredentialStorage)
	}
	if !parentInfo.IsDir() || parentInfo.Mode().Perm() != 0o700 || !ownedByCurrentUser(parentInfo) {
		return nil, fmt.Errorf("%w: parent must be owner-only directory", ErrCredentialStorage)
	}
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: file unavailable", ErrCredentialStorage)
	}
	if !linkInfo.Mode().IsRegular() || linkInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: file must be regular and not a symlink", ErrCredentialStorage)
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: open failed", ErrCredentialStorage)
	}
	f := os.NewFile(uintptr(fd), path)
	if f == nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("%w: open failed", ErrCredentialStorage)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 ||
		!ownedByCurrentUser(info) || !os.SameFile(linkInfo, info) {
		f.Close()
		return nil, fmt.Errorf("%w: file must be the owner-only opened object", ErrCredentialStorage)
	}
	return f, nil
}

func ownedByCurrentUser(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Geteuid())
}
