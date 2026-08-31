package chrome

import "os/exec"

type execCommandRunner struct{}

func (execCommandRunner) Start(executable string, args ...string) error {
	command := exec.Command(executable, args...)
	if err := command.Start(); err != nil {
		return err
	}
	// Chrome may hand the request to an existing browser process and exit. Reap
	// that child asynchronously, but never retain, signal, or otherwise use its
	// PID as browser-window ownership.
	go func() {
		_ = command.Wait()
	}()
	return nil
}
