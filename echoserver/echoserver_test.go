package echoserver

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/scotthaleen/go-app"
)

func TestAppRunReturnsServeFailure(t *testing.T) {
	wantErr := errors.New("accept failed")
	router := NewRouter()
	server := New(Config{})
	server.listener = errorListener{err: wantErr}
	application := app.New(
		context.Background(),
		app.WithSignalHandling(false),
		app.WithSequentialStartup(app.Registered(router), app.Managed(server)),
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

type errorListener struct {
	err error
}

func (l errorListener) Accept() (net.Conn, error) { return nil, l.err }
func (l errorListener) Close() error              { return nil }
func (l errorListener) Addr() net.Addr            { return testAddr("error-listener") }

type testAddr string

func (a testAddr) Network() string { return "test" }
func (a testAddr) String() string  { return string(a) }
