package profile

import (
	"fmt"
	"strings"
)

// A person's own tokens never repeat a switch the action already passes
// (Q2_008).
//
// The Quake II vis and light stages pass `-threads {option.threads}`. Adding
// `-threads 4` as a token of one's own put the switch on the command line
// twice, and which of the two a program honours is that program's business:
// ericw-tools reads the last, another tool may read the first or refuse. So a
// switch the action manages — written literally in its arguments, whether or
// not it is passed this time — is set through what manages it, and a token
// that repeats it is refused by name, with that name. A switch the action
// never passes (`-nopercent`, `-path …`) is the person's own to add.

// ManagedSwitch is one switch an action passes and what decides it.
type ManagedSwitch struct {
	// Switch is the literal argument, such as `-threads`.
	Switch string
	// Option is the parameter that decides it, when one does: the option whose
	// value follows the switch, or the option its condition reads.
	Option string
	// Root is the folder whose path follows it, when that is what it carries.
	Root string
}

// ManagedSwitches lists the switches an action's arguments write literally.
func ManagedSwitches(action Action) []ManagedSwitch {
	var out []ManagedSwitch
	seen := map[string]int{}
	for i, arg := range action.Args {
		if !isSwitchToken(arg.Value) {
			continue
		}
		managed := ManagedSwitch{Switch: arg.Value}
		if i+1 < len(action.Args) {
			next := action.Args[i+1].Value
			switch {
			case strings.HasPrefix(next, "{option.") && strings.HasSuffix(next, "}"):
				managed.Option = strings.TrimSuffix(strings.TrimPrefix(next, "{option."), "}")
			case strings.HasPrefix(next, "{root.") && strings.HasSuffix(next, "}"):
				managed.Root = strings.TrimSuffix(strings.TrimPrefix(next, "{root."), "}")
			}
		}
		if managed.Option == "" && managed.Root == "" && arg.When != nil {
			managed.Option = arg.When.Option
			managed.Root = arg.When.Root
		}
		if at, dup := seen[arg.Value]; dup {
			// The same switch under two conditions (`-level` when not fast):
			// keep the first that names what decides it.
			if out[at].Option == "" && out[at].Root == "" {
				out[at] = managed
			}
			continue
		}
		seen[arg.Value] = len(out)
		out = append(out, managed)
	}
	return out
}

// ManagedSwitchConflict refuses tokens that repeat a switch the action manages,
// naming what manages it. It returns nil when nothing repeats.
func ManagedSwitchConflict(action Action, tokens []string) error {
	managed := map[string]ManagedSwitch{}
	for _, item := range ManagedSwitches(action) {
		managed[item.Switch] = item
	}
	for i, token := range tokens {
		item, found := managed[token]
		if !found {
			continue
		}
		position := i + 1
		switch {
		case item.Option != "":
			title := item.Option
			for _, option := range action.Options {
				if option.Name == item.Option && option.Title != "" {
					title = fmt.Sprintf("%q (%s)", option.Title, option.Name)
				}
			}
			return fmt.Errorf("argument %d (%s) is already given to %s by this stage's parameter %s; set that parameter instead — "+
				"a program handed the same switch twice honours one of them, and which one is its choice, not yours",
				position, token, action.Executable, title)
		case item.Root != "":
			return fmt.Errorf("argument %d (%s) is already given to %s with the %s folder this machine has bound; change that folder in Profiles instead of adding the switch again",
				position, token, action.Executable, item.Root)
		default:
			return fmt.Errorf("argument %d (%s) is always given to %s by its profile; it cannot be added again",
				position, token, action.Executable)
		}
	}
	return nil
}

// isSwitchToken is an argument that is a switch: `-x…` or `+x…`, not a
// negative number and not a template.
func isSwitchToken(value string) bool {
	if len(value) < 2 || strings.ContainsAny(value, "{}") {
		return false
	}
	if value[0] != '-' && value[0] != '+' {
		return false
	}
	c := value[1]
	if c == '-' && len(value) > 2 {
		c = value[2]
	}
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
