package web

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenBrowser asks the OS to open rawURL in the user's default browser.
//
// Each platform has exactly one right way to do this and none of them need a
// dependency:
//
//	macOS    open <url>
//	Windows  cmd /c start "" <url>   — the empty "" is start's title argument;
//	                                   without it start treats a quoted URL as
//	                                   the window title and opens nothing
//	Linux    xdg-open <url>          — provided by xdg-utils, present on
//	                                   essentially every desktop install but
//	                                   not guaranteed on a bare server
//
// The command is started, not waited on: `open` and `xdg-open` return
// immediately, but a browser launched as a direct child can outlive the call,
// and GUI mode must not block on it. A failure here is not fatal to the
// caller — the server is already listening and the URL is printed — so the
// error is returned for reporting rather than treated as a startup failure.
func OpenBrowser(rawURL string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", rawURL)
	case "windows":
		command = exec.Command("cmd", "/c", "start", "", rawURL)
	default:
		command = exec.Command("xdg-open", rawURL)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("cannot open %s in the default browser: %w", rawURL, err)
	}
	// Reap the helper process so it does not linger as a zombie for the life
	// of a long-running server. The helper exits almost immediately; the
	// browser it spawned is unaffected.
	go func() { _ = command.Wait() }()
	return nil
}
