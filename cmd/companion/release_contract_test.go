package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/converter"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/release"
)

// The per-push bundled release (NEW_247A, NEW_371), held to the failures that
// matter: a movable revision, drifting target lists, an archive missing a program
// or its notices, a wrong-platform converter, disagreeing versions, a download
// path, write permission on the wrong job, a build-only candidate published as a
// download, and a rerun that replaces bytes silently.
//
// The decisions live in build/release-plan.py and the bundle rules in
// build/bundle-manifest.py; these tests DRIVE them rather than restating
// their rules in Go, which would be a second copy that could disagree.

func runPlan(t *testing.T, args ...string) (string, error) {
	t.Helper()
	command := exec.Command(python(t), append([]string{filepath.Join(repoRoot(t), "build", "release-plan.py")}, args...)...)
	output, err := command.CombinedOutput()

	return string(output), err
}

func mustPlan(t *testing.T, args ...string) string {
	t.Helper()
	output, err := runPlan(t, args...)
	if err != nil {
		t.Fatalf("release-plan.py %s: %v\n%s", strings.Join(args, " "), err, output)
	}

	return output
}

func writeJSON(t *testing.T, path string, document any) {
	t.Helper()
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sha(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// What the converter is built from

// The converter's source is this repository plus one exact revision of a public module, and go.mod is
// the one place that revision is written, so one commit of this repository names one converter.
func TestTheConverterIsBuiltFromTheRevisionGoModPins(t *testing.T) {
	var build struct {
		Module           string `json:"library_module"`
		RequiredProtocol string `json:"required_protocol"`
		License          string `json:"license"`
	}
	if err := json.Unmarshal([]byte(repoFile(t, "build", "converter.json")), &build); err != nil {
		t.Fatal(err)
	}
	if build.RequiredProtocol != converter.RequiredProtocol {
		t.Errorf("the build requires protocol %s and this Companion drives %s", build.RequiredProtocol, converter.RequiredProtocol)
	}
	if build.License != release.ConverterSPDX {
		t.Errorf("the build says %q and the component list says %q", build.License, release.ConverterSPDX)
	}
	if build.Module != release.ConverterLibraryModule {
		t.Errorf("the build names %s and the component list %s", build.Module, release.ConverterLibraryModule)
	}
	output := mustPlan(t, "pin")
	mustContain(t, "release-plan.py pin", output, "library_version="+release.ConverterLibraryVersion)

	for name, change := range map[string]func(map[string]any){
		"a commit written here":  func(d map[string]any) { d["commit"] = strings.Repeat("ab", 20) },
		"a version written here": func(d map[string]any) { d["version"] = "1.214" },
		"a branch to follow":     func(d map[string]any) { d["ref"] = "main" },
		"MIT alone":              func(d map[string]any) { d["license"] = "MIT" },
		"another protocol shape": func(d map[string]any) { d["required_protocol"] = "1" },
		"no repository":          func(d map[string]any) { delete(d, "library_repository") },
	} {
		document := map[string]any{}
		if err := json.Unmarshal([]byte(repoFile(t, "build", "converter.json")), &document); err != nil {
			t.Fatal(err)
		}
		change(document)
		path := filepath.Join(t.TempDir(), "converter.json")
		writeJSON(t, path, document)
		if output, err := runPlan(t, "pin", "--build", path); err == nil {
			t.Errorf("%s was accepted:\n%s", name, output)
		}
	}

	// A sibling checkout standing in for the revision is what a clean checkout could not build.
	goMod := filepath.Join(t.TempDir(), "go.mod")
	writeFile(t, goMod, repoFile(t, "go.mod")+"\nreplace "+build.Module+" => ../auto-pigeon-libraries\n")
	if output, err := runPlan(t, "pin", "--go-mod", goMod); err == nil || !strings.Contains(output, "replaces") {
		t.Errorf("a replace directive was accepted: %v\n%s", err, output)
	}
}

// ---------------------------------------------------------------------------
// One target list, derived — never a second one in a workflow

func TestTheReleaseWorkflowCarriesNoTargetListOfItsOwn(t *testing.T) {
	workflow := repoFile(t, ".github", "workflows", "release.yml")
	built, err := release.BuiltTargets()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range append(built, "goos:", "GOOS=", "GOARCH=") {
		for _, spelling := range []string{target, strings.ReplaceAll(target, "/", "-")} {
			if strings.Contains(workflow, spelling) {
				t.Errorf("release.yml names %q; its targets come from native-support.json and "+
					"build/converter.json, through build/release-plan.py", spelling)
			}
		}
	}
	mustContain(t, "release.yml", workflow,
		"release-plan.py matrix", "fromJSON(needs.bundle.outputs.acceptance)", "./build/release.sh")

	// build.yml's cross-build list is the declared one, exactly.
	pattern := regexp.MustCompile(`\{ goos: (\w+), goarch: (\w+)`)
	var fromBuild []string
	for _, match := range pattern.FindAllStringSubmatch(repoFile(t, ".github", "workflows", "build.yml"), -1) {
		fromBuild = append(fromBuild, match[1]+"/"+match[2])
	}
	sort.Strings(fromBuild)
	if strings.Join(fromBuild, ",") != strings.Join(built, ",") {
		t.Errorf("build.yml cross-builds %v and native-support.json declares %v", fromBuild, built)
	}

	// Every native runner the plan names is for a declared target, and no
	// runner is claimed for an architecture it is not.
	plan := repoFile(t, "build", "release-plan.py")
	runners := regexp.MustCompile(`"(\w+-\w+)": "([\w.-]+)",`).FindAllStringSubmatch(
		plan[strings.Index(plan, "NATIVE_RUNNERS = {"):], -1)
	if len(runners) < 3 {
		t.Fatalf("NATIVE_RUNNERS names %d runners; one per operating-system family is the minimum", len(runners))
	}
	for _, runner := range runners[:3] {
		target := strings.Replace(runner[1], "-", "/", 1)
		if sort.SearchStrings(built, target) == len(built) || built[sort.SearchStrings(built, target)] != target {
			t.Errorf("NATIVE_RUNNERS names %s, which is not a declared target", target)
		}
		if strings.HasSuffix(target, "arm64") != strings.Contains(runner[2], "macos") {
			t.Errorf("%s runs on %s: a hosted x64 runner is never a native arm64 host", target, runner[2])
		}
	}
}

func supportJSON(t *testing.T) string {
	t.Helper()
	rows, err := release.Rows()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "support.json")
	writeJSON(t, path, map[string]any{"schema": release.SupportSchema, "platforms": rows})

	return path
}

type matrixDocument struct {
	Targets []struct {
		Target   string `json:"target"`
		Platform string `json:"platform"`
		Verdict  string `json:"verdict"`
		Reason   string `json:"reason"`
	} `json:"targets"`
}

func TestTheTargetJoinGivesEveryTargetAVerdict(t *testing.T) {
	support := supportJSON(t)
	out := filepath.Join(t.TempDir(), "matrix.json")
	mustPlan(t, "matrix", "--aucom-support", support, "--out", out)
	var matrix matrixDocument
	if err := json.Unmarshal([]byte(readText(t, out)), &matrix); err != nil {
		t.Fatal(err)
	}
	built, _ := release.BuiltTargets()
	if len(matrix.Targets) != len(built) {
		t.Fatalf("the join has %d targets and AUCOM declares %d; none may be dropped", len(matrix.Targets), len(built))
	}
	for _, entry := range matrix.Targets {
		want := "bundled_release"
		if entry.Target == "windows/arm64" {
			// Until a native Windows-arm64 job has run the converter.
			want = "build_only"
		}
		if entry.Verdict != want {
			t.Errorf("%s: %s, want %s (%s)", entry.Target, entry.Verdict, want, entry.Reason)
		}
	}
	accept := mustPlan(t, "acceptance-matrix", "--matrix", out)
	for _, platform := range []string{"linux-amd64", "windows-amd64", "darwin-arm64"} {
		if !strings.Contains(accept, `"platform":"`+platform+`"`) {
			t.Errorf("no native acceptance for %s: %s", platform, accept)
		}
	}

	// A build-only entry naming a target nobody declares is a typo, not a verdict.
	document := map[string]any{}
	if err := json.Unmarshal([]byte(repoFile(t, "build", "converter.json")), &document); err != nil {
		t.Fatal(err)
	}
	document["build_only"].(map[string]any)["plan9/amd64"] = "a typo"
	path := filepath.Join(t.TempDir(), "converter.json")
	writeJSON(t, path, document)
	if output, err := runPlan(t, "matrix", "--build", path, "--aucom-support", support, "--out", out); err == nil ||
		!strings.Contains(output, "does not declare") {
		t.Errorf("an unknown build-only target was accepted: %v\n%s", err, output)
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(body)
}

// ---------------------------------------------------------------------------
// A whole release, end to end, with stand-in programs

const testVersion = "1.500"

var testCommit = strings.Repeat("ab", 20)

// realConverter builds cmd/mapio once per test binary for each platform a stand-in release holds. They
// are the real thing because the release reads what the toolchain recorded about them and runs the
// host's to ask what it speaks; a fake would prove nothing about either.
func realConverter(platform string) (string, error) {
	realConverters.Lock()
	defer realConverters.Unlock()
	if path, built := realConverters.built[platform]; built {
		return path, nil
	}
	if realConverters.built == nil {
		dir, err := os.MkdirTemp("", "aucom-converters-")
		if err != nil {
			return "", err
		}
		realConverters.dir, realConverters.built = dir, map[string]string{}
	}
	goos, goarch, _ := strings.Cut(platform, "-")
	path := filepath.Join(realConverters.dir, platform, converterName(platform))
	command := exec.Command("go", "build", "-trimpath", "-o", path, "./cmd/mapio")
	command.Dir = filepath.Join("..", "..")
	command.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building the converter for %s: %v\n%s", platform, err, output)
	}
	realConverters.built[platform] = path
	return path, nil
}

var realConverters struct {
	sync.Mutex
	dir   string
	built map[string]string
}

func converterName(platform string) string {
	if strings.HasPrefix(platform, "windows-") {
		return "auto-pigeon-mapio.exe"
	}
	return "auto-pigeon-mapio"
}

// standInPlatforms are the targets every stand-in release holds.
func standInPlatforms() []string {
	return []string{"linux-amd64", "darwin-arm64", "windows-arm64"}
}

// world is a fake AUCOM dist with real converters beside it, for three targets, linux-amd64 and
// darwin-arm64 released and windows-arm64 build-only, plus any more it was asked for. released lists
// the platforms whose archives the release publishes.
type world struct {
	dir, dist, matrix, inputs, bundles, release string
	released                                    []string
}

// newWorld holds the machine running this test as well, because the release asks the one converter it
// can execute what it speaks, so that machine has to be a target.
func newWorld(t *testing.T) world {
	t.Helper()
	host := runtime.GOOS + "-" + runtime.GOARCH
	if slices.Contains(standInPlatforms(), host) {
		return worldHolding(t)
	}
	return worldHolding(t, host)
}

// stageCompanion puts a stand-in Companion where build/releaselib.py's layout says a bundle for that
// platform holds it, so this fixture keeps no second copy of the rule that names the file.
func stageCompanion(t *testing.T, stage, platform string) {
	t.Helper()
	script := `import os, sys
sys.path.insert(0, sys.argv[1])
import releaselib
stage, platform = sys.argv[2], sys.argv[3]
if platform.startswith("darwin-"):
    os.makedirs(os.path.join(stage, releaselib.APP_NAME))
print(releaselib.layout(stage, platform)["companion"])
`
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(python(t), "-B", "-c", script, filepath.Join(repoRoot(t), "build"), stage, platform).CombinedOutput()
	if err != nil {
		t.Fatalf("the layout for %s: %v\n%s", platform, err, output)
	}
	writeFakeExecutable(t, filepath.Join(stage, filepath.FromSlash(strings.TrimSpace(string(output)))), platform, "aucom")
}

// packCompanion is the archive build/release.sh leaves for a platform: a tar.gz on Linux and a zip on
// the others.
func packCompanion(t *testing.T, w world, platform string) {
	t.Helper()
	name := filepath.Join(w.dist, "auto-pigeon-companion-"+testVersion+"-"+platform)
	if strings.HasPrefix(platform, "linux-") {
		writeTarGz(t, name+".tar.gz", map[string][]byte{"companion": fakeExecutable(platform, "aucom")})
		return
	}
	stage := filepath.Join(w.dir, "stage", platform)
	stageCompanion(t, stage, platform)
	zipDir(t, stage, name+".zip", "")
}

func worldHolding(t *testing.T, extra ...string) world {
	t.Helper()
	w := world{dir: t.TempDir(), released: []string{"linux-amd64", "darwin-arm64"}}
	w.dist = filepath.Join(w.dir, "dist")
	w.matrix, w.inputs = filepath.Join(w.dir, "matrix.json"), filepath.Join(w.dir, "converter-inputs.json")
	w.bundles, w.release = filepath.Join(w.dir, "bundles"), filepath.Join(w.dir, "release")

	rows := []map[string]any{
		{"target": "linux/amd64", "built": true, "state": "native_pass"},
		{"target": "darwin/arm64", "built": true, "state": "build_only"},
		{"target": "windows/arm64", "built": true, "state": "build_only"},
		{"target": "freebsd/amd64", "built": false, "state": "unsupported"},
	}
	for _, platform := range extra {
		rows = append(rows, map[string]any{"target": strings.Replace(platform, "-", "/", 1), "built": true, "state": "build_only"})
		w.released = append(w.released, platform)
	}
	platforms := append(standInPlatforms(), extra...)

	// The Companion's archives and the converters and notices build/release.sh leaves beside them.
	for _, platform := range platforms {
		packCompanion(t, w, platform)
		built, err := realConverter(platform)
		if err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(built)
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(w.dist, "converter", platform, converterName(platform))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, body, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(w.dist, "converter", "NOTICES-auto-pigeon-mapio.txt"),
		"auto-pigeon-mapio\ngo/mapio "+release.ConverterLibraryVersion+"\nMIT License\nApache License\n")
	writeJSON(t, filepath.Join(w.dir, "support.json"), map[string]any{"platforms": rows})

	mustPlan(t, "matrix", "--aucom-support", filepath.Join(w.dir, "support.json"), "--out", w.matrix)
	mustPlan(t, "check-converter", "--matrix", w.matrix, "--aucom-dist", w.dist, "--version", testVersion,
		"--commit", testCommit, "--go", "go", "--out", w.inputs)
	mustPlan(t, "bundle", "--matrix", w.matrix, "--converter-inputs", w.inputs, "--aucom-dist", w.dist,
		"--version", testVersion, "--out", w.bundles)

	return w
}

func (w world) releaseIt(t *testing.T) string {
	t.Helper()
	return mustPlan(t, "release", "--matrix", w.matrix, "--converter-inputs", w.inputs, "--bundles", w.bundles,
		"--version", testVersion, "--commit", strings.Repeat("cd", 20), "--out", w.release,
		"--notes", filepath.Join(w.dir, "NOTES.md"))
}

func writeTarGz(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := archive.WriteHeader(&tar.Header{Name: "./" + name, Mode: 0o755, Size: int64(len(files[name]))}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, buffer.String())
}

// zipDir zips a tree with build/releaselib.write_zip, the one archive writer.
func zipDir(t *testing.T, dir, archive, prefix string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "import sys; sys.path.insert(0, sys.argv[1]); import releaselib; releaselib.write_zip(sys.argv[2], sys.argv[3], sys.argv[4])"
	if output, err := exec.Command(python(t), "-B", "-c", script, filepath.Join(repoRoot(t), "build"), dir, archive, prefix).CombinedOutput(); err != nil {
		t.Fatalf("write_zip: %v\n%s", err, output)
	}
}

func unzipDir(t *testing.T, archive, dir string) {
	t.Helper()
	script := "import sys; sys.path.insert(0, sys.argv[1]); import releaselib; releaselib.extract_zip(sys.argv[2], sys.argv[3])"
	if output, err := exec.Command(python(t), "-B", "-c", script, filepath.Join(repoRoot(t), "build"), archive, dir).CombinedOutput(); err != nil {
		t.Fatalf("extract_zip: %v\n%s", err, output)
	}
}

func verifyArgs(w world, archive, platform string) []string {
	return []string{"verify-archive", archive, "--platform", platform, "--version", testVersion,
		"--converter-inputs", w.inputs}
}

func TestAReleaseIsTheBundledArchivesAndNothingElse(t *testing.T) {
	w := newWorld(t)
	list := strings.Fields(w.releaseIt(t))
	want := []string{"SHA256SUMS", "auto-pigeon-companion-" + testVersion + "-all.zip", "release-manifest.json"}
	for _, platform := range w.released {
		want = append(want, "auto-pigeon-companion-"+testVersion+"-"+platform+".zip")
	}
	sort.Strings(want)
	if strings.Join(list, ",") != strings.Join(want, ",") {
		t.Fatalf("the upload list is %v, want %v", list, want)
	}

	var document struct {
		Version   string `json:"version"`
		Tag       string `json:"tag"`
		Signed    bool   `json:"signed"`
		Schema    string `json:"schema"`
		Converter struct {
			Program string `json:"program"`
			Commit  string `json:"commit"`
			Version string `json:"version"`
			License string `json:"license"`
			Library struct {
				Version string `json:"version"`
				Commit  string `json:"commit"`
			} `json:"library"`
		} `json:"converter"`
		Targets []map[string]any `json:"targets"`
	}
	if err := json.Unmarshal([]byte(readText(t, filepath.Join(w.release, "release-manifest.json"))), &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != testVersion || document.Tag != "v"+testVersion || document.Signed ||
		document.Schema != "aucom.release-manifest/2.0" ||
		document.Converter.Program != "auto-pigeon-mapio" || document.Converter.Commit != testCommit ||
		document.Converter.Version != testVersion || document.Converter.License != release.ConverterSPDX ||
		document.Converter.Library.Version != release.ConverterLibraryVersion ||
		!strings.HasSuffix(release.ConverterLibraryVersion, document.Converter.Library.Commit[:12]) {
		t.Errorf("release manifest = %+v", document)
	}
	if strings.Contains(readText(t, filepath.Join(w.release, "release-manifest.json")), `"aue"`) {
		t.Error("the release manifest still has an aue member")
	}
	verdicts := map[string]string{}
	for _, row := range document.Targets {
		verdicts[row["target"].(string)] = row["verdict"].(string)
	}
	if verdicts["windows/arm64"] != "build_only" || verdicts["freebsd/amd64"] != "refused" ||
		verdicts["linux/amd64"] != "bundled_release" || verdicts["darwin/arm64"] != "bundled_release" {
		t.Errorf("verdicts = %v", verdicts)
	}

	// The build-only candidate is in the aggregate, under candidates/, and
	// nowhere a user would take it for a download.
	aggregate := filepath.Join(t.TempDir(), "all")
	unzipDir(t, filepath.Join(w.release, "auto-pigeon-companion-"+testVersion+"-all.zip"), aggregate)
	if _, err := os.Stat(filepath.Join(aggregate, "auto-pigeon-companion-"+testVersion+"-all", "candidates",
		"auto-pigeon-companion-"+testVersion+"-windows-arm64-build-only.zip")); err != nil {
		t.Errorf("the aggregate does not carry the candidate under candidates/: %v", err)
	}

	notes := readText(t, filepath.Join(w.dir, "NOTES.md"))
	mustContain(t, "the release notes", notes, release.ConverterLibraryVersion, "auto-pigeon-mapio "+testVersion,
		"windows/arm64 | build_only", "Nothing here is signed", "it is not a signature", "not supported downloads",
		"`LICENSE` in the Companion's repository", "MIT", "not under one licence", "`Apache-2.0 AND MIT`",
		"NOTICES-auto-pigeon-mapio.txt", "no extractor is in these archives", "Download this archive once by hand")
	if strings.Contains(notes, "Auto-Pigeon Companion is MIT (") || strings.Contains(notes, "MIT-licensed archive") {
		t.Errorf("the release notes describe the archive as MIT:\n%s", notes)
	}

	// The macOS archive puts the converter where a Companion in a .app looks.
	for _, platform := range w.released {
		archive := filepath.Join(w.release, "auto-pigeon-companion-"+testVersion+"-"+platform+".zip")
		mustPlan(t, verifyArgs(w, archive, platform)...)
	}
	mac := filepath.Join(t.TempDir(), "mac")
	unzipDir(t, filepath.Join(w.release, "auto-pigeon-companion-"+testVersion+"-darwin-arm64.zip"), mac)
	app := filepath.Join(mac, "auto-pigeon-companion-"+testVersion+"-darwin-arm64", "Auto-Pigeon Companion.app", "Contents")
	for _, path := range []string{"MacOS/auto-pigeon-mapio", "Resources/bundle-manifest.json", "Resources/NOTICES-auto-pigeon-mapio.txt"} {
		if _, err := os.Stat(filepath.Join(app, path)); err != nil {
			t.Errorf("the macOS bundle has no Contents/%s: %v", path, err)
		}
	}

	// An archive is what runs the Companion and nothing else (operator decision, 2026-09-25): no
	// licence or notice file but the converter's own (NEW_371), no acceptance kit, and no extractor.
	for _, platform := range []string{"linux-amd64", "darwin-arm64"} {
		unpacked := filepath.Join(t.TempDir(), platform)
		unzipDir(t, filepath.Join(w.release, "auto-pigeon-companion-"+testVersion+"-"+platform+".zip"), unpacked)
		_ = filepath.WalkDir(unpacked, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			name := entry.Name()
			if strings.HasPrefix(name, "LICENSE") || name == "THIRD_PARTY_NOTICES.md" ||
				strings.HasPrefix(name, "run-acceptance") || name == "kit-options.json" ||
				strings.Contains(name, "auto-pigeon-extractor") {
				t.Errorf("the %s archive carries %s", platform, strings.TrimPrefix(path, unpacked))
			}
			return nil
		})
	}

	// The root of a Linux or Windows archive is the program a person starts;
	// the converter, its notices and the manifest are in dependencies/.
	linux := filepath.Join(t.TempDir(), "linux-root")
	unzipDir(t, filepath.Join(w.release, "auto-pigeon-companion-"+testVersion+"-linux-amd64.zip"), linux)
	root := filepath.Join(linux, "auto-pigeon-companion-"+testVersion+"-linux-amd64")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "companion", "SHA256SUMS", "dependencies":
		default:
			t.Errorf("the linux-amd64 archive root carries %s", entry.Name())
		}
	}
	for _, name := range []string{"auto-pigeon-mapio", "NOTICES-auto-pigeon-mapio.txt", "bundle-manifest.json"} {
		if _, err := os.Stat(filepath.Join(root, "dependencies", name)); err != nil {
			t.Errorf("the linux-amd64 archive has no dependencies/%s: %v", name, err)
		}
	}
}

