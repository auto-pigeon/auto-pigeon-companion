package job

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
)

// NEW_323D against the real programs, where a machine has them: EricW 0.18.1,
// upstream 2.0.0-alpha11 and the Auto-Pigeon build, each asked what it is by
// the executor and judged by the BUILT-IN profiles. The fixture tests beside
// this one prove the mechanism; this proves the two documents say the right
// thing about the three releases a user actually has.
//
// Each variable names the folder that holds qbsp, vis and light:
//
//	AUCOM_TEST_ERICW_LEGACY   an unpacked 0.18.1's bin/
//	AUCOM_TEST_ERICW_ALPHA11  an unpacked 2.0.0-alpha11
//	AUCOM_TEST_ERICW_FORK     an Auto-Pigeon build's bin/ (1.4510 or later)
//	AUCOM_TEST_ERICW_FORK_DEFECTIVE  release 1.4502's or 1.4505's bin/, which
//	                          the fork's own profile names and refuses (NEW_323E)
//
// None is downloaded and none is assumed: a release that is not named is
// skipped, by name.
func TestTheBuiltInProfilesJudgeTheInstalledReleases(t *testing.T) {
	const upstream, fork = "auto-pigeon.ericw-tools.q1", "auto-pigeon.ericw-tools.auto-pigeon-build.q1"
	releases := []struct {
		variable string
		// want is the state under each profile.
		underUpstream, underFork binding.FactState
		identity                 string
	}{
		{"AUCOM_TEST_ERICW_LEGACY", binding.FactIncompatible, binding.FactIncompatible, "v0.18.1"},
		{"AUCOM_TEST_ERICW_ALPHA11", binding.FactCompatible, binding.FactIncompatible, "2.0.0-alpha11"},
		{"AUCOM_TEST_ERICW_FORK", binding.FactCompatible, binding.FactCompatible, "+auto-pigeon."},
		{"AUCOM_TEST_ERICW_FORK_DEFECTIVE", binding.FactCompatible, binding.FactIncompatible, "+auto-pigeon.1.450"},
	}
	ran := false
	for _, release := range releases {
		folder := os.Getenv(release.variable)
		if folder == "" {
			t.Logf("%s is not set; that release was NOT exercised", release.variable)
			continue
		}
		ran = true
		for profileID, want := range map[string]binding.FactState{upstream: release.underUpstream, fork: release.underFork} {
			t.Run(release.variable+"/"+profileID, func(t *testing.T) {
				dir := t.TempDir()
				store, err := OpenStore(filepath.Join(dir, "jobs"))
				if err != nil {
					t.Fatal(err)
				}
				suffix := ""
				if runtime.GOOS == "windows" {
					suffix = ".exe"
				}
				local := binding.LocalBinding{ProfileID: profileID, Executables: map[string]string{}}
				for _, name := range []string{"qbsp", "vis", "light"} {
					local.Executables[name] = filepath.Join(folder, name+suffix)
				}
				service, err := NewService(Options{
					Store: store, Catalog: NewCatalog(filepath.Join(dir, "profiles")),
					Bindings: func(id string) (binding.LocalBinding, bool) { return local, id == profileID },
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := service.Start(t.Context()); err != nil {
					t.Fatal(err)
				}
				defer service.Close()

				facts, err := service.CheckCompatibility(profileID, true)
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"qbsp", "vis", "light"} {
					fact := facts[name]
					if fact.State != want || !strings.Contains(fact.Identity, release.identity) {
						t.Errorf("%s: %s %q (%s), want %s …%s…", name, fact.State, fact.Identity, fact.Detail, want, release.identity)
					}
				}
				// And the executor's own answer about starting vis, which is
				// refused before vis is started when it is the wrong one.
				err = service.Compatible(profileID, "vis")
				switch {
				case want == binding.FactCompatible && err != nil:
					t.Errorf("vis is refused: %v", err)
				case want != binding.FactCompatible && failure.Of(err) != failure.ToolIncompatible:
					t.Errorf("vis is not refused as incompatible: %v", err)
				}
			})
		}
	}
	if !ran {
		t.Skip("no installed ericw-tools release was named (AUCOM_TEST_ERICW_LEGACY, _ALPHA11, _FORK, _FORK_DEFECTIVE); the built-in profiles were NOT judged against real programs")
	}
}

// Q2_008: the same, for the two Quake II documents. They ask nothing of the
// program until this task, and each requirement below was measured on the
// release named: 2.0.0-alpha7's qbsp stops its usage before `-basedir` (so the
// upstream document holds qbsp only to `-q2bsp` and `-leaktest`), 0.18.1 has
// no Quake II at all, and the fork's document accepts only its own releases.
// 1.4505 is NOT refused here: its defect was measured on Quake 1 maps.
//
//	AUCOM_TEST_ERICW_ALPHA7   an unpacked 2.0.0-alpha7
//
// and the four variables above.
func TestTheBuiltInQuake2ProfilesJudgeTheInstalledReleases(t *testing.T) {
	const upstream, fork = "auto-pigeon.ericw-tools.q2", "auto-pigeon.ericw-tools.auto-pigeon-build.q2"
	releases := []struct {
		variable                 string
		underUpstream, underFork binding.FactState
		identity                 string
	}{
		{"AUCOM_TEST_ERICW_LEGACY", binding.FactIncompatible, binding.FactIncompatible, "v0.18.1"},
		{"AUCOM_TEST_ERICW_ALPHA7", binding.FactCompatible, binding.FactIncompatible, "2.0.0-alpha7"},
		{"AUCOM_TEST_ERICW_ALPHA11", binding.FactCompatible, binding.FactIncompatible, "2.0.0-alpha11"},
		{"AUCOM_TEST_ERICW_FORK", binding.FactCompatible, binding.FactCompatible, "+auto-pigeon."},
		{"AUCOM_TEST_ERICW_FORK_DEFECTIVE", binding.FactCompatible, binding.FactCompatible, "+auto-pigeon.1.450"},
	}
	ran := false
	for _, release := range releases {
		folder := os.Getenv(release.variable)
		if folder == "" {
			t.Logf("%s is not set; that release was NOT exercised", release.variable)
			continue
		}
		ran = true
		for profileID, want := range map[string]binding.FactState{upstream: release.underUpstream, fork: release.underFork} {
			t.Run(release.variable+"/"+profileID, func(t *testing.T) {
				dir := t.TempDir()
				store, err := OpenStore(filepath.Join(dir, "jobs"))
				if err != nil {
					t.Fatal(err)
				}
				suffix := ""
				if runtime.GOOS == "windows" {
					suffix = ".exe"
				}
				local := binding.LocalBinding{ProfileID: profileID, Executables: map[string]string{}}
				for _, name := range []string{"qbsp", "vis", "light"} {
					local.Executables[name] = filepath.Join(folder, name+suffix)
				}
				service, err := NewService(Options{
					Store: store, Catalog: NewCatalog(filepath.Join(dir, "profiles")),
					Bindings: func(id string) (binding.LocalBinding, bool) { return local, id == profileID },
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := service.Start(t.Context()); err != nil {
					t.Fatal(err)
				}
				defer service.Close()

				facts, err := service.CheckCompatibility(profileID, true)
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"qbsp", "vis", "light"} {
					fact := facts[name]
					if fact.State != want || !strings.Contains(fact.Identity, release.identity) {
						t.Errorf("%s: %s %q (%s), want %s …%s…", name, fact.State, fact.Identity, fact.Detail, want, release.identity)
					}
				}
				for _, action := range []string{"compile", "vis", "light"} {
					err = service.Compatible(profileID, action)
					switch {
					case want == binding.FactCompatible && err != nil:
						t.Errorf("%s is refused: %v", action, err)
					case want != binding.FactCompatible && failure.Of(err) != failure.ToolIncompatible:
						t.Errorf("%s is not refused as incompatible: %v", action, err)
					}
				}
			})
		}
	}
	if !ran {
		t.Skip("no installed ericw-tools release was named; the built-in Quake II profiles were NOT judged against real programs")
	}
}
