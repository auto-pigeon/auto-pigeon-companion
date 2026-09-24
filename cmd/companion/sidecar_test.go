package main

import (
	"encoding/binary"
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
	writeFakeExecutable(t, filepath.Join(dir, "companion"), "linux-amd64", "companion")

	return dir
}

// fakeExecutable is the header of an executable for one platform and nothing
// after it: enough for the bundle step, which reads the header to refuse a
// program paired with the wrong machine, and never runs it. The tag keeps two
// fakes' digests apart.
func fakeExecutable(platform, tag string) []byte {
	head := make([]byte, 128)
	switch platform {
	case "linux-amd64", "linux-arm64":
		copy(head, "\x7fELF\x02\x01\x01")
		machine := uint16(0x3E)
		if platform == "linux-arm64" {
			machine = 0xB7
		}
		binary.LittleEndian.PutUint16(head[18:], machine)
	case "darwin-amd64", "darwin-arm64":
		copy(head, "\xcf\xfa\xed\xfe")
		cpu := uint32(0x01000007)
		if platform == "darwin-arm64" {
			cpu = 0x0100000C
		}
		binary.LittleEndian.PutUint32(head[4:], cpu)
	case "windows-amd64", "windows-arm64":
		copy(head, "MZ")
		binary.LittleEndian.PutUint32(head[0x3C:], 0x40)
		copy(head[0x40:], "PE\x00\x00")
		machine := uint16(0x8664)
		if platform == "windows-arm64" {
			machine = 0xAA64
		}
		binary.LittleEndian.PutUint16(head[0x44:], machine)
	}

	return append(head, []byte(tag)...)
}

func writeFakeExecutable(t *testing.T, path, platform, tag string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fakeExecutable(platform, tag), 0o755); err != nil {
		t.Fatal(err)
	}
}