// What the release says about the converter is read from the built program and its build file, and
// the checks fail on the ways that could be wrong: no notices, a notice about another revision, and a
// converter that links anything but the one pinned module.
// A target joins a stand-in release in its real layout whichever machine runs the test: Windows amd64
// CI once found `companion` where the bundle wants `companion.exe`, in a branch no other machine took.
func TestEveryExtraTargetIsAFixtureInItsOwnPlatformsLayout(t *testing.T) {
	w := worldHolding(t, "windows-amd64", "darwin-amd64", "linux-arm64")
	w.releaseIt(t)
	for _, platform := range w.released {
		archive := filepath.Join(w.release, "auto-pigeon-companion-"+testVersion+"-"+platform+".zip")
		mustPlan(t, verifyArgs(w, archive, platform)...)
	}
	unpacked := filepath.Join(t.TempDir(), "windows")
	unzipDir(t, filepath.Join(w.release, "auto-pigeon-companion-"+testVersion+"-windows-amd64.zip"), unpacked)
	root := filepath.Join(unpacked, "auto-pigeon-companion-"+testVersion+"-windows-amd64")
	if _, err := os.Stat(filepath.Join(root, "companion.exe")); err != nil {
		t.Errorf("the windows-amd64 archive has no companion.exe: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "companion")); err == nil {
		t.Error("the windows-amd64 archive holds an extensionless companion")
	}
	stage := filepath.Join(t.TempDir(), "mac")
	stageCompanion(t, stage, "darwin-amd64")
	if _, err := os.Stat(filepath.Join(stage, "Auto-Pigeon Companion.app", "Contents", "MacOS", "companion")); err != nil {
		t.Errorf("a stand-in macOS Companion is not inside the app: %v", err)
	}
}

func TestTheConverterChecksRefuseWhatTheReleaseCouldNotStandBehind(t *testing.T) {
	w := newWorld(t)
	check := func() (string, error) {
		return runPlan(t, "check-converter", "--matrix", w.matrix, "--aucom-dist", w.dist, "--version", testVersion,
			"--commit", testCommit, "--go", "go", "--out", w.inputs)
	}
	notices := filepath.Join(w.dist, "converter", "NOTICES-auto-pigeon-mapio.txt")
	body := readText(t, notices)

	writeFile(t, notices, "MIT License\nApache License\n")
	if output, err := check(); err == nil || !strings.Contains(output, release.ConverterLibraryVersion) {
		t.Errorf("notices about no revision were accepted: %v\n%s", err, output)
	}
	if err := os.Remove(notices); err != nil {
		t.Fatal(err)
	}
	if output, err := check(); err == nil || !strings.Contains(output, "is not there") {
		t.Errorf("a release with no notices was accepted: %v\n%s", err, output)
	}
	writeFile(t, notices, body)

	// A file that is not what the toolchain built: another program for the same machine.
	target := filepath.Join(w.dist, "converter", "linux-amd64", "auto-pigeon-mapio")
	good, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "build", "-trimpath", "-o", target, "./cmd/companion")
	command.Dir = repoRoot(t)
	command.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	if output, err := check(); err == nil || !strings.Contains(output, "links") {
		t.Errorf("a program that does not link the library was accepted as the converter: %v\n%s", err, output)
	}
	if err := os.WriteFile(target, good, 0o755); err != nil {
		t.Fatal(err)
	}

	if output, err := runPlan(t, "check-converter", "--matrix", w.matrix, "--aucom-dist", w.dist, "--version", testVersion,
		"--commit", "abc123", "--go", "go", "--out", w.inputs); err == nil {
		t.Errorf("a short commit was accepted:\n%s", output)
	}
	if output, err := check(); err != nil {
		t.Fatalf("the intact release was refused: %v\n%s", err, output)
	}
}

