package joincontent_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/assetsync"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/joincontent"
)

// PinnedDigest is auto-pigeon-backend's
// `internal/hostedgame.PinnedJoinContentDigest` for the same fixture. Two
// programs in two repositories compute one canonical text; this is where a
// drift in either would show.
const PinnedDigest = "sha256:cdd70f33266a40e0ba861d379745d246520b4c20be2a5f9a90992b911dad62cf"

func TestTheDigestIsTheOneAUBComputes(t *testing.T) {
	manifest := aub.JoinContentManifest{SchemaVersion: aub.JoinContentSchema, MapID: "abc123",
		MapRevision: 7, GameFamily: "quake1", Files: []aub.JoinContentFile{
			{Role: "lit", Destination: "maps/dm3.lit", Bytes: 10, SHA256: strings.Repeat("1", 64)},
			{Role: "bsp", Destination: "maps/dm3.bsp", Bytes: 20, SHA256: strings.Repeat("2", 64)},
		}}
	if manifest.Digest() != PinnedDigest {
		t.Fatalf("digest = %s, want the cross-repository pin %s", manifest.Digest(), PinnedDigest)
	}
}

func TestThisMachineRefusesWhatAUBRefusesEvenIfAUBServedIt(t *testing.T) {
	bsp := aub.JoinContentFile{Role: "bsp", Destination: "maps/dm3.bsp", Bytes: 4, SHA256: strings.Repeat("a", 64)}
	asset := func(destination string) aub.JoinContentFile {
		return aub.JoinContentFile{Role: "asset", Destination: destination, Bytes: 4,
			SHA256: strings.Repeat("b", 64), Redistributable: true}
	}
	for name, files := range map[string][]aub.JoinContentFile{
		"absolute":       {bsp, asset("/home/me/.bashrc")},
		"parent":         {bsp, asset("sound/../../../.ssh/authorized_keys")},
		"drive":          {bsp, asset("C:/Windows/x.wav")},
		"unc":            {bsp, asset(`\\server\share\x.wav`)},
		"backslash":      {bsp, asset(`sound\x.wav`)},
		"hidden":         {bsp, asset("sound/.x.wav")},
		"case collision": {bsp, asset("sound/X.wav"), asset("sound/x.wav")},
		"map source":     {bsp, asset("maps/dm3.map")},
		"base pak":       {bsp, asset("pak0.pak")},
		"game code":      {bsp, asset("progs/progs.dat")},
		"autoexec":       {bsp, asset("gfx/autoexec.cfg")},
		"undeclared":     {bsp, {Role: "asset", Destination: "sound/x.wav", Bytes: 1, SHA256: strings.Repeat("c", 64)}},
		"no bsp":         {asset("sound/x.wav")},
		"oversized":      {{Role: "bsp", Destination: "maps/x.bsp", Bytes: joincontent.MaxFileBytes + 1, SHA256: strings.Repeat("a", 64)}},
	} {
		_, err := joincontent.Check(aub.JoinContentManifest{SchemaVersion: aub.JoinContentSchema, Files: files})
		if !errors.Is(err, joincontent.ErrInvalid) {
			t.Errorf("%s: accepted (%v)", name, err)
		}
	}
}

// fixture is a verified package whose bytes a fake AUB serves.
type fixture struct {
	pkg    aub.JoinPackage
	bodies map[string][]byte
	served int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{bodies: map[string][]byte{
		"maps/pigeon.bsp": []byte("BSP29" + strings.Repeat("\x01", 2048)),
		"maps/pigeon.lit": []byte("QLIT" + strings.Repeat("\x02", 512)),
	}}
	f.pkg = aub.JoinPackage{SchemaVersion: aub.JoinContentSchema, MapID: "map1", MapRevision: 3,
		GameFamily: "quake1"}
	for _, destination := range []string{"maps/pigeon.bsp", "maps/pigeon.lit"} {
		body := f.bodies[destination]
		sum := sha256.Sum256(body)
		role := "bsp"
		if strings.HasSuffix(destination, ".lit") {
			role = "lit"
		}
		f.pkg.Files = append(f.pkg.Files, aub.JoinContentFile{Role: role, Destination: destination,
			Bytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:])})
	}
	f.pkg.PackageSHA256 = f.pkg.Manifest().Digest()

	return f
}

func (f *fixture) fetch(_ context.Context, file aub.JoinContentFile) (io.ReadCloser, error) {
	f.served++

	return io.NopCloser(bytes.NewReader(f.bodies[file.Destination])), nil
}

func newStager(t *testing.T) *joincontent.Stager {
	t.Helper()
	cache := t.TempDir()
	store, err := assetsync.Open(cache)
	if err != nil {
		t.Fatal(err)
	}

	return &joincontent.Stager{Root: filepath.Join(cache, "join-content"), Store: store}
}

