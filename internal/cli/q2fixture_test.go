package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mustExecutable is this test binary, which is what every fixture in this
// package is started as.
func mustExecutable(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary: %v", err)
	}
	return self
}

// A stand-in for ericw-tools 2.x in its Quake II mode.
//
// # Why the behaviours below are the ones it has
//
// Every one was measured by running 2.0.0-alpha7 on Linux against a synthetic
// Quake II map while `AUP/AUCOM 215` was written. They are here because they
// are the behaviours the wiring has to survive, and each of them differs from
// the qualified Quake 1 toolchain in a way a copy-and-edit would have got
// wrong:
//
//   - `qbsp` writes the portal file, the extended texinfo document and its log
//     beside the OUTPUT it was given, named after the destination's stem and
//     not the source's;
//   - `vis` opens the `.prt` of the same stem in the directory it was handed
//     its input in, fails loudly when it is not there, and writes `<stem>-vis.log`
//     rather than `vis.log`;
//   - `light` reads `<stem>.texinfo.json` back and says so, writes
//     `<stem>-light.log`, and writes NO `.lit` — Quake II lightmaps live inside
//     the BSP;
//   - all three want `-basedir` and `-gamedir`, and warn about the textures they
//     cannot find rather than refusing.
//
// A fixture that took every file as an argument would pass whatever the staging
// rules were and would have told us nothing.
//
// It is this test binary, invoked through a link named after the program it is
// standing in for — see `linkAsTool`. Three programs, because the profile
// declares three, and a single binary that could not tell which one it was
// being asked to be would not be able to reproduce the difference between them.

// q2ToolNames are the programs the fixture can be.
var q2ToolNames = []string{"qbsp", "vis", "light"}

func isQ2ToolName(name string) bool {
	for _, candidate := range q2ToolNames {
		if candidate == name {
			return true
		}
	}
	return false
}

// q2ToolMain is the fixture's entry point, dispatched from TestMain on the name
// the binary was invoked under.
func q2ToolMain(name string, args []string) int {
	switch name {
	case "qbsp":
		return fixtureQBSP(args)
	case "vis":
		return fixtureVis(args)
	case "light":
		return fixtureLight(args)
	}
	fmt.Fprintf(os.Stderr, "fixture: %q is not one of %s\n", name, strings.Join(q2ToolNames, ", "))
	return 2
}

// q2Args is what the three programs have in common, parsed the way they parse
// it: switches, then positional arguments.
type q2Args struct {
	baseDir  string
	gameDir  string
	leakTest bool
	format   string
	threads  string
	rest     []string
}

func parseQ2Args(args []string) q2Args {
	parsed := q2Args{format: "q2bsp"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-nocolor", "-verbose", "-quiet", "-notex", "-fast", "-soft", "-extra", "-extra4",
			"-noambient", "-nolights":
			// Accepted and ignored: the fixture measures wiring, not lighting.
		case "-q2bsp":
			parsed.format = "q2bsp"
		case "-qbism":
			parsed.format = "qbism"
		case "-leaktest":
			parsed.leakTest = true
		case "-basedir", "-gamedir", "-threads", "-level", "-gate", "-subdivide", "-maxedges", "-filepriority":
			if i+1 >= len(args) {
				continue
			}
			value := args[i+1]
			switch args[i] {
			case "-basedir":
				parsed.baseDir = value
			case "-gamedir":
				parsed.gameDir = value
			case "-threads":
				parsed.threads = value
			}
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(os.Stderr, "fixture: unknown option %q\n", args[i])
				return parsed
			}
			parsed.rest = append(parsed.rest, args[i])
		}
	}
	return parsed
}

// textureWarnings prints what the real compiler prints when the bound game data
// does not hold a texture the map names. The map source carries one texture
// name per `aucom/` token, which is enough for the fixture to be specific.
func textureWarnings(source string, baseDir, gameDir string) {
	body, err := os.ReadFile(source)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, field := range strings.Fields(string(body)) {
		if !strings.HasPrefix(field, "aucom/") || seen[field] {
			continue
		}
		seen[field] = true
		found := false
		for _, root := range []string{gameDir, baseDir} {
			if root == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, "textures", field+".wal")); err == nil {
				found = true
				break
			}
		}
		if !found {
			fmt.Printf("WARNING: Couldn't locate texture for %s\n", field)
		}
	}
}