// Two builds of one commit are one set of bytes, or a rerun could never show
// it rebuilt what it published.
func TestTheReleaseIsTheSameBytesTwice(t *testing.T) {
	w := newWorld(t)
	w.releaseIt(t)
	first := readText(t, filepath.Join(w.release, "SHA256SUMS"))
	if err := os.RemoveAll(w.release); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(w.bundles); err != nil {
		t.Fatal(err)
	}
	mustPlan(t, "bundle", "--matrix", w.matrix, "--converter-inputs", w.inputs, "--aucom-dist", w.dist,
		"--version", testVersion, "--out", w.bundles)
	w.releaseIt(t)
	if second := readText(t, filepath.Join(w.release, "SHA256SUMS")); second != first {
		t.Errorf("two builds of one release differ:\n%s\n---\n%s", first, second)
	}
}

// An eligible archive that lacks the converter or its notices, carries an extractor, or whose versions
// disagree, is refused by the check the release runs on it.
func TestAnIncompleteOrInconsistentArchiveIsRefused(t *testing.T) {
	w := newWorld(t)
	w.releaseIt(t)
	archive := filepath.Join(w.release, "auto-pigeon-companion-"+testVersion+"-linux-amd64.zip")
	top := "auto-pigeon-companion-" + testVersion + "-linux-amd64"

	// mutate unpacks the good archive, changes it, rewrites the manifest when
	// asked (so the digests still agree and the check has to find the real
	// fault), and packs it under the same name.
	mutate := func(name string, change func(bundle string), relist bool) string {
		dir := filepath.Join(t.TempDir(), name)
		unzipDir(t, archive, dir)
		bundle := filepath.Join(dir, top)
		change(bundle)
		if relist {
			args := []string{filepath.Join(repoRoot(t), "build", "bundle-manifest.py"), "--platform", "linux-amd64",
				"--version", testVersion, "--bundle", bundle}
			if output, err := exec.Command(python(t), args...).CombinedOutput(); err != nil {
				t.Fatalf("%s: relisting: %v\n%s", name, err, output)
			}
		}
		out := filepath.Join(t.TempDir(), name, filepath.Base(archive))
		zipDir(t, bundle, out, top)
		return out
	}
	remove := func(member string) func(string) {
		return func(bundle string) {
			if err := os.Remove(filepath.Join(bundle, member)); err != nil {
				t.Fatal(err)
			}
		}
	}

	cases := map[string]struct {
		archive string
		want    string
	}{
		"no converter": {mutate("no-converter", remove("dependencies/auto-pigeon-mapio"), true),
			"carries no auto-pigeon-mapio"},
		"no notices": {mutate("no-notices", remove("dependencies/NOTICES-auto-pigeon-mapio.txt"), false),
			"the bundle manifest lists"},
		"an extractor beside it": {mutate("extractor", func(bundle string) {
			writeFakeExecutable(t, filepath.Join(bundle, "dependencies", "auto-pigeon-extractor"), "linux-amd64", "aue")
		}, false), "not in its bundle manifest"},
		"a replaced converter": {mutate("replaced", func(bundle string) {
			writeFakeExecutable(t, filepath.Join(bundle, "dependencies", "auto-pigeon-mapio"), "linux-amd64", "something else")
		}, false), "does not match its bundle manifest"},
		"an unlisted file": {mutate("unlisted", func(bundle string) {
			writeFile(t, filepath.Join(bundle, "extra.txt"), "surprise\n")
		}, false), "not in its bundle manifest"},
	}
	for name, c := range cases {
		output, err := runPlan(t, verifyArgs(w, c.archive, "linux-amd64")...)
		if err == nil || !strings.Contains(output, c.want) {
			t.Errorf("%s: err = %v, want a refusal naming %q:\n%s", name, err, c.want, output)
		}
	}

	// The same bytes, checked against another release's facts.
	inputs := func(change func(map[string]any)) string {
		document := map[string]any{}
		if err := json.Unmarshal([]byte(readText(t, w.inputs)), &document); err != nil {
			t.Fatal(err)
		}
		change(document)
		path := filepath.Join(t.TempDir(), "inputs.json")
		writeJSON(t, path, document)
		return path
	}
	verify := func(version, platform, inputsPath string) (string, error) {
		return runPlan(t, "verify-archive", archive, "--platform", platform, "--version", version, "--converter-inputs", inputsPath)
	}
	if output, err := verify("1.501", "linux-amd64", w.inputs); err == nil {
		t.Errorf("another Companion version: accepted:\n%s", output)
	}
	if output, err := verify(testVersion, "linux-arm64", w.inputs); err == nil {
		t.Errorf("another platform: accepted:\n%s", output)
	}
	for name, change := range map[string]func(map[string]any){
		"another converter version": func(d map[string]any) { d["version"] = "1.208" },
		"another converter commit":  func(d map[string]any) { d["commit"] = strings.Repeat("ef", 20) },
		"another library revision": func(d map[string]any) {
			d["library"].(map[string]any)["version"] = "v0.0.0-20200101000000-000000000000"
		},
		"another licence": func(d map[string]any) { d["license"] = "MIT" },
	} {
		if output, err := verify(testVersion, "linux-amd64", inputs(change)); err == nil {
			t.Errorf("%s: accepted:\n%s", name, output)
		}
	}
	renamed := filepath.Join(t.TempDir(), "auto-pigeon-companion-1.501-linux-amd64.zip")
	writeFile(t, renamed, readText(t, archive))
	if output, err := runPlan(t, "verify-archive", renamed, "--platform", "linux-amd64", "--version", "1.501",
		"--converter-inputs", w.inputs); err == nil {
		t.Errorf("an archive renamed to another version was accepted:\n%s", output)
	}
}

