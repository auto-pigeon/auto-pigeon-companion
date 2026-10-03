// Package leakadapter is the one table that says which compiler answers "does
// this saved map leak" for which game, and how that compiler's answer is read
// (`Q3_018`).
//
// # Why a table
//
// The editor's Leaks → Test in Companion flow was written for Quake 1: one
// pipeline id, EricW's qbsp, a `.pts` point file, one result schema. Each of
// those was a constant in the place that used it. A Quake III map sent down
// that path would have been converted to a Quake III `.map` and handed to qbsp —
// and whatever qbsp said about it would have been returned as evidence.
//
// So the game decides, here, and nowhere else: the pipeline that may run, the
// program it is, the point file it writes and which way that file runs, the
// result schema, and how the log is turned into a verdict. A game with no row
// is UNSUPPORTED, by name. It is never Quake 1.
//
// # What decides the game
//
// The `game` the saved APMap declares, read from the bytes of the pinned,
// digest-checked revision. A hint in a link, a pipeline somebody selected and
// the state of a toolbar are all checked against that and never used instead.
//
// # Adding a game
//
// A row is a claim that somebody ran that compiler on authored controls — a
// sealed room, a room with a wall missing, a room nothing stands in — and wrote
// down what it printed, what it exited with and what it left on disk. Quake II
// has a `qbsp` too; that is not a measurement, and it has no row.
package leakadapter

import (
	"errors"
	"fmt"
	"sort"
)

// The games with a row.
const (
	ProfileQuake1 = "quake1"
	ProfileQuake3 = "quake3"
)

// The compilers, as the result envelope names them. A closed list: an
// envelope naming anything else is refused rather than parsed hopefully.
const (
	CompilerEricwQbsp = "ericw-qbsp"
	CompilerQ3Map2    = "q3map2"
)

// The point file formats, as the result envelope names them.
const (
	FormatEricwPts  = "ericw-pts"
	FormatQ3Map2Lin = "q3map2-lin"
)

// Which end of a point file is the entity the compiler reached.
//
// Measured, both: EricW 0.18.1's `.pts` begins at the occupant and ends
// outside; Q3Map2 2.5.17n's `.lin` begins OUTSIDE and its last point is the
// entity's origin. A reader that assumed the first is the occupant would look
// for the entity in the void.
const (
	DirectionOccupantFirst = "occupant_to_outside"
	DirectionOutsideFirst  = "outside_to_occupant"
)

// The result schemas. 1.0 is the Quake 1 envelope and is never reinterpreted;
// 1.1 names its game, compiler and point file format.
const (
	Schema10 = "aucom.leak-result/1.0"
	Schema11 = "aucom.leak-result/1.1"
)

// ErrUnsupported reports a game with no row.
var ErrUnsupported = errors.New("leak testing is not available for this game")

// Adapter is one row.
type Adapter struct {
	// Profile is the APMap `game` this row answers for.
	Profile string
	// PipelineID is the only pipeline a leak request for this game may run.
	PipelineID string
	// Compiler and PointfileFormat are the envelope's names for what ran and
	// what it wrote; Direction says which end of the point file is the entity.
	Compiler        string
	PointfileFormat string
	Direction       string
	// PointfileOutput and LogOutput are the pipeline outputs that carry the
	// point file and the compiler's own text.
	PointfileOutput string
	LogOutput       string
	// BSPOutput is the pipeline output that is the compiled BSP, when the
	// pipeline publishes one as evidence. Empty when it does not.
	BSPOutput string
	// ResultSchema is the envelope this row's results are returned in.
	ResultSchema string
	// QualifiedVersions are the compiler versions this row's reading of the
	// log was measured on. Another version may print other words.
	QualifiedVersions []string
	// Classify reads a finished run. Nil for a row whose log the editor reads
	// by itself, as it has since before this table (Quake 1).
	Classify func(Evidence) Verdict
}

// Qualified reports whether version is one this row was measured on.
func (a Adapter) Qualified(version string) bool {
	for _, known := range a.QualifiedVersions {
		if known == version {
			return true
		}
	}
	return false
}

var adapters = map[string]Adapter{
	ProfileQuake1: {
		Profile: ProfileQuake1, PipelineID: "auto-pigeon.q1.leak-test",
		Compiler: CompilerEricwQbsp, PointfileFormat: FormatEricwPts, Direction: DirectionOccupantFirst,
		PointfileOutput: "pts", LogOutput: "compile_log",
		ResultSchema: Schema10, QualifiedVersions: []string{"v0.18.1", "0.18.1"},
	},
	ProfileQuake3: {
		Profile: ProfileQuake3, PipelineID: "auto-pigeon.q3.leak-test",
		Compiler: CompilerQ3Map2, PointfileFormat: FormatQ3Map2Lin, Direction: DirectionOutsideFirst,
		PointfileOutput: "lin", LogOutput: "compile_log", BSPOutput: "bsp",
		ResultSchema: Schema11, QualifiedVersions: []string{Q3Map2MeasuredVersion},
		Classify: ClassifyQ3Map2,
	},
}

// ForProfile is the row for a game, or ErrUnsupported naming it.
func ForProfile(game string) (Adapter, error) {
	adapter, ok := adapters[game]
	if !ok {
		if game == "" {
			return Adapter{}, fmt.Errorf("%w: the saved map names no game", ErrUnsupported)
		}
		return Adapter{}, fmt.Errorf("%w: %q (supported: %s)", ErrUnsupported, game, supported())
	}
	return adapter, nil
}

// ForPipeline is the row whose pipeline this is.
func ForPipeline(id string) (Adapter, bool) {
	for _, adapter := range adapters {
		if adapter.PipelineID == id {
			return adapter, true
		}
	}
	return Adapter{}, false
}

// Profiles lists the games with a row, sorted.
func Profiles() []string {
	names := make([]string, 0, len(adapters))
	for name := range adapters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func supported() string {
	out := ""
	for i, name := range Profiles() {
		if i > 0 {
			out += ", "
		}
		out += name
	}
	return out
}

// The outcomes. The editor's existing vocabulary, unchanged.
const (
	OutcomeLeak       = "leak"
	OutcomeNoLeak     = "no_leak"
	OutcomeNoInterior = "no_interior"
	OutcomeIncomplete = "incomplete"
)

// Evidence is what is known about one finished run. Every field is a fact
// about THIS run: the text it printed, the status it ended with, and the files
// it left in a directory that was empty when it started.
type Evidence struct {
	// Log is the compiler's standard output.
	Log string
	// CompilerVersion is what the version probe resolved, when it did.
	CompilerVersion string
	// ExitCode is the process's own exit status; nil when it has none (it never
	// started, or it was killed).
	ExitCode *int
	// StepState is how the compile step ended: `succeeded`, `failed`,
	// `cancelled`, `interrupted`, `timed_out`. Empty when not known — a log
	// imported by hand.
	StepState string
	// Pointfile is the fresh point file's text, or empty.
	Pointfile string
	// BSP is whether this run left a BSP: nil when not known.
	BSP *bool
}

// Verdict is the reading of one run.
type Verdict struct {
	Outcome string `json:"outcome"`
	// Evidence is why, as tokens from a closed list (the Ev* constants), in the
	// order they were found.
	Evidence []string `json:"evidence,omitempty"`
	// RoutePoints is how many points the fresh point file holds, when it is a
	// usable route.
	RoutePoints int `json:"route_points,omitempty"`
}

// Has reports whether the verdict carries a token.
func (v Verdict) Has(token string) bool {
	for _, have := range v.Evidence {
		if have == token {
			return true
		}
	}
	return false
}
