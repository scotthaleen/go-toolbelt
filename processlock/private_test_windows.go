//go:build windows

package processlock

import (
	"fmt"
	"testing"

	"golang.org/x/sys/windows"
)

func privateTempDir(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf("O:%[1]sD:P(A;OICI;GA;;;%[1]s)", user.String()))
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner,
		nil,
		dacl,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	return path
}
