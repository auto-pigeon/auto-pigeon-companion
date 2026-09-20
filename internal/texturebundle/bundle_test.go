package texturebundle_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/texturebundle"
)

func publish(t testing.TB, cache *texturebundle.Cache, f fixture) (texturebundle.Entry, error) {
	t.Helper()

	return cache.Publish(context.Background(), f.build(t),
		texturebundle.Expect{MapID: f.mapID, Revision: f.revision})
}

func mustPublish(t testing.TB, cache *texturebundle.Cache, f fixture) texturebundle.Entry {
	t.Helper()

	entry, err := publish(t, cache, f)
	if err != nil {
		t.Fatalf("publishing the bundle: %v", err)
	}

	return entry
}

// --- the declaration is the map's content -----------------------------------

// Two declared WADs, and the order is the map's own. Nothing sorts it, nothing
// merges them, and the LATER declaration is still later after a round trip
// through this package — which is what Quake 1 collision precedence is.
func TestTheDeclarationOrderSurvives(t *testing.T) {
	entry := mustPublish(t, newCache(t), twoOrderedWADs())

	wads := entry.Manifest.OrderedWADs()
	if len(wads) != 2 {
		t.Fatalf("ordered WADs = %d, want 2", len(wads))
	}
	if wads[0].Name != "first.wad" || wads[1].Name != "second.wad" {
		t.Errorf("order = %q, %q; want first.wad then second.wad", wads[0].Name, wads[1].Name)
	}
	if got := entry.Receipt.WADsDeclared; len(got) != 2 || got[0] != "first.wad" || got[1] != "second.wad" {
		t.Errorf("receipt declaration = %v", got)
	}
	// Both files are on disk, separately. A collision is not resolved here and
	// the WADs are never merged: the compiler reads both and applies its own
	// precedence to the order it was handed.
	for _, name := range []string{"first.wad", "second.wad"} {
		if _, err := os.Stat(filepath.Join(entry.ContentRoot, name)); err != nil {
			t.Errorf("%s is not in the content root: %v", name, err)
		}
	}
}

// One member, two declarations. A map may declare two WAD names that resolve to
// byte-identical bytes; AUB writes the member once and names it from both, and
// that is legitimate rather than a duplicate.
func TestTheSameBytesMayBeDeclaredUnderTwoNames(t *testing.T) {
	f := twoOrderedWADs()
	f.requirements = []fixtureRequirement{
		{name: "first.wad", status: "resolved", included: true, paths: []string{"first.wad"}},
		{name: "alias.wad", status: "resolved", included: true, paths: []string{"first.wad"}},
	}
	f.wads = []string{"first.wad", "alias.wad"}
	f.files = f.files[:1]

	entry := mustPublish(t, newCache(t), f)
	if got := len(entry.Receipt.Files); got != 1 {
		t.Errorf("verified files = %d, want the one member both declarations resolve to", got)
	}
	wads := entry.Manifest.OrderedWADs()
	if len(wads) != 2 || wads[0].Files[0].SHA256 != wads[1].Files[0].SHA256 {
		t.Errorf("two declarations of one member did not both resolve to it: %+v", wads)
	}
}

// The map's own generated texture-alias WAD. It is an ordinary carried member
// with `origin: embedded`, and this package neither generates nor special-cases
// it — it is verified against the manifest like every other file.
func TestAnEmbeddedAliasWADIsCarriedLikeAnyOther(t *testing.T) {
	f := twoOrderedWADs()
	alias := []byte("WAD2" + "alias-table")
	f.wads = append(f.wads, "dm_1_aliases.wad")
	f.files = append(f.files, fixtureFile{
		path: "dm_1_aliases.wad", body: alias, source: "dm_1_aliases.wad",
	})
	f.requirements = append(f.requirements, fixtureRequirement{
		name: "dm_1_aliases.wad", status: "resolved", origin: "embedded",
		included: true, paths: []string{"dm_1_aliases.wad"},
	})

	entry := mustPublish(t, newCache(t), f)
	wads := entry.Manifest.OrderedWADs()
	last := wads[len(wads)-1]
	if last.Origin != "embedded" || !last.Included {
		t.Errorf("the alias WAD = %+v", last)
	}
	if digest := digestOf(alias); last.Files[0].SHA256 != digest {
		t.Errorf("alias digest = %q, want %q", last.Files[0].SHA256, digest)
	}
}

// --- compiler readiness is AUB's verdict, passed through --------------------

