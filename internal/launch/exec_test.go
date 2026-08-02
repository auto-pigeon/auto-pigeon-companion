package launch

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Resolution is tested for every one of the six build targets from whichever
// one the tests happen to run on. The .exe suffix and the path separators are
// exactly the details that are wrong only on the platform nobody develops on,
// and Resolve touches no filesystem precisely so this is possible.
func TestResolveAcrossPlatforms(t *testing.T) {
	config := Config{
		Game:              "quake",
		ExecutablePattern: "{game_root}/bin/{os}-{arch}/quakespasm{exe}",
		Args:              []string{"-basedir", "{game_root}", "+map", "{map}"},
	}

	for _, platform := range []Platform{
		{"windows", "amd64"}, {"windows", "arm64"},
		{"linux", "amd64"}, {"linux", "arm64"},
		{"darwin", "amd64"}, {"darwin", "arm64"},
	} {
		t.Run(platform.GOOS+"/"+platform.GOARCH, func(t *testing.T) {
			plan, err := Resolve(Request{Config: config, GameRoot: "/games/quake", Map: "e1m1", Platform: platform})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			want := filepath.FromSlash("/games/quake/bin/" + platform.GOOS + "-" + platform.GOARCH + "/quakespasm")
			if platform.GOOS == "windows" {
				want += ".exe"
			}
			if plan.Executable != want {
				t.Errorf("Executable = %q, want %q", plan.Executable, want)
			}
			if wantArgs := []string{"-basedir", "/games/quake", "+map", "e1m1"}; !reflect.DeepEqual(plan.Args, wantArgs) {
				t.Errorf("Args = %v, want %v", plan.Args, wantArgs)
			}
			if plan.WorkingDir != filepath.Dir(want) {
				t.Errorf("WorkingDir = %q, want %q", plan.WorkingDir, filepath.Dir(want))
			}
		})
	}
}

// A pattern that omits {exe} still has to get .exe on Windows — the common
// authoring mistake, and one a Linux-only test run would never catch.
func TestResolveAppendsWindowsSuffixWhenPatternOmitsIt(t *testing.T) {
	plan, err := Resolve(Request{
		Config:   Config{Game: "quake", ExecutablePattern: "{game_root}/quakespasm"},
		GameRoot: `C:\Games\Quake`,
		Platform: Platform{GOOS: "windows", GOARCH: "amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(plan.Executable, ".exe") {
		t.Errorf("Executable = %q, want a .exe suffix", plan.Executable)
	}
	if strings.HasSuffix(plan.Executable, ".exe.exe") {
		t.Errorf("Executable = %q, suffix was applied twice", plan.Executable)
	}
}

func TestResolveRequiresItsInputs(t *testing.T) {
	config := ExampleConfigs()[0]

	if _, err := Resolve(Request{Config: config, Map: "e1m1"}); err == nil {
		t.Error("expected an error when {game_root} has no value")
	}
	if _, err := Resolve(Request{Config: config, GameRoot: "/games/quake"}); err == nil {
		t.Error("expected an error when {map} has no value")
	}
	if _, err := Resolve(Request{Config: Config{Game: "x"}, GameRoot: "/games"}); err == nil {
		t.Error("expected an error for a config with no executable pattern")
	}
}

func TestResolveAppendsExtraArgs(t *testing.T) {
	plan, err := Resolve(Request{
		Config:    Config{Game: "quake", ExecutablePattern: "/games/quake/quakespasm"},
		ExtraArgs: []string{"-fullscreen"},
		Platform:  Platform{GOOS: "linux", GOARCH: "amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Args, []string{"-fullscreen"}) {
		t.Errorf("Args = %v", plan.Args)
	}
}

func TestFind(t *testing.T) {
	configs := ExampleConfigs()

	found, err := Find(configs, "QUAKE")
	if err != nil {
		t.Fatalf("Find is case sensitive: %v", err)
	}
	if found.Game != "quake" {
		t.Errorf("Game = %q", found.Game)
	}

	_, err = Find(configs, "doom")
	if err == nil {
		t.Fatal("expected an error for an unknown game")
	}
	// The available names belong in the message: a user who mistypes should not
	// have to run a second command to find out what is on offer.
	if !strings.Contains(err.Error(), "quake2") {
		t.Errorf("error does not list the available games: %v", err)
	}
}

func TestVerifyRejectsMissingAndNonExecutable(t *testing.T) {
	dir := t.TempDir()

	missing := Plan{Executable: filepath.Join(dir, "absent"), WorkingDir: dir}
	if err := missing.Verify(); err == nil {
		t.Error("expected an error for a missing executable")
	}

	if runtime.GOOS != "windows" {
		path := filepath.Join(dir, "not-executable")
		if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := (Plan{Executable: path, WorkingDir: dir}).Verify(); err == nil {
			t.Error("expected an error for a file with no execute bit")
		}
	}
}

// The AUB-backed provider must fail loudly rather than return a guessed shape.
func TestAUBProviderIsExplicitlyUnimplemented(t *testing.T) {
	provider := NewAUBProvider(nil)
	if _, err := provider.Configs(context.Background()); err == nil {
		t.Fatal("expected an error from the unconfigured AUB provider")
	}
}

func TestPlanString(t *testing.T) {
	plan := Plan{Executable: "/games/my quake/quakespasm", Args: []string{"+map", "e1m1"}}
	want := `"/games/my quake/quakespasm" +map e1m1`
	if got := plan.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
