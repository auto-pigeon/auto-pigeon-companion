package profile

import (
	"strings"
)

// Relative paths, and why every one of them is checked the same way.
//
// A profile names files inside roles it has been granted. The only spelling
// permitted is a relative POSIX path with no `..` in it, because every escape
// from a declared root that has ever mattered was one of five things: an
// absolute path, a `..`, a backslash that a POSIX check did not recognise as a
// separator, a drive letter, or a Windows device name. All five are refused
// here, in one function, so no member gets its own subtly different check.
//
// The executor checks containment again after resolution, against the real
// directory, because a symlink can be created between validation and use. This
// is the cheap check that keeps a hostile document from ever getting that far.

var windowsDeviceNames = []string{
	"CON", "PRN", "AUX", "NUL",
	"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
	"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
}

// checkRelativePath validates a path member. required decides whether an empty
// value is a fault.
func checkRelativePath(c *collector, path string, required bool) {
	if path == "" {
		if required {
			c.addf("is required and empty")
		}
		return
	}
	checkText(c, path, 512, true)
	if strings.ContainsRune(path, '\x00') {
		c.addf("contains a NUL byte")
		return
	}
	if strings.Contains(path, `\`) {
		c.fixf("use `/` as the separator; the Companion converts it for the running platform",
			"contains a backslash")
		return
	}
	if strings.HasPrefix(path, "/") {
		c.fixf("write the path relative to the root it belongs to", "is absolute")
		return
	}
	if len(path) >= 2 && isDriveLetter(path[0]) && path[1] == ':' {
		c.fixf("write the path relative to the root it belongs to", "names a drive letter")
		return
	}
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case "":
			c.fixf("remove the empty segment", "has an empty path segment (a doubled or trailing `/`)")
			return
		case ".":
			c.fixf("remove the `.` segment", "has a `.` segment")
			return
		case "..":
			c.fixf("a path may not leave the root it is declared under", "contains `..`")
			return
		}
		base, _, _ := strings.Cut(segment, ".")
		if contains(windowsDeviceNames, strings.ToUpper(base)) {
			c.fixf("rename it; this name is a device on Windows and does not address a file",
				"contains the reserved name %q", segment)
			return
		}
		if strings.HasSuffix(segment, " ") || strings.HasSuffix(segment, ".") {
			c.fixf("remove the trailing space or dot; Windows silently strips it, so two different paths become one",
				"has a segment ending in a space or a dot: %q", segment)
			return
		}
	}
}
