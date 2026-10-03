package leakadapter

import (
	"regexp"
	"strings"
)

// Q3Map2MeasuredVersion is the build every sentence below was measured on:
// NetRadiant-custom `20260114`, `auto-pigeon-tools/q3map2-contract.json`.
const Q3Map2MeasuredVersion = "2.5.17n-git-68ecbed"

// What Q3Map2 2.5.17n prints, measured on the authored controls in
// `testdata/q3/controls` (the logs beside them are those runs).
//
//	sealed room, entity inside      exit 0   `N leafs filled`, `Writing ….bsp`, `N seconds elapsed`; BSP, PRT, SRF
//	wall missing                    exit 0   banner, `Entity N, Brush 0: Entity leaked`, abort; `.lin`, no BSP
//	entity outside the room         exit 0   the same; a two-point `.lin`
//	gap covered by a patch          exit 0   the same: a patch does not seal
//	gap covered by a detail brush   exit 0   the same: a detail brush does not seal
//	wall of a nonsolid shader       exit 0   the same
//	no entity at all                exit 0   banner, NO `Entity leaked`, abort; no `.lin`, no BSP
//	every entity inside a brush     exit 0   `Entity N (…): Entity in solid`, then as "no entity at all"
//	wall naming an undefined shader exit 0   `WARNING: Couldn't find image for shader`; sealed, BSP written
//	unreadable map                  exit 1   `************ ERROR ************`
//
// Three things follow, and each is a wrong answer somebody would have shipped:
//
//   - The exit status says nothing. Seven of the ten exit 0.
//   - The `leaked` banner alone is not a leak. With nothing standing in open
//     space Q3Map2 floods nothing, calls that `leaked`, and writes no line
//     file: there was no flood, so there is no hole it found. That run is "not
//     tested", and a map with no entity in it would otherwise be reported as
//     having a hole in its walls.
//   - A missing `.lin` is not a pass, and neither is a BSP: without `-leaktest`
//     a leaked map gets both a banner and a BSP.
const (
	q3Banner        = "Q3Map (ydnar) - v"
	q3LeakedBanner  = "******* leaked *******"
	q3EntityLeaked  = ": Entity leaked"
	q3ReachedFrom   = "entity reached from outside -- leak detected"
	q3NoEntityOpen  = "no entities in open -- no filling"
	q3EntityInSolid = ": Entity in solid"
	q3Aborting      = "--- MAP LEAKED, ABORTING LEAKTEST ---"
	q3Error         = "************ ERROR ************"
	q3MissingImage  = "Couldn't find image for shader"
)

var (
	q3VersionLine = regexp.MustCompile(`(?m)^Q3Map \(ydnar\) - v(\S+)\s*$`)
	q3LeafsFilled = regexp.MustCompile(`(?m)^\s*\d+ leafs filled\s*$`)
	q3WroteBSP    = regexp.MustCompile(`(?m)^Writing \S.*\.bsp\s*$`)
	q3Elapsed     = regexp.MustCompile(`(?m)^\s*\d+ seconds elapsed\s*$`)
	q3ModelError  = regexp.MustCompile(`(?m)^ERROR: `)
)

// The evidence tokens. A closed list: the editor and the account's server
// refuse a token that is not here.
const (
	EvNotQ3Map2          = "not_q3map2_log"
	EvVersionUnqualified = "version_unqualified"
	EvLeakBanner         = "leak_banner"
	EvEntityLeaked       = "entity_leaked"
	EvRoute              = "route"
	EvRouteMissing       = "route_missing"
	EvRouteInvalid       = "route_invalid"
	EvEmptyFlood         = "empty_flood"
	EvEntityInSolid      = "entity_in_solid"
	EvNoEntityInOpen     = "no_entity_in_open"
	EvFillCompleted      = "fill_completed"
	EvBSPWritten         = "bsp_written"
	EvBSPMissing         = "bsp_missing"
	EvFinished           = "finished"
	EvTruncated          = "log_truncated"
	EvFatalError         = "fatal_error"
	EvStepNotSucceeded   = "step_not_succeeded"
	EvCancelled          = "cancelled"
	EvExitNonzero        = "exit_nonzero"
	EvShaderImageMissing = "shader_image_missing"
)

// EvidenceTokens is the closed list, for a reader that validates one.
func EvidenceTokens() []string {
	return []string{EvNotQ3Map2, EvVersionUnqualified, EvLeakBanner, EvEntityLeaked, EvRoute, EvRouteMissing,
		EvRouteInvalid, EvEmptyFlood, EvEntityInSolid, EvNoEntityInOpen, EvFillCompleted, EvBSPWritten,
		EvBSPMissing, EvFinished, EvTruncated, EvFatalError, EvStepNotSucceeded, EvCancelled, EvExitNonzero,
		EvShaderImageMissing}
}

