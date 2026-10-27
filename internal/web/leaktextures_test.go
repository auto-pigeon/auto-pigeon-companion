package web

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/aue"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle/fixturewad"
)

// NEW_357 (F-LEAKWAD): an editor's Quake 1 leak test compiles with the texture
// bundle of the exact revision it pinned — the bundle Build & Run would use,
// fetched and verified by the same owner — and with nothing else.
//
// The compiler here is a stand-in that does what qbsp does with WADs: it reads
// the map's worldspawn `wad` key, looks for each WAD under `-wadpath` (the
// declared path, then the file name) and beside the map, and prints what it
// opened with the digest of the bytes it opened. So every assertion below is
// about what the compiler was actually handed, not about a helper's answer.

const leakQbspFlag = "-aucom-web-leak-qbsp"

// leakQbspMain is the stand-in qbsp: `[-wadpath dir] <map> <bsp>`.
func leakQbspMain(args []string) int {
	wadpath := ""
	if len(args) >= 2 && args[0] == "-wadpath" {
		wadpath, args = args[1], args[2:]
	}
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "leak-qbsp: takes a map and a BSP")
		return 2
	}
	source, destination := args[0], args[1]
	stem := strings.TrimSuffix(destination, filepath.Ext(destination))
	log := &bytes.Buffer{}
	say := func(format string, values ...any) { fmt.Fprintf(log, format+"\n", values...) }
	say("---- qbsp / leak-qbsp fixture ----")
	if wadpath != "" {
		say("argv -wadpath %s", wadpath)
	}
	holdIfAsked(source)
	contents, err := os.ReadFile(source)
	if err != nil {
		fmt.Fprintln(os.Stderr, "leak-qbsp:", err)
		return 1
	}
	declared, err := playrun.DeclaredWADs(source)
	if err != nil {
		fmt.Fprintln(os.Stderr, "leak-qbsp:", err)
		return 1
	}
	var opened [][]byte
	for _, name := range declared {
		candidates := []string{filepath.Join(filepath.Dir(source), filepath.FromSlash(name))}
		if wadpath != "" {
			candidates = append([]string{filepath.Join(wadpath, filepath.FromSlash(name)),
				filepath.Join(wadpath, filepath.Base(name))}, candidates...)
		}
		found := false
		for _, candidate := range candidates {
			body, err := os.ReadFile(candidate)
			if err != nil {
				continue
			}
			sum := sha256.Sum256(body)
			say("Opened WAD: %s sha256=%s", name, hex.EncodeToString(sum[:]))
			opened, found = append(opened, body), true
			break
		}
		if !found {
			say("addArchive: WARNING: archive '%s' not found", name)
		}
	}
	if len(declared) > 0 && len(opened) == 0 {
		say("No valid WAD filenames")
	}
	for _, line := range strings.Split(string(contents), "\n") {
		texture, uses := strings.CutPrefix(strings.TrimSpace(line), "// uses ")
		if !uses {
			continue
		}
		present := false
		for _, wad := range opened {
			present = present || bytes.Contains(wad, append([]byte(texture), 0))
		}
		if !present {
			say("unable to find texture %s", texture)
		}
	}
	// ericw-tools 2.0 says this for every Quake 1 map, whatever it opened
	// (measured live, NEW_357): `skip` has no picture in any WAD.
	say("WARNING: unable to find texture skip")
	leaks := strings.Contains(string(contents), "// leaks")
	if leaks {
		say("Reached occupant at (0 0 0), no filling performed.")
		say("Leak file written to %s.pts", filepath.Base(stem))
		_ = os.WriteFile(stem+".pts", []byte("0 0 0\n64 0 0\n"), 0o600)
	} else {
		_ = os.WriteFile(destination, append([]byte("BSP:"), contents...), 0o600)
		say("WriteBSPFile: %s", filepath.Base(destination))
	}
	_ = os.WriteFile(stem+".log", log.Bytes(), 0o600)
	fmt.Print(log.String())
	if leaks {
		return 1
	}
	return 0
}

// leakToolJSON is a Quake 1 compile action over the stand-in qbsp, with the
// WAD rules the real Auto-Pigeon EricW profile has.
func leakToolJSON(self string) []byte {
	document := map[string]any{}
	if err := json.Unmarshal(fixtureToolJSON(self), &document); err != nil {
		panic(err)
	}
	document["id"], document["name"] = "aucom.fixture.leak-qbsp", "Fixture leak qbsp"
	document["capabilities"] = []map[string]any{{"id": "fixture.leak.compile", "title": "Compile",
		"consumes": []string{"q1.map.source"}, "produces": []string{"q1.bsp", "q1.pts", "q1.compile.log"}}}
	document["actions"] = []map[string]any{{
		"id": "compile", "title": "Compile", "capability": "fixture.leak.compile", "executable": "tool",
		"args": []any{leakQbspFlag,
			map[string]any{"value": "-wadpath", "when": map[string]any{"root": "content_root"}},
			map[string]any{"value": "{root.content_root}", "when": map[string]any{"root": "content_root"}},
			"{input.source_map}", "{output.bsp}"},
		"working_dir": map[string]any{"root": "workspace"},
		"inputs": []map[string]any{{"name": "source_map", "title": "Source", "role": "q1.map.source",
			"required": true, "extensions": []string{".map"}}},
		"outputs": []map[string]any{
			{"name": "bsp", "title": "BSP", "role": "q1.bsp", "path": "{option.basename}.bsp"},
			{"name": "pts", "title": "Leak point file", "role": "q1.pts", "path": "{option.basename}.pts", "optional": true},
			{"name": "log", "title": "Compiler log", "role": "q1.compile.log", "path": "{option.basename}.log", "optional": true},
		},
		"options": []map[string]any{{"name": "basename", "title": "Name", "type": "text", "default": "level", "max_length": 64}},
		"diagnostics": []map[string]any{
			{"id": "no_valid_wad", "match": "No valid WAD filenames", "severity": "error",
				"message": "The compiler opened none of the WADs the map names."},
			{"id": "wad_not_found", "match": "addArchive: WARNING: archive", "severity": "warning",
				"message": "A WAD the map names was not where the compiler looked."},
			{"id": "texture_not_found", "match": "unable to find texture", "severity": "warning",
				"message": "A texture the map uses is not in any WAD that was opened."},
			{"id": "wad_opened", "match": "Opened WAD", "severity": "info", "message": "A texture WAD was opened."},
		},
		"roots": []map[string]any{
			{"role": "workspace", "access": "read_write", "purpose": "compile"},
			{"role": "content_root", "access": "read", "optional": true, "purpose": "find the texture WADs the map names"},
		},
		"timeout_seconds": 120,
	}}
	return encodeFixture(document)
}

