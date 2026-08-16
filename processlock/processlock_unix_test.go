//go:build linux || darwin

package processlock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestUnixCreatesPrivateRegularLockFile(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "process.lock")
	lock := New(Config{Path: path})
	if err := lock.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Stop(context.Background()) }()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode = %v, want regular 0600", info.Mode())
	}
}

func TestUnixRestrictiveUmaskStillCreatesRestartableLock(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "process.lock")
	oldUmask := unix.Umask(0o777)
	t.Cleanup(func() { unix.Umask(oldUmask) })
	first := New(Config{Path: path})
	if err := first.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := first.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	unix.Umask(oldUmask)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode = %o, want 600", info.Mode().Perm())
	}
	second := New(Config{Path: path})
	if err := second.Start(context.Background()); err != nil {
		t.Fatalf("restart acquisition: %v", err)
	}
	if err := second.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestUnixRejectsUnsafeParentAndLockObjects(t *testing.T) {
	t.Run("public parent", func(t *testing.T) {
		parent := t.TempDir()
		if err := os.Chmod(parent, 0o755); err != nil {
			t.Fatal(err)
		}
		lock := New(Config{Path: filepath.Join(parent, "process.lock")})
		if err := lock.Start(context.Background()); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("Start error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("public lock file", func(t *testing.T) {
		path := filepath.Join(privateTempDir(t), "process.lock")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		lock := New(Config{Path: path})
		if err := lock.Start(context.Background()); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("Start error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("symbolic link", func(t *testing.T) {
		parent := privateTempDir(t)
		target := filepath.Join(parent, "target")
		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(parent, "process.lock")
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		lock := New(Config{Path: path})
		if err := lock.Start(context.Background()); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("Start error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("hard link", func(t *testing.T) {
		parent := privateTempDir(t)
		target := filepath.Join(parent, "target")
		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(parent, "process.lock")
		if err := os.Link(target, path); err != nil {
			t.Fatal(err)
		}
		lock := New(Config{Path: path})
		if err := lock.Start(context.Background()); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("Start error = %v, want ErrUnsafePath", err)
		}
	})
}
