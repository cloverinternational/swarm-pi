//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package server

import (
	"errors"
	"syscall"
)

func isStaleSocketError(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
