package texturebundle_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle/fixturewad"
)

// NEW_313A. AUB now carries an installed WAD whose exact bytes its operator
// declared redistributable (manifest schema 1.2), with the notice files those
// bytes travel under. These tests are the consumer's half: the bytes are staged
// only after they hash to what the manifest declares, the notices are stored
// beside the bundle, and nothing a manifest merely SAYS is taken as proof.

// refusedAndNothingPublished asserts a bundle was refused, that the refusal
// says why, and that it left nothing behind — no cache entry a later run could
// reuse, and no half-written extraction.
func refusedAndNothingPublished(t *testing.T, f fixture, want string) {
	t.Helper()

	cache := newCache(t)
	_, err := publish(t, cache, f)
	if err == nil {
		t.Fatalf("the bundle was accepted; want a refusal mentioning %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal = %v, want it to mention %q", err, want)
	}
	if _, found := cache.Lookup(texturebundle.Expect{MapID: f.mapID, Revision: f.revision}); found {
		t.Error("a refused bundle is offered as a verified entry")
	}
	if _, statErr := os.Stat(filepath.Join(cache.Root(), f.mapID)); statErr == nil {
		t.Error("a refused bundle created a directory for its map")
	}
	left, _ := filepath.Glob(filepath.Join(cache.Root(), ".staging", "bundle-*"))
	if len(left) != 0 {
		t.Errorf("a refused bundle left %v behind", left)
	}
}

// --- the 1.2 bundle -----------------------------------------------------------

// An installed WAD the deployment declared redistributable arrives in the
// bundle, is verified like every other member, and is staged in the content
// root a compiler reads. Its notice is stored once, at its manifest path,
// beside LICENSES.md and outside that content root.
func TestADeclaredInstalledWADIsCarriedAndItsNoticesAreStored(t *testing.T) {
	f := declaredInstalledWAD()
	entry := mustPublish(t, newCache(t), f)

	if entry.Receipt.ManifestSchema != texturebundle.Schema {
		t.Errorf("schema = %q, want %q", entry.Receipt.ManifestSchema, texturebundle.Schema)
	}
	if !entry.CompilerReady() {
		t.Fatalf("a bundle carrying every declared WAD is not compiler-ready: %v", entry.Receipt.CompilerRefusals)
	}

	// The bytes, in the content root, exactly as the fixture made them.
	staged, err := os.ReadFile(filepath.Join(entry.ContentRoot, "second.wad"))
	if err != nil {
		t.Fatalf("the declared installed WAD was not staged: %v", err)
	}
	if !bytes.Equal(staged, fixturewad.WAD("fx_second", 32)) {
		t.Error("the staged WAD is not the bytes the bundle carried")
	}

	// The decision travels with the requirement.
	installed := entry.Manifest.OrderedWADs()[1]
	if installed.Origin != texturebundle.OriginInstalled || !installed.Included {
		t.Fatalf("second.wad = %+v", installed)
	}
	verdict := installed.Redistribution
	if verdict == nil || verdict.Decision != texturebundle.DecisionIncluded ||
		verdict.Credit != "The Auto-Pigeon test suite" {
		t.Fatalf("redistribution = %+v", verdict)
	}
	if uploaded := entry.Manifest.OrderedWADs()[0]; uploaded.Origin != texturebundle.OriginUser || uploaded.Revision != 3 {
		t.Errorf("first.wad = %+v, want its origin and revision carried through", uploaded)
	}

	// The notice: verified, recorded, and stored under its manifest path.
	if len(entry.Receipt.Notices) != 1 {
		t.Fatalf("notices recorded = %+v, want exactly one", entry.Receipt.Notices)
	}
	recorded := entry.Receipt.Notices[0]
	if recorded.Path != "NOTICES/CREDITS.txt" || recorded.SHA256 != digestOf(creditsNotice) ||
		len(recorded.Sources) != 1 || recorded.Sources[0] != "second.wad" {
		t.Errorf("notice record = %+v", recorded)
	}
	stored, err := os.ReadFile(filepath.Join(entry.Dir, "NOTICES", "CREDITS.txt"))
	if err != nil {
		t.Fatalf("the notice was not stored beside the bundle: %v", err)
	}
	if !bytes.Equal(stored, creditsNotice) {
		t.Error("the stored notice is not the bytes the bundle carried")
	}
	// Beside LICENSES.md, and not merged into it.
	licences, err := os.ReadFile(filepath.Join(entry.Dir, texturebundle.LicensesName))
	if err != nil {
		t.Fatal(err)
	}
	if string(licences) != "# Attribution\n" {
		t.Errorf("LICENSES.md was changed: %q", licences)
	}
	// Outside the content root: a notice is not a texture, and the files a run
	// installs are the content root's.
	if _, err := os.Stat(filepath.Join(entry.ContentRoot, "NOTICES")); err == nil {
		t.Error("the notices were extracted into the content root a compiler reads")
	}
	for _, file := range entry.Receipt.Files {
		if strings.HasPrefix(file.Path, "NOTICES/") {
			t.Errorf("the notice %s is recorded as a payload file", file.Path)
		}
	}
}

