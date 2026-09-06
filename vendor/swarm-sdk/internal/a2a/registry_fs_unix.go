//go:build !windows

package a2a

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/unix"
)

var registryFchmod = unix.Fchmod
var registryTempCounter atomic.Uint64

func fileOwnerUID(fi os.FileInfo) (uint32, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
}

func registryRelative(path string) (string, error) {
	root, err := filepath.Abs(SwarmDir())
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("registry path %q escapes root %q", path, root)
	}
	return rel, nil
}

func verifyRegistryDirFD(fd int, path string) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("fstat registry directory %s: %w", path, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return &unsafeEntryError{path: path, reason: "not a directory"}
	}
	if int(st.Uid) != os.Geteuid() {
		return &unsafeEntryError{path: path, reason: "owned by another user"}
	}
	if os.FileMode(st.Mode).Perm() != dirPerm {
		if err := registryFchmod(fd, uint32(dirPerm)); err != nil {
			return fmt.Errorf("tighten registry directory %s: %w", path, err)
		}
		if err := unix.Fstat(fd, &st); err != nil {
			return fmt.Errorf("verify registry directory mode %s: %w", path, err)
		}
		if os.FileMode(st.Mode).Perm() != dirPerm {
			return fmt.Errorf("registry directory %s mode is %o after tightening, want %o", path, os.FileMode(st.Mode).Perm(), dirPerm)
		}
	}
	return nil
}

func openRegistryRoot(create bool) (int, error) {
	root := SwarmDir()
	if create {
		if err := os.MkdirAll(root, dirPerm); err != nil {
			return -1, err
		}
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, err
	}
	if err := verifyRegistryDirFD(fd, root); err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func openRegistryDir(path string, create bool) (int, error) {
	rel, err := registryRelative(path)
	if err != nil {
		return -1, err
	}
	fd, err := openRegistryRoot(create)
	if err != nil {
		return -1, err
	}
	if rel == "." {
		return fd, nil
	}
	current := SwarmDir()
	for _, name := range strings.Split(filepath.ToSlash(rel), "/") {
		if err := validateRegistryName(name); err != nil {
			unix.Close(fd)
			return -1, err
		}
		if create {
			if err := unix.Mkdirat(fd, name, uint32(dirPerm)); err != nil && !errors.Is(err, unix.EEXIST) {
				unix.Close(fd)
				return -1, err
			}
		}
		next, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			unix.Close(fd)
			return -1, err
		}
		unix.Close(fd)
		fd = next
		current = filepath.Join(current, name)
		if err := verifyRegistryDirFD(fd, current); err != nil {
			unix.Close(fd)
			return -1, err
		}
	}
	return fd, nil
}

func verifyRegistryFileFD(fd int, path string) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("fstat registry file %s: %w", path, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return &unsafeEntryError{path: path, reason: "not a regular file"}
	}
	if int(st.Uid) != os.Geteuid() {
		return &unsafeEntryError{path: path, reason: "owned by another user"}
	}
	if os.FileMode(st.Mode).Perm() != filePerm {
		if err := registryFchmod(fd, uint32(filePerm)); err != nil {
			return fmt.Errorf("tighten registry file %s: %w", path, err)
		}
		if err := unix.Fstat(fd, &st); err != nil {
			return fmt.Errorf("verify registry file mode %s: %w", path, err)
		}
		if os.FileMode(st.Mode).Perm() != filePerm {
			return fmt.Errorf("registry file %s mode is %o after tightening, want %o", path, os.FileMode(st.Mode).Perm(), filePerm)
		}
	}
	return nil
}

func registryReadFile(path string) ([]byte, error) {
	dirFD, err := openRegistryDir(filepath.Dir(path), false)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dirFD)
	fd, err := unix.Openat(dirFD, filepath.Base(path), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, &unsafeEntryError{path: path, reason: "symlink"}
		}
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	if err := verifyRegistryFileFD(fd, path); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

func registryWriteFileAtomic(path string, data []byte) error {
	dirFD, err := openRegistryDir(filepath.Dir(path), true)
	if err != nil {
		return err
	}
	defer unix.Close(dirFD)
	base := filepath.Base(path)
	tmp := fmt.Sprintf(".%s.tmp-%d-%d", base, os.Getpid(), registryTempCounter.Add(1))
	fd, err := unix.Openat(dirFD, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(filePerm))
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), tmp)
	cleanup := true
	defer func() {
		f.Close()
		if cleanup {
			_ = unix.Unlinkat(dirFD, tmp, 0)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := registryFchmod(fd, uint32(filePerm)); err != nil {
		return err
	}
	if err := verifyRegistryFileFD(fd, path); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := unix.Renameat(dirFD, tmp, dirFD, base); err != nil {
		return err
	}
	cleanup = false
	return unix.Fsync(dirFD)
}

func registryReadDir(path string) ([]os.DirEntry, error) {
	fd, err := openRegistryDir(path, false)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	return f.ReadDir(-1)
}

func registryRemove(path string) error {
	dirFD, err := openRegistryDir(filepath.Dir(path), false)
	if err != nil {
		return err
	}
	defer unix.Close(dirFD)
	err = unix.Unlinkat(dirFD, filepath.Base(path), 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}

func registryEntryMode(path string) (os.FileMode, error) {
	dirFD, err := openRegistryDir(filepath.Dir(path), false)
	if err != nil {
		return 0, err
	}
	defer unix.Close(dirFD)
	var st unix.Stat_t
	if err := unix.Fstatat(dirFD, filepath.Base(path), &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return 0, err
	}
	mode := os.FileMode(st.Mode & 0o777)
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFLNK:
		mode |= os.ModeSymlink
	case unix.S_IFSOCK:
		mode |= os.ModeSocket
	case unix.S_IFDIR:
		mode |= os.ModeDir
	}
	return mode, nil
}

func registrySecureDir(path string) error {
	fd, err := openRegistryDir(path, true)
	if err != nil {
		return err
	}
	return unix.Close(fd)
}

func registryProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return unix.Kill(pid, 0) == nil
}
