package q2deps

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2fixture"
)

func write(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "map.bsp")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheCompiledMapIsReadForWhatAnEngineWillAskFor(t *testing.T) {
	entities := `{ "classname" "worldspawn" "sky" "unit1_" }
{ "classname" "target_speaker" "noise" "world/amb1" }
{ "classname" "target_speaker" "noise" "world/amb2.wav" }
{ "classname" "misc_x" "model" "models/x/tris.md2" }
{ "classname" "func_door" "model" "*3" }
{ "classname" "target_changelevel" "map" "base2" }`
	m, err := ParseBSP(write(t, q2fixture.BSP(entities, "e1u1/wall", "e1u1/wall", "e1u1/sky1")))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, reference := range m.References {
		got = append(got, string(reference.Kind)+":"+strings.Join(reference.Paths, "|"))
	}
	want := []string{
		"texture:textures/e1u1/wall.wal", "texture:textures/e1u1/sky1.wal",
		"sky:env/unit1_rt.pcx|env/unit1_rt.tga", "sky:env/unit1_bk.pcx|env/unit1_bk.tga", "sky:env/unit1_lf.pcx|env/unit1_lf.tga",
		"sky:env/unit1_ft.pcx|env/unit1_ft.tga", "sky:env/unit1_up.pcx|env/unit1_up.tga", "sky:env/unit1_dn.pcx|env/unit1_dn.tga",
		"sound:sound/world/amb1.wav", "sound:sound/world/amb2.wav", "model:models/x/tris.md2",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("references:\n got  %v\n want %v", got, want)
	}
	if !strings.Contains(strings.Join(m.Limits, "\n"), `leads to the map "base2"`) {
		t.Errorf("limits: %v", m.Limits)
	}
}

func TestAFileThatIsNotAQuake2MapIsRefused(t *testing.T) {
	good := q2fixture.BSP(`{ "classname" "worldspawn" }`)
	q3 := append([]byte{}, good...)
	binary.LittleEndian.PutUint32(q3[4:], 46)
	outside := append([]byte{}, good...)
	binary.LittleEndian.PutUint32(outside[8+5*8:], 1<<30)
	for name, data := range map[string][]byte{
		"BSP29":        append([]byte{29, 0, 0, 0}, make([]byte, 200)...),
		"IBSP 46":      q3,
		"a lump out":   outside,
		"short":        []byte("IBSP"),
		"bad entities": q2fixture.BSP(`{ "classname" "worldspawn" `),
	} {
		if _, err := ParseBSP(write(t, data)); err == nil {
			t.Errorf("%s was read as a Quake II map", name)
		}
	}
}

// Every case here holds on any host, whatever its filesystem folds: the two
// fixture roots never hold two spellings of one folder or file, so a host that
// merges `Mine/` and `mine/` cannot change what the test is about (Q2_020, CI
// at 7b7524d: the old fixture held both, and a macOS or Windows runner merged
// them into one folder). What depends on the filesystem's case behaviour is in
// case_test.go, where it is measured first. Each case is run twice: against
// this machine's filesystem, and against the case-insensitive simulation, so a
// case-sensitive Linux runner also exercises the identity match that a
// Windows or macOS one relies on.
func TestAReferenceResolvesTheWayTheEngineSearches(t *testing.T) {
	for _, host := range []struct {
		name     string
		simulate bool
	}{{"this filesystem", false}, {"simulated case-insensitive filesystem", true}} {
		t.Run(host.name, func(t *testing.T) { resolveTheWayTheEngineSearches(t, host.simulate) })
	}
}

