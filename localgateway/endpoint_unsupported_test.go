//go:build !linux && !darwin && !windows

package localgateway

import (
	"context"
	"testing"

	"github.com/scotthaleen/go-app"
)

func testEndpoint(*testing.T) string { return "unsupported" }
func endpointRemoved(string) bool    { return true }

func TestUnsupportedPlatformRejectsStart(t *testing.T) {
	server := New(DefaultConfig("unsupported"), nil)
	ctx, _ := gatewayContextUnsupported(t)
	if err := server.Start(ctx); err == nil {
		t.Fatal("unsupported platform accepted gateway startup")
	}
}

func gatewayContextUnsupported(t *testing.T) (context.Context, <-chan struct{}) {
	t.Helper()
	runtime, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	requested := make(chan struct{}, 1)
	ctx := app.Register(context.Background(), app.RuntimeContext{Context: runtime})
	ctx = app.Register(ctx, app.RequestShutdownFunc(func() { requested <- struct{}{} }))
	return ctx, requested
}
