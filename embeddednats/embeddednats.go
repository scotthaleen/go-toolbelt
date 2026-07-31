package embeddednats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	server "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/scotthaleen/go-app"
)

const defaultReadyTimeout = 10 * time.Second

// Config configures an embedded NATS server.
//
// When Options is nil, the server listens on a random loopback port with
// JetStream enabled. When JetStream is enabled without a StoreDir, Server uses
// a private temporary directory and removes it after shutdown.
type Config struct {
	Options      *server.Options
	ReadyTimeout time.Duration
}

// DefaultOptions returns a new NATS server configuration that listens on a
// random loopback port and enables JetStream.
func DefaultOptions() *server.Options {
	return &server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		NoLog:     true,
		NoSigs:    true,
	}
}

// Option customizes a Server.
type Option func(*Server)

// WithLogger sets the logger used for lifecycle messages. A nil logger is
// ignored. It does not configure the NATS server's protocol logger.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Server) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// Server owns an embedded NATS server lifecycle.
type Server struct {
	options      *server.Options
	readyTimeout time.Duration
	logger       *slog.Logger

	mu       sync.RWMutex
	starting bool
	running  *runningServer
}

type runningServer struct {
	server   *server.Server
	done     chan struct{}
	tempDir  string
	stopOnce sync.Once
	stopErr  error
	stopping bool
}

// New constructs an embedded NATS server. Call Start, or register the returned
// value as a managed go-app component, before connecting clients.
func New(cfg Config, opts ...Option) *Server {
	natsOptions := cfg.Options
	if natsOptions == nil {
		natsOptions = DefaultOptions()
	}
	readyTimeout := cfg.ReadyTimeout
	if readyTimeout <= 0 {
		readyTimeout = defaultReadyTimeout
	}
	s := &Server{
		options:      natsOptions.Clone(),
		readyTimeout: readyTimeout,
		logger:       slog.Default(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Component returns the server's go-app lifecycle component.
func (s *Server) Component() *app.Component {
	return app.NewComponent(
		app.WithName("embedded nats"),
		app.WithOnStart(s.Start),
		app.WithOnStop(s.Stop),
	)
}

// NATSServer returns the running underlying NATS server, or nil before startup
// and after shutdown.
func (s *Server) NATSServer() *server.Server {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.running == nil {
		return nil
	}
	return s.running.server
}

// ClientURL returns the running server's normal TCP client URL. It returns an
// empty string before startup and for servers configured with DontListen.
func (s *Server) ClientURL() string {
	if s.options.DontListen {
		return ""
	}
	ns := s.NATSServer()
	if ns == nil {
		return ""
	}
	return ns.ClientURL()
}

// Connect creates a normal TCP client connection. Code using this path can
// later connect to a remote NATS cluster by changing only its configured URL.
func (s *Server) Connect(opts ...nats.Option) (*nats.Conn, error) {
	url := s.ClientURL()
	if url == "" {
		return nil, errors.New("embedded nats is not listening")
	}
	return nats.Connect(url, opts...)
}

// ConnectInProcess creates a client connection without opening a network
// socket. Authentication still applies, but this path does not exercise TCP or
// TLS transport behavior.
func (s *Server) ConnectInProcess(opts ...nats.Option) (*nats.Conn, error) {
	ns := s.NATSServer()
	if ns == nil {
		return nil, errors.New("embedded nats is not running")
	}
	return nats.Connect("", append([]nats.Option{nats.InProcessServer(ns)}, opts...)...)
}

// Start starts the NATS server and waits for it to accept connections.
func (s *Server) Start(ctx context.Context) error {
	requestShutdown := app.MustGet[app.RequestShutdownFunc](ctx)

	s.mu.Lock()
	if s.starting || s.running != nil {
		s.mu.Unlock()
		return errors.New("embedded nats already started")
	}
	s.starting = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.starting = false
		s.mu.Unlock()
	}()

	options := s.options.Clone()
	tempDir := ""
	if options.JetStream && options.StoreDir == "" {
		var err error
		tempDir, err = os.MkdirTemp("", "go-toolbelt-nats-*")
		if err != nil {
			return fmt.Errorf("create temporary JetStream store: %w", err)
		}
		options.StoreDir = tempDir
	}

	ns, err := server.NewServer(options)
	if err != nil {
		return errors.Join(fmt.Errorf("create embedded NATS server: %w", err), removeTempDir(tempDir))
	}
	ns.Start()
	if !ns.ReadyForConnections(s.readyTimeout) {
		ns.Shutdown()
		ns.WaitForShutdown()
		return errors.Join(
			fmt.Errorf("embedded NATS server was not ready within %s", s.readyTimeout),
			removeTempDir(tempDir),
		)
	}

	running := &runningServer{server: ns, done: make(chan struct{}), tempDir: tempDir}
	s.mu.Lock()
	s.running = running
	s.mu.Unlock()

	go s.waitForShutdown(running, requestShutdown)
	s.logger.InfoContext(ctx, "embedded NATS listening", "url", ns.ClientURL(), "jetstream", ns.JetStreamEnabled())
	return nil
}

func (s *Server) waitForShutdown(running *runningServer, requestShutdown app.RequestShutdownFunc) {
	running.server.WaitForShutdown()
	running.stopErr = removeTempDir(running.tempDir)
	close(running.done)

	s.mu.RLock()
	unexpected := s.running == running && !running.stopping
	s.mu.RUnlock()
	if unexpected {
		s.logger.Error("embedded NATS stopped unexpectedly")
		requestShutdown()
	}
}

// Stop shuts down the embedded NATS server and waits for temporary storage to
// be removed. Client connections should be drained before this component stops.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	running := s.running
	if running == nil {
		s.mu.Unlock()
		return nil
	}
	running.stopping = true
	running.stopOnce.Do(func() {
		go running.server.Shutdown()
	})
	s.mu.Unlock()

	select {
	case <-running.done:
		s.mu.Lock()
		if s.running == running {
			s.running = nil
		}
		s.mu.Unlock()
		return errors.Join(running.stopErr, ctx.Err())
	case <-ctx.Done():
		return ctx.Err()
	}
}

func removeTempDir(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove temporary JetStream store: %w", err)
	}
	return nil
}