// A candidate at the top of a release directory is refused, and so is a
// manifest that files a build-only target as a download.
func TestABuildOnlyCandidateIsNeverAReleaseDownload(t *testing.T) {
	w := newWorld(t)
	w.releaseIt(t)
	candidate := filepath.Join(w.bundles, "candidates", "auto-pigeon-companion-"+testVersion+"-windows-arm64-build-only.zip")
	promoted := filepath.Join(w.release, filepath.Base(candidate))
	writeFile(t, promoted, readText(t, candidate))
	if output, err := runPlan(t, "upload-list", "--release-dir", w.release); err == nil || !strings.Contains(output, "build-only") {
		t.Errorf("a build-only candidate at the top level was listed for upload: %v\n%s", err, output)
	}
	if err := os.Remove(promoted); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(w.release, "release-manifest.json")
	document := map[string]any{}
	if err := json.Unmarshal([]byte(readText(t, manifestPath)), &document); err != nil {
		t.Fatal(err)
	}
	for _, row := range document["targets"].([]any) {
		entry := row.(map[string]any)
		if entry["target"] == "linux/amd64" {
			entry["verdict"] = "build_only"
		}
	}
	writeJSON(t, manifestPath, document)
	if output, err := runPlan(t, "upload-list", "--release-dir", w.release); err == nil {
		t.Errorf("a build_only target's archive was listed as a download:\n%s", output)
	}
}