// extractorArgs are the arguments that place a fake linux-amd64 extractor.
func extractorArgs(t *testing.T, platform string) []string {
	t.Helper()
	dir := t.TempDir()
	extractor := filepath.Join(dir, "auto-pigeon-extractor-0.9.0-"+platform)
	writeFakeExecutable(t, extractor, platform, "this is the extractor")
	license := filepath.Join(dir, "LICENSE")
	if err := os.WriteFile(license, []byte("the extractor's licence\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return []string{"--extractor", extractor, "--extractor-version", "0.9.0",
		"--extractor-commit", strings.Repeat("ab", 20), "--extractor-license", license,
		"--extractor-source", "https://example.test/aue/tree/" + strings.Repeat("ab", 20)}
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

// An extractor without its version, its source, its licence file or the full
// commit it was built from is refused: the bundle must say which build it
// carries, where it came from and whose terms it is under.
func TestAnExtractorWithoutVersionSourceLicenceOrCommitIsRefused(t *testing.T) {
	without := func(flag string) []string {
		args := extractorArgs(t, "linux-amd64")
		for index := range args {
			if args[index] == flag {
				return append(append([]string{}, args[:index]...), args[index+2:]...)
			}
		}
		t.Fatalf("no %s", flag)
		return nil
	}
	shortCommit := extractorArgs(t, "linux-amd64")
	for index := range shortCommit {
		if shortCommit[index] == "--extractor-commit" {
			shortCommit[index+1] = "86ac34d"
		}
	}
	for name, extra := range map[string][]string{
		"no version":   without("--extractor-version"),
		"no source":    without("--extractor-source"),
		"no licence":   without("--extractor-license"),
		"no commit":    without("--extractor-commit"),
		"short commit": shortCommit,
	} {
		if output, err := runBundleManifest(t, newBundle(t), extra...); err == nil {
			t.Errorf("%s: accepted\n%s", name, output)
		}
	}
}

// The extractor is never MIT (NEW_247G): an archive listing it so would read as
// entirely MIT. The proprietary identifier is accepted, and so is the AGPL one
// an earlier extractor build declares — its copies keep that licence.
func TestAnExtractorDeclaredMITIsRefusedAndItsOwnLicenceIsKept(t *testing.T) {
	for _, spdx := range []string{"MIT", "mit", "MIT-0"} {
		output, err := runBundleManifest(t, newBundle(t), append(extractorArgs(t, "linux-amd64"),
			"--extractor-spdx", spdx)...)
		if err == nil || !strings.Contains(output, "the extractor is not MIT") {
			t.Errorf("an extractor declared %s: err = %v\n%s", spdx, err, output)
		}
	}
	for _, spdx := range []string{"LicenseRef-Auto-Pigeon-Proprietary", "AGPL-3.0-only"} {
		bundle := newBundle(t)
		if output, err := runBundleManifest(t, bundle, append(extractorArgs(t, "linux-amd64"),
			"--extractor-spdx", spdx)...); err != nil {
			t.Errorf("an extractor declared %s was refused: %v\n%s", spdx, err, output)
			continue
		}
		sidecar, _ := readManifest(t, bundle)["extractor"].(map[string]any)
		if sidecar["license"] != spdx {
			t.Errorf("an extractor declared %s is recorded as %v", spdx, sidecar["license"])
		}
	}
}

// A bundle pairs the two programs for ONE machine. An extractor built for
// another platform — or a file whose header says nothing — is refused, and so
// is a Companion built for another platform than the bundle's (NEW_247A).
func TestAWrongPlatformExtractorIsRefused(t *testing.T) {
	for _, platform := range []string{"linux-arm64", "windows-amd64", "darwin-amd64"} {
		output, err := runBundleManifest(t, newBundle(t), extractorArgs(t, platform)...)
		if err == nil || !strings.Contains(output, "built for "+platform) {
			t.Errorf("a %s extractor in a linux-amd64 bundle: err = %v\n%s", platform, err, output)
		}
	}
	notAProgram := extractorArgs(t, "linux-amd64")
	if err := os.WriteFile(notAProgram[1], []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := runBundleManifest(t, newBundle(t), notAProgram...); err == nil {
		t.Errorf("a script with no executable header was bundled:\n%s", output)
	}
	bundle := newBundle(t)
	writeFakeExecutable(t, filepath.Join(bundle, "companion"), "linux-arm64", "companion")
	if output, err := runBundleManifest(t, bundle, extractorArgs(t, "linux-amd64")...); err == nil {
		t.Errorf("a linux-arm64 Companion was bundled as linux-amd64:\n%s", output)
	}
}

// A given extractor is bundled as a SEPARATE FILE, with its own licence file,
// under the name the Companion looks for beside itself.
func TestAnExtractorIsBundledBesideTheCompanion(t *testing.T) {
	bundle := newBundle(t)
	if output, err := runBundleManifest(t, bundle, append(extractorArgs(t, "linux-amd64"),
		"--extractor-spdx", "LicenseRef-test")...); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	manifest := readManifest(t, bundle)
	sidecar, _ := manifest["extractor"].(map[string]any)
	if sidecar == nil || sidecar["version"] != "0.9.0" || sidecar["file"] != "auto-pigeon-extractor" ||
		sidecar["license"] != "LicenseRef-test" || sidecar["source_commit"] != strings.Repeat("ab", 20) {
		t.Fatalf("extractor = %v", sidecar)
	}
	if _, err := os.Stat(filepath.Join(bundle, "LICENSE-auto-pigeon-extractor.txt")); err != nil {
		t.Errorf("the extractor's licence file is not in the bundle: %v", err)
	}
	if info, err := os.Stat(filepath.Join(bundle, "auto-pigeon-extractor")); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Errorf("the extractor is not an executable file in the bundle: %v", err)
	}
	// Three licences for three kinds of thing: the Companion's own code (MIT),
	// the AULIBS contract files compiled into it (Apache-2.0), and the
	// extractor beside it (whatever it declares — never MIT).
	licences, _ := manifest["licenses"].([]any)
	if len(licences) != 3 {
		t.Errorf("the bundle declares %d licence(s); want the Companion's, AULIBS' and the extractor's", len(licences))
	}
	byProduct := map[string]string{}
	for _, raw := range licences {
		entry, _ := raw.(map[string]any)
		product, _ := entry["product"].(string)
		spdx, _ := entry["spdx"].(string)
		byProduct[product] = spdx
	}
	if byProduct["auto-pigeon-companion"] != "MIT" || byProduct["auto-pigeon-extractor"] != "LicenseRef-test" ||
		byProduct["auto-pigeon-libraries contract files compiled into the Companion"] != "Apache-2.0" {
		t.Errorf("the bundle's licences are %v", byProduct)
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
