package web

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/autobuild"
)

// NEW_265A, through the HTTP surface the page and `companion autobuild` use,
// with the poller running as `companion serve` runs it and AUB's map-detail
// answer held: the switch answers at once, the question in flight is shown,
// and its late answer builds nothing for a map that was switched off.
func TestTheAutoBuildSwitchAnswersPromptlyWhileAUBIsHeld(t *testing.T) {
	m := newMachine(t)
	m.preparePlay(t)
	asset := "/api/v1/autobuild/" + m.backend.asset.assetID
	service, err := m.server.autobuildService()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.server.StartAutoBuild(ctx)

	await := func(what string, ok func(map[string]any) bool) map[string]any {
		t.Helper()
		limit := time.Now().Add(15 * time.Second)
		for {
			_, body := m.call(http.MethodGet, asset, nil)
			if ok(body) {
				return body
			}
			if time.Now().After(limit) {
				t.Fatalf("%s: never; last %v", what, body)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	timed := func(what, method, path string, body any) map[string]any {
		t.Helper()
		began := time.Now()
		status, answer := m.call(method, path, body)
		took := time.Since(began)
		t.Logf("%s answered %d in %s", what, status, took)
		if status != http.StatusOK || took > time.Second {
			t.Fatalf("%s = %d after %s: %v", what, status, took, answer)
		}
		return answer
	}
	hold := func() chan struct{} {
		gate := make(chan struct{})
		m.backend.mu.Lock()
		m.backend.holdDetail = gate
		m.backend.mu.Unlock()
		return gate
	}
	unhold := func(gate chan struct{}) {
		m.backend.mu.Lock()
		m.backend.holdDetail = nil
		m.backend.mu.Unlock()
		close(gate)
	}
	saveRevision := func(n int) {
		m.backend.mu.Lock()
		m.backend.asset.revisionID, m.backend.asset.revision = "rev-00000"+string(rune('0'+n)), n
		m.backend.asset.body = []byte("{ \"classname\" \"worldspawn\" \"message\" \"revision " + string(rune('0'+n)) + "\" }\n")
		m.backend.mu.Unlock()
	}
	dueNow := func() {
		t.Helper()
		if _, err := autobuild.Update(service.Path(), func(state *autobuild.State) error {
			for i := range state.Entries {
				state.Entries[i].NextCheckAt = time.Time{}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		service.Nudge()
	}
	runs := func() int {
		_, body := m.call(http.MethodGet, "/api/v1/play/runs", nil)
		items, _ := body["items"].([]any)
		return len(items)
	}

	// Switched on while AUB is held: the switch answers at once, and the
	// question the poller asks for the baseline is shown in flight.
	gate := hold()
	body := timed("enable", http.MethodPost, asset+"/enable", map[string]any{"pipeline": "aucom.fixture.pipeline", "display_name": "First Coast"})
	if body["enabled"] != true || body["baseline"] != nil {
		t.Fatalf("after switching on: %v", body)
	}
	await("the baseline question in flight", func(b map[string]any) bool { return b["checking_since"] != nil })
	unhold(gate)
	await("the baseline", func(b map[string]any) bool {
		baseline, _ := b["baseline"].(map[string]any)
		return baseline != nil && baseline["revision"] == float64(4) && b["checking_since"] == nil
	})

	// A new revision is saved and the check is due; AUB is held again. Off
	// answers at once, and the late answer (revision 5) builds nothing.
	gate = hold()
	saveRevision(5)
	dueNow()
	await("the check in flight", func(b map[string]any) bool { return b["checking_since"] != nil })
	body = timed("disable", http.MethodPost, asset+"/disable", nil)
	if body["enabled"] != false || body["checking_since"] != nil {
		t.Fatalf("after switching off: %v", body)
	}
	unhold(gate)
	limit := time.Now().Add(15 * time.Second)
	for {
		if _, asking := service.CheckingNow(m.backend.asset.assetID); !asking {
			break
		}
		if time.Now().After(limit) {
			t.Fatal("the held question never ended")
		}
		time.Sleep(20 * time.Millisecond)
	}
	dueNow()
	time.Sleep(200 * time.Millisecond)
	_, body = m.call(http.MethodGet, asset, nil)
	if body["enabled"] != false || body["pending"] != nil || body["running"] != nil || runs() != 0 {
		t.Fatalf("a map switched off while AUB was slow: %v, %d runs", body, runs())
	}
	if observed, _ := body["observed"].(map[string]any); observed["revision"] != float64(4) {
		t.Errorf("the late answer was recorded: observed %v", observed)
	}

	// On again: a fresh baseline (revision 5, not built), then revision 6 is
	// built once, build-only, with the chosen profile.
	timed("enable again", http.MethodPost, asset+"/enable", map[string]any{"pipeline": "aucom.fixture.pipeline"})
	await("the fresh baseline", func(b map[string]any) bool {
		baseline, _ := b["baseline"].(map[string]any)
		return baseline != nil && baseline["revision"] == float64(5)
	})
	if runs() != 0 {
		t.Fatalf("the fresh baseline was built")
	}
	saveRevision(6)
	dueNow()
	// The attempt is recorded as running BEFORE the run it starts has an id
	// (autobuild records the claim, starts the run, then writes the id), so
	// an attempt is only followed once it names its run.
	named := func(attempt any) bool {
		row, _ := attempt.(map[string]any)
		id, _ := row["run_id"].(string)
		return id != ""
	}
	body = await("the build of revision 6", func(b map[string]any) bool { return named(b["running"]) || named(b["last_built"]) })
	attempt, _ := body["running"].(map[string]any)
	if attempt == nil {
		attempt, _ = body["last_built"].(map[string]any)
	}
	if attempt["revision"].(map[string]any)["revision"] != float64(6) || attempt["pipeline"] != "aucom.fixture.pipeline" {
		t.Fatalf("the build: %v", attempt)
	}
	run := m.awaitPlay(t, attempt["run_id"].(string))
	if run["state"] != "succeeded" || run["build_only"] != true || run["launch"] != nil {
		t.Fatalf("the auto-build run: %v", run)
	}
	dueNow()
	time.Sleep(200 * time.Millisecond)
	if n := runs(); n != 1 {
		t.Fatalf("%d runs, want exactly 1", n)
	}
}
