//go:build darwin

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
	var credential *unix.Xucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credential, socketErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return false, err
	}
	if socketErr != nil {
		return false, socketErr
	}
	return credential != nil && credential.Uid == uint32(os.Geteuid()), nil
}
