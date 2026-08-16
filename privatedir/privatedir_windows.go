//go:build windows

package privatedir

import (
	"errors"
	"fmt"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func ensure(path string) error {
	if err := validateCreationParent(filepath.Dir(path)); err != nil {
		return fmt.Errorf("validate creation parent: %w", err)
	}
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	descriptor, err := currentUserSecurityDescriptor()
	if err != nil {
		return err
	}
	security := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	err = windows.CreateDirectory(pathUTF16, security)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return err
	}
	return validate(path)
}

func validateCreationParent(path string) error {
	descriptor, err := directorySecurityDescriptor(path)
	if err != nil {
		return err
	}
	return validateCreationSecurity(descriptor)
}

func validate(path string) error {
	descriptor, err := directorySecurityDescriptor(path)
	if err != nil {
		return err
	}
	return validatePrivateSecurity(descriptor)
}

func directorySecurityDescriptor(path string) (*windows.SECURITY_DESCRIPTOR, error) {
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, fmt.Errorf("%w: path is not a stable directory", ErrUnsafe)
	}
	descriptor, err := windows.GetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return nil, err
	}
	return descriptor, nil
}

func validatePrivateSecurity(descriptor *windows.SECURITY_DESCRIPTOR) error {
	if descriptor == nil || !descriptor.IsValid() {
		return fmt.Errorf("%w: security descriptor is unavailable", ErrUnsafe)
	}
	currentUser, err := currentUserSID()
	if err != nil {
		return err
	}
	owner, defaulted, err := descriptor.Owner()
	if err != nil || owner == nil || defaulted || !trustedSID(owner, currentUser) {
		return fmt.Errorf("%w: directory belongs to another user", ErrUnsafe)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("%w: access control is unavailable", ErrUnsafe)
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
				return fmt.Errorf("%w: invalid access control entry", ErrUnsafe)
			}
			if trustedSID(sid, currentUser) {
				continue
			}
			return fmt.Errorf("%w: directory grants access to another user", ErrUnsafe)
		case windows.ACCESS_DENIED_ACE_TYPE:
		default:
			return fmt.Errorf("%w: directory has unsupported access controls", ErrUnsafe)
		}
	}
	return nil
}

func validateCreationSecurity(descriptor *windows.SECURITY_DESCRIPTOR) error {
	if descriptor == nil || !descriptor.IsValid() {
		return fmt.Errorf("%w: parent security descriptor is unavailable", ErrUnsafe)
	}
	currentUser, err := currentUserSID()
	if err != nil {
		return err
	}
	owner, defaulted, err := descriptor.Owner()
	if err != nil || owner == nil || defaulted || !trustedSID(owner, currentUser) {
		return fmt.Errorf("%w: parent belongs to another user", ErrUnsafe)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("%w: parent access control is unavailable", ErrUnsafe)
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
				return fmt.Errorf("%w: parent has an invalid access control entry", ErrUnsafe)
			}
			if trustedSID(sid, currentUser) {
				continue
			}
			if header.AceFlags&windows.INHERIT_ONLY_ACE == 0 && ace.Mask&mutationRights != 0 {
				return fmt.Errorf("%w: parent grants mutation access to another user", ErrUnsafe)
			}
		case windows.ACCESS_DENIED_ACE_TYPE:
		default:
			return fmt.Errorf("%w: parent has unsupported access controls", ErrUnsafe)
		}
	}
	return nil
}

func trustedSID(sid, currentUser *windows.SID) bool {
	return sid != nil && sid.IsValid() &&
		(sid.Equals(currentUser) || sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid))
}

func currentUserSecurityDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	return windows.SecurityDescriptorFromString(fmt.Sprintf("O:%[1]sD:P(A;OICI;GA;;;%[1]s)", user.String()))
}

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

const (
	fileDeleteChild = 0x00000040
	mutationRights  = windows.GENERIC_WRITE |
		windows.GENERIC_ALL |
		windows.FILE_WRITE_DATA |
		fileDeleteChild |
		windows.FILE_APPEND_DATA |
		windows.FILE_WRITE_EA |
		windows.FILE_WRITE_ATTRIBUTES |
		windows.DELETE |
		windows.WRITE_DAC |
		windows.WRITE_OWNER
)