// leakPipelineJSON is a one-stage Quake 1 leak test over that tool.
func leakPipelineJSON() []byte {
	return encodeFixture(map[string]any{
		"schema_version": "aucom.profile/1.1", "kind": "pipeline",
		"id": "aucom.fixture.q1-leak", "version": "1.0.0", "name": "Fixture Quake 1 leak test",
		"summary":      "Only the compile step, with the leak test on.",
		"publisher":    map[string]any{"name": "Auto-Pigeon tests"},
		"license":      map[string]any{"spdx": "MIT", "name": "MIT"},
		"game_profile": map[string]any{"slug": "quake1", "engine_family": "quake1"},
		"inputs": []map[string]any{{"name": "source_map", "title": "Map source", "role": "q1.map.source",
			"required": true, "extensions": []string{".map", ".apmap"}}},
		"steps": []map[string]any{{"id": "compile", "title": "Compile", "capability": "fixture.leak.compile",
			"tool": "aucom.fixture.leak-qbsp", "inputs": []map[string]any{{"name": "source_map", "from": "pipeline.source_map"}}}},
		"outputs": []map[string]any{
			{"name": "pts", "title": "Leak point file", "role": "q1.pts", "from": "compile.pts", "optional": true},
			{"name": "compile_log", "title": "qbsp log", "role": "q1.compile.log", "from": "compile.log", "optional": true},
		},
	})
}

// fixtureConverter is the extractor: it writes the `.map` an APMap carries in
// its `fixture_map` field, and counts how often it ran.
type fixtureConverter struct {
	mu    sync.Mutex
	calls int
}

func (c *fixtureConverter) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	var input, output string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--input":
			input = args[i+1]
		case "--output":
			output = args[i+1]
		}
	}
	raw, err := os.ReadFile(input)
	if err != nil {
		return nil, err
	}
	var document struct {
		Map string `json:"fixture_map"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	stem := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	return []byte("{}"), os.WriteFile(filepath.Join(output, stem+".map"), []byte(document.Map), 0o600)
}

func (c *fixtureConverter) Provenance() aue.Provenance {
	return aue.Provenance{Mode: aue.ModeBundled, Verified: true}
}

// q1Room is a tiny Quake 1 room: worldspawn names its WADs, and each `// uses`
// line is a texture its faces use.
func q1Room(wads string, leaks bool, textures ...string) string {
	text := ""
	for _, texture := range textures {
		text += "// uses " + texture + "\n"
	}
	if leaks {
		text += "// leaks\n"
	}
	return text + "{\n\"classname\" \"worldspawn\"\n\"wad\" \"" + wads + "\"\n}\n"
}

// leakBundle is an AUB texture export of the WADs named in order, carrying
// only those in carried.
func leakBundle(mapID string, revision int, order []string, carried map[string][]byte) []byte {
	manifest := texturebundle.Manifest{SchemaVersion: texturebundle.Schema, MapID: mapID, MapName: "NEW_357 room",
		Revision: revision, Game: "quake1", ExportedAt: "2026-10-27T00:00:00Z", WADsDeclared: order,
		Requirements: []texturebundle.Requirement{}, Files: []texturebundle.File{}, CompilerReady: true,
		CompilerRefusals: []string{}}
	buffer := &bytes.Buffer{}
	writer := zip.NewWriter(buffer)
	add := func(name string, body []byte) {
		out, err := writer.Create(name)
		if err != nil {
			panic(err)
		}
		if _, err := out.Write(body); err != nil {
			panic(err)
		}
	}
	for index, name := range order {
		requirement := texturebundle.Requirement{Order: index, Name: name, Game: "quake1", Kind: "wad",
			Status: "missing", Files: []texturebundle.File{}}
		if body, ok := carried[name]; ok {
			sum := sha256.Sum256(body)
			file := texturebundle.File{Path: name, Source: name, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(body))}
			requirement.Status, requirement.Included, requirement.Files = "resolved", true, []texturebundle.File{file}
			manifest.Files = append(manifest.Files, file)
			add(name, body)
		} else {
			manifest.CompilerReady = false
			manifest.CompilerRefusals = append(manifest.CompilerRefusals, "texture_source_missing: "+name)
		}
		manifest.Requirements = append(manifest.Requirements, requirement)
	}
	add(texturebundle.LicensesName, []byte("# Attribution\n"))
	encoded, err := json.Marshal(manifest)
	if err != nil {
		panic(err)
	}
	add(texturebundle.ManifestName, encoded)
	if err := writer.Close(); err != nil {
		panic(err)
	}
	return buffer.Bytes()
}

