//go:build !windows

package main

import (
	"os/exec"
	"testing"
)

func TestConfigureDetachedProcessStartsNewSession(t *testing.T) {
	cmd := exec.Command("test-command")

	configureDetachedProcess(cmd)

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatalf("configureDetachedProcess() SysProcAttr = %#v, want Setsid", cmd.SysProcAttr)
	}
}
