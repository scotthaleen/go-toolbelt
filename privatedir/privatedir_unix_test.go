//go:build linux || darwin

package privatedir

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestEnsureRestoresModeUnderRestrictiveUmask(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "private")
	oldUmask := unix.Umask(0o777)
	t.Cleanup(func() { unix.Umask(oldUmask) })
	if err := Ensure(path); err != nil {
		t.Fatal(err)
	}
	unix.Umask(oldUmask)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %o, want 700", info.Mode().Perm())
	}
}

func TestRejectsUnsafeExistingPath(t *testing.T) {
	parent := privateTempDir(t)
	public := filepath.Join(parent, "public")
	if err := os.Mkdir(public, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(public); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("public directory error = %v", err)
	}

	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(parent, "symlink")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(symlink); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("symlink error = %v", err)
	}
	if err := Validate(symlink + string(os.PathSeparator)); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("trailing-separator symlink error = %v", err)
	}

	file := filepath.Join(parent, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Validate(file); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("file error = %v", err)
	}
}

func TestEnsureNormalizesTrailingSeparator(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "private") + string(os.PathSeparator)
	if err := Ensure(path); err != nil {
		t.Fatal(err)
	}
	if err := Validate(path); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsDotComponentsBeforeResolution(t *testing.T) {
	parent := privateTempDir(t)
	separator := string(os.PathSeparator)
	for _, path := range []string{
		parent + separator + "private" + separator + ".",
		parent + separator + "private" + separator + "link" + separator + "..",
	} {
		if err := Validate(path); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("Validate(%q) error = %v, want ErrInvalidPath", path, err)
		}
	}
}
