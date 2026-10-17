package web

import (
	"os/exec"
	"testing"
)

// Q2_020: Build & Run's name in the game follows the chosen map until the
// person names it — seen live on the release candidate, where choosing one map
// and then another compiled the second map and installed and started it under
// the first map's name.
func TestTheNameInTheGameFollowsTheChosenMap(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	out, err := exec.Command("node", "testdata/mapname.check.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Logf("%s", out)
}
