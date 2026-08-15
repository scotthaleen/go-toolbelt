//go:build linux

package localgateway

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func peerAllowed(conn net.Conn) (bool, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return false, fmt.Errorf("unexpected local gateway connection type %T", conn)
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return false, err
	}
	var credential *unix.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credential, socketErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return false, err
	}
	if socketErr != nil {
		return false, socketErr
	}
	return credential != nil && credential.Uid == uint32(os.Geteuid()), nil
}
