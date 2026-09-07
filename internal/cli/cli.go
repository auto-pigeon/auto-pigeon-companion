// Package cli is the Companion's hand-rolled subcommand framework and the commands built
// on it.
//
// # Why hand-rolled
//
// No cobra, no CLI framework — matching AUE's own style, and the
// repository-wide preference for the standard library.
// The whole dispatcher is Run below; a framework would be a dependency and a
// vendored tree in exchange for code that fits on one screen.
//
// # The registration pattern
//
//   - A subcommand is a Command value in the commands slice: a Name, a one-line
//     Summary for the usage text, an argument shape for help, and a Run func
//     with the signature `func(*Env, []string) int`.
//   - Run owns its own flag.FlagSet and parses its own arguments. The
//     dispatcher never interprets a subcommand's flags.
//   - Run returns the process exit code directly, so the 1-versus-2 distinction
//     below stays visible at the point the decision is made.
//   - Everything a command writes goes through Env's streams, never os.Stdout
//     directly, so every command is testable in-process.
//
// # No subcommand means GUI mode
//
// This is the one place the dispatcher differs from a plain CLI: an empty
// argument list is not an error, it is the GUI. Running the binary with no
// arguments — which is what double-clicking it does — starts the local server
// and opens the page in the default browser. Named subcommands run headless for
// scripting.
//
// # Exit codes
//
//	0  success
//	1  the operation failed (login rejected, a job did not succeed)
//	2  the invocation was wrong (unknown subcommand, bad flags, missing argument)
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// Env carries the streams a command reads and writes, plus process-level values
// main owns. Passing them rather than reaching for os.Stdout is what makes every
// command testable without spawning a subprocess.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Version is the build-time version string, owned by main.
	Version string
	// Lookenv resolves environment variables; nil means os.LookupEnv. A field
	// so a test can supply AUL_PASSWORD without mutating the real environment.
	Lookenv func(string) (string, bool)
	// OpenBrowser opens a URL; nil means web.OpenBrowser. A field so serve is
	// testable without a browser actually launching.
	OpenBrowser func(string) error
	// ConfigPath overrides the OS-appropriate config file location. Empty means
	// the real one. A field so a test never reads or writes the developer's own
	// config — the alternative, mutating HOME for the test process, changes
	// behaviour for anything else running in it.
	ConfigPath string
}

func (e *Env) lookenv(name string) (string, bool) {
	if e.Lookenv != nil {
		return e.Lookenv(name)
	}
	return os.LookupEnv(name)
}

// Command is one registered subcommand.
type Command struct {
	Name string
	// Summary is one line, shown in the usage listing.
	Summary string
	// Usage is the argument shape, shown after the name in help.
	Usage string
	Run   func(env *Env, args []string) int
}

// commands is the registry. Order here is display order in the usage text.
var commands = []Command{
	{
		Name: "serve", Usage: "[--port <n>] [--open]",
		Summary: "run the local GUI server without opening a browser",
		Run:     runServe,
	},
	{
		Name: "auth", Usage: "login [--email <address>] | status | logout",
		Summary: "authenticate against auto-pigeon-backend",
		Run:     runAuth,
	},
	{
		Name: "job", Usage: "run | preview | list | show | logs | cancel | retry | artifacts | profiles",
		Summary: "run a profile action as a supervised job, and inspect what ran",
		Run:     runJob,
	},
	{
		Name: "build", Usage: "run | preview | list | show | pipelines",
		Summary: "build a map through a pipeline: several supervised jobs, wired, with a manifest",
		Run:     runBuild,
	},
	{
		Name: "profile", Usage: "validate | show | canonicalize | digest | diff | list | schema",
		Summary: "read, check and compare tool, engine and pipeline profiles",
		Run:     runProfile,
	},
	{
		Name: "acquire", Usage: "plan | install | accept | list | verify | use | gc | resolve",
		Summary: "obtain a profile's programs from the signed catalogue, and manage the cache",
		Run:     runAcquire,
	},
	{
		Name: "catalog", Usage: "keygen | sign | verify | show | status",
		Summary: "sign, verify and inspect the acquisition catalogue and its keyring",
		Run:     runCatalog,
	},
	{
		Name: "launch", Usage: "<game> [--map <name>] [--game-root <dir>] [--dry-run]",
		Summary: "launch a game as a supervised job, using its AUB launch config",
		Run:     runLaunch,
	},
	{
		Name: "extractor", Usage: "version",
		Summary: "run the bundled auto-pigeon-extractor (AUE)",
		Run:     runExtractor,
	},
	{
		Name:    "migrate",
		Summary: "fold Launcher and older Companion configuration into the current one",
		Run:     runMigrate,
	},
	{
		Name:    "version",
		Summary: "print the build version",
		Run:     runVersion,
	},
}

