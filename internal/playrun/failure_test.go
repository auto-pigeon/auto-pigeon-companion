package playrun

import (
	"errors"
	"strings"
	"testing"
)

// The live 246I1.1 failure, verbatim in shape: a wrapper line, the
// extractor's version line, its error, the terminal record and an incident.
const liveExtractorFailure = `the input "map": converting it to a .map: auto-pigeon-extractor convert failed with exit code 2: auto-pigeon-extractor version 9.9
error: APMap contract: APMAP_SCHEMA_DIR is not configured
AUE-TERMINAL/1.0 {"schema_version":"aue-terminal/1.0","outcome":"failed","reason":"internal_error","code":"aue.job_failed","message":"The extractor failed unexpectedly.","detail":"the APMap contract could not be loaded"}
{"code":"aue.job_failed","event":"incident.raised"}`

func TestTheSummaryIsTheExtractorsOwnSentence(t *testing.T) {
	got := Summary(liveExtractorFailure)
	want := "The extractor failed unexpectedly: the APMap contract could not be loaded."
	if got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
	if strings.Contains(got, "AUE-TERMINAL") || strings.Contains(got, "{") {
		t.Errorf("the summary carries raw output: %q", got)
	}
}

func TestTheSummaryOfAnOrdinaryErrorIsItsFirstLine(t *testing.T) {
	if got := Summary("qbsp exited 1\nline two\nline three"); got != "qbsp exited 1" {
		t.Fatalf("summary = %q", got)
	}
	if got := Summary(strings.Repeat("x", 500)); len(got) > 250 || !strings.HasSuffix(got, "…") {
		t.Fatalf("a long line is not bounded: %d bytes", len(got))
	}
}

// A missing setting on this computer must not be blamed on the map.
func TestAnExtractorSetupFailureDoesNotBlameTheMap(t *testing.T) {
	remedy := remedyFor(Converting, errors.New(liveExtractorFailure))
	if !strings.Contains(remedy, "not with your map") {
		t.Fatalf("remedy = %q", remedy)
	}
	if strings.Contains(remedy, "save it again") {
		t.Errorf("the remedy tells the user to re-save a map that is fine: %q", remedy)
	}
}

func TestAMapTheExtractorRejectsSaysSo(t *testing.T) {
	rejected := `convert failed
AUE-TERMINAL/1.0 {"outcome":"failed","reason":"schema_invalid","message":"The map does not match its schema."}`
	if remedy := remedyFor(Converting, errors.New(rejected)); !strings.Contains(remedy, "save it again") {
		t.Fatalf("remedy = %q", remedy)
	}
}

// A run recorded under the old advice is shown the new advice.
func TestAnOldRecordIsShownTheCurrentRemedy(t *testing.T) {
	record := &Record{State: Failed, FailedAt: Converting, Error: liveExtractorFailure,
		Remedy: "The extractor could not read this map. Open it in Auto-Pigeon and save it again."}
	if got := CurrentRemedy(record); !strings.Contains(got, "not with your map") {
		t.Fatalf("remedy = %q", got)
	}
	texture := &Record{State: Failed, FailedAt: DownloadingTextures, Remedy: "ask the owner for quake101.wad"}
	if got := CurrentRemedy(texture); got != texture.Remedy {
		t.Fatalf("a texture refusal's recorded remedy was replaced: %q", got)
	}
}
