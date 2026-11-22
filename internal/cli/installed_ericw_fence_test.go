package cli

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle/fixturewad"
)

// TestInstalledEricwBuildsAFenceRoomFromAutoPigeon is NEW_643's user path on a
// real compiler (auto-pigeon-libraries#15): the synthetic fence room is turned
// into an APMap by the converter built from this commit — the direction the old
// library pin refused — served as an exact AUB revision, and built by `companion
// build run` through the built-in Quake 1 pipeline, with its two WADs in the
// approved texture folder. Every stage must succeed, and the BSP must carry the
// fence texture, its pixels read from the fence WAD, on the slab's faces. The
// same room with an ordinary slab is the control.
//
// Skipped unless AUCOM_ERICW_BIN names the folder holding qbsp, vis and light:
//
//	AUCOM_ERICW_BIN=/path/auto-pigeon-ericw-tools-1.4556-linux-amd64/bin \
//	    go test ./internal/cli -run InstalledEricwBuildsAFenceRoom -v
func TestInstalledEricwBuildsAFenceRoomFromAutoPigeon(t *testing.T) {
	bin := os.Getenv("AUCOM_ERICW_BIN")
	if bin == "" {
		t.Skip("AUCOM_ERICW_BIN is not set; no installed compiler is exercised")
	}
	converterPath, err := realMapio()
	if err != nil {
		t.Fatal(err)
	}
	useRealConverter(t)
	wads := t.TempDir()
	for name, data := range fixturewad.FenceRoomWADs() {
		if err := os.WriteFile(filepath.Join(wads, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	executables := map[string]string{}
	for _, name := range []string{"qbsp", "vis", "light"} {
		executables[name] = filepath.Join(bin, name+suffix)
	}

	for _, texture := range []string{fixturewad.FenceRoomFence, fixturewad.FenceRoomWall} {
		stem := strings.TrimPrefix(texture, "{") + "_room"
		source := filepath.Join(t.TempDir(), stem+".map")
		if err := os.WriteFile(source, []byte(fixturewad.FenceRoomMap(texture)), 0o600); err != nil {
			t.Fatal(err)
		}
		out := t.TempDir()
		if output, err := exec.Command(converterPath, "convert", "--q1map-to-apmap",
			"--input", source, "--output", out).CombinedOutput(); err != nil {
			t.Fatalf("%s: q1map-to-apmap: %v\n%s", texture, err, output)
		}
		document, err := os.ReadFile(filepath.Join(out, stem+".apmap"))
		if err != nil {
			t.Fatal(err)
		}

		backend := fakeCompanionAUBServing(t, stem+".apmap", "application/json", string(document))
		env, _, _ := signedInEnv(t, backend.URL)
		bindTool(t, env, builtin.EricwAutoPigeonQ1, executables, map[string]string{profile.RootContent: wads})
		runEnv, runOut, runErr := testEnvAt(t, env.ConfigPath)
		if code := Run(runEnv, []string{"build", "run", "--json", "--pipeline", builtin.Q1Normal,
			"--input", "source_map=aub:map/map0000000000001@rev0000000000002#" + stem + ".apmap"}); code != 0 {
			t.Fatalf("%s: build run exit %d\nstdout: %s\nstderr: %s", texture, code, runOut, runErr)
		}
		var manifest build.Manifest
		if err := json.Unmarshal(manifestJSON(t, runOut.String()), &manifest); err != nil {
			t.Fatalf("%v\n%s", err, runOut)
		}
		if manifest.State != "succeeded" {
			t.Fatalf("%s: the build is %s: %s", texture, manifest.State, manifest.Error)
		}
		input := manifest.Inputs[0]
		if input.Conversion == nil || input.Conversion.Direction != "apmap-to-q1map" {
			t.Fatalf("%s: the input was not converted from the APMap: %+v", texture, input)
		}
		var bspPath string
		for _, step := range manifest.Steps {
			command := ""
			if step.Command != nil {
				command = step.Command.Executable + " " + strings.Join(step.Command.Args, " ")
			}
			t.Logf("%s: step %s %s exit=%v: %s", texture, step.ID, step.State, exitOf(step.ExitCode), command)
			if step.State != "succeeded" {
				t.Fatalf("%s: step %s is %s: %s", texture, step.ID, step.State, step.Error)
			}
			for _, output := range step.Outputs {
				if output.Role == "q1.bsp" && output.Path != "" {
					bspPath = output.Path
				}
			}
		}
		if bspPath == "" {
			t.Fatalf("%s: no step produced a BSP", texture)
		}
		data, err := os.ReadFile(bspPath)
		if err != nil {
			t.Fatal(err)
		}
		faces, embedded, firstPixel, err := bspTextureFaces(data, fixturewad.FenceRoomFence)
		if err != nil {
			t.Fatalf("%s: %v", texture, err)
		}
		t.Logf("%s: BSP %d bytes, %d faces textured %s, embedded=%v, first pixel %d; staged %s (%d bytes, sha256 %s)",
			texture, len(data), faces, fixturewad.FenceRoomFence, embedded, firstPixel, input.Path, input.Size, input.SHA256)
		if texture == fixturewad.FenceRoomFence {
			if faces == 0 || !embedded || firstPixel != 255 {
				t.Fatalf("the BSP does not carry the fence: %d faces, embedded=%v, first pixel %d",
					faces, embedded, firstPixel)
			}
		} else if faces != 0 {
			t.Fatalf("the control's BSP has %d %s faces", faces, fixturewad.FenceRoomFence)
		}
	}
}

func exitOf(code *int) any {
	if code == nil {
		return "-"
	}
	return *code
}

// bspTextureFaces reads a Quake 1 BSP (BSP29 or BSP2) and returns how many
// faces use the miptex called name, whether that miptex carries its pixels
// (qbsp found it in a WAD), and the first of those pixels.
func bspTextureFaces(data []byte, name string) (faces int, embedded bool, firstPixel int, err error) {
	le := binary.LittleEndian
	if len(data) < 4+15*8 {
		return 0, false, 0, errShortBSP
	}
	faceSize, texinfoAt := 20, 10
	switch version := le.Uint32(data); version {
	case 29:
	case 0x32505342: // "BSP2"
		faceSize, texinfoAt = 28, 16
	default:
		return 0, false, 0, errUnknownBSP
	}
	lump := func(index int) []byte {
		offset, length := le.Uint32(data[4+index*8:]), le.Uint32(data[8+index*8:])
		if uint64(offset)+uint64(length) > uint64(len(data)) {
			return nil
		}
		return data[offset : offset+length]
	}
	textures, texinfo, faceLump := lump(2), lump(6), lump(7)
	if len(textures) < 4 {
		return 0, false, 0, errShortBSP
	}
	count := int(le.Uint32(textures))
	wanted := -1
	for index := 0; index < count && 4+index*4+4 <= len(textures); index++ {
		offset := int32(le.Uint32(textures[4+index*4:]))
		if offset < 0 || int(offset)+40 > len(textures) {
			continue
		}
		header := textures[offset:]
		if string(bytes.TrimRight(header[:16], "\x00")) != name {
			continue
		}
		wanted = index
		if pixels := le.Uint32(header[24:]); pixels != 0 && int(offset)+int(pixels) < len(textures) {
			embedded, firstPixel = true, int(textures[int(offset)+int(pixels)])
		}
	}
	if wanted < 0 {
		return 0, false, 0, nil
	}
	for at := 0; at+faceSize <= len(faceLump); at += faceSize {
		var info int
		if faceSize == 20 {
			info = int(le.Uint16(faceLump[at+texinfoAt:]))
		} else {
			info = int(le.Uint32(faceLump[at+texinfoAt:]))
		}
		if info*40+40 <= len(texinfo) && int(le.Uint32(texinfo[info*40+32:])) == wanted {
			faces++
		}
	}
	return faces, embedded, firstPixel, nil
}

type bspError string

func (e bspError) Error() string { return string(e) }

const (
	errShortBSP   = bspError("the BSP is too short to hold its lumps")
	errUnknownBSP = bspError("the file is not a Quake 1 BSP (29 or BSP2)")
)
