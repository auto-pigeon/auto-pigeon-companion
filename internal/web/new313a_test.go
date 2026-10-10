package web

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle/fixturewad"
)

// NEW_313A, end to end. AUB carries an installed WAD whose exact bytes its
// operator declared redistributable. A Build & Run then needs no folder from
// the person; the bytes are verified before a compiler sees them; the notices
// they travel under are kept; Technical details says which source supplied
// each WAD; and when a WAD really was not sent, the page says what is known —
// that no redistribution permission is on record for those exact bytes — and
// never whose the file is.

// exportWAD is one declared WAD in a fixture export.
type exportWAD struct {
	name   string
	origin string
	body   []byte
	// carried says whether the requirement names the member and says
	// `included: true`. A WAD that is not carried adds its refusal.
	carried bool
	// revision is an uploaded source's revision number.
	revision int
	// redistribution is the 1.2 decision, written as it is given.
	redistribution map[string]any
	// forge* build the manifests AUB never writes.
	forgeIncludedWithoutFile bool   // `included: true`, no file, no member
	forgeDigest              string // the manifest's digest for the member
	forgeOmitMember          bool   // declared, and not in the archive
	forgePath                string // the member's path, in manifest and archive
}

// exportNotice is one notice file in a fixture export.
type exportNotice struct {
	path        string
	body        []byte
	sources     []string
	forgeDigest string
}

// exportSpec is a map texture export, built the way AUB builds one unless a
// forge field says otherwise.
type exportSpec struct {
	schema   string
	mapID    string
	revision int
	wads     []exportWAD
	notices  []exportNotice
	// forgeReady calls the bundle compiler-ready whatever it carries.
	forgeReady bool
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)

	return hex.EncodeToString(sum[:])
}

func (spec exportSpec) build(t testing.TB) []byte {
	t.Helper()

	buffer := &bytes.Buffer{}
	writer := zip.NewWriter(buffer)
	add := func(name string, body []byte) {
		out, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = out.Write(body); err != nil {
			t.Fatal(err)
		}
	}

	files := []map[string]any{}
	requirements := []map[string]any{}
	declared := []string{}
	refusals := []string{}
	for order, wad := range spec.wads {
		declared = append(declared, wad.name)
		requirement := map[string]any{
			"order": order, "name": wad.name, "game": "quake1", "kind": "wad",
			"origin": wad.origin, "status": "resolved", "included": wad.carried,
			"files": []map[string]any{},
		}
		if wad.revision > 0 {
			requirement["revision"] = wad.revision
		}
		if wad.redistribution != nil {
			requirement["redistribution"] = wad.redistribution
		}
		switch {
		case wad.forgeIncludedWithoutFile:
			requirement["included"] = true
		case wad.carried:
			path := wad.name
			if wad.forgePath != "" {
				path = wad.forgePath
			}
			digest := sha256Hex(wad.body)
			if wad.forgeDigest != "" {
				digest = wad.forgeDigest
			}
			member := map[string]any{"path": path, "source": wad.name, "sha256": digest, "bytes": len(wad.body)}
			requirement["files"] = []map[string]any{member}
			requirement["source_sha256"] = digest
			files = append(files, member)
			if !wad.forgeOmitMember {
				add(path, wad.body)
			}
		default:
			refusals = append(refusals, "wad_bytes_not_carried: "+wad.name)
		}
		requirements = append(requirements, requirement)
	}
	notices := []map[string]any{}
	for _, notice := range spec.notices {
		digest := sha256Hex(notice.body)
		if notice.forgeDigest != "" {
			digest = notice.forgeDigest
		}
		notices = append(notices, map[string]any{
			"path": notice.path, "sha256": digest, "bytes": len(notice.body), "sources": notice.sources,
		})
		add(notice.path, notice.body)
	}
	if spec.forgeReady {
		refusals = []string{}
	}
	schema := spec.schema
	if schema == "" {
		schema = texturebundle.Schema
	}
	manifest := map[string]any{
		"schema_version": schema,
		"map_id":         spec.mapID, "map_name": "First Coast", "revision": spec.revision, "game": "quake1",
		"exported_at":   "2026-10-10T00:00:00Z",
		"wads_declared": declared, "requirements": requirements, "files": files,
		"compiler_ready": len(refusals) == 0, "compiler_refusals": refusals,
	}
	if schema == texturebundle.Schema {
		manifest["notices"] = notices
	}
	add(texturebundle.LicensesName, []byte("# Attribution\n"))
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	add(texturebundle.ManifestName, encoded)
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}

	return buffer.Bytes()
}

