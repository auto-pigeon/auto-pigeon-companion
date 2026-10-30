package main

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The converter in the bundle, and the ways it must refuse.
//
// The release puts the two SEPARATELY BUILT programs in one archive: the
// converter as its own file beside the Companion, never inside it, and nothing
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

// converterArgs are the arguments that place a fake converter for platform.
func converterArgs(t *testing.T, platform string) []string {
	t.Helper()
	dir := t.TempDir()
	converter := filepath.Join(dir, "auto-pigeon-mapio-0.9.0-"+platform)
	writeFakeExecutable(t, converter, platform, "this is the converter")
	notices := filepath.Join(dir, "notices.txt")
	if err := os.WriteFile(notices, []byte("MIT License\nApache License\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	return []string{"--converter", converter, "--converter-version", "0.9.0",
		"--converter-commit", strings.Repeat("ab", 20),
		"--converter-source", "https://example.test/aucom/tree/" + strings.Repeat("ab", 20),
		"--converter-notices", notices,
		"--library-version", "v0.0.0-20261030000000-abababababab",
		"--library-commit", strings.Repeat("ab", 20)}
}

func readManifest(t *testing.T, bundle string) map[string]any {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(bundle, "dependencies", "bundle-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{}
	if err = json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}

	return manifest
}

// Without a converter the bundle is complete and says what is missing — and
// that nothing downloads it.
func TestABundleWithoutAConverterSaysSo(t *testing.T) {
	bundle := newBundle(t)
	if output, err := runBundleManifest(t, bundle); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	manifest := readManifest(t, bundle)
	if manifest["converter"] != nil {
		t.Errorf("converter = %v, want none", manifest["converter"])
	}
	absent, _ := manifest["converter_absent"].(map[string]any)
	consequence, _ := absent["consequence"].(string)
	if !strings.Contains(consequence, "downloads no program") {
		t.Errorf("converter_absent = %v", absent)
	}
}

// A converter without its version, its source, its notices, its library revision or the full commit
// it was built from is refused: the bundle must say which build it carries and where it came from.
func TestAConverterWithoutItsProvenanceOrNoticesIsRefused(t *testing.T) {
	without := func(flag string) []string {
		args := converterArgs(t, "linux-amd64")
		for index := range args {
			if args[index] == flag {
				return append(append([]string{}, args[:index]...), args[index+2:]...)
			}
		}
		t.Fatalf("no %s", flag)
		return nil
	}
	shortCommit := converterArgs(t, "linux-amd64")
	for index := range shortCommit {
		if shortCommit[index] == "--converter-commit" {
			shortCommit[index+1] = "86ac34d"
		}
	}
	for name, extra := range map[string][]string{
		"no version":         without("--converter-version"),
		"no source":          without("--converter-source"),
		"no commit":          without("--converter-commit"),
		"no notices":         without("--converter-notices"),
		"no library version": without("--library-version"),
		"no library commit":  without("--library-commit"),
		"short commit":       shortCommit,
	} {
		if output, err := runBundleManifest(t, newBundle(t), extra...); err == nil {
			t.Errorf("%s: accepted\n%s", name, output)
		}
	}
}

// The converter links an Apache-2.0 library: MIT alone would describe an archive carrying it as
// entirely MIT, and a licence without Apache-2.0 would drop the library's terms.
func TestAConverterMustBeListedUnderItsLibrarysLicence(t *testing.T) {
	for _, spdx := range []string{"MIT", "mit", "MIT-0", "MIT AND Apache-2.0", "LicenseRef-Auto-Pigeon-Proprietary"} {
		output, err := runBundleManifest(t, newBundle(t), append(converterArgs(t, "linux-amd64"),
			"--converter-spdx", spdx)...)
		if err == nil || !strings.Contains(output, "the converter is Apache-2.0 AND MIT") {
			t.Errorf("a converter declared %s: err = %v\n%s", spdx, err, output)
		}
	}
	bundle := newBundle(t)
	if output, err := runBundleManifest(t, bundle, converterArgs(t, "linux-amd64")...); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	sidecar, _ := readManifest(t, bundle)["converter"].(map[string]any)
	if sidecar["license"] != "Apache-2.0 AND MIT" {
		t.Errorf("the converter is recorded as %v", sidecar["license"])
	}
}

// A bundle pairs the two programs for ONE machine. A converter built for another platform — or a file
// whose header says nothing — is refused, and so is a Companion built for another platform than the
// bundle's (NEW_247A).
func TestAWrongPlatformConverterIsRefused(t *testing.T) {
	for _, platform := range []string{"linux-arm64", "windows-amd64", "darwin-amd64"} {
		output, err := runBundleManifest(t, newBundle(t), converterArgs(t, platform)...)
		if err == nil || !strings.Contains(output, "built for "+platform) {
			t.Errorf("a %s converter in a linux-amd64 bundle: err = %v\n%s", platform, err, output)
		}
	}
	notAProgram := converterArgs(t, "linux-amd64")
	if err := os.WriteFile(notAProgram[1], []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := runBundleManifest(t, newBundle(t), notAProgram...); err == nil {
		t.Errorf("a script with no executable header was bundled:\n%s", output)
	}
	bundle := newBundle(t)
	writeFakeExecutable(t, filepath.Join(bundle, "companion"), "linux-arm64", "companion")
	if output, err := runBundleManifest(t, bundle, converterArgs(t, "linux-amd64")...); err == nil {
		t.Errorf("a linux-arm64 Companion was bundled as linux-amd64:\n%s", output)
	}
}

// A given converter is bundled as a SEPARATE FILE, with its licences named and its notices beside it,
// under the name the Companion looks for beside itself.
func TestAConverterIsBundledBesideTheCompanionWithItsNotices(t *testing.T) {
	bundle := newBundle(t)
	if output, err := runBundleManifest(t, bundle, converterArgs(t, "linux-amd64")...); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	manifest := readManifest(t, bundle)
	sidecar, _ := manifest["converter"].(map[string]any)
	if sidecar == nil || sidecar["version"] != "0.9.0" || sidecar["file"] != "dependencies/auto-pigeon-mapio" ||
		sidecar["notices"] != "dependencies/NOTICES-auto-pigeon-mapio.txt" ||
		sidecar["source_commit"] != strings.Repeat("ab", 20) {
		t.Fatalf("converter = %v", sidecar)
	}
	library, _ := sidecar["library"].(map[string]any)
	if library["module"] != "github.com/auto-pigeon/auto-pigeon-libraries" || library["license"] != "Apache-2.0" {
		t.Fatalf("library = %v", library)
	}
	if _, err := os.Stat(filepath.Join(bundle, "dependencies", "NOTICES-auto-pigeon-mapio.txt")); err != nil {
		t.Errorf("the converter's notices are not in the bundle: %v", err)
	}
	// Windows has no executable bit to observe; the release composes its
	// bundles on Linux, where this is checked.
	if info, err := os.Stat(filepath.Join(bundle, "dependencies", "auto-pigeon-mapio")); err != nil ||
		(info.Mode().Perm()&0o111 == 0 && runtime.GOOS != "windows") {
		t.Errorf("the converter is not an executable file in the bundle: %v", err)
	}
	// Three licences for three kinds of thing: the Companion's own code (MIT), the AULIBS contract
	// files compiled into it (Apache-2.0), and the converter beside it (both).
	licences, _ := manifest["licenses"].([]any)
	if len(licences) != 3 {
		t.Errorf("the bundle declares %d licence(s); want the Companion's, AULIBS' and the converter's", len(licences))
	}
	byProduct := map[string]string{}
	for _, raw := range licences {
		entry, _ := raw.(map[string]any)
		product, _ := entry["product"].(string)
		spdx, _ := entry["spdx"].(string)
		byProduct[product] = spdx
	}
	if byProduct["auto-pigeon-companion"] != "MIT" || byProduct["auto-pigeon-mapio"] != "Apache-2.0 AND MIT" ||
		byProduct["auto-pigeon-libraries contract files compiled into the Companion"] != "Apache-2.0" {
		t.Errorf("the bundle's licences are %v", byProduct)
	}
	found := 0
	for _, entry := range manifest["members"].([]any) {
		member, _ := entry.(map[string]any)
		if member["path"] == "dependencies/auto-pigeon-mapio" || member["path"] == "dependencies/NOTICES-auto-pigeon-mapio.txt" {
			found++
			if member["product"] != "auto-pigeon-mapio" || member["version"] != "0.9.0" ||
				!strings.HasPrefix(member["sha256"].(string), "sha256:") {
				t.Errorf("a converter member is recorded as %v", member)
			}
		}
	}
	if found != 2 {
		t.Error("the converter and its notices are not both in the member list")
	}
}
