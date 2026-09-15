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
	"fmt"
	"os"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/cli"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
)

// version is the build-time version string. Override it with:
//
//	go build -ldflags "-X main.version=0.2.0" ./cmd/companion
var version = "0.1.0-dev"

func main() {
	// The optional development `.env` — see internal/config/envfile.go. Read
	// before anything else so every command, the GUI included, resolves the
	// same backend address. Absent is the ordinary case and says nothing.
	report, err := config.LoadDevEnvFile(os.Getenv, os.LookupEnv, os.Setenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
	if len(report.Applied) > 0 {
		fmt.Fprintf(os.Stderr, "using %s from %s\n", strings.Join(report.Applied, ", "), report.Path)
	}
	if len(report.Ignored) > 0 {
		fmt.Fprintf(os.Stderr, "warning: %s may only set %s; ignored %s\n",
			report.Path, strings.Join(config.DevEnvKeys, ", "), strings.Join(report.Ignored, ", "))
	}

	env := &cli.Env{
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Version: version,
	}
	os.Exit(cli.Run(env, os.Args[1:]))
}
