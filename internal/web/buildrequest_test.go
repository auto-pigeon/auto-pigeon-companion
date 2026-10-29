package web

import (
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/buildintent"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakintent"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// The editor's normal build request (`autopigeon://build/request/<id>`),
// against a fixture AUB that keeps the contract's rules: one GET per request,
// statuses stored only forwards, a terminal state final.

var buildRequestCount int

// editorBuildRequest makes AUB hold a request for the fixture's one map — the
// exact revision, its digest, the given game — and records its link on this
// machine the way `game open` does. change edits AUB's copy before it is held.
func (m *machine) editorBuildRequest(intent, game string, change func(map[string]any)) aub.BuildRequestLink {
	m.t.Helper()
	buildRequestCount++
	link := aub.BuildRequestLink{RequestID: fmt.Sprintf("%032x", 0xb0000+buildRequestCount), Intent: intent}
	now := time.Now().UTC()
	request := map[string]any{
		"request_id": link.RequestID, "intent": intent, "map_id": m.backend.asset.assetID,
		"revision": m.backend.asset.revision, "revision_id": m.backend.asset.revisionID,
		"content_sha256": m.backend.asset.digest(), "game": game, "map_name": m.backend.asset.name,
		"state": "pending", "sequence": 0,
		"created_at": now.Format(time.RFC3339), "updated_at": now.Format(time.RFC3339),
		"expires_at": now.Add(30 * time.Minute).Format(time.RFC3339),
	}
	if change != nil {
		change(request)
	}
	m.backend.mu.Lock()
	if m.backend.buildRequests == nil {
		m.backend.buildRequests = map[string]map[string]any{}
	}
	m.backend.buildRequests[link.RequestID] = request
	m.backend.mu.Unlock()
	m.receiveBuildRequest(link)
	return link
}

func (m *machine) receiveBuildRequest(link aub.BuildRequestLink) {
	m.t.Helper()
	dir, err := m.server.configDir()
	if err != nil {
		m.t.Fatal(err)
	}
	if err := buildintent.Receive(buildintent.Path(dir), link, time.Now().UTC()); err != nil {
		m.t.Fatal(err)
	}
}

func (m *machine) pendingBuildLink() *aub.BuildRequestLink {
	m.t.Helper()
	request, _, err := m.server.pendingBuildRequest()
	if err != nil {
		m.t.Fatal(err)
	}
	return request
}

// buildStatusesSoon waits until AUB has stored `want` statuses.
func (m *machine) buildStatusesSoon(want int) []string {
	deadline := time.Now().Add(10 * time.Second)
	for {
		m.backend.mu.Lock()
		got := append([]string(nil), m.backend.buildStatuses...)
		m.backend.mu.Unlock()
		if len(got) >= want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// buildStatusSoon waits until AUB has stored one particular status.
func (m *machine) buildStatusSoon(want string) []string {
	deadline := time.Now().Add(10 * time.Second)
	for {
		m.backend.mu.Lock()
		got := append([]string(nil), m.backend.buildStatuses...)
		m.backend.mu.Unlock()
		if containsString(got, want) || time.Now().After(deadline) {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// buildStatusesSettled is every stored status once nothing new has arrived for
// a moment: what a test reads to say nothing MORE was sent.
func (m *machine) buildStatusesSettled() []string {
	var last []string
	for quiet := 0; quiet < 10; {
		time.Sleep(30 * time.Millisecond)
		m.backend.mu.Lock()
		got := append([]string(nil), m.backend.buildStatuses...)
		m.backend.mu.Unlock()
		if len(got) == len(last) {
			quiet++
		} else {
			quiet = 0
		}
		last = got
	}
	return last
}

func TestABuildRequestIsResolvedWithTheCompanionsOwnSessionAndReceivedOnce(t *testing.T) {
	m := newMachine(t)
	m.backend.savedAPMap("quake1")
	// AUB holds a sequence from a Companion whose clock ran ahead: every
	// status sent from here still goes above it.
	ahead := time.Now().UnixMilli() + 10_000_000
	link := m.editorBuildRequest(aub.BuildIntentBuildRun, "quake1", func(r map[string]any) { r["sequence"] = ahead })

	// The watch answers from this machine alone.
	before := m.backend.count()
	status, body := m.call(http.MethodGet, "/api/v1/build-request/request", nil)
	if status != http.StatusOK || body["request_id"] != link.RequestID || body["intent"] != "build_run" || body["run_after"] != true {
		t.Fatalf("local request: %d %v", status, body)
	}
	if m.backend.count() != before {
		t.Fatalf("the watch asked the account server %d time(s)", m.backend.count()-before)
	}

	status, body = m.call(http.MethodGet, "/api/v1/build-request/pending", nil)
	if status != http.StatusOK || body["sign_in_required"] != true || body["request_id"] != link.RequestID {
		t.Fatalf("signed out: %d %v", status, body)
	}
	if m.pendingBuildLink() == nil {
		t.Fatal("a sign-in prompt dropped the request")
	}
	m.signIn()
	for range 3 {
		status, body = m.call(http.MethodGet, "/api/v1/build-request/pending?request_id="+link.RequestID, nil)
	}
	if status != http.StatusOK || body["asset_id"] != m.backend.asset.assetID || body["revision_id"] != m.backend.asset.revisionID ||
		body["revision"] != float64(m.backend.asset.revision) || body["content_sha256"] != m.backend.asset.digest() ||
		body["game_profile"] != "quake1" || body["run_after"] != true || body["name"] != m.backend.asset.name ||
		body["source_file"] != "fixture.apmap" || body["preferred_tool"] != builtin.EricwAutoPigeonQ1 ||
		body["normal_pipeline"] != builtin.Q1Normal {
		t.Fatalf("resolved: %d %v", status, body)
	}
	if got := m.buildStatusesSettled(); strings.Join(got, "|") != link.RequestID+" received" {
		t.Fatalf("three resolves told the editor %v, want `received` once", got)
	}

	if status, body := m.call(http.MethodPost, "/api/v1/build-request/review", map[string]any{"request_id": link.RequestID}); status != http.StatusOK {
		t.Fatalf("review: %d %v", status, body)
	}
	got := m.buildStatusesSoon(2)
	if len(got) != 2 || got[1] != link.RequestID+" reviewing" {
		t.Fatalf("statuses %v", got)
	}
	m.backend.mu.Lock()
	bodies := append([]map[string]any(nil), m.backend.buildStatusBodies...)
	m.backend.mu.Unlock()
	if first, _ := bodies[0]["sequence"].(float64); first <= float64(ahead) {
		t.Fatalf("a status went out at sequence %v, below AUB's %d", first, ahead)
	}
	if status, _ := m.call(http.MethodPost, "/api/v1/build-request/review", map[string]any{"request_id": strings.Repeat("f", 32)}); status != http.StatusConflict {
		t.Fatalf("reviewing a request that is not pending: %d", status)
	}
}

// AUB's 404 is unknown, expired, another account's, or a map no longer
// readable. The page is told the account cannot find it, and the request stays
// so a sign-in to the right account can resolve it.
func TestABuildRequestAUBCannotFindIsKept(t *testing.T) {
	m := newMachine(t)
	m.backend.savedAPMap("quake1")
	m.signIn()
	link := aub.BuildRequestLink{RequestID: strings.Repeat("d", 32), Intent: aub.BuildIntentBuild}
	m.receiveBuildRequest(link)
	status, body := m.call(http.MethodGet, "/api/v1/build-request/pending", nil)
	if status != http.StatusOK || body["not_found"] != true || body["request_id"] != link.RequestID {
		t.Fatalf("not found: %d %v", status, body)
	}
	if m.pendingBuildLink() == nil {
		t.Fatal("a request AUB could not find was forgotten")
	}
	if got := m.buildStatusesSettled(); len(got) != 0 {
		t.Fatalf("told the editor %v about a request this account cannot see", got)
	}
}

// Every refusal is said to the editor as `blocked` with why, and nothing is
// taken into a review.
func TestABuildRequestThatDoesNotMatchIsBlocked(t *testing.T) {
	m := newMachine(t)
	m.backend.savedAPMap("quake1")
	m.signIn()
	for _, tc := range []struct {
		name, game, stage string
		change            func(map[string]any)
	}{
		{"intent", "quake1", buildStageIntentMismatch, func(r map[string]any) { r["intent"] = "build" }},
		{"digest", "quake1", buildStageRevisionMismatch, func(r map[string]any) { r["content_sha256"] = strings.Repeat("0", 64) }},
		{"number", "quake1", buildStageRevisionMismatch, func(r map[string]any) { r["revision"] = 3 }},
		{"game", "quake2", buildStageProfileMismatch, nil},
	} {
		link := m.editorBuildRequest(aub.BuildIntentBuildRun, tc.game, tc.change)
		status, body := m.call(http.MethodGet, "/api/v1/build-request/pending", nil)
		if status != http.StatusConflict || body["code"] != "blocked" {
			t.Fatalf("%s: %d %v", tc.name, status, body)
		}
		want := link.RequestID + " blocked " + tc.stage
		if got := m.buildStatusSoon(want); !containsString(got, want) {
			t.Fatalf("%s: statuses %v, want %q", tc.name, got, want)
		}
	}
}

// NEW_368 (post-push review): the editor opened its request again, and a
// build of it started from an earlier link meanwhile. AUB answers this
// delivery as blocked; nothing is offered, nothing is posted, and the reason
// is the real one. The acknowledgement still names this Companion: it did
// receive this press.
func TestADeliveryReadAfterABuildStartedElsewhereOffersNoSecondBuild(t *testing.T) {
	m := newMachine(t)
	m.backend.savedAPMap("quake1")
	m.signIn()
	link := m.editorBuildRequest(aub.BuildIntentBuildRun, "quake1", func(r map[string]any) {
		r["state"], r["stage"], r["sequence"] = "blocked", aub.BuildStageBuildingElsewhere, 7
	})
	status, body := m.call(http.MethodGet, "/api/v1/build-request/pending", nil)
	if status != http.StatusConflict || body["code"] != "building_elsewhere" || !strings.Contains(fmt.Sprint(body["error"]), "earlier link") {
		t.Fatalf("pending: %d %v", status, body)
	}
	for _, line := range m.buildStatusesSettled() {
		if strings.HasPrefix(line, link.RequestID) {
			t.Fatalf("a status was posted for a delivery that is building elsewhere: %v", line)
		}
	}
}

// A game Build & Run does not build is said once, plainly, and never built as
// Quake 1.
func TestABuildRequestForAnUnsupportedGameIsAnsweredNotGuessed(t *testing.T) {
	m := newMachine(t)
	m.backend.savedAPMap("quake3")
	m.signIn()
	link := m.editorBuildRequest(aub.BuildIntentBuild, "", nil)
	status, body := m.call(http.MethodGet, "/api/v1/build-request/pending", nil)
	if status != http.StatusOK || body["unsupported"] != true || body["game_profile"] != "quake3" ||
		strings.Join(stringsOf(body["supported_profiles"]), ",") != "quake1,quake2" || body["revision_id"] != nil {
		t.Fatalf("unsupported: %d %v", status, body)
	}
	if got := m.buildStatusesSoon(1); strings.Join(got, "|") != link.RequestID+" blocked "+buildStageUnsupported {
		t.Fatalf("statuses %v", got)
	}
}

// A request replaced while the page asks about the older one is answered
// "replaced"; Dismiss forgets only the request it names.
func TestABuildRequestIsReplacedAndDismissedByName(t *testing.T) {
	m := newMachine(t)
	m.backend.savedAPMap("quake1")
	m.signIn()
	older := m.editorBuildRequest(aub.BuildIntentBuild, "quake1", nil)
	newer := m.editorBuildRequest(aub.BuildIntentBuildRun, "quake1", nil)
	status, body := m.call(http.MethodGet, "/api/v1/build-request/pending?request_id="+older.RequestID, nil)
	if status != http.StatusOK || body["replaced"] != true || body["request_id"] != newer.RequestID || body["revision_id"] != nil {
		t.Fatalf("replaced: %d %v", status, body)
	}
	if status, body := m.call(http.MethodPost, "/api/v1/build-request/dismiss", map[string]any{"request_id": older.RequestID}); status != http.StatusOK || body["dismissed"] != false {
		t.Fatalf("dismissing the older request: %d %v", status, body)
	}
	if pending := m.pendingBuildLink(); pending == nil || *pending != newer {
		t.Fatalf("a dismissal of the older request removed %+v", pending)
	}
	if status, body := m.call(http.MethodPost, "/api/v1/build-request/dismiss", map[string]any{"request_id": newer.RequestID}); status != http.StatusOK || body["dismissed"] != true {
		t.Fatalf("dismiss: %d %v", status, body)
	}
	if m.pendingBuildLink() != nil {
		t.Fatal("a dismissed request is still pending")
	}
	if got := m.buildStatusesSoon(1); strings.Join(got, "|") != newer.RequestID+" cancelled "+buildStageDismissed {
		t.Fatalf("statuses %v", got)
	}
	// Nothing touched the leak flow's file.
	dir, _ := m.server.configDir()
	if request, _, _ := leakintent.Read(leakintent.Path(dir), time.Now().UTC()); request != nil {
		t.Fatalf("a build request reached the leak-test intent: %+v", request)
	}
}

// The press is the authorization: a start naming the pending request, for its
// exact revision and what the editor asked for, consumes it once, runs the
// ordinary Build & Run, and tells the editor `building` and then how it ended —
// once. A start that does not match starts nothing.
func TestAStartForABuildRequestConsumesItOnceAndReportsTheRun(t *testing.T) {
	previous := buildRunPoll
	buildRunPoll = 20 * time.Millisecond
	t.Cleanup(func() { buildRunPoll = previous })
	m := newMachine(t)
	m.preparePlay(t)
	runs := func() int {
		_, list := m.call(http.MethodGet, "/api/v1/play/runs", nil)
		items, _ := list["items"].([]any)
		return len(items)
	}
	// The fixture map is a `.map`, which the review cannot read a game from,
	// so what resolving would have found is put where resolving keeps it.
	resolve := func(link aub.BuildRequestLink) {
		m.server.buildReqs.resolvedAs(buildRequestFacts{Link: link, AssetID: m.backend.asset.assetID,
			Revision: m.backend.asset.revision, RevisionID: m.backend.asset.revisionID,
			ContentSHA256: m.backend.asset.digest(), SourceFile: m.backend.asset.fileName, Game: "quake1"}, 0, aub.BuildRequestPending)
	}
	start := func(link aub.BuildRequestLink, change func(map[string]any)) (int, map[string]any) {
		body := playBody(m)
		body["build_request_id"] = link.RequestID
		if change != nil {
			change(body)
		}
		return m.call(http.MethodPost, "/api/v1/play/runs", body)
	}

	// A build-only request never starts an engine.
	buildOnly := m.editorBuildRequest(aub.BuildIntentBuild, "quake1", nil)
	resolve(buildOnly)
	if status, body := start(buildOnly, nil); status != http.StatusConflict || body["code"] != "build_request_mismatch" {
		t.Fatalf("Build & Run for a build-only request: %d %v", status, body)
	}

	link := m.editorBuildRequest(aub.BuildIntentBuildRun, "quake1", nil)
	resolve(link)
	for name, change := range map[string]func(map[string]any){
		"another request":   func(b map[string]any) { b["build_request_id"] = strings.Repeat("e", 32) },
		"the older request": func(b map[string]any) { b["build_request_id"] = buildOnly.RequestID },
		"another revision":  func(b map[string]any) { b["revision_id"] = "rev-000003" },
		"another number":    func(b map[string]any) { b["revision_number"] = 3 },
		"another map":       func(b map[string]any) { b["asset_id"] = "map-other" },
		"build only":        func(b map[string]any) { delete(b, "engine"); delete(b, "action"); b["build_only"] = true },
	} {
		if status, body := start(link, change); status != http.StatusConflict || !strings.HasPrefix(fmt.Sprint(body["code"]), "build_request") {
			t.Fatalf("%s: %d %v", name, status, body)
		}
	}
	if runs() != 0 || m.pendingBuildLink() == nil {
		t.Fatalf("a refused start started %d run(s) or took the request", runs())
	}

	status, started := start(link, nil)
	if status != http.StatusAccepted {
		t.Fatalf("start: %d %v", status, started)
	}
	runID, _ := started["id"].(string)
	if m.pendingBuildLink() != nil {
		t.Fatal("the started request is still pending")
	}
	// The same press again is pointed at the run, never a second one.
	status, again := start(link, nil)
	if status != http.StatusConflict || (again["code"] != "already_running" && again["code"] != "build_request_used") ||
		(again["code"] == "already_running" && again["run_id"] != runID) {
		t.Fatalf("a second press: %d %v", status, again)
	}
	run := m.awaitPlay(t, runID)
	if run["state"] != "succeeded" {
		t.Fatalf("the run %v", run)
	}
	if runs() != 1 {
		t.Fatalf("%d runs for one request", runs())
	}
	got := m.buildStatusesSettled()
	var mine []string
	for _, line := range got {
		if strings.HasPrefix(line, link.RequestID) {
			mine = append(mine, strings.TrimPrefix(line, link.RequestID+" "))
		}
	}
	if len(mine) < 2 || !strings.HasPrefix(mine[0], "building") || mine[len(mine)-1] != "completed engine_started" {
		t.Fatalf("the editor was told %v", mine)
	}
	for _, line := range mine[:len(mine)-1] {
		if !strings.HasPrefix(line, "building") {
			t.Fatalf("the editor was told %v", mine)
		}
	}
	m.backend.mu.Lock()
	var sequences []float64
	var last map[string]any
	for _, body := range m.backend.buildStatusBodies {
		if body["run_id"] == runID {
			sequences = append(sequences, body["sequence"].(float64))
			last = body
		}
	}
	m.backend.mu.Unlock()
	for i := 1; i < len(sequences); i++ {
		if sequences[i] <= sequences[i-1] {
			t.Fatalf("sequences %v do not only grow", sequences)
		}
	}
	if last["build_id"] == "" || last["build_id"] == nil {
		t.Fatalf("the ending names no build: %v", last)
	}

	// A late status after the ending is not sent: the ending is final.
	posts := func() int {
		m.backend.mu.Lock()
		defer m.backend.mu.Unlock()
		return m.backend.buildStatusPosts
	}
	before := posts()
	m.server.buildReqs.report(link, aub.BuildRequestReviewing, "", "", "")
	m.server.buildReqs.report(link, aub.BuildRequestBuilding, "compiling", "", runID)
	if m.buildStatusesSettled(); posts() != before {
		t.Fatalf("%d status(es) were sent after the ending", posts()-before)
	}
	// And the request, used, starts nothing again.
	if status, body := start(link, nil); status != http.StatusConflict || body["code"] != "build_request_used" {
		t.Fatalf("a used request: %d %v", status, body)
	}
	if runs() != 1 {
		t.Fatalf("%d runs for one request", runs())
	}
}

// A status AUB cannot take now is retried with backoff and never blocks the
// page: the resolve answers at once, and the editor is told when AUB is back.
func TestABuildRequestStatusIsRetriedWithoutBlockingThePage(t *testing.T) {
	m := newMachine(t)
	m.backend.savedAPMap("quake1")
	m.signIn()
	m.backend.mu.Lock()
	m.backend.failBuildStatus = 1
	m.backend.mu.Unlock()
	link := m.editorBuildRequest(aub.BuildIntentBuild, "quake1", nil)
	if status, body := m.call(http.MethodGet, "/api/v1/build-request/pending", nil); status != http.StatusOK || body["revision_id"] == nil {
		t.Fatalf("pending: %d %v", status, body)
	}
	if got := m.buildStatusesSoon(1); strings.Join(got, "|") != link.RequestID+" received" {
		t.Fatalf("after one refusal the editor was told %v", got)
	}
}

// The page's notice: answers fenced by generation, and the association-drop
// rule, run by node on the shipped script (testdata/buildrequest.check.mjs).
func TestTheBuildRequestNoticeBehavesAsShipped(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed; the build-request notice was NOT exercised")
	}
	out, err := exec.Command("node", "testdata/buildrequest.check.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("buildrequest.check.mjs: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
