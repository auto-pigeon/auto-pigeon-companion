package web

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
)

// A build that finishes WHILE it is being read (NEW_323B).
//
// A read of a build looks at two things — what this process knows about the
// run, and the manifest on disk — and a build can end between them. Release run
// 38040182778 failed on that: the manifest was read `running`, the build
// finished, and the response said `live: false` over the `running` manifest,
// which a page reads as "the build running" and stops polling on. The list had
// the same two looks and a worse ending: it took the run for one a stopped
// Companion had abandoned and wrote `interrupted` over a manifest that said
// `succeeded`.
//
// None of that is produced by waiting. These tests hold the fixture compiler
// until they release it, and release it from inside the read, between its two
// looks, through the one seam the registry has for it. They assert what a
// response may say, not the order the handler looks in.

// heldBuild starts a build whose compiler runs until release is called, and
// returns once the manifest on disk says its stage is running.
func (m *machine) heldBuild(options map[string]map[string]string) (id, source string, release func()) {
	m.t.Helper()
	gate := filepath.Join(m.dir, "release-the-compiler")
	path := filepath.Join(m.content, "held.map")
	source = "{ \"classname\" \"worldspawn\" }\n" + holdUntilMarker + gate + "\n"
	writeFixtureFile(m.t, path, source)
	var once sync.Once
	release = func() {
		once.Do(func() {
			if err := os.WriteFile(gate, nil, 0o600); err != nil {
				m.t.Errorf("releasing the compiler: %v", err)
			}
		})
	}
	m.t.Cleanup(release)

	request := map[string]any{
		"pipeline": "aucom.fixture.pipeline",
		"inputs":   map[string]string{"source_map": path},
		"label":    "a build held open",
	}
	if options != nil {
		request["options"] = options
	}
	status, body := m.call(http.MethodPost, "/api/v1/build/runs", request)
	if status != http.StatusAccepted {
		m.t.Fatalf("starting the build = %d: %v", status, body["error"])
	}
	id, _ = body["build"].(string)
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		manifest, err := build.Find(m.builds, id)
		if err == nil && len(manifest.Steps) == 1 && manifest.Steps[0].State == job.Running && manifest.Steps[0].JobID != "" {
			return id, source, release
		}
		if time.Now().After(deadline) {
			m.t.Fatalf("build %s never recorded its stage running", id)
		}
	}
}

// finishDuringTheNextRead releases the compiler at the first point the next
// read of a build reaches, and holds that read there until this process has
// recorded the build as over. One read only.
func (m *machine) finishDuringTheNextRead(id string, release func()) {
	m.t.Helper()
	run, tracked := m.server.builds.get(id)
	if !tracked {
		m.t.Fatalf("build %s is not one this process is running", id)
	}
	var once sync.Once
	m.server.builds.observed = func(string) {
		once.Do(func() {
			release()
			for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(time.Millisecond) {
				if finished, _ := run.state(); finished {
					return
				}
				if time.Now().After(deadline) {
					m.t.Errorf("build %s did not finish after its compiler was released", id)
					return
				}
			}
		})
	}
	m.t.Cleanup(func() { m.server.builds.observed = nil })
}

// requireCoherent is the contract of one answer about one build: `live` and
// the manifest beside it describe the same moment.
func requireCoherent(t *testing.T, where string, live any, manifest map[string]any) (terminal bool) {
	t.Helper()
	state, _ := manifest["state"].(string)
	steps, _ := manifest["steps"].([]any)
	if live == true {
		if state != "running" {
			t.Fatalf("%s: live with a manifest that says %q", where, state)
		}
		return false
	}
	if live != false {
		t.Fatalf("%s: live = %v, which is neither answer", where, live)
	}
	switch state {
	case "succeeded", "failed", "cancelled", "interrupted":
	default:
		t.Fatalf("%s: not live, and the manifest beside that says %q — a finished build described by a manifest from before it finished", where, state)
	}
	if finished, _ := manifest["finished_at"].(string); finished == "" || strings.HasPrefix(finished, "0001-") {
		t.Fatalf("%s: a %s build with no finishing time (%q)", where, state, finished)
	}
	for _, raw := range steps {
		step, _ := raw.(map[string]any)
		if step["state"] == "running" || step["state"] == "queued" {
			t.Fatalf("%s: a %s build whose %v stage is still %v", where, state, step["id"], step["state"])
		}
	}
	return true
}

func bspOutput(t *testing.T, manifest map[string]any) map[string]any {
	t.Helper()
	outputs, _ := manifest["outputs"].([]any)
	for _, raw := range outputs {
		if output, _ := raw.(map[string]any); output["name"] == "bsp" {
			return output
		}
	}
	t.Fatalf("the manifest declares no bsp output: %v", manifest["outputs"])
	return nil
}

