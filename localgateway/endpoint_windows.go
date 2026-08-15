//go:build windows

package localgateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func listenLocal(endpoint string, inputBufferBytes, outputBufferBytes int32) (net.Listener, func() error, error) {
	if !strings.HasPrefix(strings.ToLower(endpoint), `\\.\pipe\`) {
		return nil, nil, errors.New(`Windows gateway endpoint must start with \\.\pipe\`)
	}
	descriptor, err := currentUserSecurityDescriptor()
	if err != nil {
		return nil, nil, err
	}
	listener, err := winio.ListenPipe(endpoint, &winio.PipeConfig{
		SecurityDescriptor: descriptor,
		MessageMode:        false,
		InputBufferSize:    inputBufferBytes,
		OutputBufferSize:   outputBufferBytes,
	})
	return listener, nil, err
}

func dialLocal(ctx context.Context, endpoint string) (net.Conn, error) {
	if !strings.HasPrefix(strings.ToLower(endpoint), `\\.\pipe\`) {
		return nil, errors.New(`Windows gateway endpoint must start with \\.\pipe\`)
	}
	conn, err := winio.DialPipeContext(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	if err := verifyCurrentUserPipeServer(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
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
	if err != nil || !serverUser.Equals(currentUser) {
		return errors.New("Windows gateway server belongs to another user")
	}
	return nil
}

func currentUserSecurityDescriptor() (string, error) {
	user, err := currentUserSID()
	if err != nil {
		return "", err
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
