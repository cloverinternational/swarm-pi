//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package server

// Unix control sockets are unavailable on these platforms, so there is no
// platform-specific refused-connection error that proves a socket is stale.
func isStaleSocketError(error) bool { return false }
