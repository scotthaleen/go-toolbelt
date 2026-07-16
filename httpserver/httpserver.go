package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
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

// Server owns a standard-library HTTP server and its listener.
type Server struct {
	cfg       Config
	handler   http.Handler
	listener  net.Listener
	server    *http.Server
	serveDone chan struct{}
}

// New constructs a server. An empty address defaults to :8080.
func New(cfg Config, handler http.Handler, opts ...Option) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	if handler == nil {
		handler = http.NotFoundHandler()
	}
	server := &Server{cfg: cfg, handler: handler}
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

	go func() {
		defer close(serveDone)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.ErrorContext(runtime, "http server failed", "reason", err)
			requestShutdown()
		}
	}()

	slog.InfoContext(ctx, "http server listening", "addr", listener.Addr().String())
	return nil
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}