func TestAPackageIsDownloadedOnceVerifiedAndStagedAtomically(t *testing.T) {
	f := newFixture(t)
	stager := newStager(t)

	files, err := joincontent.Verify(f.pkg, f.pkg.PackageSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stager.Lookup(f.pkg.PackageSHA256, files); !errors.Is(err, joincontent.ErrNotStaged) {
		t.Fatalf("before staging: %v", err)
	}
	if err = stager.Download(context.Background(), files, f.fetch, nil); err != nil {
		t.Fatal(err)
	}
	stage, err := stager.Stage(f.pkg.PackageSHA256, files)
	if err != nil {
		t.Fatal(err)
	}
	if stage.Record.MapName != "pigeon" || stage.Record.GameDir != joincontent.GameDirName(f.pkg.PackageSHA256) {
		t.Fatalf("record = %+v", stage.Record)
	}
	got, err := os.ReadFile(filepath.Join(stage.GameDirPath, "maps", "pigeon.bsp"))
	if err != nil || !bytes.Equal(got, f.bodies["maps/pigeon.bsp"]) {
		t.Fatalf("staged bsp: %v", err)
	}

	// Again: nothing fetched, the same stage.
	if err = stager.Download(context.Background(), files, f.fetch, nil); err != nil {
		t.Fatal(err)
	}
	if f.served != 2 {
		t.Fatalf("served %d times, want 2: verified objects are reused", f.served)
	}
	entries, _ := os.ReadDir(stager.Root)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".staging-") {
			t.Fatalf("a temporary staging directory survived: %s", entry.Name())
		}
	}
}

func TestATamperedServedFileNeverReachesTheStage(t *testing.T) {
	f := newFixture(t)
	stager := newStager(t)
	files, err := joincontent.Verify(f.pkg, f.pkg.PackageSHA256)
	if err != nil {
		t.Fatal(err)
	}
	f.bodies["maps/pigeon.bsp"] = append([]byte("EVIL!"), f.bodies["maps/pigeon.bsp"][5:]...)
	if err = stager.Download(context.Background(), files, f.fetch, nil); !errors.Is(err, assetsync.ErrDigestMismatch) {
		t.Fatalf("a tampered file: %v", err)
	}
	if _, err = stager.Stage(f.pkg.PackageSHA256, files); err == nil {
		t.Fatal("a package with a missing object was staged")
	}
	if _, statErr := os.Stat(filepath.Join(stager.Root, strings.TrimPrefix(f.pkg.PackageSHA256, "sha256:")[:16])); statErr == nil {
		t.Fatal("a partial package directory exists")
	}
}

func TestAManifestThatIsNotTheNamedPackageIsRefused(t *testing.T) {
	f := newFixture(t)
	other := f.pkg
	other.Files = append([]aub.JoinContentFile(nil), f.pkg.Files[:1]...)
	other.PackageSHA256 = other.Manifest().Digest()
	if _, err := joincontent.Verify(other, f.pkg.PackageSHA256); !errors.Is(err, joincontent.ErrInvalid) {
		t.Fatalf("a different package under the expected digest: %v", err)
	}
	lying := f.pkg
	lying.PackageSHA256 = "sha256:" + strings.Repeat("0", 64)
	if _, err := joincontent.Verify(lying, ""); !errors.Is(err, joincontent.ErrInvalid) {
		t.Fatalf("a manifest served under a digest it does not hash to: %v", err)
	}
}

func TestAStageThatChangedIsNotReportedAsStaged(t *testing.T) {
	f := newFixture(t)
	stager := newStager(t)
	files, _ := joincontent.Verify(f.pkg, f.pkg.PackageSHA256)
	if err := stager.Download(context.Background(), files, f.fetch, nil); err != nil {
		t.Fatal(err)
	}
	stage, err := stager.Stage(f.pkg.PackageSHA256, files)
	if err != nil {
		t.Fatal(err)
	}

	for name, damage := range map[string]func(){
		"edited": func() {
			os.WriteFile(filepath.Join(stage.GameDirPath, "maps", "pigeon.lit"), []byte("nope"), 0o600)
		},
		"extra file": func() {
			os.WriteFile(filepath.Join(stage.GameDirPath, "autoexec.cfg"), []byte("bind x quit"), 0o600)
		},
		"symlink": func() {
			os.Remove(filepath.Join(stage.GameDirPath, "maps", "pigeon.lit"))
			os.Symlink("/etc/passwd", filepath.Join(stage.GameDirPath, "maps", "pigeon.lit"))
		},
	} {
		damage()
		if _, err = stager.Lookup(f.pkg.PackageSHA256, files); !errors.Is(err, joincontent.ErrStageDamaged) {
			t.Fatalf("%s: lookup = %v", name, err)
		}
		// A re-stage replaces it wholesale from the verified objects.
		if stage, err = stager.Stage(f.pkg.PackageSHA256, files); err != nil {
			t.Fatalf("%s: restage: %v", name, err)
		}
	}
}