// Two carried sources under one notice: the file is in the archive once and is
// stored once.
func TestANoticeSharedByTwoSourcesIsStoredOnce(t *testing.T) {
	f := declaredInstalledWAD()
	third := fixturewad.WAD("fx_third", 48)
	f.wads = append(f.wads, "third.wad")
	f.files = append(f.files, fixtureFile{path: "third.wad", body: third, source: "third.wad"})
	verdict := *f.requirements[1].redistribution
	verdict.SHA256, verdict.Bytes = digestOf(third), int64(len(third))
	f.requirements = append(f.requirements, fixtureRequirement{
		name: "third.wad", status: "resolved", origin: "installed", included: true,
		paths: []string{"third.wad"}, redistribution: &verdict,
	})
	f.notices[0].sources = []string{"second.wad", "third.wad"}

	entry := mustPublish(t, newCache(t), f)
	if len(entry.Receipt.Notices) != 1 || len(entry.Receipt.Notices[0].Sources) != 2 {
		t.Fatalf("notices = %+v, want the one file with both sources", entry.Receipt.Notices)
	}
	stored, _ := filepath.Glob(filepath.Join(entry.Dir, "NOTICES", "*"))
	if len(stored) != 1 {
		t.Errorf("stored notices = %v, want one", stored)
	}
}

// A withheld installed source in a 1.2 manifest is named, not carried, and its
// reason reaches the caller. The bundle is still a bundle.
func TestAWithheldInstalledWADKeepsItsReason(t *testing.T) {
	f := declaredInstalledWAD()
	f.files = f.files[:1]
	f.notices = nil
	f.requirements[1] = fixtureRequirement{
		name: "second.wad", status: "resolved", origin: "installed", included: false,
		redistribution: &texturebundle.Redistribution{
			Decision: texturebundle.DecisionWithheld, Reason: texturebundle.ReasonDigestMismatch,
		},
	}
	f.compilerReady = false
	f.compilerRefusals = []string{"wad_bytes_not_carried: second.wad"}
	f.installedNotice = "This texture source is installed on the Auto-Pigeon deployment."

	entry := mustPublish(t, newCache(t), f)
	if entry.CompilerReady() {
		t.Fatal("a bundle without one of its WADs called itself compiler-ready")
	}
	withheld := entry.Manifest.OrderedWADs()[1]
	if withheld.Included || withheld.Redistribution == nil ||
		withheld.Redistribution.Reason != texturebundle.ReasonDigestMismatch {
		t.Errorf("second.wad = %+v", withheld)
	}
	if _, err := os.Stat(filepath.Join(entry.ContentRoot, "second.wad")); err == nil {
		t.Error("a withheld WAD is in the content root")
	}
}

// --- 1.1 still reads exactly as it did ------------------------------------------

func TestAnOlderManifestStillReadsAsItAlwaysDid(t *testing.T) {
	f := twoOrderedWADs()
	f.schema = texturebundle.SchemaV11
	f.files = f.files[:1]
	f.wads = []string{"first.wad", "quake101.wad"}
	f.requirements = []fixtureRequirement{
		{name: "first.wad", status: "resolved", included: true, paths: []string{"first.wad"}},
		// 1.1: an installed source is never carried, and nothing says why
		// beyond the note.
		{name: "quake101.wad", status: "installed", origin: "installed", included: false,
			note: "This texture source is installed on the Auto-Pigeon deployment."},
	}
	f.compilerReady = false
	f.compilerRefusals = []string{"wad_bytes_not_carried: quake101.wad"}

	cache := newCache(t)
	entry := mustPublish(t, cache, f)
	if entry.Receipt.ManifestSchema != texturebundle.SchemaV11 {
		t.Errorf("schema = %q", entry.Receipt.ManifestSchema)
	}
	if entry.CompilerReady() || len(entry.Receipt.CompilerRefusals) != 1 {
		t.Errorf("verdict = %v %v", entry.CompilerReady(), entry.Receipt.CompilerRefusals)
	}
	if len(entry.Receipt.Notices) != 0 {
		t.Errorf("a 1.1 bundle recorded notices: %+v", entry.Receipt.Notices)
	}
	if wad := entry.Manifest.OrderedWADs()[1]; wad.Redistribution != nil || wad.Included {
		t.Errorf("quake101.wad = %+v", wad)
	}
	// And it is reusable offline, like any other verified entry.
	if _, found := cache.Lookup(texturebundle.Expect{MapID: f.mapID, Revision: f.revision}); !found {
		t.Error("a verified 1.1 bundle is not reused")
	}

	// A complete 1.1 bundle is ready, as before.
	ready := twoOrderedWADs()
	ready.schema = texturebundle.SchemaV11
	if entry := mustPublish(t, newCache(t), ready); !entry.CompilerReady() {
		t.Error("a complete 1.1 bundle is not compiler-ready")
	}
}

