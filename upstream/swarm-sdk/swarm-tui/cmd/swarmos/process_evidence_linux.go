//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func readProcessEvidence(pid int) (start, executable, uid string, err error) {
	if pid <= 0 {
		return "", "", "", fmt.Errorf("invalid pid %d", pid)
	}
	statPath := filepath.Join("/proc", strconv.Itoa(pid), "stat")
	data, err := os.ReadFile(statPath)
	if err != nil {
		return "", "", "", err
	}
	closeParen := strings.LastIndexByte(string(data), ')')
	if closeParen < 0 {
		return "", "", "", fmt.Errorf("malformed %s", statPath)
	}
	fields := strings.Fields(string(data[closeParen+1:]))
	if len(fields) < 20 {
		return "", "", "", fmt.Errorf("malformed %s: only %d fields after comm", statPath, len(fields))
	}
	start = fields[19] // proc(5) field 22; fields starts at field 3.
	executable, err = os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		return "", "", "", err
	}
	var st syscall.Stat_t
	if err := syscall.Stat(filepath.Join("/proc", strconv.Itoa(pid)), &st); err != nil {
		return "", "", "", err
	}
	uid = strconv.FormatUint(uint64(st.Uid), 10)
	return start, executable, uid, nil
}