func resolveTheWayTheEngineSearches(t *testing.T, simulate bool) {
	root := t.TempDir()
	base := mustWrite(t, filepath.Join(root, "base"), map[string][]byte{
		"pak0.pak": q2fixture.PAK(
			[]string{"env/skyrt.tga", "textures/e1/Wall.wal", "textures/e1/shared.wal", "models/m/base.pcx", "textures/E1/Upper.wal"},
			map[string][]byte{"env/skyrt.tga": {1}, "textures/e1/Wall.wal": {2, 2}, "textures/e1/shared.wal": {11},
				"models/m/base.pcx": {13}, "textures/E1/Upper.wal": {14}}),
		"pak1.pak": q2fixture.PAK([]string{"textures/e1/wall.wal"}, map[string][]byte{"textures/e1/wall.wal": {3, 3, 3}}),
		// The same path loose in the folder: the archives are searched first.
		"textures/e1/wall.wal": {4, 4, 4, 4},
		"textures/e1/x.wal":    {12},
	})
	content := mustWrite(t, filepath.Join(root, "content"), map[string][]byte{
		"env/skyrt.tga":                {5},
		"models/m/tris.md2":            q2fixture.MD2("models/m/skin.pcx", "models/m/gone.pcx", "models/m/base.pcx"),
		"models/m/skin.pcx":            {6},
		"textures/Mine/Upper.wal":      {7},
		"textures/other/lower.wal":     {8},
		"textures/e1/shared.wal":       {9},
		"textures/archived/in-pak.pak": {10},
	})
	baseIndex, err := IndexDir(base)
	if err != nil {
		t.Fatal(err)
	}
	contentIndex, err := IndexDir(content)
	if err != nil {
		t.Fatal(err)
	}
	if simulate {
		baseIndex.stat, contentIndex.stat = foldingStat(base), foldingStat(content)
	}
	if len(contentIndex.Archives) != 0 {
		t.Errorf("an archive below the top of the content folder was opened: %v", contentIndex.Archives)
	}
	texture := func(name string) Reference {
		return Reference{Kind: KindTexture, Name: name, Paths: []string{"textures/" + name + ".wal"}}
	}
	refs := []Reference{
		{Kind: KindSky, Name: "skyrt", Paths: []string{"env/skyrt.pcx", "env/skyrt.tga"}},
		texture("e1/wall"), texture("e1/UPPER"), texture("e1/x"), texture("e1/shared"),
		texture("Mine/Upper"), texture("OTHER/LOWER"), texture("Gone/Absent"),
		{Kind: KindModel, Name: "models/m/tris.md2", Paths: []string{"models/m/tris.md2"}},
	}
	type want struct {
		where    Where
		path     string // the virtual path that resolved
		location string // the spelling the folder holds
		archive  string
		bytes    string
		shadows  bool
	}
	wants := map[string]want{
		// The content's tga is found before the base's: the mod is searched
		// first for each name, and pcx is asked for before tga everywhere.
		"skyrt": {FromContent, "env/skyrt.tga", "env/skyrt.tga", "", "\x05", true},
		// pak1 is searched before pak0, whatever the case of its member, and
		// both before the folder's loose copy of the same path.
		"e1/wall":  {FromBase, "textures/e1/wall.wal", "textures/e1/wall.wal", "pak1.pak", "\x03\x03\x03", false},
		"e1/UPPER": {FromBase, "textures/e1/UPPER.wal", "textures/E1/Upper.wal", "pak0.pak", "\x0e", false},
		// No archive has it: the base folder's loose file.
		"e1/x": {FromBase, "textures/e1/x.wal", "textures/e1/x.wal", "", "\x0c", false},
		// The content's copy wins over the base game's, and says it shadows it.
		"e1/shared": {FromContent, "textures/e1/shared.wal", "textures/e1/shared.wal", "", "\x09", true},
		// The exact spelling, mixed case and all.
		"Mine/Upper": {FromContent, "textures/Mine/Upper.wal", "textures/Mine/Upper.wal", "", "\x07", false},
		// Not under the spelling asked for on a case-sensitive filesystem, so
		// lower-cased; a case-insensitive one opens it at the first try. The
		// file, and the spelling on disk, are the same either way.
		"OTHER/LOWER": {FromContent, "textures/OTHER/LOWER.wal", "textures/other/lower.wal", "", "\x08", false},
		"Gone/Absent": {Missing, "", "", "", "", false},
		// A model the content holds is read for its skins, each resolved the
		// same way: one in the content, one only in the base, one nowhere.
		"models/m/tris.md2": {FromContent, "models/m/tris.md2", "models/m/tris.md2", "", "", false},
		"models/m/skin.pcx": {FromContent, "models/m/skin.pcx", "models/m/skin.pcx", "", "\x06", false},
		"models/m/base.pcx": {FromBase, "models/m/base.pcx", "models/m/base.pcx", "pak0.pak", "\x0d", false},
		"models/m/gone.pcx": {Missing, "", "", "", "", false},
	}
	report := Resolve(&Map{References: refs}, contentIndex, baseIndex)
	got := map[string]Resolved{}
	for _, file := range report.Files {
		got[file.Name] = file
	}
	failed := false
	for name, w := range wants {
		file, ok := got[name]
		switch {
		case !ok:
			t.Errorf("%s was not resolved at all", name)
		case file.Where != w.where || file.Path != w.path || file.Location.Path != w.location ||
			file.Location.Archive != w.archive || file.Shadows != w.shadows:
			t.Errorf("%s: %s %q at %q in %q shadows=%v, want %s %q at %q in %q shadows=%v", name,
				file.Where, file.Path, file.Location.Path, file.Location.Archive, file.Shadows,
				w.where, w.path, w.location, w.archive, w.shadows)
		case w.bytes != "" && contentOf(t, map[Where]*Index{FromContent: contentIndex, FromBase: baseIndex}[file.Where], file.Location) != w.bytes:
			t.Errorf("%s: %s holds %q, want %q — another file won", name, file.Location.Path,
				contentOf(t, map[Where]*Index{FromContent: contentIndex, FromBase: baseIndex}[file.Where], file.Location), w.bytes)
		default:
			continue
		}
		failed = true
	}
	if len(report.Files) != len(wants) {
		t.Errorf("%d files resolved, want %d", len(report.Files), len(wants))
		failed = true
	}
	if failed {
		t.Logf("the fixture as this host enumerates it:\n%s", listing(t, root))
	}
}

