//go:build !unix

package atomicfile

import "os"

// flockExclusive is a no-op on platforms without flock (e.g. Windows).
func flockExclusive(_ *os.File) error { return nil }

// flockUnlock is a no-op on platforms without flock (e.g. Windows).
func flockUnlock(_ *os.File) error { return nil }