// --- `included: true` is a claim, not the bytes -----------------------------------

// A forged manifest: it says the installed WAD is included, calls the bundle
// compiler-ready, and carries no such file. Nothing is published, so no run can
// reach a compiler on it.
func TestAnIncludedFlagWithNoFileIsRefused(t *testing.T) {
	f := declaredInstalledWAD()
	f.files = f.files[:1]
	f.requirements[1].paths = nil

	refusedAndNothingPublished(t, f, "says it is included and names no file")

	// The same forgery on an ordinary uploaded source, in a 1.1 manifest.
	plain := twoOrderedWADs()
	plain.schema = texturebundle.SchemaV11
	plain.files = plain.files[:1]
	plain.requirements[1].paths = nil
	refusedAndNothingPublished(t, plain, "says it is included and names no file")
}

// An installed source is carried only under a decision that says so, for the
// bytes it names.
func TestAnInstalledWADIsCarriedOnlyUnderItsDeclaration(t *testing.T) {
	t.Run("no decision at all", func(t *testing.T) {
		f := declaredInstalledWAD()
		f.requirements[1].redistribution = nil
		refusedAndNothingPublished(t, f, "carried with no redistribution decision")
	})
	t.Run("withheld, and included anyway", func(t *testing.T) {
		f := declaredInstalledWAD()
		f.requirements[1].redistribution = &texturebundle.Redistribution{
			Decision: texturebundle.DecisionWithheld, Reason: texturebundle.ReasonUndeclared,
		}
		refusedAndNothingPublished(t, f, "withheld by its redistribution decision")
	})
	t.Run("the decision is for other bytes", func(t *testing.T) {
		f := declaredInstalledWAD()
		verdict := *f.requirements[1].redistribution
		verdict.SHA256 = digestOf([]byte("some other file"))
		f.requirements[1].redistribution = &verdict
		refusedAndNothingPublished(t, f, "which is not a file it names")
	})
	t.Run("no credit", func(t *testing.T) {
		f := declaredInstalledWAD()
		verdict := *f.requirements[1].redistribution
		verdict.Credit = " "
		f.requirements[1].redistribution = &verdict
		refusedAndNothingPublished(t, f, "states no credit")
	})
	t.Run("a decision that is not one", func(t *testing.T) {
		f := declaredInstalledWAD()
		verdict := *f.requirements[1].redistribution
		verdict.Decision = "probably"
		f.requirements[1].redistribution = &verdict
		refusedAndNothingPublished(t, f, "which is not one")
	})
}

// The manifest's digest for the carried installed WAD and the bytes in the
// archive disagree.
func TestADeclaredInstalledWADWhoseBytesDoNotMatchIsRefused(t *testing.T) {
	f := declaredInstalledWAD()
	// The archive carries a different WAD under the declared name; the manifest
	// and the redistribution decision still name the original's digest.
	original := f.files[1].body
	f.files[1].body = fixturewad.WAD("fx_swapped", 99)
	f.files[1].digest = digestOf(original)
	size := int64(len(original))
	f.files[1].bytes = &size

	refusedAndNothingPublished(t, f, "second.wad hashes to")
}

// The manifest names the member and the archive does not carry it.
func TestADeclaredInstalledWADMissingFromTheArchiveIsRefused(t *testing.T) {
	f := declaredInstalledWAD()
	f.declareOnly = []fixtureFile{f.files[1]}
	f.files = f.files[:1]

	refusedAndNothingPublished(t, f, "second.wad, which the bundle does not carry")
}

// --- unsafe paths -------------------------------------------------------------------

