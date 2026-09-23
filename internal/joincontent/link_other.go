//go:build !windows

package joincontent

import "os"

// linkDir makes link a link to the directory source. See link_windows.go for
// why this is a function of its own.
func linkDir(source, link string) error {
	return os.Symlink(source, link)
}
