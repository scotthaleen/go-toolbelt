//go:build windows

package privatedir

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func privateTempDir(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	path := parent + `\private`
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := currentUserSecurityDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	security := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	if err := windows.CreateDirectory(pathUTF16, security); err != nil {
		t.Fatal(err)
	}
	return path
}
