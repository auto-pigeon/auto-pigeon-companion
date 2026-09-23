package playrun

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// An own copy answers "may not redistribute" and nothing else.
func TestOwnWADsNeededOnlyForWADsAUBMayNotCarry(t *testing.T) {
	cases := []struct {
		refusals []string
		names    []string
		ok       bool
	}{
		{[]string{"wad_inventory_incomplete", "wad_bytes_not_carried: metal.wad"}, []string{"metal.wad"}, true},
		{[]string{"wad_bytes_not_carried: gfx/base.wad", "wad_bytes_not_carried: metal.wad"}, []string{"base.wad", "metal.wad"}, true},
		{[]string{`wad_bytes_not_carried: C:\quake\id1\METAL.WAD`}, []string{"METAL.WAD"}, true},
		{[]string{"wad_bytes_not_carried: metal.wad", "texture_source_private: x"}, nil, false},
		{[]string{"texture_missing: sky1"}, nil, false},
		{[]string{"wad_inventory_incomplete"}, nil, false},
		{nil, nil, false},
	}
	for _, c := range cases {
		names, ok := OwnWADsNeeded(c.refusals)
		if ok != c.ok || !reflect.DeepEqual(names, c.names) {
			t.Errorf("%v: got %v %v, want %v %v", c.refusals, names, ok, c.names, c.ok)
		}
	}
}

func TestFindOwnWADsIsExactThenCaseInsensitiveAndNeverRecursive(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "METAL.WAD"), []byte("wad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "gfx"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gfx", "base.wad"), []byte("wad"), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := FindOwnWADs(dir, []string{"metal.wad", "base.wad"})
	if err != nil {
		t.Fatal(err)
	}
	if !found[0].Found || filepath.Base(found[0].Path) != "METAL.WAD" || found[0].Bytes != 3 {
		t.Errorf("metal.wad: %+v", found[0])
	}
	if found[1].Found {
		t.Errorf("base.wad was found in a subfolder; only the named folder is searched: %+v", found[1])
	}
	if _, err := FindOwnWADs("relative/dir", []string{"metal.wad"}); err == nil {
		t.Error("a relative folder was accepted")
	}
}

// Own WADs are staged where the map DECLARES them, because the compiler joins
// `-wadpath` with the declared path: dm2 declares `gfx/metal.wad`, and a copy at
// the root was never opened — every texture compiled missing (2026-09-23).
func TestAnOwnWADIsStagedWhereTheMapDeclaresIt(t *testing.T) {
	refusals := []string{"wad_bytes_not_carried: gfx/metal.wad", "wad_bytes_not_carried: ../../etc/evil.wad",
		`wad_bytes_not_carried: C:\quake\id1\base.wad`}
	destinations := OwnWADDestinations(refusals)
	want := map[string][]string{"metal.wad": {"gfx/metal.wad"}, "evil.wad": {"evil.wad"}, "base.wad": {"base.wad"}}
	if !reflect.DeepEqual(destinations, want) {
		t.Fatalf("destinations = %v, want %v", destinations, want)
	}

	own := t.TempDir()
	if err := os.WriteFile(filepath.Join(own, "METAL.WAD"), []byte("WAD2"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{store: store}
	record := &Record{ID: "run1", Request: Request{OwnWADsDir: own}}
	root, staged, err := s.completeWithOwnWADs(record, t.TempDir(), []string{"metal.wad"},
		OwnWADDestinations(refusals[:1]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "gfx", "metal.wad")); err != nil {
		t.Fatalf("metal.wad is not where the map declares it: %v", err)
	}
	if len(staged) != 1 || staged[0].Path != "gfx/metal.wad" {
		t.Fatalf("staged = %+v", staged)
	}
}
