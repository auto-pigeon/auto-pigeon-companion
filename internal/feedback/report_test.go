package feedback_test

import (
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/feedback"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/maturity"
)

const today = "2026-09-07"

func input() feedback.Input {
	return feedback.Input{
		EngineFamily:     "quake2",
		Summary:          "Areaportals do not seal in the compiled map",
		Description:      "Two rooms joined by a corridor. The areaportal compiles but both rooms stay in one area.",
		Operation:        "compile",
		CompanionVersion: "0.1.0",
		EditorVersion:    "2026.9.0",
		Platform:         "linux/amd64",
		Profiles: []feedback.ProfileRef{
			{Role: "tool", ID: "auto-pigeon.ericw-tools.q2", Version: "1.0.0", ToolVersion: "2.0.0-alpha7"},
			{Role: "pipeline", ID: "auto-pigeon.q2.normal", Version: "1.0.0"},
			{Role: "game", ID: "quake2"},
		},
		Diagnostics: []feedback.Diagnostic{
			{ID: "texture_missing", Severity: "error", Message: "A texture the map uses was not found under the base game data or the mod.", Count: 14},
			{ID: "builtin_palette", Severity: "warning", Message: "The Quake II palette was not found in the bound game data.", Count: 1},
		},
	}
}

// Nothing is attached by default. A surface that forgot to pass the user's
// choices sends the user's own words and nothing else, which is the safe
// failure.
func TestNothingIsAttachedWithoutConsent(t *testing.T) {
	report, err := feedback.Build(input(), feedback.Consent{}, today)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if report.Versions != nil {
		t.Errorf("versions were attached with no consent: %+v", report.Versions)
	}
	if report.Operation != "" {
		t.Errorf("the operation was attached with no consent: %q", report.Operation)
	}
	if len(report.Profiles) != 0 {
		t.Errorf("%d profiles were attached with no consent", len(report.Profiles))
	}
	if len(report.Diagnostics) != 0 {
		t.Errorf("%d diagnostics were attached with no consent", len(report.Diagnostics))
	}
	if report.Summary == "" {
		t.Error("the user's own summary was dropped; it is the reason the report exists")
	}
	if !report.Shared.Nothing() {
		t.Error("the report claims something was shared")
	}
}

// And each flag attaches exactly its own thing.
func TestEachConsentFlagAttachesOnlyItsOwnPart(t *testing.T) {
	cases := []struct {
		name    string
		consent feedback.Consent
		check   func(feedback.Report) string
	}{
		{"versions", feedback.Consent{Versions: true}, func(r feedback.Report) string {
			if r.Versions == nil || r.Versions.Companion != "0.1.0" {
				return "the versions did not arrive"
			}
			if r.Operation != "" || len(r.Profiles) > 0 || len(r.Diagnostics) > 0 {
				return "something else came with them"
			}
			return ""
		}},
		{"operation", feedback.Consent{Operation: true}, func(r feedback.Report) string {
			if r.Operation != "compile" {
				return "the operation did not arrive"
			}
			if r.Versions != nil || len(r.Profiles) > 0 || len(r.Diagnostics) > 0 {
				return "something else came with it"
			}
			return ""
		}},
		{"profiles", feedback.Consent{Profiles: true}, func(r feedback.Report) string {
			if len(r.Profiles) != 3 {
				return "the profiles did not arrive"
			}
			if r.Versions != nil || r.Operation != "" || len(r.Diagnostics) > 0 {
				return "something else came with them"
			}
			return ""
		}},
		{"diagnostics", feedback.Consent{Diagnostics: true}, func(r feedback.Report) string {
			if len(r.Diagnostics) != 2 {
				return "the diagnostics did not arrive"
			}
			if r.Versions != nil || r.Operation != "" || len(r.Profiles) > 0 {
				return "something else came with them"
			}
			return ""
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			report, err := feedback.Build(input(), c.consent, today)
			if err != nil {
				t.Fatalf("%v", err)
			}
			if problem := c.check(report); problem != "" {
				t.Errorf("%s: %s", c.name, problem)
			}
		})
	}
}

// The sanitization requirement, stated as the thing it is: a report carrying a
// credential, a machine path, a home directory or a network address is refused
// — at every consent setting, because there is no setting that allows one.
func TestAReportCarryingASecretAPathOrAnAddressIsRefused(t *testing.T) {
	everything := feedback.Consent{Versions: true, Profiles: true, Operation: true, Diagnostics: true}
	poisons := map[string]string{
		"a JSON Web Token":      "it failed with token=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2ln",
		"an absolute path":      "the map is at /home/andrea/maps/secret/level.map",
		"a Windows path":        `the map is at C:\Users\andrea\maps\level.map`,
		"a home directory":      "~/quake2/baseq2 is where I keep it",
		"a loopback address":    "the backend at 127.0.0.1:9190 refused it",
		"a LAN address":         "the backend at 192.168.0.33:9190 refused it",
		"a password in a URL":   "I fetched https://andrea:hunter2@example.org/pak0.pak",
		"an API key assignment": "api_key = 9f8c1d2e3b4a5f60718293a4b5c6d7e8",
	}
	for name, text := range poisons {
		t.Run(name, func(t *testing.T) {
			in := input()
			in.Description = text
			if _, err := feedback.Build(in, everything, today); err == nil {
				t.Fatalf("a report containing %s was accepted", name)
			}
			// And in the summary, which is the field that is always shared.
			in = input()
			in.Summary = text
			if _, err := feedback.Build(in, feedback.Consent{}, today); err == nil {
				t.Fatalf("a summary containing %s was accepted with no consent at all", name)
			}
		})
	}
}