// The fixture's WADs and notice. fixturewad makes the WAD bytes — a 16×16
// single-colour miptex each — and the notice was written for this test.
var (
	fixtureFirstWAD  = fixturewad.WAD("fx_first", 16)
	fixtureSecondWAD = fixturewad.WAD("fx_second", 32)
	fixtureCredits   = []byte("Fixture textures by the Auto-Pigeon test suite.\nFree to redistribute with this notice.\n")
)

const fixtureCredit = "The Auto-Pigeon test suite"

// declaredRedistribution is the decision AUB writes for a carried installed
// source.
func declaredRedistribution(body []byte) map[string]any {
	return map[string]any{
		"decision": "included", "reason": "declared",
		"sha256": sha256Hex(body), "bytes": len(body),
		"source": "Fixture texture set", "credit": fixtureCredit,
		"terms":          "Free to redistribute with this notice.",
		"primary_notice": "CREDITS.txt in the fixture set", "notice_version": "2026-10-10",
		"notice_paths": []string{"NOTICES/CREDITS.txt"},
	}
}

// declaredExport is `first.wad` uploaded by a user and `second.wad` installed
// on the deployment and carried under its operator's declaration.
func declaredExport(m *machine) exportSpec {
	return exportSpec{
		mapID: m.backend.asset.assetID, revision: m.backend.asset.revision,
		wads: []exportWAD{
			{name: "first.wad", origin: "user", body: fixtureFirstWAD, carried: true, revision: 3},
			{name: "second.wad", origin: "installed", body: fixtureSecondWAD, carried: true,
				redistribution: declaredRedistribution(fixtureSecondWAD)},
		},
		notices: []exportNotice{
			{path: "NOTICES/CREDITS.txt", body: fixtureCredits, sources: []string{"second.wad"}},
		},
	}
}

// withheldExport is the same map with `second.wad` installed and NOT sent.
// reason is AUB's code; empty writes the 1.1 manifest, which gives none.
func withheldExport(m *machine, reason string) exportSpec {
	spec := exportSpec{
		mapID: m.backend.asset.assetID, revision: m.backend.asset.revision,
		wads: []exportWAD{
			{name: "first.wad", origin: "user", body: fixtureFirstWAD, carried: true, revision: 3},
			{name: "second.wad", origin: "installed"},
		},
	}
	if reason == "" {
		spec.schema = texturebundle.SchemaV11
	} else {
		spec.wads[1].redistribution = map[string]any{"decision": "withheld", "reason": reason}
	}

	return spec
}

func wadSource(t *testing.T, run map[string]any, name string) map[string]any {
	t.Helper()
	rows, _ := run["wad_sources"].([]any)
	for _, row := range rows {
		if source, _ := row.(map[string]any); source["name"] == name {
			return source
		}
	}
	t.Fatalf("the run does not say which source supplied %s: %v", name, run["wad_sources"])

	return nil
}

// --- the declared WAD is staged, and nobody is asked for a folder ----------------

