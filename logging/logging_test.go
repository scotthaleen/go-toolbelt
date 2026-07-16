package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestContextHandlerAddsRequestID(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(Config{Output: &buf, Format: FormatJSON, Verbosity: 1})
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
}
