package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestContextHandlerAddsRequestID(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(t, Config{Output: &buf, Format: FormatJSON, Verbosity: 1})
	logger.InfoContext(WithRequestID(context.Background(), "req-123"), "hello")

	got := buf.String()
	if !strings.Contains(got, `"request_id":"req-123"`) {
		t.Fatalf("log output = %s, want request_id", got)
	}
}

func TestRequestIDFromContext(t *testing.T) {
	ctx := WithRequestID(context.Background(), "req-123")
	requestID, ok := RequestIDFromContext(ctx)
	if !ok || requestID != "req-123" {
		t.Fatalf("RequestIDFromContext() = %q, %v, want req-123, true", requestID, ok)
	}
	if _, ok := RequestIDFromContext(context.Background()); ok {
		t.Fatal("RequestIDFromContext() ok = true without request ID")
	}
}

func TestLevelFromVerbosity(t *testing.T) {
	if got := LevelFromVerbosity(-1); got != slog.LevelWarn {
		t.Fatalf("LevelFromVerbosity(-1) = %v, want warn", got)
	}
	if got := LevelFromVerbosity(0); got != slog.LevelWarn {
		t.Fatalf("LevelFromVerbosity(0) = %v, want warn", got)
	}
	if got := LevelFromVerbosity(1); got != slog.LevelInfo {
		t.Fatalf("LevelFromVerbosity(1) = %v, want info", got)
	}
	if got := LevelFromVerbosity(2); got != slog.LevelDebug {
		t.Fatalf("LevelFromVerbosity(2) = %v, want debug", got)
	}
	if got := LevelFromVerbosity(3); got != slog.Level(-8) {
		t.Fatalf("LevelFromVerbosity(3) = %v, want -8", got)
	}
}

func TestExplicitFormats(t *testing.T) {
	tests := []struct {
		name   string
		format Format
		want   string
	}{
		{name: "JSON", format: FormatJSON, want: `"msg":"hello"`},
		{name: "text", format: FormatText, want: `msg=hello`},
		{name: "Tint", format: FormatTint, want: `hello`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			newTestLogger(t, Config{Output: &buf, Format: tt.format, Verbosity: 1}).Info("hello")
			if got := buf.String(); !strings.Contains(got, tt.want) {
				t.Fatalf("log output = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAutoFormatSelection(t *testing.T) {
	original := isTerminal
	t.Cleanup(func() { isTerminal = original })

	var buf bytes.Buffer
	isTerminal = func(io.Writer) bool { return false }
	newTestLogger(t, Config{Output: &buf, Format: FormatAuto, Verbosity: 1}).Info("json-output")
	if got := buf.String(); !strings.Contains(got, `"msg":"json-output"`) {
		t.Fatalf("non-terminal output = %q, want JSON", got)
	}

	buf.Reset()
	isTerminal = func(io.Writer) bool { return true }
	newTestLogger(t, Config{Output: &buf, Format: FormatAuto, Verbosity: 1}).Info("tint-output")
	if got := buf.String(); !strings.Contains(got, "tint-output") || strings.Contains(got, `"msg"`) {
		t.Fatalf("terminal output = %q, want Tint", got)
	}
}

func TestReplaceAttr(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(t, Config{
		Output:    &buf,
		Format:    FormatJSON,
		Verbosity: 1,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == "secret" {
				return slog.String("secret", "REDACTED")
			}
			return attr
		},
	})
	logger.Info("hello", "secret", "value")
	if got := buf.String(); !strings.Contains(got, `"secret":"REDACTED"`) {
		t.Fatalf("log output = %q, want rewritten attribute", got)
	}
}

func TestSourceAndTimestampRewriting(t *testing.T) {
	var buf bytes.Buffer
	newTestLogger(t, Config{Output: &buf, Format: FormatJSON, Verbosity: 1, AddSource: true}).Info("hello")

	var record struct {
		Time   string `json:"time"`
		Source struct {
			File string `json:"file"`
		} `json:"source"`
	}
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("unmarshal log output: %v", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, record.Time); err != nil {
		t.Fatalf("time = %q, want RFC3339Nano: %v", record.Time, err)
	}
	if record.Source.File != "logging_test.go" {
		t.Fatalf("source file = %q, want logging_test.go", record.Source.File)
	}
}

func TestContextHandlerOmitsEmptyRequestID(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(t, Config{Output: &buf, Format: FormatJSON, Verbosity: 1})
	logger.InfoContext(WithRequestID(context.Background(), ""), "hello")
	if got := buf.String(); strings.Contains(got, "request_id") {
		t.Fatalf("log output = %q, want no request_id", got)
	}
}

func TestUnsupportedFormatReturnsError(t *testing.T) {
	if _, err := NewHandler(Config{Format: "invalid"}); err == nil {
		t.Fatal("NewHandler() error = nil, want unsupported format error")
	}
}

func TestTimeAttributeDoesNotPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(t, Config{Output: &buf, Format: FormatJSON, Verbosity: 1})
	logger.Info("event", "time", "unknown")
	if got := buf.String(); !strings.Contains(got, `"time":"unknown"`) {
		t.Fatalf("log output = %q, want user time attribute", got)
	}
}

func newTestLogger(t *testing.T, cfg Config) *slog.Logger {
	t.Helper()
	logger, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	return logger
}
