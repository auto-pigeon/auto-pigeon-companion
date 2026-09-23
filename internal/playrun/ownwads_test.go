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