func TestADeclaredInstalledWADBuildsWithoutAFolderOfYourOwn(t *testing.T) {
	m := newMachine(t)
	m.preparePlay(t)
	m.backend.textures = declaredExport(m).build(t)

	// The review: ready, and no "use your own copy" offer.
	status, textures := m.call(http.MethodGet,
		"/api/v1/play/textures?asset_id="+m.backend.asset.assetID+"&revision=4", nil)
	if status != http.StatusOK {
		t.Fatalf("textures = %d: %v", status, textures["error"])
	}
	if textures["compiler_ready"] != true {
		t.Fatalf("compiler_ready = %v, refusals %v", textures["compiler_ready"], textures["compiler_refusals"])
	}
	if _, offered := textures["own_wads_possible"]; offered {
		t.Error("the review offers a folder of your own for a bundle that carries every WAD")
	}
	if listed, _ := textures["notices"].([]any); len(listed) != 1 {
		t.Errorf("the review lists notices %v, want the one the bundle carried", textures["notices"])
	}

	// The run: no own_wads_dir in the request at all.
	body := playBody(m)
	if _, named := body["own_wads_dir"]; named {
		t.Fatal("the fixture request names a folder; this test is about not needing one")
	}
	status, started := m.call(http.MethodPost, "/api/v1/play/runs", body)
	if status != http.StatusAccepted {
		t.Fatalf("starting = %d: %v", status, started["error"])
	}
	run := m.awaitPlay(t, started["id"].(string))
	if run["state"] != "succeeded" {
		t.Fatalf("the run %v at %v: %v", run["state"], run["failed_at"], run["error"])
	}

	// The compiler was given the declared installed WAD's verified bytes: they
	// are what was staged beside the level.
	staged, err := os.ReadFile(filepath.Join(m.gameRoot, "auto-pigeon", engine.WADDir, "second.wad"))
	if err != nil {
		t.Fatalf("second.wad was not staged: %v", err)
	}
	if !bytes.Equal(staged, fixtureSecondWAD) {
		t.Error("the staged second.wad is not the bytes the bundle carried")
	}

	// Technical details: which source supplied each WAD.
	second := wadSource(t, run, "second.wad")
	if second["origin"] != "installed" || second["staged"] != true ||
		second["sha256"] != sha256Hex(fixtureSecondWAD) || second["credit"] != fixtureCredit {
		t.Errorf("second.wad's source = %v", second)
	}
	first := wadSource(t, run, "first.wad")
	if first["origin"] != "user" || first["revision"] != float64(3) ||
		first["sha256"] != sha256Hex(fixtureFirstWAD) {
		t.Errorf("first.wad's source = %v", first)
	}
	if _, credited := first["credit"]; credited {
		t.Errorf("an uploaded source was given a credit it never declared: %v", first)
	}

	// The notice, stored beside the bundle at its manifest path, as it arrived,
	// and not folded into LICENSES.md.
	dir, _ := run["notices_dir"].(string)
	if dir == "" {
		t.Fatalf("the run does not say where the notices are: %v", run["notices"])
	}
	stored, err := os.ReadFile(filepath.Join(dir, "NOTICES", "CREDITS.txt"))
	if err != nil {
		t.Fatalf("the notice was not stored: %v", err)
	}
	if !bytes.Equal(stored, fixtureCredits) {
		t.Error("the stored notice is not the bytes the bundle carried")
	}
	licences, err := os.ReadFile(filepath.Join(dir, texturebundle.LicensesName))
	if err != nil || string(licences) != "# Attribution\n" {
		t.Errorf("LICENSES.md beside the notices = %q, %v", licences, err)
	}
	found := 0
	_ = filepath.WalkDir(m.dir, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && entry.Name() == "CREDITS.txt" {
			found++
		}

		return nil
	})
	if found != 1 {
		t.Errorf("the notice is stored %d times, want once", found)
	}
}

// --- a 1.1 export still builds --------------------------------------------------

