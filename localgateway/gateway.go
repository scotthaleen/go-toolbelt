package localgateway

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

const (
	DefaultPipeBufferBytes = 64 << 10
	MaxPipeBufferBytes     = 16 << 20
	DefaultMaxConnections  = 64
	MaxConnections         = 4096
	BaseURL                = "http://local.gateway"
)

// Config configures a protected local HTTP gateway.
type Config struct {
	Endpoint              string
	Name                  string
	ReadTimeout           time.Duration
	ReadHeaderTimeout     time.Duration
	WriteTimeout          time.Duration
	IdleTimeout           time.Duration
	MaxHeaderBytes        int
	PipeInputBufferBytes  int32
	PipeOutputBufferBytes int32
}

// DefaultConfig returns conservative HTTP and named-pipe defaults.
func DefaultConfig(endpoint string) Config {
	return Config{
		Endpoint:              endpoint,
		Name:                  "local HTTP gateway",
		ReadTimeout:           10 * time.Second,
		ReadHeaderTimeout:     2 * time.Second,
		IdleTimeout:           30 * time.Second,
		MaxHeaderBytes:        16 << 10,
		PipeInputBufferBytes:  DefaultPipeBufferBytes,
		PipeOutputBufferBytes: DefaultPipeBufferBytes,
	}
}

// Option customizes a Server.
type Option func(*Server)

// WithLogger sets the logger used for gateway lifecycle messages.
func WithLogger(logger *slog.Logger) Option {
	return func(server *Server) {
		if logger != nil {
			server.logger = logger
		}
	}
}

// WithMaxConnections bounds transport connections accepted by the gateway.
// Values outside 1 through MaxConnections use DefaultMaxConnections.
func WithMaxConnections(max int) Option {
	return func(server *Server) {
		server.maxConnections = normalizeMaxConnections(max)
	}
}

// Server owns a protected local listener and standard-library HTTP server.
type Server struct {
	cfg            Config
	handler        http.Handler
	logger         *slog.Logger
	listener       net.Listener
	cleanup        func() error
	server         *http.Server
	serveDone      chan struct{}
	mu             sync.Mutex
	stopping       bool
	stopped        bool
	errMu          sync.Mutex
	serveErr       error
	handlers       handlerGate
	maxConnections int
}

// New constructs a local gateway. Endpoint validation occurs during startup.
func New(cfg Config, handler http.Handler, opts ...Option) *Server {
	defaults := DefaultConfig(cfg.Endpoint)
	if cfg.Name == "" {
		cfg.Name = defaults.Name
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = defaults.ReadTimeout
	}
	if cfg.ReadHeaderTimeout == 0 {
		cfg.ReadHeaderTimeout = defaults.ReadHeaderTimeout
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = defaults.IdleTimeout
	}
	if cfg.MaxHeaderBytes <= 0 {
		cfg.MaxHeaderBytes = defaults.MaxHeaderBytes
	}
	cfg.PipeInputBufferBytes = pipeBufferSize(cfg.PipeInputBufferBytes)
	cfg.PipeOutputBufferBytes = pipeBufferSize(cfg.PipeOutputBufferBytes)
	if handler == nil {
		handler = http.NotFoundHandler()
	}
	server := &Server{
		cfg:            cfg,
		handler:        handler,
		logger:         slog.Default(),
		maxConnections: DefaultMaxConnections,
	}
	for _, opt := range opts {
		opt(server)
	}
	return server
}

func normalizeMaxConnections(max int) int {
	if max <= 0 || max > MaxConnections {
		return DefaultMaxConnections
	}
	return max
}

func pipeBufferSize(value int32) int32 {
	if value <= 0 || value > MaxPipeBufferBytes {
		return DefaultPipeBufferBytes
	}
	return value
}

// Component returns the gateway's go-app lifecycle component.
func (s *Server) Component() *app.Component {
	return app.NewComponent(
		app.WithName(s.cfg.Name),
		app.WithOnStart(s.Start),
		app.WithOnStop(s.Stop),
	)
}

// Endpoint returns the configured platform endpoint.
func (s *Server) Endpoint() string { return s.cfg.Endpoint }