// Q2_015A: the account textures a map binds — the build's verified copy of the
// export — are searched first. They win the content folder's and the base
// game's files of the same path, which are recorded rather than dropped; an
// exact mixed-case spelling AUB admits is found as spelled; and a path the
// account does not hold is resolved as before.
func TestTheAccountTexturesAMapBindsAreFoundFirst(t *testing.T) {
	root := t.TempDir()
	account := mustWrite(t, filepath.Join(root, "overlay"), map[string][]byte{
		"textures/aup687697486/red_7a099681.wal": {1},
		"textures/AupMixed/Blue_Case.wal":        {2},
	})
	content := mustWrite(t, filepath.Join(root, "content"), map[string][]byte{
		"textures/aup687697486/red_7a099681.wal": {3},
		"textures/mine/own.wal":                  {4},
	})
	base := mustWrite(t, filepath.Join(root, "base"), map[string][]byte{
		"pak0.pak": q2fixture.PAK([]string{"textures/aup687697486/red_7a099681.wal", "textures/e1u1/metal1_1.wal"},
			map[string][]byte{"textures/aup687697486/red_7a099681.wal": {5}, "textures/e1u1/metal1_1.wal": {6}}),
	})
	layers := Layers{}
	for _, side := range []struct {
		dir   string
		index **Index
	}{{account, &layers.Account}, {content, &layers.Content}, {base, &layers.Base}} {
		index, err := IndexDir(side.dir)
		if err != nil {
			t.Fatal(err)
		}
		*side.index = index
	}
	texture := func(name string) Reference {
		return Reference{Kind: KindTexture, Name: name, Paths: []string{"textures/" + name + ".wal"}}
	}
	report := ResolveLayers(&Map{References: []Reference{texture("aup687697486/red_7a099681"),
		texture("AupMixed/Blue_Case"), texture("mine/own"), texture("e1u1/metal1_1"), texture("e1u1/absent")}}, layers)
	got := map[string]Resolved{}
	for _, file := range report.Files {
		got[file.Name] = file
	}
	red := got["aup687697486/red_7a099681"]
	if red.Where != FromAccount || contentOf(t, layers.Account, red.Location) != "\x01" || !red.Shadows ||
		red.ShadowedContent == nil || contentOf(t, layers.Content, *red.ShadowedContent) != "\x03" {
		t.Errorf("red: %+v", red)
	}
	if blue := got["AupMixed/Blue_Case"]; blue.Where != FromAccount || blue.Location.Path != "textures/AupMixed/Blue_Case.wal" {
		t.Errorf("blue: %+v", blue)
	}
	for name, where := range map[string]Where{"mine/own": FromContent, "e1u1/metal1_1": FromBase, "e1u1/absent": Missing} {
		if got[name].Where != where {
			t.Errorf("%s: %s, want %s", name, got[name].Where, where)
		}
	}
	// Without the account layer, the same map resolves as it always did.
	plain := Resolve(&Map{References: []Reference{texture("aup687697486/red_7a099681")}}, layers.Content, layers.Base)
	if plain.Files[0].Where != FromContent {
		t.Errorf("no account layer: %+v", plain.Files[0])
	}
}
