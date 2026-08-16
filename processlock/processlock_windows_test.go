//go:build windows

package processlock

import (
	"errors"
	"fmt"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsRejectsDeleteChildForUntrustedPrincipal(t *testing.T) {
	current, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf(
		"O:%sD:P(A;;GA;;;%s)(A;;0x00000040;;;%s)",
		current,
		current,
		world,
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCurrentUserSecurity(descriptor, "test directory"); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("validation error = %v, want ErrUnsafePath", err)
	}
}

func TestWindowsAllowsReadOnlyUntrustedPrincipal(t *testing.T) {
	current, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf(
		"O:%sD:P(A;;GA;;;%s)(A;;GR;;;%s)",
		current,
		current,
		world,
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCurrentUserSecurity(descriptor, "test directory"); err != nil {
		t.Fatalf("validation error = %v", err)
	}
}
