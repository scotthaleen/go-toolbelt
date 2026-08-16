//go:build windows

package privatedir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsRejectsUntrustedAccess(t *testing.T) {
	current, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	for _, ace := range []string{"(A;;GR;;;%s)", "(A;OICIIO;GW;;;%s)"} {
		descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf(
			"O:%sD:P(A;;GA;;;%s)"+ace,
			current,
			current,
			world,
		))
		if err != nil {
			t.Fatal(err)
		}
		if err := validateCurrentUserSecurity(descriptor); !errors.Is(err, ErrUnsafe) {
			t.Fatalf("validation error = %v, want ErrUnsafe", err)
		}
	}
}

func TestWindowsRejectsWrongOwnerAndNonDirectory(t *testing.T) {
	current, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf("O:%sD:P(A;;GA;;;%s)", admins, current))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCurrentUserSecurity(descriptor); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("wrong-owner error = %v, want ErrUnsafe", err)
	}

	file := filepath.Join(privateTempDir(t), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Validate(file); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("file error = %v, want ErrUnsafe", err)
	}
}

func TestWindowsCreatedDirectoryHasProtectedInheritedUserACL(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "child")
	if err := Ensure(path); err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("created directory DACL is not protected")
	}
	if err := validateCurrentUserSecurity(descriptor); err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil || dacl.AceCount != 1 {
		t.Fatalf("ACE count = %v, want 1", dacl)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	header := (*windows.ACE_HEADER)(unsafe.Pointer(ace))
	current, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !sid.IsValid() || !sid.Equals(current) {
		t.Fatalf("created directory ACE belongs to %v, want current user", sid)
	}
	// Windows maps GENERIC_ALL from the SDDL to concrete file rights.
	const fileAllAccess = 0x001f01ff
	if ace.Mask&fileAllAccess != fileAllAccess {
		t.Fatalf("created directory ACE mask = %#x, want FILE_ALL_ACCESS", ace.Mask)
	}
	if header.AceFlags&(windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE) != windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE {
		t.Fatalf("ACE flags = %#x", header.AceFlags)
	}
}

func TestWindowsRejectsReparsePoint(t *testing.T) {
	parent := privateTempDir(t)
	target := filepath.Join(parent, "target")
	if err := Ensure(target); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("creating a Windows symlink requires additional privilege: %v", err)
	}
	if err := Validate(link); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("reparse validation error = %v, want ErrUnsafe", err)
	}
}

func TestWindowsAllowsTrustedPrincipals(t *testing.T) {
	current, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf(
		"O:%sD:P(A;;GA;;;%s)(A;;GA;;;%s)(A;;GA;;;%s)",
		current,
		current,
		system,
		admins,
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCurrentUserSecurity(descriptor); err != nil {
		t.Fatal(err)
	}
}