// A rerun uploads only what is missing, leaves identical assets alone, and
// fails — uploading nothing — on any asset that differs, cannot be compared,
// or was not produced by this build.
func TestARerunCannotReplaceAssetsWithDifferentBytes(t *testing.T) {
	w := newWorld(t)
	names := strings.Fields(w.releaseIt(t))
	digests := map[string]string{}
	for _, name := range names {
		digests[name] = sha([]byte(readText(t, filepath.Join(w.release, name))))
	}
	existing := func(mutate func(map[string]string)) string {
		rows := map[string]string{}
		for name, digest := range digests {
			rows[name] = digest
		}
		mutate(rows)
		var out strings.Builder
		for name, digest := range rows {
			out.WriteString(name + "\t" + digest + "\n")
		}
		path := filepath.Join(t.TempDir(), "existing.tsv")
		writeFile(t, path, out.String())
		return path
	}

	if output := mustPlan(t, "reconcile", "--release-dir", w.release, "--existing", existing(func(map[string]string) {})); strings.TrimSpace(output) != "" {
		t.Errorf("an identical release would upload %q", output)
	}
	partial := existing(func(rows map[string]string) { delete(rows, "SHA256SUMS") })
	if output := mustPlan(t, "reconcile", "--release-dir", w.release, "--existing", partial); strings.TrimSpace(output) != "SHA256SUMS" {
		t.Errorf("a partial release would upload %q, want only SHA256SUMS", output)
	}
	for name, mutate := range map[string]func(map[string]string){
		"different bytes": func(rows map[string]string) {
			rows["auto-pigeon-companion-"+testVersion+"-linux-amd64.zip"] = "sha256:" + strings.Repeat("0", 64)
		},
		"no digest":     func(rows map[string]string) { rows["release-manifest.json"] = "" },
		"foreign asset": func(rows map[string]string) { rows["auto-pigeon-companion-linux-amd64.zip"] = "sha256:00" },
	} {
		if output, err := runPlan(t, "reconcile", "--release-dir", w.release, "--existing", existing(mutate)); err == nil ||
			!strings.Contains(output, "nothing was uploaded") {
			t.Errorf("%s: err = %v\n%s", name, err, output)
		}
	}

	// Promotion checks the bytes on the release against its own manifest.
	if output, err := runPlan(t, "promote-check", "--release-dir", w.release, "--existing",
		existing(func(map[string]string) {}), "--commit", strings.Repeat("cd", 20)); err != nil {
		t.Errorf("promote-check refused an intact release: %v\n%s", err, output)
	}
	if output, err := runPlan(t, "promote-check", "--release-dir", w.release, "--existing",
		existing(func(map[string]string) {}), "--commit", strings.Repeat("ef", 20)); err == nil {
		t.Errorf("promote-check accepted another commit:\n%s", output)
	}

	if strings.Contains(repoFile(t, ".github", "workflows", "release.yml"), "--clobber") {
		t.Error("release.yml overwrites release assets with --clobber")
	}
}

