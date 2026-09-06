//go:build linux

package server

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

// validatePeerUser restricts the local control plane to the effective user that
// owns the server process. Socket permissions are still enforced separately.
func validatePeerUser(conn net.Conn) error {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("peer credentials require a Unix connection")
	}
	rawConn, err := unixConn.SyscallConn()
	if err != nil {
		return fmt.Errorf("get raw Unix connection: %w", err)
	}
	var (
		credential *syscall.Ucred
		socketErr  error
	)
	if err := rawConn.Control(func(fd uintptr) {
		credential, socketErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return fmt.Errorf("inspect peer credentials: %w", err)
	}
	if socketErr != nil {
		return fmt.Errorf("inspect peer credentials: %w", socketErr)
	}
	return checkPeerCredentialUID(credential, uint32(os.Geteuid()))
}

// checkPeerCredentialUID validates an already-retrieved SO_PEERCRED
// credential against the expected (server) effective UID. It is split out
// from validatePeerUser so the same-user/different-user decision can be
// unit tested directly with synthetic *syscall.Ucred values instead of
// requiring two distinct real OS users in the test environment, which is
// rarely available in CI and never available inside an unprivileged
// sandbox. A nil credential (the kernel can return one for a peer that
// disconnected mid-handshake, or on a socket type where SO_PEERCRED is not
// populated) is treated the same as a mismatched UID: fail closed rather
// than treat "unknown" as "trusted".
func checkPeerCredentialUID(credential *syscall.Ucred, expectedUID uint32) error {
	if credential == nil {
		return fmt.Errorf("control socket presented no peer credential")
	}
	if credential.Uid != expectedUID {
		return fmt.Errorf("control socket peer uid does not match server uid")
	}
	return nil
}