func TestAnOlderExportStillBuilds(t *testing.T) {
	m := newMachine(t)
	m.preparePlay(t)
	m.backend.textures = exportSpec{
		schema: texturebundle.SchemaV11,
		mapID:  m.backend.asset.assetID, revision: m.backend.asset.revision,
		wads: []exportWAD{
			{name: "first.wad", body: fixtureFirstWAD, carried: true},
			{name: "second.wad", body: fixtureSecondWAD, carried: true},
		},
	}.build(t)

	status, started := m.call(http.MethodPost, "/api/v1/play/runs", playBody(m))
	if status != http.StatusAccepted {
		t.Fatalf("starting = %d: %v", status, started["error"])
	}
	run := m.awaitPlay(t, started["id"].(string))
	if run["state"] != "succeeded" {
		t.Fatalf("the run %v at %v: %v", run["state"], run["failed_at"], run["error"])
	}
	for _, name := range []string{"first.wad", "second.wad"} {
		source := wadSource(t, run, name)
		if source["staged"] != true || source["sha256"] == nil {
			t.Errorf("%s = %v", name, source)
		}
	}
	if run["notices"] != nil {
		t.Errorf("a 1.1 bundle produced notices: %v", run["notices"])
	}
}

// --- nothing unverified reaches a compiler ---------------------------------------

// Each of these is a bundle AUB would not write and a hostile or damaged one
// could be. The run stops in the texture stage: the extractor is not started,
// no build exists, nothing is installed.
func TestAForgedOrDamagedExportStopsBeforeTheCompiler(t *testing.T) {
	cases := []struct {
		name  string
		forge func(spec *exportSpec)
		want  string
	}{
		{"an included flag with no file behind it", func(spec *exportSpec) {
			spec.wads[1].forgeIncludedWithoutFile = true
			spec.forgeReady = true
		}, "says it is included and names no file"},
		{"the bytes are not the declared digest", func(spec *exportSpec) {
			spec.wads[1].forgeDigest = sha256Hex(fixtureSecondWAD)
			spec.wads[1].body = fixturewad.WAD("fx_swapped", 99)
		}, "second.wad hashes to"},
		{"the archive does not carry the member", func(spec *exportSpec) {
			spec.wads[1].forgeOmitMember = true
		}, "which the bundle does not carry"},
		{"a WAD path that climbs out", func(spec *exportSpec) {
			spec.wads[1].forgePath = "../second.wad"
		}, "path segment"},
		{"an absolute WAD path", func(spec *exportSpec) {
			spec.wads[1].forgePath = "/second.wad"
		}, "absolute path"},
		{"a notice path that climbs out of NOTICES", func(spec *exportSpec) {
			spec.notices[0].path = "NOTICES/../CREDITS.txt"
		}, "not a plain relative path"},
		{"a notice outside NOTICES", func(spec *exportSpec) {
			spec.notices[0].path = "CREDITS.txt"
			spec.wads[1].redistribution["notice_paths"] = []string{"CREDITS.txt"}
		}, "is not NOTICES/<file name>"},
		{"a notice whose bytes are not its digest", func(spec *exportSpec) {
			spec.notices[0].forgeDigest = sha256Hex([]byte("another notice"))
		}, "the notice NOTICES/CREDITS.txt hashes to"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			m := newMachine(t)
			m.preparePlay(t)
			spec := declaredExport(m)
			testCase.forge(&spec)
			m.backend.textures = spec.build(t)

			status, started := m.call(http.MethodPost, "/api/v1/play/runs", playBody(m))
			if status != http.StatusAccepted {
				t.Fatalf("starting = %d: %v", status, started["error"])
			}
			run := m.awaitPlay(t, started["id"].(string))

			if run["state"] != "failed" || run["failed_at"] != "downloading_textures" {
				t.Fatalf("state = %v at %v: %v", run["state"], run["failed_at"], run["error"])
			}
			if message, _ := run["error"].(string); !strings.Contains(message, testCase.want) {
				t.Errorf("error = %q, want it to mention %q", message, testCase.want)
			}
			for _, row := range run["stages"].([]any) {
				stage, _ := row.(map[string]any)
				if stage["state"] == "converting" || stage["state"] == "compiling" {
					t.Errorf("the run reached %v on a bundle that did not verify", stage["state"])
				}
			}
			if run["build_id"] != nil {
				t.Error("a build was started on a bundle that did not verify")
			}
			if _, err := os.Stat(filepath.Join(m.gameRoot, "auto-pigeon")); err == nil {
				t.Error("a refused run created the mod directory")
			}
			// And nothing of it is kept as a verified entry for a later run.
			cache, err := m.server.textureCache()
			if err != nil {
				t.Fatal(err)
			}
			if _, found := cache.Lookup(texturebundle.Expect{
				MapID: m.backend.asset.assetID, Revision: m.backend.asset.revision,
			}); found {
				t.Error("a bundle that did not verify is cached as verified")
			}
		})
	}
}

