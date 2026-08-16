//go:build linux || darwin

package processlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type unixLock struct {
	file *os.File
}

func acquire(path string) (lockHandle, error) {
	if err := validatePrivateDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("%w: lock file is a symbolic link", ErrUnsafePath)
		}
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("open process lock file")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = file.Close()
		return nil, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = file.Close()
		return nil, fmt.Errorf("%w: lock file is not regular", ErrUnsafePath)
	}
	if stat.Uid != uint32(os.Geteuid()) {
		_ = file.Close()
		return nil, fmt.Errorf("%w: lock file belongs to another user", ErrUnsafePath)
	}
	if stat.Nlink != 1 {
		_ = file.Close()
		return nil, fmt.Errorf("%w: lock file has multiple links", ErrUnsafePath)
	}
	if stat.Mode&0o077 != 0 {
		_ = file.Close()
		return nil, fmt.Errorf("%w: lock file permissions are not private", ErrUnsafePath)
	}
	if err := unix.Fchmod(fd, 0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &unixLock{file: file}, nil
}

func validatePrivateDirectory(path string) error {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return fmt.Errorf("%w: parent is not a directory", ErrUnsafePath)
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%w: parent belongs to another user", ErrUnsafePath)
	}
	if stat.Mode&0o077 != 0 {
		return fmt.Errorf("%w: parent directory permissions are not private", ErrUnsafePath)
	}
	return nil
}

func (l *unixLock) release() (bool, error) {
	unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	return unlockErr == nil || closeErr == nil, errors.Join(unlockErr, closeErr)
}
