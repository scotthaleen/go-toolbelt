package privatedir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	// ErrInvalidPath identifies an empty, non-canonical, or dot-containing path.
	ErrInvalidPath = errors.New("private directory path is required")
	// ErrUnsafe identifies a directory that is not owned and confidentiality-
	// protected according to the current platform policy.
	ErrUnsafe = errors.New("unsafe private directory")
	// ErrUnsupported means the current platform is not Linux, macOS, or Windows.
	ErrUnsupported = errors.New("private directories are unsupported")
)

// Ensure creates path when absent and validates it as a current-user-private
// directory. The parent directory must already exist. Existing unsafe paths are
// rejected rather than modified.
func Ensure(path string) error {
	if path == "" {
		return ErrInvalidPath
	}
	path, err := normalize(path)
	if err != nil {
		return err
	}
	return ensure(path)
}

// Validate checks that path is a current-user-owned directory which untrusted
// principals cannot read or mutate. It does not modify the path.
func Validate(path string) error {
	if path == "" {
		return ErrInvalidPath
	}
	path, err := normalize(path)
	if err != nil {
		return err
	}
	return validate(path)
}

func normalize(path string) (string, error) {
	start := 0
	for i := 0; i <= len(path); i++ {
		if i != len(path) && !os.IsPathSeparator(path[i]) {
			continue
		}
		if segment := path[start:i]; segment == "." || segment == ".." {
			return "", fmt.Errorf("%w: path must not contain dot components", ErrInvalidPath)
		}
		start = i + 1
	}
	trimmed := path
	rootLength := len(filepath.VolumeName(path)) + 1
	for len(trimmed) > rootLength && os.IsPathSeparator(trimmed[len(trimmed)-1]) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if filepath.Clean(trimmed) != trimmed {
		return "", fmt.Errorf("%w: path must not contain dot components", ErrInvalidPath)
	}
	return trimmed, nil
}
