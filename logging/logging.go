package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/term"
)

type requestIDContextKey struct{}

var requestIDKey requestIDContextKey

type Config struct {
	Verbosity int
	Output    io.Writer
	Format    Format
	AddSource bool
}

type Format string

const (
	FormatAuto Format = "auto"
	FormatText Format = "text"
	FormatJSON Format = "json"
)

type ContextHandler struct {
	next slog.Handler
}

func LevelFromVerbosity(v int) slog.Level {
	switch {
	case v <= 0:
		return slog.LevelWarn
	case v == 1:
		return slog.LevelInfo
	case v == 2:
		return slog.LevelDebug
	default:
		return slog.Level(-8)
	}
}

func NewLogger(cfg Config) *slog.Logger {
	return slog.New(NewHandler(cfg))
}

func NewHandler(cfg Config) slog.Handler {
	out := cfg.Output
	if out == nil {
		out = os.Stdout
	}

	opts := &slog.HandlerOptions{
		Level:     LevelFromVerbosity(cfg.Verbosity),
		AddSource: cfg.AddSource,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.String(a.Key, a.Value.Time().UTC().Format(time.RFC3339Nano))
			}
			if a.Key == slog.SourceKey {
				if source, ok := a.Value.Any().(*slog.Source); ok && source != nil {
					source.File = filepath.Base(source.File)
				}
			}
			return a
		},
	}

	var handler slog.Handler
	switch cfg.Format {
	case FormatJSON:
		handler = slog.NewJSONHandler(out, opts)
	case FormatText:
		handler = slog.NewTextHandler(out, opts)
	case FormatAuto, "":
		if file, ok := out.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
			handler = slog.NewTextHandler(out, opts)
		} else {
			handler = slog.NewJSONHandler(out, opts)
		}
	default:
		handler = slog.NewTextHandler(out, opts)
	}
	return ContextHandler{next: handler}
}

func Setup(cfg Config) *slog.Logger {
	logger := NewLogger(cfg)
	slog.SetDefault(logger)
	return logger
}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

func RequestIDFromContext(ctx context.Context) (string, bool) {
	requestID, ok := ctx.Value(requestIDKey).(string)
	return requestID, ok && requestID != ""
}

func (h ContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if requestID, ok := RequestIDFromContext(ctx); ok {
		r.Add("request_id", slog.StringValue(requestID))
	}
	return h.next.Handle(ctx, r)
}

func (h ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return ContextHandler{next: h.next.WithAttrs(attrs)}
}

func (h ContextHandler) WithGroup(name string) slog.Handler {
	return ContextHandler{next: h.next.WithGroup(name)}
}
