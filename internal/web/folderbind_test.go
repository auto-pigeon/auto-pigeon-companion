package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// NEW_244D: an already-present EricW directory is bound by choosing the
// FOLDER, in the page, rather than five separate file pickers — and what that
// records is a local binding, never a verified download.

// writeEricwLayout lays out a release the way the profile declares it, on the
// platform the test is running on.
//
// The profile declares `bin/qbsp{platform.exe_suffix}`, so on Windows the files
// it looks for are `qbsp.exe` and friends. Writing POSIX names there made the
// bind refuse with "missing bin/qbsp.exe, bin/vis.exe, ..." and this test failed
// on windows/amd64 for a reason that had nothing to do with what it checks:
// the fixture was wrong, not the binding. Found by AUCOM/AUT 246I running the
// suite on Windows for the first time.
func writeEricwLayout(t *testing.T, root string, programs ...string) {
	t.Helper()
	for _, program := range programs {
		name := program + currentPlatform().ExeSuffix()
		writeFixtureFile(t, filepath.Join(root, "bin", name), "#!/bin/sh\n")
		if err := os.Chmod(filepath.Join(root, "bin", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAFolderBindsEveryDeclaredProgramAsALocalBinding(t *testing.T) {
	m := newMachine(t)
	root := filepath.Join(m.dir, "ericw tools v0.18.1")
	writeEricwLayout(t, root, "qbsp", "vis", "light", "bspinfo", "bsputil")

	for _, chosen := range []string{root, filepath.Join(root, "bin")} {
		status, body := m.call(http.MethodPost, "/api/v1/profiles/auto-pigeon.ericw-tools.q1/bind",
			map[string]any{"folder": chosen})
		if status != http.StatusOK {
			t.Fatalf("binding %s = %d %v", chosen, status, body)
		}
		recorded, _ := body["binding"].(map[string]any)
		executables, _ := recorded["executables"].(map[string]any)
		wantVis := filepath.Join(root, "bin", "vis"+currentPlatform().ExeSuffix())
		if len(executables) != 5 || executables["vis"] != wantVis {
			t.Errorf("choosing %s recorded %v", chosen, executables)
		}
		if recorded["acquisition"] != string(profile.AcquireUserPath) {
			t.Errorf("a folder the user chose was recorded as %v", recorded["acquisition"])
		}
	}
}

func TestAFolderWithHalfAToolchainIsRefusedAndRecordsNothing(t *testing.T) {
	m := newMachine(t)
	root := filepath.Join(m.dir, "partial")
	writeEricwLayout(t, root, "qbsp", "light")
	status, body := m.call(http.MethodPost, "/api/v1/profiles/auto-pigeon.ericw-tools.q1/bind",
		map[string]any{"folder": root})
	message, _ := body["error"].(string)
	if status != http.StatusBadRequest || !strings.Contains(message, "bin/vis") {
		t.Fatalf("status = %d, error = %q", status, message)
	}
	set, err := binding.LoadFile(m.bindings)
	if err == nil {
		if _, found := set.Find("auto-pigeon.ericw-tools.q1"); found {
			t.Error("a refused folder still recorded a binding")
		}
	}
}

// TestNamingAProgramReplacesAManagedProvenance: paths a person typed over a
// managed install are not the bytes the catalogue verified.
func TestNamingAProgramReplacesAManagedProvenance(t *testing.T) {
	m := newMachine(t)
	root := filepath.Join(m.dir, "mine")
	writeEricwLayout(t, root, "qbsp", "vis", "light", "bspinfo", "bsputil")
	_, err := binding.Update(m.bindings, func(set *binding.Set) error {
		return set.Put(binding.LocalBinding{
			SchemaVersion: binding.SchemaVersion, ProfileID: "auto-pigeon.ericw-tools.q1",
			ProfileVersion: "1.0.0", ProfileDigest: "sha256:" + strings.Repeat("0", 64),
			Trust:       profile.TrustBuiltin,
			Acquisition: profile.AcquireManagedDownload,
			Installs: []binding.PinnedInstall{{
				PackageID: "ericw-tools.q1", Version: "0.18.1", Digest: "sha256:" + strings.Repeat("1", 64),
			}},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	status, body := m.call(http.MethodPost, "/api/v1/profiles/auto-pigeon.ericw-tools.q1/bind",
		map[string]any{"executables": map[string]string{"qbsp": filepath.Join(root, "bin", "qbsp"+currentPlatform().ExeSuffix())}})
	if status != http.StatusOK {
		t.Fatalf("status = %d %v", status, body)
	}
	recorded, _ := body["binding"].(map[string]any)
	if recorded["acquisition"] != string(profile.AcquireUserPath) || recorded["installs"] != nil {
		t.Errorf("recorded %v / installs %v, want a local binding with no managed install", recorded["acquisition"], recorded["installs"])
	}
}

// NEW_247B: DarkPlaces's own Linux archive names its SDL client
// darkplaces-linux-x86_64-sdl, not the darkplaces-sdl distributions ship, and
// choosing the unpacked folder refused it as "missing darkplaces-sdl".
func TestTheDarkPlacesFolderUpstreamPublishesIsFound(t *testing.T) {
	if platform := currentPlatform(); platform.OS != "linux" || platform.Arch != "amd64" {
		t.Skip("the upstream spelling under test is DarkPlaces's linux/amd64 one")
	}
	m := newMachine(t)
	root := filepath.Join(m.dir, "darkplacesengine")
	for _, name := range []string{"darkplaces-linux-x86_64-glx", "darkplaces-linux-x86_64-sdl", "darkplaces-linux-x86_64-dedicated"} {
		writeFixtureFile(t, filepath.Join(root, name), "#!/bin/sh\n")
		if err := os.Chmod(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	status, body := m.call(http.MethodPost, "/api/v1/profiles/auto-pigeon.engine.darkplaces/bind",
		map[string]any{"folder": root})
	if status != http.StatusOK {
		t.Fatalf("binding %s = %d %v", root, status, body)
	}
	recorded, _ := body["binding"].(map[string]any)
	executables, _ := recorded["executables"].(map[string]any)
	// The SDL client, which is what the profile names — not the GLX one that
	// sorts first in the directory.
	if want := filepath.Join(root, "darkplaces-linux-x86_64-sdl"); executables["engine"] != want {
		t.Errorf("recorded %v, want engine = %s", executables, want)
	}
}
