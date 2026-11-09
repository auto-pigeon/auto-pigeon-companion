package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestInstalledEricwCompilesTheCanonicalCopyAlike is the boundary's evidence
// against a real compiler: every map named is compiled twice by the installed
// ericw-tools, once as written and once as the canonical copy a map from
// Auto-Pigeon is staged as (canonical.go), and the two BSPs must be the same
// bytes. A map the boundary refuses is reported with the reason, not compiled.
//
// Two facts about qbsp shape the comparison (measured on fork release 1.4556,
// NEW_434). With more than one thread its output is not reproducible at all, so
// every program runs with `-threads 1`. And its output depends on the LINE each
// brush starts on — it orders brushes by `(chop_index, line_number)`
// (`qbsp/map.cc`) — so one comment line added to a map changes its BSP. The
// canonical copy carries no comments; when the two BSPs differ, the map as
// written is compiled once more with its whole-line comments removed, which
// moves lines and changes nothing else, and the canonical copy must match that.
// A map whose WAD entries the boundary reduced to their file names is compared
// with the map as written carrying that same reduced list.
// Beyond lines, the order of the face indices inside each leaf (the
// `marksurfaces` lump) moves with the text's layout itself: respelling `48` as
// `48.0`, or adding one space, changes it and nothing else. So a BSP that is
// identical in every other lump, and whose every leaf lists the same faces in
// another order, is the same compiled map; compareBSP says which it is.
//
// Skipped unless AUCOM_ERICW_BIN names the folder holding qbsp (vis and light
// beside it are used when AUCOM_ERICW_FULL=1) and AUCOM_ERICW_MAPS lists maps,
// separated by the OS list separator. AUCOM_ERICW_QBSP_ARGS adds qbsp
// arguments (`-q2bsp` for Quake II maps):
//
//	AUCOM_ERICW_BIN=/path/auto-pigeon-ericw-tools-1.4556-linux-amd64/bin \
//	AUCOM_ERICW_MAPS=/path/a.map:/path/b.map \
//	go test ./cmd/mapio -run InstalledEricw -v
func TestInstalledEricwCompilesTheCanonicalCopyAlike(t *testing.T) {
	bin, list := os.Getenv("AUCOM_ERICW_BIN"), os.Getenv("AUCOM_ERICW_MAPS")
	if bin == "" || list == "" {
		t.Skip("AUCOM_ERICW_BIN / AUCOM_ERICW_MAPS are not set; no installed compiler is exercised")
	}
	full := os.Getenv("AUCOM_ERICW_FULL") == "1"
	compiled, refused := 0, 0
	for _, source := range filepath.SplitList(list) {
		name := filepath.Base(source)
		text, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		canonical, changed, err := canonicalMapText(text)
		if err != nil {
			refused++
			t.Logf("REFUSED %s: %v", name, err)
			continue
		}
		// A map whose WAD entries the boundary reduced is compared with the map
		// as written carrying the same reduced list, and nothing else changed.
		written := text
		if len(changed) > 0 {
			before, _ := ericwEntities(string(text))[0].get("wad")
			after, _ := ericwEntities(string(canonical))[0].get("wad")
			written = []byte(strings.Replace(string(text), `"`+before+`"`, `"`+after+`"`, 1))
		}
		bsps := [3][]byte{}
		skipped := false
		for index, body := range [][]byte{written, canonical, withoutCommentLines(written)} {
			dir := filepath.Join(t.TempDir(), "m")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			mapPath := filepath.Join(dir, name)
			if err := os.WriteFile(mapPath, body, 0o600); err != nil {
				t.Fatal(err)
			}
			qbsp := append([]string{"qbsp", "-threads", "1"}, strings.Fields(os.Getenv("AUCOM_ERICW_QBSP_ARGS"))...)
			steps := [][]string{append(qbsp, "-wadpath", filepath.Dir(source), mapPath)}
			if full {
				bspPath := strings.TrimSuffix(mapPath, filepath.Ext(mapPath)) + ".bsp"
				steps = append(steps, []string{"vis", "-threads", "1", "-fast", bspPath},
					[]string{"light", "-threads", "1", bspPath})
			}
			for _, step := range steps {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				command := exec.CommandContext(ctx, filepath.Join(bin, step[0]), step[1:]...)
				command.Dir = dir
				output, err := command.CombinedOutput()
				cancel()
				if err != nil && index == 0 {
					// The map as written does not compile here: nothing to compare.
					t.Logf("UNCOMPILABLE-AS-WRITTEN %s: %s: %v", name, step[0], err)
					skipped = true
					break
				}
				if err != nil {
					t.Fatalf("%s: %s on copy %d: %v\n%s", name, step[0], index, err, output)
				}
			}
			if skipped {
				break
			}
			var err error
			bsps[index], err = os.ReadFile(strings.TrimSuffix(mapPath, filepath.Ext(mapPath)) + ".bsp")
			if err != nil {
				t.Fatal(err)
			}
		}
		if skipped {
			continue
		}
		compiled++
		switch {
		case bytes.Equal(bsps[0], bsps[1]) && len(changed) > 0:
			t.Logf("SAME-WITH-REDUCED-WADS %s: identical to the map as written with its WAD list reduced as the boundary did %v", name, changed)
		case bytes.Equal(bsps[0], bsps[1]):
			t.Logf("SAME %s: %d-byte BSP from %d map bytes as written and %d canonical", name, len(bsps[0]), len(text), len(canonical))
		case bytes.Equal(bsps[2], bsps[1]):
			t.Logf("SAME-AS-UNCOMMENTED %s: the canonical BSP is the one the map compiles to without its comment lines", name)
		case compareBSP(bsps[0], bsps[1]) == "":
			t.Logf("SAME-UP-TO-LEAF-FACE-ORDER %s: every lump equal, each leaf's faces the same set", name)
		default:
			t.Errorf("DIFFERENT %s: the canonical copy compiled to another map: %s", name, compareBSP(bsps[0], bsps[1]))
		}
	}
	t.Logf("compiled %d, refused %d", compiled, refused)
}

