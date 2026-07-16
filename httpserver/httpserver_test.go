package httpserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/scotthaleen/go-app"
)

func TestServerServesAndStops(t *testing.T) {
	server := New(Config{Addr: "127.0.0.1:0"}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	ctx, requested := serverContext(t)

	if err := server.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if server.Addr() == nil || server.Addr().String() == "127.0.0.1:0" {
		t.Fatalf("Addr() = %v, want bound address", server.Addr())
	}

	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + server.Addr().String())
	if err != nil {
		t.Fatalf("GET server: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("response body = %q, want ok", body)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case <-server.serveDone:
	case <-time.After(time.Second):
		t.Fatal("Serve() did not return after Stop()")
	}
	select {
	case <-requested:
		t.Fatal("unexpected shutdown request during normal stop")
	default:
	}
	if _, err := client.Get("http://" + server.Addr().String()); err == nil {
		t.Fatal("GET after Stop() error = nil")
	}
}

func TestServerUsesInjectedListenerAndTimeouts(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	cfg := Config{
		ReadTimeout:       time.Second,
		ReadHeaderTimeout: 2 * time.Second,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       4 * time.Second,
	}
	server := New(cfg, http.NotFoundHandler(), WithListener(listener))
	ctx, _ := serverContext(t)

	if err := server.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if server.Addr().String() != listener.Addr().String() {
		t.Fatalf("Addr() = %v, want %v", server.Addr(), listener.Addr())
	}
	if server.server.ReadTimeout != cfg.ReadTimeout ||
		server.server.ReadHeaderTimeout != cfg.ReadHeaderTimeout ||
		server.server.WriteTimeout != cfg.WriteTimeout ||
		server.server.IdleTimeout != cfg.IdleTimeout {
		t.Fatalf("http.Server timeouts = %+v, want %+v", server.server, cfg)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if tcpListener, ok := listener.(*net.TCPListener); ok {
		_ = tcpListener.SetDeadline(time.Now().Add(time.Second))
	}
	if _, err := listener.Accept(); err == nil {
		t.Fatal("injected listener remains open after Stop()")
	}
}

func TestServerGracefulShutdownWaitsForRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := New(Config{Addr: "127.0.0.1:0"}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "complete")
	}))
	ctx, _ := serverContext(t)
	if err := server.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	requestDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: time.Second}
		response, err := client.Get("http://" + server.Addr().String())
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not reach handler")
	}

	stopDone := make(chan error, 1)
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { stopDone <- server.Stop(stopCtx) }()
	select {
	case err := <-stopDone:
		t.Fatalf("Stop() returned before request completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop() did not return after request completed")
	}
	if err := <-requestDone; err != nil {
		t.Fatalf("request error = %v", err)
	}
}

func TestServerOccupiedAddressFailsStart(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	server := New(Config{Addr: listener.Addr().String()}, http.NotFoundHandler())
	ctx, _ := serverContext(t)
	if err := server.Start(ctx); err == nil {
		t.Fatal("Start() error = nil, want bind error")
	}
	if server.server != nil {
		t.Fatal("server initialized after bind failure")
	}
	if err := server.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() after failed Start() error = %v", err)
	}
}

func TestServerRequestsShutdownOnServeFailure(t *testing.T) {
	wantErr := errors.New("accept failed")
	listener := &errorListener{err: wantErr}
	server := New(Config{}, http.NotFoundHandler(), WithListener(listener))
	ctx, requested := serverContext(t)

	if err := server.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("serve failure did not request shutdown")
	}
	if err := server.Stop(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Stop() error = %v, want errors.Is(_, %v)", err, wantErr)
	}
}

func TestAppRunReturnsServeFailure(t *testing.T) {
	wantErr := errors.New("accept failed")
	server := New(Config{}, http.NotFoundHandler(), WithListener(&errorListener{err: wantErr}))
	application := app.New(
		context.Background(),
		app.WithSignalHandling(false),
		app.WithSequentialStartup(app.Managed(server)),
	)

	done := make(chan error, 1)
	go func() { done <- application.Run() }()
	select {
	case err := <-done:
		if !errors.Is(err, wantErr) {
			t.Fatalf("Run() error = %v, want errors.Is(_, %v)", err, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return after serve failure")
	}
}

func TestServerUsesConfiguredLogger(t *testing.T) {
	var output strings.Builder
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	server := New(
		Config{Addr: "127.0.0.1:0"},
		http.NotFoundHandler(),
		WithLogger(logger),
	)
	ctx, _ := serverContext(t)
	if err := server.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if got := output.String(); !strings.Contains(got, `"msg":"http server listening"`) {
		t.Fatalf("log output = %q, want configured logger output", got)
	}
}

func TestStopBeforeStart(t *testing.T) {
	server := New(Config{}, http.NotFoundHandler())
	if err := server.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestServerRejectsDuplicateStart(t *testing.T) {
	server := New(Config{Addr: "127.0.0.1:0"}, http.NotFoundHandler())
	ctx, _ := serverContext(t)
	if err := server.Start(ctx); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	t.Cleanup(func() { _ = server.Stop(context.Background()) })
	firstServer := server.server
	firstAddr := server.Addr().String()
	if err := server.Start(ctx); err == nil {
		t.Fatal("second Start() error = nil, want already started error")
	}
	if server.server != firstServer || server.Addr().String() != firstAddr {
		t.Fatal("second Start() replaced running server state")
	}
}

func serverContext(t *testing.T) (context.Context, <-chan struct{}) {
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

type errorListener struct {
	err error
}

func (l *errorListener) Accept() (net.Conn, error) { return nil, l.err }
func (l *errorListener) Close() error              { return nil }
func (l *errorListener) Addr() net.Addr            { return testAddr("error-listener") }

type testAddr string

func (a testAddr) Network() string { return "test" }
func (a testAddr) String() string  { return string(a) }
