//go:build windows

package joincontent

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"unicode/utf16"
)

// errPrivilegeNotHeld is ERROR_PRIVILEGE_NOT_HELD: creating a symbolic link on
// Windows needs SeCreateSymbolicLinkPrivilege, which an ordinary account has
// only with Developer Mode turned on.
const errPrivilegeNotHeld syscall.Errno = 1314

// linkDir makes link point at the directory source.
//
// A symbolic link when Windows allows one, and otherwise a directory JUNCTION,
// which any account may create for a local directory. Operator, 2026-09-23, on
// Windows: "linking the game folder: symlink … id1 …: A required privilege is not
// held by the client." A junction is what `mklink /J` makes; the engine reads the
// game's files through it exactly as through a symlink, and removing it (os.Remove,
// and os.RemoveAll, which removes an entry before it would ever descend) removes
// the junction and never the folder it points at.
func linkDir(source, link string) error {
	err := os.Symlink(source, link)
	if err == nil || !errors.Is(err, errPrivilegeNotHeld) {
		return err
	}

	return junction(source, link)
}

const (
	reparseTagMountPoint = 0xA0000003
	fsctlSetReparsePoint = 0x000900A4
)

// junction creates link as a mount-point reparse point to the absolute
// directory target, with nothing but the standard library.
func junction(target, link string) error {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if err = os.Mkdir(link, 0o700); err != nil {
		return err
	}
	made := false
	defer func() {
		if !made {
			_ = os.Remove(link)
		}
	}()

	name, err := syscall.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_OPEN_REPARSE_POINT|syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(handle)

	// REPARSE_DATA_BUFFER, MountPointReparseBuffer: the substitute name is the
	// NT path (`\??\C:\…`), the print name the ordinary one, each terminated.
	substitute := utf16.Encode([]rune(`\??\` + absolute))
	printed := utf16.Encode([]rune(absolute))
	subBytes, printBytes := len(substitute)*2, len(printed)*2
	pathBytes := subBytes + 2 + printBytes + 2
	dataLength := 8 + pathBytes
	buffer := make([]byte, 8+dataLength)
	le := binary.LittleEndian
	le.PutUint32(buffer[0:], reparseTagMountPoint)
	le.PutUint16(buffer[4:], uint16(dataLength))
	le.PutUint16(buffer[8:], 0)
	le.PutUint16(buffer[10:], uint16(subBytes))
	le.PutUint16(buffer[12:], uint16(subBytes+2))
	le.PutUint16(buffer[14:], uint16(printBytes))
	offset := 16
	for _, unit := range substitute {
		le.PutUint16(buffer[offset:], unit)
		offset += 2
	}
	offset += 2
	for _, unit := range printed {
		le.PutUint16(buffer[offset:], unit)
		offset += 2
	}

	var returned uint32
	if err = syscall.DeviceIoControl(handle, fsctlSetReparsePoint, &buffer[0], uint32(len(buffer)),
		nil, 0, &returned, nil); err != nil {
		return &os.LinkError{Op: "junction", Old: absolute, New: link, Err: err}
	}
	made = true

	return nil
}
