//go:build darwin

package localgateway

import "golang.org/x/sys/unix"

func restoreSocketNoReplace(from, to string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_EXCL)
}
