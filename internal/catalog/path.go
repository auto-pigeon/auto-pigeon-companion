package catalog

import (
	"errors"
	"path"
	"strings"
)

// MaxUnpackedSize is the ceiling on everything one archive may expand to.
//
// A hard limit in the code as well as the `unpacked_size` a catalogue declares.
// The declared number is only meaningful once the catalogue has been verified,
// and it is written by the same publisher whose archive is being extracted; a
// number in the program is the bound that does not move.
const MaxUnpackedSize = 2 << 30

// MaxArchiveEntries is the ceiling on how many members one archive may have.
// A hundred thousand files is not a toolchain, it is a way of spending an
// afternoon of somebody's disk and inode budget.
const MaxArchiveEntries = 100_000

// MaxCompressionRatio is how much larger than the archive its contents may be.
//
// The classic bomb is a few kilobytes that expand to gigabytes. The size and
// unpacked-size declarations already bound this for a *verified* catalogue;
// this bounds it for the case the declarations are generous or wrong, which is
// the case that matters, because a publisher who is compromised writes both.
const MaxCompressionRatio = 200

// CheckArchivePath validates a path taken from inside an archive, or from a
// catalogue entry that points into one.
//
// Everything here is refused rather than sanitized. Sanitizing a path means
// deciding what the archive "meant", and an archive that names `../../bin/sh`
// meant that; writing it somewhere else is still acting on a document that has
// already proved hostile. The error is a clause, so callers read
// "which %w" — "which escapes the archive", "which is absolute".
func CheckArchivePath(name string) error {
	switch {
	case name == "":
		return errors.New("is empty")
	case strings.ContainsRune(name, 0):
		return errors.New("contains a NUL byte")
	case strings.ContainsRune(name, '\\'):
		return errors.New("contains a backslash, which is a path separator on Windows and a filename character elsewhere")
	case strings.HasPrefix(name, "/"):
		return errors.New("is absolute")
	case len(name) > 1 && name[1] == ':':
		return errors.New("names a Windows drive")
	case strings.HasPrefix(name, "~"):
		return errors.New("starts at a home directory")
	}
	for _, element := range strings.Split(name, "/") {
		switch element {
		case "":
			return errors.New("has an empty path element")
		case ".", "..":
			return errors.New("escapes the archive")
		}
		if strings.HasPrefix(element, " ") || strings.HasSuffix(element, " ") ||
			strings.HasSuffix(element, ".") {
			return errors.New("has an element that Windows cannot store as written")
		}
		if reservedWindowsName(element) {
			return errors.New("names a reserved Windows device")
		}
	}
	if cleaned := path.Clean(name); cleaned != name {
		return errors.New("is not already in its simplest form, so what it names depends on who cleans it")
	}
	return nil
}

// checkArchivePath is the unexported spelling used inside this package's
// validation, so the error reads as a clause in a longer sentence.
func checkArchivePath(name string) error { return CheckArchivePath(name) }

// reservedWindowsName reports the DOS device names that are still special in
// every directory on Windows. An archive containing `aux.txt` cannot be
// extracted there, and finding that out halfway through an extraction leaves
// half a toolchain on disk.
func reservedWindowsName(element string) bool {
	base, _, _ := strings.Cut(element, ".")
	switch strings.ToUpper(base) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	}
	return false
}
