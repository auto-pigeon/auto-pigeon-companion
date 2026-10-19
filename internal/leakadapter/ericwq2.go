package leakadapter

import (
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// EricwQ2MeasuredVersion is the build every sentence below was measured on:
// the Auto-Pigeon fork of ericw-tools, release 1.4518, in its `-q2bsp` mode
// (`Q2_010`). Upstream 2.0.0-alpha7 — what the family's default tool profile
// describes — was not available to measure, so it is not qualified.
const EricwQ2MeasuredVersion = "2.0.0-alpha11-32-g0c787d7a+auto-pigeon.1.4518"

// EricwQ2CandidateVersion is the fork's candidate 1.4522 (`Q2_005B`: entity
// lines kept in order, `light -lmextents float32`), built from commit 0d64c054
// by the fork's own CI scripts and NOT published. Every control below was run
// again with its qbsp (`testdata/q2/logs-1.4522`, `pts-1.4522`): the same exits,
// the same files left, the same point files byte for byte, and every row read
// as 1.4518's is. Only this exact banner is added; nothing else 1.x is.
const EricwQ2CandidateVersion = "2.0.0-alpha11-36-g0d64c054+auto-pigeon.1.4522"

// EricwQ2ReleaseVersion is the fork's release 1.4524 (`Q2_021`), the one the
// Auto-Pigeon build profiles are written for: 1.4522's two changes plus two to
// how qbsp splits a portal. Every control below was run again with the
// published linux-amd64 archive's qbsp (`testdata/q2/logs-1.4524`,
// `pts-1.4524`): the same exits, files left and point files as 1.4522 and
// 1.4518, the logs equal but for the banner and the timings. Only this exact
// banner is added.
const EricwQ2ReleaseVersion = "2.0.0-alpha11-38-gd4717d68+auto-pigeon.1.4524"

// EricwQ2MeasuredVersions are the qbsp builds the reading below was measured on.
var EricwQ2MeasuredVersions = []string{EricwQ2MeasuredVersion, EricwQ2CandidateVersion, EricwQ2ReleaseVersion}

// What qbsp 1.4518 prints with `-q2bsp -leaktest`, measured on the authored
// controls in `testdata/q2/controls` (the logs beside them are those runs,
// with only the directories renamed):
//
//	A  sealed room, entity inside        exit 0  `N outside leaves` (twice), `Writing … as Quake II BSP IBSP:38`; BSP, PRT, texinfo
//	B  wall with an 8-unit slot          exit 1  `Reached occupant "light" at (0 0 192)`, `Leak file written`, ERROR; .pts, .leak.prt, no BSP
//	C  a light outside the shell         exit 1  the same, the occupant being that light
//	D  no entity at all                  exit 0  `No entities in empty space -- no filling performed (hull 0)`; BSP written
//	E  every entity inside a brush       exit 0  the same line as D: qbsp does not say "in solid"
//	F  slot covered by detail, by clip,  exit 1  as B: none of the three seals
//	   by a translucent window
//	G  file cut off mid-plane            exit 1  ERROR `parse_token: …: Line is incomplete`; nothing but the log
//	G' a face whose points are collinear exit 1  `Brush plane with no normal`, the brush dropped, then as B
//	H  a wall naming a texture with no   exit 0  `Couldn't locate texture for q2010/nothere`; sealed, BSP written
//	   WAL
//	I  A, with a stale level.pts and     exit 0  as A; qbsp DELETES the stale .pts and leaves the stale
//	   level.leak.prt planted                    .leak.prt. Before the fill (G), the stale .pts survives.
//
// And with `-quiet` a sealed run prints no fill report at all.
//
// What follows, each a wrong answer somebody would have shipped:
//
//   - A BSP and exit 0 are not a seal: D, E and H have both.
//   - A point file is not a leak: only the run that SAYS it wrote one made it.
//     Beside a run that says nothing of the kind, a point file is somebody
//     else's, and the run is no verdict.
//   - A texture qbsp could not open is a Quake II face whose contents nobody
//     read. A room sealed by it is not known to be sealed.
//   - The direction is EricW's: the first point is the entity reached (its
//     origin is the one the log names), the last is outside.
const (
	q2Banner        = "---- qbsp / ericw-tools "
	q2Mode          = `"q2bsp" was set to "1"`
	q2Error         = "************ ERROR ************"
	q2NoEntities    = "No entities in empty space"
	q2LeakFile      = "Leak file written to "
	q2FillHeading   = "---- FillOutside ----"
	q2SkipTexture   = "skip"
	q2PlaneNoNormal = "Brush plane with no normal"
	q2BoundsRange   = "brush bounds out of range"
)

var (
	q2VersionLine = regexp.MustCompile(`(?m)^---- qbsp / ericw-tools (\S+) ----\s*$`)
	q2Reached     = regexp.MustCompile(`(?m)Reached occupant (?:"[^"\n]*" )?at \(\s*(\S+)\s+(\S+)\s+(\S+)\s*\)`)
	// `Q2_019`: a real-size map prints "[.   ]    1,723 outside leaves" — a progress prefix and grouped digits.
	q2OutsideLeaves  = regexp.MustCompile(`(?m)^(?:\[[^\]\n]*\])?\s*\d[\d,]* outside leaves\s*$`)
	q2WritingIBSP38  = regexp.MustCompile(`(?m)^Writing \S.* as Quake II BSP IBSP:38\s*$`)
	q2Wrote          = regexp.MustCompile(`(?m)^Wrote \S.*\.bsp\s*$`)
	q2Elapsed        = regexp.MustCompile(`(?m)^\s*[\d.]+s? seconds elapsed\s*$`)
	q2MissingTexture = regexp.MustCompile(`(?m)WARNING: Couldn't locate texture for (\S+)\s*$`)
)

// The Quake II tokens, in the same closed list as the Q3Map2 ones.
const (
	EvNotEricwQ2Log      = "not_ericw_q2_log"
	EvRouteUnexpected    = "route_unexpected"
	EvRouteDisagrees     = "route_disagrees"
	EvTextureMissing     = "texture_missing"
	EvBrushInvalid       = "brush_invalid"
	EvFillUnreported     = "fill_unreported"
	EvBSPFormatWrong     = "bsp_format_unexpected"
	EvOccupantReached    = "occupant_reached"
	EvLeakFileReported   = "leak_file_reported"
	EvQuake2BSPConfirmed = "bsp_ibsp38"
)

// EricwQ2LogVersion is the version a log names itself as, or empty.
func EricwQ2LogVersion(log string) string {
	if match := q2VersionLine.FindStringSubmatch(log); match != nil {
		return match[1]
	}
	return ""
}

// ClassifyEricwQ2 reads one qbsp `-q2bsp` run.
//
// The precedence is ClassifyQ3Map2's, with what Quake II measured:
//
//  1. A text that is not an EricW qbsp log in its Quake II mode is no verdict.
//  2. qbsp SAYING it reached an occupant, or that it wrote a leak file, is a
//     leak — whatever the exit status, whatever else stands beside it. The
//     route is the fresh point file, and its first point must be the occupant
//     the log names.
//  3. A point file beside a run that said neither is not this run's: no
//     verdict.
//  4. An error qbsp stopped on, a cancelled step: no verdict.
//  5. "No entities in empty space" is an empty flood — not tested, whatever
//     BSP was written. That reading is this version's.
//  6. "No leak" needs everything: the fill reported, the Quake II BSP written
//     and there, the footer, exit 0, the step succeeded, every texture found,
//     and the measured version.
func ClassifyEricwQ2(e Evidence) Verdict {
	// A progress line ends in a carriage return, and the next line is written
	// after it (measured, `-verbose`): both are line ends here.
	log := strings.ReplaceAll(e.Log, "\r", "\n")
	if !strings.Contains(log, q2Banner) || !strings.Contains(log, q2Mode) {
		return Verdict{Outcome: OutcomeIncomplete, Evidence: []string{EvNotEricwQ2Log}}
	}
	var evidence []string
	add := func(token string) { evidence = append(evidence, token) }
	version := e.CompilerVersion
	if logged := EricwQ2LogVersion(log); logged != "" {
		version = logged
	}
	qualified := slices.Contains(EricwQ2MeasuredVersions, version)
	missingTexture := false
	for _, match := range q2MissingTexture.FindAllStringSubmatch(log, -1) {
		// qbsp looks up a `skip` of its own on every run, measured on every
		// control; it is not a face of the map.
		missingTexture = missingTexture || match[1] != q2SkipTexture
	}
	brushInvalid := strings.Contains(log, q2PlaneNoNormal) || strings.Contains(log, q2BoundsRange)
	warnings := func() {
		if missingTexture {
			add(EvTextureMissing)
		}
		if brushInvalid {
			add(EvBrushInvalid)
		}
		if !qualified {
			add(EvVersionUnqualified)
		}
	}

	reached := q2Reached.FindStringSubmatch(log)
	leakFile := strings.Contains(log, q2LeakFile)
	points, routeErr := 0, error(nil)
	if e.Pointfile != "" {
		points, routeErr = CountPoints(e.Pointfile)
	}
	if reached != nil || leakFile {
		if reached != nil {
			add(EvOccupantReached)
		}
		if leakFile {
			add(EvLeakFileReported)
		}
		verdict := Verdict{Outcome: OutcomeLeak}
		switch {
		case e.Pointfile == "":
			add(EvRouteMissing)
		case routeErr != nil:
			add(EvRouteInvalid)
		case reached != nil && !startsAt(e.Pointfile, reached[1:4]):
			add(EvRouteDisagrees)
		default:
			add(EvRoute)
			verdict.RoutePoints = points
		}
		warnings()
		verdict.Evidence = evidence
		return verdict
	}

	incomplete := func(tokens ...string) Verdict {
		for _, token := range tokens {
			add(token)
		}
		warnings()
		return Verdict{Outcome: OutcomeIncomplete, Evidence: evidence}
	}
	if e.Pointfile != "" {
		add(EvRouteUnexpected)
	}
	if strings.Contains(log, q2Error) {
		return incomplete(EvFatalError)
	}
	switch e.StepState {
	case "cancelled", "interrupted":
		return incomplete(EvCancelled)
	}
	if e.Pointfile != "" {
		return incomplete()
	}

	if strings.Contains(log, q2NoEntities) {
		if !qualified {
			return incomplete()
		}
		add(EvEmptyFlood)
		add(EvNoEntityInOpen)
		warnings()
		return Verdict{Outcome: OutcomeNoInterior, Evidence: evidence}
	}

	if e.ExitCode != nil && *e.ExitCode != 0 {
		return incomplete(EvExitNonzero)
	}
	if !q2Wrote.MatchString(log) || !q2Elapsed.MatchString(log) {
		return incomplete(EvTruncated)
	}
	fill := strings.Index(log, q2FillHeading)
	if fill < 0 || !q2OutsideLeaves.MatchString(log[fill:]) {
		// `-quiet` prints no fill report; a run that does not say it filled
		// the outside has not shown that it did.
		return incomplete(EvFillUnreported)
	}
	add(EvFillCompleted)
	if !q2WritingIBSP38.MatchString(log) || (e.BSPFormat != "" && e.BSPFormat != "IBSP:38") {
		return incomplete(EvBSPFormatWrong)
	}
	add(EvFinished)
	if e.BSP != nil && !*e.BSP {
		return incomplete(EvBSPMissing)
	}
	if e.StepState != "" && e.StepState != "succeeded" {
		return incomplete(EvStepNotSucceeded)
	}
	if missingTexture || !qualified {
		return incomplete()
	}
	add(EvBSPWritten)
	add(EvQuake2BSPConfirmed)
	warnings()
	return Verdict{Outcome: OutcomeNoLeak, Evidence: evidence}
}

// startsAt reports whether a point file's first point is the coordinate the
// log names, to within a unit.
func startsAt(pointfile string, named []string) bool {
	for _, line := range strings.Split(strings.TrimPrefix(pointfile, "\xef\xbb\xbf"), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 3 {
			return false
		}
		for axis := range 3 {
			got, err1 := strconv.ParseFloat(fields[axis], 64)
			want, err2 := strconv.ParseFloat(named[axis], 64)
			if err1 != nil || err2 != nil || math.Abs(got-want) > 1 {
				return false
			}
		}
		return true
	}
	return false
}

// BSPFormatOf names a BSP header the way qbsp's log does — `IBSP:38` for
// Quake II — or empty for a header too short to read.
func BSPFormatOf(header []byte) string {
	if len(header) < 8 {
		return ""
	}
	version := int32(header[4]) | int32(header[5])<<8 | int32(header[6])<<16 | int32(header[7])<<24
	ident := string(header[:4])
	for _, r := range ident {
		if r < 0x20 || r > 0x7e {
			// Quake 1's BSP29 has no ident: its first four bytes are the version.
			return "BSP:" + strconv.Itoa(int(int32(header[0])|int32(header[1])<<8|int32(header[2])<<16|int32(header[3])<<24))
		}
	}
	return ident + ":" + strconv.Itoa(int(version))
}
