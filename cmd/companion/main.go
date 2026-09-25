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

	"github.com/auto-pigeon/auto-pigeon-companion/internal/cli"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
)

// version is the build-time version string, `1.<commit-count>` — the format
// AUP and AUG report, where the count is `git rev-list --count HEAD` in this
// repository. build/release.sh sets it:
//
//	go build -ldflags "-X main.version=1.$(git rev-list --count HEAD)" ./cmd/companion
//
// A build nobody stamped says `unknown`. A version-shaped placeholder would be
// read out, quoted in a bug report and compared, and be wrong every time. The
// commit itself is in the binary's own build information and `companion
// version` prints it.
var version = "unknown"

func main() {
	// A released Companion takes a non-official server address only from the
	// config.json beside it, or when started with --debug (operator,
	// 2026-09-25; internal/config/release_policy.go). Applied first, so the
	// environment variable it sets aside cannot shadow that file.
	officialOnly, err := config.ApplyReleasePolicy(version != "unknown", config.WantsDebug(os.Args[1:]),
		os.LookupEnv, os.Unsetenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}

	// The optional development `.env` — see internal/config/envfile.go. Read
	// before anything else so every command, the GUI included, resolves the
	// same backend address. Absent is the ordinary case and says nothing.
	report, err := config.LoadDevEnvFile(os.Getenv, os.LookupEnv, os.Setenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
	if officialOnly {
		for i, key := range report.Applied {
			if key == config.EnvAUBBaseURL {
				config.NoteIgnoredAddress(os.Getenv(key), report.Path)
				_ = os.Unsetenv(key)
				report.Applied = append(report.Applied[:i:i], report.Applied[i+1:]...)
				break
			}
		}
	}
	if len(report.Applied) > 0 {
		fmt.Fprintf(os.Stderr, "using %s from %s\n", strings.Join(report.Applied, ", "), report.Path)
	}
	if len(report.Ignored) > 0 {
		fmt.Fprintf(os.Stderr, "warning: %s may only set %s; ignored %s\n",
			report.Path, strings.Join(config.DevEnvKeys, ", "), strings.Join(report.Ignored, ", "))
	}
	// And the optional config.json beside the executable: the root of an
	// unpacked release, for a person pointing it at a development stack
	// without an environment variable. It may set the server address and the
	// port, nothing else.
	if executable, err := os.Executable(); err == nil {
		override, err := config.LoadExecutableOverride(executable, os.LookupEnv, os.Setenv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(2)
		}
		if len(override.Applied) > 0 {
			fmt.Fprintf(os.Stderr, "using %s from %s\n", strings.Join(override.Applied, ", "), override.Path)
		}
	}
	if ignored := config.IgnoredAddresses(); len(ignored) > 0 {
		fmt.Fprintf(os.Stderr, "ignored server address %s. %s\n", strings.Join(ignored, "; "), config.IgnoredExplanation)
	}

	env := &cli.Env{
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Version: version,
	}
	os.Exit(cli.Run(env, os.Args[1:]))
}
