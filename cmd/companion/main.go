// Command companion is the Auto-Pigeon Companion (AUCOM) desktop application.
//
// The binary has two modes, both served by the same executable:
//
//   - No subcommand: start the local loopback HTTP server and open the
//     embedded frontend in the OS default browser. This is "GUI mode", and it
//     is what double-clicking the installed binary does.
//   - A named subcommand: run headless, for scripting and CLI use.
//
// This file is deliberately thin: it owns the build-time version string and
// the process's streams and exit code, and nothing else. Subcommand
// registration, dispatch, and the commands themselves live in internal/cli,
// where they can be tested without spawning a subprocess.
package main

import (
	"os"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/cli"
)

// version is the build-time version string. Override it with:
//
//	go build -ldflags "-X main.version=0.2.0" ./cmd/companion
var version = "0.1.0-dev"

func main() {
	env := &cli.Env{
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Version: version,
	}
	os.Exit(cli.Run(env, os.Args[1:]))
}