// leakMachine is a signed-in machine whose Quake 1 leak test is the fixture
// pipeline above, with the fixture extractor.
func newLeakMachine(t *testing.T) (*machine, *fixtureConverter) {
	t.Helper()
	m := newMachine(t)
	m.writeProfile("aucom.fixture.leak-qbsp.json", leakToolJSON(mustExecutable(t)))
	m.writeProfile("aucom.fixture.q1-leak.json", leakPipelineJSON())
	m.approveAndBind("aucom.fixture.leak-qbsp")
	converter := &fixtureConverter{}
	m.server.runner = converter
	m.signIn()
	if status, body := m.call(http.MethodPost, "/api/v1/leak-test/pipelines",
		map[string]any{"game": "quake1", "pipeline": "aucom.fixture.q1-leak"}); status != http.StatusOK {
		t.Fatalf("pinning the fixture leak test = %d: %v", status, body["error"])
	}
	return m, converter
}

// saveRoom makes the backend's one map a saved Quake 1 APMap at a revision,
// with the texture bundle AUB would export for it.
func (m *machine) saveRoom(mapID string, revision int, room string, bundle []byte) {
	m.t.Helper()
	apmap, err := json.Marshal(map[string]any{"apmap_version": "1.5", "document_id": "fixture:" + mapID,
		"revision": revision, "game": "quake1", "entities": []any{}, "fixture_map": room})
	if err != nil {
		m.t.Fatal(err)
	}
	m.backend.mu.Lock()
	defer m.backend.mu.Unlock()
	m.backend.asset.assetID, m.backend.asset.revision = mapID, revision
	m.backend.asset.revisionID = fmt.Sprintf("%s-rev-%d", mapID, revision)
	m.backend.asset.fileName, m.backend.asset.body = "fixture.apmap", apmap
	m.backend.textures = bundle
}

// leakRun is one leak build, read back the way the page and the editor read it.
type leakRun struct {
	status   int
	body     map[string]any
	manifest map[string]any
	result   map[string]any
	log      string
}

// startLeak sends the build the page sends for a reviewed request.
func (m *machine) startLeak(link aub.LeakTestLink, roots map[string]string) leakRun {
	m.t.Helper()
	request := map[string]any{
		"pipeline":        "aucom.fixture.q1-leak",
		"inputs":          map[string]string{"source_map": "aub:map/" + link.AssetID + "@" + m.backend.asset.revisionID + "#fixture.apmap"},
		"leak_request_id": link.RequestID,
	}
	if roots != nil {
		request["roots"] = roots
	}
	run := leakRun{}
	run.status, run.body = m.call(http.MethodPost, "/api/v1/build/runs", request)
	if run.status != http.StatusAccepted {
		return run
	}
	id, _ := run.body["build"].(string)
	run.manifest = m.waitForBuild(id)
	// The goroutine that ran it returns the result and then removes the
	// texture copy; wait for both, the way the page waits for the return.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, body := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/return", nil); body["state"] != "pending" &&
			body["state"] != "sending" && len(m.leakTextureDirs()) == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	status, result := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/result", nil)
	if status == http.StatusOK {
		run.result = result
		run.log, _ = result["log"].(string)
	}
	return run
}

// leakTextureDirs is every job-owned texture copy left in the builds folder.
func (m *machine) leakTextureDirs() []string {
	entries, _ := os.ReadDir(m.builds)
	var out []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), leakTexturesPrefix) {
			out = append(out, entry.Name())
		}
	}
	return out
}

func (m *machine) textureExports() int {
	m.backend.mu.Lock()
	defer m.backend.mu.Unlock()
	return m.backend.textureExports
}

// rootBundle is the bundle the manifest records as the compile's texture root.
func rootBundle(t *testing.T, manifest map[string]any) map[string]any {
	t.Helper()
	roots, _ := manifest["roots"].([]any)
	for _, raw := range roots {
		root, _ := raw.(map[string]any)
		if root["role"] != "content_root" {
			continue
		}
		source, _ := root["source"].(map[string]any)
		if source["kind"] != string(build.RootFromTextureBundle) {
			t.Fatalf("the texture root came from %v", source["kind"])
		}
		bundle, _ := source["bundle"].(map[string]any)
		return bundle
	}
	t.Fatalf("the build recorded no texture root: %v", manifest["roots"])
	return nil
}

// compileDiagnostics is the rule ids the compile step's output matched.
func compileDiagnostics(manifest map[string]any) []string {
	var ids []string
	steps, _ := manifest["steps"].([]any)
	for _, raw := range steps {
		step, _ := raw.(map[string]any)
		diagnostics, _ := step["diagnostics"].([]any)
		for _, rawDiagnostic := range diagnostics {
			diagnostic, _ := rawDiagnostic.(map[string]any)
			ids = append(ids, diagnostic["rule_id"].(string))
		}
	}
	return ids
}

// findingsOf is the compiler lines the compile step's findings of one
// severity matched.
func findingsOf(manifest map[string]any, severity string) []string {
	var lines []string
	steps, _ := manifest["steps"].([]any)
	for _, raw := range steps {
		step, _ := raw.(map[string]any)
		diagnostics, _ := step["diagnostics"].([]any)
		for _, rawDiagnostic := range diagnostics {
			diagnostic, _ := rawDiagnostic.(map[string]any)
			if diagnostic["severity"] == severity {
				lines = append(lines, diagnostic["raw"].(string))
			}
		}
	}
	return lines
}

