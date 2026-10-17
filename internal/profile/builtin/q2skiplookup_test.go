package builtin

import (
	"sort"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Q2_020: ericw-tools qbsp in Quake II mode prints `WARNING: Couldn't locate
// texture for skip` on EVERY run — measured in each of the 19 recorded Quake
// II logs under internal/leakadapter/testdata, sealed controls included — for
// a `skip` it looks up itself and no game ships. Classified as a missing
// texture, every successful build carried an error. That one exact line is
// qbsp's own lookup and says so; any other missing name, including a map's
// own `e1u1/skip`, is still the error it was. The leak adapter excepts the
// same exact name (internal/leakadapter/ericwq2.go), so a build's findings
// and its leak verdict agree.
func TestQbspsOwnSkipLookupIsNotAMissingTexture(t *testing.T) {
	cases := map[string][]string{
		"WARNING: Couldn't locate texture for skip":            {"info:texture_skip_lookup"},
		"WARNING: Couldn't locate texture for q2010/nothere":   {"error:texture_missing"},
		"WARNING: Couldn't locate texture for e1u1/skip":       {"error:texture_missing"},
		"WARNING: Couldn't locate texture for e1u1/floor1_3":   {"error:texture_missing"},
		"WARNING: Couldn't locate texture for skipped/wall_02": {"error:texture_missing"},
	}
	for _, id := range []string{EricwQ2, EricwAutoPigeonQ2} {
		entry, err := Find(id)
		if err != nil {
			t.Fatal(err)
		}
		var compile *profile.Action
		for _, action := range entry.Profile.ActionList() {
			if action.ID == "compile" {
				action := action
				compile = &action
			}
		}
		if compile == nil {
			t.Fatalf("%s has no compile action", id)
		}
		for line, want := range cases {
			var got []string
			for _, rule := range compile.Diagnostics {
				if rule.Matches("stdout", line) && (strings.Contains(rule.ID, "texture") || rule.Severity == profile.SeverityError) {
					got = append(got, string(rule.Severity)+":"+rule.ID)
				}
			}
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("%s: %q classified %v, want %v", id, line, got, want)
			}
		}
	}
}