// A source the deployment has installed, or that belongs to somebody else, or
// that nobody could read, is named and not carried. The bundle is still a
// bundle; it just is not compiler-ready, and the refusals say which is which.
func TestAnIncompleteBundleIsPublishedAndIsNotCompilerReady(t *testing.T) {
	f := twoOrderedWADs()
	f.files = f.files[:1]
	f.requirements = []fixtureRequirement{
		{name: "first.wad", status: "resolved", included: true, paths: []string{"first.wad"}},
		{name: "quake101.wad", status: "installed", included: false,
			note: "This texture source is installed on the Auto-Pigeon deployment."},
		{name: "andrea_private.wad", status: "private", included: false},
		{name: "nowhere.wad", status: "missing", included: false},
	}
	f.wads = []string{"first.wad", "quake101.wad", "andrea_private.wad", "nowhere.wad"}
	f.compilerReady = false
	f.compilerRefusals = []string{
		"wad_bytes_not_carried: quake101.wad",
		"texture_source_private: andrea_private.wad",
		"texture_source_missing: nowhere.wad",
	}

	entry := mustPublish(t, newCache(t), f)
	if entry.CompilerReady() {
		t.Fatal("a bundle missing three of its four declared WADs called itself compiler-ready")
	}
	if len(entry.Receipt.CompilerRefusals) != 3 {
		t.Errorf("refusals = %v, want all three carried through verbatim", entry.Receipt.CompilerRefusals)
	}
	// Every refusal AUB named survives to the caller. Losing one would mean a
	// page that says "not ready" and cannot say why.
	for _, want := range f.compilerRefusals {
		if !contains(entry.Receipt.CompilerRefusals, want) {
			t.Errorf("refusal %q was dropped", want)
		}
	}
}

// A manifest that claims both is a manifest nobody can act on.
func TestABundleMayNotBeReadyAndRefusedAtOnce(t *testing.T) {
	f := twoOrderedWADs()
	f.compilerReady = true
	f.compilerRefusals = []string{"wad_bytes_not_carried: second.wad"}

	if _, err := publish(t, newCache(t), f); err == nil {
		t.Fatal("a manifest that is ready and refused at once was accepted")
	}
}

// --- identity ----------------------------------------------------------------

// A current-revision export must never be paired with a historical map
// revision. The check is here as well as on the AUB route, because the two are
// separate requests and only one of them is on this machine.
func TestARevisionMismatchIsRefused(t *testing.T) {
	cache := newCache(t)
	f := twoOrderedWADs()
	bundle := f.build(t)

	_, err := cache.Publish(context.Background(), bundle,
		texturebundle.Expect{MapID: f.mapID, Revision: f.revision + 1})
	if !errors.Is(err, texturebundle.ErrRevisionMismatch) {
		t.Fatalf("publishing revision %d as revision %d = %v, want a revision mismatch",
			f.revision, f.revision+1, err)
	}
	// No directory named after a revision this bundle is not, and nothing left
	// in staging either.
	if _, err := os.Stat(filepath.Join(cache.Root(), f.mapID)); err == nil {
		t.Error("a refused bundle created a revision directory")
	}
	left, _ := filepath.Glob(filepath.Join(cache.Root(), "*", "bundle-*"))
	if len(left) != 0 {
		t.Errorf("a refused bundle left %v behind", left)
	}
}

func TestAMapMismatchIsRefused(t *testing.T) {
	cache := newCache(t)
	f := twoOrderedWADs()

	_, err := cache.Publish(context.Background(), f.build(t),
		texturebundle.Expect{MapID: "map0000000002", Revision: f.revision})
	if !errors.Is(err, texturebundle.ErrRevisionMismatch) {
		t.Fatalf("publishing another map's bundle = %v, want a mismatch", err)
	}
}

func TestAnUnknownSchemaIsRefused(t *testing.T) {
	f := twoOrderedWADs()
	f.schema = "aub-map-texture-export/9.9"

	if _, err := publish(t, newCache(t), f); err == nil ||
		!strings.Contains(err.Error(), "aub-map-texture-export/9.9") {
		t.Fatalf("an unknown schema = %v, want a refusal naming it", err)
	}
}

// --- the bytes must be the bytes ---------------------------------------------

func TestADigestMismatchIsRefused(t *testing.T) {
	f := twoOrderedWADs()
	f.files[1].digest = digestOf([]byte("something else entirely"))

	err := publishError(t, f)
	if err == nil || !strings.Contains(err.Error(), "second.wad") {
		t.Fatalf("a digest mismatch = %v, want a refusal naming the file", err)
	}
}

func TestASizeMismatchIsRefused(t *testing.T) {
	f := twoOrderedWADs()
	wrong := int64(999999)
	f.files[0].bytes = &wrong

	err := publishError(t, f)
	if err == nil || !strings.Contains(err.Error(), "first.wad") {
		t.Fatalf("a size mismatch = %v, want a refusal naming the file", err)
	}
}

