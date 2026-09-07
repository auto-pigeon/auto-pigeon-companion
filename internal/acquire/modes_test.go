package acquire

import (
	"context"
	"go/build"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// The four routes, resolved through one function.
//
// Each one ends in the same [Result], and the differences that remain are
// facts on it — which mode, whether there is a cache entry, what the
// description says about who vouched. A caller that had to branch on the mode
// to find out where the executables are would be a caller that eventually
// forgot one branch.

func fixtureExecutables() []profile.Executable {
	return []profile.Executable{{Name: "fixture", File: "bin/fixture{platform.exe_suffix}"}}
}

// writeTool puts an executable file where a test says a user already has one.
func writeTool(t *testing.T, dir, relative string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
	if err := os.WriteFile(path, []byte(toolScript), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
	return path
}

func TestAUserPathResolvesAgainstADirectoryTheUserChose(t *testing.T) {
	f := newFixture(t)
	root := filepath.Join(t.TempDir(), "my-tools")
	tool := writeTool(t, root, "bin/fixture")

	result, err := f.acquirer().Resolve(context.Background(), Request{
		Option:      profile.AcquisitionOption{Mode: profile.AcquireUserPath, Title: "Choose it", Hint: "the folder"},
		Executables: fixtureExecutables(),
		UserPath:    root,
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if result.Executables["fixture"] != tool {
		t.Errorf("resolved %q, want %q", result.Executables["fixture"], tool)
	}
	if result.Install != nil {
		t.Error("a user path produced a cache entry")
	}
	if !strings.Contains(result.Description, "nothing has verified it") {
		t.Errorf("the description is not honest about what was checked: %q", result.Description)
	}
}

func TestAUserPathCannotReachOutsideTheDirectoryItNames(t *testing.T) {
	f := newFixture(t)
	root := t.TempDir()
	_, err := f.acquirer().Resolve(context.Background(), Request{
		Option:      profile.AcquisitionOption{Mode: profile.AcquireUserPath},
		Executables: []profile.Executable{{Name: "fixture", File: "../../bin/sh"}},
		UserPath:    root,
	})
	if err == nil {
		t.Fatal("an executable path that escapes its root was resolved")
	}
}

func TestASystemPathResolutionLooksUpOnlyTheCommandsTheOptionNames(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	tool := writeTool(t, dir, "fixture")

	result, err := f.acquirer().Resolve(context.Background(), Request{
		Option:      profile.AcquisitionOption{Mode: profile.AcquireSystemPath, Commands: []string{"fixture"}},
		Executables: []profile.Executable{{Name: "fixture", File: "fixture{platform.exe_suffix}"}},
		LookPath: func(name string) (string, error) {
			if name != "fixture"+CurrentPlatform().ExeSuffix() {
				t.Errorf("looked up %q", name)
			}
			return tool, nil
		},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if result.Executables["fixture"] != tool {
		t.Errorf("resolved %q, want %q", result.Executables["fixture"], tool)
	}
	if result.ToolRoot != "" {
		t.Error("a PATH lookup has no common root and must not invent one")
	}

	// A profile that declares an executable the option does not name cannot be
	// resolved this way, and saying so beats resolving one of two.
	_, err = f.acquirer().Resolve(context.Background(), Request{
		Option:      profile.AcquisitionOption{Mode: profile.AcquireSystemPath, Commands: []string{"fixture"}},
		Executables: []profile.Executable{{Name: "other", File: "other"}},
		LookPath:    func(string) (string, error) { return tool, nil },
	})
	if err == nil {
		t.Fatal("a PATH lookup resolved a command the option never named")
	}
}

func TestAnAlreadyInstalledRouteStaysInsideTheConfiguredRoot(t *testing.T) {
	f := newFixture(t)
	gameRoot := t.TempDir()
	tool := writeTool(t, gameRoot, "tools/bin/fixture")

	result, err := f.acquirer().Resolve(context.Background(), Request{
		Option: profile.AcquisitionOption{
			Mode: profile.AcquireAlreadyInstalled, RelativeTo: profile.RootGame, Path: "tools",
		},
		Executables: fixtureExecutables(),
		Roots:       map[string]string{profile.RootGame: gameRoot},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if result.Executables["fixture"] != tool {
		t.Errorf("resolved %q, want %q", result.Executables["fixture"], tool)
	}

	// No configured root is an error that names the root, not a silent guess.
	_, err = f.acquirer().Resolve(context.Background(), Request{
		Option: profile.AcquisitionOption{
			Mode: profile.AcquireAlreadyInstalled, RelativeTo: profile.RootGame, Path: "tools",
		},
		Executables: fixtureExecutables(),
	})
	if err == nil || !strings.Contains(err.Error(), profile.RootGame) {
		t.Fatalf("a missing root was not reported by name: %v", err)
	}
}

func TestAManagedDownloadResolvesThroughTheCacheAndSaysWhoVouched(t *testing.T) {
	f := newFixture(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", goodArchive(t),
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.publish()

	result, err := f.acquirer().Resolve(context.Background(), Request{
		Option: profile.AcquisitionOption{
			Mode: profile.AcquireManagedDownload, CatalogPackage: "fixture.tool",
		},
		Executables: fixtureExecutables(),
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if result.Install == nil {
		t.Fatal("a managed download produced no install record")
	}
	if !strings.HasPrefix(result.Executables["fixture"], result.ToolRoot) {
		t.Errorf("the executable %q is not under the tool root %q", result.Executables["fixture"], result.ToolRoot)
	}
	for _, want := range []string{f.catalogKeyID, "serial 1", result.Install.Digest} {
		if !strings.Contains(result.Description, want) {
			t.Errorf("the description does not say %q: %q", want, result.Description)
		}
	}
}

func TestAProfileCannotNameAFileTheCatalogueNeverVouchedFor(t *testing.T) {
	f := newFixture(t)
	f.addPackage("fixture.tool", "1.0.0", "fixture-1.0.tar.gz", goodArchive(t),
		catalog.KindTarGz, []string{"bin/fixture"}, "tool-1.0")
	f.publish()

	_, err := f.acquirer().Resolve(context.Background(), Request{
		Option: profile.AcquisitionOption{
			Mode: profile.AcquireManagedDownload, CatalogPackage: "fixture.tool",
		},
		Executables: []profile.Executable{{Name: "fixture", File: "bin/nothing-here"}},
	})
	if err == nil {
		t.Fatal("a profile naming a file the archive never contained resolved")
	}
	if !strings.Contains(err.Error(), "install record") {
		t.Errorf("the error should say the file is not in the install record: %v", err)
	}
}

func TestANonExecutableFileIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not work this way on Windows")
	}
	f := newFixture(t)
	root := t.TempDir()
	path := filepath.Join(root, "bin", "fixture")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
	if err := os.WriteFile(path, []byte(toolScript), 0o644); err != nil {
		t.Fatalf("%v", err)
	}
	_, err := f.acquirer().Resolve(context.Background(), Request{
		Option:      profile.AcquisitionOption{Mode: profile.AcquireUserPath},
		Executables: fixtureExecutables(),
		UserPath:    root,
	})
	if err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("a file with no executable bit was accepted: %v", err)
	}
}

// The executor never acquires anything.
//
// Executables reach a job through the request its caller built from a binding,
// resolved before the job was submitted. That is what lets the garbage
// collector import internal/job to ask what a job used; if the dependency ran
// the other way there would be a cycle, and the collector would have to be told
// its references by every caller instead of asking.
func TestTheJobAndBindingPackagesDoNotImportAcquire(t *testing.T) {
	for _, name := range []string{
		"github.com/andrea-dintino/auto-pigeon-companion/internal/job",
		"github.com/andrea-dintino/auto-pigeon-companion/internal/binding",
		"github.com/andrea-dintino/auto-pigeon-companion/internal/profile",
	} {
		pkg, err := build.Import(name, "", 0)
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, imported := range pkg.Imports {
			if strings.HasSuffix(imported, "/internal/acquire") || strings.HasSuffix(imported, "/internal/catalog") {
				t.Errorf("%s imports %s; the dependency runs the other way", name, imported)
			}
		}
	}
}
