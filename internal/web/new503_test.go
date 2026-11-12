package web

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle/fixturewad"
)

// AUCOM #24 (NEW_503): an editor's leak test that FOUND a leak worked. Its
// compile step still ends failed — the truthful state of a compile that wrote
// no BSP — but nothing raises an incident for it, the build list does not call
// it failed, and the result goes back to the editor with its route. A compiler
// that stopped on an error is still the failure it always was, and is still
// reported; a sealed map raises nothing because nothing failed.
func TestAFoundLeakIsAResultAndOnlyACompilerFailureIsAnIncident(t *testing.T) {
	for _, c := range []struct {
		name, room   string
		state        string
		reported     bool
		leakFound    bool
		wantPoints   bool
		wantReturned bool
	}{
		{name: "a leak with a point file", room: q1Room("n503.wad", true, "n503_floor"),
			state: "failed", leakFound: true, wantPoints: true, wantReturned: true},
		{name: "a sealed map", room: q1Room("n503.wad", false, "n503_floor"),
			state: "succeeded", wantReturned: true},
		{name: "a compiler that stopped on an error",
			room:  "{\n\"classname\" \"worldspawn\"\n\"wad\" \"n503.wad\"\n\"_aucom_test_fails\" \"1\"\n}\n",
			state: "failed", reported: true, wantReturned: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			m, _ := newLeakMachine(t)
			var mu sync.Mutex
			var reported []*job.Job
			m.server.reportJobFailure = func(j *job.Job) {
				mu.Lock()
				defer mu.Unlock()
				reported = append(reported, j)
			}
			wad := fixturewad.WAD("n503_floor", 8)
			m.saveRoom("new503", 3, c.room,
				leakBundle("new503", 3, []string{"n503.wad"}, map[string][]byte{"n503.wad": wad}))
			link := m.receiveLeakLink(strings.Repeat("5", 32), "quake1")
			run := m.startLeak(link, nil)
			if run.status != http.StatusAccepted || run.manifest["state"] != c.state {
				t.Fatalf("the build: %d %v state=%v", run.status, run.body, run.manifest["state"])
			}
			id := run.manifest["build_id"].(string)

			mu.Lock()
			got := append([]*job.Job(nil), reported...)
			mu.Unlock()
			if c.reported != (len(got) == 1) || len(got) > 1 {
				t.Fatalf("incidents raised: %d, want reported=%v", len(got), c.reported)
			}
			if c.reported && (got[0].State != job.Failed || got[0].ExitCode == nil || *got[0].ExitCode != 1) {
				t.Fatalf("the reported job: %+v", got[0])
			}

			// The exact request and revision travel back, with the route.
			m.backend.mu.Lock()
			returned, gotID := m.backend.leakResult, m.backend.leakRequestID
			m.backend.mu.Unlock()
			if c.wantReturned && (gotID != link.RequestID || returned["revision"] != float64(3) ||
				returned["revision_id"] != "new503-rev-3" || returned["map_id"] != "new503") {
				t.Fatalf("AUB received request %q result %v", gotID, returned)
			}
			if points, _ := returned["pointfile"].(string); c.wantPoints != (points != "") {
				t.Fatalf("the returned route: %q", points)
			}
			if _, body := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+id+"/return", nil); body["state"] != "returned" {
				t.Fatalf("the return: %v", body)
			}

			// The list says what was found, never "failed" for a leak.
			_, list := m.call(http.MethodGet, "/api/v1/build/runs?limit=5", nil)
			items, _ := list["items"].([]any)
			if len(items) == 0 {
				t.Fatal("no builds listed")
			}
			first := items[0].(map[string]any)
			if found, _ := first["leak_found"].(bool); found != c.leakFound {
				t.Fatalf("leak_found = %v, want %v (%v)", first["leak_found"], c.leakFound, first)
			}
		})
	}
}

