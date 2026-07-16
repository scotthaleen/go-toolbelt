package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/scotthaleen/go-app"
)

// Config configures the listener address and HTTP server timeouts.
type Config struct {
	Addr              string
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

// Option customizes a Server.
type Option func(*Server)

// WithListener supplies the listener used by the server. It is useful for
// socket activation and deterministic tests.
func WithListener(listener net.Listener) Option {
	return func(server *Server) {
		server.listener = listener
	}
}

// WithLogger sets the logger used for server lifecycle messages. A nil logger
// is ignored.
func WithLogger(logger *slog.Logger) Option {
	return func(server *Server) {
		if logger != nil {
			server.logger = logger
		}
	}
}

// Server owns a standard-library HTTP server and its listener.
type Server struct {
	cfg       Config
	handler   http.Handler
	listener  net.Listener
	server    *http.Server
	serveDone chan struct{}
	logger    *slog.Logger
	errMu     sync.Mutex
	serveErr  error
}

// New constructs a server. An empty address defaults to :8080.
func New(cfg Config, handler http.Handler, opts ...Option) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	if handler == nil {
		handler = http.NotFoundHandler()
	}
	server := &Server{cfg: cfg, handler: handler, logger: slog.Default()}
	for _, opt := range opts {
		opt(server)
	}
	return server
}

// Component returns the server's go-app lifecycle component.
func (s *Server) Component() *app.Component {
	return app.NewComponent(
		app.WithName("http server"),
		app.WithOnStart(s.Start),
		app.WithOnStop(s.Stop),
	)
}

// Addr returns the configured listener address. It returns nil before startup
// unless the server was constructed with WithListener.
func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Start binds the listener and starts serving in the background.
func (s *Server) Start(ctx context.Context) error {
	if s.server != nil {
		return errors.New("http server already started")
	}
	runtime := app.MustGet[app.RuntimeContext](ctx)
	requestShutdown := app.MustGet[app.RequestShutdownFunc](ctx)

	listener := s.listener
	if listener == nil {
		var err error
		listener, err = net.Listen("tcp", s.cfg.Addr)
		if err != nil {
			return fmt.Errorf("listen for HTTP: %w", err)
		}
		s.listener = listener
	}

	server := &http.Server{
		Handler:           s.handler,
		ReadTimeout:       s.cfg.ReadTimeout,
		ReadHeaderTimeout: s.cfg.ReadHeaderTimeout,
		WriteTimeout:      s.cfg.WriteTimeout,
		IdleTimeout:       s.cfg.IdleTimeout,
	}
	s.server = server
	serveDone := make(chan struct{})
	s.serveDone = serveDone
	logger := s.logger

	go func() {
		defer close(serveDone)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.errMu.Lock()
			s.serveErr = err
			s.errMu.Unlock()
			logger.ErrorContext(runtime, "http server failed", "reason", err)
			requestShutdown()
		}
	}()

	logger.InfoContext(ctx, "http server listening", "addr", listener.Addr().String())
	return nil
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	shutdownErr := s.server.Shutdown(ctx)
	var closeErr error
	if shutdownErr != nil {
		closeErr = s.server.Close()
	}
	if s.serveDone != nil {
		select {
		case <-s.serveDone:
		case <-ctx.Done():
			shutdownErr = errors.Join(shutdownErr, ctx.Err())
		}
	}
	s.errMu.Lock()
	serveErr := s.serveErr
	s.errMu.Unlock()
	return errors.Join(shutdownErr, closeErr, serveErr)
}
