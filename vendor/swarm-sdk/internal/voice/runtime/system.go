package runtime

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"syscall"
)

type OSFileOps struct{}

func (OSFileOps) MkdirAll(path string, mode os.FileMode) error { return os.MkdirAll(path, mode) }
func (OSFileOps) ReadFile(path string) ([]byte, error)         { return os.ReadFile(path) }
func (OSFileOps) WriteFile(path string, data []byte, mode os.FileMode) error {
	return os.WriteFile(path, data, mode)
}
func (OSFileOps) Rename(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }

// ExecRunner uses CommandContext so cancellation terminates the child process.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	if command.Name == "" {
		return CommandResult{}, errors.New("runtime: empty command")
	}
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	cmd.Dir = command.Dir
	cmd.Env = os.Environ()
	keys := make([]string, 0, len(command.Env))
	for key := range command.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cmd.Env = append(cmd.Env, key+"="+command.Env[key])
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if exitErr := new(exec.ExitError); errors.As(err, &exitErr) {
		result.Code = exitErr.ExitCode()
	}
	return result, err
}

type NetPortChecker struct{}

func (NetPortChecker) InUse(ctx context.Context, host string, port int) (bool, error) {
	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err == nil {
		_ = listener.Close()
		return false, nil
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true, nil
	}
	return false, err
}
