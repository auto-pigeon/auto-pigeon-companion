package web

import (
	"os/exec"
	"testing"
)

// NEW_345: what the choices that are not ready need is said once — one closed
// list under the select, a row per item naming the TOOL to set up from the
// profile id the server attaches, the exact words one click further. The
// shipped core.js is run as it is by testdata/setupcauses.check.mjs.
func TestWhatTheUnreadyChoicesNeedIsSaidOnce(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable; the setup-causes table was not run")
	}
	out, err := exec.Command("node", "testdata/setupcauses.check.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Logf("%s", out)
}
