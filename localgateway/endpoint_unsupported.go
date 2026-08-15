//go:build !linux && !darwin && !windows

package localgateway

import (
	"context"
	"errors"
	"net"
)

var errUnsupported = errors.New("local gateway is unsupported on this platform")

func listenLocal(string, int32, int32) (net.Listener, func() error, error) {
	return nil, nil, errUnsupported
}
func dialLocal(context.Context, string) (net.Conn, error) { return nil, errUnsupported }
