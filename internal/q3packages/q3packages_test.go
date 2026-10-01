package q3packages

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetsync"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3vfs"
)

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func apmap(t *testing.T, extension string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "room.apmap")
	body := `{"apmap_version":"1.5","game":"quake3","entities":[
 {"id":"ent_w","classname":"worldspawn","properties":{}` + extension + `},
 {"id":"ent_p","classname":"info_player_deathmatch","properties":{}}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func ledgerJSON(packages string) string {
	return `,"extensions":{"auto-pigeon.packages":{"schema_version":"auto-pigeon.packages/1","base_root":"baseq3","mod_root":"",
 "packages":[` + packages + `]}}`
}

func binding(name string, data []byte) string {
	return `{"game":"quake3","root":"baseq3","archive_name":"` + name + `","archive_sha256":"` + digest(data) + `"}`
}

func TestTheLedgerIsReadOutOfWorldspawn(t *testing.T) {
	a, b := []byte("archive a"), []byte("archive b")
	ledger, err := ReadLedger(apmap(t, ledgerJSON(binding("zz_a.pk3", a)+","+binding("zz_b.pk3", b))))
	if err != nil {
		t.Fatal(err)
	}
	if ledger == nil || len(ledger.Packages) != 2 || ledger.BaseRoot != "baseq3" {
		t.Fatalf("ledger = %+v", ledger)
	}
	if ledger.Packages[1].ArchiveName != "zz_b.pk3" || ledger.Packages[1].ArchiveSHA256 != digest(b) {
		t.Errorf("the second binding = %+v", ledger.Packages[1])
	}

	// A map with no ledger binds nothing, and that is not an error.
	none, err := ReadLedger(apmap(t, ""))
	if err != nil || none != nil {
		t.Errorf("no ledger: %+v, %v", none, err)
	}
}

// Unreadable is not empty: a build that read a newer or damaged ledger as
// "nothing bound" would compile the map without the packages it says it needs.
func TestALedgerThisBuildCannotReadIsRefusedNotIgnored(t *testing.T) {
	good := digest([]byte("x"))
	for name, extension := range map[string]string{
		"a future schema": `,"extensions":{"auto-pigeon.packages":{"schema_version":"auto-pigeon.packages/2","packages":[]}}`,
		"not an object":   `,"extensions":{"auto-pigeon.packages":"zz.pk3"}`,
		"a digest that is not one": ledgerJSON(
			`{"game":"quake3","root":"baseq3","archive_name":"zz.pk3","archive_sha256":"abc"}`),
		"an upper-case digest": ledgerJSON(
			`{"game":"quake3","root":"baseq3","archive_name":"zz.pk3","archive_sha256":"` + strings.ToUpper(good) + `"}`),
		"an archive name that is a path": ledgerJSON(
			`{"game":"quake3","root":"baseq3","archive_name":"../../zz.pk3","archive_sha256":"` + good + `"}`),
		"an archive that is not a pk3": ledgerJSON(
			`{"game":"quake3","root":"baseq3","archive_name":"zz.sh","archive_sha256":"` + good + `"}`),
		"a game folder that is a parent reference": ledgerJSON(
			`{"game":"quake3","root":"..","archive_name":"zz.pk3","archive_sha256":"` + good + `"}`),
		"no game folder": ledgerJSON(
			`{"game":"quake3","root":"","archive_name":"zz.pk3","archive_sha256":"` + good + `"}`),
	} {
		ledger, err := ReadLedger(apmap(t, extension))
		if err == nil || failure.Of(err) != failure.ContentRefused {
			t.Errorf("%s: ledger %+v, err %v (class %q)", name, ledger, err, failure.Of(err))
		}
	}
}

// account is the AUB an account's packages are held in.
type account struct {
	rows     []aub.AssetPackage
	archives map[string][]byte
	listed   int
	fetched  []string
}

func (a *account) AssetPackages(context.Context) ([]aub.AssetPackage, error) {
	a.listed++
	return a.rows, nil
}

func (a *account) AssetPackageArchive(_ context.Context, id string) (io.ReadCloser, int64, error) {
	a.fetched = append(a.fetched, id)
	body, held := a.archives[id]
	if !held {
		return nil, 0, errors.New("404")
	}
	return io.NopCloser(bytes.NewReader(body)), int64(len(body)), nil
}

func cache(t *testing.T) *assetsync.Store {
	t.Helper()
	store, err := assetsync.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func ledgerOf(root string, archives map[string][]byte, order ...string) *Ledger {
	ledger := &Ledger{SchemaVersion: Schema, BaseRoot: "baseq3"}
	for _, name := range order {
		ledger.Packages = append(ledger.Packages, Binding{
			Game: "quake3", Root: root, ArchiveName: name, ArchiveSHA256: digest(archives[name]),
		})
	}
	return ledger
}

// The property Q3_010 asks for: what a build is handed is found in the account
// BY THE DIGEST THE MAP RECORDS, verified against it, and nothing else.
func TestEachBoundPackageIsFetchedByItsDigestAndVerified(t *testing.T) {
	archives := map[string][]byte{"zz_a.pk3": []byte("bytes of a"), "zz_b.pk3": []byte("bytes of b")}
	held := &account{
		rows: []aub.AssetPackage{
			// Same NAME as a bound archive, different bytes: must not be taken.
			{ID: "decoy", ArchiveName: "zz_a.pk3", ArchiveSHA256: digest([]byte("other")), ArchiveAvailable: true},
			{ID: "pkg_a", ArchiveName: "renamed.pk3", ArchiveSHA256: digest(archives["zz_a.pk3"]), ArchiveAvailable: true},
			{ID: "pkg_b", ArchiveName: "zz_b.pk3", ArchiveSHA256: digest(archives["zz_b.pk3"]), ArchiveAvailable: true},
		},
		archives: map[string][]byte{"pkg_a": archives["zz_a.pk3"], "pkg_b": archives["zz_b.pk3"], "decoy": []byte("other")},
	}
	store := cache(t)
	packages, err := Resolve(context.Background(), held, store, ledgerOf("baseq3", archives, "zz_a.pk3", "zz_b.pk3"))
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 2 || strings.Join(held.fetched, ",") != "pkg_a,pkg_b" || held.listed != 1 {
		t.Fatalf("packages %d, fetched %v, listed %d", len(packages), held.fetched, held.listed)
	}
	for i, name := range []string{"zz_a.pk3", "zz_b.pk3"} {
		got := packages[i]
		if got.ArchiveName != name || got.SHA256 != "sha256:"+digest(archives[name]) || got.Origin != q3vfs.OriginAccount {
			t.Errorf("package %d = %+v", i, got)
		}
		onDisk, err := os.ReadFile(got.Path)
		if err != nil || !bytes.Equal(onDisk, archives[name]) {
			t.Errorf("%s on disk is not the bound archive: %v", name, err)
		}
	}

	// A second build of the same map needs neither the account nor a network.
	again, err := Resolve(context.Background(), nil, store, ledgerOf("baseq3", archives, "zz_a.pk3", "zz_b.pk3"))
	if err != nil {
		t.Fatalf("offline, with the packages cached: %v", err)
	}
	if len(again) != 2 || again[0].Origin != q3vfs.OriginCache {
		t.Errorf("offline = %+v", again)
	}
}

func TestBytesThatAreNotTheBoundArchiveAreNotHandedOver(t *testing.T) {
	archives := map[string][]byte{"zz_a.pk3": []byte("what the map bound")}
	held := &account{
		rows: []aub.AssetPackage{{ID: "pkg_a", ArchiveName: "zz_a.pk3",
			ArchiveSHA256: digest(archives["zz_a.pk3"]), ArchiveAvailable: true}},
		// The row claims the digest; the bytes it serves are something else.
		archives: map[string][]byte{"pkg_a": []byte("what the server sent instead")},
	}
	store := cache(t)
	packages, err := Resolve(context.Background(), held, store, ledgerOf("baseq3", archives, "zz_a.pk3"))
	if err == nil || failure.Of(err) != failure.ArchiveDamaged {
		t.Fatalf("packages %+v, err %v (class %q)", packages, err, failure.Of(err))
	}
	if store.Has(digest(archives["zz_a.pk3"])) {
		t.Error("the wrong bytes were kept under the bound digest")
	}
}

func TestAPackageNobodyHoldsIsMissingGameDataSaidByName(t *testing.T) {
	archives := map[string][]byte{"zz_a.pk3": []byte("a")}
	ledger := ledgerOf("baseq3", archives, "zz_a.pk3")
	for name, held := range map[string]Account{
		"the account does not hold it": &account{},
		"the account lost its archive": &account{rows: []aub.AssetPackage{{ID: "pkg_a",
			ArchiveSHA256: digest(archives["zz_a.pk3"]), ArchiveAvailable: false}}},
		"signed out and not cached": nil,
	} {
		_, err := Resolve(context.Background(), held, cache(t), ledger)
		if err == nil || failure.Of(err) != failure.GameDataMissing {
			t.Errorf("%s: err %v (class %q)", name, err, failure.Of(err))
			continue
		}
		if !strings.Contains(err.Error(), "zz_a.pk3") || !strings.Contains(err.Error(), digest(archives["zz_a.pk3"])) {
			t.Errorf("%s: the refusal names neither the archive nor its digest: %v", name, err)
		}
	}
}

func TestAMapThatBindsNothingResolvesToNothing(t *testing.T) {
	for _, ledger := range []*Ledger{nil, {SchemaVersion: Schema}} {
		packages, err := Resolve(context.Background(), nil, cache(t), ledger)
		if err != nil || packages != nil {
			t.Errorf("packages %+v, err %v", packages, err)
		}
	}
}
