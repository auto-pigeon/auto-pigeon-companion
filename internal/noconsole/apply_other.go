//go:build !windows

package noconsole

import "os/exec"

// Apply does nothing here: only Windows gives a child a window of its own.
func Apply(*exec.Cmd) {}
