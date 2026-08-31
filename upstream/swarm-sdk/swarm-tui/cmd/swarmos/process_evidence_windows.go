//go:build windows

package main

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/windows"
)

func readProcessEvidence(pid int) (start, executable, uid string, err error) {
	if pid <= 0 {
		return "", "", "", fmt.Errorf("invalid pid %d", pid)
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", "", "", err
	}
	defer windows.CloseHandle(h)

	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return "", "", "", err
	}
	start = fmt.Sprintf("%d", created.Nanoseconds())

	buf := make([]uint16, syscall.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "", "", "", err
	}
	executable = windows.UTF16ToString(buf[:size])

	var token windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return "", "", "", err
	}
	defer token.Close()
	tokenUser, err := token.GetTokenUser()
	if err != nil {
		return "", "", "", err
	}
	uid = tokenUser.User.Sid.String()
	return start, executable, uid, nil
}
