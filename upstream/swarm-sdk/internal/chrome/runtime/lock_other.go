//go:build !unix && !windows

package runtime

import (
	"fmt"
	"io/fs"
	"os"
)

func acquirePlatformLock(string) (*os.File, func(*os.File) error, error) {
	return nil, nil, fmt.Errorf("%w: host locking is unsupported on this platform", ErrInsecureStorage)
}

func validatePlatformDirectory(string, fs.FileInfo) error {
	return fmt.Errorf("%w: secure storage is unsupported on this platform", ErrInsecureStorage)
}

func validatePlatformFile(string) error {
	return fmt.Errorf("%w: secure storage is unsupported on this platform", ErrInsecureStorage)
}

func readPlatformFile(string) ([]byte, error) {
	return nil, fmt.Errorf("%w: secure storage is unsupported on this platform", ErrInsecureStorage)
}

func protectPlatformFile(string) error {
	return fmt.Errorf("%w: secure storage is unsupported on this platform", ErrInsecureStorage)
}
