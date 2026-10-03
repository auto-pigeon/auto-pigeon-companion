package cli

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A stand-in for Q3Map2 in the three stages this build drives it in.
//
// # Why the behaviours below are the ones it has
//
// Every one was measured by running Q3Map2 2.5.17n-git-68ecbed on Linux against
// a synthetic Quake III map while `AUP/AUCOM 216` was written. They are here
// because they are the behaviours the wiring has to survive, and each differs
// from both EricW toolchains in a way a copy-and-edit would have got wrong:
//
//   - there is no output argument at all. `-bsp` writes `<stem>.bsp`,
//     `<stem>.prt` and `<stem>.srf` BESIDE THE INPUT it was given;
//   - `-vis` opens `<stem>.prt` beside the BSP, and DELETES it unless
//     `-saveprt` was passed;
//   - `-light` refuses to start without BOTH `<stem>.srf` and `<stem>.map`
//     beside the BSP, and says `Script file … was not found`;
//   - a leak with `-leaktest` writes `<stem>.lin`, writes no BSP, and EXITS 0;
//   - a texture it cannot find under any `-fs_basepath` is a warning and exit 0.
//
// It is one program with a stage switch, not three programs — which is itself
// the shape being tested, because the Quake II fixture is three.
//
// It is this test binary, invoked through a link named `q3map2`; see
// `linkAsTool`, which the Quake II fixture already has.

// q3ToolName is the program the fixture can be.
const q3ToolName = "q3map2"

// q3Args is what the fixture read out of its command line.
type q3Args struct {
	stage     string
	basePaths []string
	homePath  string
	source    string
	leaktest  bool
	saveprt   bool
	game      string
}

func parseQ3Args(args []string) q3Args {
	parsed := q3Args{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-bsp", "-vis", "-light":
			parsed.stage = strings.TrimPrefix(args[i], "-")
		case "-leaktest":
			parsed.leaktest = true
		case "-saveprt":
			parsed.saveprt = true
		case "-fs_basepath":
			if i+1 < len(args) {
				parsed.basePaths = append(parsed.basePaths, args[i+1])
				i++
			}
		case "-fs_homepath":
			if i+1 < len(args) {
				parsed.homePath = args[i+1]
				i++
			}
		case "-game", "-fs_game", "-threads", "-samplesize", "-samples", "-bounce":
			i++ // a switch with a value this fixture does not act on
			if args[i-1] == "-game" && i < len(args) {
				parsed.game = args[i]
			}
		default:
			if !strings.HasPrefix(args[i], "-") {
				parsed.source = args[i]
			}
		}
	}
	return parsed
}

// q3ToolMain is the fixture's entry point, dispatched from TestMain on the name
// the binary was invoked under.
func q3ToolMain(args []string) int {
	parsed := parseQ3Args(args)
	fmt.Println("2.5.17n-git-68ecbed")
	fmt.Println("Q3Map (ydnar) - v2.5.17n-git-68ecbed")
	if parsed.homePath != "" {
		fmt.Printf("VFS Init: %s\n", filepath.Join(parsed.homePath, "baseq3"))
	}
	for _, base := range parsed.basePaths {
		fmt.Printf("VFS Init: %s\n", filepath.Join(base, "baseq3"))
	}
	if parsed.source == "" {
		fmt.Fprintln(os.Stderr, "************ ERROR ************")
		fmt.Fprintln(os.Stderr, "Usage: q3map2 [general options] [options] mapfile")
		return 1
	}
	switch parsed.stage {
	case "bsp":
		return fixtureQ3BSP(parsed)
	case "vis":
		return fixtureQ3Vis(parsed)
	case "light":
		return fixtureQ3Light(parsed)
	}
	fmt.Fprintln(os.Stderr, "************ ERROR ************")
	fmt.Fprintf(os.Stderr, "no stage selected\n")
	return 1
}

// stem is the path with its extension removed — how Q3Map2 finds every file it
// works with.
func stem(path string) string { return strings.TrimSuffix(path, filepath.Ext(path)) }

func fixtureQ3BSP(parsed q3Args) int {
	source, err := os.ReadFile(parsed.source)
	if err != nil {
		fmt.Fprintln(os.Stderr, "************ ERROR ************")
		fmt.Fprintf(os.Stderr, "Script file %s was not found: No such file or directory\n", parsed.source)
		return 1
	}
	fmt.Printf("entering %s\n", parsed.source)
	q3TextureWarnings(string(source), parsed.basePaths)

	base := stem(parsed.source)
	// A leak: measured, Q3Map2 writes the line file, writes no BSP, and exits
	// zero. The build fails because a required output is not there, which is
	// the property the acceptance checks.
	if strings.Contains(string(source), "aucom_leak_me") {
		fmt.Println("******* leaked *******")
		fmt.Println("Entity 4, Brush 0: Entity leaked")
		// The measured shape: from outside the map to the entity reached.
		if err := os.WriteFile(base+".lin", []byte("280.000000 136.000000 128.000000\n256.000000 128.000000 128.000000\n0.000000 0.000000 192.000000\n"), 0o644); err != nil {
			return 1
		}
		if parsed.leaktest {
			fmt.Println("--- MAP LEAKED, ABORTING LEAKTEST ---")
			return 0
		}
	}
	// Measured (Q3_010): a `misc_model` whose file is absent is this line, then
	// every output as usual, then exit status 0.
	if strings.Contains(string(source), "aucom_missing_model") {
		fmt.Println(`ERROR: Unable to open file "models/aucom/nothere.md3".`)
	}
	// Measured (Q3_018): a map with nothing standing in open space floods
	// nothing, prints the same banner, names no entity and writes no line file.
	if strings.Contains(string(source), "aucom_no_occupant") {
		fmt.Println("******* leaked *******")
		if parsed.leaktest {
			fmt.Println("--- MAP LEAKED, ABORTING LEAKTEST ---")
			return 0
		}
	}
	fmt.Println("       28 leafs filled")
	if err := os.WriteFile(base+".prt", []byte(fixtureQ3Portals), 0o644); err != nil {
		return 1
	}
	fmt.Printf("writing %s\n", base+".prt")
	surfaces := "default\n{\n\tcastShadows 1\n}\n"
	if strings.Contains(string(source), "aucom_empty_srf") {
		// Not something Q3Map2 was seen to do. It stands for the file that is
		// present and wrong — a full disk, an interrupted write — which the
		// REAL `-light` was measured to accept without a word.
		surfaces = ""
	}
	if err := os.WriteFile(base+".srf", []byte(surfaces), 0o644); err != nil {
		return 1
	}
	fmt.Printf("Writing %s\n", base+".srf")
	if err := os.WriteFile(base+".bsp", fixtureQ3BSPBytes(), 0o644); err != nil {
		return 1
	}
	fmt.Printf("Writing %s\n", base+".bsp")
	fmt.Println("        0 seconds elapsed")
	return 0
}

