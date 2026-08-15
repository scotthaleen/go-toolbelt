//go:build linux || darwin || windows

package localgateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/scotthaleen/go-app"
)

func TestGatewayServesAndStops(t *testing.T) {
	endpoint := testEndpoint(t)
	server := New(DefaultConfig(endpoint), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != nil {
			t.Errorf("request context error = %v", r.Context().Err())
		}
		_, _ = io.WriteString(w, "ok")
	}))
	ctx, requested := gatewayContext(t)
	if err := server.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	client := NewClient(endpoint, ClientConfig{})
	response, err := client.Get(BaseURL + "/health")
	if err != nil {
		t.Fatalf("GET gateway: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "ok" {
		t.Fatalf("response = %q, err = %v", body, err)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := server.Stop(stopCtx); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
	select {
	case <-requested:
		t.Fatal("normal stop requested application shutdown")
	default:
	}
	if !endpointRemoved(endpoint) {
		t.Fatal("endpoint remains after stop")
	}
}

func TestGatewayRequiresEndpoint(t *testing.T) {
	server := New(Config{}, nil)
	ctx, _ := gatewayContext(t)
	if err := server.Start(ctx); err == nil {
		t.Fatal("Start() accepted empty endpoint")
	}
}

func TestGatewayStopRetriesAfterDeadline(t *testing.T) {
	endpoint := testEndpoint(t)
	started := make(chan struct{})
	release := make(chan struct{})
	server := New(DefaultConfig(endpoint), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "late")
	}))
	ctx, _ := gatewayContext(t)
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	requestDone := make(chan error, 1)
	go func() {
		response, err := NewClient(endpoint, ClientConfig{}).Get(BaseURL)
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	if err := server.Stop(stopCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Stop() error = %v", err)
	}
	cancel()
	close(release)
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("forced-close request did not return")
	}
	if err := server.Stop(context.Background()); err != nil {
		t.Fatalf("retry Stop() error = %v", err)
	}
}

func gatewayContext(t *testing.T) (context.Context, <-chan struct{}) {
	t.Helper()
	runtime, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	requested := make(chan struct{}, 1)
	ctx := app.Register(context.Background(), app.RuntimeContext{Context: runtime})
	ctx = app.Register(ctx, app.RequestShutdownFunc(func() {
		select {
		case requested <- struct{}{}:
		default:
		}
	}))
	return ctx, requested
}
