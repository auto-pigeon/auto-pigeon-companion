package web

import (
	"net/http"
	"os/exec"
	"slices"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakadapter"
)

// NEW_507A (#21): the Build page's view of a Quake II leak test names the qbsp
// release that ran — read from the run's own banner — and the releases the
// reading was measured on, so the sentence never names one historical
// baseline. Release 1.4559, what a fresh managed install takes, reads a sealed
// control as no_leak and the slot as a leak with its route.
func TestAQuake2LeakViewNamesTheReleaseThatRanAndTheMeasuredOnes(t *testing.T) {
	m := newMachine(t)
	for i, c := range []struct {
		run      q2Run
		outcome  string
		points   float64
		released string
	}{
		{q2Run{control: "a_sealed", state: job.Succeeded, bsp: ibsp38, release: "1.4559"}, "no_leak", 0, "1.4559"},
		{q2Run{control: "b_gap", state: job.Failed, exit: 1, pts: "b_gap", release: "1.4559"}, "leak", 10, "1.4559"},
		{q2Run{control: "g_truncated", state: job.Failed, exit: 1, release: "1.4559"}, "incomplete", 0, "1.4559"},
		{q2Run{control: "a_sealed", state: job.Succeeded, bsp: ibsp38}, "no_leak", 0, "1.4518"},
		// NEW_508C: 1.4560, measured on every control, is read the same way.
		{q2Run{control: "a_sealed", state: job.Succeeded, bsp: ibsp38, release: "1.4560"}, "no_leak", 0, "1.4560"},
		{q2Run{control: "b_gap", state: job.Failed, exit: 1, pts: "b_gap", release: "1.4560"}, "leak", 10, "1.4560"},
	} {
		id := "20261112T000000Z-0000050" + string(rune('a'+i))
		m.q2LeakBuild(id, c.run)
		status, view := m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
		leak, _ := view["leak_test"].(map[string]any)
		diagnostic, _ := leak["diagnostic"].(map[string]any)
		var releases []string
		for _, r := range leak["qualified_releases"].([]any) {
			releases = append(releases, r.(string))
		}
		if status != http.StatusOK || diagnostic["outcome"] != c.outcome || leak["compiler_release"] != c.released ||
			(c.points > 0 && diagnostic["route_points"] != c.points) ||
			!slices.Contains(releases, "1.4559") || !slices.Contains(releases, "1.4560") || !slices.Contains(releases, "1.4518") ||
			slices.Contains(releases, "1.4561") {
			t.Fatalf("%s (%s): %d %v", c.run.control, c.run.release, status, leak)
		}
	}
}

// NEW_507A (#21): the envelope returned to the editor names the qbsp that ran
// — its checked identity — not the version the profile was written for. Seen
// live: a fresh managed install ran 1.4559 under the profile that declares
// 1.4556, and every result sent to AUB said 1.4556.
func TestAQuake2LeakResultNamesTheQbspThatRan(t *testing.T) {
	m := newMachine(t)
	ran := leakadapter.EricwQ2Release1559Version
	for i, c := range []struct {
		run  q2Run
		want string
	}{
		{q2Run{control: "a_sealed", state: job.Succeeded, bsp: ibsp38, release: "1.4559", identity: ran}, ran},
		{q2Run{control: "a_sealed", state: job.Succeeded, bsp: ibsp38}, leakadapter.EricwQ2MeasuredVersion},
	} {
		id := "20261112T000001Z-0000050" + string(rune('a'+i))
		m.q2LeakBuild(id, c.run)
		status, body := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/result", nil)
		diagnostic, _ := body["diagnostic"].(map[string]any)
		if status != http.StatusOK || body["compiler_version"] != c.want || diagnostic["outcome"] != "no_leak" {
			t.Fatalf("%d: %d %v", i, status, body["compiler_version"])
		}
	}
}

// NEW_507A: the sentence itself (testdata/leakverdictq2.check.mjs).
func TestTheQuake2LeakSentenceNamesTheReleaseInUse(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	out, err := exec.Command(node, "testdata/leakverdictq2.check.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("leakverdictq2.check.mjs: %v\n%s", err, out)
	}
}