// Q3Map2LogVersion is the version a log names itself as, or empty.
func Q3Map2LogVersion(log string) string {
	if match := q3VersionLine.FindStringSubmatch(log); match != nil {
		return match[1]
	}
	return ""
}

// ClassifyQ3Map2 reads one Q3Map2 BSP-stage run.
//
// The order is the precedence, and it is deliberate:
//
//  1. A text that is not a Q3Map2 log is no verdict.
//  2. The compiler SAYING it reached an entity from outside is a leak, whatever
//     else is true of the run — it exited 0, it was the last thing printed
//     before somebody cancelled, another warning stands beside it. A fresh,
//     well-formed line file under the banner says the same thing.
//  3. Otherwise an error the compiler stopped on, a cancelled or failed step or
//     a log that was cut short is no verdict.
//  4. Otherwise the banner with nothing reached is an empty flood: not tested.
//     That reading is this version's; on another version it is no verdict.
//  5. Otherwise "no leak" needs everything: the fill ran, the BSP was written
//     and is there, the footer was printed, the process exited 0, the step
//     succeeded, and the version is the measured one. Anything missing is no
//     verdict, with the missing thing named.
func ClassifyQ3Map2(e Evidence) Verdict {
	log := e.Log
	if !strings.Contains(log, q3Banner) {
		return Verdict{Outcome: OutcomeIncomplete, Evidence: []string{EvNotQ3Map2}}
	}
	var evidence []string
	add := func(token string) { evidence = append(evidence, token) }
	version := e.CompilerVersion
	if logged := Q3Map2LogVersion(log); logged != "" {
		// The log's own banner is this run's; a probe is an earlier run.
		version = logged
	}
	qualified := version == Q3Map2MeasuredVersion
	warnings := func() {
		if strings.Contains(log, q3MissingImage) {
			add(EvShaderImageMissing)
		}
		if !qualified {
			add(EvVersionUnqualified)
		}
	}

	banner := strings.Contains(log, q3LeakedBanner)
	reached := strings.Contains(log, q3EntityLeaked) || strings.Contains(log, q3ReachedFrom)
	points, routeErr := 0, error(nil)
	if e.Pointfile != "" {
		points, routeErr = CountPoints(e.Pointfile)
	}
	if reached || (banner && e.Pointfile != "" && routeErr == nil) {
		if banner {
			add(EvLeakBanner)
		}
		if reached {
			add(EvEntityLeaked)
		}
		verdict := Verdict{Outcome: OutcomeLeak}
		switch {
		case e.Pointfile == "":
			add(EvRouteMissing)
		case routeErr != nil:
			add(EvRouteInvalid)
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
	if strings.Contains(log, q3Error) {
		return incomplete(EvFatalError)
	}
	switch e.StepState {
	case "cancelled", "interrupted":
		return incomplete(EvCancelled)
	}

	if banner {
		add(EvLeakBanner)
		if !strings.Contains(log, q3Aborting) && e.StepState == "" && !strings.Contains(log, q3NoEntityOpen) &&
			!strings.Contains(log, q3EntityInSolid) {
			// A banner with nothing after it and no word about why: the text
			// may simply stop here.
			return incomplete(EvTruncated)
		}
		if !qualified {
			return incomplete()
		}
		add(EvEmptyFlood)
		if strings.Contains(log, q3EntityInSolid) {
			add(EvEntityInSolid)
		}
		if strings.Contains(log, q3NoEntityOpen) {
			add(EvNoEntityInOpen)
		}
		warnings()
		return Verdict{Outcome: OutcomeNoInterior, Evidence: evidence}
	}

	// No banner. Everything a pass needs, or no verdict.
	if e.ExitCode != nil && *e.ExitCode != 0 {
		return incomplete(EvExitNonzero)
	}
	if !q3LeafsFilled.MatchString(log) {
		return incomplete(EvTruncated)
	}
	add(EvFillCompleted)
	if !q3WroteBSP.MatchString(log) || !q3Elapsed.MatchString(log) {
		return incomplete(EvTruncated)
	}
	add(EvFinished)
	if e.BSP != nil && !*e.BSP {
		return incomplete(EvBSPMissing)
	}
	if e.StepState != "" && e.StepState != "succeeded" {
		// A model the compiler could not open, a timeout: the step failed for a
		// reason of its own, and a BSP missing geometry is not this map.
		return incomplete(EvStepNotSucceeded)
	}
	if e.StepState == "" && q3ModelError.MatchString(log) {
		return incomplete(EvStepNotSucceeded)
	}
	if !qualified {
		return incomplete()
	}
	add(EvBSPWritten)
	warnings()
	return Verdict{Outcome: OutcomeNoLeak, Evidence: evidence}
}
