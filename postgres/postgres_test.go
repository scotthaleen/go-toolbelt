package postgres

import (
	"context"
	"strings"
	"testing"
)

func TestStartRequiresDSN(t *testing.T) {
	store := New(Config{})
	err := store.Start(context.Background())
	if err == nil {
		t.Fatal("Start() error = nil, want DSN error")
	}
	if !strings.Contains(err.Error(), "dsn is required") {
		t.Fatalf("Start() error = %v, want DSN error", err)
	}
	if store.DB() != nil {
		t.Fatal("DB() is non-nil after failed start")
	}
}