func TestAnUndeclaredMemberIsRefused(t *testing.T) {
	f := twoOrderedWADs()
	f.extra = []fixtureFile{{path: "surprise.exe", body: []byte("MZ")}}

	err := publishError(t, f)
	if err == nil || !strings.Contains(err.Error(), "surprise.exe") {
		t.Fatalf("an undeclared member = %v, want a refusal naming it", err)
	}
}

// A manifest that names a member the archive does not carry is refused. The
// complementary rule to the undeclared-member one: the two together mean the
// archive and its manifest describe exactly the same set of files.
func TestADeclaredMemberThatIsAbsentIsRefused(t *testing.T) {
	f := twoOrderedWADs()
	// The manifest still declares both WADs; only the first is written.
	absent := f
	absent.files = []fixtureFile{f.files[0]}
	absent.declareOnly = []fixtureFile{f.files[1]}

	err := publishError(t, absent)
	if err == nil || !strings.Contains(err.Error(), "second.wad") {
		t.Fatalf("a manifest naming an absent member = %v, want a refusal naming it", err)
	}
}

func TestAManifestIsRequired(t *testing.T) {
	f := twoOrderedWADs()
	f.omitManifest = true

	err := publishError(t, f)
	if err == nil || !strings.Contains(err.Error(), texturebundle.ManifestName) {
		t.Fatalf("a bundle with no manifest = %v", err)
	}
}

// --- hostile archives ---------------------------------------------------------

func TestHostileMemberNamesAreRefused(t *testing.T) {
	cases := []struct {
		name   string
		member string
		want   string
	}{
		{"traversal", "../../id1/pak0.pak", "path segment"},
		{"nested traversal", "wads/../../escape.wad", "not a plain relative path"},
		{"absolute", "/etc/passwd", "absolute path"},
		{"drive", `C:/Windows/system32/x.dll`, "drive"},
		{"backslash", `wads\..\..\escape.wad`, "backslash"},
		{"directory entry", "wads/", "directory entry"},
		{"dot", "./first.wad", "not a plain relative path"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			f := twoOrderedWADs()
			body := []byte("payload")
			if strings.HasSuffix(testCase.member, "/") {
				body = nil
			}
			f.extra = []fixtureFile{{path: testCase.member, body: body}}

			err := publishError(t, f)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("%s = %v, want a refusal mentioning %q", testCase.member, err, testCase.want)
			}
		})
	}
}

func TestASymlinkMemberIsRefused(t *testing.T) {
	f := twoOrderedWADs()
	f.extra = []fixtureFile{{
		path: "link.wad", body: []byte("/etc/passwd"), mode: fs.ModeSymlink | 0o777,
	}}

	err := publishError(t, f)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("a symlink member = %v", err)
	}
}

func TestADeviceMemberIsRefused(t *testing.T) {
	f := twoOrderedWADs()
	f.extra = []fixtureFile{{path: "dev.wad", body: nil, mode: fs.ModeDevice | 0o666}}

	err := publishError(t, f)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("a device member = %v", err)
	}
}

func TestADuplicateMemberIsRefused(t *testing.T) {
	f := twoOrderedWADs()
	f.extra = []fixtureFile{{path: "first.wad", body: []byte("a second first.wad")}}

	err := publishError(t, f)
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("a duplicate member = %v", err)
	}
}

// Two names that differ only in case are one file on Windows and macOS. A
// bundle that relies on them being two is a bundle that extracts differently
// depending on whose machine it lands on.
func TestACaseFoldCollisionIsRefused(t *testing.T) {
	f := twoOrderedWADs()
	f.extra = []fixtureFile{{path: "FIRST.WAD", body: []byte("the other one")}}

	err := publishError(t, f)
	if err == nil || !strings.Contains(err.Error(), "differ only in case") {
		t.Fatalf("a case-fold collision = %v", err)
	}
}

// --- bounds -------------------------------------------------------------------

func TestTheMemberCountIsBounded(t *testing.T) {
	cache := newCache(t)
	cache.SetLimits(texturebundle.Limits{
		CompressedBytes: 1 << 20, UncompressedBytes: 1 << 20, MemberBytes: 1 << 20, Members: 2,
	})
	f := twoOrderedWADs() // two WADs plus a manifest and a licences file: four members

	_, err := cache.Publish(context.Background(), f.build(t),
		texturebundle.Expect{MapID: f.mapID, Revision: f.revision})
	if err == nil || !strings.Contains(err.Error(), "the limit is 2") {
		t.Fatalf("four members against a limit of two = %v", err)
	}
}

