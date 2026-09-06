package job

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestEveryStateTransitionIsDecidedByOneTable(t *testing.T) {
	// Written out rather than derived from `transitions`, so that a change to
	// the table has to be a deliberate change to this list too.
	allowed := map[State][]State{
		Queued:      {Resolving, Cancelling, Cancelled, Failed, Interrupted},
		Resolving:   {Running, Cancelling, Failed, Interrupted},
		Running:     {Cancelling, Succeeded, Failed, Interrupted},
		Cancelling:  {Cancelled, Failed, Succeeded, Interrupted},
		Succeeded:   {},
		Failed:      {},
		Cancelled:   {},
		Interrupted: {},
	}
	if len(States) != len(allowed) {
		t.Fatalf("States lists %d states, the table has %d", len(States), len(allowed))
	}
	for _, from := range States {
		want := map[State]bool{}
		for _, to := range allowed[from] {
			want[to] = true
		}
		for _, to := range States {
			err := Transition("j", from, to)
			if want[to] && err != nil {
				t.Errorf("%s -> %s was refused: %v", from, to, err)
			}
			if !want[to] && err == nil {
				t.Errorf("%s -> %s was allowed", from, to)
			}
		}
		if from.Terminal() != (len(allowed[from]) == 0) {
			t.Errorf("%s.Terminal() = %v", from, from.Terminal())
		}
		// Active and Terminal are the two halves of one question.
		if from.Active() == from.Terminal() {
			t.Errorf("%s is both or neither of active and terminal", from)
		}
	}

	// A job never moves to the state it is already in: a double-cancel and a
	// double-finish are both bugs worth hearing about.
	for _, state := range States {
		if err := Transition("j", state, state); err == nil {
			t.Errorf("%s -> %s (itself) was allowed", state, state)
		}
	}
	// And an unknown state is not a state.
	if err := Transition("j", Running, State("nearly-done")); err == nil {
		t.Error("a move to an unknown state was allowed")
	}
	var transitionErr *TransitionError
	err := Transition("j", Succeeded, Running)
	if !errors.As(err, &transitionErr) {
		t.Fatalf("error = %v, want a *TransitionError", err)
	}
	if !strings.Contains(err.Error(), "already succeeded") {
		t.Errorf("error = %q, want it to say the job has already finished", err)
	}
}

func TestARestartMarksAnAbandonedJobInterruptedAndNeverRerunsIt(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "jobs"))
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}

	// A job the previous run left claiming to be running, with a heartbeat old
	// enough that nothing is supervising it. This is what a crash leaves.
	id, err := NewID(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("minting an id: %v", err)
	}
	abandoned := &Job{
		SchemaVersion: SchemaVersion,
		ID:            id,
		State:         Running,
		Request:       Request{ProfileID: "test.crash", ActionID: "write"},
		ProfileID:     "test.crash",
		ActionID:      "write",
		CreatedAt:     time.Now().Add(-time.Hour).UTC(),
		StartedAt:     time.Now().Add(-time.Hour).UTC(),
		Owner:         Owner{PID: 999999},
	}
	if err := store.Save(abandoned); err != nil {
		t.Fatalf("saving the abandoned job: %v", err)
	}
	if err := store.Heartbeat(id, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("writing a stale heartbeat: %v", err)
	}

	recovered, err := store.Recover(time.Now().UTC())
	if err != nil {
		t.Fatalf("recovering: %v", err)
	}
	if len(recovered) != 1 || recovered[0] != id {
		t.Fatalf("recovered %v, want [%s]", recovered, id)
	}

	after, err := store.Load(id)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if after.State != Interrupted {
		t.Fatalf("state = %s, want interrupted", after.State)
	}
	if !strings.Contains(after.Error, "nothing here knows how it ended") {
		t.Errorf("error = %q, want it to say the outcome is unknown", after.Error)
	}
	if after.FinishedAt.IsZero() {
		t.Error("an interrupted job has no finish time")
	}
	// Recovery is idempotent: a second pass has nothing left to take.
	again, err := store.Recover(time.Now().UTC())
	if err != nil || len(again) != 0 {
		t.Errorf("a second recovery took %v (%v)", again, err)
	}
}

func TestRecoveryLeavesAJobAnotherProcessIsStillSupervising(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "jobs"))
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	id, err := NewID(time.Now())
	if err != nil {
		t.Fatalf("minting an id: %v", err)
	}
	live := &Job{SchemaVersion: SchemaVersion, ID: id, State: Running, CreatedAt: time.Now().UTC()}
	if err := store.Save(live); err != nil {
		t.Fatalf("saving: %v", err)
	}
	if err := store.Heartbeat(id, time.Now()); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	recovered, err := store.Recover(time.Now().UTC())
	if err != nil {
		t.Fatalf("recovering: %v", err)
	}
	if len(recovered) != 0 {
		t.Fatalf("recovery took %v, but its owner is still saying it is alive", recovered)
	}
}

