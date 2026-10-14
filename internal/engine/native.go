package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// FaultMissingLibrary: the program is there, and a library it loads at start —
// a Quake II engine's game code or its renderer — is not, or is not one this
// computer can load.
const FaultMissingLibrary Fault = "missing_library"

// nativePart is a file an engine's own release loads from beside its program
// or from the game folder, which a profile has no field for.
//
// It is a table of facts about other people's builds, keyed on the profile's
// `runtime`, on purpose — the same reason `profile`'s upstream spellings are:
// a document member would be a schema change every older Companion refuses,
// for something only setup needs. Each entry was read from the engine's own
// source and then measured by removing the file (`Q2_009`, Yamagi 8.70):
//
//   - no `baseq2/game.so`: the client starts, prints `ERROR: failed to load
//     game DLL` and sits at its console;
//   - no `ref_*.so`: `Error: No usable renderer found!`, exit 1.
type nativePart struct {
	id    string
	title string
	// files are the names to look for, any one of which is enough, by OS.
	files map[string][]string
	// inGameRoot says the game folder is searched as well as the program's.
	inGameRoot bool
	// clientOnly parts are not needed by a program with no window.
	clientOnly bool
	fix        string
}

var nativeParts = map[string][]nativePart{
	"yamagi-quake2": {
		{
			id: "game_library", title: "the game library",
			files: map[string][]string{
				"linux": {"baseq2/game.so"}, "darwin": {"baseq2/game.dylib"}, "windows": {"baseq2/game.dll"},
			},
			inGameRoot: true,
			fix: "Yamagi loads the game library built for this computer from a baseq2 folder beside its program, or from the " +
				"game folder's baseq2. Use the folder of a complete build — upstream's releases and a build from source both " +
				"put it there. The retail game's gamex86.dll is for Windows on 32-bit x86 only.",
		},
		{
			id: "renderer", title: "a renderer",
			files: map[string][]string{
				"linux":   {"ref_gl1.so", "ref_gl3.so", "ref_gles3.so", "ref_soft.so", "ref_vk.so"},
				"darwin":  {"ref_gl1.dylib", "ref_gl3.dylib", "ref_gles3.dylib", "ref_soft.dylib", "ref_vk.dylib"},
				"windows": {"ref_gl1.dll", "ref_gl3.dll", "ref_gles3.dll", "ref_soft.dll", "ref_vk.dll"},
			},
			clientOnly: true,
			fix:        "Yamagi loads its renderers from the folder its program is in. Use the folder of a complete build.",
		},
	},
}

// nativeProblems checks what an engine loads besides its program, and that
// what it loads is for this computer. It starts nothing.
func (c Checker) nativeProblems(document profile.Profile, action profile.Action, local binding.LocalBinding, program string) Problems {
	engine, ok := document.(*profile.EngineProfile)
	if !ok {
		return nil
	}
	parts := nativeParts[engine.Runtime]
	if len(parts) == 0 {
		return nil
	}
	// The engine looks beside the program it really is, through any link:
	// Yamagi reads its own directory from the running executable's path.
	dir := filepath.Dir(program)
	if real, err := filepath.EvalSymlinks(program); err == nil {
		dir = filepath.Dir(real)
	}
	var problems Problems
	for _, part := range parts {
		if part.clientOnly && action.SessionRole == profile.SessionDedicated {
			continue
		}
		names := part.files[c.Platform.OS]
		if len(names) == 0 {
			continue
		}
		dirs := []string{dir}
		if root := local.Roots[profile.RootGame]; part.inGameRoot && root != "" {
			dirs = append(dirs, root)
		}
		var found, wrong []string
		for _, where := range dirs {
			for _, name := range names {
				path := filepath.Join(where, filepath.FromSlash(name))
				if info, err := c.stat(path); err != nil || info.IsDir() {
					continue
				}
				if detail := c.foreign(path); detail != "" {
					wrong = append(wrong, path+" ("+detail+")")
					continue
				}
				found = append(found, path)
			}
		}
		if len(found) > 0 {
			continue
		}
		summary := fmt.Sprintf("%s's %q needs %s — %s — and there is none in %s.", engine.Meta.Name, action.Title,
			part.title, strings.Join(names, " or "), strings.Join(dirs, " or "))
		if len(wrong) > 0 {
			summary = fmt.Sprintf("%s's %q needs %s for this computer, and the one there is not: %s.", engine.Meta.Name,
				action.Title, part.title, strings.Join(wrong, "; "))
		}
		problems = append(problems, Problem{Fault: FaultMissingLibrary, Summary: summary, Fix: part.fix})
	}
	return problems
}