// ---------------------------------------------------------------------------
// The workflows

// jobBlocks splits a workflow's `jobs:` into job name -> its text, by
// indentation. Deliberately small: these files are ours, and a YAML library
// would be the first external module this program links.
func jobBlocks(t *testing.T, workflow string) (header string, jobs map[string]string) {
	t.Helper()
	index := strings.Index(workflow, "\njobs:\n")
	if index < 0 {
		t.Fatal("no jobs: block")
	}
	header, jobs = workflow[:index], map[string]string{}
	name := ""
	for _, line := range strings.Split(workflow[index+len("\njobs:\n"):], "\n") {
		if match := regexp.MustCompile(`^  ([a-z][a-z0-9_-]*):\s*$`).FindStringSubmatch(line); match != nil {
			name = match[1]
		}
		if name != "" {
			jobs[name] += line + "\n"
		}
	}

	return header, jobs
}

func TestOnlyThePublishJobCanWrite(t *testing.T) {
	header, jobs := jobBlocks(t, repoFile(t, ".github", "workflows", "release.yml"))
	if !strings.Contains(header, "\npermissions:\n  contents: read\n") {
		t.Error("release.yml's default permissions are not `contents: read`")
	}
	if strings.Contains(header, "pull_request") {
		t.Error("release.yml runs on pull requests; build.yml is the pull-request workflow and holds no credential")
	}
	for name, body := range jobs {
		if !strings.Contains(body, "    permissions:\n") {
			t.Errorf("job %s does not declare its permissions", name)
		}
		writes := regexp.MustCompile(`:\s*write`).MatchString(body)
		if writes != (name == "publish") {
			t.Errorf("job %s: write permission = %v; only `publish` may write", name, writes)
		}
		if strings.Contains(body, "secrets.") {
			t.Errorf("job %s reads a secret; a release builds from public source and needs none", name)
		}
	}
	if _, ok := jobs["publish"]; !ok {
		t.Fatal("release.yml has no publish job")
	}
	mustContain(t, "the publish job", jobs["publish"], "needs: [plan, bundle, acceptance]",
		"needs.acceptance.result == 'success'", "--prerelease", "release-plan.py reconcile",
		"release-plan.py upload-list")
	// `gh release create/upload` runs inside the assets directory, outside the
	// checkout; without GH_REPO it cannot name the repository and the publish
	// step fails after everything else passed (runs 36062935436, 36111051517).
	mustContain(t, "the publish job", jobs["publish"], "GH_REPO: ${{ github.repository }}")
	if regexp.MustCompile(`gh release (create|upload)[^\n]*\./\*`).MatchString(jobs["publish"]) {
		t.Error("the publish job attaches a glob; it attaches the upload list")
	}

	if regexp.MustCompile(`:\s*write`).MatchString(repoFile(t, ".github", "workflows", "build.yml")) {
		t.Error("build.yml, which runs on every pull request, asks for write permission")
	}
}

