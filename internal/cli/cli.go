// Package cli is AUC's hand-rolled subcommand framework and the commands built
// on it.
//
// # Why hand-rolled
//
// This repository is stdlib-first: no CLI framework, no GUI toolkit, no HTTP
// client library. The dispatcher below is that commitment made concrete, and
// it is deliberately shaped like AUE's internal/cli so the two repositories
// read the same way.
//
// # The registration pattern
//
// Every future prompt that adds a subcommand follows the shape set here:
//
//   - A subcommand is a Command value in the commands slice: a Name, an
//     optional Aliases list, a one-line Summary for the usage text, a Usage
//     string describing the argument shape, and a Run func with the signature
//     `func(*Env, []string) int`.
//   - Run owns its own flag.FlagSet. It parses its own arguments; the
//     dispatcher never interprets a subcommand's flags.
//   - Run returns the process exit code directly, so the 1-versus-2
//     distinction below is never flattened by a shared error-to-code mapping.
//   - Everything a command writes goes through Env's streams, never os.Stdout
//     directly, so every command is testable without a subprocess.
//
// # No subcommand means GUI mode
//
// AUE exits 2 when invoked with no arguments. AUC does not: an empty argument
// list is the documented way a desktop user launches the app (double-clicking
// the installed binary passes no arguments), so it dispatches to `serve`,
// which starts the local server and opens the default browser. Only an
// *unknown* subcommand is a bad invocation.
//
// # Exit codes
//
//	0  success
//	1  the operation failed (server could not start, AUE call failed, login
//	   rejected)
//	2  the invocation was wrong (unknown subcommand, bad flags, missing
//	   required argument)
package cli

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Env carries the streams a command writes to. Passing them rather than using
// os.Stdout/os.Stderr is what makes every command testable in-process.
type Env struct {
	Stdout io.Writer
	Stderr io.Writer
	// Version is the build-time version string, owned by main.
	Version string
}

// Command is one registered subcommand.
type Command struct {
	Name    string
	Aliases []string
	// Summary is one line, shown in the usage listing.
	Summary string
	// Usage is the argument shape, shown after the name in help.
	Usage string
	Run   func(env *Env, args []string) int
}

// commands is the registry. Order here is display order in the usage text.
var commands = []Command{
	{
		Name: "version", Summary: "print the build version",
		Run: runVersion,
	},
	{
		Name: "serve", Usage: "[--addr <host:port>] [--no-browser]",
		Summary: "start the local GUI server (default when no subcommand is given)",
		Run:     runServe,
	},
	{
		Name: "auth", Usage: "<login|status|logout> [flags]",
		Summary: "manage the AUB session stored in the local config",
		Run:     runAuth,
	},
}

// defaultCommand is what an empty argument list dispatches to. See the package
// comment: launching the installed binary with no arguments is GUI mode, not a
// usage error.
const defaultCommand = "serve"

// lookup resolves a name or alias to its command.
func lookup(name string) (Command, bool) {
	for _, command := range commands {
		if command.Name == name {
			return command, true
		}
		for _, alias := range command.Aliases {
			if alias == name {
				return command, true
			}
		}
	}
	return Command{}, false
}

// Names lists every registered name and alias, sorted. Exported for tests that
// assert the registry rather than a hand-copied list.
func Names() []string {
	var names []string
	for _, command := range commands {
		names = append(names, command.Name)
		names = append(names, command.Aliases...)
	}
	sort.Strings(names)
	return names
}

// Run executes one invocation and returns the process exit code. This is the
// whole dispatcher.
func Run(env *Env, args []string) int {
	if len(args) == 0 {
		command, ok := lookup(defaultCommand)
		if !ok {
			// Unreachable unless the registry above is edited wrongly; failing
			// loudly beats silently doing nothing when a user double-clicks.
			fmt.Fprintf(env.Stderr, "error: default command %q is not registered\n", defaultCommand)
			return 2
		}
		return command.Run(env, nil)
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

// UsageText is the help output, generated from the registry so a new
// subcommand cannot be added without appearing here.
func UsageText() string {
	var builder strings.Builder
	builder.WriteString("companion — Auto-Pigeon Companion: local map operations with an AUB account\n\n")
	builder.WriteString("usage:\n  companion [command] [arguments]\n\n")
	builder.WriteString("With no command, companion starts the local GUI server and opens it in\nyour default browser.\n\ncommands:\n")

	width := 0
	invocations := make([]string, len(commands))
	for index, command := range commands {
		invocation := command.Name
		if len(command.Aliases) > 0 {
			invocation += " | " + strings.Join(command.Aliases, " | ")
		}
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

// newFlagSet builds a subcommand's FlagSet with AUC's shared conventions:
// errors go to the command's own stderr, and the flag package's automatic
// usage dump is suppressed so a bad flag produces one clear line rather than a
// wall of text.
func newFlagSet(env *Env, name string) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(env.Stderr)
	set.Usage = func() {}
	return set
}

// parseInterspersed parses a subcommand's arguments allowing flags to appear
// after positionals, and returns the positionals in order.
//
// Go's flag package stops parsing at the first non-flag argument, so
// `auth login --email a@b.c` would otherwise treat `--email` as a positional.
// The loop is the standard remedy: parse, take one positional, parse the rest,
// repeat. Ported from AUE's internal/cli so both CLIs accept the same shapes.
func parseInterspersed(set *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for {
		if err := set.Parse(args); err != nil {
			return nil, err
		}
		rest := set.Args()
		if len(rest) == 0 {
			return positionals, nil
		}
		positionals = append(positionals, rest[0])
		args = rest[1:]
	}
}
