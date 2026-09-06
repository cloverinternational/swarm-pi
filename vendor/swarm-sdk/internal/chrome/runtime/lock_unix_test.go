//go:build unix

package runtime

import "os"

func osWriteFile(path string) error {
	return os.WriteFile(path, []byte("x"), 0o600)
}

func makeSymlink(target, link string) error {
	return os.Symlink(target, link)
}
