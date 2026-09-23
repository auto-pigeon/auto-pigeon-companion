package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The extractor in the bundle, and the ways it must refuse.
//
// The release puts the two SEPARATELY BUILT programs in one archive: the
// extractor as its own file beside the Companion, never inside it, and nothing
// downloads it. These drive `build/bundle-manifest.py` the way the release
// workflow drives it, because the rules are in that script and a test that
// restated them in Go would be a second copy that could disagree.

func repoRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	return root
}

func python(t *testing.T) string {
	t.Helper()

	path, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3 on this machine; the bundle manifest is built by a Python script")
	}

	return path
}

// runBundleManifest drives the script over a prepared bundle directory.
func runBundleManifest(t *testing.T, bundle string, extra ...string) (string, error) {
	t.Helper()

	args := append([]string{filepath.Join(repoRoot(t), "build", "bundle-manifest.py"),
		"--platform", "linux-amd64", "--version", "1.999", "--bundle", bundle}, extra...)
	output, err := exec.Command(python(t), args...).CombinedOutput()

	return string(output), err
}

func newBundle(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "bundle")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "companion"), []byte("not really a binary"), 0o600); err != nil {
		t.Fatal(err)
	}

	return dir
}

func readManifest(t *testing.T, bundle string) map[string]any {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(bundle, "bundle-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{}
	if err = json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}

	return manifest
}

// Without an extractor the bundle is complete and says what is missing — and
// that nothing downloads it.
func TestABundleWithoutAnExtractorSaysSo(t *testing.T) {
	bundle := newBundle(t)
	if output, err := runBundleManifest(t, bundle); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	manifest := readManifest(t, bundle)
	if manifest["extractor"] != nil {
		t.Errorf("extractor = %v, want none", manifest["extractor"])
	}
	absent, _ := manifest["extractor_absent"].(map[string]any)
	consequence, _ := absent["consequence"].(string)
	if !strings.Contains(consequence, "downloads no program") {
		t.Errorf("extractor_absent = %v", absent)
	}
}

// An extractor without its version or its source offer is refused: the bundle
// must say which build it carries and where the AGPL source is.
func TestAnExtractorWithoutVersionOrSourceIsRefused(t *testing.T) {
	extractor := filepath.Join(t.TempDir(), "aue")
	if err := os.WriteFile(extractor, []byte("aue"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, extra := range map[string][]string{
		"no version": {"--extractor", extractor, "--extractor-source", "https://example.test/src"},
		"no source":  {"--extractor", extractor, "--extractor-version", "0.9.0"},
	} {
		if output, err := runBundleManifest(t, newBundle(t), extra...); err == nil {
			t.Errorf("%s: accepted\n%s", name, output)
		}
	}
}

// A given extractor is bundled as a SEPARATE FILE, under its own licence, with
// the name the Companion looks for beside itself.
func TestAnExtractorIsBundledBesideTheCompanion(t *testing.T) {
	extractor := filepath.Join(t.TempDir(), "auto-pigeon-extractor-0.9.0-linux-amd64")
	if err := os.WriteFile(extractor, []byte("this is the extractor"), 0o700); err != nil {
		t.Fatal(err)
	}
	bundle := newBundle(t)
	if output, err := runBundleManifest(t, bundle, "--extractor", extractor,
		"--extractor-version", "0.9.0", "--extractor-source", "https://example.test/src"); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	manifest := readManifest(t, bundle)
	sidecar, _ := manifest["extractor"].(map[string]any)
	if sidecar == nil || sidecar["version"] != "0.9.0" || sidecar["file"] != "auto-pigeon-extractor" ||
		sidecar["license"] != "AGPL-3.0-only" {
		t.Fatalf("extractor = %v", sidecar)
	}
	if info, err := os.Stat(filepath.Join(bundle, "auto-pigeon-extractor")); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Errorf("the extractor is not an executable file in the bundle: %v", err)
	}
	if licences, _ := manifest["licenses"].([]any); len(licences) != 2 {
		t.Errorf("the bundle declares %d licence(s) and carries two programs", len(licences))
	}
	found := false
	for _, entry := range manifest["members"].([]any) {
		member, _ := entry.(map[string]any)
		if member["path"] == "auto-pigeon-extractor" {
			found = true
			if member["product"] != "auto-pigeon-extractor" || member["version"] != "0.9.0" ||
				!strings.HasPrefix(member["sha256"].(string), "sha256:") {
				t.Errorf("the extractor member is recorded as %v", member)
			}
		}
	}
	if !found {
		t.Error("the extractor is not in the member list, so the Companion could not check its digest")
	}
}
