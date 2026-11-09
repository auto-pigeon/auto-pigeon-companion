package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// TestInstalledEricwBuildsAMapFromAutoPigeon is the compile boundary end to
// end, on a real compiler (NEW_434): a Quake 1 map served as an exact AUB
// revision is built by `companion build run` through the built-in Quake 1
// pipeline and the Auto-Pigeon EricW profile bound to the installed programs.
// The staged input must be the converter's canonical copy, every stage must
// succeed, and the same map built from a local file must succeed too.
//
// Skipped unless AUCOM_ERICW_BIN names the folder holding qbsp, vis and light,
// and AUCOM_ERICW_AUB_MAP a Quake 1 map:
//
//	AUCOM_ERICW_BIN=/path/auto-pigeon-ericw-tools-1.4556-linux-amd64/bin \
//	AUCOM_ERICW_AUB_MAP=/path/q1_fence.map go test ./internal/cli -run InstalledEricwBuilds -v
func TestInstalledEricwBuildsAMapFromAutoPigeon(t *testing.T) {
	bin, mapPath := os.Getenv("AUCOM_ERICW_BIN"), os.Getenv("AUCOM_ERICW_AUB_MAP")
	if bin == "" || mapPath == "" {
		t.Skip("AUCOM_ERICW_BIN / AUCOM_ERICW_AUB_MAP are not set; no installed compiler is exercised")
	}
	text, err := os.ReadFile(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	useRealConverter(t)
	backend := fakeCompanionAUBServing(t, "level.map", "text/plain", string(text))
	env, _, _ := signedInEnv(t, backend.URL)
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	executables := map[string]string{}
	for _, name := range []string{"qbsp", "vis", "light"} {
		executables[name] = filepath.Join(bin, name+suffix)
	}
	bindTool(t, env, builtin.EricwAutoPigeonQ1, executables, map[string]string{})

	type manifest struct {
		State  string `json:"state"`
		Error  string `json:"error"`
		Inputs []struct {
			Name   string          `json:"name"`
			Path   string          `json:"path"`
			Source json.RawMessage `json:"source"`
		} `json:"inputs"`
		Steps []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"steps"`
	}
	build := func(input string) manifest {
		t.Helper()
		env, stdout, stderr := testEnvAt(t, env.ConfigPath)
		if code := Run(env, []string{"build", "run", "--json", "--pipeline", builtin.Q1Normal,
			"--input", "source_map=" + input}); code != 0 {
			t.Fatalf("build run of %s exit %d\nstdout: %s\nstderr: %s", input, code, stdout, stderr)
		}
		var got manifest
		if err := json.Unmarshal(manifestJSON(t, stdout.String()), &got); err != nil {
			t.Fatalf("%v\n%s", err, stdout)
		}
		return got
	}

	fromAUB := build("aub:map/map0000000000001@rev0000000000002#level.map")
	if fromAUB.State != "succeeded" {
		t.Fatalf("the build of the map from Auto-Pigeon is %s: %s", fromAUB.State, fromAUB.Error)
	}
	for _, step := range fromAUB.Steps {
		t.Logf("from Auto-Pigeon: step %s %s", step.ID, step.State)
	}
	staged, err := os.ReadFile(fromAUB.Inputs[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(staged), "//") || len(fromAUB.Inputs[0].Source) == 0 {
		t.Fatalf("the staged input is not the canonical copy of an Auto-Pigeon map:\n%.400s", staged)
	}
	t.Logf("staged canonical copy: %d bytes from %d", len(staged), len(text))

	local := filepath.Join(t.TempDir(), "level.map")
	if err := os.WriteFile(local, text, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := build(local); got.State != "succeeded" {
		t.Fatalf("the local build is %s: %s", got.State, got.Error)
	}
}