func compileArgs(manifest map[string]any) []string {
	steps, _ := manifest["steps"].([]any)
	step, _ := steps[0].(map[string]any)
	command, _ := step["command"].(map[string]any)
	raw, _ := command["args"].([]any)
	args := make([]string, 0, len(raw))
	for _, value := range raw {
		args = append(args, value.(string))
	}
	return args
}

// T1, T2: cold, then warm — the same WAD bytes reach the compiler both times,
// through a copy this build owns, and the result is a whole compile.
func TestAnEditorLeakTestCompilesWithItsRevisionsTextureBundle(t *testing.T) {
	m, _ := newLeakMachine(t)
	floor, arrow := fixturewad.WAD("n357_floor", 11), fixturewad.WAD("n357_arrow", 12)
	wad := append(append([]byte{}, floor...), arrow...)
	m.saveRoom("new357a", 1, q1Room("new357.wad", false, "n357_floor", "n357_arrow"),
		leakBundle("new357a", 1, []string{"new357.wad"}, map[string][]byte{"new357.wad": wad}))

	cold := m.startLeak(m.receiveLeakLink(strings.Repeat("1", 32), "quake1"), nil)
	if cold.status != http.StatusAccepted || cold.result == nil {
		t.Fatalf("cold: %d %v", cold.status, cold.body)
	}
	opened := "Opened WAD: new357.wad sha256=" + sha256Hex(wad)
	for _, want := range []string{opened, "argv -wadpath "} {
		if !strings.Contains(cold.log, want) {
			t.Errorf("cold: the compiler's log has no %q:\n%s", want, cold.log)
		}
	}
	for _, refused := range []string{"No valid WAD filenames", "not found", "unable to find texture n357"} {
		if strings.Contains(cold.log, refused) {
			t.Errorf("cold: the compiler said %q:\n%s", refused, cold.log)
		}
	}
	if ids := strings.Join(compileDiagnostics(cold.manifest), ","); strings.Contains(ids, "no_valid_wad") ||
		strings.Contains(ids, "wad_not_found") || cold.manifest["state"] != "succeeded" {
		t.Errorf("cold: state %v, findings %s", cold.manifest["state"], ids)
	}
	// The one line every 2.0 compile prints, about `skip`, is information: a
	// sealed test with every texture found is a complete result.
	if warned := findingsOf(cold.manifest, "warning"); len(warned) != 0 {
		t.Errorf("cold: a compile that found every texture warned: %v", warned)
	}
	args := compileArgs(cold.manifest)
	wadpath := ""
	for i, arg := range args {
		if arg == "-wadpath" && i+1 < len(args) {
			wadpath = args[i+1]
		}
	}
	if !strings.HasPrefix(filepath.Base(wadpath), leakTexturesPrefix) || filepath.Dir(wadpath) != m.builds {
		t.Errorf("cold: -wadpath %q is not a copy this build owns in %s", wadpath, m.builds)
	}
	bundle := rootBundle(t, cold.manifest)
	if bundle["map_id"] != "new357a" || bundle["revision"] != float64(1) || bundle["compiler_ready"] != true {
		t.Errorf("cold: recorded bundle %v", bundle)
	}
	if cold.result["map_id"] != "new357a" || cold.result["revision"] != float64(1) ||
		cold.result["revision_id"] != "new357a-rev-1" {
		t.Errorf("cold: result %v", cold.result)
	}
	if exports := m.textureExports(); exports != 1 {
		t.Errorf("cold: %d texture export(s), want 1", exports)
	}
	if left := m.leakTextureDirs(); len(left) != 0 {
		t.Errorf("cold: the build's texture copy was left behind: %v", left)
	}

	// Warm: the same revision again, with AUB now unable to send a bundle at
	// all. Only the verified cache entry can supply it.
	m.backend.mu.Lock()
	m.backend.textures = []byte("not a bundle")
	m.backend.mu.Unlock()
	warm := m.startLeak(m.receiveLeakLink(strings.Repeat("2", 32), "quake1"), nil)
	if warm.status != http.StatusAccepted || !strings.Contains(warm.log, opened) {
		t.Fatalf("warm: %d %v\n%s", warm.status, warm.body, warm.log)
	}
	if exports := m.textureExports(); exports != 1 {
		t.Errorf("warm: %d texture export(s), want the one cold fetch", exports)
	}
	if digest := rootBundle(t, warm.manifest)["digest"]; digest != bundle["digest"] {
		t.Errorf("warm: bundle %v, cold %v", digest, bundle["digest"])
	}
}

// T3: two maps whose bundles hold a WAD of the same name with different
// pixels. Each compile reads its own map's, cold or warm, in either order.
func TestALeakTestUsesItsOwnMapsBundleNotAnotherWithTheSameWADName(t *testing.T) {
	m, _ := newLeakMachine(t)
	red, blue := fixturewad.WAD("n357_floor", 40), fixturewad.WAD("n357_floor", 200)
	room := q1Room("shared.wad", false, "n357_floor")
	maps := []struct {
		id  string
		wad []byte
	}{{"new357red", red}, {"new357blue", blue}, {"new357red", red}}
	for index, which := range maps {
		m.saveRoom(which.id, 1, room, leakBundle(which.id, 1, []string{"shared.wad"}, map[string][]byte{"shared.wad": which.wad}))
		run := m.startLeak(m.receiveLeakLink(strings.Repeat(fmt.Sprint(index+3), 32), "quake1"), nil)
		if run.status != http.StatusAccepted {
			t.Fatalf("%s: %d %v", which.id, run.status, run.body)
		}
		if !strings.Contains(run.log, "Opened WAD: shared.wad sha256="+sha256Hex(which.wad)) {
			t.Errorf("%s (run %d) compiled with another WAD:\n%s", which.id, index, run.log)
		}
		if bundle := rootBundle(t, run.manifest); bundle["map_id"] != which.id {
			t.Errorf("%s (run %d) recorded the bundle of %v", which.id, index, bundle["map_id"])
		}
	}
	if exports := m.textureExports(); exports != 2 {
		t.Errorf("%d texture exports, want one per map", exports)
	}
}

