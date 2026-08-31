//go:build windows

package a2a

import (
	"fmt"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func acquireRegistryHandleLock(path string) (func() error, error) {
	if err := registrySecureDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".lock")
	name, err := windows.UTF16PtrFromString(lockPath)
	if err != nil {
		return nil, err
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, err
	}
	tokenUser, err := token.GetTokenUser()
	_ = token.Close()
	if err != nil {
		return nil, err
	}
	owner := tokenUser.User.Sid
	sd, err := windows.SecurityDescriptorFromString(
		"O:" + owner.String() + "D:P(A;;GA;;;" + owner.String() + ")",
	)
	if err != nil {
		return nil, fmt.Errorf("build owner-only registry lock security descriptor: %w", err)
	}
	securityAttributes := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		securityAttributes,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	runtime.KeepAlive(sd)
	if err != nil {
		return nil, fmt.Errorf("open registry handle lock %s: %w", lockPath, err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = windows.CloseHandle(handle)
		}
	}()

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return nil, fmt.Errorf("inspect registry handle lock %s: %w", lockPath, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, &unsafeEntryError{path: lockPath, reason: "reparse point"}
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return nil, &unsafeEntryError{path: lockPath, reason: "not a regular file"}
	}

	sd, err = windows.GetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return nil, fmt.Errorf("inspect registry handle lock security %s: %w", lockPath, err)
	}
	fileOwner, _, err := sd.Owner()
	if err != nil {
		return nil, fmt.Errorf("read registry handle lock owner %s: %w", lockPath, err)
	}
	if !fileOwner.Equals(owner) {
		return nil, &unsafeEntryError{path: lockPath, reason: "owned by another user"}
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return nil, fmt.Errorf("read registry handle lock DACL %s: %w", lockPath, err)
	}
	if dacl == nil || dacl.AceCount == 0 {
		return nil, &unsafeEntryError{path: lockPath, reason: "missing owner-only DACL"}
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return nil, fmt.Errorf("read registry handle lock ACE %s: %w", lockPath, err)
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return nil, &unsafeEntryError{path: lockPath, reason: "unsupported owner-only DACL entry"}
		}
		aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !aceSID.Equals(owner) {
			return nil, &unsafeEntryError{path: lockPath, reason: "DACL grants access to another principal"}
		}
	}

	overlapped := new(windows.Overlapped)
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		return nil, fmt.Errorf("lock registry handle %s: %w", lockPath, err)
	}
	closeOnError = false
	return func() error {
		unlockErr := windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
		closeErr := windows.CloseHandle(handle)
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, nil
}
