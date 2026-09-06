//go:build windows

package a2a

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

var registryFchmod = func(int, uint32) error {
	return fmt.Errorf("fchmod is unsupported on windows")
}

func fileOwnerUID(os.FileInfo) (uint32, bool) {
	return 0, false
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

func rejectReparse(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return &unsafeEntryError{path: path, reason: "reparse point"}
	}
	return nil
}

func registrySecureDir(path string) error {
	if _, err := registryRelative(path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, dirPerm); err != nil {
		return err
	}
	return rejectReparse(path)
}

func registryReadFile(path string) ([]byte, error) {
	if _, err := registryRelative(path); err != nil {
		return nil, err
	}
	if err := rejectReparse(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := rejectReparse(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func registryWriteFileAtomic(path string, data []byte) error {
	if err := registrySecureDir(filepath.Dir(path)); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(filePerm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func registryReadDir(path string) ([]os.DirEntry, error) {
	if err := registrySecureDir(path); err != nil {
		return nil, err
	}
	return os.ReadDir(path)
}

func registryRemove(path string) error {
	if _, err := registryRelative(path); err != nil {
		return err
	}
	if err := rejectReparse(filepath.Dir(path)); err != nil {
		return err
	}
	return fmt.Errorf("registry destructive operation is unsupported on windows without handle-relative delete")
}

func registryEntryMode(path string) (os.FileMode, error) {
	if err := rejectReparse(path); err != nil {
		return 0, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Mode(), nil
}

func registryProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	const stillActive = 259
	return windows.GetExitCodeProcess(h, &code) == nil && code == stillActive
}
