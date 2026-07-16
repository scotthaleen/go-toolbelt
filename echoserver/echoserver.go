package echoserver

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/scotthaleen/go-app"
)

type Config struct {
	Addr              string
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

type Router struct {
	echo *echo.Echo
}

func NewRouter() *Router {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	return &Router{echo: e}
}

func (r *Router) Component() *app.Component {
	return app.NewComponent(
		app.WithName("echo router"),
		app.WithOnStart(r.Start),
	)
}

func (r *Router) Echo() *echo.Echo { return r.echo }

func (r *Router) Group(prefix string, middleware ...echo.MiddlewareFunc) *echo.Group {
	return r.echo.Group(prefix, middleware...)
}

func (r *Router) Start(ctx context.Context) error {
	return nil
}

type Server struct {
	cfg    Config
	server *http.Server
}

func New(cfg Config) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	return &Server{cfg: cfg}
}

func (s *Server) Component() *app.Component {
	return app.NewComponent(
		app.WithName("echo server"),
		app.WithOnStart(s.Start),
		app.WithOnStop(s.Stop),
	)
}

func (s *Server) Start(ctx context.Context) error {
	router := app.MustGet[*Router](ctx)
	runtime := app.MustGet[app.RuntimeContext](ctx)
	requestShutdown := app.MustGet[app.RequestShutdownFunc](ctx)

	listener, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return err
	}

	s.server = &http.Server{
		Handler:           router.Echo(),
		ReadTimeout:       s.cfg.ReadTimeout,
		ReadHeaderTimeout: s.cfg.ReadHeaderTimeout,
		WriteTimeout:      s.cfg.WriteTimeout,
		IdleTimeout:       s.cfg.IdleTimeout,
	}

	go func() {
		if err := s.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.ErrorContext(runtime, "echo server failed", "reason", err)
			requestShutdown()
		}
	}()

	slog.InfoContext(ctx, "echo server listening", "addr", listener.Addr().String())
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}