// withoutCommentLines drops every line that is only a `//` comment.
func withoutCommentLines(text []byte) []byte {
	var out bytes.Buffer
	for _, line := range strings.SplitAfter(string(text), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "//") {
			out.WriteString(line)
		}
	}
	return out.Bytes()
}

// compareBSP returns "" when two Quake 1 BSPs (BSP29 or BSP2) are equal in
// every lump but `marksurfaces`, and each leaf lists the same faces there in
// some order; otherwise what differs.
func compareBSP(a, b []byte) string {
	const lumps, leafs, marks = 15, 10, 11
	if len(a) < 4+8*lumps || len(b) < 4+8*lumps || !bytes.Equal(a[:4], b[:4]) {
		return "not two BSPs of one version"
	}
	leafSize, markSize := 28, 2
	if string(a[:4]) == "BSP2" {
		leafSize, markSize = 44, 4
	} else if binary.LittleEndian.Uint32(a) != 29 {
		return "not a Quake 1 BSP"
	}
	lump := func(d []byte, index int) []byte {
		offset := int(binary.LittleEndian.Uint32(d[4+8*index:]))
		length := int(binary.LittleEndian.Uint32(d[8+8*index:]))
		if offset < 0 || length < 0 || offset+length > len(d) {
			return nil
		}
		return d[offset : offset+length]
	}
	for index := 0; index < lumps; index++ {
		if index != marks && !bytes.Equal(lump(a, index), lump(b, index)) {
			return fmt.Sprintf("lump %d differs", index)
		}
	}
	leafLump, markA, markB := lump(a, leafs), lump(a, marks), lump(b, marks)
	if len(markA) != len(markB) {
		return "marksurfaces differ in length"
	}
	read := func(d []byte, at int) int {
		if markSize == 2 {
			return int(binary.LittleEndian.Uint16(d[at:]))
		}
		return int(binary.LittleEndian.Uint32(d[at:]))
	}
	for leaf := 0; leaf+leafSize <= len(leafLump); leaf += leafSize {
		first := read(leafLump, leaf+leafSize-4-2*markSize)
		count := read(leafLump, leaf+leafSize-4-markSize)
		if (first+count)*markSize > len(markA) {
			return "a leaf's faces lie outside marksurfaces"
		}
		seen := map[int]int{}
		for at := first; at < first+count; at++ {
			seen[read(markA, at*markSize)]++
			seen[read(markB, at*markSize)]--
		}
		for _, n := range seen {
			if n != 0 {
				return fmt.Sprintf("leaf %d lists other faces", leaf/leafSize)
			}
		}
	}
	return ""
}
