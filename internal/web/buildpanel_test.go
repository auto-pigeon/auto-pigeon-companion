package web

import (
	"os/exec"
	"testing"
)

// The Build panel reads top to bottom — status, findings, stages, files,
// output — and three things it computes are checked here without a page: a
// span is said the way it is read (a lighting pass was "708399 ms"), a run of
// output lines that differ only in their numbers is folded to two and a count
// (a compiler that warns once per entity printed the same sentence forty
// times), and every stage's errors and warnings are one list, the same finding
// counted once, errors first.
func TestTheBuildPanelSaysSpansFoldsRepeatsAndGroupsFindings(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed; the build panel's helpers were NOT exercised")
	}
	out, err := exec.Command("node", "testdata/buildpanel.check.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Logf("%s", out)
}