func TestRetryIsANewJobThatRemembersTheOldOne(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.retry", writeAction()))

	first := h.runToEnd(h.helperRequest("test.retry", "write"))
	if first.State != Succeeded {
		t.Fatalf("state = %s, want succeeded (error: %s)", first.State, first.Error)
	}

	second, err := h.service.Retry(first.ID)
	if err != nil {
		t.Fatalf("retrying: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("the retry reused the first job's id, destroying the record of what happened")
	}
	if second.Request.RetryOf != first.ID {
		t.Errorf("retry_of = %q, want %s", second.Request.RetryOf, first.ID)
	}
	finished := h.waitFor(second.ID)
	if finished.State != Succeeded {
		t.Fatalf("the retry ended %s (error: %s)", finished.State, finished.Error)
	}

	// The first job's record and artifacts are still there.
	original, err := h.service.Get(first.ID)
	if err != nil {
		t.Fatalf("the first job is gone: %v", err)
	}
	if original.State != Succeeded || len(original.Artifacts) != 1 {
		t.Errorf("the first job's record changed: %s, %d artifacts", original.State, len(original.Artifacts))
	}
}

func TestARunningJobCannotBeRetried(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.retry.running", modeAction("slow", "sleep", "30")))

	submitted, err := h.service.Submit(h.helperRequest("test.retry.running", "slow"))
	if err != nil {
		t.Fatalf("submitting: %v", err)
	}
	waitForState(t, h, submitted.ID, Running)

	if _, err := h.service.Retry(submitted.ID); err == nil {
		t.Fatal("a running job was retried")
	}
	if _, err := h.service.Cancel(submitted.ID); err != nil {
		t.Fatalf("cancelling: %v", err)
	}
	h.waitFor(submitted.ID)
}

func TestConcurrentJobsAreBoundedAndDoNotShareAWorkspace(t *testing.T) {
	const jobs = 6
	const concurrency = 2
	action := modeAction("slow", "sleep", "1")
	action.Timeout = 60
	h := newHarness(t, fixtureProfile(t, "test.concurrency", action, writeAction()),
		func(o *Options) { o.Concurrency = concurrency })

	started := time.Now()
	ids := make([]string, 0, jobs)
	for i := 0; i < jobs; i++ {
		submitted, err := h.service.Submit(h.helperRequest("test.concurrency", "slow"))
		if err != nil {
			t.Fatalf("submitting %d: %v", i, err)
		}
		ids = append(ids, submitted.ID)
	}

	workspaces := map[string]string{}
	for _, id := range ids {
		finished := h.waitFor(id)
		if finished.State != Succeeded {
			t.Fatalf("job %s ended %s (error: %s)", id, finished.State, finished.Error)
		}
		if previous, seen := workspaces[finished.Workspace]; seen {
			t.Fatalf("jobs %s and %s shared the workspace %s", previous, id, finished.Workspace)
		}
		workspaces[finished.Workspace] = id
	}
	elapsed := time.Since(started)

	// Six one-second jobs, two at a time, cannot be done in under three
	// seconds. The bound is what is being tested, not the speed.
	if minimum := (jobs / concurrency) * time.Second; elapsed < minimum {
		t.Errorf("%d jobs finished in %s with a concurrency of %d; the limit was not applied", jobs, elapsed, concurrency)
	}
}

func TestArtifactsOfDifferentJobsDoNotCollide(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.collision", writeAction()))

	paths := map[string]string{}
	for _, text := range []string{"first", "second", "third"} {
		request := h.helperRequest("test.collision", "write")
		request.Options = map[string]string{"text": text}
		finished := h.runToEnd(request)
		if finished.State != Succeeded {
			t.Fatalf("state = %s (error: %s)", finished.State, finished.Error)
		}
		if len(finished.Artifacts) != 1 {
			t.Fatalf("artifacts = %d, want 1", len(finished.Artifacts))
		}
		path := finished.Artifacts[0].Path
		if owner, seen := paths[path]; seen {
			t.Fatalf("jobs %s and %s published to the same path %s", owner, finished.ID, path)
		}
		paths[path] = finished.ID

		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if string(content) != text {
			t.Errorf("artifact of the %q job says %q", text, content)
		}
	}

	// Every earlier artifact is still what it was: a later job did not reach
	// back into one.
	for path, id := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("the artifact of %s went away: %v", id, err)
		}
	}
}

