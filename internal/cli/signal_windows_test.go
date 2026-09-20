//go:build windows

package cli

import "errors"

// terminateSelf has no signal to send on Windows.
//
// There is no SIGTERM, and `os.Process.Signal` supports only Kill there — which
// is not the graceful path under test. The caller is skipped on Windows rather
// than pretending to measure a shutdown that never happened; this declaration
// exists so the package COMPILES for that target, which is the whole point.
// Same shape as internal/job's helper_signal_windows_test.go.
func terminateSelf() error {
	return errors.New("there is no SIGTERM to send on Windows")
}
