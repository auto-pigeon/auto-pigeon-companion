package cli

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/urischeme"
)

// First-use registration of the `autopigeon://` handler (NEW_310, HITL).
//
// The editor's Leaks → Test in Companion and Live Games' join links reach this
// program only through the operating system's handler for the scheme. Until
// now a person had to find Settings › Links from Auto-Pigeon, or a terminal,
// before the first click worked; on the Windows beta machine the key was simply
// absent, Chrome showed no prompt at all, and the editor said "It may not be
// running" about a Companion that was running. HITL's decision: the Companion
// registers the handler itself at first use, and records that it did in
// config.json, so it is done once.
//
// "Once" means the record is what is honoured afterwards:
//
//   - no record: register (unless the handler already runs this program), and
//     record it. That is the first use.
//   - a record, and the handler still runs the program the record names, which
//     is not this one: an older Companion this machine registered before — the
//     release folder of the previous version. A link would start THAT one, or
//     nothing when its folder is gone, so this one takes the handler over.
//   - a record, and no handler: somebody removed it after it was registered
//     (Settings, `companion uri unregister`, a registry cleaner). Not undone
//     behind their back; Settings still offers the button.
//   - a record, and a handler running some OTHER program: somebody chose it.
//     Left alone, for the same reason.
//
// Only an application launch does this — a double-click, or a cold start from
// a link — never `serve` in server mode, which is what scripts and tests run.
// macOS declares the handler in the .app bundle, so there is nothing to write.

// uriFirstUse performs the first-use registration and returns one line for the
// log saying what it did, or why nothing.
func uriFirstUse(env *Env, settings config.Config, now time.Time) string {
	registrar := env.uriRegistrar()
	goos := registrar.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	if goos == "darwin" {
		return "link handler: macOS reads it from the application bundle; nothing to register"
	}
	self, err := registrar.Self()
	if err != nil {
		return "link handler: not registered: " + err.Error()
	}
	state, err := registrar.Status()
	if err != nil {
		return "link handler: its state could not be read: " + err.Error()
	}
	record := settings.URIHandler

	switch {
	case state.Registered:
		if record == nil || !samePath(goos, record.Executable, self) {
			if err := recordURIHandler(env, self, state.Method, now, "found"); err != nil {
				return "link handler: already this Companion; recording it failed: " + err.Error()
			}
		}
		return "link handler: " + state.Detail
	case record == nil:
		// First use.
	case state.Handler == "":
		return "link handler: removed since " + record.At.Format(time.RFC3339) +
			"; not registered again (Settings › Links from Auto-Pigeon has the button)"
	case samePath(goos, state.Handler, record.Executable):
		// The Companion registered before, from somewhere else: take it over.
	default:
		return "link handler: " + state.Detail + "; another program was chosen, left as it is"
	}

	registered, err := registrar.Register()
	if err != nil {
		if errors.Is(err, urischeme.ErrNotPerformable) {
			return "link handler: " + registered.Detail
		}
		return "link handler: registering failed: " + err.Error()
	}
	how := "registered"
	if record != nil {
		how = "taken over"
	}
	if err := recordURIHandler(env, self, registered.Method, now, how); err != nil {
		return "link handler: " + registered.Detail + "; recording it in config.json failed: " + err.Error()
	}
	return "link handler: " + how + " at first use: " + registered.Detail
}

// recordURIHandler writes the record into config.json, creating the file when
// this is the first thing ever saved.
func recordURIHandler(env *Env, executable, method string, now time.Time, how string) error {
	_, err := updateSettings(env, func(c *config.Config) error {
		c.URIHandler = &config.URIHandler{
			Executable: executable,
			Method:     method,
			At:         now.UTC(),
			How:        how,
		}
		return nil
	})
	return err
}

// samePath compares two executable paths the way the platform the registrar
// acts for does (NEW_310A): Windows paths without regard to case or to which
// slash separates them, every other platform byte for byte after cleaning. The
// registrar's platform, not this process's — Windows rules are tested on Linux
// through a fake registrar, and a Linux handler at /opt/Companion is not the
// one at /opt/companion.
func samePath(goos, a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if goos == "windows" {
		clean := func(p string) string {
			p = strings.ReplaceAll(p, "/", `\`)
			for strings.Contains(p, `\\`) && !strings.HasPrefix(p, `\\`) {
				p = strings.ReplaceAll(p, `\\`, `\`)
			}
			return strings.TrimSuffix(p, `\`)
		}
		return strings.EqualFold(clean(a), clean(b))
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	return a != "." && a == b
}
