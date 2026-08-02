package web

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// OpenBrowser asks the OS to open url in the user's default browser.
//
// # One file, a runtime switch, no build tags
//
// The per-platform difference is one command name, so a runtime.GOOS switch
// keeps it visible in a single place. Build-tagged files would give each target
// its own copy of the same three lines and hide from a Linux developer that the
// Windows branch even exists — and the branch that breaks is always the one on
// the platform nobody builds on.
//
// # Not fatal
//
// A failure here is reported, never fatal. The server is already listening and
// the URL is already printed; a machine with no default browser handler — a
// headless Linux box, a stripped container — should leave the user able to open
// the page themselves, not exit.
func OpenBrowser(url string) error {
	name, args, err := openCommand(runtime.GOOS, url)
	if err != nil {
		return err
	}

	command := exec.Command(name, args...)
	if err := command.Start(); err != nil {
		return fmt.Errorf("web: opening %s with %s: %w", url, name, err)
	}
	// Deliberately not waited on. `open` and `xdg-open` return immediately, but
	// `cmd /c start` and some xdg-open implementations outlive the call, and
	// blocking AUL's startup on the browser's lifetime would be wrong. The
	// process is left to the OS; releasing it here would need a Wait in a
	// goroutine whose only effect is reaping, which Go's os/exec already
	// handles for a Start'd process that is never Wait'ed at exit.
	go command.Wait()
	return nil
}

// openCommand is the platform table, split out so every branch is testable from
// any host.
func openCommand(goos, url string) (string, []string, error) {
	if strings.TrimSpace(url) == "" {
		return "", nil, fmt.Errorf("web: no URL to open")
	}
	// Only the loopback URLs this server produces are ever passed here. Guard
	// against anything else reaching a shell-adjacent command like `start`: a
	// URL beginning with "-" would be read as a flag, and a non-http scheme is
	// not something AUL should be handing to the OS opener.
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "", nil, fmt.Errorf("web: refusing to open a non-HTTP URL: %q", url)
	}

	switch goos {
	case "windows":
		// The empty string is `start`'s window-title argument. Without it a
		// quoted URL is taken as the title and no browser opens — a classic
		// and silent failure, so it is passed explicitly.
		return "cmd", []string{"/c", "start", "", url}, nil
	case "darwin":
		return "open", []string{url}, nil
	default:
		// Linux, and the BSDs if AUL is ever built for one. xdg-open is the
		// freedesktop standard and is what every desktop environment installs.
		return "xdg-open", []string{url}, nil
	}
}
