//go:build linux

package launch

import (
	"os/exec"
	"testing"
)

func TestConfigureProcessDetachesProcessGroup(t *testing.T) {
	cmd := exec.Command("true")

	configureProcess(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("expected SysProcAttr to be configured")
	}
	if !cmd.SysProcAttr.Setpgid {
		t.Fatal("expected Setpgid to be enabled")
	}
	if cmd.SysProcAttr.Pgid != 0 {
		t.Fatalf("expected Pgid 0, got %d", cmd.SysProcAttr.Pgid)
	}
}
