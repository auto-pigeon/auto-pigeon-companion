package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The sidecar pin, and the three ways it must refuse.
//
// `AUCOM/AUE/AUT 246I1` allows the release to put the two SEPARATELY BUILT
// programs in one archive, and allows it only against an exact published
// version and an exact digest. These drive `build/bundle-manifest.py` the way
// the release workflow drives it, because the rules are in that script and a
// test that restated them in Go would be a second copy that could disagree.

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
func runBundleManifest(t *testing.T, bundle, pin string) (string, error) {
	t.Helper()

	command := exec.Command(python(t),
		filepath.Join(repoRoot(t), "build", "bundle-manifest.py"),
		"--platform", "linux-amd64", "--version", "1.999",
		"--bundle", bundle, "--pin", pin)
	output, err := command.CombinedOutput()

	return string(output), err
}

func writePin(t *testing.T, pin map[string]any) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "pin.json")
	encoded, err := json.Marshal(pin)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	return path
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

// The pin that ships is DISABLED, and says why. A pin filled in from a branch
// head would be the thing 246I1 forbids.
func TestTheSidecarPinIsDisabledAndSaysWhy(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "build", "sidecar-pin.json"))
	if err != nil {
		t.Fatal(err)
	}
	pin := map[string]any{}
	if err = json.Unmarshal(raw, &pin); err != nil {
		t.Fatal(err)
	}
	if pin["enabled"] != false {
		t.Fatal("the sidecar pin is enabled. It may only be enabled with an exact published " +
			"extractor version and the exact SHA-256 of each asset, and with the handoff that " +
			"says where those came from")
	}
	why, _ := pin["why_disabled"].(string)
	if len(strings.TrimSpace(why)) < 60 {
		t.Errorf("the pin says %q about why it is off, which is not a reason anybody can act on", why)
	}
	// And a disabled pin still produces a complete manifest that says the
	// extractor is not there and how it is obtained instead.
	bundle := newBundle(t)
	if output, err := runBundleManifest(t, bundle,
		filepath.Join(repoRoot(t), "build", "sidecar-pin.json")); err != nil {
		t.Fatalf("building a bundle with the shipped pin failed: %v\n%s", err, output)
	}
	manifest := map[string]any{}
	body, err := os.ReadFile(filepath.Join(bundle, "bundle-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest["extractor"] != nil {
		t.Errorf("a disabled pin still recorded an extractor: %v", manifest["extractor"])
	}
	absent, _ := manifest["extractor_absent"].(map[string]any)
	how, _ := absent["how_it_is_obtained"].(string)
	if !strings.Contains(how, "signed catalogue") || !strings.Contains(how, "managed install") {
		t.Errorf("the manifest does not say how the extractor IS obtained: %q", how)
	}
	// Every member is accounted for, with a digest.
	members, _ := manifest["members"].([]any)
	if len(members) < 1 {
		t.Fatalf("the manifest records no members; it must map every file in the bundle")
	}
	for _, entry := range members {
		member, _ := entry.(map[string]any)
		digest, _ := member["sha256"].(string)
		if !strings.HasPrefix(digest, "sha256:") {
			t.Errorf("%v has the digest %q", member["path"], digest)
		}
	}
}

// A mutable URL is refused. An asset behind `latest` is an asset nobody can
// state the contents of in advance.
func TestAMutableSidecarURLIsRefused(t *testing.T) {
	pin := writePin(t, map[string]any{
		"schema_version": "aucom.sidecar-pin/1.0", "enabled": true,
		"product": "auto-pigeon-extractor", "version": "1.171",
		"release_base_url": "https://example.test/releases/latest",
		"artifacts": []map[string]any{{
			"platform": "linux-amd64", "file": "auto-pigeon-extractor-1.171-linux-amd64",
			"sha256": "sha256:" + strings.Repeat("a", 64),
		}},
	})

	output, err := runBundleManifest(t, newBundle(t), pin)
	if err == nil {
		t.Fatal("a pin pointing at a `latest` url was accepted")
	}
	if !strings.Contains(output, "mutable url") {
		t.Errorf("the refusal does not say why: %s", output)
	}
}

