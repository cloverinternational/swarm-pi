package mcp

import (
	"context"
	"os"
)

// osFS implements core.ConfigFileSystem using the os package.
type osFS struct{}

func (f *osFS) Read(ctx context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (f *osFS) Write(ctx context.Context, path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}

func (f *osFS) Exists(ctx context.Context, path string) (bool, error) {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func (f *osFS) MkdirAll(ctx context.Context, path string) error {
	return os.MkdirAll(path, 0755)
}

func (f *osFS) Rename(ctx context.Context, oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}

func (f *osFS) Remove(ctx context.Context, path string) error {
	return os.Remove(path)
}
