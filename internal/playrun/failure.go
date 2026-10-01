package playrun

import (
	"errors"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aue"
)

// What a failed run says to a person, as opposed to what it recorded.
//
// A run records the whole error, because Jobs and the handoff need the exact
// words. The Activity card needs one sentence and a remedy. `AUCOM/AUE/AUB
// 246I1.1` found the card printing an extractor's stderr verbatim — exit
// code, the AUE-TERMINAL record and an incident envelope, in red — and the
// remedy blaming the map for what was a missing setting on this computer.

// ExtractorTerminal is the part of AUE's terminal record a person is shown.
// The reader is internal/aue's: a build that converts a map reads the same
// record (Q3_010), and two readers would be two opinions about one line.
type ExtractorTerminal = aue.Terminal

// extractorTerminal finds AUE's terminal record inside an error's text.
func extractorTerminal(text string) (ExtractorTerminal, bool) { return aue.ReadTerminal(text) }

// Reasons AUE gives when the MAP is what it could not use.
var mapReasons = map[string]bool{
	"invalid_input": true, "schema_invalid": true, "geometry_invalid": true,
	"unsupported_target": true, "unsupported_profile": true,
}

// Reasons AUE gives when the problem is the machine or the extractor itself.
var setupReasons = map[string]bool{
	"internal_error": true, "panic": true, "timed_out": true,
	"resource_exhausted": true, "scratch_io_error": true, "worker_killed": true,
}

// Summary is one plain sentence for a recorded error: the extractor's own
// message when its terminal record is there, otherwise the first line.
func Summary(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if record, ok := extractorTerminal(text); ok && record.Message != "" {
		sentence := strings.TrimSuffix(record.Message, ".")
		if record.Detail != "" {
			sentence += ": " + strings.TrimSuffix(record.Detail, ".")
		}

		return sentence + "."
	}
	first, _, _ := strings.Cut(text, "\n")
	const limit = 240
	if len(first) > limit {
		first = strings.TrimSpace(first[:limit]) + "…"
	}

	return first
}

// convertRemedy tells a map the extractor could not use apart from an
// extractor that could not run.
func convertRemedy(cause error) string {
	record, ok := extractorTerminal(cause.Error())
	switch {
	case ok && mapReasons[record.Reason]:
		return "The extractor could not use this map. Open it in Auto-Pigeon and save it again, " +
			"or report the map to its author."
	case ok && setupReasons[record.Reason]:
		return "This is a problem with the extractor on this computer, not with your map. " +
			"Technical details show what it reported; fix that and press Try again."
	default:
		return "The extractor stopped before converting the map. Technical details show what it " +
			"reported; if it names the map, open it in Auto-Pigeon and save it again."
	}
}

// CurrentRemedy is the remedy to show for a record now.
//
// A failed conversion's remedy is derived again from the recorded error, so a
// run recorded before the Companion could tell a rejected map from a broken
// extractor setup is not still shown advice that blames the map. Every other
// stage keeps the remedy it recorded — the texture refusal's, in particular,
// names AUB's specific reasons and is not re-derivable from the error alone.
func CurrentRemedy(record *Record) string {
	if record.State == Failed && record.FailedAt == Converting && record.Error != "" {
		return convertRemedy(errors.New(record.Error))
	}

	return record.Remedy
}
