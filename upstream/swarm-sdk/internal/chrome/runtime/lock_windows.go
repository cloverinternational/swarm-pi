//go:build windows

package runtime

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	goruntime "runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func currentUserSecurity() (*windows.SID, *windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, nil, fmt.Errorf("chrome runtime: current user SID: %w", err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, nil, fmt.Errorf("chrome runtime: owner-only DACL: %w", err)
	}
	return user.User.Sid, sd, nil
}

func acquirePlatformLock(path string) (*os.File, func(*os.File) error, error) {
	sid, sd, err := currentUserSecurity()
	if err != nil {
		return nil, nil, err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, nil, err
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, sa,
		windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	goruntime.KeepAlive(sd)
	if err != nil {
		return nil, nil, fmt.Errorf("chrome runtime: open host lock: %w", err)
	}
	f := os.NewFile(uintptr(h), path)
	if err := verifyWindowsOwner(h, sid); err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, nil, ErrHostLocked
		}
		return nil, nil, fmt.Errorf("chrome runtime: lock host authority: %w", err)
	}
	if err := applyWindowsDACL(h, sd); err != nil {
		_ = windows.UnlockFileEx(h, 0, 1, 0, &overlapped)
		_ = f.Close()
		return nil, nil, err
	}
	return f, func(file *os.File) error {
		var unlockOverlapped windows.Overlapped
		return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &unlockOverlapped)
	}, nil
}

func verifyWindowsOwner(h windows.Handle, want *windows.SID) error {
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("chrome runtime: inspect file owner: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(want) {
		return fmt.Errorf("%w: file is not owned by the current user", ErrInsecureStorage)
	}
	return nil
}

func verifyWindowsSecurity(h windows.Handle, wantOwner *windows.SID) error {
	if err := verifyWindowsOwner(h, wantOwner); err != nil {
		return err
	}
	actual, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("chrome runtime: inspect file DACL: %w", err)
	}
	dacl, _, err := actual.DACL()
	if err != nil || dacl == nil || dacl.AceCount == 0 {
		return fmt.Errorf("%w: object DACL is missing", ErrInsecureStorage)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("chrome runtime: inspect DACL entry: %w", err)
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("%w: object DACL contains unsupported entry", ErrInsecureStorage)
		}
		aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !aceSID.Equals(wantOwner) {
			return fmt.Errorf("%w: object DACL grants another principal", ErrInsecureStorage)
		}
	}
	return nil
}

func applyWindowsDACL(h windows.Handle, sd *windows.SECURITY_DESCRIPTOR) error {
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("chrome runtime: read owner-only DACL: %w", err)
	}
	if err := windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("chrome runtime: apply owner-only DACL: %w", err)
	}
	return nil
}

func validatePlatformDirectory(path string, info fs.FileInfo) error {
	if !info.IsDir() {
		return fmt.Errorf("%w: storage parent is not a directory", ErrInsecureStorage)
	}
	sid, sd, err := currentUserSecurity()
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(p, windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return fmt.Errorf("chrome runtime: open storage directory: %w", err)
	}
	defer windows.CloseHandle(h)
	var fileInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &fileInfo); err != nil {
		return fmt.Errorf("chrome runtime: inspect storage directory: %w", err)
	}
	if fileInfo.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 ||
		fileInfo.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("%w: storage parent is not a real directory", ErrInsecureStorage)
	}
	if err := verifyWindowsOwner(h, sid); err != nil {
		return err
	}
	if err := applyWindowsDACL(h, sd); err != nil {
		return err
	}
	return verifyWindowsSecurity(h, sid)
}

func validatePlatformFile(path string) error {
	_, err := readPlatformFile(path)
	return err
}

func readPlatformFile(path string) ([]byte, error) {
	sid, _, err := currentUserSecurity()
	if err != nil {
		return nil, err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.READ_CONTROL|windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	defer f.Close()
	if err := verifyWindowsSecurity(h, sid); err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return nil, fmt.Errorf("chrome runtime: inspect secure file: %w", err)
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return nil, fmt.Errorf("%w: file is not a regular non-reparse file", ErrInsecureStorage)
	}
	return readBoundedStoreFile(f)
}

func protectPlatformFile(path string) error {
	sid, sd, err := currentUserSecurity()
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(p, windows.READ_CONTROL|windows.WRITE_DAC, windows.FILE_SHARE_READ,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	if err := verifyWindowsOwner(h, sid); err != nil {
		return err
	}
	return applyWindowsDACL(h, sd)
}