// fixtureQ3Portals is a portal file the way Q3Map2 lays one out: the header,
// the cluster, portal and face counts, then a line for each portal and face.
const fixtureQ3Portals = "PRT1\n2\n1\n1\n" +
	"4 0 1 0 (0 0 0 ) (0 0 256 ) (0 256 256 ) (0 256 0 ) \n" +
	"4 0 (0 0 0 ) (0 0 256 ) (0 256 256 ) (0 256 0 ) \n"

// fixtureQ3BSPBytes is the smallest file that is structurally a Quake III BSP:
// `IBSP`, version 46, seventeen lumps that lie inside the file, an entity
// string and one model. Since Q3_010 a declared output is READ as what its role
// says, so a placeholder that merely began with the magic is refused — which is
// the point, and is why the fixture now writes the container properly. Later
// stages append to it, as the real ones grow it; the lump table stays valid.
func fixtureQ3BSPBytes() []byte {
	const header = 8 + 17*8
	entities := []byte("{\n\"classname\" \"worldspawn\"\n}\n\x00")
	models := make([]byte, 40)
	out := make([]byte, header, header+len(entities)+len(models))
	copy(out, "IBSP")
	binary.LittleEndian.PutUint32(out[4:], 46)
	end := uint32(header + len(entities) + len(models))
	for lump := 0; lump < 17; lump++ {
		offset, length := end, uint32(0)
		switch lump {
		case 0:
			offset, length = header, uint32(len(entities))
		case 7:
			offset, length = uint32(header+len(entities)), uint32(len(models))
		}
		binary.LittleEndian.PutUint32(out[8+lump*8:], offset)
		binary.LittleEndian.PutUint32(out[12+lump*8:], length)
	}
	out = append(out, entities...)
	return append(out, models...)
}

func fixtureQ3Vis(parsed q3Args) int {
	base := stem(parsed.source)
	if _, err := os.Stat(base + ".prt"); err != nil {
		fmt.Fprintln(os.Stderr, "************ ERROR ************")
		fmt.Fprintf(os.Stderr, "Couldn't open %s\n", base+".prt")
		return 1
	}
	fmt.Printf("Loading %s\n", parsed.source)
	fmt.Printf("Loading %s\n", base+".prt")
	fmt.Println("     3 portalclusters")
	fmt.Println("visdatasize:32")
	if err := appendTo(parsed.source, "vised\n"); err != nil {
		return 1
	}
	fmt.Printf("Writing %s\n", parsed.source)
	if !parsed.saveprt {
		// Measured. Without -saveprt the portal file is gone afterwards, which
		// is why the profile always sends it.
		_ = os.Remove(base + ".prt")
	}
	return 0
}

func fixtureQ3Light(parsed q3Args) int {
	base := stem(parsed.source)
	fmt.Printf("Loading %s\n", parsed.source)
	// Measured: the surface file first, then the map source, and either one
	// missing is the same refusal.
	for _, companion := range []string{base + ".srf", base + ".map"} {
		if _, err := os.Stat(companion); err != nil {
			fmt.Fprintln(os.Stderr, "************ ERROR ************")
			fmt.Fprintf(os.Stderr, "Script file %s was not found: No such file or directory\n", companion)
			return 1
		}
		fmt.Printf("entering %s\n", companion)
	}
	if err := appendTo(parsed.source, "lit\n"); err != nil {
		return 1
	}
	fmt.Println("     1 total lightmaps")
	fmt.Printf("Writing %s\n", parsed.source)
	return 0
}

// q3TextureWarnings prints the warning Q3Map2 prints for a shader it cannot
// find an image for — the one that is a warning and not a refusal.
func q3TextureWarnings(source string, basePaths []string) {
	seen := map[string]bool{}
	for _, line := range strings.Split(source, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.HasPrefix(strings.TrimSpace(line), "(") {
			continue
		}
		name := ""
		closed := 0
		for _, field := range fields {
			if field == ")" {
				closed++
				continue
			}
			if closed == 3 && !strings.HasPrefix(field, "(") {
				name = field
				break
			}
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if !q3TextureExists(name, basePaths) {
			fmt.Printf("WARNING: Couldn't find image for shader textures/%s\n", name)
		}
	}
}

func q3TextureExists(name string, basePaths []string) bool {
	for _, base := range basePaths {
		for _, extension := range []string{".tga", ".jpg", ".png"} {
			candidate := filepath.Join(base, "baseq3", "textures", filepath.FromSlash(name)+extension)
			if _, err := os.Stat(candidate); err == nil {
				return true
			}
		}
	}
	return false
}
