package localgateway

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

func TestAdmissionListenerReservesBeforeAcceptAndReusesSlot(t *testing.T) {
	base := newQueuedListener()
	listener := newAdmissionListener(base, 1)
	t.Cleanup(func() { _ = listener.Close() })

	client1, server1 := net.Pipe()
	defer client1.Close()
	base.results <- acceptResult{conn: server1}
	conn1, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	assertAcceptCalled(t, base)

	client2, server2 := net.Pipe()
	defer client2.Close()
	base.results <- acceptResult{conn: server2}
	waiting := make(chan struct{})
	listener.onWait = sync.OnceFunc(func() { close(waiting) })
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, _ := listener.Accept()
		accepted <- conn
	}()
	awaitSignal(t, waiting, "saturated Accept did not wait for a reservation")
	assertAcceptNotCalled(t, base)

	if err := conn1.Close(); err != nil {
		t.Fatal(err)
	}
	if err := conn1.Close(); err != nil {
		t.Fatal(err)
	}
	assertAcceptCalled(t, base)
	select {
	case conn2 := <-accepted:
		if conn2 == nil {
			t.Fatal("slot reuse returned no connection")
		}
		_ = conn2.Close()
	case <-time.After(time.Second):
		t.Fatal("released slot was not reused")
	}
}

func TestAdmissionListenerAcceptErrorReleasesReservation(t *testing.T) {
	base := newQueuedListener()
	listener := newAdmissionListener(base, 1)
	t.Cleanup(func() { _ = listener.Close() })
	wantErr := errors.New("accept failed")
	base.results <- acceptResult{err: wantErr}
	if _, err := listener.Accept(); !errors.Is(err, wantErr) {
		t.Fatalf("Accept() error = %v", err)
	}
	assertAcceptCalled(t, base)

	client, server := net.Pipe()
	defer client.Close()
	base.results <- acceptResult{conn: server}
	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("Accept() after error = %v", err)
	}
	assertAcceptCalled(t, base)
	_ = conn.Close()
}

func TestAdmissionListenerCloseUnblocksSaturatedAccept(t *testing.T) {
	base := newQueuedListener()
	listener := newAdmissionListener(base, 1)
	t.Cleanup(func() { _ = listener.Close() })

	client, server := net.Pipe()
	defer client.Close()
	base.results <- acceptResult{conn: server}
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	assertAcceptCalled(t, base)

	waiting := make(chan struct{})
	listener.onWait = sync.OnceFunc(func() { close(waiting) })
	result := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if conn != nil {
			_ = conn.Close()
		}
		result <- err
	}()
	awaitSignal(t, waiting, "saturated Accept did not wait for a reservation")
	assertAcceptNotCalled(t, base)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Accept() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock saturated Accept")
	}
	assertAcceptNotCalled(t, base)
	assertActiveReservations(t, listener, 1)
}

func TestAdmissionListenerCloseWhileUnderlyingAcceptInFlight(t *testing.T) {
	base := newQueuedListener()
	listener := newAdmissionListener(base, 1)
	t.Cleanup(func() { _ = listener.Close() })
	result := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if conn != nil {
			_ = conn.Close()
		}
		result <- err
	}()
	assertAcceptCalled(t, base)
	assertActiveReservations(t, listener, 1)

	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Accept() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("in-flight Accept did not return after Close")
	}
	assertActiveReservations(t, listener, 0)
}

func TestAdmissionListenerClosesConnectionReturnedDuringClose(t *testing.T) {
	base := newQueuedListener()
	listener := newAdmissionListener(base, 1)
	t.Cleanup(func() { _ = listener.Close() })
	client, server := net.Pipe()
	defer client.Close()
	base.results <- acceptResult{
		conn: server,
		beforeReturn: func() {
			if err := listener.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}
		},
	}

	result := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if conn != nil {
			_ = conn.Close()
		}
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Accept() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Accept did not return after concurrent Close")
	}
	assertActiveReservations(t, listener, 0)

	var buffer [1]byte
	if _, err := client.Read(buffer[:]); err == nil {
		t.Fatal("unpublished connection remained open")
	}
}

func assertAcceptCalled(t *testing.T, listener *queuedListener) {
	t.Helper()
	select {
	case <-listener.called:
	case <-time.After(time.Second):
		t.Fatal("underlying Accept was not called")
	}
}

func assertAcceptNotCalled(t *testing.T, listener *queuedListener) {
	t.Helper()
	select {
	case <-listener.called:
		t.Fatal("underlying Accept was called without a reservation")
	default:
	}
}

func assertActiveReservations(t *testing.T, listener *admissionListener, want int) {
	t.Helper()
	listener.mu.Lock()
	defer listener.mu.Unlock()
	if listener.active != want {
		t.Fatalf("active reservations = %d, want %d", listener.active, want)
	}
}

func awaitSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

type acceptResult struct {
	conn         net.Conn
	err          error
	beforeReturn func()
}

type queuedListener struct {
	results chan acceptResult
	called  chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func newQueuedListener() *queuedListener {
	return &queuedListener{
		results: make(chan acceptResult, 2),
		called:  make(chan struct{}, 2),
		closed:  make(chan struct{}),
	}
}

func (l *queuedListener) Accept() (net.Conn, error) {
	l.called <- struct{}{}
	select {
	case result := <-l.results:
		if result.beforeReturn != nil {
			result.beforeReturn()
		}
		return result.conn, result.err
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *queuedListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *queuedListener) Addr() net.Addr { return testAddr("local") }

type testAddr string

func (a testAddr) Network() string { return string(a) }
func (a testAddr) String() string  { return string(a) }
