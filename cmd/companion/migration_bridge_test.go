package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/release"
)

// A migration bridge is prepared by build/migration-bridge.py and by nothing else in a release. These
// drive that tool with stand-in programs and a stand-in historical release; that the unmodified older
// updater takes the result is a live observation, not something a test here could establish.

const (
	bridgeVersion  = "1.341"
	legacyVersion  = "1.336"
	bridgePlatform = "linux-amd64"
)

func runBridge(t *testing.T, args ...string) (string, error) {
	t.Helper()
	command := exec.Command(python(t), append([]string{filepath.Join(repoRoot(t), "build", "migration-bridge.py")}, args...)...)
	output, err := command.CombinedOutput()
	return string(output), err
}

// bridgeInputs is a historical release and a fresh Companion, with pins for that release.
type bridgeInputs struct {
	dir, pins, archive, manifest, companion, converter, notices, out string
}

func (i bridgeInputs) args(change map[string]string) []string {
	values := map[string]string{"--version": bridgeVersion, "--commit": testCommit, "--platform": bridgePlatform,
		"--companion": i.companion, "--converter": i.converter, "--notices": i.notices,
		"--library-version": release.ConverterLibraryVersion, "--library-commit": strings.Repeat("cd", 20),
		"--legacy-archive": i.archive, "--legacy-manifest": i.manifest, "--out": i.out, "--pins": i.pins}
	for key, value := range change {
		values[key] = value
	}
	args := []string{"build"}
	for key, value := range values {
		args = append(args, key, value)
	}
	return args
}

func newBridgeInputs(t *testing.T) bridgeInputs {
	t.Helper()
	i := bridgeInputs{dir: t.TempDir()}
	i.out = filepath.Join(i.dir, "out")
	legacy := filepath.Join(i.dir, "legacy", "auto-pigeon-companion-"+legacyVersion+"-"+bridgePlatform)
	extractor := filepath.Join(legacy, "dependencies", "auto-pigeon-extractor")
	writeFakeExecutable(t, extractor, bridgePlatform, "the historical extractor")
	body := fakeExecutable(bridgePlatform, "the historical extractor")
	file := map[string]any{"product": "auto-pigeon-extractor", "version": "1.245", "platform": bridgePlatform,
		"file": "dependencies/auto-pigeon-extractor", "license": "LicenseRef-Auto-Pigeon-Proprietary",
		"source_commit": strings.Repeat("e", 40)}
	writeJSON(t, filepath.Join(legacy, "dependencies", "bundle-manifest.json"), map[string]any{
		"schema_version": "aucom.bundle-manifest/1.2", "extractor": file,
		"members": []map[string]any{{"path": "dependencies/auto-pigeon-extractor", "product": "auto-pigeon-extractor",
			"version": "1.245", "platform": bridgePlatform, "size": len(body), "sha256": sha(body)}}})
	i.archive = filepath.Join(i.dir, "auto-pigeon-companion-"+legacyVersion+"-"+bridgePlatform+".zip")
	zipDir(t, filepath.Join(i.dir, "legacy"), i.archive, "")
	i.manifest = filepath.Join(i.dir, "release-manifest.json")
	writeJSON(t, i.manifest, map[string]any{"schema": "aucom.release-manifest/1.0", "tag": "v" + legacyVersion,
		"commit": testCommit, "aue": map[string]any{"version": "1.245", "protocol": "1.0",
			"commit": strings.Repeat("e", 40), "license": "LicenseRef-Auto-Pigeon-Proprietary"}})
	i.pins = filepath.Join(i.dir, "pins.json")
	writeJSON(t, i.pins, map[string]any{"schema": "aucom.migration-bridge-pins/1", "legacy": map[string]any{
		"version": legacyVersion, "tag": "v" + legacyVersion, "commit": testCommit,
		"release_manifest_sha256": sha([]byte(readText(t, i.manifest))),
		"targets": map[string]any{bridgePlatform: map[string]any{"archive": filepath.Base(i.archive),
			"sha256": sha([]byte(readText(t, i.archive))), "extractor_sha256": sha(body)}}}})
	i.companion, i.converter = filepath.Join(i.dir, "companion"), filepath.Join(i.dir, "auto-pigeon-mapio")
	writeFakeExecutable(t, i.companion, bridgePlatform, "aucom")
	writeFakeExecutable(t, i.converter, bridgePlatform, "mapio")
	i.notices = filepath.Join(i.dir, "notices.txt")
	writeFile(t, i.notices, "auto-pigeon-mapio\ngo/mapio "+release.ConverterLibraryVersion+"\nMIT License\nApache License\n")
	return i
}

