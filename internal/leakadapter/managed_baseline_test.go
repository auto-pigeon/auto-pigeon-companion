package leakadapter

import (
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
)

// NEW_507A (#21, option A): the managed installer never offers anything older
// than its Baseline, so the Baseline must itself be a release the Quake II
// reading was measured on. Raising it without measuring that release's
// controls (aucom.leak-adapters, "Qualifying the next managed release") fails
// here. A release newer than the Baseline that a fresh install takes is
// qualified the same way, by its own testdata/q2/logs-<release>.
func TestTheManagedEricwBaselineIsAQualifiedQuake2Release(t *testing.T) {
	for _, tool := range managedtools.Tools {
		if tool.ID != managedtools.EricW {
			continue
		}
		for _, version := range EricwQ2MeasuredVersions {
			if EricwQ2ReleaseOf(version) == tool.Baseline {
				return
			}
		}
		t.Fatalf("the managed EricW baseline %s is not a measured Quake II release (%v)", tool.Baseline, EricwQ2MeasuredVersions)
	}
	t.Fatal("no managed EricW tool")
}