func TestTwoOutputsNamingOneFileArePublishedSeparately(t *testing.T) {
	// Two declared outputs resolving to the same file in the workspace. Both
	// are collected, each under its own name, rather than one overwriting the
	// other or the second being reported missing.
	action := writeAction()
	action.Outputs = append(action.Outputs, map[string]any{
		"name": "same", "title": "The same file, under another name", "role": "test.alias", "path": "out/result.txt",
	})
	h := newHarness(t, fixtureProfile(t, "test.collision.same", action))

	request := h.helperRequest("test.collision.same", "write")
	request.Options = map[string]string{"text": "one-file-two-names"}
	finished := h.runToEnd(request)

	if finished.State != Succeeded {
		t.Fatalf("state = %s (error: %s)", finished.State, finished.Error)
	}
	if len(finished.Artifacts) != 2 {
		t.Fatalf("artifacts = %d, want 2", len(finished.Artifacts))
	}
	seen := map[string]bool{}
	for _, artifact := range finished.Artifacts {
		if artifact.Missing {
			t.Fatalf("the output %q was reported missing", artifact.Name)
		}
		if seen[artifact.Path] {
			t.Errorf("two outputs published to the same path %s", artifact.Path)
		}
		seen[artifact.Path] = true
		content, err := os.ReadFile(artifact.Path)
		if err != nil || string(content) != "one-file-two-names" {
			t.Errorf("artifact %q: %v %q", artifact.Name, err, content)
		}
	}
}

func TestAMissingRequiredOutputFailsTheJobAndAnOptionalOneDoesNot(t *testing.T) {
	action := writeAction()
	action.Outputs = append(action.Outputs,
		map[string]any{"name": "extra", "title": "Never written", "role": "test.extra", "path": "out/extra.txt", "optional": true},
		map[string]any{"name": "needed", "title": "Also never written", "role": "test.needed", "path": "out/needed.txt"},
	)
	h := newHarness(t, fixtureProfile(t, "test.outputs.missing", action))

	finished := h.runToEnd(h.helperRequest("test.outputs.missing", "write"))
	if finished.State != Failed {
		t.Fatalf("state = %s, want failed: a required output was not produced", finished.State)
	}
	if !strings.Contains(finished.Error, "needed") {
		t.Errorf("error = %q, want it to name the missing output", finished.Error)
	}
	if strings.Contains(finished.Error, "extra") {
		t.Errorf("error = %q, an optional output's absence is not a failure", finished.Error)
	}
	// The one output that *was* produced is still published: a failed job's
	// partial results are usually the evidence.
	for _, artifact := range finished.Artifacts {
		if artifact.Name == "result" && artifact.Missing {
			t.Error("the output that was written was not collected")
		}
	}
}

func TestNoGoroutinesOrProcessesSurviveASoak(t *testing.T) {
	if testing.Short() {
		t.Skip("the soak is slow by design")
	}
	before := runtime.NumGoroutine()

	action := modeAction("quick", "echo", "soak")
	h := newHarness(t, fixtureProfile(t, "test.soak", action), func(o *Options) { o.Concurrency = 4 })

	const runs = 24
	ids := make([]string, 0, runs)
	for i := 0; i < runs; i++ {
		submitted, err := h.service.Submit(h.helperRequest("test.soak", "quick"))
		if err != nil {
			t.Fatalf("submitting %d: %v", i, err)
		}
		ids = append(ids, submitted.ID)
	}
	var pids []int
	for _, id := range ids {
		finished := h.waitFor(id)
		if finished.State != Succeeded {
			t.Fatalf("job %s ended %s (error: %s)", id, finished.State, finished.Error)
		}
		pids = append(pids, pidFromHistory(t, finished))
	}

	if err := h.service.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	// Every process the soak started is gone. Checked by pid because that is
	// what a leaked process would be: something still holding a slot after the
	// job that owned it was recorded as finished.
	for _, pid := range pids {
		if processGroupAlive(pid) {
			t.Errorf("the process group of pid %d is still alive after its job finished", pid)
		}
	}

	// Goroutines settle asynchronously, so this is a bound rather than an
	// equality: what it catches is a per-job leak, which after 24 runs at a
	// concurrency of 4 would be far past this.
	deadline := time.Now().Add(5 * time.Second)
	after := runtime.NumGoroutine()
	for time.Now().Before(deadline) && after > before+8 {
		time.Sleep(100 * time.Millisecond)
		after = runtime.NumGoroutine()
	}
	if after > before+8 {
		t.Errorf("goroutines went from %d to %d over %d jobs", before, after, runs)
	}
}

func pidFromHistory(t *testing.T, j *Job) int {
	t.Helper()
	for _, event := range j.History {
		if event.State != Running {
			continue
		}
		if _, digits, found := strings.Cut(event.Note, "pid "); found {
			pid, err := strconv.Atoi(strings.TrimSpace(digits))
			if err == nil {
				return pid
			}
		}
	}
	t.Fatalf("job %s never recorded the pid it ran as", j.ID)
	return 0
}

