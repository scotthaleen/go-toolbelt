package echoserver

import (
	"context"
	"net"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/scotthaleen/go-app"
	"github.com/scotthaleen/go-toolbelt/httpserver"
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
	cfg       Config
	listener  net.Listener
	transport *httpserver.Server
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
	opts := make([]httpserver.Option, 0, 1)
	if s.listener != nil {
		opts = append(opts, httpserver.WithListener(s.listener))
	}
	s.transport = httpserver.New(httpserver.Config{
		Addr:              s.cfg.Addr,
		ReadTimeout:       s.cfg.ReadTimeout,
		ReadHeaderTimeout: s.cfg.ReadHeaderTimeout,
		WriteTimeout:      s.cfg.WriteTimeout,
		IdleTimeout:       s.cfg.IdleTimeout,
	}, router.Echo(), opts...)
	return s.transport.Start(ctx)
}

func (s *Server) Stop(ctx context.Context) error {
	if s.transport == nil {
		return nil
	}
	return s.transport.Stop(ctx)
}
