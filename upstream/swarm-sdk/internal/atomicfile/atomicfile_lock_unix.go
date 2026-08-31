//go:build unix

package atomicfile

import (
	"os"
	"syscall"
)

// flockExclusive takes an exclusive (LOCK_EX) advisory lock on f.
func flockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

// flockUnlock releases the advisory lock on f.
func flockUnlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