func lookup(name string) (Command, bool) {
	for _, command := range commands {
		if command.Name == name {
			return command, true
		}
	}
	return Command{}, false
}

// Names lists every registered subcommand, for tests that assert the registry
// rather than a hand-copied list.
func Names() []string {
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		names = append(names, command.Name)
	}
	return names
}

// Run executes one invocation and returns the process exit code.
func Run(env *Env, args []string) int {
	if len(args) == 0 {
		// GUI mode: no subcommand starts the server and opens the browser.
		return runServe(env, []string{"--open"})
	}

	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(env.Stdout, UsageText())
		return 0
	}

	command, ok := lookup(args[0])
	if !ok {
		fmt.Fprintf(env.Stderr, "error: unknown command %q\n\n", args[0])
		fmt.Fprint(env.Stderr, UsageText())
		return 2
	}
	return command.Run(env, args[1:])
}

// UsageText is the help output, generated from the registry so a new subcommand
// cannot be added without appearing here.
func UsageText() string {
	var builder strings.Builder
	builder.WriteString("companion — Auto-Pigeon Companion: build Quake maps, inspect them, and launch games\n\n")
	builder.WriteString("usage:\n")
	builder.WriteString("  companion                 start the local GUI and open a browser\n")
	builder.WriteString("  companion <command> [arguments]\n\ncommands:\n")

	width := 0
	invocations := make([]string, len(commands))
	for index, command := range commands {
		invocation := command.Name
		if command.Usage != "" {
			invocation += " " + command.Usage
		}
		invocations[index] = invocation
		if len(invocation) > width {
			width = len(invocation)
		}
	}
	for index, command := range commands {
		fmt.Fprintf(&builder, "  %-*s  %s\n", width, invocations[index], command.Summary)
	}
	return builder.String()
}

// newFlagSet builds a subcommand's FlagSet with the Companion's shared conventions:
// errors go to the command's own stderr, and the flag package's automatic usage
// dump is suppressed so a bad flag produces one clear line.
func newFlagSet(env *Env, name string) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(env.Stderr)
	set.Usage = func() {}
	return set
}

// parseFlags parses a subcommand's arguments and reports the exit code to
// return when parsing failed.
func parseFlags(env *Env, set *flag.FlagSet, args []string) ([]string, int, bool) {
	if err := set.Parse(args); err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return nil, 2, false
	}
	return set.Args(), 0, true
}

// parseInterspersed parses a subcommand's arguments allowing flags to appear
// after positionals, and returns the positionals in order.
//
// Go's flag package stops parsing at the first non-flag argument, so a plain
// Parse of `launch quake --map e1m1` yields four positionals and no flags. That
// is not how anyone types a command. The loop is the standard remedy: parse,
// take one positional, parse the rest, repeat.
//
// Not every command wants this. `build --tool x -- --tool-flag` deliberately
// uses parseFlags instead, because there the arguments after `--` belong to the
// external tool verbatim and must not be re-parsed as the Companion's own.
func parseInterspersed(env *Env, set *flag.FlagSet, args []string) ([]string, int, bool) {
	var positionals []string
	for {
		if err := set.Parse(args); err != nil {
			fmt.Fprintf(env.Stderr, "error: %v\n", err)
			return nil, 2, false
		}
		rest := set.Args()
		if len(rest) == 0 {
			return positionals, 0, true
		}
		positionals = append(positionals, rest[0])
		args = rest[1:]
	}
}

// signalContext returns a context cancelled on SIGINT or SIGTERM.
//
// Every long-running command uses it, which is what makes Ctrl-C shut the
// server down gracefully and stop a running tool instead of orphaning it.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
