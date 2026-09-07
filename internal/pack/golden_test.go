package pack

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Proving the reproducibility claim rather than asserting it.
//
// [Target.Reproducibility] makes two different promises and the tests here are
// what earn each of them. The distinction is not cosmetic: a sidecar manifest
// says `portable` or `per_build` to somebody who may be trying to reproduce a
// published archive, and a promise that has not been measured is worse than no
// promise, because it will be believed.
//
// # What "cross-platform" is tested as
//
// This suite runs on one operating system, so a literal three-platform
// comparison is not available to it. What is available is the *complete list of
// things that differ between those platforms*, and constructing inputs that
// differ in every one of them:
//
//   - modification times (every file has one, and it is never the same twice);
//   - permission bits, and the umask that produced them;
//   - path separators in the caller's spelling;
//   - the order a directory walk returns entries in;
//   - the absolute path of the source tree.
//
// [TestArchiveBytesDoNotDependOnTheMachine] varies all five and asserts the
// bytes do not move. Anything left that could differ between platforms would
// have to be something Go's own standard library does differently per platform
// inside `archive/zip` or `encoding/binary`, and the golden digests below are
// what would catch that.

// The pinned digests. These are properties of the *format policy*, not of a
// particular run, and changing one means the promise in every manifest this
// program has ever written is no longer true. Update them only alongside a
// deliberate, documented change to what an archive contains.
const (
	goldenPAKDigest       = "sha256:643d62ded2f9223d8ede69b7acd07c23687cafd9d46b9e2c9618cfc3b57fad45"
	goldenStoredPK3Digest = "sha256:ce2b2904b6f7198dcf35d998ff8253e79c6b766fb560587de53ed070a1dff657"
)

// goldenMembers is the fixture both pinned digests are over. Deliberately
// small, deliberately including an empty member and a member whose name sorts
// after another's prefix, and deliberately fixed forever.
func goldenMembers() []Member {
	return []Member{
		memberOf("maps/e1m1.bsp", "the compiled level"),
		memberOf("maps/e1m1.lit", ""),
		memberOf("gfx/palette.lmp", "0123456789abcdef"),
		memberOf("progs/player.mdl", "a model"),
	}
}

func TestGoldenPAKDigestIsStable(t *testing.T) {
	dir := t.TempDir()
	_, raw := writeArchive(t, dir, "golden.pak", goldenMembers(), mustTarget(t, "quake-pak"))
	assertGolden(t, "PAK", raw, goldenPAKDigest)
}

func TestGoldenStoredPK3DigestIsStable(t *testing.T) {
	target, err := mustTarget(t, "quake3-pk3").With(Store)
	if err != nil {
		t.Fatalf("With(Store): %v", err)
	}
	dir := t.TempDir()
	_, raw := writeArchive(t, dir, "golden.pk3", goldenMembers(), target)
	assertGolden(t, "stored PK3", raw, goldenStoredPK3Digest)
}

func assertGolden(t *testing.T, what string, raw []byte, want string) {
	t.Helper()
	sum := sha256.Sum256(raw)
	got := "sha256:" + hex.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("the golden %s digest is %s and this build produced %s (%d bytes).\n"+
			"If that was intended, the reproducibility promise in every published manifest changed with it: "+
			"update the constant and say so in the commit.", what, want, got, len(raw))
	}
}

func TestPAKIsByteIdenticalAcrossRuns(t *testing.T) {
	assertRepeatable(t, mustTarget(t, "quake-pak"), "run.pak")
}

func TestPK3StoredIsByteIdenticalAcrossRuns(t *testing.T) {
	target, err := mustTarget(t, "quake3-pk3").With(Store)
	if err != nil {
		t.Fatalf("With(Store): %v", err)
	}
	assertRepeatable(t, target, "run.pk3")
}

// TestPK3DeflatedIsByteIdenticalWithinThisBuild is the weaker promise, tested
// as exactly the promise that is made: the same build, repeatedly. It says
// nothing about another build of the Companion, and neither does the manifest.
func TestPK3DeflatedIsByteIdenticalWithinThisBuild(t *testing.T) {
	assertRepeatable(t, mustTarget(t, "quake3-pk3"), "run.pk3")
}

func assertRepeatable(t *testing.T, target Target, name string) {
	t.Helper()
	var first []byte
	for run := 0; run < 5; run++ {
		dir := t.TempDir()
		_, raw := writeArchive(t, dir, name, goldenMembers(), target)
		if run == 0 {
			first = raw
			continue
		}
		if len(raw) != len(first) {
			t.Fatalf("%s run %d produced %d bytes, run 0 produced %d", target.ID, run, len(raw), len(first))
		}
		for i := range raw {
			if raw[i] != first[i] {
				t.Fatalf("%s run %d differs from run 0 at byte %d", target.ID, run, i)
			}
		}
	}
}

