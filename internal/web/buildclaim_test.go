package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle/fixturewad"
)

// A build's ending, held open (NEW_371D).
//
// A build request is claimed while it runs, and a read of the build and a start
// of the same build must agree about when that stops. They have two windows to
// disagree in. The runner writes the terminal manifest before it returns, so a
// read says "over" before the run is recorded as over; and the run is recorded
// as over before a leak result has gone back over the network. A person who
// reads either and presses Build was told "already running", about a build
// nothing was doing.
//
// These tests hold a build's goroutine in each window and act through the
// handlers. The second half of each is the mirror image: once a build no
// longer holds its request, nothing the old goroutine does on its way out may
// touch the claim of the build that replaced it.

// buildEndings holds the first build to return from its runner, and says when
// each build's goroutine has gone.
type buildEndings struct {
	mu      sync.Mutex
	armed   bool
	arrived chan string
	proceed chan struct{}
	release func()
	exits   map[string]chan struct{}
}

func (m *machine) holdBuildEndings() *buildEndings {
	m.t.Helper()
	proceed := make(chan struct{})
	var once sync.Once
	endings := &buildEndings{armed: true, arrived: make(chan string, 1), proceed: proceed,
		release: func() { once.Do(func() { close(proceed) }) }, exits: map[string]chan struct{}{}}
	m.t.Cleanup(endings.release)
	m.server.builds.passed = endings.passed
	return endings
}

func (e *buildEndings) arm(armed bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.armed = armed
}

func (e *buildEndings) passed(id, point string) {
	switch point {
	case "returned":
		e.mu.Lock()
		take := e.armed
		e.armed = false
		e.mu.Unlock()
		if take {
			e.arrived <- id
			<-e.proceed
		}
	case "exited":
		if id != "" {
			close(e.exit(id))
		}
	}
}

func (e *buildEndings) exit(id string) chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.exits[id] == nil {
		e.exits[id] = make(chan struct{})
	}
	return e.exits[id]
}

func (e *buildEndings) awaitHeld(t *testing.T, want string) {
	t.Helper()
	select {
	case id := <-e.arrived:
		if want != "" && id != want {
			t.Fatalf("held build %s, want %s", id, want)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("no build returned from its runner")
	}
}

func (e *buildEndings) awaitGone(t *testing.T, id string) {
	t.Helper()
	select {
	case <-e.exit(id):
	case <-time.After(30 * time.Second):
		t.Fatalf("build %s's goroutine never finished", id)
	}
}

// gatedBuild is a request whose compile stops at a file: open lets every
// compile of it through, shut stops the next one.
func (m *machine) gatedBuild(name string) (request map[string]any, open, shut func()) {
	m.t.Helper()
	gate := filepath.Join(m.dir, name+"-gate")
	path := filepath.Join(m.content, name+".map")
	writeFixtureFile(m.t, path, "{ \"classname\" \"worldspawn\" }\n"+holdUntilMarker+gate+"\n")
	open = func() {
		if err := os.WriteFile(gate, nil, 0o600); err != nil {
			m.t.Errorf("opening the gate: %v", err)
		}
	}
	shut = func() {
		if err := os.Remove(gate); err != nil {
			m.t.Errorf("shutting the gate: %v", err)
		}
	}
	m.t.Cleanup(open)
	return map[string]any{
		"pipeline": "aucom.fixture.pipeline",
		"inputs":   map[string]string{"source_map": path},
	}, open, shut
}

func (m *machine) startBuild(request map[string]any) string {
	m.t.Helper()
	status, body := m.call(http.MethodPost, "/api/v1/build/runs", request)
	if status != http.StatusAccepted {
		m.t.Fatalf("starting the build = %d: %v", status, body)
	}
	id, _ := body["build"].(string)
	if id == "" {
		m.t.Fatalf("a started build with no id: %v", body)
	}
	return id
}

func (m *machine) awaitCompiling(id string) {
	m.t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		manifest, err := build.Find(m.builds, id)
		if err == nil && len(manifest.Steps) == 1 && manifest.Steps[0].State == job.Running && manifest.Steps[0].JobID != "" {
			return
		}
		if time.Now().After(deadline) {
			m.t.Fatalf("build %s never recorded its stage running", id)
		}
	}
}

// requireRefusedFor is a press of a build that is running: refused, naming it.
func (m *machine) requireRefusedFor(request map[string]any, id string) {
	m.t.Helper()
	status, body := m.call(http.MethodPost, "/api/v1/build/runs", request)
	if status != http.StatusConflict || body["code"] != "already_running" || body["build"] != id {
		m.t.Fatalf("a press of running build %s = %d: %v", id, status, body)
	}
}

func (m *machine) activeBuilds() []string {
	out := []string{}
	for _, work := range m.server.ActiveWork() {
		if work.Kind == "build" {
			out = append(out, work.ID)
		}
	}
	return out
}

