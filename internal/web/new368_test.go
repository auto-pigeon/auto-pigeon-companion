package web

import (
	"net/http"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
)

// NEW_368: the fence an "Update now" of the Companion closes.

// S3: once the fence closes nothing new is admitted, the requests already
// admitted are waited for, and a request that arrives meanwhile is told to
// retry — nothing of it started.
func TestTheUpdateFenceWaitsForAdmittedWorkAndRefusesNewWorkRetryably(t *testing.T) {
	var a admission
	release, ok := a.enter()
	if !ok {
		t.Fatal("an open fence refused work")
	}
	closed := make(chan bool, 1)
	go func() { closed <- a.close(5 * time.Second) }()
	time.Sleep(50 * time.Millisecond)
	if _, ok := a.enter(); ok {
		t.Fatal("a closing fence admitted new work")
	}
	select {
	case <-closed:
		t.Fatal("the fence closed while an admitted request was still running")
	default:
	}
	release()
	if !<-closed {
		t.Fatal("the fence did not close once the admitted request returned")
	}
	// A fence that cannot close in time opens again: the update waits.
	var b admission
	held, _ := b.enter()
	if b.close(50 * time.Millisecond) {
		t.Fatal("closed while a request was in flight")
	}
	if _, ok := b.enter(); !ok {
		t.Fatal("a fence that gave up stayed closed")
	}
	held()
}

// S3: through the server — while the fence is closed, a request that would
// start work gets a retryable 503 with the updating code; reading and the
// page's own lease and Quit routes still answer.
func TestWhileTheCompanionStopsForAnUpdateWorkIsRefusedAndReadsAnswer(t *testing.T) {
	server, _ := newTestServer(t, nil)
	if !server.admission.close(time.Second) {
		t.Fatal("the fence did not close on an idle server")
	}
	response, body := do(t, server, http.MethodPost, "/api/v1/jobs", `{}`)
	if response.StatusCode != http.StatusServiceUnavailable || body["code"] != codeUpdating || response.Header.Get("Retry-After") == "" {
		t.Fatalf("a job while updating: %d %v", response.StatusCode, body)
	}
	if response, _ := do(t, server, http.MethodGet, "/api/status", ""); response.StatusCode != http.StatusOK {
		t.Fatalf("status while updating: %d", response.StatusCode)
	}
	if response, body := do(t, server, http.MethodPost, "/api/lifecycle/quit", `{}`); response.StatusCode == http.StatusServiceUnavailable {
		t.Fatalf("Quit was fenced: %v", body)
	}
	server.admission.reopen()
	if response, body := do(t, server, http.MethodPost, "/api/v1/jobs", `{}`); response.StatusCode == http.StatusServiceUnavailable {
		t.Fatalf("a reopened fence still refuses: %v", body)
	}
}

// S3: RestartForUpdate looks again after the fence: work found then keeps
// the process running and the fence open; an idle server stops for the
// update with the staged record kept for the exit.
func TestRestartForUpdateStopsOnlyAnIdleServer(t *testing.T) {
	server, _ := newTestServer(t, nil)
	server.lifecycle = NewLifecycle(LifecycleOptions{Interactive: true})
	host := selfHost{s: server}
	release := server.toolGate.hold()
	busy, err := host.RestartForUpdate(managedtools.InstallRecord{Version: "1.320"})
	if err != nil || len(busy) == 0 || server.admission.isClosed() {
		t.Fatalf("with a build running: busy %v, err %v, fence closed %v", busy, err, server.admission.isClosed())
	}
	if _, stopped := server.SelfRestartRecord(); stopped {
		t.Fatal("a busy server recorded a restart")
	}
	release()
	busy, err = host.RestartForUpdate(managedtools.InstallRecord{Version: "1.320"})
	if err != nil || len(busy) != 0 {
		t.Fatalf("idle: busy %v err %v", busy, err)
	}
	if record, stopped := server.SelfRestartRecord(); !stopped || record.Version != "1.320" {
		t.Fatal("the restart's record is not kept for the exit")
	}
	if server.lifecycle.Cause() != ExitUpdate {
		t.Fatalf("stopped with %q", server.lifecycle.Cause())
	}
}