func fixtureQBSP(args []string) int {
	parsed := parseQ2Args(args)
	if len(parsed.rest) < 2 {
		fmt.Fprintln(os.Stderr, "fixture qbsp: needs a source and a destination")
		return 2
	}
	source, destination := parsed.rest[0], parsed.rest[1]
	fmt.Println("---- qbsp / ericw-tools 2.0.0-alpha7 ----")
	if parsed.baseDir == "" {
		fmt.Println("WARNING: failed to find basedir")
	}
	if _, err := os.Stat(filepath.Join(parsed.baseDir, "pics", "colormap.pcx")); err != nil {
		fmt.Println("LoadPCXPalette: Failed to load 'pics/colormap.pcx'.")
		fmt.Println("INFO: using built-in palette.")
	}
	textureWarnings(source, parsed.baseDir, parsed.gameDir)

	body, err := os.ReadFile(source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "************ ERROR ************\n%v\n", err)
		return 1
	}
	// Everything is named after the destination's stem, which is what was
	// measured: a run with `source.map level.bsp` writes `level.prt`, never
	// `source.prt`.
	stem := strings.TrimSuffix(destination, filepath.Ext(destination))
	if strings.Contains(string(body), "func_areaportal") {
		fmt.Println("---- EmitAreaPortals ----")
		fmt.Println("    1 AREAPORTAL brushes")
	}
	if strings.Contains(string(body), "aucom-leak") {
		fmt.Println(`WARNING: Reached occupant "info_player_start" at (128 128 32), no filling performed.`)
		if err := os.WriteFile(stem+".pts", []byte("0 0 0\n"), 0o600); err != nil {
			return 1
		}
		fmt.Printf("Leak file written to %s.pts\n", stem)
		fmt.Printf("            1 portals written to %s.leak.prt\n", stem)
		if err := os.WriteFile(stem+".leak.prt", []byte("PRT1\n1\n0\n"), 0o600); err != nil {
			return 1
		}
		if parsed.leakTest {
			fmt.Println("Aborting because -leaktest was used.")
			return 1
		}
	}
	ident := "IBSP"
	if parsed.format == "qbism" {
		ident = "QBSP"
	}
	if err := os.WriteFile(destination, []byte(ident+":38 compiled from "+filepath.Base(source)+"\n"), 0o600); err != nil {
		return 1
	}
	if err := os.WriteFile(stem+".prt", []byte("PRT1\n2\n0\n"), 0o600); err != nil {
		return 1
	}
	// The extended texinfo document, which `light` reads back. Its absence is
	// silent in the real tool, which is exactly why the pipeline wires it.
	if err := os.WriteFile(stem+".texinfo.json", []byte("{}\n"), 0o600); err != nil {
		return 1
	}
	if err := os.WriteFile(stem+".log", []byte("qbsp fixture log\n"), 0o600); err != nil {
		return 1
	}
	fmt.Printf("Writing %s as Quake II BSP %s:38\n", destination, ident)
	return 0
}

func fixtureVis(args []string) int {
	parsed := parseQ2Args(args)
	if len(parsed.rest) < 1 {
		fmt.Fprintln(os.Stderr, "fixture vis: needs a BSP")
		return 2
	}
	bsp := parsed.rest[0]
	stem := strings.TrimSuffix(bsp, filepath.Ext(bsp))
	fmt.Println("---- vis / ericw-tools 2.0.0-alpha7 ----")
	portals, err := os.ReadFile(stem + ".prt")
	if err != nil {
		// The measured failure: vis opens the portal file by name, in the
		// directory it was handed the BSP in, and stops when it is not there.
		fmt.Printf("************ ERROR ************\nLoadPrtFile: unknown header/empty portal file %s.prt\n", stem)
		return 1
	}
	if !strings.HasPrefix(string(portals), "PRT1") {
		fmt.Printf("************ ERROR ************\nLoadPrtFile: unknown header/empty portal file %s.prt\n", stem)
		return 1
	}
	if err := appendTo(bsp, "vis\n"); err != nil {
		return 1
	}
	fmt.Println("     2 clusters")
	fmt.Println("visdatasize:2  compressed from 2")
	// Quake II's second pass, which Quake 1 does not have.
	fmt.Println("---- CalcPHS ----")
	fmt.Println("Average clusters hearable: 1")
	if err := os.WriteFile(stem+"-vis.log", []byte("vis fixture log\n"), 0o600); err != nil {
		return 1
	}
	return 0
}

func fixtureLight(args []string) int {
	parsed := parseQ2Args(args)
	if len(parsed.rest) < 1 {
		fmt.Fprintln(os.Stderr, "fixture light: needs a BSP")
		return 2
	}
	bsp := parsed.rest[0]
	stem := strings.TrimSuffix(bsp, filepath.Ext(bsp))
	fmt.Println("---- light / ericw-tools 2.0.0-alpha7 ----")
	if _, err := os.Stat(stem + ".texinfo.json"); err == nil {
		fmt.Printf("Loading extended texinfo flags from %s.texinfo.json...\n", stem)
	}
	if err := appendTo(bsp, "light\n"); err != nil {
		return 1
	}
	fmt.Println("lightmap size (total): 9408")
	fmt.Println("0 empty lightmaps")
	if err := os.WriteFile(stem+"-light.log", []byte("light fixture log\n"), 0o600); err != nil {
		return 1
	}
	// And deliberately no `.lit`: asked for one, the real tool prints a line
	// and writes nothing.
	return 0
}

func appendTo(path, text string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.WriteString(file, text)
	return err
}

// linkAsTool puts this test binary into `dir` under the name of one of the
// programs the profile declares, so the fixture can tell which one it is being
// asked to be.
//
// A symlink where the platform has them and a copy where it does not. The copy
// is not a fallback in the sense this repository dislikes — both produce a file
// at the path the profile resolves, and the difference is invisible to
// everything downstream.
func linkAsTool(self, dir, name, exeSuffix string) (string, error) {
	target := filepath.Join(dir, name+exeSuffix)
	if err := os.Symlink(self, target); err == nil {
		return target, nil
	} else if !errors.Is(err, os.ErrExist) {
		source, openErr := os.Open(self)
		if openErr != nil {
			return "", openErr
		}
		defer source.Close()
		destination, createErr := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if createErr != nil {
			return "", createErr
		}
		if _, copyErr := io.Copy(destination, source); copyErr != nil {
			destination.Close()
			return "", copyErr
		}
		if closeErr := destination.Close(); closeErr != nil {
			return "", closeErr
		}
	}
	return target, nil
}
