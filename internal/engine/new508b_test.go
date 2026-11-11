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
