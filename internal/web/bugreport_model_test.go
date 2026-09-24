package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/incident"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
)

// NEW_247H: every report the Companion files is classified — application,
// report type, area — by the shared contract, and only by it.
//
// The page's model (assets/bugreport-model.mjs, the module the dialog imports)
// is run by node against the vendored contract, fed the incidents the real Go
// reporter raises. Every expectation below comes from bug-report-rules.json,
// read here by Go: the labels, the Companion's areas, the incident mapping and
// the headings are the contract's data, never a table in this test. Skipped,
// loudly, without node.

type bugRules struct {
	Components  []string `json:"components"`
	ReportTypes map[string]struct {
		Label    string            `json:"label"`
		Headings map[string]string `json:"headings"`
	} `json:"report_types"`
	Areas        map[string]string `json:"areas"`
	Applications map[string]struct {
		Label string   `json:"label"`
		Areas []string `json:"areas"`
	} `json:"applications"`
	IncidentAreas struct {
		ByCode map[string]string `json:"by_code"`
	} `json:"incident_areas"`
	IncidentReportType string `json:"incident_report_type"`
}

func loadBugRules(t *testing.T) bugRules {
	t.Helper()
	raw, err := fs.ReadFile(assetsFS(), "vendor/incident-contract/schema/bug-report-rules.json")
	if err != nil {
		t.Fatal(err)
	}
	var rules bugRules
	if err := json.Unmarshal(raw, &rules); err != nil {
		t.Fatal(err)
	}
	return rules
}

type modelEntry struct {
	Type          string          `json:"type"`
	Area          string          `json:"area"`
	Errors        []string        `json:"errors"`
	Component     string          `json:"component"`
	Kind          string          `json:"kind"`
	Labels        []string        `json:"labels"`
	Text          string          `json:"text"`
	JSON          string          `json:"json"`
	Document      json.RawMessage `json:"document"`
	Fits          bool            `json:"fits"`
	URLLength     int             `json:"urlLength"`
	PrefillLimit  int             `json:"prefillLimit"`
	URLLabels     *string         `json:"urlLabels"`
	URLBody       string          `json:"urlBody"`
	URLTitle      string          `json:"urlTitle"`
	URLTemplate   *string         `json:"urlTemplate"`
	PrefilledBody string          `json:"prefilledBody"`
	ServerBody    string          `json:"serverBody"`
}

type modelObservations struct {
	Application string `json:"application"`
	Types       []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"types"`
	Areas []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"areas"`
	Cold struct {
		ReportType string `json:"reportType"`
		Area       string `json:"area"`
	} `json:"cold"`
	ColdMissing []string `json:"coldMissing"`
	ColdBuild   struct {
		OK     bool     `json:"ok"`
		Errors []string `json:"errors"`
	} `json:"coldBuild"`
	Headings  map[string]map[string]string `json:"headings"`
	Every     []modelEntry                 `json:"every"`
	Incidents []struct {
		Code  string `json:"code"`
		Start struct {
			ReportType string `json:"reportType"`
			Area       string `json:"area"`
		} `json:"start"`
		Suggested struct {
			Kind        string          `json:"kind"`
			Labels      []string        `json:"labels"`
			Incident    json.RawMessage `json:"incident"`
			Correlation string          `json:"correlation"`
			Errors      []string        `json:"errors"`
		} `json:"suggested"`
		Corrected struct {
			Kind   string   `json:"kind"`
			Labels []string `json:"labels"`
			Text   string   `json:"text"`
			Errors []string `json:"errors"`
		} `json:"corrected"`
	} `json:"incidents"`
	Foreign []struct {
		Area   string   `json:"area"`
		OK     bool     `json:"ok"`
		Errors []string `json:"errors"`
	} `json:"foreign"`
	BadType []string `json:"badType"`
}

