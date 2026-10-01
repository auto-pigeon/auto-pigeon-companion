package builtin

import (
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
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

// Q3Map2 2.5.17n, measured (Q3_010): a `misc_model` whose file is absent prints
// this line, writes the BSP without the model and exits 0. The line is the
// only thing that says so, so the rule that matches it is `fatal` — and the
// rules for a leak and for a missing image carry a class, so a build tells
// them apart by a token and not by their wording.
func TestQ3Map2sExitZeroFindingsAreClassedAndAMissingModelIsFatal(t *testing.T) {
	for _, c := range []struct {
		action, raw, id, class string
		fatal                  bool
	}{
		{"compile", `ERROR: Unable to open file "models/q3010/nothere.md3".`, "model_missing", "model_missing", true},
		{"compile", "ERROR: Invalid MD3 header: some offsets are outside the file", "model_unreadable", "model_unreadable", true},
		{"compile", "ERROR: Invalid MD3 file: Magic bytes not found", "model_unreadable", "model_unreadable", true},
		{"compile", "ERROR: MD3 File is too small.", "model_truncated", "model_unreadable", true},
		{"compile", "******* leaked *******", "leaked", "leak", false},
		{"compile", "--- MAP LEAKED, ABORTING LEAKTEST ---", "leaktest_abort", "leak", false},
		{"compile", "WARNING: Couldn't find image for shader textures/q3004/floor", "missing_image", "shader_image_missing", false},
		{"light", "WARNING: Couldn't find image for shader textures/q3004/floor", "missing_image", "shader_image_missing", false},
	} {
		rule, ok := classify(t, Q3Map2, c.action, "stdout", c.raw)
		if !ok || rule.ID != c.id || rule.Class != c.class || rule.Fatal != c.fatal {
			t.Errorf("%s: %q classified as %+v, want %s class %s fatal %t", c.action, c.raw, rule, c.id, c.class, c.fatal)
		}
	}
	// A line that merely mentions a file being unreadable is not the model line.
	if rule, ok := classify(t, Q3Map2, "compile", "stdout", "Unable to open file"); ok && rule.Fatal {
		t.Errorf("a bare phrase matched the fatal rule: %+v", rule)
	}
}

// A platform the Q3Map2 document makes no claim about is refused, and the
// refusal is CLASSED: "this program does not run here" is a different thing to
// tell somebody than "this program is not installed", and before Q3_010 the
// two differed only in their wording. The three platforms it does name are
// each `supported` or `unverified` — unverified is information, not a refusal.
func TestQ3Map2OnAPlatformItMakesNoClaimAboutIsAClassedRefusal(t *testing.T) {
	entry, err := Find(Q3Map2)
	if err != nil {
		t.Fatal(err)
	}
	request := func(platform profile.Platform) profile.Request {
		return profile.Request{
			Platform: platform,
			Roots: map[string]string{
				profile.RootWorkspace: "/w", profile.RootGame: "/g", profile.RootContent: "/c", profile.RootToolInstall: "/t",
			},
			Inputs: map[string]string{"source_map": "/w/input/source_map/a.map"},
		}
	}
	_, err = profile.Resolve(entry.Profile, "compile", request(profile.Platform{OS: "plan9", Arch: "amd64"}))
	if err == nil || failure.Of(err) != failure.PlatformUnsupported {
		t.Fatalf("plan9: err = %v (class %q)", err, failure.Of(err))
	}
	for _, platform := range []profile.Platform{{OS: "linux", Arch: "amd64"}, {OS: "windows", Arch: "amd64"}, {OS: "darwin", Arch: "arm64"}} {
		if _, err := profile.Resolve(entry.Profile, "compile", request(platform)); failure.Of(err) == failure.PlatformUnsupported {
			t.Errorf("%s is refused as unsupported: %v", platform, err)
		}
	}
	// And a build with no game folder at all is its own class.
	missing := request(profile.Platform{OS: "linux", Arch: "amd64"})
	delete(missing.Roots, profile.RootGame)
	if _, err := profile.Resolve(entry.Profile, "compile", missing); failure.Of(err) != failure.GameDataMissing {
		t.Errorf("no game folder: err = %v (class %q)", err, failure.Of(err))
	}
}

// ioquake3 1.36, measured (Q3_010): with no `pak0.pk3` the engine prints this
// and exits 3. The line is classed, so a job that failed on it says "game data
// is missing" and not merely "the program failed".
func TestIoquake3SaysItsGameDataIsMissingAndTheLineIsClassed(t *testing.T) {
	line := `Quake 3 data files are missing. Please copy "pak0.pk3" through "pak8.pk3" from the "baseq3" directory in your Quake 3 install or CD-ROM to:`
	for _, action := range []string{"play_map", "host_dedicated"} {
		rule, ok := classify(t, IoQuake3, action, "stdout", line)
		if !ok || rule.Class != "game_data_missing" || rule.Severity != profile.SeverityError {
			t.Errorf("%s: classified as %+v", action, rule)
		}
	}
}
