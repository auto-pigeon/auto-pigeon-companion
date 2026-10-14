package builtin

import (
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Q2_009, seen live: vis printed `    10 clusters` for a sealed two-room map and
// the build said "The BSP has no visibility clusters". The rule matched the
// substring `0 clusters`, which every count ending in 0 contains. ericw-tools
// right-aligns the count, so a zero is preceded by a space and nothing else is.
func TestOnlyAZeroClusterCountIsReportedAsNoClusters(t *testing.T) {
	for _, id := range []string{EricwQ2, EricwAutoPigeonQ2} {
		entry, err := Find(id)
		if err != nil {
			t.Fatal(err)
		}
		var rule *profile.DiagnosticRule
		for _, action := range entry.Profile.ActionList() {
			for i := range action.Diagnostics {
				if action.Diagnostics[i].ID == "no_clusters" {
					rule = &action.Diagnostics[i]
				}
			}
		}
		if rule == nil {
			t.Fatalf("%s has no no_clusters rule", id)
		}
		for line, want := range map[string]bool{"     0 clusters": true, "    10 clusters": false,
			"    37 clusters": false, "   100 clusters": false, "  1200 clusters": false} {
			if got := rule.Matches("stdout", line); got != want {
				t.Errorf("%s: %q matched=%v, want %v", id, line, got, want)
			}
		}
	}
}
