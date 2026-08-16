//go:build windows

package localgateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"strings"
	"syscall"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

var impersonateNamedPipeClient = windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateNamedPipeClient")

var errPipeClientRejected = errors.New("Windows gateway pipe client rejected")

type peerListener struct {
	net.Listener
	verify func(net.Conn) error
}

func (l peerListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		err = l.verify(conn)
		if err == nil {
			return conn, nil
		}
		_ = conn.Close()
		if !errors.Is(err, errPipeClientRejected) {
			return nil, err
		}
	}
}

func listenLocal(endpoint string, inputBufferBytes, outputBufferBytes int32) (net.Listener, func() error, error) {
	if !strings.HasPrefix(strings.ToLower(endpoint), `\\.\pipe\`) {
		return nil, nil, errors.New(`Windows gateway endpoint must start with \\.\pipe\`)
	}
	currentUser, err := currentUserSID()
	if err != nil {
		return nil, nil, err
	}
	descriptor, err := securityDescriptorForUser(currentUser)
	if err != nil {
		return nil, nil, err
	}
	listener, err := winio.ListenPipe(endpoint, &winio.PipeConfig{
		SecurityDescriptor: descriptor,
		MessageMode:        false,
		InputBufferSize:    inputBufferBytes,
		OutputBufferSize:   outputBufferBytes,
	})
	if err != nil {
		return nil, nil, err
	}
	return peerListener{
		Listener: listener,
		verify: func(conn net.Conn) error {
			return verifyCurrentUserPipeClient(conn, currentUser)
		},
	}, nil, nil
}

func dialLocal(ctx context.Context, endpoint string) (net.Conn, error) {
	if !strings.HasPrefix(strings.ToLower(endpoint), `\\.\pipe\`) {
		return nil, errors.New(`Windows gateway endpoint must start with \\.\pipe\`)
	}
	conn, err := winio.DialPipeAccessImpLevel(
		ctx,
		endpoint,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		winio.PipeImpLevelIdentification,
	)
	if err != nil {
		return nil, err
	}
	if err := verifyCurrentUserPipeServer(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func verifyCurrentUserPipeClient(conn net.Conn, expected *windows.SID) error {
	fd, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return errors.New("Windows gateway pipe handle is unavailable")
	}
	handle := windows.Handle(fd.Fd())
	if handle == 0 || handle == windows.InvalidHandle {
		return errors.New("Windows gateway pipe handle is invalid")
	}
	if expected == nil || !expected.IsValid() {
		return errors.New("current Windows user identity is unavailable")
	}

	runtime.LockOSThread()
	impersonated, _, callErr := impersonateNamedPipeClient.Call(uintptr(handle))
	if impersonated == 0 {
		runtime.UnlockOSThread()
		if callErr == syscall.Errno(0) {
			callErr = syscall.EINVAL
		}
		return fmt.Errorf("%w: identify client: %v", errPipeClientRejected, callErr)
	}
	defer revertPipeClientImpersonation()

	var token windows.Token
	if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &token); err != nil {
		return fmt.Errorf("%w: client token is unavailable", errPipeClientRejected)
	}
	defer token.Close()
	client, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("read Windows gateway client identity: %w", err)
	}
	if client.User.Sid == nil || !client.User.Sid.IsValid() {
		return errors.New("Windows gateway client identity is unavailable")
	}
	if !sameWindowsSID(client.User.Sid, expected) {
		return fmt.Errorf("%w: client belongs to another user", errPipeClientRejected)
	}
	return nil
}

func revertPipeClientImpersonation() {
	if err := windows.RevertToSelf(); err != nil {
		// Continuing could run unrelated goroutines on a client-impersonating thread.
		panic(fmt.Errorf("revert Windows gateway client impersonation: %w", err))
	}
	runtime.UnlockOSThread()
}

func sameWindowsSID(left, right *windows.SID) bool {
	return left != nil && right != nil && left.IsValid() && right.IsValid() && left.Equals(right)
}

func verifyCurrentUserPipeServer(conn net.Conn) error {
	fd, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return errors.New("Windows gateway pipe handle is unavailable")
	}
	descriptor, err := windows.GetSecurityInfo(windows.Handle(fd.Fd()), windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil || descriptor == nil || !descriptor.IsValid() {
		return errors.New("Windows gateway server security is unavailable")
	}
	serverUser, defaulted, err := descriptor.Owner()
	if err != nil || serverUser == nil || defaulted || !serverUser.IsValid() {
		return errors.New("Windows gateway server owner is unavailable")
	}
	currentUser, err := currentUserSID()
	if err != nil || !sameWindowsSID(serverUser, currentUser) {
		return errors.New("Windows gateway server belongs to another user")
	}
	return nil
}

func securityDescriptorForUser(user *windows.SID) (string, error) {
	if user == nil || !user.IsValid() {
		return "", errors.New("current Windows user identity is unavailable")
	}
	return fmt.Sprintf("O:%[1]sD:P(A;;GA;;;%[1]s)", user.String()), nil
}

func currentUserSID() (*windows.SID, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return nil, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	if user.User.Sid == nil {
		return nil, errors.New("current Windows user has no SID")
	}
	return user.User.Sid.Copy()
}