// T4: a save after the editor's click makes the request stale. Nothing is
// fetched for the new revision, nothing is compiled, and the request is not
// quietly moved to @current.
func TestAStaleLeakRequestFetchesNoTexturesAndCompilesNothing(t *testing.T) {
	m, converter := newLeakMachine(t)
	wad := fixturewad.WAD("n357_floor", 9)
	m.saveRoom("new357s", 1, q1Room("new357.wad", false, "n357_floor"),
		leakBundle("new357s", 1, []string{"new357.wad"}, map[string][]byte{"new357.wad": wad}))
	link := m.receiveLeakLink(strings.Repeat("6", 32), "quake1")
	m.saveRoom("new357s", 2, q1Room("new357.wad", true, "n357_floor"),
		leakBundle("new357s", 2, []string{"new357.wad"}, map[string][]byte{"new357.wad": wad}))

	if status, body := m.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil); status != http.StatusConflict {
		t.Fatalf("the review of a stale request: %d %v", status, body)
	}
	jobs := len(m.jobIDs())
	run := m.startLeak(link, nil) // the page's revision is now revision 2's id
	if run.status != http.StatusConflict {
		t.Fatalf("a build for another revision than the request's: %d %v", run.status, run.body)
	}
	if got := len(m.jobIDs()); got != jobs || m.textureExports() != 0 || len(m.leakTextureDirs()) != 0 {
		t.Errorf("a stale request ran %d job(s), fetched %d bundle(s), left %v", got-jobs, m.textureExports(), m.leakTextureDirs())
	}
	if pending := m.server.pendingLeakRequest(link.RequestID); pending == nil || pending.Revision != 1 {
		t.Errorf("the request was consumed or moved: %+v", pending)
	}
	_ = converter
}

// T5, T6: a bundle that cannot be had, or does not verify, is a refusal
// before anything runs — never a WAD of the same name from this machine — and
// the request stays pending so it can be built again.
func TestALeakTestWhoseTexturesCannotBeHadRefusesAndUsesNoHostWAD(t *testing.T) {
	good := fixturewad.WAD("n357_floor", 21)
	tampered := leakBundle("new357r", 1, []string{"new357.wad"}, map[string][]byte{"new357.wad": good})
	// The member's bytes change after the manifest recorded their digest: a
	// download that is not what AUB declared.
	tampered = replaceMember(t, tampered, "new357.wad", fixturewad.WAD("n357_floor", 22))
	for name, c := range map[string]struct {
		refusal int
		bundle  []byte
		want    string
	}{
		"AUB refuses the export":     {refusal: http.StatusForbidden, want: "could not be fetched, so nothing was compiled"},
		"the bundle does not verify": {bundle: tampered, want: "could not be fetched, so nothing was compiled"},
	} {
		t.Run(name, func(t *testing.T) {
			m, _ := newLeakMachine(t)
			bundle := c.bundle
			if bundle == nil {
				bundle = leakBundle("new357r", 1, []string{"new357.wad"}, map[string][]byte{"new357.wad": good})
			}
			m.saveRoom("new357r", 1, q1Room("new357.wad", false, "n357_floor"), bundle)
			m.backend.mu.Lock()
			m.backend.textureRefusal = c.refusal
			m.backend.mu.Unlock()
			// A folder on this machine with a WAD of the right name: the page
			// sending it must not make it the answer.
			host := filepath.Join(m.dir, "host-wads")
			writeFixtureFile(t, filepath.Join(host, "new357.wad"), string(fixturewad.WAD("n357_floor", 99)))
			link := m.receiveLeakLink(strings.Repeat("8", 32), "quake1")
			jobs := len(m.jobIDs())
			run := m.startLeak(link, map[string]string{"content_root": host})
			if run.status < 400 || !strings.Contains(fmt.Sprint(run.body["error"]), c.want) {
				t.Fatalf("%d %v", run.status, run.body)
			}
			if strings.Contains(fmt.Sprint(run.body["error"]), m.backend.url()) {
				t.Errorf("the refusal names the account server's address: %v", run.body["error"])
			}
			if got := len(m.jobIDs()); got != jobs || len(m.leakTextureDirs()) != 0 {
				t.Errorf("%d job(s) started, texture copies %v", got-jobs, m.leakTextureDirs())
			}
			cache, err := m.server.textureCache()
			if err != nil {
				t.Fatal(err)
			}
			if _, found := cache.Lookup(texturebundle.Expect{MapID: "new357r", Revision: 1}); found {
				t.Error("a bundle that was refused is in the cache as verified")
			}
			if m.server.pendingLeakRequest(link.RequestID) == nil {
				t.Error("the refused build used up the editor's request")
			}
		})
	}
}

