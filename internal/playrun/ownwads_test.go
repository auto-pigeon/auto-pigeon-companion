package playrun

import (
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// An own copy answers "was not sent" and nothing else.
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

// The selection rule, on listings rather than a filesystem, so it holds the
// same on a case-insensitive host (macOS, Windows) as on Linux.
func TestSelectOwnWADTakesTheListingsSpellingNeverTheRequestedOne(t *testing.T) {
	file := func(name string) wadEntry { return wadEntry{name: name, regular: true} }
	other := func(name string) wadEntry { return wadEntry{name: name, regular: false} }
	cases := []struct {
		name      string
		listing   []wadEntry
		want      string
		ambiguous bool
	}{
		{"exact match", []wadEntry{file("metal.wad")}, "metal.wad", false},
		{"exact match wins over a case variant", []wadEntry{file("METAL.WAD"), file("metal.wad"), file("Metal.Wad")}, "metal.wad", false},
		{"exact match wins whatever the listing order", []wadEntry{file("Metal.Wad"), file("metal.wad")}, "metal.wad", false},
		{"one case-insensitive match uses the real spelling", []wadEntry{file("METAL.WAD")}, "METAL.WAD", false},
		{"a directory is not a WAD", []wadEntry{other("metal.wad")}, "", false},
		{"a non-regular exact entry does not hide a regular case variant", []wadEntry{other("metal.wad"), file("METAL.WAD")}, "METAL.WAD", false},
		{"two case variants and no exact match are refused", []wadEntry{file("METAL.WAD"), file("Metal.wad")}, "", true},
		{"nothing matches", []wadEntry{file("base.wad")}, "", false},
		{"an empty folder", nil, "", false},
	}
	for _, c := range cases {
		got, err := selectOwnWAD(c.listing, "metal.wad")
		if c.ambiguous {
			if err == nil || !strings.Contains(err.Error(), "METAL.WAD, Metal.wad") {
				t.Errorf("%s: got %q, %v; want an ambiguity naming both spellings in sorted order", c.name, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

// The folder's own spelling reaches Path on every host, and a subfolder is
// never searched.
func TestFindOwnWADsRecordsTheSpellingOnDisk(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "METAL.WAD"), []byte("wad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "base.wad"), 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		found, err := FindOwnWADs(dir, []string{"metal.wad", "base.wad", "METAL.WAD"})
		if err != nil {
			t.Fatal(err)
		}
		for _, index := range []int{0, 2} {
			if !found[index].Found || filepath.Base(found[index].Path) != "METAL.WAD" {
				t.Errorf("%s: %+v; want the on-disk spelling METAL.WAD", found[index].Name, found[index])
			}
		}
		if found[1].Found {
			t.Errorf("base.wad is a directory and was accepted: %+v", found[1])
		}
	}
}

// NEW_313A. What is known about a WAD that was not sent is what the deployment
// has on record about its exact bytes. The remedy says that, by AUB's reason
// when the bundle gave one, and never whose the file is.
func TestTheRemedyForAWADThatWasNotSentSaysWhatIsKnown(t *testing.T) {
	refusals := []string{"wad_bytes_not_carried: metal.wad"}
	for _, c := range []struct{ reason, want string }{
		{"", "no redistribution permission on record for that file's exact bytes"},
		{"undeclared", "no redistribution permission on record for that file's exact bytes"},
		{"a_code_from_the_future", "no redistribution permission on record for that file's exact bytes"},
		{"declared_withheld", "declared that its bytes are not to be redistributed"},
		{"digest_mismatch", "not the exact file its redistribution permission names"},
		{"declaration_incomplete", "does not state a credit and terms"},
		{"source_unreadable", "could not read its copy of the file"},
		{"policy_unavailable", "redistribution records could not be read"},
	} {
		var sources []WADSource
		if c.reason != "" {
			sources = []WADSource{{Name: "metal.wad", Origin: OriginInstalled, NotSentReason: c.reason}}
		}
		remedy := compilerRefusalRemedy(refusals, sources)
		if !strings.Contains(remedy, "metal.wad was not sent") || !strings.Contains(remedy, c.want) ||
			!strings.Contains(remedy, "your own copy") {
			t.Errorf("reason %q: remedy = %q", c.reason, remedy)
		}
		for _, claim := range []string{"somebody else", "game", "id Software", "cannot redistribute"} {
			if strings.Contains(remedy, claim) {
				t.Errorf("reason %q: the remedy says %q: %q", c.reason, claim, remedy)
			}
		}
	}
}

// A run recorded before NEW_313A stored the sentence that said whose the file
// was. It is shown the current one, derived from the refusals it still holds.
func TestARunRecordedUnderTheRetiredWordingIsShownTheCurrentRemedy(t *testing.T) {
	record := &Record{
		State: Failed, FailedAt: DownloadingTextures,
		Bundle: &build.BundleRef{CompilerRefusals: []string{"wad_bytes_not_carried: metal.wad"}},
		Remedy: retiredNotSentRemedyPrefix + "metal.wad — and then a claim about whose it is.",
	}
	got := CurrentRemedy(record)
	if !strings.Contains(got, "metal.wad was not sent") || strings.Contains(got, "whose it is") {
		t.Fatalf("remedy = %q", got)
	}
}

// A person's own copy completes the declaration it answers, and the record
// keeps both facts: whose bytes were used, and why the deployment sent none.
func TestAnOwnCopyIsRecordedAgainstTheDeclarationItCompleted(t *testing.T) {
	sources := []WADSource{
		{Name: "first.wad", Origin: OriginUser, Staged: true, Revision: 3, SHA256: strings.Repeat("a", 64), Bytes: 10},
		{Name: "gfx/metal.wad", Origin: OriginInstalled, NotSentReason: "undeclared"},
	}
	own := []StagedFile{{Path: "metal.wad", SHA256: strings.Repeat("b", 64), Bytes: 20}}

	got := withOwnCopies(sources, own)
	if len(got) != 2 {
		t.Fatalf("sources = %+v", got)
	}
	if got[0] != sources[0] {
		t.Errorf("a WAD the bundle carried was changed: %+v", got[0])
	}
	metal := got[1]
	if metal.Origin != OriginOwnCopy || !metal.Staged || metal.SHA256 != own[0].SHA256 ||
		metal.Bytes != 20 || metal.NotSentReason != "undeclared" || metal.Credit != "" {
		t.Errorf("metal.wad = %+v", metal)
	}
	if sources[1].Staged {
		t.Error("the bundle's own source list was written to")
	}
}
