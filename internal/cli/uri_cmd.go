package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/urischeme"
)

// `companion uri` — the operating system's handler for autopigeon:// links.
//
// Three subcommands and no fourth, because there are only three things a person
// wants: to know what is registered, to register it, and to take it off again.
// `status` is first in the usage text on purpose — it writes nothing, and it is
// the answer to "did the installer do this already".

const uriUsage = `usage:
  companion uri status                what handles autopigeon:// on this machine
  companion uri register              make this program the handler, for this user
  companion uri unregister            stop handling it

A link opens ` + "`companion game open <link>`" + `, which shows the game in the
Companion's page and starts nothing. Joining is a separate, reviewed act.

Linux    a NoDisplay desktop entry and a mimeapps.list default, both under
         $XDG_DATA_HOME. Installing the .deb or .rpm ships the entry; this
         command is what a tarball install needs, and what changes the default.
Windows  HKCU\Software\Classes\autopigeon, per user. The installer writes the
         same keys and removes them on uninstall.
macOS    the .app bundle's Info.plist. There is no way to register a scheme for
         a loose binary, so this command reports and does not perform it.
`

func runURI(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, uriUsage)
		return 2
	}
	registrar := env.uriRegistrar()
	switch args[0] {
	case "status":
		state, err := registrar.Status()
		if err != nil {
			return fail(env, err)
		}
		printURIState(env, state)
		return 0
	case "register":
		state, err := registrar.Register()
		if errors.Is(err, urischeme.ErrNotPerformable) {
			printURIState(env, state)
			return 0
		}
		if err != nil {
			return fail(env, err)
		}
		printURIState(env, state)
		return 0
	case "unregister":
		state, err := registrar.Unregister()
		if errors.Is(err, urischeme.ErrNotPerformable) {
			printURIState(env, state)
			return 0
		}
		if err != nil {
			return fail(env, err)
		}
		printURIState(env, state)
		return 0
	}
	fmt.Fprintf(env.Stderr, "error: unknown uri subcommand %q\n", args[0])
	fmt.Fprint(env.Stderr, uriUsage)
	return 2
}

func printURIState(env *Env, state urischeme.State) {
	fmt.Fprintf(env.Stdout, "scheme:     %s://\n", state.Scheme)
	fmt.Fprintf(env.Stdout, "platform:   %s (%s)\n", state.Platform, state.Method)
	registered := "no"
	if state.Registered {
		registered = "yes"
	}
	fmt.Fprintf(env.Stdout, "registered: %s\n", registered)
	if len(state.Command) > 0 {
		fmt.Fprintf(env.Stdout, "command:    %s\n", strings.Join(state.Command, " "))
	}
	for i, location := range state.Locations {
		label := "locations: "
		if i > 0 {
			label = "           "
		}
		fmt.Fprintf(env.Stdout, "%s %s\n", label, location)
	}
	if state.Detail != "" {
		fmt.Fprintf(env.Stdout, "\n%s\n", state.Detail)
	}
}