// raisedIncidents are what the real reporter offers the page after one failed
// job and one failed readiness check — through the real route.
func raisedIncidents(t *testing.T) []incident.RecentIncident {
	t.Helper()
	reporter := incident.NewReporter(incident.Config{Release: "1.150", Environment: "production"}, incident.Options{})
	exit := 2
	incident.JobHook(reporter, nil)(&job.Job{ID: "job-failed-1", State: job.Failed, ExitCode: &exit}, "")
	reporter.Capture(incident.ReadinessDraft(errors.New("connection refused"), time.Second, 5*time.Second, ""))

	server, err := NewServer(Options{Version: "test", Jobs: newTestJobs(t), Incidents: reporter.Recent})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/api/v1/bug-reports/incidents", nil)
	request.Host = "127.0.0.1"
	request.Header.Set("X-AUCOM-Token", server.token.Value())
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("GET /api/v1/bug-reports/incidents: %d %s", recorder.Code, recorder.Body.String())
	}
	var answer struct {
		Incidents []incident.RecentIncident `json:"incidents"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if len(answer.Incidents) != 2 {
		t.Fatalf("the route offered %d incidents, want the 2 raised: %s", len(answer.Incidents), recorder.Body.String())
	}
	if answer.Incidents[0].Code != incident.CodeReadinessFailed || answer.Incidents[1].Code != incident.CodeJobFailed {
		t.Errorf("not newest first: %+v", answer.Incidents)
	}
	if answer.Incidents[1].JobID != "job-failed-1" {
		t.Errorf("the job failure does not name its job: %+v", answer.Incidents[1])
	}
	for _, forbidden := range []string{"message", "user_action", "release", "environment"} {
		if strings.Contains(recorder.Body.String(), `"`+forbidden+`"`) {
			t.Errorf("the route carries %q; it offers typed fields only: %s", forbidden, recorder.Body.String())
		}
	}
	return answer.Incidents
}

func runBugReportModel(t *testing.T, incidents []incident.RecentIncident) modelObservations {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine; the page's bug-report model was NOT exercised")
	}
	model, err := filepath.Abs("assets/bugreport-model.mjs")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := filepath.Abs("assets/vendor/incident-contract/src/index.mjs")
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{
		"model":     "file://" + filepath.ToSlash(model),
		"contract":  "file://" + filepath.ToSlash(contract),
		"incidents": incidents,
	})
	command := exec.Command(node, "testdata/bugreport-model.check.mjs")
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("running the page's model: %v\n%s", err, stderr.String())
	}
	var observed modelObservations
	if err := json.Unmarshal(output, &observed); err != nil {
		t.Fatalf("the model answered %q: %v", output, err)
	}
	return observed
}

func TestTheCompanionsBugReportsAreClassifiedByTheContract(t *testing.T) {
	rules := loadBugRules(t)
	incidents := raisedIncidents(t)
	observed := runBugReportModel(t, incidents)
	aucom := rules.Applications["AUCOM"]

	// The application is a fixed fact: AUCOM, and nothing else.
	if observed.Application != "AUCOM" || aucom.Label != "AUCOM" {
		t.Fatalf("the page reports as %q (rules label %q)", observed.Application, aucom.Label)
	}

	// The controls offer exactly the contract's types and the Companion's areas, in its order.
	var typeIDs, areaIDs []string
	for _, choice := range observed.Types {
		typeIDs = append(typeIDs, choice.ID)
		if choice.Name != rules.ReportTypes[choice.ID].Label {
			t.Errorf("type %s is shown as %q; the contract's label is %q", choice.ID, choice.Name, rules.ReportTypes[choice.ID].Label)
		}
	}
	for _, choice := range observed.Areas {
		areaIDs = append(areaIDs, choice.ID)
		if "Area: "+choice.Name != rules.Areas[choice.ID] {
			t.Errorf("area %s is shown as %q; the contract's label is %q", choice.ID, choice.Name, rules.Areas[choice.ID])
		}
	}
	if strings.Join(typeIDs, ",") != "bug,feature_request" {
		t.Errorf("types offered: %v", typeIDs)
	}
	if strings.Join(areaIDs, ",") != strings.Join(aucom.Areas, ",") {
		t.Errorf("areas offered %v; the contract gives the Companion %v", areaIDs, aucom.Areas)
	}

	// Cold: nothing chosen, Review refused, and the contract refuses to build it.
	if observed.Cold.ReportType != "" || observed.Cold.Area != "" {
		t.Errorf("a cold report starts preselected: %+v", observed.Cold)
	}
	if strings.Join(observed.ColdMissing, ",") != "report_type,area" {
		t.Errorf("a cold report is missing %v, want both choices", observed.ColdMissing)
	}
	if observed.ColdBuild.OK || !contains(observed.ColdBuild.Errors, "report_type_required") || !contains(observed.ColdBuild.Errors, "area_required") {
		t.Errorf("an unclassified cold report was built: %+v", observed.ColdBuild)
	}

	// Every type x area: exactly three labels, AUCOM's, from the contract's data;
	// one document behind the text, the JSON, the prefilled URL and the server body.
	if len(observed.Every) != len(rules.ReportTypes)*len(aucom.Areas) {
		t.Fatalf("%d combinations observed, want %d", len(observed.Every), len(rules.ReportTypes)*len(aucom.Areas))
	}
	for _, entry := range observed.Every {
		name := entry.Type + "/" + entry.Area
		if len(entry.Errors) > 0 {
			t.Errorf("%s did not build: %v", name, entry.Errors)
			continue
		}
		want := []string{aucom.Label, rules.ReportTypes[entry.Type].Label, rules.Areas[entry.Area]}
		if strings.Join(entry.Labels, "|") != strings.Join(want, "|") {
			t.Errorf("%s is labelled %v, want %v", name, entry.Labels, want)
		}
		if entry.Component != "AUCOM" || entry.Kind != "cold" {
			t.Errorf("%s: component %q kind %q", name, entry.Component, entry.Kind)
		}
		for _, label := range entry.Labels {
			if label == "AUP" || label == "AUG" {
				t.Errorf("%s carries another application's label %q", name, label)
			}
		}
		// Headings follow the type; the other type's never appear.
		headings, other := rules.ReportTypes[entry.Type].Headings, rules.ReportTypes["bug"].Headings
		if entry.Type == "bug" {
			other = rules.ReportTypes["feature_request"].Headings
		}
		for _, key := range []string{"steps", "expected", "actual"} {
			if !strings.Contains(entry.Text, "\n"+headings[key]+"\n") {
				t.Errorf("%s: the text has no %q heading", name, headings[key])
			}
			if strings.Contains(entry.Text, "\n"+other[key]+"\n") {
				t.Errorf("%s: the text carries the other type's heading %q", name, other[key])
			}
		}
		// Redaction and bounds are unchanged by the type.
		for _, secret := range []string{"ghp_abcdefghijklmnopqrstuvwxyz0123", "/home/alice", "token=zzz"} {
			if strings.Contains(entry.Text, secret) || strings.Contains(entry.URLBody, secret) || strings.Contains(entry.JSON, secret) {
				t.Errorf("%s: %q survived redaction", name, secret)
			}
		}
		// The prefilled URL: the three labels, inside the bound, no template.
		if entry.URLLabels == nil || *entry.URLLabels != strings.Join(want, ",") {
			t.Errorf("%s: the prefilled URL's labels are %v, want %q", name, entry.URLLabels, strings.Join(want, ","))
		}
		if !entry.Fits || entry.URLLength > entry.PrefillLimit {
			t.Errorf("%s: the prefilled URL is %d long (limit %d, fits %v)", name, entry.URLLength, entry.PrefillLimit, entry.Fits)
		}
		if entry.URLTemplate != nil {
			t.Errorf("%s: the prefilled URL names a template %q", name, *entry.URLTemplate)
		}
		// One document: the JSON download is the document; the URL and the
		// server body both carry the reviewed text.
		var fromJSON, doc any
		_ = json.Unmarshal([]byte(entry.JSON), &fromJSON)
		_ = json.Unmarshal(entry.Document, &doc)
		a, _ := json.Marshal(fromJSON)
		b, _ := json.Marshal(doc)
		if !bytes.Equal(a, b) {
			t.Errorf("%s: the .json download is not the reviewed document", name)
		}
		if entry.URLBody != entry.PrefilledBody || !strings.Contains(entry.URLBody, strings.TrimSuffix(entry.Text, "\n")) ||
			!strings.Contains(entry.ServerBody, strings.TrimSuffix(entry.Text, "\n")) {
			t.Errorf("%s: the prefilled issue or the server body is not the reviewed text", name)
		}
		if !strings.HasPrefix(entry.URLTitle, "[AUCOM] ") {
			t.Errorf("%s: the issue title is %q", name, entry.URLTitle)
		}
	}

	// Incidents the Go reporter raised start where the contract's by_code says,
	// as bugs, and the person can change both before Review.
	if len(observed.Incidents) != len(incidents) {
		t.Fatalf("%d incidents observed, %d raised", len(observed.Incidents), len(incidents))
	}
	for i, entry := range observed.Incidents {
		wantArea := rules.IncidentAreas.ByCode[entry.Code]
		if !contains(aucom.Areas, wantArea) {
			wantArea = "other"
		}
		if entry.Start.ReportType != rules.IncidentReportType || entry.Start.Area != wantArea {
			t.Errorf("%s starts as %+v, want %s/%s", entry.Code, entry.Start, rules.IncidentReportType, wantArea)
		}
		want := []string{"AUCOM", rules.ReportTypes["bug"].Label, rules.Areas[wantArea]}
		if entry.Suggested.Kind != "incident" || strings.Join(entry.Suggested.Labels, "|") != strings.Join(want, "|") {
			t.Errorf("%s as suggested: kind %q labels %v, want incident %v (%v)", entry.Code, entry.Suggested.Kind, entry.Suggested.Labels, want, entry.Suggested.Errors)
		}
		var carried struct {
			IncidentID string `json:"incident_id"`
			Code       string `json:"code"`
		}
		_ = json.Unmarshal(entry.Suggested.Incident, &carried)
		if carried.IncidentID != incidents[i].IncidentID || carried.Code != incidents[i].Code {
			t.Errorf("%s: the report carries incident %+v, not the one raised (%s)", entry.Code, carried, incidents[i].IncidentID)
		}
		if entry.Suggested.Correlation != incidents[i].CorrelationID {
			t.Errorf("%s: correlation %q, raised with %q", entry.Code, entry.Suggested.Correlation, incidents[i].CorrelationID)
		}
		corrected := []string{"AUCOM", rules.ReportTypes["feature_request"].Label, rules.Areas["documentation"]}
		if entry.Corrected.Kind != "incident" || strings.Join(entry.Corrected.Labels, "|") != strings.Join(corrected, "|") {
			t.Errorf("%s corrected: kind %q labels %v, want %v (%v)", entry.Code, entry.Corrected.Kind, entry.Corrected.Labels, corrected, entry.Corrected.Errors)
		}
		if !strings.Contains(entry.Corrected.Text, "\n"+rules.ReportTypes["feature_request"].Headings["steps"]+"\n") {
			t.Errorf("%s corrected to a feature request still renders a bug's headings", entry.Code)
		}
	}
	// The two codes the Companion raises map where the prompt says they do.
	for code, area := range map[string]string{incident.CodeJobFailed: "compile_run", incident.CodeReadinessFailed: "companion"} {
		if rules.IncidentAreas.ByCode[code] != area {
			t.Errorf("the contract maps %s to %q; NEW_247H expects %q", code, rules.IncidentAreas.ByCode[code], area)
		}
	}

	// Another application's area, a label string, or nonsense: refused.
	for _, entry := range observed.Foreign {
		if entry.OK || !contains(entry.Errors, "area_invalid") {
			t.Errorf("area %q was accepted for a Companion report: %+v", entry.Area, entry)
		}
	}
	if !contains(observed.BadType, "report_type_invalid") {
		t.Errorf("a label string was accepted as a report type: %v", observed.BadType)
	}

	// The field labels the form shows are the contract's headings.
	for _, reportType := range []string{"bug", "feature_request"} {
		for key, heading := range rules.ReportTypes[reportType].Headings {
			if observed.Headings[reportType][key] != heading {
				t.Errorf("the %s form labels %s %q; the contract says %q", reportType, key, observed.Headings[reportType][key], heading)
			}
		}
	}
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