// The first window, at each way a build ends: the manifest on disk is terminal
// and the run is not recorded as over. A read says so; a press is a new build.
// And then the mirror image, with the build that ended still to finish
// returning: its goroutine leaves the new build's claim alone.
func TestAnEndedBuildCanBeStartedAgainFromTheMomentAReadSaysSo(t *testing.T) {
	for _, exit := range []struct {
		name  string
		want  string
		fails bool
		// end stops the first build, when it does not end by itself.
		end func(m *machine, id string)
	}{
		{name: "succeeded", want: "succeeded"},
		{name: "failed", want: "failed", fails: true},
		{name: "cancelled", want: "cancelled", end: func(m *machine, id string) {
			if status, body := m.call(http.MethodPost, "/api/v1/build/runs/"+id+"/cancel", nil); status != http.StatusAccepted {
				m.t.Fatalf("cancelling = %d: %v", status, body)
			}
		}},
	} {
		t.Run(exit.name, func(t *testing.T) {
			m := newMachine(t)
			m.approveAndBindTool()
			endings := m.holdBuildEndings()
			request, open, shut := m.gatedBuild("again")
			if exit.fails {
				// The fixture compiler fails on an option, which makes this a
				// second request; both builds below ask for it.
				request["options"] = map[string]map[string]string{"compile": {"fail": "true"}}
			}
			if exit.end == nil {
				open()
			}
			first := m.startBuild(request)
			if exit.end != nil {
				m.awaitCompiling(first)
				exit.end(m, first)
			}
			endings.awaitHeld(t, first)

			run, tracked := m.server.builds.get(first)
			if !tracked {
				t.Fatal("the build is not tracked")
			}
			if finished, _ := run.state(); finished {
				t.Fatal("the test is not holding the window: the run is already recorded as over")
			}
			manifest := m.waitForBuild(first)
			if manifest["state"] != exit.want {
				t.Fatalf("the read said %v: %v", manifest["state"], manifest["error"])
			}
			if status, _ := m.call(http.MethodPost, "/api/v1/build/runs/"+first+"/cancel", nil); status != http.StatusConflict {
				t.Fatalf("cancelling a build a read says is over = %d", status)
			}

			if exit.end == nil {
				shut()
			} else if err := os.Remove(filepath.Join(m.dir, "again-gate")); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			second := m.startBuild(request)
			if second == first {
				t.Fatal("no new build: the press was answered with the one that ended")
			}
			m.awaitCompiling(second)
			m.requireRefusedFor(request, second)

			// The build that ended goes on now, and every cleanup it had
			// deferred runs while its replacement holds the request.
			endings.release()
			endings.awaitGone(t, first)
			m.requireRefusedFor(request, second)
			if active := m.activeBuilds(); len(active) != 1 || active[0] != second {
				t.Fatalf("listed as active: %v, want only %s", active, second)
			}

			// What stops work reaches the new build, and no other.
			m.server.cancelWork(m.server.ActiveWork())
			if stopped := m.waitForBuild(second); stopped["state"] != "cancelled" {
				t.Fatalf("the new build after being stopped: %v", stopped["state"])
			}
			endings.awaitGone(t, second)
			if settled := m.waitForBuild(first); settled["state"] != exit.want {
				t.Fatalf("the first build's manifest became %v", settled["state"])
			}
		})
	}
}

// The second window: the run is recorded as over and a leak result is still on
// its way back. A slow network is not a reason to hold the request, and nothing
// about the delivery is lost by not holding it.
func TestASlowLeakReturnDoesNotHoldTheRequest(t *testing.T) {
	m, _ := newLeakMachine(t)
	wad := fixturewad.WAD("n371d_floor", 8)
	m.saveRoom("new371d", 1, q1Room("new371d.wad", false, "n371d_floor"),
		leakBundle("new371d", 1, []string{"new371d.wad"}, map[string][]byte{"new371d.wad": wad}))
	endings := m.holdBuildEndings()
	endings.arm(false)

	hold, held := make(chan struct{}), make(chan struct{})
	m.backend.mu.Lock()
	m.backend.holdLeakResult, m.backend.leakResultHeld = hold, held
	m.backend.mu.Unlock()
	var once sync.Once
	letGo := func() { once.Do(func() { close(hold) }) }
	t.Cleanup(letGo)

	link := m.receiveLeakLink(strings.Repeat("7", 32), "quake1")
	request := map[string]any{
		"pipeline":        "aucom.fixture.q1-leak",
		"inputs":          map[string]string{"source_map": "aub:map/" + link.AssetID + "@" + m.backend.asset.revisionID + "#fixture.apmap"},
		"leak_request_id": link.RequestID,
	}
	id := m.startBuild(request)
	select {
	case <-held:
	case <-time.After(30 * time.Second):
		t.Fatal("the leak result never reached the server")
	}

	manifest := m.waitForBuild(id)
	if manifest["state"] != "succeeded" && manifest["state"] != "failed" {
		t.Fatalf("the read said %v", manifest["state"])
	}
	if active := m.activeBuilds(); len(active) != 0 {
		t.Fatalf("listed as active while returning a result: %v", active)
	}
	// The request itself is single-use, so it is refused — by what is true of
	// it, not by a build that has ended.
	status, body := m.call(http.MethodPost, "/api/v1/build/runs", request)
	if status != http.StatusConflict || body["code"] == "already_running" {
		t.Fatalf("a press during the delivery = %d: %v", status, body)
	}

	// And the delivery finishes, and the run's texture copy goes, unheld.
	letGo()
	endings.awaitGone(t, id)
	m.backend.mu.Lock()
	returned := m.backend.leakResult
	m.backend.mu.Unlock()
	if returned == nil {
		t.Error("the result never reached the server")
	}
	if left := m.leakTextureDirs(); len(left) != 0 {
		t.Errorf("the run left its texture copy: %v", left)
	}
}

