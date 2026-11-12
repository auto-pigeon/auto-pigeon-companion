package job

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin/retiredfixture"
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
//	AUCOM_TEST_ERICW_FORK     an Auto-Pigeon build's bin/ (1.4535 or later)
//	AUCOM_TEST_ERICW_FORK_DEFECTIVE  release 1.4502's or 1.4505's bin/, which
//	                          the fork's own profile names and refuses (NEW_323E)
//	AUCOM_TEST_ERICW_FORK_NO_PARSER_FIX  a release from 1.4510 to 1.4534, which
//	                          lacks 1.4535's map-text parser fix and which both
//	                          fork profiles name and refuse (NEW_507)
//
// None is downloaded and none is assumed: a release that is not named is
// skipped, by name.
func TestTheBuiltInProfilesJudgeTheInstalledReleases(t *testing.T) {
	const upstream, fork = retiredfixture.EricwQ1, "auto-pigeon.ericw-tools.auto-pigeon-build.q1"
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
		{"AUCOM_TEST_ERICW_FORK_NO_PARSER_FIX", binding.FactCompatible, binding.FactIncompatible, "+auto-pigeon.1.45"},
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
				installRetired(t, dir, upstream)
				suffix := ""
				if runtime.GOOS == "windows" {
					suffix = ".exe"
				}
				local := binding.LocalBinding{ProfileID: profileID, Executables: map[string]string{}}
				for _, name := range []string{"qbsp", "vis", "light"} {
					local.Executables[name] = filepath.Join(folder, name+suffix)
				}
				catalog := NewCatalog(filepath.Join(dir, "profiles"))
				approveRetired(t, catalog, upstream, &local)
				service, err := NewService(Options{
					Store: store, Catalog: catalog,
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
		t.Skip("no installed ericw-tools release was named (AUCOM_TEST_ERICW_LEGACY, _ALPHA11, _FORK, _FORK_DEFECTIVE, _FORK_NO_PARSER_FIX); the built-in profiles were NOT judged against real programs")
	}
}

// Q2_008: the same, for the two Quake II documents. They ask nothing of the
// program until this task, and each requirement below was measured on the
// release named: 2.0.0-alpha7's qbsp stops its usage before `-basedir` (so the
// upstream document holds qbsp only to `-q2bsp` and `-leaktest`), 0.18.1 has
// no Quake II at all, and the fork's document accepts only its own releases.
// 1.4505's solid-in-air defect is not held against it here (it was measured on
// Quake 1 maps), but since NEW_507 it is refused like every release before
// 1.4535, for the map-text parser both games share.
//
//	AUCOM_TEST_ERICW_ALPHA7   an unpacked 2.0.0-alpha7
//
// and the four variables above.
func TestTheBuiltInQuake2ProfilesJudgeTheInstalledReleases(t *testing.T) {
	const upstream, fork = retiredfixture.EricwQ2, "auto-pigeon.ericw-tools.auto-pigeon-build.q2"
	releases := []struct {
		variable                 string
		underUpstream, underFork binding.FactState
		identity                 string
	}{
		{"AUCOM_TEST_ERICW_LEGACY", binding.FactIncompatible, binding.FactIncompatible, "v0.18.1"},
		{"AUCOM_TEST_ERICW_ALPHA7", binding.FactCompatible, binding.FactIncompatible, "2.0.0-alpha7"},
		{"AUCOM_TEST_ERICW_ALPHA11", binding.FactCompatible, binding.FactIncompatible, "2.0.0-alpha11"},
		{"AUCOM_TEST_ERICW_FORK", binding.FactCompatible, binding.FactCompatible, "+auto-pigeon."},
		{"AUCOM_TEST_ERICW_FORK_DEFECTIVE", binding.FactCompatible, binding.FactIncompatible, "+auto-pigeon.1.450"},
		{"AUCOM_TEST_ERICW_FORK_NO_PARSER_FIX", binding.FactCompatible, binding.FactIncompatible, "+auto-pigeon.1.45"},
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
				installRetired(t, dir, upstream)
				suffix := ""
				if runtime.GOOS == "windows" {
					suffix = ".exe"
				}
				local := binding.LocalBinding{ProfileID: profileID, Executables: map[string]string{}}
				for _, name := range []string{"qbsp", "vis", "light"} {
					local.Executables[name] = filepath.Join(folder, name+suffix)
				}
				catalog := NewCatalog(filepath.Join(dir, "profiles"))
				approveRetired(t, catalog, upstream, &local)
				service, err := NewService(Options{
					Store: store, Catalog: catalog,
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

// installRetired puts a retired upstream document where a person's own profile
// goes: this build ships it no longer, and it is still what upstream's programs
// are judged by here.
func installRetired(t *testing.T, dir, id string) {
	t.Helper()
	raw, err := retiredfixture.Raw(id)
	if err != nil {
		t.Fatal(err)
	}
	profiles := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(profiles, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profiles, "retired.tool.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// approveRetired records the person having approved the retired document,
// which this test installs as a user's own profile: a user profile nobody
// approved is never asked anything (aucom.compiler-compatibility, "When a
// program is asked"). A built-in document needs no approval and is left alone.
func approveRetired(t *testing.T, catalog *DirCatalog, retired string, local *binding.LocalBinding) {
	t.Helper()
	if local.ProfileID != retired {
		return
	}
	entries, err := catalog.readDir()
	if err != nil {
		t.Fatal(err)
	}
	grants := &grantSource{bindings: map[string]binding.LocalBinding{}}
	for _, entry := range entries {
		if entry.Profile.Metadata().ID == retired {
			grants.grantEverything(t, entry)
		}
	}
	approved, found := grants.bindings[retired]
	if !found {
		t.Fatalf("the retired document %s was not installed", retired)
	}
	local.SchemaVersion, local.ProfileDigest, local.Trust, local.Grant = approved.SchemaVersion, approved.ProfileDigest, approved.Trust, approved.Grant
}