func TestAnUnsafeWADPathIsRefused(t *testing.T) {
	for _, testCase := range []struct{ path, want string }{
		{"../second.wad", "path segment"},
		{"/second.wad", "absolute path"},
		{"wads/../../second.wad", "not a plain relative path"},
	} {
		t.Run(testCase.path, func(t *testing.T) {
			f := declaredInstalledWAD()
			f.files[1].path = testCase.path
			f.requirements[1].paths = []string{testCase.path}
			refusedAndNothingPublished(t, f, testCase.want)
		})
	}
}

// A notice path becomes a path on this machine. It is `NOTICES/<one plain
// name>` or it is refused — before the archive is consulted.
func TestANoticePathThatEscapesItsDirectoryIsRefused(t *testing.T) {
	for _, path := range []string{
		"NOTICES/../CREDITS.txt",
		"NOTICES/../../outside.txt",
		"/etc/CREDITS.txt",
		"CREDITS.txt",
		"NOTICES/sub/CREDITS.txt",
		"NOTICES/",
		"NOTICES/..",
		"notices/CREDITS.txt",
		`NOTICES/a\..\b.txt`,
	} {
		t.Run(path, func(t *testing.T) {
			// Declared in the manifest only: the refusal must not depend on
			// the archive's own name check getting there first.
			f := declaredInstalledWAD()
			f.notices[0].path = path
			f.notices[0].absent = true
			verdict := *f.requirements[1].redistribution
			verdict.NoticePaths = []string{path}
			f.requirements[1].redistribution = &verdict
			refusedAndNothingPublished(t, f, "is not NOTICES/<file name>")
		})
	}
	// And as an archive member, the escaping name is refused by name.
	f := declaredInstalledWAD()
	f.extra = []fixtureFile{{path: "NOTICES/../outside.txt", body: []byte("x")}}
	refusedAndNothingPublished(t, f, "not a plain relative path")
}

// --- the notices are verified like the bytes they travel with ---------------------

func TestANoticeWhoseBytesDoNotMatchIsRefused(t *testing.T) {
	f := declaredInstalledWAD()
	f.notices[0].digest = digestOf([]byte("the notice somebody meant to send"))

	refusedAndNothingPublished(t, f, "the notice NOTICES/CREDITS.txt hashes to")
}

func TestANoticeTheArchiveDoesNotCarryIsRefused(t *testing.T) {
	f := declaredInstalledWAD()
	f.notices[0].absent = true

	refusedAndNothingPublished(t, f, "the notice NOTICES/CREDITS.txt, which the bundle does not carry")
}

// A carried source requires a notice the manifest does not list: the bytes
// would arrive without the text they travel under.
func TestARequiredNoticeTheManifestDoesNotListIsRefused(t *testing.T) {
	f := declaredInstalledWAD()
	f.notices = nil

	refusedAndNothingPublished(t, f, `requires the notice "NOTICES/CREDITS.txt"`)
}

// A member under NOTICES/ that the manifest does not list is an undeclared
// member like any other.
func TestAnUnlistedNoticeMemberIsRefused(t *testing.T) {
	f := declaredInstalledWAD()
	f.notices = append(f.notices, fixtureNotice{
		path: "NOTICES/EXTRA.txt", body: []byte("nobody asked for this"), unlisted: true,
	})

	refusedAndNothingPublished(t, f, "NOTICES/EXTRA.txt, which its manifest does not declare")
}

func TestOneMemberMayNotBeBothAFileAndANotice(t *testing.T) {
	f := declaredInstalledWAD()
	f.declareOnly = []fixtureFile{{path: "NOTICES/CREDITS.txt", body: creditsNotice, source: "second.wad"}}

	refusedAndNothingPublished(t, f, "both as a file and as a notice")
}

// --- offline reuse re-checks the notices too ---------------------------------------

func TestACachedBundleWhoseNoticeChangedIsNotReused(t *testing.T) {
	for _, damage := range []struct {
		name string
		do   func(path string) error
	}{
		{"edited", func(path string) error { return os.WriteFile(path, []byte("public domain, trust me\n"), 0o600) }},
		{"removed", os.Remove},
	} {
		t.Run(damage.name, func(t *testing.T) {
			cache := newCache(t)
			f := declaredInstalledWAD()
			entry := mustPublish(t, cache, f)
			want := texturebundle.Expect{MapID: f.mapID, Revision: f.revision}
			if _, found := cache.Lookup(want); !found {
				t.Fatal("a verified bundle is not reused")
			}
			if err := damage.do(filepath.Join(entry.Dir, "NOTICES", "CREDITS.txt")); err != nil {
				t.Fatal(err)
			}
			if _, found := cache.Lookup(want); found {
				t.Error("a bundle whose notice changed after it was verified is still reused")
			}
		})
	}
}
