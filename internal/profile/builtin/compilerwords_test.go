package builtin

import (
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// classify returns the first rule of the action that matches the line.
func classify(t *testing.T, profileID, actionID, stream, raw string) (profile.DiagnosticRule, bool) {
	t.Helper()
	entry, err := Find(profileID)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range entry.Profile.ActionList() {
		if action.ID != actionID {
			continue
		}
		for _, rule := range action.Diagnostics {
			if rule.Matches(stream, raw) {
				return rule, true
			}
		}
		return profile.DiagnosticRule{}, false
	}
	t.Fatalf("%s has no action %s", profileID, actionID)

	return profile.DiagnosticRule{}, false
}

// The lines ericw-tools 2.0.0-alpha11 printed for dm2 with its WAD missing
// (compile job 20260923T112326Z-7d5551dde659). The older `WARNING 16` rules
// matched none of them, and Build & Run reads these ids (playrun/textures.go).
func TestTheQ1CompilerRulesReadEricwTools2(t *testing.T) {
	for raw, want := range map[string]string{
		"addArchive: WARNING: archive 'gfx/metal.wad' not found": "wad_not_found",
		"WARNING: No valid WAD filenames in worldmodel":          "no_valid_wad",
		"WARNING: unable to find texture SKY4":                   "texture_not_found",
	} {
		rule, ok := classify(t, "auto-pigeon.ericw-tools.q1", "compile", "stdout", raw)
		if !ok || rule.ID != want {
			t.Errorf("%q classified as %q, want %q", raw, rule.ID, want)
		}
	}
}

// vkQuake 1.36.0 aborts while quitting. The line only that quit prints is the
// proof of a clean stop, on stderr because the profile sets LIBC_FATAL_STDERR_.
func TestVkQuakesQuitCrashIsACleanStop(t *testing.T) {
	entry, err := Find("auto-pigeon.engine.vkquake")
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range entry.Profile.ActionList() {
		rule, ok := classify(t, "auto-pigeon.engine.vkquake", action.ID, "stderr", "*** buffer overflow detected ***: terminated")
		if !ok || !rule.CleanStop {
			t.Errorf("%s: the quit crash is %+v, want a clean_stop rule", action.ID, rule)
		}
		if action.Environment.Set["LIBC_FATAL_STDERR_"] != "1" {
			t.Errorf("%s: LIBC_FATAL_STDERR_ is not set, so glibc writes the line to the terminal", action.ID)
		}
	}
}
