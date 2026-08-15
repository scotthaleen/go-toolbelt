//go:build linux || darwin

package localgateway

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
)

func TestGatewayRefusesUnsafeExistingEndpoint(t *testing.T) {
	endpoint := testEndpoint(t)
	if err := os.WriteFile(endpoint, []byte("do not remove"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := New(DefaultConfig(endpoint), nil)
	ctx, _ := gatewayContext(t)
	if err := server.Start(ctx); err == nil {
		t.Fatal("Start() accepted regular file endpoint")
	}
	if err := server.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() after failed Start() error = %v", err)
	}
	data, err := os.ReadFile(endpoint)
	if err != nil || string(data) != "do not remove" {
		t.Fatalf("existing endpoint changed: data=%q err=%v", data, err)
	}
}

func TestGatewayStopDoesNotUnlinkReplacementSocket(t *testing.T) {
	endpoint := testEndpoint(t)
	server := New(DefaultConfig(endpoint), nil)
	ctx, _ := gatewayContext(t)
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	moved := endpoint + ".moved"
	if err := os.Rename(endpoint, moved); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.Listen("unix", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if unix, ok := replacement.(*net.UnixListener); ok {
		unix.SetUnlinkOnClose(false)
	}
	t.Cleanup(func() {
		_ = replacement.Close()
		_ = os.Remove(endpoint)
		_ = os.Remove(moved)
	})
	if err := os.Chmod(endpoint, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := server.Stop(context.Background()); err == nil {
		t.Fatal("Stop() did not report replaced endpoint")
	}
	if err := validateSocket(endpoint); err != nil {
		t.Fatalf("replacement socket was removed: %v", err)
	}
}

func TestGatewayDoesNotDisplaceActiveEndpoint(t *testing.T) {
	endpoint := testEndpoint(t)
	first := New(DefaultConfig(endpoint), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "first")
	}))
	ctx, _ := gatewayContext(t)
	if err := first.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Stop(context.Background()) })

	second := New(DefaultConfig(endpoint), nil)
	if err := second.Start(ctx); err == nil {
		t.Fatal("second gateway displaced active endpoint")
	}
	if err := second.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
	response, err := NewClient(endpoint, ClientConfig{}).Get(BaseURL)
	if err != nil {
		t.Fatalf("first gateway unavailable: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "first" {
		t.Fatalf("first gateway response=%q err=%v", body, err)
	}
}