func TestTheOverlayLinksTheOwnedGameAndWritesNothingIntoIt(t *testing.T) {
	f := newFixture(t)
	stager := newStager(t)
	files, _ := joincontent.Verify(f.pkg, f.pkg.PackageSHA256)
	stager.Download(context.Background(), files, f.fetch, nil)
	stage, err := stager.Stage(f.pkg.PackageSHA256, files)
	if err != nil {
		t.Fatal(err)
	}

	owned := t.TempDir()
	os.MkdirAll(filepath.Join(owned, "id1"), 0o755)
	os.WriteFile(filepath.Join(owned, "id1", "pak0.pak"), []byte("PACK"), 0o644)
	before := snapshot(t, owned)

	if err = stage.Overlay(owned, []string{"id1", "hipnotic"}); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(stage.BaseDir, "id1"))
	if err != nil || target != filepath.Join(owned, "id1") {
		t.Fatalf("id1 link = %q, %v", target, err)
	}
	if _, err = os.Lstat(filepath.Join(stage.BaseDir, "hipnotic")); err == nil {
		t.Fatal("a link was made to a base directory the user does not have")
	}
	// The binding moved: the link follows it.
	moved := t.TempDir()
	os.MkdirAll(filepath.Join(moved, "id1"), 0o755)
	if err = stage.Overlay(moved, []string{"id1"}); err != nil {
		t.Fatal(err)
	}
	if target, _ = os.Readlink(filepath.Join(stage.BaseDir, "id1")); target != filepath.Join(moved, "id1") {
		t.Fatalf("after a move the link is %q", target)
	}
	if after := snapshot(t, owned); after != before {
		t.Fatalf("the owned game directory changed:\n%s\n%s", before, after)
	}
	if err = stage.Overlay(owned, []string{"../etc"}); err == nil {
		t.Fatal("a base directory name with a separator was accepted")
	}
	// The overlay is not part of what Lookup verifies, and does not break it.
	if _, err = stager.Lookup(f.pkg.PackageSHA256, files); err != nil {
		t.Fatalf("lookup after overlay: %v", err)
	}
}

func TestAHostPackageIsTheBuildOutputAndChangesAreCaught(t *testing.T) {
	dir := t.TempDir()
	bsp := filepath.Join(dir, "Pigeon.bsp")
	lit := filepath.Join(dir, "Pigeon.lit")
	os.WriteFile(bsp, []byte("BSP29 level"), 0o600)
	os.WriteFile(lit, []byte("QLIT light"), 0o600)

	built, err := joincontent.Build(joincontent.Level{BSP: bsp, Lit: lit}, "map1", 4, "quake1")
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Manifest.Files) != 2 || built.Manifest.Files[0].Destination != "maps/pigeon.bsp" {
		t.Fatalf("manifest = %+v", built.Manifest)
	}

	client := &fakeUploader{}
	if _, err = joincontent.Upload(context.Background(), client, built); err != nil {
		t.Fatal(err)
	}
	if client.uploads != 1 || !bytes.Equal(client.received["maps/pigeon.bsp"], []byte("BSP29 level")) {
		t.Fatalf("upload = %+v", client)
	}

	os.WriteFile(bsp, []byte("BSP29 rebuilt level"), 0o600)
	if _, err = joincontent.Upload(context.Background(), &fakeUploader{}, built); err == nil ||
		!strings.Contains(err.Error(), "changed after it was hashed") {
		t.Fatalf("a rebuilt file uploaded under its old digest: %v", err)
	}
}

type fakeUploader struct {
	uploads  int
	received map[string][]byte
}

func (f *fakeUploader) OwnJoinPackage(context.Context, string) (aub.JoinPackage, error) {
	return aub.JoinPackage{}, errors.New("not found")
}

func (f *fakeUploader) UploadJoinPackage(_ context.Context, manifest aub.JoinContentManifest,
	open func(aub.JoinContentFile) (io.ReadCloser, error),
) (aub.JoinPackage, bool, error) {
	f.uploads++
	f.received = map[string][]byte{}
	for _, file := range manifest.Files {
		body, err := open(file)
		if err != nil {
			return aub.JoinPackage{}, false, err
		}
		raw, err := io.ReadAll(body)
		body.Close()
		if err != nil {
			return aub.JoinPackage{}, false, err
		}
		f.received[file.Destination] = raw
	}

	return aub.JoinPackage{PackageSHA256: manifest.Digest()}, true, nil
}

func snapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil {
			fmt.Fprintf(&b, "%s %d %s\n", path, info.Size(), info.Mode())
		}
		return nil
	})

	return b.String()
}