// Start creates the protected endpoint and starts serving in the background.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping || s.stopped {
		return fmt.Errorf("%s is stopped", s.cfg.Name)
	}
	if s.server != nil {
		return fmt.Errorf("%s already started", s.cfg.Name)
	}
	if s.cfg.Endpoint == "" {
		return errors.New("local gateway endpoint is required")
	}
	runtime := app.MustGet[app.RuntimeContext](ctx)
	requestShutdown := app.MustGet[app.RequestShutdownFunc](ctx)
	listener, cleanup, err := listenLocal(s.cfg.Endpoint, s.cfg.PipeInputBufferBytes, s.cfg.PipeOutputBufferBytes)
	if err != nil {
		return fmt.Errorf("listen for %s: %w", s.cfg.Name, err)
	}
	listener = newAdmissionListener(listener, s.maxConnections)
	s.listener = listener
	s.cleanup = cleanup
	server := &http.Server{
		Handler:           &gatedHandler{gate: &s.handlers, handler: s.handler},
		ReadTimeout:       s.cfg.ReadTimeout,
		ReadHeaderTimeout: s.cfg.ReadHeaderTimeout,
		WriteTimeout:      s.cfg.WriteTimeout,
		IdleTimeout:       s.cfg.IdleTimeout,
		MaxHeaderBytes:    s.cfg.MaxHeaderBytes,
		BaseContext: func(net.Listener) context.Context {
			return runtime
		},
	}
	s.server = server
	s.serveDone = make(chan struct{})
	go func() {
		defer close(s.serveDone)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.errMu.Lock()
			s.serveErr = err
			s.errMu.Unlock()
			s.logger.ErrorContext(runtime, s.cfg.Name+" failed", "reason", err)
			requestShutdown()
		}
	}()
	s.logger.DebugContext(ctx, s.cfg.Name+" listening")
	return nil
}

// Stop prevents new application handler entry, shuts down HTTP, waits for every
// entered handler to exit, and removes the Unix socket when applicable. A
// handler that ignores request cancellation can make Stop wait beyond ctx. Stop
// is safe to call more than once. An entered handler must not call or wait for
// Stop because Stop waits for entered handlers; request shutdown and return
// instead.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return nil
	}
	s.stopping = true
	s.handlers.stop()
	var result error
	if s.server != nil {
		if err := s.server.Shutdown(ctx); err != nil {
			result = errors.Join(result, err, s.server.Close())
		}
	}
	if s.listener != nil {
		if err := s.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			result = errors.Join(result, err)
		}
	}
	if s.serveDone != nil {
		<-s.serveDone
	}
	s.handlers.wait()
	if s.cleanup != nil {
		if err := s.cleanup(); err != nil {
			result = errors.Join(result, err)
		} else {
			s.cleanup = nil
		}
	}
	s.errMu.Lock()
	result = errors.Join(result, s.serveErr)
	s.errMu.Unlock()
	if result == nil {
		s.stopped = true
	}
	return result
}

type handlerGate struct {
	mu       sync.Mutex
	cond     *sync.Cond
	stopping bool
	active   int
}

func (g *handlerGate) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopping {
		return false
	}
	g.active++
	return true
}

func (g *handlerGate) leave() {
	g.mu.Lock()
	g.active--
	if g.active == 0 && g.cond != nil {
		g.cond.Broadcast()
	}
	g.mu.Unlock()
}

func (g *handlerGate) stop() {
	g.mu.Lock()
	g.stopping = true
	g.mu.Unlock()
}

func (g *handlerGate) wait() {
	g.mu.Lock()
	if g.cond == nil {
		g.cond = sync.NewCond(&g.mu)
	}
	for g.active != 0 {
		g.cond.Wait()
	}
	g.mu.Unlock()
}

type gatedHandler struct {
	gate    *handlerGate
	handler http.Handler
}

func (h *gatedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.gate.enter() {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	defer h.gate.leave()
	h.handler.ServeHTTP(w, r)
}

// ClientConfig configures the HTTP client bound to a local endpoint.
type ClientConfig struct {
	DialTimeout           time.Duration
	ResponseHeaderTimeout time.Duration
	IdleConnTimeout       time.Duration
	MaxIdleConns          int
}

// NewClient returns a standard HTTP client whose connections use the protected
// local endpoint. Requests should use BaseURL as their URL origin.
func NewClient(endpoint string, cfg ClientConfig) *http.Client {
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = time.Second
	}
	if cfg.IdleConnTimeout <= 0 {
		cfg.IdleConnTimeout = 30 * time.Second
	}
	if cfg.MaxIdleConns <= 0 {
		cfg.MaxIdleConns = 8
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialCtx, cancel := context.WithTimeout(ctx, cfg.DialTimeout)
			defer cancel()
			return dialLocal(dialCtx, endpoint)
		},
		ForceAttemptHTTP2:     false,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		IdleConnTimeout:       cfg.IdleConnTimeout,
		MaxIdleConns:          cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxIdleConns,
	}
	return &http.Client{Transport: transport}
}

// Dial opens and verifies one protected local connection.
func Dial(ctx context.Context, endpoint string) (net.Conn, error) {
	return dialLocal(ctx, endpoint)
}