// --- a WAD that was not sent ------------------------------------------------------

// retiredOwnershipClaims are the things this program used to say about a WAD
// that was not sent. None of them was ever known: what is known is that the
// deployment has no redistribution permission on record for those exact bytes.
var retiredOwnershipClaims = []string{
	"somebody else's game",
	"someone else's game",
	"part of the game you own",
	"game you own",
	"may not hand out",
	"cannot redistribute",
	"id Software",
	"usually your game's id1",
	"gioco di qualcun altro",
	"gioco che possiedi",
}

func saysNothingAboutWhoseItIs(t *testing.T, what, text string) {
	t.Helper()
	for _, claim := range retiredOwnershipClaims {
		if strings.Contains(strings.ToLower(text), strings.ToLower(claim)) {
			t.Errorf("%s says %q: %q", what, claim, text)
		}
	}
}

func TestAWADThatWasNotSentIsExplainedByWhatIsKnownAndYourOwnCopyStillWorks(t *testing.T) {
	for _, testCase := range []struct {
		name, reason, want string
	}{
		{"1.2, undeclared", "undeclared", "no redistribution permission on record for that file's exact bytes"},
		{"1.2, digest mismatch", "digest_mismatch", "not the exact file its redistribution permission names"},
		{"1.2, declared withheld", "declared_withheld", "declared that its bytes are not to be redistributed"},
		{"1.2, a reason this build does not know", "something_new", "no redistribution permission on record for that file's exact bytes"},
		{"1.1, no reason given", "", "no redistribution permission on record for that file's exact bytes"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			m := newMachine(t)
			m.preparePlay(t)
			m.backend.textures = withheldExport(m, testCase.reason).build(t)

			// The review offers the local recovery, and carries the reason.
			status, textures := m.call(http.MethodGet,
				"/api/v1/play/textures?asset_id="+m.backend.asset.assetID+"&revision=4", nil)
			if status != http.StatusOK {
				t.Fatalf("textures = %d: %v", status, textures["error"])
			}
			if textures["compiler_ready"] != false || textures["own_wads_possible"] != true {
				t.Fatalf("review = ready %v, own copy possible %v",
					textures["compiler_ready"], textures["own_wads_possible"])
			}
			if needed := stringsOf(textures["own_wads_needed"]); len(needed) != 1 || needed[0] != "second.wad" {
				t.Errorf("own_wads_needed = %v", needed)
			}
			wads, _ := textures["wads"].([]any)
			withheld, _ := wads[1].(map[string]any)
			verdict, _ := withheld["redistribution"].(map[string]any)
			if testCase.reason == "" {
				if verdict != nil {
					t.Errorf("a 1.1 manifest produced a redistribution decision: %v", verdict)
				}
			} else if verdict["reason"] != testCase.reason {
				t.Errorf("the review's reason = %v, want %q", verdict["reason"], testCase.reason)
			}

			// Without a folder: stopped before the compiler, in neutral words.
			status, started := m.call(http.MethodPost, "/api/v1/play/runs", playBody(m))
			if status != http.StatusAccepted {
				t.Fatalf("starting = %d: %v", status, started["error"])
			}
			run := m.awaitPlay(t, started["id"].(string))
			if run["state"] != "failed" || run["failed_at"] != "downloading_textures" || run["build_id"] != nil {
				t.Fatalf("state = %v at %v, build %v", run["state"], run["failed_at"], run["build_id"])
			}
			remedy, _ := run["remedy"].(string)
			if !strings.Contains(remedy, "second.wad was not sent") || !strings.Contains(remedy, testCase.want) ||
				!strings.Contains(remedy, "your own copy") {
				t.Errorf("remedy = %q", remedy)
			}
			saysNothingAboutWhoseItIs(t, "the remedy", remedy)
			encoded, _ := json.Marshal(map[string]any{"run": run, "textures": textures})
			saysNothingAboutWhoseItIs(t, "the page's data", string(encoded))

			// With a folder the person names: the run completes, and the record
			// says the bytes were theirs and why the deployment sent none.
			own := t.TempDir()
			mine := fixturewad.WAD("fx_mine", 64)
			if err := os.WriteFile(filepath.Join(own, "second.wad"), mine, 0o600); err != nil {
				t.Fatal(err)
			}
			body := playBody(m)
			body["own_wads_dir"] = own
			status, started = m.call(http.MethodPost, "/api/v1/play/runs", body)
			if status != http.StatusAccepted {
				t.Fatalf("starting with a folder = %d: %v", status, started["error"])
			}
			run = m.awaitPlay(t, started["id"].(string))
			if run["state"] != "succeeded" {
				t.Fatalf("with your own copy the run %v at %v: %v", run["state"], run["failed_at"], run["error"])
			}
			second := wadSource(t, run, "second.wad")
			if second["origin"] != "own_copy" || second["staged"] != true || second["sha256"] != sha256Hex(mine) {
				t.Errorf("second.wad's source = %v", second)
			}
			if testCase.reason != "" && second["not_sent_reason"] != testCase.reason {
				t.Errorf("the record lost why the deployment sent none: %v", second)
			}
			if _, credited := second["credit"]; credited {
				t.Errorf("your own copy was given a credit: %v", second)
			}
		})
	}
}

