package cli

// The user's half of the converter boundary, as commands: the commands a user
// reaches for when there is no converter say something they can act on.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/converter"
)

func TestConverterStatusWithNothingConfiguredSaysThereIsNone(t *testing.T) {
	env := acquireEnv(t)
	t.Setenv(converter.EnvBinaryOverride, "")
	t.Setenv(converter.RetiredEnvBinaryOverrides[0], "")

	code, out, errOut := runCLI(t, env, "converter", "status")
	if code != 0 {
		t.Fatalf("exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, "converter: none") {
		t.Errorf("stdout = %q", out)
	}
}

// The override is reported as unverified wherever it appears, and the sentence
// is the whole of what a user needs to know about it.
func TestConverterStatusReportsAnOverrideAsUnverified(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits")
	}
	env := acquireEnv(t)
	path := filepath.Join(t.TempDir(), "my-aue")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(converter.EnvBinaryOverride, path)

	code, out, errOut := runCLI(t, env, "converter", "status")
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
func TestConverterRefusesAnUnknownSubcommand(t *testing.T) {
	env := acquireEnv(t)

	code, _, errOut := runCLI(t, env, "converter", "reticulate")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "status") || !strings.Contains(errOut, "version") {
		t.Errorf("stderr = %q", errOut)
	}
}

// `install` and `plan` are gone, and say why instead of "unknown".
func TestConverterInstallSaysNothingDownloadsIt(t *testing.T) {
	env := acquireEnv(t)

	code, _, errOut := runCLI(t, env, "converter", "install")
	if code != 2 || !strings.Contains(errOut, "nothing downloads it") {
		t.Errorf("exit = %d, stderr = %q", code, errOut)
	}
}

// `converter convert` goes through the build's own conversion path: the
// release fixture reaches the extractor as `convert --apmap-to-q1map --input
// <it> --output converted-<name>/`, and the .map it wrote is printed with its
// digest and the provenance of what wrote it (NEW_247A).
func TestConverterConvertRunsTheBuildsConversionPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake converter is a shell script")
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
	// The fake writes the .map the way the converter does: into --output, named
	// after --input. $2 is the direction.
	fake := filepath.Join(t.TempDir(), "converter")
	script := "#!/bin/sh\n[ \"$1\" = convert ] || exit 9\n[ \"$2\" = --apmap-to-q1map ] || exit 8\n" +
		"echo '{\"classname\" \"worldspawn\"}' > \"$6/slab.map\"\necho '{}'\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(converter.EnvBinaryOverride, fake)

	code, out, errOut := runCLI(t, env, "converter", "convert", input)
	if code != 0 {
		t.Fatalf("exited %d: %s", code, errOut)
	}
	want := filepath.Join(filepath.Dir(input), "converted-slab", "slab.map")
	if !strings.HasPrefix(out, want+"\n") || !strings.Contains(out, "sha256:") || !strings.Contains(out, "UNVERIFIED") {
		t.Errorf("stdout = %q, want %s, its digest and an UNVERIFIED override", out, want)
	}

	if code, _, _ := runCLI(t, env, "converter", "convert", "slab.map"); code != 2 {
		t.Errorf("a non-APMap input exited %d, want 2", code)
	}
}

// `companion extractor` is the former spelling. It runs the same converter, and what it reports is the
// converter: an alias must never suggest the Auto-Pigeon Extractor is what runs.
func TestTheExtractorCommandIsAnAliasOfTheConverter(t *testing.T) {
	t.Setenv(converter.EnvBinaryOverride, "")
	for _, name := range converter.RetiredEnvBinaryOverrides {
		t.Setenv(name, "")
	}
	env := acquireEnv(t)
	code, out, _ := runCLI(t, env, "extractor", "status")
	if code != 0 || !strings.Contains(out, "converter: none") {
		t.Fatalf("exit %d, %q", code, out)
	}
}

// A setting that named a full extractor is told what happened, and is not run.
func TestARetiredOverrideEarnsAMigrationMessageAndChangesNothing(t *testing.T) {
	t.Setenv(converter.EnvBinaryOverride, "")
	t.Setenv(converter.RetiredEnvBinaryOverrides[0], "/some/auto-pigeon-extractor")
	env := acquireEnv(t)
	code, out, errOut := runCLI(t, env, "converter", "status")
	if code != 0 || !strings.Contains(out, "converter: none") {
		t.Fatalf("exit %d, %q", code, out)
	}
	if !strings.Contains(errOut, "no longer has any effect") || !strings.Contains(errOut, converter.EnvBinaryOverride) {
		t.Fatalf("no migration message: %q", errOut)
	}
}