func TestABridgeSaysWhatItIsInBothManifests(t *testing.T) {
	i := newBridgeInputs(t)
	if output, err := runBridge(t, i.args(nil)...); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	var document struct {
		Schema string `json:"schema"`
		AUE    struct {
			Version string `json:"version"`
		} `json:"aue"`
		Bridge struct {
			Kind          string `json:"kind"`
			RunsExtractor *bool  `json:"runs_extractor"`
		} `json:"migration_bridge"`
		Targets []struct {
			Verdict, Archive, SHA256 string
		} `json:"targets"`
	}
	if err := json.Unmarshal([]byte(readText(t, filepath.Join(i.out, "release-manifest.json"))), &document); err != nil {
		t.Fatal(err)
	}
	if document.Schema != "aucom.release-manifest/1.0" || document.AUE.Version != "1.245" ||
		document.Bridge.Kind != "aucom.migration-bridge/1" || document.Bridge.RunsExtractor == nil || *document.Bridge.RunsExtractor {
		t.Fatalf("release manifest: %+v", document)
	}

	unpacked := filepath.Join(t.TempDir(), "bridge")
	archive := filepath.Join(i.out, "auto-pigeon-companion-"+bridgeVersion+"-"+bridgePlatform+".zip")
	unzipDir(t, archive, unpacked)
	root := filepath.Join(unpacked, "auto-pigeon-companion-"+bridgeVersion+"-"+bridgePlatform)
	var bundle struct {
		Schema  string `json:"schema_version"`
		Members []struct {
			Path, Product string
		} `json:"members"`
		Licenses []struct {
			Product, SPDX string
		} `json:"licenses"`
		Bridge struct {
			Kind string `json:"kind"`
		} `json:"migration_bridge"`
	}
	if err := json.Unmarshal([]byte(readText(t, filepath.Join(root, "dependencies", "bundle-manifest.json"))), &bundle); err != nil {
		t.Fatal(err)
	}
	products := map[string]string{}
	for _, member := range bundle.Members {
		products[member.Path] = member.Product
	}
	if bundle.Schema != "aucom.bundle-manifest/1.2" || bundle.Bridge.Kind != "aucom.migration-bridge/1" ||
		products["dependencies/auto-pigeon-mapio"] != "auto-pigeon-mapio" ||
		products["dependencies/auto-pigeon-extractor"] != "auto-pigeon-extractor" {
		t.Fatalf("bundle manifest: %+v", bundle)
	}
	licences := map[string]string{}
	for _, licence := range bundle.Licenses {
		licences[licence.Product] = licence.SPDX
	}
	if licences["auto-pigeon-mapio"] != release.ConverterSPDX || licences["auto-pigeon-extractor"] != "LicenseRef-Auto-Pigeon-Proprietary" {
		t.Fatalf("licences: %+v", bundle.Licenses)
	}
	mustContain(t, "the bridge's notices", readText(t, filepath.Join(root, "dependencies", "NOTICES-migration-bridge.txt")),
		"It does not run auto-pigeon-extractor", "LicenseRef-Auto-Pigeon-Proprietary", "next ordinary update of this Companion removes it")

	// A bridge can never pass for a release.
	inputs := filepath.Join(i.dir, "inputs.json")
	writeJSON(t, inputs, map[string]any{"version": bridgeVersion, "commit": testCommit})
	if output, err := runPlan(t, "verify-archive", archive, "--platform", bridgePlatform, "--version", bridgeVersion,
		"--converter-inputs", inputs); err == nil || !strings.Contains(output, "ships no Auto-Pigeon Extractor") {
		t.Fatalf("a release accepted a bridge: %v\n%s", err, output)
	}
}

