package engine

import (
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Issue #36: the fix a missing folder names for a TOOL profile is a command
// that exists and can be run for that profile — by its id, touching only the
// folder. It was `acquire resolve <its profile file> --mode user_path
// --user-path <folder> …`: a built-in tool has no file, and a tool the
// Companion installed would have had its programs replaced. An engine keeps
// `engine bind`, which is the command for engines.
func TestAToolsMissingFolderNamesACommandThatSetsOnlyTheFolder(t *testing.T) {
	tool := &profile.ToolProfile{}
	tool.Meta.ID, tool.Meta.Kind = "auto-pigeon.q3map2.auto-pigeon-build", profile.KindTool
	fix := rootFix(tool, profile.RootGame, "engine words")
	want := "`companion acquire roots auto-pigeon.q3map2.auto-pigeon-build --root game_root=<path>`"
	if !strings.Contains(fix, want) {
		t.Errorf("the fix is %q; it should name %s", fix, want)
	}
	for _, wrong := range []string{"engine bind", "<its profile file>", "--user-path", "--mode"} {
		if strings.Contains(fix, wrong) {
			t.Errorf("the fix for a tool still says %q: %s", wrong, fix)
		}
	}
	engine := &profile.EngineProfile{}
	engine.Meta.Kind = profile.KindEngine
	if got := rootFix(engine, profile.RootGame, "engine words"); got != "engine words" {
		t.Errorf("an engine's fix was replaced: %q", got)
	}
}

// Issue #36, the second half (NEW_505): the fix a missing PROGRAM names for a
// tool profile can be followed. A built-in tool has no profile file, so
// `acquire resolve <its profile file>` could not be run for it, and its
// `--mode user_path` would have replaced a managed install. A tool a managed
// toolchain serves names `tools use-managed <its id>`; another tool names the
// Profiles action alone.
func TestAToolsMissingProgramNamesARecoveryThatCanBeFollowed(t *testing.T) {
	for _, tc := range []struct {
		id      string
		want    []string
		without []string
	}{
		{id: "auto-pigeon.ericw-tools.auto-pigeon-build.q1",
			want: []string{"in Profiles", "press Install beside it in Profiles",
				"`companion tools use-managed auto-pigeon.ericw-tools.auto-pigeon-build.q1`"}},
		{id: "auto-pigeon.q3map2.auto-pigeon-build",
			want: []string{"`companion tools use-managed auto-pigeon.q3map2.auto-pigeon-build`", "q3map2 (Auto-Pigeon build)"}},
		{id: "someone.qbsp.own", want: []string{"\"Where these programs are on this machine\" in Profiles."},
			without: []string{"use-managed", "Install"}},
	} {
		tool := &profile.ToolProfile{}
		tool.Meta.ID, tool.Meta.Kind = tc.id, profile.KindTool
		for _, again := range []bool{false, true} {
			fix := bindFix(tool, again, "engine words")
			for _, want := range tc.want {
				if !strings.Contains(fix, want) {
					t.Errorf("%s (again=%v): the fix %q does not name %s", tc.id, again, fix, want)
				}
			}
			for _, wrong := range append([]string{"<its profile file>", "acquire resolve", "--user-path", "engine bind", "tools retry"}, tc.without...) {
				if strings.Contains(fix, wrong) {
					t.Errorf("%s (again=%v): the fix still says %q: %s", tc.id, again, wrong, fix)
				}
			}
		}
	}
	engine := &profile.EngineProfile{}
	engine.Meta.Kind = profile.KindEngine
	if got := bindFix(engine, false, "engine words"); got != "engine words" {
		t.Errorf("an engine's fix was replaced: %q", got)
	}
}