// formatProblems refuses a program built for another computer, read from its
// header: starting it would only say "exec format error", and an engine that
// fails that way in a launch shows nothing at all.
func (c Checker) formatProblems(document profile.Profile, action profile.Action, program string) Problems {
	detail := c.foreign(program)
	if detail == "" {
		return nil
	}
	return Problems{{
		Fault: FaultToolUnusable,
		Summary: fmt.Sprintf("The %s recorded for %s is not a program this computer can run (%s): %s.",
			recordedNoun(document, action), document.Metadata().Name, detail, program),
		Fix: "Choose the build for this operating system and processor.",
	}}
}

// foreign is why a binary file is not for this computer, or "" when it is, or
// when it is not a binary this can read (a script, say).
func (c Checker) foreign(path string) string {
	goos, goarch, err := binaryPlatform(path)
	if err != nil || goos == "" {
		return ""
	}
	if goos != c.Platform.OS || goarch != "" && goarch != c.Platform.Arch {
		built := goos
		if goarch != "" {
			built += "/" + goarch
		}
		return "it is built for " + built + "; this computer is " + c.Platform.String()
	}
	return ""
}

var errNotABinary = errors.New("not a binary")

// binaryPlatform reads the operating system and processor an executable or
// library is built for, from its header alone: an ELF file's `e_machine`, a
// PE file's machine after its signature, a Mach-O file's CPU type. A fat
// Mach-O file is for darwin and whichever of its processors is asked for, so it
// reports no processor; a file that is none of these (a script) reports none.
func binaryPlatform(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	header := make([]byte, 64)
	n, _ := io.ReadFull(file, header)
	header = header[:n]
	switch {
	case n >= 20 && string(header[:4]) == "\x7fELF":
		machine := binary.LittleEndian.Uint16(header[18:])
		if header[5] == 2 {
			machine = binary.BigEndian.Uint16(header[18:])
		}
		arch := map[uint16]string{62: "amd64", 3: "386", 183: "arm64", 40: "arm", 243: "riscv64", 21: "ppc64le",
			22: "s390x", 258: "loong64"}[machine]
		goos := "linux"
		if header[7] == 9 {
			goos = "freebsd"
		}
		return goos, arch, nil
	case n >= 0x40 && header[0] == 'M' && header[1] == 'Z':
		offset := int64(binary.LittleEndian.Uint32(header[0x3c:]))
		signature := make([]byte, 6)
		if _, err := file.ReadAt(signature, offset); err != nil || string(signature[:4]) != "PE\x00\x00" {
			return "windows", "", nil
		}
		arch := map[uint16]string{0x8664: "amd64", 0x14c: "386", 0xaa64: "arm64"}[binary.LittleEndian.Uint16(signature[4:])]
		return "windows", arch, nil
	case n >= 4 && (string(header[:4]) == "\xca\xfe\xba\xbe" || string(header[:4]) == "\xbe\xba\xfe\xca"):
		return "darwin", "", nil
	case n >= 8 && (string(header[:4]) == "\xcf\xfa\xed\xfe" || string(header[:4]) == "\xce\xfa\xed\xfe"):
		arch := map[uint32]string{0x01000007: "amd64", 0x0100000c: "arm64", 7: "386"}[binary.LittleEndian.Uint32(header[4:])]
		return "darwin", arch, nil
	}
	return "", "", errNotABinary
}

// programPath is where the program an action starts is on this machine, the
// way engineProblems finds it, or "" when nothing says.
func (c Checker) programPath(document profile.Profile, action profile.Action, local binding.LocalBinding) string {
	if path := local.Executables[action.Executable]; path != "" {
		return path
	}
	root := local.Roots[profile.RootToolInstall]
	file := executableFile(document, action.Executable)
	if root == "" || file == "" {
		return ""
	}
	return filepath.Join(root, filepath.FromSlash(strings.ReplaceAll(file, "{platform.exe_suffix}", c.Platform.ExeSuffix())))
}