func TestABridgeRefusesWhatItCannotStandBehind(t *testing.T) {
	i := newBridgeInputs(t)
	other := filepath.Join(i.dir, "other", filepath.Base(i.archive))
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, other, readText(t, i.archive)+"tampered")
	wrongManifest := filepath.Join(i.dir, "wrong-manifest.json")
	writeFile(t, wrongManifest, readText(t, i.manifest)+" ")
	badNotices := filepath.Join(i.dir, "bad-notices.txt")
	writeFile(t, badNotices, "MIT License\n")
	wrongMachine := filepath.Join(i.dir, "wrong-machine")
	writeFakeExecutable(t, wrongMachine, "windows-amd64", "mapio")
	for name, c := range map[string]struct {
		change map[string]string
		says   string
	}{
		"a historical archive with other bytes":  {map[string]string{"--legacy-archive": other}, "is not the archive"},
		"a historical manifest with other bytes": {map[string]string{"--legacy-manifest": wrongManifest}, "is not the release manifest"},
		"a version an older Companion is not on": {map[string]string{"--version": legacyVersion}, "not newer"},
		"macOS, which is replaced by hand":       {map[string]string{"--platform": "darwin-arm64"}, "replaced by hand"},
		"a platform nothing is pinned for":       {map[string]string{"--platform": "linux-arm64"}, "pins no archive"},
		"notices about another library revision": {map[string]string{"--notices": badNotices}, "does not name"},
		"a converter for another machine":        {map[string]string{"--converter": wrongMachine}, "not built for"},
		"a short commit":                         {map[string]string{"--commit": "abc123"}, "full commit"},
	} {
		t.Run(name, func(t *testing.T) {
			if output, err := runBridge(t, i.args(c.change)...); err == nil || !strings.Contains(output, c.says) {
				t.Fatalf("%v\n%s", err, output)
			}
		})
	}
}

// The older evaluator reads six manifests, newest version first. A bridge below six newer releases is
// one nothing will ever reach, and a tag that exists is never replaced.
func TestABridgeIsPlannedByWhatAnOlderCompanionWouldReach(t *testing.T) {
	i := newBridgeInputs(t)
	releases := func(t *testing.T, tags ...int) string {
		t.Helper()
		var list []map[string]string
		for _, tag := range tags {
			list = append(list, map[string]string{"tag_name": fmt.Sprintf("v1.%d", tag)})
		}
		path := filepath.Join(t.TempDir(), "releases.json")
		writeJSON(t, path, list)
		return path
	}
	plan := func(t *testing.T, version string, tags ...int) (string, error) {
		t.Helper()
		return runBridge(t, "plan", "--version", version, "--releases", releases(t, tags...), "--pins", i.pins)
	}
	if output, err := plan(t, "1.341", 336, 342, 343, 344, 345, 346); err != nil || !strings.Contains(output, "read 6 of 6") {
		t.Fatalf("the last reachable place: %v\n%s", err, output)
	}
	if output, err := plan(t, "1.341", 336, 342, 343, 344, 345, 346, 347); err == nil || !strings.Contains(output, "never reach") {
		t.Fatalf("a bridge past the sixth read: %v\n%s", err, output)
	}
	if output, err := plan(t, "1.341", 336, 341); err == nil || !strings.Contains(output, "never replaced") {
		t.Fatalf("a tag that exists: %v\n%s", err, output)
	}
}

// No workflow makes one. A bridge carries a proprietary executable, so it can never be a step a push runs.
func TestNoWorkflowBuildsABridge(t *testing.T) {
	for _, workflow := range []string{"release.yml", "build.yml"} {
		if strings.Contains(repoFile(t, ".github", "workflows", workflow), "migration-bridge") {
			t.Errorf("%s runs the migration bridge", workflow)
		}
	}
	if strings.Contains(readText(t, filepath.Join(repoRoot(t), "build", "release-plan.py")), "migration-bridge") {
		t.Error("the release plan knows about a bridge")
	}
}
