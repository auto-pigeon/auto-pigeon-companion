package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// aulibsIncidentDir is the authority for the bug-report document, when a
// sibling checkout is present.
const aulibsIncidentDir = "../../../auto-pigeon-libraries/ts/incident-contract"

// The vendored incident contract must be byte-for-byte AULIBS'. The page builds
// the SAME bug-report document AUG and AUP build; a copy nobody compares would
// be a third report format waiting to happen. Skipped, loudly, without a
// sibling checkout: a skip says "not checked here", a pass says "identical".
func TestVendoredIncidentContractIsExactlyAULIBS(t *testing.T) {
	vendored, err := fs.Sub(assetsFS(), "vendor/incident-contract")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	err = fs.WalkDir(vendored, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		want, err := os.ReadFile(filepath.Join(aulibsIncidentDir, name))
		if err != nil {
			t.Skipf("no auto-pigeon-libraries checkout beside this one (%v); the vendored %s was NOT compared", err, name)
		}
		got, err := fs.ReadFile(vendored, name)
		if err != nil {
			return err
		}
		if string(got) != string(want) {
			t.Errorf("assets/vendor/incident-contract/%s has drifted from %s. Re-copy it; do not edit either by hand.",
				name, filepath.Join(aulibsIncidentDir, name))
		}
		checked++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("nothing is vendored under assets/vendor/incident-contract")
	}
}