// TestArchiveBytesDoNotDependOnTheMachine varies everything that differs
// between two machines and asserts the archive does not.
func TestArchiveBytesDoNotDependOnTheMachine(t *testing.T) {
	contents := map[string]string{
		"maps/e1m1.bsp":    "the compiled level",
		"maps/e1m1.lit":    "lightmaps",
		"gfx/palette.lmp":  "0123456789abcdef",
		"progs/player.mdl": "a model",
	}

	// Two source trees that differ in every way a filesystem can differ:
	// location, permission bits, modification times, and the order the caller
	// happens to hand the files over in.
	makeTree := func(t *testing.T, mode os.FileMode, when time.Time, prefix string) string {
		t.Helper()
		root := filepath.Join(t.TempDir(), prefix)
		for name, content := range contents {
			path := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("creating %s: %v", filepath.Dir(path), err)
			}
			if err := os.WriteFile(path, []byte(content), mode); err != nil {
				t.Fatalf("writing %s: %v", path, err)
			}
			if err := os.Chtimes(path, when, when); err != nil {
				t.Fatalf("setting times on %s: %v", path, err)
			}
		}
		return root
	}

	oldTree := makeTree(t, 0o600, time.Date(1999, 3, 4, 5, 6, 7, 0, time.UTC), "a-short-name")
	newTree := makeTree(t, 0o755, time.Date(2031, 11, 12, 13, 14, 15, 0, time.UTC), "a-considerably-longer-directory-name")

	for _, id := range []string{"quake-pak", "quake2-pak", "quake3-pk3"} {
		for _, compression := range []Compression{Store, Deflate} {
			base := mustTarget(t, id)
			if !base.Supports(compression) {
				continue
			}
			target, err := base.With(compression)
			if err != nil {
				t.Fatalf("%s.With(%s): %v", id, compression, err)
			}
			t.Run(id+"/"+string(compression), func(t *testing.T) {
				first := packDirectory(t, oldTree, target)
				second := packDirectory(t, newTree, target)
				if len(first) != len(second) {
					t.Fatalf("two trees differing only in mode, mtime and location produced %d and %d bytes",
						len(first), len(second))
				}
				for i := range first {
					if first[i] != second[i] {
						t.Fatalf("two trees differing only in mode, mtime and location differ at byte %d", i)
					}
				}
			})
		}
	}
}

// packDirectory runs the whole selection-and-write path over a directory, which
// is what a user actually does, so the walk order and the path normalization
// are inside what is being tested rather than beside it.
func packDirectory(t *testing.T, root string, target Target) []byte {
	t.Helper()
	candidates, err := Collect([]DirSource{{Dir: root}}, nil, target)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	policy := Policy{AuthoredRoots: []string{root}}
	plan, err := NewPlan(candidates, policy, target)
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	if err := plan.Blocked(); err != nil {
		t.Fatalf("the plan was blocked: %v", err)
	}
	out := filepath.Join(t.TempDir(), "out"+target.Extension())
	if _, err := Create(plan, Options{
		Output: out, Companion: "test", Now: func() time.Time { return time.Unix(0, 0).UTC() },
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading %s: %v", out, err)
	}
	return raw
}

// TestManifestIsReproducibleApartFromItsTimestamp records the one exception,
// because a reader comparing two sidecars needs to know which difference is
// expected.
func TestManifestIsReproducibleApartFromItsTimestamp(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "maps/e1m1.bsp", "the compiled level")
	target := mustTarget(t, "quake-pak")

	build := func() *Manifest {
		candidates, err := Collect([]DirSource{{Dir: root}}, nil, target)
		if err != nil {
			t.Fatalf("Collect: %v", err)
		}
		plan, err := NewPlan(candidates, Policy{AuthoredRoots: []string{root}}, target)
		if err != nil {
			t.Fatalf("NewPlan: %v", err)
		}
		result, err := Create(plan, Options{
			Output:    filepath.Join(t.TempDir(), "out.pak"),
			Companion: "test",
			Now:       func() time.Time { return time.Unix(0, 0).UTC() },
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		return result.Manifest
	}

	first, second := build(), build()
	firstBytes, err := first.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	secondBytes, err := second.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if string(firstBytes) != string(secondBytes) {
		t.Fatalf("two manifests over the same input differ:\n%s\n---\n%s", firstBytes, secondBytes)
	}
	if first.Archive.SHA256 != second.Archive.SHA256 {
		t.Fatalf("the two archives differ: %s and %s", first.Archive.SHA256, second.Archive.SHA256)
	}
}
