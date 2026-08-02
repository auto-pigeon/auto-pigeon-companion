// Command launcher is the Auto-Pigeon Launcher (AUL).
//
// Run with no arguments it starts a local HTTP server on 127.0.0.1 and opens
// the page in the default browser — that is the GUI. Named subcommands run
// headless for scripting. See internal/cli.
//
// This file is deliberately thin: it owns the build-time version string and the
// process's streams and exit code, and nothing else. Dispatch and the commands
// themselves live in internal/cli, where they can be tested without spawning a
// subprocess.
package main

import (
	"os"

	"github.com/andrea-dintino/auto-pigeon-launcher/internal/cli"
)

// version is the build-time version string. Override it with:
//
//	go build -ldflags "-X main.version=0.2.0" ./cmd/launcher
var version = "0.1.0-dev"

func main() {
	os.Exit(cli.Run(&cli.Env{
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Version: version,
	}, os.Args[1:]))
}