// --- nothing in the page, the locales or the manual says whose a WAD is ------------

// The texts a person reads about a WAD that was not sent: the Build & Run page
// and its translations, the Go that writes a run's remedy and details, and the
// manual. Comments are included on purpose — a comment that says it is how the
// next string comes to say it.
func TestNothingSaysWhoseAWADThatWasNotSentIs(t *testing.T) {
	root := filepath.Join("..", "..")
	var files []string
	for _, pattern := range []string{
		"internal/web/assets/*.js", "internal/web/assets/*.html", "internal/web/assets/locales/*.js",
		"internal/web/testdata/playjourney.js",
		"internal/playrun/*.go", "internal/texturebundle/*.go", "internal/web/play*.go",
		"internal/build/roots.go",
	} {
		matched, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil || len(matched) == 0 {
			t.Fatalf("%s matches nothing (%v); this test would then be checking nothing", pattern, err)
		}
		files = append(files, matched...)
	}
	checked := 0
	for _, file := range files {
		// This file holds the retired phrases in order to look for them.
		if filepath.Base(file) == "new313a_test.go" {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for number, line := range strings.Split(string(raw), "\n") {
			lower := strings.ToLower(line)
			// A WAD line, or one of the two sentences that never named the WAD.
			aboutWADs := strings.Contains(lower, "wad") || strings.Contains(lower, "hand out") ||
				strings.Contains(lower, "your own copy") || strings.Contains(lower, "la tua copia")
			if !aboutWADs {
				continue
			}
			for _, claim := range retiredOwnershipClaims {
				if strings.Contains(lower, strings.ToLower(claim)) {
					t.Errorf("%s:%d still says %q: %s", file, number+1, claim, strings.TrimSpace(line))
				}
			}
		}
	}
	if checked < 20 {
		t.Fatalf("only %d files were read; the patterns no longer cover the page", checked)
	}
}
