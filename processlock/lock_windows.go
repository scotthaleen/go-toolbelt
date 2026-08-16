//go:build windows

package processlock

import (
	"errors"
	"fmt"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsLock struct {
	handle windows.Handle
}

func acquire(path string) (lockHandle, error) {
	if err := validatePrivateDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	descriptor, err := currentUserSecurityDescriptor()
	if err != nil {
		return nil, err
	}
	security := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	handle, err := windows.CreateFile(
		pathUTF16,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0,
		security,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return nil, ErrLocked
		}
		return nil, err
	}
	if err := validateLockHandle(handle); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	return &windowsLock{handle: handle}, nil
}

func validatePrivateDirectory(path string) error {
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(
		pathUTF16,
		windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("%w: parent is not a stable directory", ErrUnsafePath)
	}
	descriptor, err := windows.GetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return err
	}
	return validateCurrentUserSecurity(descriptor, "parent directory")
}

func currentUserSecurityDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	return windows.SecurityDescriptorFromString(fmt.Sprintf("O:%[1]sD:P(A;;GA;;;%[1]s)", user.String()))
}

func validateLockHandle(handle windows.Handle) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return fmt.Errorf("%w: lock file is not regular", ErrUnsafePath)
	}
	if info.NumberOfLinks != 1 {
		return fmt.Errorf("%w: lock file has multiple links", ErrUnsafePath)
	}
	descriptor, err := windows.GetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return err
	}
	return validateCurrentUserSecurity(descriptor, "lock file")
}

func validateCurrentUserSecurity(descriptor *windows.SECURITY_DESCRIPTOR, object string) error {
	if descriptor == nil || !descriptor.IsValid() {
		return fmt.Errorf("%w: %s security is unavailable", ErrUnsafePath, object)
	}
	currentUser, err := currentUserSID()
	if err != nil {
		return err
	}
	owner, defaulted, err := descriptor.Owner()
	if err != nil || owner == nil || defaulted || !owner.IsValid() || !owner.Equals(currentUser) {
		return fmt.Errorf("%w: %s belongs to another user", ErrUnsafePath, object)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("%w: %s access control is unavailable", ErrUnsafePath, object)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return err
		}
		header := (*windows.ACE_HEADER)(unsafe.Pointer(ace))
		switch header.AceType {
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !sid.IsValid() {
				return fmt.Errorf("%w: %s has an invalid access control entry", ErrUnsafePath, object)
			}
			if sid.Equals(currentUser) || sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
				continue
			}
			if header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Mask&mutationRights == 0 {
				continue
			}
			if !sid.Equals(currentUser) {
				return fmt.Errorf("%w: %s grants access to another user", ErrUnsafePath, object)
			}
		case windows.ACCESS_DENIED_ACE_TYPE:
		default:
			return fmt.Errorf("%w: %s has unsupported access controls", ErrUnsafePath, object)
		}
	}
	return nil
}

const mutationRights = windows.GENERIC_WRITE |
	windows.GENERIC_ALL |
	windows.FILE_WRITE_DATA |
	fileDeleteChild |
	windows.FILE_APPEND_DATA |
	windows.FILE_WRITE_EA |
	windows.FILE_WRITE_ATTRIBUTES |
	windows.DELETE |
	windows.WRITE_DAC |
	windows.WRITE_OWNER

// FILE_DELETE_CHILD is a directory-specific right not currently exported by
// x/sys/windows. It permits deleting children even without DELETE on the child.
const fileDeleteChild = 0x00000040

func currentUserSID() (*windows.SID, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return nil, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	if user.User.Sid == nil {
		return nil, errors.New("current Windows user has no SID")
	}
	return user.User.Sid.Copy()
}

func (l *windowsLock) release() (bool, error) {
	err := windows.CloseHandle(l.handle)
	return err == nil, err
}