// AUCOM #22 (NEW_503): a link with no request id is listed as unusable, is not
// acknowledged or relayed as a request, and Dismiss forgets it and nothing else.
func TestALeakLinkWithNoRequestIDIsListedAsUnusableAndDismissedAlone(t *testing.T) {
	m := newMachine(t)
	m.backend.savedAPMap("quake1")
	m.signIn()
	valid := m.receiveLeakLink(strings.Repeat("6", 32), "quake1")
	m.receiveLeakLink("", "")

	_, listed := m.call(http.MethodGet, "/api/v1/leak-test/request", nil)
	requests, _ := listed["requests"].([]any)
	if len(requests) != 2 {
		t.Fatalf("listed: %v", listed)
	}
	var unusable, usable map[string]any
	for _, entry := range requests {
		item := entry.(map[string]any)
		if item["request_id"] == "" {
			unusable = item
		} else {
			usable = item
		}
	}
	if unusable == nil || unusable["malformed"] != "no_request_id" || unusable["relay"] != nil {
		t.Fatalf("the link with no id: %v", unusable)
	}
	if usable == nil || usable["request_id"] != valid.RequestID || usable["malformed"] != nil {
		t.Fatalf("the link with an id: %v", usable)
	}

	if status, body := m.call(http.MethodPost, "/api/v1/leak-test/dismiss", map[string]any{"without_id": true}); status != http.StatusOK || body["dismissed"] != true {
		t.Fatalf("dismiss without id: %d %v", status, body)
	}
	_, listed = m.call(http.MethodGet, "/api/v1/leak-test/request", nil)
	requests, _ = listed["requests"].([]any)
	if len(requests) != 1 || requests[0].(map[string]any)["request_id"] != valid.RequestID {
		t.Fatalf("after the dismiss: %v", listed)
	}
	// And the one with an id is still a request a build can answer.
	if pending := m.server.pendingLeakRequest(valid.RequestID); pending == nil {
		t.Fatal("dismissing the bad link forgot the good one")
	}
}

// The verdict that decides "no incident" and the badge is read from the real
// Q3Map2 outputs: a leak, a sealed map and a map with nothing to flood from are
// answers; a compiler that stopped on an error gave none (NEW_503).
func TestALeakVerdictIsAnAnswerOnlyWhenTheRunGaveOne(t *testing.T) {
	m := newMachine(t)
	for _, c := range []struct {
		id, control string
		state       job.State
		lin, bsp    bool
		want        string
	}{
		{"20261112T000000Z-00000001", "b_gap", job.Failed, true, false, "leak"},
		{"20261112T000000Z-00000002", "a_sealed", job.Succeeded, false, true, "no_leak"},
		{"20261112T000000Z-00000003", "d_no_occupant", job.Failed, false, false, "no_interior"},
		{"20261112T000000Z-00000004", "f_malformed", job.Failed, false, false, ""},
		{"20261112T000000Z-00000005", "a_sealed", job.Cancelled, false, false, ""},
	} {
		manifest := m.q3LeakBuild(c.id, c.control, c.state, c.lin, c.bsp)
		if got := m.server.leakVerdict(manifest); got != c.want {
			t.Errorf("%s as %s: verdict %q, want %q", c.control, c.state, got, c.want)
		}
		if found := m.server.leakFound(manifest); found != (c.want == "leak") {
			t.Errorf("%s as %s: leakFound %v", c.control, c.state, found)
		}
	}
	_, list := m.call(http.MethodGet, "/api/v1/build/runs?limit=10", nil)
	verdicts := map[string]any{}
	for _, entry := range list["items"].([]any) {
		item := entry.(map[string]any)
		verdicts[item["manifest"].(map[string]any)["build_id"].(string)] = item["leak_verdict"]
	}
	if verdicts["20261112T000000Z-00000001"] != "leak" || verdicts["20261112T000000Z-00000003"] != "no_interior" ||
		verdicts["20261112T000000Z-00000004"] != nil || verdicts["20261112T000000Z-00000002"] != nil {
		t.Fatalf("the list's verdicts: %v", verdicts)
	}
}