// A release builds from this repository and a public module, so a fork or a clean machine can build all
// of it: no other repository's checkout, no token, and nothing naming the proprietary extractor as an
// input (NEW_371).
func TestTheReleaseNeedsNoOtherRepositoryAndNoCredential(t *testing.T) {
	workflow := repoFile(t, ".github", "workflows", "release.yml")
	_, jobs := jobBlocks(t, workflow)
	if _, ok := jobs["aue"]; ok {
		t.Error("release.yml still has an aue job")
	}
	for _, forbidden := range []string{"AUE_CHECKOUT_TOKEN", "repository: ${{", "auto-pigeon-extractor", "aue-release", "aue-pin", "token:"} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("release.yml names %q", forbidden)
		}
	}
	for _, file := range [][]string{{"build", "release.sh"}, {"build", "bundle-sidecar.sh"}, {"build", "converter.json"}} {
		if body := repoFile(t, file...); strings.Contains(body, "../auto-pigeon-extractor") || strings.Contains(body, "AUE_CHECKOUT_TOKEN") {
			t.Errorf("%s depends on the extractor", filepath.Join(file...))
		}
	}
	if _, err := os.Stat(filepath.Join(repoRoot(t), "build", "aue-pin.json")); err == nil {
		t.Error("build/aue-pin.json came back")
	}
	if strings.Contains(repoFile(t, "go.mod"), "auto-pigeon-extractor") {
		t.Error("go.mod names the extractor")
	}
	mustContain(t, "the bundle job", jobs["bundle"], "needs: [plan, aucom]", "release-plan.py check-converter")
}

