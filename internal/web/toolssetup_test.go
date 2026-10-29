package web

import (
	"os/exec"
	"testing"
)

// NEW_368: the shipped tools.js shows the server's background checks in the
// "Setting up build tools" card — a running check with a spinner and its own
// title, a failure with its reason, Retry (one POST) and Details — and takes
// the card away when nothing runs or failed.
func TestTheSetupCardShowsBackgroundChecksAndRetriesAFailedOne(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed; the setup card was NOT exercised")
	}
	out, err := exec.Command("node", "testdata/toolssetup.check.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Logf("%s", out)
}
