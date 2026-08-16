package privatedir

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureAndValidate(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("private directories unsupported")
	}
	path := filepath.Join(privateTempDir(t), "private")
	if err := Ensure(path); err != nil {
		t.Fatal(err)
	}
	if err := Validate(path); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(path); err != nil {
		t.Fatalf("idempotent Ensure: %v", err)
	}
}

func TestEmptyPath(t *testing.T) {
	if err := Ensure(""); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Ensure error = %v", err)
	}
	if err := Validate(""); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Validate error = %v", err)
	}
}
