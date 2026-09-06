//go:build !linux

package server

import "net"

// validatePeerUser is a portable fallback. Owner-only directory and socket
// permissions remain the access control on platforms without Linux SO_PEERCRED.
func validatePeerUser(net.Conn) error { return nil }