// The claim's own job: while a build works, however many presses arrive, one
// build starts and every refusal names it; another request is another build.
// And at the end of it, exactly one of a crowd of presses becomes the next.
func TestABuildStartsOnceHoweverManyPressesArrive(t *testing.T) {
	m := newMachine(t)
	m.approveAndBindTool()
	endings := m.holdBuildEndings()
	endings.arm(false)
	request, open, shut := m.gatedBuild("crowd")
	first := m.startBuild(request)
	m.awaitCompiling(first)

	press := func(presses int) (started []string, refusedNaming []string) {
		var mu sync.Mutex
		var group sync.WaitGroup
		for range presses {
			group.Add(1)
			go func() {
				defer group.Done()
				status, body := m.call(http.MethodPost, "/api/v1/build/runs", request)
				id, _ := body["build"].(string)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case status == http.StatusAccepted:
					started = append(started, id)
				case status == http.StatusConflict && body["code"] == "already_running":
					refusedNaming = append(refusedNaming, id)
				default:
					t.Errorf("a press = %d: %v", status, body)
				}
			}()
		}
		group.Wait()
		return started, refusedNaming
	}

	started, refused := press(8)
	if len(started) != 0 || len(refused) != 8 {
		t.Fatalf("while it runs: started %v, refused %v", started, refused)
	}
	for _, id := range refused {
		if id != first {
			t.Fatalf("a refusal named %q, not the running %s", id, first)
		}
	}
	other, openOther, _ := m.gatedBuild("another")
	openOther()
	different := m.startBuild(other)
	if different == first {
		t.Fatal("a different request was answered with the first")
	}
	m.waitForBuild(different)
	endings.awaitGone(t, different)

	endings.arm(true)
	open()
	endings.awaitHeld(t, first)
	m.waitForBuild(first)
	shut()

	started, refused = press(8)
	if len(started) != 1 || len(refused) != 7 {
		t.Fatalf("after it ended: started %v, refused %v", started, refused)
	}
	next := started[0]
	if next == first {
		t.Fatal("a press was answered with the build that ended")
	}
	for _, id := range refused {
		// A press in the moment before the new build has an id cannot name
		// it, and says so by naming nothing. It must never name the old one.
		if id != "" && id != next {
			t.Fatalf("a refusal named %q, not the running %s", id, next)
		}
	}
	endings.release()
	open()
	m.waitForBuild(next)
}

// A request that never became a build gives its claim back, at every early
// exit, and a claim held before any id exists is a claim all the same.
func TestAClaimBeforeABuildHasAnIDIsHeldAndGivenBack(t *testing.T) {
	m := newMachine(t)
	m.approveAndBindTool()
	request, open, _ := m.gatedBuild("early")
	open()
	input := request["inputs"].(map[string]string)["source_map"]
	key := buildRequestKey(buildRequestBody{Pipeline: "aucom.fixture.pipeline",
		Inputs: map[string]string{"source_map": input}})

	// Refused while the holder has no id: naming nothing, and not replaced.
	_, _ = m.server.builds.claim(key)
	status, body := m.call(http.MethodPost, "/api/v1/build/runs", request)
	if status != http.StatusConflict || body["code"] != "already_running" || body["build"] != "" {
		t.Fatalf("a press against a claim with no id = %d: %v", status, body)
	}
	m.server.builds.mu.Lock()
	delete(m.server.builds.starting, key)
	m.server.builds.mu.Unlock()

	for name, failing := range map[string]map[string]any{
		"an input that is not there": {"pipeline": "aucom.fixture.pipeline",
			"inputs": map[string]string{"source_map": filepath.Join(m.dir, "missing.map")}},
		"a pipeline nothing resolves": {"pipeline": "aucom.fixture.no-such-pipeline",
			"inputs": map[string]string{"source_map": input}},
	} {
		for press := range 2 {
			status, body := m.call(http.MethodPost, "/api/v1/build/runs", failing)
			if status < 400 || body["code"] == "already_running" {
				t.Fatalf("%s, press %d = %d: %v", name, press, status, body)
			}
		}
	}
	m.server.builds.mu.Lock()
	left := fmt.Sprint(m.server.builds.starting)
	count := len(m.server.builds.starting)
	m.server.builds.mu.Unlock()
	if count != 0 {
		t.Fatalf("an early refusal left a claim: %s", left)
	}
	m.waitForBuild(m.startBuild(request))
}
