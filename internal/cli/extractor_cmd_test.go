package cli

// The publishing half and the user's half of the extractor boundary, as
// commands. The packages behind them cover the mechanism; these cover the
// wiring — that a release manifest a build wrote is one `catalog release`
// composes from, that what it composes is signable, and that the commands a
// user reaches for when there is no extractor say something they can act on.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aue"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/catalog"
)

const exampleManifest = "../catalog/testdata/example-release-manifest.json"

// The publishing path, and what it produces is what `catalog sign` accepts.
// Two commands that agreed about nothing would be a procedure that is
// executable in one direction only.
func TestCatalogReleaseComposesDocumentsThatCatalogSignAccepts(t *testing.T) {
	env := acquireEnv(t)
	dir := filepath.Dir(env.ConfigPath)

	key := filepath.Join(dir, "catalog.key.json")
	if code, _, errOut := runCLI(t, env, "catalog", "keygen", "--role", "catalog", "--out", key); code != 0 {
		t.Fatalf("keygen exited %d: %s", code, errOut)
	}
	loaded, err := catalog.LoadPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	packagePath := filepath.Join(dir, "package.json")
	componentPath := filepath.Join(dir, "component.json")
	code, _, errOut := runCLI(t, env, "catalog", "release",
		"--manifest", exampleManifest,
		"--base-url", "https://releases.invalid/aue/0.0.0-example/",
		"--signer", loaded.KeyID,
		"--min-companion", "0.1.0",
		"--out-package", packagePath,
		"--out-component", componentPath)
	if code != 0 {
		t.Fatalf("catalog release exited %d: %s", code, errOut)
	}

	// The package, folded into a catalogue and signed for real.
	var pkg catalog.Package
	readJSON(t, packagePath, &pkg)
	if pkg.ID != aue.ComponentID || len(pkg.Artifacts) != 5 {
		t.Fatalf("package = %s with %d artifacts", pkg.ID, len(pkg.Artifacts))
	}
	if pkg.License.SPDX != "AGPL-3.0-only" || pkg.License.CorrespondingSource == "" {
		t.Errorf("licence = %+v", pkg.License)
	}
	document := &catalog.Catalog{
		SchemaVersion: catalog.SchemaVersion, CatalogID: "test", Serial: 1,
		IssuedAt: nowForTest(), ExpiresAt: nowForTest().AddDate(0, 1, 0),
		Packages: []catalog.Package{pkg},
	}
	catalogPath := filepath.Join(dir, "catalog.json")
	writeJSON(t, catalogPath, document)
	if code, _, errOut := runCLI(t, env, "catalog", "sign", "--key", key, "--out",
		filepath.Join(dir, "catalog.signed.json"), catalogPath); code != 0 {
		t.Fatalf("catalog sign exited %d: %s", code, errOut)
	}

	// And the compatibility component, likewise.
	var component catalog.Component
	readJSON(t, componentPath, &component)
	if component.Component != aue.ComponentID || len(component.Requirements) != 1 {
		t.Fatalf("component = %+v", component)
	}
	if component.Requirements[0].MinProtocol != "1.0" {
		t.Errorf("min_protocol = %q", component.Requirements[0].MinProtocol)
	}
	compatibility := catalog.NewCompatibility("test-compatibility", 1, nowForTest(), 30*24*hour)
	compatibility.Components = []catalog.Component{component}
	compatibilityPath := filepath.Join(dir, "compatibility.json")
	writeJSON(t, compatibilityPath, compatibility)

	if code, _, errOut := runCLI(t, env, "catalog", "sign", "--key", key, "--out",
		filepath.Join(dir, "compatibility.signed.json"), compatibilityPath); code != 0 {
		t.Fatalf("signing the compatibility manifest exited %d: %s", code, errOut)
	}
}

const hour = time.Hour

func nowForTest() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) }

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// The copyleft rule, at the command a publisher actually runs. It is refused
// before anything can sign it, which is the difference between a broken release
// and a broken release with a signature on it.
func TestCatalogReleaseRefusesAReleaseWithNoCorrespondingSource(t *testing.T) {
	env := acquireEnv(t)
	dir := filepath.Dir(env.ConfigPath)

	raw, err := os.ReadFile(exampleManifest)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["source"].(map[string]any)["corresponding_source"] = ""
	damaged := filepath.Join(dir, "no-source.json")
	writeJSON(t, damaged, document)

	code, _, errOut := runCLI(t, env, "catalog", "release",
		"--manifest", damaged, "--base-url", "https://releases.invalid/aue/",
		"--signer", "k-example", "--min-companion", "0.1.0")
	if code == 0 {
		t.Fatal("a copyleft release offering no corresponding source was accepted")
	}
	if !strings.Contains(errOut, "corresponding source") {
		t.Errorf("stderr = %q", errOut)
	}
}

// With nothing configured there is no extractor, and the status says so in a
// sentence rather than failing. A user asking "have I got one" gets an answer.
func TestExtractorStatusWithNothingConfiguredSaysThereIsNone(t *testing.T) {
	env := acquireEnv(t)

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
	if !strings.Contains(errOut, "status") || !strings.Contains(errOut, "install") {
		t.Errorf("stderr = %q", errOut)
	}
}
