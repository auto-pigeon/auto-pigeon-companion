// Package incident is the Companion's half of the Auto-Pigeon incident
// contract: the envelope, the taxonomy lookup, central redaction, the
// correlation convention and the one reporter that sends to the incident
// backend.
//
// # The contract is AULIBS', read as data
//
// contract/incident-codes.json and contract/redaction-rules.json are
// BYTE-FOR-BYTE copies of `auto-pigeon-libraries/ts/incident-contract/schema/`,
// embedded so a release binary needs no sibling checkout, and
// TestEmbeddedContractIsExactlyAULIBS fails the build the moment either drifts.
// This package reads them; it never restates a code or a pattern. That is the
// arrangement AUB and AUE already have, and a fourth sincere transcription of
// the same rules is what it exists to avoid.
//
// # Stdlib only, on purpose
//
// AUB and AUE use the Sentry Go SDK. The Companion does not, because it has no
// third-party Go dependency at all and `release.TestThisProgramLinksNoExternalModule`
// keeps it that way (THIRD_PARTY_NOTICES.md). The store endpoint takes one JSON
// event, which is what AULIBS' own `toSentryEvent` produces and what AUP and AUG
// already POST without an SDK; this package builds the same event in Go and
// POSTs it. Nothing an SDK would add — default integrations, argv, environment,
// hostname, module inventory, stack frames — can therefore be added by accident:
// there is no SDK to add it.
package incident

import (
	_ "embed"
	"encoding/json"
	"sync"
)

//go:embed contract/incident-codes.json
var codesJSON []byte

// Component is the workspace acronym the envelope files every Companion
// incident under.
const Component = "AUCOM"

// The codes the Companion raises. Named constants, not literals at call sites,
// and TestTheCompanionRaisesOnlyItsOwnCodes fails if one stops being a real
// taxonomy entry attributed to AUCOM.
const (
	// CodeJobFailed is a supervised job that ended failed. Which job travels on
	// `subsystem`/`operation`; the command line, the arguments and every path
	// stay on this machine.
	CodeJobFailed = "aucom.job_failed"
	// CodeReadinessFailed is the Companion not becoming ready: its AUB link did
	// not answer within its bound.
	CodeReadinessFailed = "aucom.readiness_failed"

	// CodeTelemetryUnavailable is the incident backend's own absence. It is
	// recorded LOCALLY and never sent: raising an incident about the sink would
	// go to the sink.
	CodeTelemetryUnavailable = "reporting.telemetry_unavailable"
)

// Code is one taxonomy entry.
type Code struct {
	Code        string `json:"code"`
	Component   string `json:"component"`
	Recoverable bool   `json:"recoverable"`
	Summary     string `json:"summary"`
}

var (
	codesOnce  sync.Once
	codesByKey map[string]Code
)

func loadCodes() {
	codesOnce.Do(func() {
		var doc struct {
			Codes []Code `json:"codes"`
		}
		// An embedded file that does not parse is a build-time mistake the
		// contract test catches; an empty map makes Validate refuse every code,
		// which is the honest failure rather than a crash on a failing path.
		_ = json.Unmarshal(codesJSON, &doc)
		codesByKey = make(map[string]Code, len(doc.Codes))
		for _, c := range doc.Codes {
			codesByKey[c.Code] = c
		}
	})
}

// Lookup returns the taxonomy entry for code.
func Lookup(code string) (Code, bool) {
	loadCodes()
	c, ok := codesByKey[code]
	return c, ok
}

// Components is the closed set the envelope's `component` enum allows.
var Components = []string{"AUP", "AUB", "AUC", "AUE", "AUG", "AUT", "AUCOM"}
