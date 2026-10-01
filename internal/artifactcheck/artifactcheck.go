// Package artifactcheck says whether a file is the kind of file its artifact
// role claims, by reading it.
//
// # Why this exists (Q3_010)
//
// A stage used to be judged by two things: the program's exit status, and
// whether each declared output existed. Q3Map2 2.5.17n makes both of those
// insufficient, and each case below was measured rather than supposed:
//
//   - `-light` handed an EMPTY or garbage `.srf` exits 0 and writes a lit BSP.
//     The surface file is where every surface's lightmap settings come from, so
//     that BSP is lit from nothing the map said — and nothing complained.
//   - `-vis` handed a garbage `.prt` does fail, with `LoadPortals: failed to
//     read header`, but only after the process started: the file was already
//     wrong when it was staged.
//   - a file that is merely PRESENT under the right name satisfies an
//     existence check, which is what a leaked build's leftovers, a truncated
//     copy or a disk that filled up all look like.
//
// So a role may have a reader here, and the executor asks it twice: of every
// input before the program starts, and of every output after it stops. A role
// with no reader is not checked and is not claimed to be — [Known] says which
// is which, and the list is short on purpose. Each check reads a bounded
// prefix; none of them parses the whole format, because "is this a Quake III
// BSP whose lump table fits inside the file" is a question with an answer, and
// "is this BSP correct" is the compiler's.
package artifactcheck

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Invalid reports a file that is not what its role says.
type Invalid struct {
	Role   string
	Path   string
	Reason string
}

func (e *Invalid) Error() string {
	return fmt.Sprintf("%s: %s", describe(e.Role), e.Reason)
}

// IsInvalid reports whether an error is a content verdict, as opposed to a
// failure to read the file at all.
func IsInvalid(err error) bool {
	var invalid *Invalid
	return errors.As(err, &invalid)
}

type check struct {
	what string
	run  func(file *os.File, size int64) string
}

// checks is every role with a reader. Three BSP roles share one reader: what
// `-vis` and `-light` add is a lump's content, and whether a visibility lump
// may legitimately be empty is the compiler's business (`No portals means no
// vis`), so only the container is judged.
var checks = map[string]check{
	"q3.bsp":        {"a Quake III BSP", quake3BSP},
	"q3.bsp.vised":  {"a Quake III BSP", quake3BSP},
	"q3.bsp.lit":    {"a Quake III BSP", quake3BSP},
	"q3.prt":        {"a Quake III portal file", quake3Portals},
	"q3.srf":        {"a Q3Map2 surface file", quake3Surfaces},
	"q3.lin":        {"a leak line file", leakLine},
	"q3.map.source": {"a Quake III map source", mapSource},
}

// Known is every role this package reads, sorted.
func Known() []string {
	out := make([]string, 0, len(checks))
	for role := range checks {
		out = append(out, role)
	}
	sort.Strings(out)
	return out
}

func describe(role string) string {
	if c, ok := checks[role]; ok {
		return "not " + c.what
	}
	return "not a valid " + role
}

