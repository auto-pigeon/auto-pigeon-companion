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

func TestAReferenceResolvesTheWayTheEngineSearches(t *testing.T) {
	root := t.TempDir()
	base, _ := q2fixture.Write(filepath.Join(root, "base"), map[string][]byte{
		"pak0.pak": q2fixture.PAK([]string{"env/skyrt.tga", "textures/e1/Wall.wal"},
			map[string][]byte{"env/skyrt.tga": {1}, "textures/e1/Wall.wal": {2}}),
		"pak1.pak":          q2fixture.PAK([]string{"textures/e1/wall.wal"}, map[string][]byte{"textures/e1/wall.wal": {3}}),
		"textures/e1/x.wal": {4},
	})
	content, _ := q2fixture.Write(filepath.Join(root, "content"), map[string][]byte{
		"env/skyrt.tga":                {5},
		"models/m/tris.md2":            q2fixture.MD2("models/m/skin.pcx", "models/m/gone.pcx"),
		"models/m/skin.pcx":            {6},
		"textures/Mine/Upper.wal":      {7},
		"textures/mine/lower.wal":      {8},
		"textures/archived/in-pak.pak": {9},
	})
	baseIndex, err := IndexDir(base)
	if err != nil {
		t.Fatal(err)
	}
	contentIndex, err := IndexDir(content)
	if err != nil {
		t.Fatal(err)
	}
	refs := []Reference{
		{Kind: KindSky, Name: "skyrt", Paths: []string{"env/skyrt.pcx", "env/skyrt.tga"}},
		{Kind: KindTexture, Name: "e1/wall", Paths: []string{"textures/e1/wall.wal"}},
		{Kind: KindTexture, Name: "Mine/Upper", Paths: []string{"textures/Mine/Upper.wal"}},
		{Kind: KindTexture, Name: "MINE/LOWER", Paths: []string{"textures/MINE/LOWER.wal"}},
		{Kind: KindModel, Name: "models/m/tris.md2", Paths: []string{"models/m/tris.md2"}},
	}
	report := Resolve(&Map{References: refs}, contentIndex, baseIndex)
	got := map[string]string{}
	for _, file := range report.Files {
		got[file.Name] = string(file.Where) + " " + file.Location.Archive + file.Path
	}
	want := map[string]string{
		// The content's tga is found before the base's: the mod is searched
		// first for each name, and pcx is asked for before tga everywhere.
		"skyrt": "content env/skyrt.tga",
		// pak1 is searched before pak0, whatever the case of its member.
		"e1/wall":           "base pak1.paktextures/e1/wall.wal",
		"Mine/Upper":        "content textures/Mine/Upper.wal",
		"MINE/LOWER":        "content textures/MINE/LOWER.wal",
		"models/m/tris.md2": "content models/m/tris.md2",
		"models/m/skin.pcx": "content models/m/skin.pcx",
		"models/m/gone.pcx": "missing ",
	}
	for name, where := range want {
		if got[name] != where {
			t.Errorf("%s resolved to %q, want %q", name, got[name], where)
		}
	}
	// A loose file is found under its own spelling or lower-cased, as Yamagi
	// tries them; `textures/mine/lower.wal` is the second. On a
	// case-insensitive filesystem `Mine/` and `mine/` are ONE folder, spelled
	// whichever way it was created first, and the operating system opens the
	// file under either: the file is the one holding byte 8 either way.
	insensitive := caseInsensitive(t, root)
	for _, file := range report.Files {
		if file.Name != "MINE/LOWER" {
			continue
		}
		switch {
		case !insensitive && file.Location.Path != "textures/mine/lower.wal":
			t.Errorf("MINE/LOWER is %s", file.Location.Path)
		case insensitive && (!strings.EqualFold(file.Location.Path, "textures/mine/lower.wal") ||
			bytesOf(t, file.Location) != "\x08"):
			t.Errorf("MINE/LOWER is %s on a case-insensitive filesystem", file.Location.Path)
		}
	}
}
