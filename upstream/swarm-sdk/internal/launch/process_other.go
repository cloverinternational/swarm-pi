//go:build !linux

package launch

import "os/exec"

func configureProcess(_ *exec.Cmd) {}

func releaseProcess(_ *exec.Cmd) {}