// Check reads the file at path as the given role. It returns nil for a role
// with no reader, an [*Invalid] for a file that is not what the role says, and
// any other error when the file could not be read.
func Check(role, path string) error {
	c, ok := checks[role]
	if !ok {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if reason := c.run(file, info.Size()); reason != "" {
		return &Invalid{Role: role, Path: path, Reason: reason}
	}
	return nil
}

// Quake III's BSP: `IBSP`, version 46, then seventeen (offset, length) pairs.
const (
	q3BSPVersion  = 46
	q3BSPLumps    = 17
	q3BSPHeader   = 8 + q3BSPLumps*8
	q3LumpEntity  = 0
	q3LumpModels  = 7
	q3ModelRecord = 40
)

func quake3BSP(file *os.File, size int64) string {
	if size < q3BSPHeader {
		return fmt.Sprintf("it is %d bytes, and the header alone is %d", size, q3BSPHeader)
	}
	header := make([]byte, q3BSPHeader)
	if _, err := io.ReadFull(file, header); err != nil {
		return "its header could not be read: " + err.Error()
	}
	if string(header[:4]) != "IBSP" {
		return fmt.Sprintf("it starts with %q, not IBSP", printable(header[:4]))
	}
	if version := int32(binary.LittleEndian.Uint32(header[4:8])); version != q3BSPVersion {
		return fmt.Sprintf("it is IBSP version %d; Quake III is %d", version, q3BSPVersion)
	}
	for lump := 0; lump < q3BSPLumps; lump++ {
		offset := int64(int32(binary.LittleEndian.Uint32(header[8+lump*8:])))
		length := int64(int32(binary.LittleEndian.Uint32(header[12+lump*8:])))
		if offset < 0 || length < 0 || offset+length > size {
			return fmt.Sprintf("lump %d claims bytes %d..%d of a %d-byte file: it is truncated or was not finished",
				lump, offset, offset+length, size)
		}
		switch {
		case lump == q3LumpEntity && length == 0:
			return "it has no entities, and every map has a worldspawn"
		case lump == q3LumpModels && length < q3ModelRecord:
			return "it has no models, and the world is model 0"
		}
	}
	return ""
}

// A portal file, as Q3Map2 writes it: `PRT1`, then the cluster, portal and face
// counts each on a line, then one line per portal. Checked as far as the
// counts and that the lines they promise are there — a file cut off halfway is
// what `LoadPortals: reading portal N` is, and it is cheaper to say so before
// the process starts.
func quake3Portals(file *os.File, _ int64) string {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	if !scanner.Scan() {
		return "it is empty"
	}
	if first := strings.TrimSpace(scanner.Text()); first != "PRT1" {
		return fmt.Sprintf("its first line is %q, not PRT1", printable([]byte(clip(first))))
	}
	counts := make([]int64, 0, 3)
	for len(counts) < 3 {
		if !scanner.Scan() {
			return "it ends before its cluster, portal and face counts"
		}
		n, err := strconv.ParseInt(strings.TrimSpace(scanner.Text()), 10, 64)
		if err != nil || n < 0 {
			return fmt.Sprintf("count %d is %q, not a number", len(counts)+1, printable([]byte(clip(scanner.Text()))))
		}
		counts = append(counts, n)
	}
	wanted := counts[1] + counts[2]
	var lines int64
	for lines < wanted && scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			lines++
		}
	}
	if lines < wanted {
		return fmt.Sprintf("it declares %d portals and %d faces and holds %d of those lines: it is truncated",
			counts[1], counts[2], lines)
	}
	return ""
}

// A surface file is a script: `default`, a block, then one block per surface.
// Q3Map2 reads an empty one, or one that is not a script at all, without a
// word and lights the map from it (measured) — so the first two tokens are
// checked here, which is all it takes to tell a surface file from its absence.
func quake3Surfaces(file *os.File, _ int64) string {
	tokens := firstTokens(file, 2)
	switch {
	case len(tokens) == 0:
		return "it is empty, and Q3Map2 would light the map from it without complaint"
	case tokens[0] != "default":
		return fmt.Sprintf("it starts with %q, not `default`", printable([]byte(clip(tokens[0]))))
	case len(tokens) < 2 || tokens[1] != "{":
		return "its `default` block never opens"
	}
	return ""
}

// A leak line file is points, three numbers to a line.
func leakLine(file *os.File, _ int64) string {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 4<<10), 64<<10)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return fmt.Sprintf("its first line has %d fields, not the three numbers of a point", len(fields))
		}
		for _, field := range fields {
			if _, err := strconv.ParseFloat(field, 64); err != nil {
				return fmt.Sprintf("its first line holds %q, which is not a number", printable([]byte(clip(field))))
			}
		}
		return ""
	}
	return "it is empty: a leak file with no points leads nowhere"
}

// A map source opens an entity. That is all this asks: the dialect, the
// brushes and the patches are the compiler's to read, and the converter's to
// have written.
func mapSource(file *os.File, _ int64) string {
	tokens := firstTokens(file, 1)
	switch {
	case len(tokens) == 0:
		return "it is empty"
	case tokens[0] != "{":
		return fmt.Sprintf("it starts with %q, not the `{` that opens an entity", printable([]byte(clip(tokens[0]))))
	}
	return ""
}

// firstTokens returns the first n whitespace-separated tokens of a script,
// skipping `//` comments, out of a bounded prefix.
func firstTokens(file *os.File, n int) []string {
	scanner := bufio.NewScanner(io.LimitReader(file, 1<<20))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var tokens []string
	for len(tokens) < n && scanner.Scan() {
		line := scanner.Text()
		if cut := strings.Index(line, "//"); cut >= 0 {
			line = line[:cut]
		}
		for _, field := range strings.Fields(line) {
			if len(tokens) < n {
				tokens = append(tokens, field)
			}
		}
	}
	return tokens
}

func clip(s string) string {
	if len(s) > 24 {
		return s[:24]
	}
	return s
}

// printable keeps a quoted excerpt of somebody else's file readable: anything
// that is not printable ASCII becomes `?`.
func printable(raw []byte) string {
	out := make([]byte, len(raw))
	for i, b := range raw {
		if b < 0x20 || b > 0x7e {
			b = '?'
		}
		out[i] = b
	}
	return string(out)
}