// requireSucceededWhole is everything a succeeded answer promises: the stage's
// exit, the declared output's record, and the bytes behind the download.
func (m *machine) requireSucceededWhole(where, id, source string, body, manifest map[string]any) {
	m.t.Helper()
	if manifest["state"] != "succeeded" {
		m.t.Fatalf("%s: the build %v: %v", where, manifest["state"], manifest["error"])
	}
	if message, present := body["error"]; present {
		m.t.Fatalf("%s: a succeeded build answered with an error: %v", where, message)
	}
	if manifest["build_id"] != id {
		m.t.Fatalf("%s: asked about %s and was answered about %v", where, id, manifest["build_id"])
	}
	steps, _ := manifest["steps"].([]any)
	step, _ := steps[0].(map[string]any)
	if step["state"] != "succeeded" {
		m.t.Fatalf("%s: succeeded, with its compile stage %v", where, step["state"])
	}
	if code, present := step["exit_code"].(float64); present && code != 0 {
		m.t.Fatalf("%s: succeeded, with exit status %v", where, code)
	}
	want := "BSP:" + source
	output := bspOutput(m.t, manifest)
	if output["missing"] == true || output["path"] == "" || output["path"] == nil {
		m.t.Fatalf("%s: succeeded, and its bsp is not recorded as produced: %v", where, output)
	}
	if size, _ := output["size"].(float64); int(size) != len(want) {
		m.t.Fatalf("%s: the bsp is recorded as %v bytes, want %d", where, output["size"], len(want))
	}

	// And the download the answer invites is there, whole, at once.
	response, _ := send(m.t, m.server, request(m.t, m.server, http.MethodGet,
		"/api/v1/build/runs/"+id+"/output/bsp", ""))
	if response.StatusCode != http.StatusOK {
		m.t.Fatalf("%s: fetching the bsp of a build answered as succeeded = %d", where, response.StatusCode)
	}
	got, err := io.ReadAll(response.Body)
	if err != nil {
		m.t.Fatal(err)
	}
	if string(got) != want {
		m.t.Fatalf("%s: the bsp is %q, want %q", where, got, want)
	}
}

// The detail route: the read that failed the release.
func TestABuildThatFinishesDuringItsReadIsNotAnsweredHalfFinished(t *testing.T) {
	m := newMachine(t)
	m.approveAndBindTool()
	id, source, release := m.heldBuild(nil)
	m.finishDuringTheNextRead(id, release)

	status, body := m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("reading the build = %d: %v", status, body["error"])
	}
	manifest, _ := body["manifest"].(map[string]any)
	if !requireCoherent(t, "the read the build finished during", body["live"], manifest) {
		// Still live is a true answer about the moment it looked. The next
		// read, with nothing racing it, is the one that has to be final.
		status, body = m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
		if status != http.StatusOK {
			t.Fatalf("reading the build again = %d: %v", status, body["error"])
		}
		manifest, _ = body["manifest"].(map[string]any)
		if !requireCoherent(t, "the read after it", body["live"], manifest) {
			t.Fatal("the build is recorded as over and is still answered as live")
		}
	}
	m.requireSucceededWhole("the finished answer", id, source, body, manifest)
	if _, present := body["log"]; !present {
		t.Error("the answer about a build this process ran lost its output")
	}
}

// A failure ends a build as much as a success does, and its answer carries the
// state and the reason together.
func TestABuildThatFailsDuringItsReadSaysSoWithItsReason(t *testing.T) {
	m := newMachine(t)
	m.approveAndBindTool()
	id, _, release := m.heldBuild(map[string]map[string]string{"compile": {"fail": "true"}})
	m.finishDuringTheNextRead(id, release)

	_, body := m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
	manifest, _ := body["manifest"].(map[string]any)
	if !requireCoherent(t, "the read the build failed during", body["live"], manifest) {
		_, body = m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
		manifest, _ = body["manifest"].(map[string]any)
		requireCoherent(t, "the read after it", body["live"], manifest)
	}
	if manifest["state"] != "failed" {
		t.Fatalf("the build %v, want failed", manifest["state"])
	}
	recorded, _ := manifest["error"].(string)
	answered, _ := body["error"].(string)
	if !strings.Contains(recorded, "compile") || answered != recorded {
		t.Fatalf("a failed build answered error %q beside a manifest that records %q", answered, recorded)
	}
	steps, _ := manifest["steps"].([]any)
	step, _ := steps[0].(map[string]any)
	if code, _ := step["exit_code"].(float64); step["state"] != "failed" || code != 3 {
		t.Fatalf("the failed stage is recorded %v with exit status %v", step["state"], step["exit_code"])
	}
	// A failed build promised a BSP and did not make one, and says that rather
	// than offering a download.
	if output := bspOutput(t, manifest); output["missing"] != true {
		t.Fatalf("a failed compile records its bsp as %v", output)
	}
	response, _ := send(t, m.server, request(t, m.server, http.MethodGet, "/api/v1/build/runs/"+id+"/output/bsp", ""))
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("fetching the bsp of a failed compile = %d, want 404", response.StatusCode)
	}
}

