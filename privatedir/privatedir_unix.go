//go:build linux || darwin

package privatedir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func ensure(path string) error {
	if err := validateCreationParent(filepath.Dir(path)); err != nil {
		return fmt.Errorf("validate creation parent: %w", err)
	}
	err := os.Mkdir(path, 0o700)
	switch {
	case err == nil:
		var stat unix.Stat_t
		if statErr := unix.Lstat(path, &stat); statErr != nil {
			return statErr
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Uid != uint32(os.Geteuid()) {
			return fmt.Errorf("%w: created path changed identity", ErrUnsafe)
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return err
		}
		return validate(path)
	case errors.Is(err, os.ErrExist):
		return validate(path)
	default:
		return err
	}
}

func validateCreationParent(path string) error {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return fmt.Errorf("%w: parent is not a directory", ErrUnsafe)
	}
	stickyTrusted := stat.Mode&unix.S_ISVTX != 0 && (stat.Uid == 0 || stat.Uid == uint32(os.Geteuid()))
	if stat.Mode&0o022 == 0 || stickyTrusted {
		return nil
	}
	return fmt.Errorf("%w: parent permits unsafe replacement", ErrUnsafe)
}

func validate(path string) error {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return fmt.Errorf("%w: path is not a directory", ErrUnsafe)
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%w: directory belongs to another user", ErrUnsafe)
	}
	if stat.Mode&0o077 != 0 {
		return fmt.Errorf("%w: directory permissions permit access by another user", ErrUnsafe)
	}
	return nil
}