// A diagnostic carries the profile's own message. A caller that tried to put
// the tool's line in it — which is where a user's filenames actually are — is
// refused by the same rule.
func TestADiagnosticCannotCarryTheToolsOutput(t *testing.T) {
	in := input()
	in.Diagnostics = []feedback.Diagnostic{{
		ID:       "texture_missing",
		Severity: "error",
		Message:  "WARNING: Couldn't locate texture for /home/andrea/maps/unreleased/textures/aucom/wall",
		Count:    1,
	}}
	if _, err := feedback.Build(in, feedback.Consent{Diagnostics: true}, today); err == nil {
		t.Fatal("a diagnostic carrying a machine path was accepted")
	}
}

// There is no member that can hold a map, a log or a file of any kind. This is
// the structural half of "do not auto-upload maps, logs or paths": the filter
// is not what stops it, the absence of anywhere to put it is.
func TestTheReportHasNowhereToPutAFile(t *testing.T) {
	report, err := feedback.Build(input(), feedback.Consent{Versions: true, Profiles: true, Operation: true, Diagnostics: true}, today)
	if err != nil {
		t.Fatalf("%v", err)
	}
	encoded, err := feedback.Encode(report)
	if err != nil {
		t.Fatalf("%v", err)
	}
	// Every member of the encoded document, enumerated. A new one that could
	// hold bytes has to be added here first, which is the review this test is.
	allowed := map[string]bool{
		"schema": true, "created_on": true, "engine_family": true, "maturity": true,
		"summary": true, "description": true, "shared": true, "versions": true,
		"operation": true, "profiles": true, "diagnostics": true,
	}
	var top map[string]any
	if err := jsonUnmarshal(encoded, &top); err != nil {
		t.Fatalf("%v", err)
	}
	for member := range top {
		if !allowed[member] {
			t.Errorf("the report has a member %q that this test has not reviewed", member)
		}
	}
	for _, absent := range []string{"log", "logs", "map", "map_source", "paths", "attachments", "files"} {
		if _, present := top[absent]; present {
			t.Errorf("the report has a member %q, which is somewhere a file could go", absent)
		}
	}
}

// A description long enough to be a pasted log is refused rather than truncated.
// Truncating would mean sending most of a log and calling it a description.
func TestAPastedLogIsRefusedRatherThanTruncated(t *testing.T) {
	in := input()
	in.Description = strings.Repeat("a compiler line that says nothing sensitive. ", 200)
	if _, err := feedback.Build(in, feedback.Consent{}, today); err == nil {
		t.Fatal("a description over the cap was accepted")
	}
}

// The report states what this build told the user about the family, so a
// maintainer can see that the user was warned before they wrote in.
func TestTheReportCarriesTheMaturityThisBuildClaims(t *testing.T) {
	report, err := feedback.Build(input(), feedback.Consent{}, today)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if report.Maturity != string(maturity.WorkInProgress) {
		t.Errorf("a Quake II report says the family is %q", report.Maturity)
	}
	q1 := input()
	q1.EngineFamily = "quake1"
	stable, err := feedback.Build(q1, feedback.Consent{}, today)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if stable.Maturity != string(maturity.Stable) {
		t.Errorf("a Quake 1 report says the family is %q", stable.Maturity)
	}
}

// A report read back from a file is validated exactly as one that was just
// built: a document that arrived from somewhere else has had nobody vouch for
// it.
func TestAReportReadBackIsValidatedAgain(t *testing.T) {
	forged := []byte(`{
      "schema": "aucom.compat-report/1.0",
      "created_on": "2026-09-07",
      "engine_family": "quake2",
      "maturity": "work_in_progress",
      "summary": "it broke",
      "shared": {"versions": false, "profiles": false, "operation": false, "diagnostics": false},
      "versions": {"companion": "0.1.0"}
    }`)
	if _, err := feedback.Decode(forged); err == nil {
		t.Fatal("a report claiming nothing was shared, with versions attached, was accepted")
	} else if !strings.Contains(err.Error(), "did not agree") {
		t.Errorf("the refusal does not say what is wrong:\n%v", err)
	}
}

func jsonUnmarshal(data []byte, target any) error { return jsonUnmarshalImpl(data, target) }
