package engine

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// elfHeader is a 64-bit little-endian ELF header for a processor, and nothing
// else: enough for a reader of headers, and no program at all.
func elfHeader(machine uint16) []byte {
	header := make([]byte, 64)
	copy(header, "\x7fELF")
	header[4], header[5], header[6] = 2, 1, 1
	binary.LittleEndian.PutUint16(header[16:], 3)
	binary.LittleEndian.PutUint16(header[18:], machine)
	binary.LittleEndian.PutUint32(header[20:], 1)
	binary.LittleEndian.PutUint16(header[52:], 64)
	binary.LittleEndian.PutUint16(header[54:], 56)
	binary.LittleEndian.PutUint16(header[58:], 64)
	return header
}

// peHeader is a DOS stub pointing at a PE header for x86-64 with no sections.
func peHeader() []byte {
	out := make([]byte, 0x40)
	out[0], out[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(out[0x3c:], 0x40)
	out = append(out, 'P', 'E', 0, 0)
	coff := make([]byte, 20)
	binary.LittleEndian.PutUint16(coff[0:], 0x8664)
	return append(out, coff...)
}

type yamagiSetup struct {
	program, gameRoot string
	local             binding.LocalBinding
}

// newYamagi is a Yamagi folder the way a build of it is laid out — the
// program, a renderer, and `baseq2/game.so` beside it — and a game folder
// holding `baseq2/pak0.pak`. Every file is a stand-in: what is checked is
// where things are and what they are built for.
func newYamagi(t *testing.T) yamagiSetup {
	t.Helper()
	root := t.TempDir()
	install := filepath.Join(root, "yamagi")
	game := filepath.Join(root, "Quake II")
	files := map[string][]byte{
		filepath.Join(install, "quake2"):                 []byte("#!/bin/sh\n"),
		filepath.Join(install, "ref_soft.so"):            elfHeader(62),
		filepath.Join(install, "baseq2", "game.so"):      elfHeader(62),
		filepath.Join(game, "baseq2", "pak0.pak"):        []byte("PACK"),
		filepath.Join(root, "content", "placeholder.md"): []byte("x"),
	}
	for path, data := range files {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return yamagiSetup{program: filepath.Join(install, "quake2"), gameRoot: game, local: binding.LocalBinding{
		ProfileID:   builtin.YamagiQuake2,
		Executables: map[string]string{"engine": filepath.Join(install, "quake2"), "dedicated": filepath.Join(install, "quake2")},
		Roots:       map[string]string{profile.RootGame: game, profile.RootContent: filepath.Join(root, "content")},
	}}
}

func checkYamagi(t *testing.T, setup yamagiSetup, action string) Problems {
	t.Helper()
	entry, err := builtin.Find(builtin.YamagiQuake2)
	if err != nil {
		t.Fatal(err)
	}
	return Checker{Platform: profile.Platform{OS: "linux", Arch: "amd64"}}.Check(entry.Profile, entry.Trust(), entry.Digest, setup.local, action)
}

func TestACompleteYamagiFolderIsReady(t *testing.T) {
	if problems := checkYamagi(t, newYamagi(t), profile.ActionPlayMap); len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
}

func TestAYamagiSetupMissingWhatTheEngineLoadsIsNamedNotStarted(t *testing.T) {
	for _, c := range []struct {
		name, want string
		fault      Fault
		break_     func(s yamagiSetup)
	}{
		{"a baseq2 with no game in it", "holds no baseq2/pak0.pak or baseq2/pics/colormap.pcx", FaultMissingGameData, func(s yamagiSetup) {
			os.Remove(filepath.Join(s.gameRoot, "baseq2", "pak0.pak"))
			os.WriteFile(filepath.Join(s.gameRoot, "baseq2", "readme.txt"), []byte("not a game"), 0o644)
		}},
		{"no game library", "needs the game library — baseq2/game.so — and there is none", FaultMissingLibrary, func(s yamagiSetup) {
			os.Remove(filepath.Join(filepath.Dir(s.program), "baseq2", "game.so"))
		}},
		{"the retail Windows game library only", "needs the game library", FaultMissingLibrary, func(s yamagiSetup) {
			os.Remove(filepath.Join(filepath.Dir(s.program), "baseq2", "game.so"))
			os.WriteFile(filepath.Join(s.gameRoot, "baseq2", "gamex86.dll"), peHeader(), 0o644)
		}},
		{"a game library for another processor", "built for linux/arm64; this computer is linux/amd64", FaultMissingLibrary, func(s yamagiSetup) {
			os.WriteFile(filepath.Join(filepath.Dir(s.program), "baseq2", "game.so"), elfHeader(183), 0o644)
		}},
		{"no renderer", "needs a renderer — ref_gl1.so or", FaultMissingLibrary, func(s yamagiSetup) {
			os.Remove(filepath.Join(filepath.Dir(s.program), "ref_soft.so"))
		}},
		{"a program for another processor", "is not a program this computer can run (it is built for linux/arm64", FaultToolUnusable, func(s yamagiSetup) {
			os.WriteFile(s.program, elfHeader(183), 0o755)
		}},
		{"a Windows program", "it is built for windows/amd64", FaultToolUnusable, func(s yamagiSetup) {
			os.WriteFile(s.program, peHeader(), 0o755)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			setup := newYamagi(t)
			c.break_(setup)
			problems := checkYamagi(t, setup, profile.ActionPlayMap)
			if !problems.Has(c.fault) || !strings.Contains(problems.Error(), c.want) {
				t.Fatalf("problems: %v", problems)
			}
		})
	}
}

func TestTheGameLibraryInTheGameFolderIsEnoughAndTheServerNeedsNoRenderer(t *testing.T) {
	setup := newYamagi(t)
	install := filepath.Dir(setup.program)
	os.Rename(filepath.Join(install, "baseq2", "game.so"), filepath.Join(setup.gameRoot, "baseq2", "game.so"))
	if problems := checkYamagi(t, setup, profile.ActionPlayMap); len(problems) != 0 {
		t.Fatalf("game.so in the game folder: %v", problems)
	}
	os.Remove(filepath.Join(install, "ref_soft.so"))
	if problems := checkYamagi(t, setup, "host_dedicated"); len(problems) != 0 {
		t.Fatalf("the dedicated server and no renderer: %v", problems)
	}
	if problems := checkYamagi(t, setup, profile.ActionPlayMap); !problems.Has(FaultMissingLibrary) {
		t.Fatalf("the client and no renderer: %v", problems)
	}
}

func TestYamagisClientIsFoundUnderTheNameItsBuildGivesIt(t *testing.T) {
	entry, err := builtin.Find(builtin.YamagiQuake2)
	if err != nil {
		t.Fatal(err)
	}
	engine := entry.Profile.(*profile.EngineProfile)
	for _, executable := range engine.Executables {
		if executable.Name != "engine" {
			continue
		}
		linux := executable.FileCandidates(profile.Platform{OS: "linux", Arch: "amd64"})
		windows := executable.FileCandidates(profile.Platform{OS: "windows", Arch: "amd64"})
		if strings.Join(linux, ",") != "yquake2,quake2" || strings.Join(windows, ",") != "yquake2.exe" {
			t.Errorf("candidates: linux %v, windows %v", linux, windows)
		}
	}
}

func TestBaseq2IsNeverAStagingTarget(t *testing.T) {
	for _, name := range []string{"baseq2", "BASEQ2", "xatrix", "ctf"} {
		if err := CheckModName(name); err == nil {
			t.Errorf("%q was accepted as a mod folder", name)
		}
	}
}
