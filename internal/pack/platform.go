package pack

import "runtime"

// caseInsensitiveFilesystem reports whether this platform's default filesystem
// treats two paths differing only in case as one file.
//
// A platform check rather than a probe of the actual directory, and the
// difference matters. A probe is more accurate — a case-sensitive volume on
// macOS exists, and so does a case-insensitive one mounted on Linux — but it
// requires creating a file to find out, in a directory this program may only be
// about to write to. The platform answer is wrong only in the direction that
// refuses something it could have allowed, which is the right way for this
// particular check to be wrong.
func caseInsensitiveFilesystem() bool {
	switch runtime.GOOS {
	case "windows", "darwin":
		return true
	}
	return false
}
