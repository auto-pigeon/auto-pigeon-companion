package config

import (
	"fmt"
	"strings"
	"sync"
)

// Where a released Companion may take a server address from.
//
// # An operator decision, 2026-09-25
//
// A released Companion on a Windows machine opened on a LAN address nobody had
// configured for it: `AUCOM_AUB_BASE_URL` was still set in that computer's
// environment by an earlier development session, and the sign-in dialog could
// then offer nothing but that "development server". The operator's rule: the
// default is Auto-Pigeon or Auto-Pigeon beta, and a released Companion uses
// any other address ONLY when the config.json beside the executable says so.
//
// So in a stamped release started without --debug:
//
//   - AUCOM_AUB_BASE_URL from the process environment is ignored;
//   - AUCOM_AUB_BASE_URL from a development `.env` is ignored;
//   - an address saved in the per-user config.json that is not an official
//     deployment (left by a --debug session or an older version) is ignored;
//   - the config.json beside the executable is honoured, and so is an official
//     deployment chosen in Settings.
//
// Each ignored source is recorded and reported — on the terminal, in Settings
// and in the sign-in dialog — because an address that silently stops working
// is its own kind of wrong answer. A development build (unstamped, which is
// what `go run`, `go build` and auto-pigeon-tools' harnesses make) and any
// build started with --debug keep every source, exactly as before.

var releasePolicy struct {
	sync.Mutex
	officialOnly bool
	ignored      []string
}

// SetOfficialOnly turns the release rule on or off for this process.
func SetOfficialOnly(on bool) {
	releasePolicy.Lock()
	defer releasePolicy.Unlock()
	releasePolicy.officialOnly = on
}

// OfficialOnly reports whether the release rule is on.
func OfficialOnly() bool {
	releasePolicy.Lock()
	defer releasePolicy.Unlock()
	return releasePolicy.officialOnly
}

// NoteIgnoredAddress records a server address the release rule set aside.
func NoteIgnoredAddress(address, source string) {
	note := fmt.Sprintf("%s from %s", strings.TrimSpace(address), source)
	releasePolicy.Lock()
	defer releasePolicy.Unlock()
	for _, existing := range releasePolicy.ignored {
		if existing == note {
			return
		}
	}
	releasePolicy.ignored = append(releasePolicy.ignored, note)
}

// IgnoredAddresses are the addresses set aside, each with where it came from.
func IgnoredAddresses() []string {
	releasePolicy.Lock()
	defer releasePolicy.Unlock()
	return append([]string(nil), releasePolicy.ignored...)
}

// IgnoredExplanation is the one sentence that goes with IgnoredAddresses.
const IgnoredExplanation = "A released Companion uses a server other than Auto-Pigeon or Auto-Pigeon beta " +
	"only when the config.json beside the program names it, or when it is started with --debug."

// ApplyReleasePolicy enforces the rule on the process environment before any
// other source is read: a stamped release started without --debug drops
// AUCOM_AUB_BASE_URL from the environment and records that it did. It returns
// whether the rule is on, and the caller applies it to the `.env` it loads.
func ApplyReleasePolicy(release, debug bool, lookup func(string) (string, bool), unset func(string) error) (bool, error) {
	on := release && !debug
	SetOfficialOnly(on)
	if !on {
		return false, nil
	}
	if value, set := lookup(EnvAUBBaseURL); set {
		if err := unset(EnvAUBBaseURL); err != nil {
			return true, err
		}
		if strings.TrimSpace(value) != "" {
			NoteIgnoredAddress(value, "the "+EnvAUBBaseURL+" environment variable on this computer")
		}
	}
	return true, nil
}

// WantsDebug reports whether a command line starts the Companion in debug
// mode: `companion --debug` or `companion serve --debug`.
func WantsDebug(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--debug" || arg == "-debug" || strings.HasPrefix(arg, "--debug=") && arg != "--debug=false" {
			return true
		}
	}
	return false
}