// A digest mismatch is refused, and nothing is bundled.
func TestASidecarDigestMismatchBundlesNothing(t *testing.T) {
	payload := []byte("this is the extractor, allegedly")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	name := "auto-pigeon-extractor-1.171-linux-amd64"
	pin := writePin(t, map[string]any{
		"schema_version": "aucom.sidecar-pin/1.0", "enabled": true,
		"product": "auto-pigeon-extractor", "version": "1.171",
		"release_base_url":     server.URL + "/v1.171",
		"corresponding_source": "https://example.test/source",
		"artifacts": []map[string]any{{
			"platform": "linux-amd64", "file": name,
			"sha256": "sha256:" + strings.Repeat("b", 64),
		}},
	})

	bundle := newBundle(t)
	output, err := runBundleManifest(t, bundle, pin)
	if err == nil {
		t.Fatal("a sidecar whose bytes did not match the pin was bundled")
	}
	if !strings.Contains(output, "Nothing is bundled") {
		t.Errorf("the refusal does not say that nothing was bundled: %s", output)
	}
	if _, statErr := os.Stat(filepath.Join(bundle, name)); statErr == nil {
		t.Error("the downloaded file was left in the bundle after the digest failed")
	}
	if _, statErr := os.Stat(filepath.Join(bundle, "bundle-manifest.json")); statErr == nil {
		t.Error("a manifest was written for a bundle that was refused")
	}
}

// And a matching digest bundles the extractor as a SEPARATE FILE, under its own
// licence, named as itself.
func TestAPinnedSidecarIsBundledAsItself(t *testing.T) {
	payload := []byte("this is the extractor, verifiably")
	sum := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	name := "auto-pigeon-extractor-1.171-linux-amd64"
	pin := writePin(t, map[string]any{
		"schema_version": "aucom.sidecar-pin/1.0", "enabled": true,
		"product": "auto-pigeon-extractor", "version": "1.171",
		"release_base_url":     server.URL + "/v1.171",
		"corresponding_source": "https://example.test/source",
		"artifacts": []map[string]any{{
			"platform": "linux-amd64", "file": name,
			"sha256": "sha256:" + hex.EncodeToString(sum[:]),
		}},
	})

	bundle := newBundle(t)
	if output, err := runBundleManifest(t, bundle, pin); err != nil {
		t.Fatalf("a correctly pinned sidecar was refused: %v\n%s", err, output)
	}
	body, err := os.ReadFile(filepath.Join(bundle, "bundle-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{}
	if err = json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	sidecar, _ := manifest["extractor"].(map[string]any)
	if sidecar == nil {
		t.Fatal("the pinned extractor is not in the manifest")
	}
	if sidecar["version"] != "1.171" || sidecar["file"] != name {
		t.Errorf("the sidecar is recorded as %v", sidecar)
	}
	if sidecar["license"] != "AGPL-3.0-or-later" {
		t.Errorf("the sidecar's licence is %v; the two programs are under two licences and the "+
			"bundle has to say which is which", sidecar["license"])
	}
	// It is a separate file beside the Companion, not renamed and not inside it.
	if _, statErr := os.Stat(filepath.Join(bundle, name)); statErr != nil {
		t.Errorf("the extractor is not a file in the bundle: %v", statErr)
	}
	licences, _ := manifest["licenses"].([]any)
	if len(licences) != 2 {
		t.Errorf("the bundle declares %d licence(s) and carries two programs", len(licences))
	}
	// And the member list names it as the OTHER product, at the OTHER version.
	members, _ := manifest["members"].([]any)
	found := false
	for _, entry := range members {
		member, _ := entry.(map[string]any)
		if member["path"] == name {
			found = true
			if member["product"] != "auto-pigeon-extractor" || member["version"] != "1.171" {
				t.Errorf("the extractor member is recorded as %v", member)
			}
		}
	}
	if !found {
		t.Error("the extractor is not in the member list")
	}
}