// Nothing in a workflow or in the release tools fetches a program. The
// Companion downloads no executable (operator, 2026-09-23), and neither does
// the release that builds it: the converter is BUILT from this commit.
func TestNoWorkflowOrReleaseToolRegainsADownloadPath(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)\bcurl\b|\bwget\b|invoke-webrequest|\biwr\b|start-bitstransfer|` +
		`/releases/download/|/releases/latest|@latest\b|urllib\.request|\brequests\.get|http\.client`)
	var files []string
	for _, pattern := range []string{".github/workflows/*.yml", "build/*.sh", "build/*.py"} {
		matches, err := filepath.Glob(filepath.Join(repoRoot(t), pattern))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	if len(files) < 6 {
		t.Fatalf("found only %v", files)
	}
	for _, path := range files {
		body := readText(t, path)
		for number, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if match := forbidden.FindString(line); match != "" {
				t.Errorf("%s:%d fetches something (%q): %s", filepath.Base(path), number+1, match, strings.TrimSpace(line))
			}
			if strings.Contains(line, "gh release download") && !strings.Contains(line, "--pattern release-manifest.json") {
				t.Errorf("%s:%d downloads release assets other than the manifest: %s", filepath.Base(path), number+1, line)
			}
		}
	}
	// The converter is built, never downloaded, and go.mod's checksum is what vouches for its library.
	mustContain(t, "release.yml", repoFile(t, ".github", "workflows", "release.yml"), "./build/release.sh", "go mod verify")
}

// A native acceptance run refuses a platform the machine is not.
func TestAcceptanceRefusesAnotherMachinesPlatform(t *testing.T) {
	other := "windows-arm64"
	if strings.HasPrefix(runtimeTarget(), "windows") {
		other = "linux-arm64"
	}
	command := exec.Command(python(t), filepath.Join(repoRoot(t), "build", "release-acceptance.py"),
		"--archive", "none.zip", "--platform", other, "--version", testVersion,
		"--fixture", filepath.Join(repoRoot(t), "build", "release-fixtures", "release-acceptance.apmap"))
	output, err := command.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 || !strings.Contains(string(output), "never on another") {
		t.Errorf("err = %v, output = %s", err, output)
	}
}

func runtimeTarget() string {
	return release.ReadBuild().Target
}

// A release cannot diagnose browser startup with the signal its own cleanup
// sent. Drive the harness's delayed-start, transient-read and process tests on
// every native OS running go test, without requiring an installed browser here.
func TestReleaseBrowserStartupHarness(t *testing.T) {
	command := exec.Command(python(t), "-B", filepath.Join(repoRoot(t), "build", "test_release_acceptance.py"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("release browser startup harness: %v\n%s", err, output)
	}
}
