//go:build !windows

package raisewindow

import "time"

// Raise does nothing here, and says so.
func Raise(int, time.Duration) bool { return false }
