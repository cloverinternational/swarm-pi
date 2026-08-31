//go:build !unix && !windows

package runtime

import "errors"

func osWriteFile(string) error {
	return errors.New("secure host locking unsupported")
}

func makeSymlink(string, string) error {
	return errors.New("secure host locking unsupported")
}