// The control: a build that is still running when it is read is answered at
// once, as running. A read is never a wait for a compiler.
func TestABuildStillRunningIsAnsweredAtOnceAsRunning(t *testing.T) {
	m := newMachine(t)
	m.approveAndBindTool()
	id, source, release := m.heldBuild(nil)

	type answer struct {
		status int
		body   map[string]any
	}
	for _, path := range []string{"/api/v1/build/runs/" + id, "/api/v1/build/runs"} {
		answered := make(chan answer, 1)
		go func() {
			status, body := m.call(http.MethodGet, path, nil)
			answered <- answer{status, body}
		}()
		select {
		case got := <-answered:
			if got.status != http.StatusOK {
				t.Fatalf("GET %s = %d: %v", path, got.status, got.body["error"])
			}
			item := got.body
			if items, listed := got.body["items"].([]any); listed {
				if len(items) != 1 {
					t.Fatalf("the list holds %d builds, want 1", len(items))
				}
				item, _ = items[0].(map[string]any)
			}
			manifest, _ := item["manifest"].(map[string]any)
			if item["live"] != true || manifest["state"] != "running" {
				t.Fatalf("GET %s while the compiler runs: live = %v, state = %v", path, item["live"], manifest["state"])
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("GET %s did not answer while the compiler was running: a read waited for the build", path)
		}
	}
	// It is still running, on disk too: reading it changed nothing.
	if manifest, err := build.Find(m.builds, id); err != nil || manifest.State != job.Running {
		t.Fatalf("after two reads the running build is recorded %v (%v)", manifest.State, err)
	}

	release()
	status, body := m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
	for deadline := time.Now().Add(30 * time.Second); status == http.StatusOK && body["live"] == true; {
		if time.Now().After(deadline) {
			t.Fatal("the released build did not finish")
		}
		time.Sleep(10 * time.Millisecond)
		status, body = m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
	}
	manifest, _ := body["manifest"].(map[string]any)
	requireCoherent(t, "the read after release", body["live"], manifest)
	m.requireSucceededWhole("the released build", id, source, body, manifest)
}

// The list has the same two looks, and used to end worse: a build that finished
// between them was taken for one a stopped Companion abandoned, and
// `interrupted` was written over its manifest.
func TestABuildThatFinishesDuringTheListIsNotRecordedInterrupted(t *testing.T) {
	m := newMachine(t)
	m.approveAndBindTool()
	id, source, release := m.heldBuild(nil)
	m.finishDuringTheNextRead(id, release)

	status, body := m.call(http.MethodGet, "/api/v1/build/runs", nil)
	if status != http.StatusOK {
		t.Fatalf("listing builds = %d: %v", status, body["error"])
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("the list holds %d builds, want 1", len(items))
	}
	item, _ := items[0].(map[string]any)
	listed, _ := item["manifest"].(map[string]any)
	if requireCoherent(t, "the list the build finished during", item["live"], listed) && listed["state"] != "succeeded" {
		t.Fatalf("the list answered a build that succeeded as %v: %v", listed["state"], listed["error"])
	}

	// What the list said is one thing; what it left on disk is the record every
	// later read, and every later Companion, is given.
	recorded, err := build.Find(m.builds, id)
	if err != nil {
		t.Fatal(err)
	}
	if recorded.State != job.Succeeded || recorded.Error != "" {
		t.Fatalf("after the list, the manifest on disk says %s (%q); the build succeeded", recorded.State, recorded.Error)
	}

	status, body = m.call(http.MethodGet, "/api/v1/build/runs", nil)
	items, _ = body["items"].([]any)
	item, _ = items[0].(map[string]any)
	listed, _ = item["manifest"].(map[string]any)
	if !requireCoherent(t, "the list after it", item["live"], listed) {
		t.Fatal("the build is recorded as over and is still listed as running here")
	}
	_, detail := m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
	manifest, _ := detail["manifest"].(map[string]any)
	requireCoherent(t, "the detail after the list", detail["live"], manifest)
	m.requireSucceededWhole("the build the list raced", id, source, detail, manifest)
	if listed["state"] != manifest["state"] || listed["finished_at"] != manifest["finished_at"] {
		t.Fatalf("the list says %v at %v and the detail says %v at %v",
			listed["state"], listed["finished_at"], manifest["state"], manifest["finished_at"])
	}
}

// Cancelling goes through the same answer: the build ends `cancelled`, the
// compiler it was running is gone, and there is nothing left to cancel.
func TestCancellingARunningBuildEndsItAndTheProcessItOwned(t *testing.T) {
	m := newMachine(t)
	m.approveAndBindTool()
	id, _, _ := m.heldBuild(nil)

	status, body := m.call(http.MethodPost, "/api/v1/build/runs/"+id+"/cancel", nil)
	if status != http.StatusAccepted {
		t.Fatalf("cancelling the build = %d: %v", status, body["error"])
	}
	jobs, _ := body["cancelled_jobs"].([]any)
	if len(jobs) != 1 {
		t.Fatalf("cancelling a build in its compile stage stopped %v; want that stage's job", body["cancelled_jobs"])
	}
	manifest := m.waitForBuild(id)
	if manifest["state"] != "cancelled" {
		t.Fatalf("the cancelled build is recorded %v: %v", manifest["state"], manifest["error"])
	}
	_, answer := m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
	requireCoherent(t, "the cancelled build", answer["live"], manifest)
	if message, _ := answer["error"].(string); message == "" || message != manifest["error"] {
		t.Fatalf("the cancelled build is answered with %q beside a manifest that records %q", message, manifest["error"])
	}
	// The process is over, not merely disowned: the executor recorded the job
	// it signalled as ended, and the compiler never got to write its output.
	if stopped := m.waitForJob(jobs[0].(string)); stopped["state"] != "cancelled" {
		t.Fatalf("the cancelled stage's job is %v", stopped["state"])
	}
	if output := bspOutput(t, manifest); output["missing"] != true {
		t.Fatalf("a cancelled compile records its bsp as %v", output)
	}
	if status, _ := m.call(http.MethodPost, "/api/v1/build/runs/"+id+"/cancel", nil); status != http.StatusConflict {
		t.Fatalf("cancelling a build that is over = %d, want 409", status)
	}
}

// The case reconciliation IS for, kept: a manifest a stopped Companion left
// `running` is answered `interrupted` by the next one, on both routes and on
// disk, and nothing is run again.
func TestABuildAStoppedCompanionLeftRunningIsStillAnsweredInterrupted(t *testing.T) {
	m := newMachine(t)
	m.approveAndBindTool()
	status, body := m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline": "aucom.fixture.pipeline",
		"inputs":   map[string]string{"source_map": m.writeSourceMap()},
	})
	if status != http.StatusAccepted {
		t.Fatalf("starting the build = %d: %v", status, body["error"])
	}
	id, _ := body["build"].(string)
	m.waitForBuild(id)

	// What a Companion killed mid-compile leaves behind: the manifest as it
	// was while the stage ran, and a job that is no longer active.
	abandoned, err := build.Find(m.builds, id)
	if err != nil {
		t.Fatal(err)
	}
	abandoned.State, abandoned.Error, abandoned.Outputs = job.Running, "", nil
	abandoned.FinishedAt, abandoned.DurationMS = time.Time{}, 0
	abandoned.Steps[0].State = job.Running
	if err := abandoned.Save(abandoned.Directory); err != nil {
		t.Fatal(err)
	}
	before := len(m.jobIDs())

	next := m.restart()
	for _, path := range []string{"/api/v1/build/runs", "/api/v1/build/runs/" + id} {
		status, body := next.call(http.MethodGet, path, nil)
		if status != http.StatusOK {
			t.Fatalf("GET %s = %d: %v", path, status, body["error"])
		}
		item := body
		if items, listed := body["items"].([]any); listed {
			item, _ = items[0].(map[string]any)
		}
		manifest, _ := item["manifest"].(map[string]any)
		requireCoherent(t, "GET "+path+" after the restart", item["live"], manifest)
		if manifest["state"] != "interrupted" || manifest["error"] != build.InterruptedNote {
			t.Fatalf("GET %s after the restart: %v (%v)", path, manifest["state"], manifest["error"])
		}
	}
	if recorded, err := build.Find(m.builds, id); err != nil || recorded.State != job.Interrupted {
		t.Fatalf("after the restart the manifest on disk says %v (%v)", recorded.State, err)
	}
	if after := len(next.jobIDs()); after != before {
		t.Fatalf("reading an interrupted build started %d job(s)", after-before)
	}
	if status, _ := next.call(http.MethodPost, "/api/v1/build/runs/"+id+"/cancel", nil); status != http.StatusConflict {
		t.Fatalf("cancelling a build this Companion is not running = %d, want 409", status)
	}
}
