package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aue"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/release"
)

// The per-push bundled release (NEW_247A), held to the failures the prompt
// named: a movable pin, drifting target lists, an archive missing a program or
// a licence, a wrong-platform extractor, disagreeing versions, a download path,
// write permission on the wrong job, a build-only candidate published as a
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
// The pin

// The extractor a release bundles is the head of AUE's main branch when the
// release runs (operator, 2026-10-02): a commit written here is how v1.192
// shipped an extractor that could not read the maps the editor was saving. The
// branch is resolved to one commit by `aue-checkout`, so what is refused here
// is a commit, a tag or a ref path standing where the branch name goes.
func TestTheExtractorIsTheHeadOfItsBranchAndThisBuildsProtocol(t *testing.T) {
	var pin struct {
		Ref              string `json:"ref"`
		Repository       string `json:"repository"`
		RequiredProtocol string `json:"required_protocol"`
	}
	if err := json.Unmarshal([]byte(repoFile(t, "build", "aue-pin.json")), &pin); err != nil {
		t.Fatal(err)
	}
	if pin.Ref != "main" {
		t.Errorf("build/aue-pin.json follows %q; a release bundles the head of main", pin.Ref)
	}
	if pin.RequiredProtocol != aue.RequiredProtocol {
		t.Errorf("the pin requires protocol %s and this Companion drives %s", pin.RequiredProtocol, aue.RequiredProtocol)
	}
	mustPlan(t, "pin")

	for name, change := range map[string]func(map[string]any){
		"no branch":            func(d map[string]any) { delete(d, "ref") },
		"a commit as the ref":  func(d map[string]any) { d["ref"] = strings.Repeat("ab", 20) },
		"a ref path":           func(d map[string]any) { d["ref"] = "refs/heads/main" },
		"a ref with a space":   func(d map[string]any) { d["ref"] = "main; rm" },
		"a parent directory":   func(d map[string]any) { d["ref"] = "../main" },
		"a commit kept beside": func(d map[string]any) { d["commit"] = strings.Repeat("ab", 20) },
		"a version kept":       func(d map[string]any) { d["version"] = "1.214" },
	} {
		document := map[string]any{}
		if err := json.Unmarshal([]byte(repoFile(t, "build", "aue-pin.json")), &document); err != nil {
			t.Fatal(err)
		}
		change(document)
		path := filepath.Join(t.TempDir(), "pin.json")
		writeJSON(t, path, document)
		if output, err := runPlan(t, "pin", "--pin", path); err == nil {
			t.Errorf("%s was accepted:\n%s", name, output)
		}
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
					"`aue-release targets`, through build/release-plan.py", spelling)
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

// aueTargets writes what `aue-release targets` prints, today's shape unless
// told otherwise.
func aueTargets(t *testing.T, published, candidates, unsupported []string) string {
	t.Helper()
	dir := t.TempDir()
	lines := func(entries []string, suffix string) string {
		var out strings.Builder
		for _, entry := range entries {
			out.WriteString(strings.Replace(entry, "/", " ", 1) + suffix + "\n")
		}
		return out.String()
	}
	writeFile(t, filepath.Join(dir, "published.txt"), lines(published, ""))
	writeFile(t, filepath.Join(dir, "candidates.txt"), lines(candidates, " build_only No native machine has run it."))
	writeFile(t, filepath.Join(dir, "unsupported.txt"), lines(unsupported, " It is refused, for a reason."))

	return dir
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
	today := aueTargets(t,
		[]string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64"},
		[]string{"windows/arm64"},
		[]string{"windows/arm64", "linux/386", "linux/arm"})
	out := filepath.Join(t.TempDir(), "matrix.json")
	mustPlan(t, "matrix", "--aucom-support", support, "--aue-targets", today, "--out", out)
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
			// Until a native Windows-arm64 job runs the extractor and AUE's own
			// release authority promotes it (AUCOM/AUE/AUT 246I1).
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

	// AUE refusing a target refuses it here; AUE saying NOTHING about one is a
	// failure, never a silent drop.
	refused := aueTargets(t, []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"},
		nil, []string{"windows/amd64", "windows/arm64"})
	mustPlan(t, "matrix", "--aucom-support", support, "--aue-targets", refused, "--out", out)
	if err := json.Unmarshal([]byte(readText(t, out)), &matrix); err != nil {
		t.Fatal(err)
	}
	for _, entry := range matrix.Targets {
		if strings.HasPrefix(entry.Target, "windows/") && (entry.Verdict != "refused" || !strings.Contains(entry.Reason, "refused, for a reason")) {
			t.Errorf("%s: %s (%s), want refused with AUE's reason", entry.Target, entry.Verdict, entry.Reason)
		}
	}
	silent := aueTargets(t, []string{"linux/amd64"}, nil, nil)
	if output, err := runPlan(t, "matrix", "--aucom-support", support, "--aue-targets", silent, "--out", out); err == nil ||
		!strings.Contains(output, "never dropped silently") {
		t.Errorf("a target AUE says nothing about was accepted: %v\n%s", err, output)
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

const (
	testVersion    = "1.500"
	testAUEVersion = "1.207"
)

var testAUECommit = strings.Repeat("ab", 20)

// world is a fake AUCOM dist and a fake staged AUE release for three
// targets: linux-amd64 and darwin-arm64 released, windows-arm64 build-only.
type world struct {
	dir, dist, aue, matrix, inputs, bundles, release string
}

func newWorld(t *testing.T) world {
	t.Helper()
	w := world{dir: t.TempDir()}
	w.dist, w.aue = filepath.Join(w.dir, "dist"), filepath.Join(w.dir, "aue-release")
	w.matrix, w.inputs = filepath.Join(w.dir, "matrix.json"), filepath.Join(w.dir, "aue-inputs.json")
	w.bundles, w.release = filepath.Join(w.dir, "bundles"), filepath.Join(w.dir, "release")

	// The Companion's archives, the shapes build/release.sh makes.
	writeTarGz(t, filepath.Join(w.dist, "auto-pigeon-companion-"+testVersion+"-linux-amd64.tar.gz"), map[string][]byte{
		"companion": fakeExecutable("linux-amd64", "aucom"),
	})
	for _, platform := range []string{"darwin-arm64", "windows-arm64"} {
		stage := filepath.Join(w.dir, "stage", platform)
		if platform == "darwin-arm64" {
			writeFakeExecutable(t, filepath.Join(stage, "Auto-Pigeon Companion.app", "Contents", "MacOS", "companion"), platform, "aucom")
		} else {
			writeFakeExecutable(t, filepath.Join(stage, "companion.exe"), platform, "aucom")
		}
		zipDir(t, stage, filepath.Join(w.dist, "auto-pigeon-companion-"+testVersion+"-"+platform+".zip"), "")
	}
	writeJSON(t, filepath.Join(w.dir, "support.json"), map[string]any{"platforms": []map[string]any{
		{"target": "linux/amd64", "built": true, "state": "native_pass"},
		{"target": "darwin/arm64", "built": true, "state": "build_only"},
		{"target": "windows/arm64", "built": true, "state": "build_only"},
		{"target": "freebsd/amd64", "built": false, "state": "unsupported"},
	}})

	// The extractor's staged release, as its build-release.sh + stage leave it.
	artifact := func(platform, dir string, status string) map[string]any {
		goos, goarch, _ := strings.Cut(platform, "-")
		name := "auto-pigeon-extractor-" + testAUEVersion + "-" + platform
		if goos == "windows" {
			name += ".exe"
		}
		file := name
		if dir != "" {
			file = dir + "/" + name
		}
		body := fakeExecutable(platform, "aue")
		writeFile(t, filepath.Join(w.aue, file), string(body))
		return map[string]any{"platform": map[string]string{"os": goos, "arch": goarch}, "file": file,
			"kind": "file", "size": len(body), "sha256": sha(body), "status": status}
	}
	writeJSON(t, filepath.Join(w.aue, "release-manifest.json"), map[string]any{
		"schema_version": "aue-release-manifest/1.0", "version": testAUEVersion, "protocol": "1.0",
		"license":    map[string]string{"spdx": "LicenseRef-test"},
		"source":     map[string]string{"repository": "https://example.test/aue", "commit": testAUECommit},
		"toolchain":  map[string]any{"go": "go1.26.8", "cgo_enabled": false},
		"artifacts":  []any{artifact("linux-amd64", "", "published"), artifact("darwin-arm64", "", "published")},
		"candidates": []any{artifact("windows-arm64", "candidates", "build_only")},
	})
	writeFile(t, filepath.Join(w.aue, "LICENSE"), "the extractor's licence\n")
	targets := aueTargets(t, []string{"linux/amd64", "darwin/arm64"}, []string{"windows/arm64"},
		[]string{"windows/arm64", "freebsd/amd64"})

	pin := map[string]any{}
	if err := json.Unmarshal([]byte(repoFile(t, "build", "aue-pin.json")), &pin); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(w.dir, "pin.json"), pin)

	mustPlan(t, "matrix", "--aucom-support", filepath.Join(w.dir, "support.json"), "--aue-targets", targets, "--out", w.matrix)
	mustPlan(t, "check-aue", "--pin", filepath.Join(w.dir, "pin.json"), "--matrix", w.matrix,
		"--aue-release", w.aue, "--aue-version", testAUEVersion, "--aue-commit", testAUECommit, "--out", w.inputs)
	mustPlan(t, "bundle", "--matrix", w.matrix, "--aue-inputs", w.inputs, "--aucom-dist", w.dist,
		"--version", testVersion, "--out", w.bundles)

	return w
}

func (w world) releaseIt(t *testing.T) string {
	t.Helper()
	return mustPlan(t, "release", "--matrix", w.matrix, "--aue-inputs", w.inputs, "--bundles", w.bundles,
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

func verifyArgs(archive, platform string) []string {
	return []string{"verify-archive", archive, "--platform", platform, "--version", testVersion,
		"--aue-version", testAUEVersion, "--aue-commit", testAUECommit}
}

func TestAReleaseIsTheBundledArchivesAndNothingElse(t *testing.T) {
	w := newWorld(t)
	list := strings.Fields(w.releaseIt(t))
	want := []string{
		"SHA256SUMS",
		"auto-pigeon-companion-" + testVersion + "-all.zip",
		"auto-pigeon-companion-" + testVersion + "-darwin-arm64.zip",
		"auto-pigeon-companion-" + testVersion + "-linux-amd64.zip",
		"release-manifest.json",
	}
	if strings.Join(list, ",") != strings.Join(want, ",") {
		t.Fatalf("the upload list is %v, want %v", list, want)
	}

	var document struct {
		Version string `json:"version"`
		Tag     string `json:"tag"`
		Signed  bool   `json:"signed"`
		AUE     struct {
			Commit  string `json:"commit"`
			Version string `json:"version"`
		} `json:"aue"`
		Targets []map[string]any `json:"targets"`
	}
	if err := json.Unmarshal([]byte(readText(t, filepath.Join(w.release, "release-manifest.json"))), &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != testVersion || document.Tag != "v"+testVersion || document.Signed ||
		document.AUE.Commit != testAUECommit || document.AUE.Version != testAUEVersion {
		t.Errorf("release manifest = %+v", document)
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
	mustContain(t, "the release notes", notes, testAUECommit, "Auto-Pigeon Extractor "+testAUEVersion,
		"windows/arm64 | build_only", "Nothing here is signed", "it is not a signature", "not supported downloads",
		"`LICENSE` in the Companion's repository", "MIT", "not under one licence", "Apache-2.0", "declared `LicenseRef-test`")
	if strings.Contains(notes, "Auto-Pigeon Companion is MIT (") || strings.Contains(notes, "MIT-licensed archive") {
		t.Errorf("the release notes describe the archive as MIT:\n%s", notes)
	}

	// The macOS archive puts the extractor where a Companion in a .app looks.
	for _, platform := range []string{"linux-amd64", "darwin-arm64"} {
		archive := filepath.Join(w.release, "auto-pigeon-companion-"+testVersion+"-"+platform+".zip")
		mustPlan(t, verifyArgs(archive, platform)...)
	}
	mac := filepath.Join(t.TempDir(), "mac")
	unzipDir(t, filepath.Join(w.release, "auto-pigeon-companion-"+testVersion+"-darwin-arm64.zip"), mac)
	app := filepath.Join(mac, "auto-pigeon-companion-"+testVersion+"-darwin-arm64", "Auto-Pigeon Companion.app", "Contents")
	for _, path := range []string{"MacOS/auto-pigeon-extractor", "Resources/bundle-manifest.json"} {
		if _, err := os.Stat(filepath.Join(app, path)); err != nil {
			t.Errorf("the macOS bundle has no Contents/%s: %v", path, err)
		}
	}

	// An archive is what runs the Companion and nothing else (operator
	// decision, 2026-09-25): no licence or notice file, no acceptance kit.
	for _, platform := range []string{"linux-amd64", "darwin-arm64"} {
		unpacked := filepath.Join(t.TempDir(), platform)
		unzipDir(t, filepath.Join(w.release, "auto-pigeon-companion-"+testVersion+"-"+platform+".zip"), unpacked)
		_ = filepath.WalkDir(unpacked, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			name := entry.Name()
			if strings.HasPrefix(name, "LICENSE") || name == "THIRD_PARTY_NOTICES.md" ||
				strings.HasPrefix(name, "run-acceptance") || name == "kit-options.json" {
				t.Errorf("the %s archive carries %s", platform, strings.TrimPrefix(path, unpacked))
			}
			return nil
		})
	}

	// The root of a Linux or Windows archive is the program a person starts;
	// the extractor and the manifest are in dependencies/.
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
	for _, name := range []string{"auto-pigeon-extractor", "bundle-manifest.json"} {
		if _, err := os.Stat(filepath.Join(root, "dependencies", name)); err != nil {
			t.Errorf("the linux-amd64 archive has no dependencies/%s: %v", name, err)
		}
	}
}

// The extractor's licence is quoted from its own release manifest (NEW_247G):
// MIT is refused — an archive listing the extractor so would read as entirely
// MIT — and the proprietary identifier reaches the release manifest and the
// notes as declared, with the sentence that using it needs the owner's written
// authorization.
func TestTheExtractorsDeclaredLicenceIsQuotedAndNeverMIT(t *testing.T) {
	w := newWorld(t)
	manifestPath := filepath.Join(w.aue, "release-manifest.json")
	declare := func(spdx string) {
		document := map[string]any{}
		if err := json.Unmarshal([]byte(readText(t, manifestPath)), &document); err != nil {
			t.Fatal(err)
		}
		document["license"] = map[string]string{"spdx": spdx}
		writeJSON(t, manifestPath, document)
	}
	checkAUE := func() (string, error) {
		return runPlan(t, "check-aue", "--pin", filepath.Join(w.dir, "pin.json"), "--matrix", w.matrix,
			"--aue-release", w.aue, "--aue-version", testAUEVersion, "--aue-commit", testAUECommit, "--out", w.inputs)
	}

	declare("MIT")
	if output, err := checkAUE(); err == nil || !strings.Contains(output, "the extractor is not MIT") {
		t.Errorf("an extractor declaring MIT: err = %v\n%s", err, output)
	}

	declare("LicenseRef-Auto-Pigeon-Proprietary")
	if output, err := checkAUE(); err != nil {
		t.Fatalf("a proprietary extractor with no source offer was refused: %v\n%s", err, output)
	}
	if err := os.RemoveAll(w.bundles); err != nil {
		t.Fatal(err)
	}
	mustPlan(t, "bundle", "--matrix", w.matrix, "--aue-inputs", w.inputs, "--aucom-dist", w.dist,
		"--version", testVersion, "--out", w.bundles)
	w.releaseIt(t)

	var document struct {
		AUE struct {
			License string `json:"license"`
		} `json:"aue"`
	}
	if err := json.Unmarshal([]byte(readText(t, filepath.Join(w.release, "release-manifest.json"))), &document); err != nil {
		t.Fatal(err)
	}
	if document.AUE.License != "LicenseRef-Auto-Pigeon-Proprietary" {
		t.Errorf("the release manifest lists the extractor as %q", document.AUE.License)
	}
	notes := readText(t, filepath.Join(w.dir, "NOTES.md"))
	mustContain(t, "the release notes", notes, "declared `LicenseRef-Auto-Pigeon-Proprietary`",
		"Auto-Pigeon Extractor is proprietary", "Andrea D'Intino", "written authorization",
		"does not cover it")
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
	mustPlan(t, "bundle", "--matrix", w.matrix, "--aue-inputs", w.inputs, "--aucom-dist", w.dist,
		"--version", testVersion, "--out", w.bundles)
	w.releaseIt(t)
	if second := readText(t, filepath.Join(w.release, "SHA256SUMS")); second != first {
		t.Errorf("two builds of one release differ:\n%s\n---\n%s", first, second)
	}
}

// An eligible archive that lacks the extractor, either licence, or whose
// versions disagree, is refused by the check the release runs on it.
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
		"no extractor": {mutate("no-extractor", remove("dependencies/auto-pigeon-extractor"), true),
			"carries no auto-pigeon-extractor"},
		"a replaced extractor": {mutate("replaced", func(bundle string) {
			writeFakeExecutable(t, filepath.Join(bundle, "dependencies", "auto-pigeon-extractor"), "linux-amd64", "something else")
		}, false), "does not match its bundle manifest"},
		"an unlisted file": {mutate("unlisted", func(bundle string) {
			writeFile(t, filepath.Join(bundle, "extra.txt"), "surprise\n")
		}, false), "not in its bundle manifest"},
	}
	for name, c := range cases {
		output, err := runPlan(t, verifyArgs(c.archive, "linux-amd64")...)
		if err == nil || !strings.Contains(output, c.want) {
			t.Errorf("%s: err = %v, want a refusal naming %q:\n%s", name, err, c.want, output)
		}
	}

	// The same bytes, checked as another version or another platform.
	for name, args := range map[string][]string{
		"another Companion version": {"verify-archive", archive, "--platform", "linux-amd64", "--version", "1.501",
			"--aue-version", testAUEVersion, "--aue-commit", testAUECommit},
		"another extractor version": {"verify-archive", archive, "--platform", "linux-amd64", "--version", testVersion,
			"--aue-version", "1.208", "--aue-commit", testAUECommit},
		"another extractor commit": {"verify-archive", archive, "--platform", "linux-amd64", "--version", testVersion,
			"--aue-version", testAUEVersion, "--aue-commit", strings.Repeat("ef", 20)},
		"another platform": {"verify-archive", archive, "--platform", "linux-arm64", "--version", testVersion,
			"--aue-version", testAUEVersion, "--aue-commit", testAUECommit},
	} {
		if output, err := runPlan(t, args...); err == nil {
			t.Errorf("%s: accepted:\n%s", name, output)
		}
	}
	renamed := filepath.Join(t.TempDir(), "auto-pigeon-companion-1.501-linux-amd64.zip")
	writeFile(t, renamed, readText(t, archive))
	if output, err := runPlan(t, "verify-archive", renamed, "--platform", "linux-amd64", "--version", "1.501",
		"--aue-version", testAUEVersion, "--aue-commit", testAUECommit); err == nil {
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
		if strings.Contains(body, "secrets.") && name != "aue" {
			t.Errorf("job %s reads a secret; only `aue` reads one, to check out the extractor", name)
		}
	}
	if _, ok := jobs["publish"]; !ok {
		t.Fatal("release.yml has no publish job")
	}
	mustContain(t, "the aue job", jobs["aue"], "persist-credentials: false", "AUE_CHECKOUT_TOKEN is not set")
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

// The extractor's gates run against the AULIBS commit its own data names,
// checked out where its tests look for it: beside the extractor, as
// auto-pigeon-libraries (run 36031053076 checked it out as `aulibs`, and the
// extractor's contract tests could not see it).
func TestTheExtractorsGatesRunAgainstItsOwnAULIBSBesideIt(t *testing.T) {
	_, jobs := jobBlocks(t, repoFile(t, ".github", "workflows", "release.yml"))
	job := jobs["aue"]
	checkout := regexp.MustCompile(`(?s)- uses: actions/checkout@[0-9a-f]{40}[^\n]*\n        with:\n((?:          [^\n]*\n)+)`)
	var aue, aulibs string
	for _, match := range checkout.FindAllStringSubmatch(job, -1) {
		switch {
		case strings.Contains(match[1], "needs.plan.outputs.aulibs_repository"):
			aulibs = match[1]
		case strings.Contains(match[1], "needs.plan.outputs.aue_repository"):
			aue = match[1]
		}
	}
	if aue == "" || aulibs == "" {
		t.Fatalf("the aue job does not check out both the extractor and AULIBS:\n%s", job)
	}
	if !strings.Contains(aue, "          path: aue\n") {
		t.Errorf("the extractor is not checked out at `aue`:\n%s", aue)
	}
	// The sibling the extractor's tests resolve, and the only one.
	if !strings.Contains(aulibs, "          path: auto-pigeon-libraries\n") {
		t.Errorf("AULIBS is not checked out beside the extractor as auto-pigeon-libraries:\n%s", aulibs)
	}
	// The exact commit the pinned extractor names; never a branch, tag or latest.
	if !strings.Contains(aulibs, "          ref: ${{ steps.aue.outputs.aulibs_commit }}\n") {
		t.Errorf("AULIBS is not checked out at the commit the pinned extractor names:\n%s", aulibs)
	}
	if regexp.MustCompile(`ref: *(main|master|latest|refs/|v[0-9])`).MatchString(aulibs) {
		t.Errorf("AULIBS is checked out at a movable ref:\n%s", aulibs)
	}
	// Every AULIBS_DIR names that same checkout.
	dirs := regexp.MustCompile(`AULIBS_DIR: *([^\n]+)`).FindAllStringSubmatch(job, -1)
	if len(dirs) == 0 {
		t.Fatal("no step of the aue job names AULIBS_DIR")
	}
	for _, dir := range dirs {
		if dir[1] != "${{ github.workspace }}/auto-pigeon-libraries" {
			t.Errorf("AULIBS_DIR is %s; want the checkout beside the extractor", dir[1])
		}
	}
	// The extractor's own fast gate runs, unconditionally, in its checkout.
	gates := regexp.MustCompile(`(?s)- name: The extractor's own gates\n(.*?)\n      - `).FindStringSubmatch(job)
	if gates == nil {
		t.Fatal("the aue job has no step running the extractor's own gates")
	}
	mustContain(t, "the extractor's gates", gates[1], "working-directory: aue", "./scripts/test-fast.sh")
	for _, weakened := range []string{"if:", "continue-on-error", "|| true", "-skip", "AUE_UPDATE_FROZEN"} {
		if strings.Contains(gates[1], weakened) {
			t.Errorf("the extractor's gates are weakened with %q", weakened)
		}
	}
	// And the pinned extractor's AULIBS is the repository the plan named.
	mustContain(t, "the aue job", job, "release-plan.py aue-checkout --pin aucom/build/aue-pin.json --aue-dir aue")
}

// Nothing in a workflow or in the release tools fetches a program. The
// Companion downloads no executable (operator, 2026-09-23), and neither does
// the release that builds it: the extractor is BUILT from a pinned checkout.
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
	// The extractor is checked out — never downloaded — at the branch the plan
	// names, and built from the one commit that checkout resolved to.
	mustContain(t, "release.yml", repoFile(t, ".github", "workflows", "release.yml"),
		"ref: ${{ needs.plan.outputs.aue_ref }}", "COMMIT: ${{ steps.aue.outputs.commit }}", "./scripts/build-release.sh")
}

// A native acceptance run refuses a platform the machine is not.
func TestAcceptanceRefusesAnotherMachinesPlatform(t *testing.T) {
	other := "windows-arm64"
	if strings.HasPrefix(runtimeTarget(), "windows") {
		other = "linux-arm64"
	}
	command := exec.Command(python(t), filepath.Join(repoRoot(t), "build", "release-acceptance.py"),
		"--archive", "none.zip", "--platform", other, "--version", testVersion, "--aue-version", testAUEVersion,
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