// replaceMember rewrites one member of a ZIP and keeps every other, the
// manifest among them, as it was.
func replaceMember(t *testing.T, archive []byte, name string, body []byte) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	buffer := &bytes.Buffer{}
	writer := zip.NewWriter(buffer)
	for _, file := range reader.File {
		contents := body
		if file.Name != name {
			opened, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			contents, err = io.ReadAll(opened)
			opened.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		out, err := writer.Create(file.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// T6, the copy: a cache file changed after it was verified is not handed on.
func TestATextureCopyRefusesBytesThatNoLongerMatchTheirDigest(t *testing.T) {
	dir := t.TempDir()
	body := fixturewad.WAD("n357_floor", 5)
	source := filepath.Join(dir, "cache.wad")
	if err := os.WriteFile(source, append([]byte{}, body...), 0o600); err != nil {
		t.Fatal(err)
	}
	member := texturebundle.Member{Path: "new357.wad", SHA256: sha256Hex(body), Bytes: int64(len(body))}
	if err := copyVerified(source, filepath.Join(dir, "ok", "new357.wad"), member); err != nil {
		t.Fatalf("an intact file: %v", err)
	}
	if err := os.WriteFile(source, append(body[:len(body)-1:len(body)-1], 0xff), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyVerified(source, filepath.Join(dir, "bad", "new357.wad"), member); err == nil ||
		!strings.Contains(err.Error(), "no longer matches") {
		t.Fatalf("a changed file: %v", err)
	}
}

// T7: a bundle AUB says is incomplete is compiled as it is. The compiler's
// warnings name what was missing, and nothing hides them.
func TestALeakTestWithAnIncompleteBundleKeepsTheMissingWADVisible(t *testing.T) {
	m, _ := newLeakMachine(t)
	floor := fixturewad.WAD("n357_floor", 31)
	m.saveRoom("new357i", 1, q1Room("new357.wad;n357_extra.wad", false, "n357_floor", "n357_other"),
		leakBundle("new357i", 1, []string{"new357.wad", "n357_extra.wad"}, map[string][]byte{"new357.wad": floor}))
	run := m.startLeak(m.receiveLeakLink(strings.Repeat("9", 32), "quake1"), nil)
	if run.status != http.StatusAccepted || run.result == nil {
		t.Fatalf("%d %v", run.status, run.body)
	}
	for _, want := range []string{"Opened WAD: new357.wad sha256=" + sha256Hex(floor),
		"archive 'n357_extra.wad' not found", "unable to find texture n357_other"} {
		if !strings.Contains(run.log, want) {
			t.Errorf("the compiler's log has no %q:\n%s", want, run.log)
		}
	}
	ids := strings.Join(compileDiagnostics(run.manifest), ",")
	if !strings.Contains(ids, "wad_not_found") || !strings.Contains(ids, "texture_not_found") {
		t.Errorf("the findings lost the missing WAD: %s", ids)
	}
	if warned := strings.Join(findingsOf(run.manifest, "warning"), "|"); !strings.Contains(warned, "n357_other") ||
		!strings.Contains(warned, "n357_extra.wad") || strings.Contains(warned, "texture skip") {
		t.Errorf("the warnings are not the missing WAD and texture: %s", warned)
	}
	if bundle := rootBundle(t, run.manifest); bundle["compiler_ready"] != false {
		t.Errorf("the recorded bundle calls itself ready: %v", bundle)
	}
	_, detail := m.call(http.MethodGet, "/api/v1/build/runs/"+run.body["build"].(string), nil)
	if log, _ := detail["log"].(string); !strings.Contains(log, "not complete enough to compile") {
		t.Errorf("the build log does not say the bundle is incomplete:\n%s", log)
	}
}

// T8: the same link handled twice, and Build pressed twice at once: one
// request, one build, one result, and no copy left behind by the loser.
func TestOneLeakRequestIsOneBuildHoweverOftenItArrives(t *testing.T) {
	m, _ := newLeakMachine(t)
	wad := fixturewad.WAD("n357_floor", 3)
	m.saveRoom("new357d", 1, q1Room("new357.wad", false, "n357_floor"),
		leakBundle("new357d", 1, []string{"new357.wad"}, map[string][]byte{"new357.wad": wad}))
	link := m.receiveLeakLink(strings.Repeat("a", 32), "quake1")
	m.receiveLeakLink(link.RequestID, "quake1") // the handler ran again for the same link
	jobs := len(m.jobIDs())

	request := map[string]any{
		"pipeline":        "aucom.fixture.q1-leak",
		"inputs":          map[string]string{"source_map": "aub:map/new357d@" + m.backend.asset.revisionID + "#fixture.apmap"},
		"leak_request_id": link.RequestID,
	}
	statuses := make([]int, 2)
	ids := make([]string, 2)
	var wait sync.WaitGroup
	for i := range statuses {
		wait.Add(1)
		go func() {
			defer wait.Done()
			status, body := m.call(http.MethodPost, "/api/v1/build/runs", request)
			statuses[i] = status
			ids[i], _ = body["build"].(string)
		}()
	}
	wait.Wait()
	accepted := 0
	for i, status := range statuses {
		if status == http.StatusAccepted {
			accepted++
			m.waitForBuild(ids[i])
		} else if status != http.StatusConflict {
			t.Errorf("the second press: %d", status)
		}
	}
	if accepted != 1 {
		t.Fatalf("%d of two presses started a build: %v", accepted, statuses)
	}
	if again := m.startLeak(link, nil); again.status != http.StatusConflict {
		t.Errorf("a used request built again: %d", again.status)
	}
	if got := len(m.jobIDs()); got != jobs+1 {
		t.Errorf("%d job(s) for one request", got-jobs)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(m.leakTextureDirs()) != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if left := m.leakTextureDirs(); len(left) != 0 {
		t.Errorf("texture copies left behind: %v", left)
	}
}

// T9: a Companion restarted with a request pending builds it with the same
// pinned map and bundle, and removes the texture copies a stopped one left.
func TestARestartedCompanionBuildsAPendingRequestWithItsPinnedBundle(t *testing.T) {
	m, converter := newLeakMachine(t)
	wad := fixturewad.WAD("n357_floor", 17)
	m.saveRoom("new357k", 1, q1Room("new357.wad", false, "n357_floor"),
		leakBundle("new357k", 1, []string{"new357.wad"}, map[string][]byte{"new357.wad": wad}))
	first := m.startLeak(m.receiveLeakLink(strings.Repeat("b", 32), "quake1"), nil)
	if first.status != http.StatusAccepted {
		t.Fatalf("%d %v", first.status, first.body)
	}
	link := m.receiveLeakLink(strings.Repeat("c", 32), "quake1")

	stale := filepath.Join(m.builds, leakTexturesPrefix+"left-by-a-killed-run")
	fresh := filepath.Join(m.builds, leakTexturesPrefix+"another-process-is-using")
	for _, dir := range []string{stale, fresh} {
		writeFixtureFile(t, filepath.Join(dir, "new357.wad"), "WAD2")
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	restarted := m.restartWithConfig()
	restarted.server.runner = converter
	if _, err := os.Stat(stale); err == nil {
		t.Error("a texture copy a stopped Companion left was not removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a recent texture copy was removed at start-up")
	}
	_ = os.RemoveAll(fresh)
	if status, body := restarted.call(http.MethodGet, "/api/v1/leak-test/pending?request_id="+link.RequestID, nil); status != http.StatusOK ||
		body["revision_id"] != "new357k-rev-1" {
		t.Fatalf("the pending request after a restart: %d %v", status, body)
	}
	second := restarted.startLeak(link, nil)
	if second.status != http.StatusAccepted || !strings.Contains(second.log, "Opened WAD: new357.wad sha256="+sha256Hex(wad)) {
		t.Fatalf("%d %v\n%s", second.status, second.body, second.log)
	}
	if a, b := rootBundle(t, first.manifest), rootBundle(t, second.manifest); a["digest"] != b["digest"] ||
		b["map_id"] != "new357k" || b["revision"] != float64(1) {
		t.Errorf("before %v, after %v", a, b)
	}
}

// restartWithConfig is restart() keeping this machine's config folder, where
// the pending request lives.
func (m *machine) restartWithConfig() *machine {
	m.t.Helper()
	next := m.restart()
	next.server.paths.ConfigDir = filepath.Join(m.dir, "config")
	next.server.mu.Lock()
	next.server.settings.LeakTestPipelines = map[string]string{"quake1": "aucom.fixture.q1-leak"}
	next.server.mu.Unlock()
	// The sweep ran in NewServer, before the paths above were set; it reads
	// only the builds folder, which restart() kept.
	return next
}

// T10: cancelled while fetching, and cancelled while the compiler runs: the
// build's texture copy is gone, nothing was published as verified, and no
// result says the map was tested.
func TestACancelledLeakTestLeavesNoTexturesAndNoResult(t *testing.T) {
	m, _ := newLeakMachine(t)
	wad := fixturewad.WAD("n357_floor", 8)
	gate := filepath.Join(m.dir, "release-compiler")
	m.saveRoom("new357c", 1, holdUntilMarker+gate+"\n"+q1Room("new357.wad", false, "n357_floor"),
		leakBundle("new357c", 1, []string{"new357.wad"}, map[string][]byte{"new357.wad": wad}))

	// While fetching: the request's own context ends before the bundle is had.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source := build.SourceRef{AssetType: aub.AssetTypeMap, AssetID: "new357c", Revision: 1}
	if _, _, err := m.server.stageLeakTextures(ctx, source, filepath.Join(m.dir, "none.map")); err == nil {
		t.Fatal("a cancelled fetch staged textures")
	}
	cache, _ := m.server.textureCache()
	if _, found := cache.Lookup(texturebundle.Expect{MapID: "new357c", Revision: 1}); found || len(m.leakTextureDirs()) != 0 {
		t.Fatalf("a cancelled fetch left a verified entry (%v) or copies %v", found, m.leakTextureDirs())
	}

	// While the compiler holds: cancel the build.
	link := m.receiveLeakLink(strings.Repeat("d", 32), "quake1")
	status, body := m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline":        "aucom.fixture.q1-leak",
		"inputs":          map[string]string{"source_map": "aub:map/new357c@" + m.backend.asset.revisionID + "#fixture.apmap"},
		"leak_request_id": link.RequestID,
	})
	if status != http.StatusAccepted {
		t.Fatalf("%d %v", status, body)
	}
	id := body["build"].(string)
	deadline := time.Now().Add(20 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("the compiler never started")
		}
		_, detail := m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
		if strings.Contains(fmt.Sprint(detail["log"]), "holding") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(m.leakTextureDirs()) != 1 {
		t.Errorf("a running leak test has %d texture copies, want its own one", len(m.leakTextureDirs()))
	}
	if status, body := m.call(http.MethodPost, "/api/v1/build/runs/"+id+"/cancel", nil); status >= 400 {
		t.Fatalf("cancel: %d %v", status, body)
	}
	if manifest := m.waitForBuild(id); manifest["state"] != "cancelled" {
		t.Errorf("state %v", manifest["state"])
	}
	_ = os.WriteFile(gate, nil, 0o600)
	for len(m.leakTextureDirs()) != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if left := m.leakTextureDirs(); len(left) != 0 {
		t.Errorf("a cancelled build left its texture copy: %v", left)
	}
	m.backend.mu.Lock()
	returned := m.backend.leakResult
	m.backend.mu.Unlock()
	if returned != nil && returned["build_state"] == "succeeded" {
		t.Errorf("a cancelled build returned a successful result: %v", returned)
	}
}

// T11: Quake II and Quake III leak tests keep their own resources; nothing
// here fetches a Quake 1 WAD bundle for them.
func TestOnlyAQuake1LeakTestFetchesAWADBundle(t *testing.T) {
	m := newMachine(t)
	m.backend.savedAPMap("quake3")
	m.server.runner = &fixtureConverter{}
	m.signIn()
	link := m.receiveLeakLink(strings.Repeat("e", 32), "quake3")
	status, _ := m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline":        "auto-pigeon.q3.leak-test",
		"inputs":          map[string]string{"source_map": "aub:map/" + link.AssetID + "@" + m.backend.asset.revisionID + "#fixture.apmap"},
		"leak_request_id": link.RequestID,
	})
	if status == http.StatusAccepted {
		t.Fatal("a Quake III leak test started with no Q3Map2 set up")
	}
	if exports := m.textureExports(); exports != 0 || len(m.leakTextureDirs()) != 0 {
		t.Errorf("a Quake III leak request fetched %d WAD bundle(s), left %v", exports, m.leakTextureDirs())
	}
}

// T12: a map on this machine, built with a folder on this machine, is built
// exactly as before: no account, no bundle, the folder the person chose.
func TestALocalMapBuildStillUsesTheFolderThePersonChose(t *testing.T) {
	m := newMachine(t)
	m.approveAndBindTool()
	folder := filepath.Join(m.dir, "my-wads")
	writeFixtureFile(t, filepath.Join(folder, "mine.wad"), "WAD2")
	status, body := m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline": "aucom.fixture.pipeline",
		"inputs":   map[string]string{"source_map": m.writeSourceMap()},
		"roots":    map[string]string{"content_root": folder},
	})
	if status != http.StatusAccepted {
		t.Fatalf("%d %v", status, body)
	}
	manifest := m.waitForBuild(body["build"].(string))
	roots, _ := manifest["roots"].([]any)
	root, _ := roots[0].(map[string]any)
	source, _ := root["source"].(map[string]any)
	if manifest["state"] != "succeeded" || root["path"] != folder || source["kind"] != string(build.RootFromLocalDirectory) {
		t.Errorf("state %v, root %v", manifest["state"], root)
	}
	if m.textureExports() != 0 || len(m.leakTextureDirs()) != 0 {
		t.Error("a local build fetched or staged a bundle")
	}
}

