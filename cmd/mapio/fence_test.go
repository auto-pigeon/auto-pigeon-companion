package main

// NEW_643 (auto-pigeon-libraries#15): the converter this Companion ships reads a
// Quake 1 map with a legal fence texture in every direction a build or a user
// takes it, and keeps the texture's name.
//
// Before the library pin moved to b5d7e3b, `convert --q1map-to-apmap` refused
// this very room at its first fence face — `fence_room.map:55:43: expected
// texture name after face points`, reason `invalid_input` — while
// `canonical-map` already read it (it parses with quake3map, NEW_434). This
// test is that difference, through the program's own command line.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle/fixturewad"
)

// fenceCanonical runs `canonical-map` on input and returns the copy it wrote.
func fenceCanonical(t *testing.T, input, output string) []byte {
	t.Helper()
	code, _, stderr := invoke(t, context.Background(), "canonical-map", "--input", input, "--output", output, "--json")
	if code != exitOK {
		t.Fatalf("canonical-map %s: exit %d: %s", filepath.Base(input), code, stderr)
	}
	out, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAQuake1FenceRoomConvertsBothWaysAndIsCompiledAsDrawn(t *testing.T) {
	dir := t.TempDir()
	canonical := map[string][]byte{}
	for _, texture := range []string{fixturewad.FenceRoomFence, fixturewad.FenceRoomWall} {
		stem := strings.TrimPrefix(texture, "{") + "_room"
		source := filepath.Join(dir, stem+".map")
		text := fixturewad.FenceRoomMap(texture)
		if err := os.WriteFile(source, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}

		// .map → APMap: the direction the old pin refused.
		imported := filepath.Join(dir, stem+"-apmap")
		code, _, stderr := invoke(t, context.Background(), "convert", "--q1map-to-apmap",
			"--input", source, "--output", imported, "--json")
		if code != exitOK {
			t.Fatalf("%s: q1map-to-apmap exit %d: %s", texture, code, stderr)
		}
		document, err := os.ReadFile(filepath.Join(imported, stem+".apmap"))
		if err != nil {
			t.Fatal(err)
		}
		var probe map[string]any
		if err := json.Unmarshal(document, &probe); err != nil || probe["game"] != "quake1" {
			t.Fatalf("%s: the APMap is not a Quake 1 document (%v): %.200s", texture, err, document)
		}
		if got := strings.Count(string(document), `"`+fixturewad.FenceRoomFence+`"`); texture == fixturewad.FenceRoomFence &&
			got != fixturewad.FenceRoomFenceFaces {
			t.Fatalf("the APMap names %s on %d faces, want %d", texture, got, fixturewad.FenceRoomFenceFaces)
		}

		// APMap → .map: the direction a build of a downloaded revision takes.
		exported := filepath.Join(dir, stem+"-map")
		code, _, stderr = invoke(t, context.Background(), "convert", "--apmap-to-q1map",
			"--input", filepath.Join(imported, stem+".apmap"), "--output", exported, "--json")
		if code != exitOK {
			t.Fatalf("%s: apmap-to-q1map exit %d: %s", texture, code, stderr)
		}
		roundTripped := filepath.Join(exported, stem+".map")

		// What a build compiles: the canonical copy, from the drawn map and from
		// the round trip alike, and stable when read and written again.
		fromSource := fenceCanonical(t, source, filepath.Join(dir, stem+".canonical.map"))
		fromRoundTrip := fenceCanonical(t, roundTripped, filepath.Join(dir, stem+".roundtrip.canonical.map"))
		again := fenceCanonical(t, filepath.Join(dir, stem+".canonical.map"), filepath.Join(dir, stem+".again.map"))
		if string(fromSource) != string(fromRoundTrip) || string(fromSource) != string(again) {
			t.Fatalf("%s: the canonical copy is not one map:\nsource:\n%s\nround trip:\n%s\nagain:\n%s",
				texture, fromSource, fromRoundTrip, again)
		}
		bothModelsAgree(t, texture, fromSource)
		canonical[texture] = fromSource
	}

	// The fence survives as the texture every face of the slab names, and the
	// two rooms differ in nothing else: the ordinary room is the control.
	fence := ericwEntities(string(canonical[fixturewad.FenceRoomFence]))[0].textures
	if got := strings.Count(strings.Join(fence, "\n")+"\n", fixturewad.FenceRoomFence+"\n"); got != fixturewad.FenceRoomFenceFaces {
		t.Fatalf("the canonical copy names %s on %d faces, want %d: %v",
			fixturewad.FenceRoomFence, got, fixturewad.FenceRoomFenceFaces, fence)
	}
	if want := strings.ReplaceAll(string(canonical[fixturewad.FenceRoomFence]), fixturewad.FenceRoomFence,
		fixturewad.FenceRoomWall); want != string(canonical[fixturewad.FenceRoomWall]) {
		t.Fatalf("the fence room and its control differ beyond the texture name:\n%s\n---\n%s",
			want, canonical[fixturewad.FenceRoomWall])
	}
	if !strings.Contains(string(canonical[fixturewad.FenceRoomFence]),
		`"wad" "`+fixturewad.FenceRoomFenceWAD+";"+fixturewad.FenceRoomWallWAD+`"`) {
		t.Fatalf("the WAD list did not survive as written:\n%s", canonical[fixturewad.FenceRoomFence])
	}
}

// What #15 accepts is a fence NAME, not a brace anywhere: a face whose texture is
// a lone `{`, or a brace glued to a name the wrong way, is still refused by the
// converter with the reason and position it always gave, and by the compile
// boundary before any compiler could start.
func TestABraceThatIsNotAFenceNameIsStillRefused(t *testing.T) {
	dir := t.TempDir()
	room := fixturewad.FenceRoomMap(fixturewad.FenceRoomFence)
	for name, broken := range map[string]string{
		"lone brace":    strings.Replace(room, fixturewad.FenceRoomFence+" 0 0 0 1 1", "{ 0 0 0 1 1", 1),
		"double brace":  strings.Replace(room, fixturewad.FenceRoomFence, "{"+fixturewad.FenceRoomFence, 1),
		"closing brace": strings.Replace(room, fixturewad.FenceRoomFence, fixturewad.FenceRoomFence+"}", 1),
	} {
		source := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".map")
		if err := os.WriteFile(source, []byte(broken), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+"-out")
		code, _, stderr := invoke(t, context.Background(), "convert", "--q1map-to-apmap",
			"--input", source, "--output", out)
		if code != exitFailed || !strings.Contains(stderr, `"reason":"invalid_input"`) ||
			!strings.Contains(stderr, filepath.Base(source)+":55:") {
			t.Fatalf("%s: q1map-to-apmap exit %d, want a refusal at the fence face (line 55):\n%s", name, code, stderr)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatalf("%s: a refused conversion created its output: %v", name, err)
		}
		copyPath := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".canonical.map")
		code, _, stderr = invoke(t, context.Background(), "canonical-map", "--input", source, "--output", copyPath)
		if code != exitFailed || !strings.Contains(stderr, `"reason":"map_refused"`) {
			t.Fatalf("%s: canonical-map exit %d, want map_refused:\n%s", name, code, stderr)
		}
		if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
			t.Fatalf("%s: a refused map left a copy a compiler could read: %v", name, err)
		}
	}
}
