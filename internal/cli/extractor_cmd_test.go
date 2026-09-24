package cli

// The user's half of the extractor boundary, as commands: the commands a user
// reaches for when there is no extractor say something they can act on.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aue"
)

func TestExtractorStatusWithNothingConfiguredSaysThereIsNone(t *testing.T) {
	env := acquireEnv(t)
	t.Setenv(aue.EnvBinaryOverride, "")
	t.Setenv(aue.LegacyEnvBinaryOverride, "")

	code, out, errOut := runCLI(t, env, "extractor", "status")
	if code != 0 {
		t.Fatalf("exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, "extractor: none") {
		t.Errorf("stdout = %q", out)
	}
}

// The override is reported as unverified wherever it appears, and the sentence
// is the whole of what a user needs to know about it.
func TestExtractorStatusReportsAnOverrideAsUnverified(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits")
	}
	env := acquireEnv(t)
	path := filepath.Join(t.TempDir(), "my-aue")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(aue.EnvBinaryOverride, path)

	code, out, errOut := runCLI(t, env, "extractor", "status")
	if code != 0 {
		t.Fatalf("exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, "UNVERIFIED") || !strings.Contains(out, path) {
		t.Errorf("stdout = %q", out)
	}
	if !strings.Contains(out, "never uploaded or published") {
		t.Errorf("the status does not say what an override is for: %q", out)
	}
}

// An unknown subcommand is exit 2 and prints the ones there are.
func TestExtractorRefusesAnUnknownSubcommand(t *testing.T) {
	env := acquireEnv(t)

	code, _, errOut := runCLI(t, env, "extractor", "reticulate")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "status") || !strings.Contains(errOut, "version") {
		t.Errorf("stderr = %q", errOut)
	}
}

// `install` and `plan` are gone, and say why instead of "unknown".
func TestExtractorInstallSaysNothingDownloadsIt(t *testing.T) {
	env := acquireEnv(t)

	code, _, errOut := runCLI(t, env, "extractor", "install")
	if code != 2 || !strings.Contains(errOut, "nothing downloads it") {
		t.Errorf("exit = %d, stderr = %q", code, errOut)
	}
}

// `extractor convert` goes through the build's own conversion path: the
// release fixture reaches the extractor as `convert --apmap-to-q1map --input
// <it> --output converted-<name>/`, and the .map it wrote is printed with its
// digest and the provenance of what wrote it (NEW_247A).
func TestExtractorConvertRunsTheBuildsConversionPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake extractor is a shell script")
	}
	env := acquireEnv(t)
	fixture, err := os.ReadFile(filepath.Join("..", "..", "build", "release-fixtures", "release-acceptance.apmap"))
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "slab.apmap")
	if err := os.WriteFile(input, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	// The fake writes the .map the way the extractor does: into --output, named
	// after --input. $2 is the direction.
	fake := filepath.Join(t.TempDir(), "aue")
	script := "#!/bin/sh\n[ \"$1\" = convert ] || exit 9\n[ \"$2\" = --apmap-to-q1map ] || exit 8\n" +
		"echo '{\"classname\" \"worldspawn\"}' > \"$6/slab.map\"\necho '{}'\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(aue.EnvBinaryOverride, fake)

	code, out, errOut := runCLI(t, env, "extractor", "convert", input)
	if code != 0 {
		t.Fatalf("exited %d: %s", code, errOut)
	}
	want := filepath.Join(filepath.Dir(input), "converted-slab", "slab.map")
	if !strings.HasPrefix(out, want+"\n") || !strings.Contains(out, "sha256:") || !strings.Contains(out, "UNVERIFIED") {
		t.Errorf("stdout = %q, want %s, its digest and an UNVERIFIED override", out, want)
	}

	if code, _, _ := runCLI(t, env, "extractor", "convert", "slab.map"); code != 2 {
		t.Errorf("a non-APMap input exited %d, want 2", code)
	}
}