// The review's command preview names the texture root the build will be
// given, from the same owner, and a bundle that cannot be had is said there —
// before Build. A preview copies nothing and runs nothing.
func TestTheLeakReviewPreviewShowsTheBundleTheBuildWillCompileWith(t *testing.T) {
	m, _ := newLeakMachine(t)
	wad := fixturewad.WAD("n357_floor", 13)
	m.saveRoom("new357p", 1, q1Room("new357.wad", false, "n357_floor"),
		leakBundle("new357p", 1, []string{"new357.wad"}, map[string][]byte{"new357.wad": wad}))
	link := m.receiveLeakLink(strings.Repeat("f", 32), "quake1")
	request := map[string]any{
		"pipeline":        "aucom.fixture.q1-leak",
		"inputs":          map[string]string{"source_map": "aub:map/new357p@" + m.backend.asset.revisionID + "#fixture.apmap"},
		"leak_request_id": link.RequestID,
	}
	jobs := len(m.jobIDs())
	status, preview := m.call(http.MethodPost, "/api/v1/build/preview", request)
	if status != http.StatusOK {
		t.Fatalf("preview: %d %v", status, preview)
	}
	bundle := rootBundle(t, preview)
	if bundle["map_id"] != "new357p" || bundle["revision"] != float64(1) {
		t.Errorf("the preview names the bundle %v", bundle)
	}
	cache, _ := m.server.textureCache()
	entry, found := cache.Lookup(texturebundle.Expect{MapID: "new357p", Revision: 1})
	if !found {
		t.Fatal("the preview did not leave the verified bundle in the cache")
	}
	args := strings.Join(compileArgs(preview), " ")
	if !strings.Contains(args, "-wadpath "+entry.ContentRoot) {
		t.Errorf("the previewed command does not read the bundle: %s", args)
	}
	if len(m.jobIDs()) != jobs || len(m.leakTextureDirs()) != 0 || m.server.pendingLeakRequest(link.RequestID) == nil {
		t.Error("a preview ran something, copied textures, or used up the request")
	}

	m.saveRoom("new357q", 1, q1Room("new357.wad", false, "n357_floor"),
		leakBundle("new357q", 1, []string{"new357.wad"}, map[string][]byte{"new357.wad": wad}))
	m.backend.mu.Lock()
	m.backend.textureRefusal = http.StatusForbidden
	m.backend.mu.Unlock()
	refused := m.receiveLeakLink(strings.Repeat("0", 32), "quake1")
	request["inputs"] = map[string]string{"source_map": "aub:map/new357q@" + m.backend.asset.revisionID + "#fixture.apmap"}
	request["leak_request_id"] = refused.RequestID
	if status, body := m.call(http.MethodPost, "/api/v1/build/preview", request); status < 400 ||
		!strings.Contains(fmt.Sprint(body["error"]), "could not be fetched") {
		t.Errorf("a preview whose textures are refused: %d %v", status, body)
	}
}
