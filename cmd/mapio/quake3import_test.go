package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// q3map-to-apmap through the converter (NEW_389): the library's Quake III import as a process
// invocation — the document is published whole with no manifest, a refusal publishes nothing and
// says why in the terminal record, and version names every direction the build converts.

const quake3Room = `{
"classname" "worldspawn"
{
( 0 64 16 ) ( 64 64 16 ) ( 64 0 16 ) textures/base/floor 0 0 0 0.5 0.5 0 0 0
( 0 0 0 ) ( 64 0 0 ) ( 64 64 0 ) textures/base/floor 0 0 0 0.5 0.5 0 0 0
( 64 0 16 ) ( 64 64 16 ) ( 64 64 0 ) base/wall 0 0 0 0.5 0.5 0 0 0
( 0 64 16 ) ( 0 0 16 ) ( 0 0 0 ) base/wall 0 0 0 0.5 0.5 0 0 0
( 64 64 16 ) ( 0 64 16 ) ( 0 64 0 ) base/wall 0 0 0 0.5 0.5 0 0 0
( 0 0 16 ) ( 64 0 16 ) ( 64 0 0 ) base/wall 0 0 0 0.5 0.5 0 0 0
}
{
patchDef2
{
textures/base/arch
( 3 3 0 0 0 )
(
( ( 0 0 16 0 0 ) ( 0 32 16 0 0.5 ) ( 0 64 16 0 1 ) )
( ( 32 0 40 0.5 0 ) ( 32 32 40.125 0.5 0.5 ) ( 32 64 40 0.5 1 ) )
( ( 64 0 16 1 0 ) ( 64 32 16 1 0.5 ) ( 64 64 16 1 1 ) )
)
}
}
}
`

func TestAQuake3MapImportsWholeAndWritesBack(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "q3room.map")
	if err := os.WriteFile(source, []byte(quake3Room), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(directory, "out")
	code, stdout, stderr := invoke(t, context.Background(), "convert", "--q3map-to-apmap", "--input", source, "--output", out, "--json")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var result convertResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.Manifest != "" || !result.Validated {
		t.Fatalf("result %s (%v)", stdout, err)
	}
	document, err := os.ReadFile(filepath.Join(out, "q3room.apmap"))
	if err != nil || !strings.Contains(string(document), `"game": "quake3"`) || !strings.Contains(string(document), `"kind": "patch"`) ||
		!strings.Contains(string(document), `"texture": "base/arch"`) {
		t.Fatalf("the document: %v\n%s", err, document)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 2 { // the document and the commit lock
		names := []string{}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("output holds %v", names)
	}
	back := filepath.Join(directory, "back")
	code, _, stderr = invoke(t, context.Background(), "convert", "--apmap-to-q3map", "--input", filepath.Join(out, "q3room.apmap"), "--output", back)
	if code != exitOK {
		t.Fatalf("writing it back: %s", stderr)
	}
	written, _ := os.ReadFile(filepath.Join(back, "q3room.map"))
	if !strings.Contains(string(written), "patchDef2") || !strings.Contains(string(written), "40.125") {
		t.Fatalf("the written map lost the patch:\n%s", written)
	}
}

func TestARefusedQuake3ImportPublishesNothing(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "d3.map")
	if err := os.WriteFile(source, []byte("{\n{\nbrushDef3\n{\n}\n}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(directory, "out")
	code, _, stderr := invoke(t, context.Background(), "convert", "--q3map-to-apmap", "--input", source, "--output", out)
	if code != exitFailed || !strings.Contains(stderr, TerminalMarker) || !strings.Contains(stderr, `"reason":"quake3_import"`) ||
		!strings.Contains(stderr, "[quake3_import_parse]") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(out, "d3.apmap")); !os.IsNotExist(err) {
		t.Fatalf("a refused import published a document: %v", err)
	}
	// A Quake III map under a Quake 1 direction is refused as the wrong game, by name.
	room := filepath.Join(directory, "q3room.map")
	if err := os.WriteFile(room, []byte(quake3Room), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = invoke(t, context.Background(), "convert", "--q1map-to-apmap", "--input", room, "--output", out)
	if code != exitFailed || !strings.Contains(stderr, `"reason":"unsupported_game"`) || !strings.Contains(stderr, "q3map-to-apmap") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
}

func TestVersionNamesTheLibraryAndEveryDirection(t *testing.T) {
	code, stdout, stderr := invoke(t, context.Background(), "version", "--json")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var version struct {
		Module     string   `json:"library_module"`
		Library    string   `json:"library_version"`
		Directions []string `json:"directions"`
	}
	if err := json.Unmarshal([]byte(stdout), &version); err != nil {
		t.Fatal(err)
	}
	if version.Module != "github.com/auto-pigeon/auto-pigeon-libraries" || version.Library == "" || len(version.Directions) != 8 ||
		!strings.Contains(strings.Join(version.Directions, ","), "q3map-to-apmap") {
		t.Fatalf("version %s", stdout)
	}
}