func TestTheUncompressedTotalIsBounded(t *testing.T) {
	cache := newCache(t)
	cache.SetLimits(texturebundle.Limits{
		CompressedBytes: 1 << 20, UncompressedBytes: 8, MemberBytes: 1 << 20, Members: 32,
	})
	f := twoOrderedWADs()

	_, err := cache.Publish(context.Background(), f.build(t),
		texturebundle.Expect{MapID: f.mapID, Revision: f.revision})
	if err == nil || !strings.Contains(err.Error(), "will extract") {
		t.Fatalf("a bundle over the extraction total = %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(cache.Root(), "*", "bundle-*")); len(left) != 0 {
		t.Errorf("a refused extraction left %v behind", left)
	}
}

func TestOneMemberIsBounded(t *testing.T) {
	cache := newCache(t)
	cache.SetLimits(texturebundle.Limits{
		CompressedBytes: 1 << 20, UncompressedBytes: 1 << 20, MemberBytes: 4, Members: 32,
	})
	f := twoOrderedWADs()

	_, err := cache.Publish(context.Background(), f.build(t),
		texturebundle.Expect{MapID: f.mapID, Revision: f.revision})
	if err == nil || !strings.Contains(err.Error(), "the limit is 4") {
		t.Fatalf("an oversized member = %v", err)
	}
}

func TestTheCompressedBundleIsBounded(t *testing.T) {
	cache := newCache(t)
	cache.SetLimits(texturebundle.Limits{
		CompressedBytes: 16, UncompressedBytes: 1 << 20, MemberBytes: 1 << 20, Members: 32,
	})
	f := twoOrderedWADs()

	_, err := cache.Publish(context.Background(), f.build(t),
		texturebundle.Expect{MapID: f.mapID, Revision: f.revision})
	if err == nil || !strings.Contains(err.Error(), "the limit is 16") {
		t.Fatalf("an oversized bundle = %v", err)
	}
}

// --- cancellation and atomicity -------------------------------------------------

// A cancelled publish leaves nothing: no entry, and no temporary directory for
// the next Lookup to find half of.
func TestCancellationPublishesNothing(t *testing.T) {
	cache := newCache(t)
	f := twoOrderedWADs()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := cache.Publish(ctx, f.build(t),
		texturebundle.Expect{MapID: f.mapID, Revision: f.revision})
	if !errors.Is(err, texturebundle.ErrCancelled) {
		t.Fatalf("a cancelled publish = %v, want ErrCancelled", err)
	}
	if _, found := cache.Lookup(texturebundle.Expect{MapID: f.mapID, Revision: f.revision}); found {
		t.Error("a cancelled publish left a usable entry")
	}
	left, _ := filepath.Glob(filepath.Join(cache.Root(), "*", "bundle-*"))
	if len(left) != 0 {
		t.Errorf("a cancelled publish left %v behind", left)
	}
}

// --- offline reuse ----------------------------------------------------------------

// A complete verified entry is reusable with no network and no session. That is
// what the cache is for.
func TestACompleteEntryIsReusedOffline(t *testing.T) {
	cache := newCache(t)
	f := twoOrderedWADs()
	published := mustPublish(t, cache, f)

	found, ok := cache.Lookup(texturebundle.Expect{MapID: f.mapID, Revision: f.revision})
	if !ok {
		t.Fatal("a published entry was not found again")
	}
	if found.Dir != published.Dir || found.Digest() != published.Digest() {
		t.Errorf("lookup found %s/%s, want %s/%s",
			found.Dir, found.Digest(), published.Dir, published.Digest())
	}
	if !found.CompilerReady() {
		t.Error("a reused entry lost its compiler-ready verdict")
	}
}

// And an entry whose files no longer match what was verified is NOT reused.
// Offline reuse is only honest if the reuse re-checks.
func TestATamperedEntryIsNotReused(t *testing.T) {
	cache := newCache(t)
	f := twoOrderedWADs()
	entry := mustPublish(t, cache, f)

	if err := os.WriteFile(filepath.Join(entry.ContentRoot, "first.wad"),
		[]byte("not what was verified"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found := cache.Lookup(texturebundle.Expect{MapID: f.mapID, Revision: f.revision}); found {
		t.Fatal("an edited cache entry was reused")
	}
	if err := entry.Verify(); err == nil {
		t.Error("Verify passed on an edited entry")
	}
}

// Publishing the same bytes twice is one entry, not two.
func TestPublishingTheSameBundleTwiceIsOneEntry(t *testing.T) {
	cache := newCache(t)
	f := twoOrderedWADs()

	first := mustPublish(t, cache, f)
	second := mustPublish(t, cache, f)
	if first.Dir != second.Dir {
		t.Errorf("two publishes of one bundle produced %s and %s", first.Dir, second.Dir)
	}
}

// --- helpers ---------------------------------------------------------------------

func publishError(t testing.TB, f fixture) error {
	t.Helper()

	_, err := publish(t, newCache(t), f)

	return err
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}
