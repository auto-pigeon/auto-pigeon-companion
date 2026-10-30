package playrun

import (
	"errors"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/converter"
)

// What a failed run says to a person, as opposed to what it recorded.
//
// A run records the whole error, because Jobs and the handoff need the exact
// words. The Activity card needs one sentence and a remedy. `AUCOM/AUE/AUB
// 246I1.1` found the card printing a converter's stderr verbatim — exit
// code, the terminal record and an incident envelope, in red — and the
// remedy blaming the map for what was a missing setting on this computer.

// ConverterTerminal is the part of a terminal record a person is shown.
// The reader is internal/converter's: a build that converts a map reads the same
// record (Q3_010), and two readers would be two opinions about one line.
type ConverterTerminal = converter.Terminal

// converterTerminal finds a terminal record inside an error's text.
func converterTerminal(text string) (ConverterTerminal, bool) { return converter.ReadTerminal(text) }

// Reasons the converter gives when the MAP is what it could not use. The last line is the reasons the
// extractor gave, which a run recorded before NEW_371 still carries.
var mapReasons = map[string]bool{
	"invalid_input": true, "validation_failed": true, "unsupported_version": true, "unsupported_game": true,
	"quake2_admission": true, "quake3_map": true, "quake3_surface": true, "input_too_large": true,
	"schema_invalid": true, "geometry_invalid": true, "unsupported_target": true, "unsupported_profile": true,
}

// Reasons given when the problem is the machine or the converter itself.
var setupReasons = map[string]bool{
	"io_error":       true,
	"internal_error": true, "panic": true, "timed_out": true,
	"resource_exhausted": true, "scratch_io_error": true, "worker_killed": true,
}

// Summary is one plain sentence for a recorded error: the converter's own
// message when its terminal record is there, otherwise the first line.
func Summary(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if record, ok := converterTerminal(text); ok && record.Message != "" {
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

// convertRemedy tells a map the converter could not use apart from an
// converter that could not run.
func convertRemedy(cause error) string {
	record, ok := converterTerminal(cause.Error())
	switch {
	case ok && mapReasons[record.Reason]:
		return "The converter could not use this map. Open it in Auto-Pigeon and save it again, " +
			"or report the map to its author."
	case ok && setupReasons[record.Reason]:
		return "This is a problem with the converter on this computer, not with your map. " +
			"Technical details show what it reported; fix that and press Try again."
	default:
		return "The converter stopped before converting the map. Technical details show what it " +
			"reported; if it names the map, open it in Auto-Pigeon and save it again."
	}
}

// retiredNotSentRemedyPrefix is how the pre-`NEW_313A` remedy for a WAD that
// was not sent began. Only the beginning is kept here, to recognise it.
const retiredNotSentRemedyPrefix = "Auto-Pigeon cannot redistribute "

// CurrentRemedy is the remedy to show for a record now.
//
// A failed conversion's remedy is derived again from the recorded error, so a
// run recorded before the Companion could tell a rejected map from a broken
// converter setup is not still shown advice that blames the map. Every other
// stage keeps the remedy it recorded — the texture refusal's, in particular,
// names AUB's specific reasons and is not re-derivable from the error alone.
//
// One recorded texture remedy IS replaced: the sentence written before
// `NEW_313A` for a WAD that was not sent, which went on to say whose the file
// was. That was never known, so a run recorded then is shown the current
// sentence, derived from the refusals the record still holds.
func CurrentRemedy(record *Record) string {
	if record.State == Failed && record.FailedAt == Converting && record.Error != "" {
		return convertRemedy(errors.New(record.Error))
	}
	if record.State == Failed && record.FailedAt == DownloadingTextures && record.Bundle != nil &&
		strings.HasPrefix(record.Remedy, retiredNotSentRemedyPrefix) {
		return compilerRefusalRemedy(record.Bundle.CompilerRefusals, record.WADSources)
	}

	return record.Remedy
}
