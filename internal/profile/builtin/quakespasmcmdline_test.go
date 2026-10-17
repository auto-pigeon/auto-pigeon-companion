package builtin

import (
	"strings"
	"testing"
)

// Q2_020, measured on vkQuake 1.36.0 through the 1.270 candidate: QuakeSpasm
// and the engines built on it (vkQuake, QuakeSpasm-Spiked, Ironwail) run the
// `+` commands of their command line from ONE string of at most 256 bytes that
// begins with argv[0]. A `-basedir` is read from argv wherever it is, but a
// `+map` past byte 256 is simply never run: the engine started its demo loop
// instead of the map. The Companion runs an AppImage extracted under the job's
// own TMPDIR, so argv[0] alone was ~170 bytes and Build & Run's `+map` fell
// off the end; by hand, from /tmp, the same argv loaded the map. So in these
// profiles every `+` command comes before the options that carry paths.
func TestQuakeSpasmLineageEnginesRunTheirPlusCommandsWithLongPaths(t *testing.T) {
	const limit = 256
	argv0 := "/home/someone/.cache/auto-pigeon-companion/jobs/20261017T073840Z-62ba05088374/tmp/" +
		"appimage_extracted_b434e54c938b2cdea29f2bcd5afbdf2f/AppRun"
	gameRoot := "/home/someone/Games/My Quake Install With A Long Name/and a second level of folders"
	values := map[string]string{
		"{root.game_root}": gameRoot, "{runtime.mod_name}": "auto-pigeon", "{runtime.map_name}": "q2020q1room",
		"{option.max_players}": "8",
	}
	for _, id := range []string{"auto-pigeon.engine.vkquake", "auto-pigeon.engine.quakespasm",
		"auto-pigeon.engine.quakespasm-spiked", "auto-pigeon.engine.ironwail"} {
		entry, err := Find(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, action := range entry.Profile.ActionList() {
			var args []string
			wanted := ""
			for _, arg := range action.Args {
				value := arg.Value
				for placeholder, replacement := range values {
					value = strings.ReplaceAll(value, placeholder, replacement)
				}
				if arg.When != nil {
					continue // conditional arguments are not part of this action's map load
				}
				args = append(args, value)
				if len(args) >= 2 && args[len(args)-2] == "+map" {
					wanted = "map " + value
				}
			}
			if wanted == "" {
				continue
			}
			line := strings.Join(append([]string{argv0}, args...), " ")
			if len(line) > limit-1 {
				line = line[:limit-1]
			}
			if !strings.Contains(plusCommands(line), wanted) {
				t.Errorf("%s %s: the engine would not run %q; its command line is %q", id, action.ID, wanted, line)
			}
		}
	}
}

// plusCommands is what a QuakeSpasm-lineage engine stuffs: each `+` starts a
// command that runs to the next ` +` or ` -`.
func plusCommands(line string) string {
	var commands []string
	for _, part := range strings.Split(" "+line, " +")[1:] {
		if end := strings.Index(part, " -"); end >= 0 {
			part = part[:end]
		}
		commands = append(commands, part)
	}
	return strings.Join(commands, "\n")
}
