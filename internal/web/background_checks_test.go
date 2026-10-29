package web

import (
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
)

// NEW_368: the background checks the page shows in one place.

func TestABackgroundCheckRunsOnceAndItsFailureIsKeptUntilItSucceeds(t *testing.T) {
	var board checkBoard
	if !board.begin("k", checkingPrograms, "") || board.begin("k", checkingPrograms, "") {
		t.Fatal("a check already running was started again")
	}
	board.detail("k", "EricW tools")
	if rows, _ := board.snapshot(); len(rows) != 1 || rows[0].Detail != "EricW tools" {
		t.Fatalf("running: %+v", rows)
	}
	board.end("k", errors.New("vis did not answer"), true)
	rows, failures := board.snapshot()
	if len(rows) != 0 || len(failures) != 1 || failures[0].Error != "vis did not answer" || !failures[0].Retry ||
		failures[0].Kind != checkingPrograms {
		t.Fatalf("after a failure: %+v %+v", rows, failures)
	}
	if !board.begin("k", checkingPrograms, "") {
		t.Fatal("a failed check cannot be retried")
	}
	board.end("k", nil, true)
	if _, failures := board.snapshot(); len(failures) != 0 {
		t.Fatalf("a success left the failure: %+v", failures)
	}
}

// The start-up program check is in the status every tab polls while it runs;
// a tab asking while it runs starts nothing; a failure says why and Retry
// runs it again, once, after which the failure is gone.
func TestTheStartupProgramCheckIsShownOnceToEveryTabAndRetried(t *testing.T) {
	f := newCompatFixture(t)
	modern := f.release("ericw-tools-2.0.0-alpha11", fixtureModern)
	f.bindFolder(modern)
	if _, err := binding.Update(f.m.bindings, func(set *binding.Set) error {
		local, _ := set.Find(ericwQ1)
		local.Compatibility = nil
		return set.Put(local)
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(f.log)
	if state, _ := programState(f.profile(), "vis"); state != "not_checked" {
		t.Fatalf("with no answer on record vis reads %q", state)
	}

	// While a check runs (held open here), every tab sees it and none starts one.
	if !f.m.server.board.begin(checkingPrograms, checkingPrograms, "EricW tools (Quake)") {
		t.Fatal("the check could not be held open")
	}
	for tab := 0; tab < 2; tab++ {
		_, body := f.m.call(http.MethodGet, "/api/v1/tools", nil)
		rows, _ := body["checks"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["kind"] != checkingPrograms {
			t.Fatalf("tab %d: %v", tab, body["checks"])
		}
		f.m.server.RefreshCompatibility()
		f.m.call(http.MethodPost, "/api/v1/checks/retry", map[string]any{})
	}
	if after, _ := os.ReadFile(f.log); len(after) != len(before) {
		t.Fatalf("a check started while one ran:\n%s", after[len(before):])
	}
	f.m.server.board.end(checkingPrograms, errors.New("EricW tools (Quake): vis did not answer"), true)
	_, body := f.m.call(http.MethodGet, "/api/v1/tools", nil)
	failures, _ := body["check_failures"].([]any)
	if len(failures) != 1 || failures[0].(map[string]any)["retry"] != true {
		t.Fatalf("the failure is not shown with Retry: %v", body["check_failures"])
	}

	status, _ := f.m.call(http.MethodPost, "/api/v1/checks/retry", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("retry: %d", status)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, body = f.m.call(http.MethodGet, "/api/v1/tools", nil)
		if body["checks"] == nil && body["check_failures"] == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the retried check did not finish: %v %v", body["checks"], body["check_failures"])
		}
		time.Sleep(20 * time.Millisecond)
	}
	if state, _ := programState(f.profile(), "vis"); state == "not_checked" || !ready(f.profile()) {
		t.Fatalf("the retried check did not record its answers: vis %q", state)
	}
}
