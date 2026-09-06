//go:build windows

package main

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestConfigureDetachedProcessStartsNewProcessGroup(t *testing.T) {
	cmd := exec.Command("test-command")

	configureDetachedProcess(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("configureDetachedProcess() left SysProcAttr nil")
	}
	if got := cmd.SysProcAttr.CreationFlags; got&syscall.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("configureDetachedProcess() CreationFlags = %#x, want CREATE_NEW_PROCESS_GROUP", got)
	}
}