func TestAJobIsRefusedOnceTheServiceIsShuttingDown(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.shutdown", modeAction("quick", "echo", "hello")))
	if err := h.service.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	if _, err := h.service.Submit(h.helperRequest("test.shutdown", "quick")); err == nil {
		t.Fatal("a job was accepted after shutdown")
	}
}

func TestAPreviewResolvesTheSameCommandTheJobRunsAndStartsNothing(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.preview", writeAction()))

	request := h.helperRequest("test.preview", "write")
	request.Options = map[string]string{"text": "previewed"}
	previewed, err := h.service.Preview(request)
	if err != nil {
		t.Fatalf("previewing: %v", err)
	}
	if previewed.Command == nil {
		t.Fatal("the preview has no command")
	}
	if _, err := os.Stat(filepath.Join(h.store.Root(), previewed.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("previewing created %s on disk", previewed.ID)
	}
	if jobs, err := h.service.List(); err != nil || len(jobs) != 0 {
		t.Errorf("previewing left %d jobs behind (%v)", len(jobs), err)
	}

	finished := h.runToEnd(request)
	if finished.State != Succeeded {
		t.Fatalf("state = %s (error: %s)", finished.State, finished.Error)
	}
	// The workspace path differs — a different job, a different directory — so
	// what has to match is everything else: the program, the shape of the
	// argument array, and the arguments that are not paths.
	if previewed.Command.Executable != finished.Command.Executable {
		t.Errorf("preview ran %s, the job ran %s", previewed.Command.Executable, finished.Command.Executable)
	}
	if len(previewed.Command.Args) != len(finished.Command.Args) {
		t.Fatalf("preview argv %v, job argv %v", previewed.Command.Args, finished.Command.Args)
	}
	for i := range previewed.Command.Args {
		want, got := previewed.Command.Args[i], finished.Command.Args[i]
		if want == got {
			continue
		}
		// The only permitted difference is the job id inside a workspace path.
		if strings.Contains(want, previewed.ID) && strings.Contains(got, finished.ID) &&
			strings.Replace(want, previewed.ID, finished.ID, 1) == got {
			continue
		}
		t.Errorf("argv[%d]: preview %q, job %q", i, want, got)
	}
}

func TestAJobRecordSurvivesAReadByAnotherProcess(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.store.roundtrip", writeAction()))
	finished := h.runToEnd(h.helperRequest("test.store.roundtrip", "write"))

	other, err := OpenStore(h.store.Root())
	if err != nil {
		t.Fatalf("opening a second view: %v", err)
	}
	reread, err := other.Load(finished.ID)
	if err != nil {
		t.Fatalf("re-reading: %v", err)
	}
	if reread.State != finished.State || reread.ProfileDigest != finished.ProfileDigest {
		t.Errorf("the record changed across processes: %+v", reread)
	}
	if len(reread.Artifacts) != len(finished.Artifacts) {
		t.Errorf("artifacts = %d, want %d", len(reread.Artifacts), len(finished.Artifacts))
	}
}

func TestAJobIdIsRefusedWhenItIsNotOneThisPackageMinted(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "jobs"))
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	for _, id := range []string{
		"", "..", "../../etc/passwd", "20260906T000000Z-../../x", "not-an-id",
		"20260906T000000Z-0d13ed8e44d", // one hex digit short
		strings.Repeat("a", 300),
	} {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true", id)
		}
		if _, err := store.Load(id); !errors.Is(err, ErrNotFound) {
			t.Errorf("Load(%q) = %v, want ErrNotFound", id, err)
		}
	}
	good, err := NewID(time.Now())
	if err != nil {
		t.Fatalf("minting: %v", err)
	}
	if !ValidID(good) {
		t.Errorf("a freshly minted id %q is not valid", good)
	}
}

func TestContextCancellationInterruptsRatherThanFails(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.shutdown.running", modeAction("slow", "sleep", "60")))

	submitted, err := h.service.Submit(h.helperRequest("test.shutdown.running", "slow"))
	if err != nil {
		t.Fatalf("submitting: %v", err)
	}
	waitForState(t, h, submitted.ID, Running)

	// The Companion going away, not the tool failing.
	h.cancel()
	if err := h.service.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	after, err := h.store.Load(submitted.ID)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if after.State != Interrupted {
		t.Fatalf("state = %s, want interrupted: the Companion stopped it, the tool did not fail", after.State)
	}
	if !strings.Contains(after.Error, "shut down") {
		t.Errorf("error = %q, want it to say the Companion shut down", after.Error)
	}
}
