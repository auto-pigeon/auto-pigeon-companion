package web

import (
	"os/exec"
	"testing"
)

func TestProfileWizardKeepsTheValidatedDraftAndInstallationIdentity(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable; wizard event-order checks not run")
	}
	out, err := exec.Command("node", "testdata/profilewizard.check.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Logf("%s", out)
}
