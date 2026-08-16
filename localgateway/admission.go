package localgateway

import (
	"net"
	"sync"
)

type admissionListener struct {
	net.Listener
	max    int
	mu     sync.Mutex
	cond   *sync.Cond
	active int
	closed bool
	onWait func()
}

func newAdmissionListener(listener net.Listener, max int) *admissionListener {
	l := &admissionListener{Listener: listener, max: max}
	l.cond = sync.NewCond(&l.mu)
	return l
}

func (l *admissionListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	for l.active >= l.max && !l.closed {
		if l.onWait != nil {
			l.onWait()
		}
		l.cond.Wait()
	}
	if l.closed {
		l.mu.Unlock()
		return nil, net.ErrClosed
	}
	l.active++
	l.mu.Unlock()

	conn, err := l.Listener.Accept()
	if err != nil {
		l.release()
		return nil, err
	}

	l.mu.Lock()
	if l.closed {
		l.active--
		l.cond.Signal()
		l.mu.Unlock()
		_ = conn.Close()
		return nil, net.ErrClosed
	}
	l.mu.Unlock()

	return &admittedConn{Conn: conn, release: l.release}, nil
}

func (l *admissionListener) Close() error {
	l.mu.Lock()
	if !l.closed {
		l.closed = true
		l.cond.Broadcast()
	}
	l.mu.Unlock()
	return l.Listener.Close()
}

func (l *admissionListener) release() {
	l.mu.Lock()
	l.active--
	l.cond.Signal()
	l.mu.Unlock()
}

type admittedConn struct {
	net.Conn
	release func()
	once    sync.Once
}

func (c *admittedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
