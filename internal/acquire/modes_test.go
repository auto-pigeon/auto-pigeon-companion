package acquire

import (
	"go/build"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// The three routes, resolved through one function.
//
// Each one ends in the same [Result], and the differences that remain are
// facts on it — which mode, and what the description says about who vouched. A caller that had to branch on the mode
// to find out where the executables are would be a caller that eventually
// forgot one branch.

// toolScript is the fixture "tool": this repository's own bytes, executable
// enough to prove a permission bit.
const toolScript = "#!/bin/sh\necho fixture tool\n"

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
	root := filepath.Join(t.TempDir(), "my-tools")
	tool := writeTool(t, root, "bin/fixture")

	result, err := Resolve(Request{
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
	if !strings.Contains(result.Description, "nothing has verified it") {
		t.Errorf("the description is not honest about what was checked: %q", result.Description)
	}
}

func TestAUserPathCannotReachOutsideTheDirectoryItNames(t *testing.T) {
	root := t.TempDir()
	_, err := Resolve(Request{
		Option:      profile.AcquisitionOption{Mode: profile.AcquireUserPath},
		Executables: []profile.Executable{{Name: "fixture", File: "../../bin/sh"}},
		UserPath:    root,
	})
	if err == nil {
		t.Fatal("an executable path that escapes its root was resolved")
	}
}

func TestASystemPathResolutionLooksUpOnlyTheCommandsTheOptionNames(t *testing.T) {
	dir := t.TempDir()
	tool := writeTool(t, dir, "fixture")

	result, err := Resolve(Request{
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
	_, err = Resolve(Request{
		Option:      profile.AcquisitionOption{Mode: profile.AcquireSystemPath, Commands: []string{"fixture"}},
		Executables: []profile.Executable{{Name: "other", File: "other"}},
		LookPath:    func(string) (string, error) { return tool, nil },
	})
	if err == nil {
		t.Fatal("a PATH lookup resolved a command the option never named")
	}
}

func TestAnAlreadyInstalledRouteStaysInsideTheConfiguredRoot(t *testing.T) {
	gameRoot := t.TempDir()
	tool := writeTool(t, gameRoot, "tools/bin/fixture")

	result, err := Resolve(Request{
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
	_, err = Resolve(Request{
		Option: profile.AcquisitionOption{
			Mode: profile.AcquireAlreadyInstalled, RelativeTo: profile.RootGame, Path: "tools",
		},
		Executables: fixtureExecutables(),
	})
	if err == nil || !strings.Contains(err.Error(), profile.RootGame) {
		t.Fatalf("a missing root was not reported by name: %v", err)
	}
}

func TestANonExecutableFileIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not work this way on Windows")
	}
	root := t.TempDir()
	path := filepath.Join(root, "bin", "fixture")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
	if err := os.WriteFile(path, []byte(toolScript), 0o644); err != nil {
		t.Fatalf("%v", err)
	}
	_, err := Resolve(Request{
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
// resolved before the job was submitted.
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
			if strings.HasSuffix(imported, "/internal/acquire") {
				t.Errorf("%s imports %s; the dependency runs the other way", name, imported)
			}
		}
	}
}

// A document that still lists a managed download is read, and the route is
// refused with what to do instead: nothing here downloads a program.
func TestAManagedDownloadRouteIsRefusedWithTheWayAround(t *testing.T) {
	_, err := Resolve(Request{
		Option:      profile.AcquisitionOption{Mode: profile.AcquireManagedDownload},
		Executables: fixtureExecutables(),
	})
	if err != ErrNoDownloads {
		t.Fatalf("err = %v, want ErrNoDownloads", err)
	}
}

// Nothing that finds a program can fetch one. A structural check rather than a
// behavioural one: the packages that locate executables — this one, the
// extractor's and the profile model — import no HTTP client, so no future
// change can add a download route without this test failing first.
func TestNothingThatFindsAProgramCanDownloadOne(t *testing.T) {
	for _, name := range []string{
		"github.com/andrea-dintino/auto-pigeon-companion/internal/acquire",
		"github.com/andrea-dintino/auto-pigeon-companion/internal/aue",
		"github.com/andrea-dintino/auto-pigeon-companion/internal/profile",
	} {
		pkg, err := build.Import(name, "", 0)
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, imported := range pkg.Imports {
			if imported == "net/http" || strings.HasSuffix(imported, "/internal/aub") {
				t.Errorf("%s imports %s; a package that finds programs must not be able to fetch one", name, imported)
			}
		}
	}
}
