package job

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// What `AUCOM/AUT 229` was asked to measure, and the half of it that belongs
// here.
//
// The prompt's real question — does the Companion's RESIDENT memory stay flat
// while a real child floods a real pipe for a quarter of a gigabyte — cannot be
// answered by a Go test. `testing.AllocsPerRun` and `runtime.MemStats` describe
// this process's heap, and the thing under measurement is a whole program's RSS
// on a real operating system, which only that operating system can report. AUT
// takes that measurement against the built binary.
//
// What is checkable here is everything the measurement is held to: that the
// published limits are the ones the capture actually implements, that the
// record a flood produces is true, and that the two failure modes a supervisor
// has under load — mixing two jobs' output, and leaving a process tree behind —
// do not happen. A harness measuring RSS against a program that lied about its
// own limits would be measuring nothing.

// TestTheLimitsPublishWhatTheCaptureActuallyDoes ties [RetentionLimits] to the
// constants it reports.
//
// The limits exist so AUT does not have to invent a threshold. That only works
// while they describe this build: a published bound that drifted from the
// constant would let a harness pass a program that had quietly started keeping
// ten times as much.
func TestTheLimitsPublishWhatTheCaptureActuallyDoes(t *testing.T) {
	limits := RetentionLimits()

	if limits.HeadBytes != headBytes || limits.TailBytes != tailBytes {
		t.Errorf("published head/tail %d/%d, the capture uses %d/%d",
			limits.HeadBytes, limits.TailBytes, headBytes, tailBytes)
	}
	if want := int64(headBytes + tailBytes); limits.KeptBytesPerStream != want {
		t.Errorf("kept per stream = %d, want %d", limits.KeptBytesPerStream, want)
	}
	if limits.ReadChunkBytes != readChunk || limits.MaxLineBytes != maxLineBytes {
		t.Errorf("published buffers %d/%d, the capture uses %d/%d",
			limits.ReadChunkBytes, limits.MaxLineBytes, readChunk, maxLineBytes)
	}
	if limits.MaxDiagnostics != maxDiagnostics {
		t.Errorf("published %d diagnostics, the collector keeps %d",
			limits.MaxDiagnostics, maxDiagnostics)
	}
	if limits.StreamsPerJob != 2 {
		t.Errorf("streams per job = %d, want 2", limits.StreamsPerJob)
	}
	if limits.ResidentBytesPerJob != int64(limits.StreamsPerJob)*limits.ResidentBytesPerStream {
		t.Errorf("per-job %d is not %d streams of %d",
			limits.ResidentBytesPerJob, limits.StreamsPerJob, limits.ResidentBytesPerStream)
	}
	// The resident bound must be above the kept bound. A harness holding a
	// running program to the kept figure would fail a correct build, which is
	// the mistake this field exists to stop somebody making.
	if limits.ResidentBytesPerStream <= limits.KeptBytesPerStream {
		t.Errorf("resident %d is not above kept %d: the tail is compacted at twice its bound",
			limits.ResidentBytesPerStream, limits.KeptBytesPerStream)
	}
}

// TestTheElisionMarkerFitsItsBudget checks the 128 bytes
// [Limits.MaxLogFileBytes] adds for the marker written between head and tail.
//
// Cited from limits.go, because that budget is otherwise a number somebody
// chose: the marker names a byte count that grows with the flood, so "128 is
// plenty" has to be measured against an implausibly large one rather than
// assumed.
func TestTheElisionMarkerFitsItsBudget(t *testing.T) {
	// Every byte an exabyte-scale count could need, which no disk can produce.
	marker := elision(1 << 62)
	if len(marker) > 128 {
		t.Errorf("the elision marker is %d bytes, over the %d MaxLogFileBytes allows",
			len(marker), 128)
	}
	if !strings.Contains(string(marker), "were not kept") {
		t.Errorf("the marker does not say that anything was dropped: %q", marker)
	}
}

// TestALineCountIsKeptWhenNoRuleAsksForOne is the defect `229` measured.
//
// `lineHandler` returns nil for an action that declares no diagnostic rules,
// `split` used to return immediately on that nil, and so the line counter never
// ran. A job that wrote a megabyte over ten thousand lines recorded `lines: 0`
// — beside a `bytes` and a `dropped` that were both correct, which is what made
// it invisible. Most profiles declare no rules, including every one the
// acceptance harness authors, so this was the normal case rather than an edge.
func TestALineCountIsKeptWhenNoRuleAsksForOne(t *testing.T) {
	const flooded = 1 << 20
	action := modeAction("flood", "flood", "1048576")
	action.Timeout = 120
	if len(action.Diagnostics) != 0 {
		t.Fatal("this fixture must declare no diagnostic rules; that is the condition under test")
	}
	h := newHarness(t, fixtureProfile(t, "test.stress.count", action))

	finished := h.runToEnd(h.helperRequest("test.stress.count", "flood"))
	if finished.State != Succeeded {
		t.Fatalf("state = %s, want succeeded (error: %s)", finished.State, finished.Error)
	}
	// The helper writes 100-byte lines, so the count is knowable rather than
	// merely non-zero: a counter that fired once per read would also be > 0.
	wantLines := int64(finished.Stdout.Bytes / 100)
	if finished.Stdout.Lines != wantLines {
		t.Errorf("counted %d lines for %d bytes of 100-byte lines, want %d",
			finished.Stdout.Lines, finished.Stdout.Bytes, wantLines)
	}
	if finished.Stdout.Bytes < flooded {
		t.Errorf("counted %d bytes, want at least %d", finished.Stdout.Bytes, flooded)
	}
}

// TestAStreamWithNoNewlinesIsCountedAndStillBounded exercises the path where
// the program never ends a line.
//
// The splitter has to bound `partial` itself here — a tool that writes a
// megabyte with no newline must not become a megabyte held in memory waiting to
// become one line.
func TestAStreamWithNoNewlinesIsCountedAndStillBounded(t *testing.T) {
	const flooded = 4 << 20
	action := modeAction("unbroken", "flood-unbroken", "4194304")
	action.Timeout = 120
	h := newHarness(t, fixtureProfile(t, "test.stress.unbroken", action))

	finished := h.runToEnd(h.helperRequest("test.stress.unbroken", "unbroken"))
	if finished.State != Succeeded {
		t.Fatalf("state = %s, want succeeded (error: %s)", finished.State, finished.Error)
	}
	if finished.Stdout.Bytes < flooded {
		t.Errorf("counted %d bytes, want at least %d", finished.Stdout.Bytes, flooded)
	}
	// Split at maxLineBytes, so the count is the flood over that bound — not
	// zero, and not one per read.
	if want := finished.Stdout.Bytes / maxLineBytes; finished.Stdout.Lines < want {
		t.Errorf("counted %d pieces of a %d-byte unbroken stream, want at least %d",
			finished.Stdout.Lines, finished.Stdout.Bytes, want)
	}
	limits := RetentionLimits()
	if finished.Stdout.Stored > limits.KeptBytesPerStream {
		t.Errorf("stored %d bytes, over the published %d",
			finished.Stdout.Stored, limits.KeptBytesPerStream)
	}
}

// TestBothStreamsFloodWithoutMixing runs stdout and stderr full at once.
//
// One capture per stream, one goroutine each, and the only shared thing between
// them is the diagnostic collector. A byte of stderr in the stdout log would
// mean the two had been merged somewhere — which is exactly what a supervisor
// that used a single pipe for both would produce, and is invisible until
// something floods both at once.
func TestBothStreamsFloodWithoutMixing(t *testing.T) {
	const flooded = 8 << 20
	action := modeAction("both", "flood-both", "8388608")
	action.Timeout = 180
	h := newHarness(t, fixtureProfile(t, "test.stress.both", action))

	finished := h.runToEnd(h.helperRequest("test.stress.both", "both"))
	if finished.State != Succeeded {
		t.Fatalf("state = %s, want succeeded (error: %s)", finished.State, finished.Error)
	}
	if finished.Stdout.Bytes == 0 || finished.Stderr.Bytes == 0 {
		t.Fatalf("one stream produced nothing: stdout %d, stderr %d",
			finished.Stdout.Bytes, finished.Stderr.Bytes)
	}
	if total := finished.Stdout.Bytes + finished.Stderr.Bytes; total < flooded {
		t.Errorf("the two streams carried %d bytes together, want at least %d", total, flooded)
	}

	limits := RetentionLimits()
	for _, stream := range []struct {
		name    string
		log     StreamLog
		content byte
		other   byte
	}{
		{"stdout", finished.Stdout, 'q', 'z'},
		{"stderr", finished.Stderr, 'z', 'q'},
	} {
		if stream.log.Stored > limits.KeptBytesPerStream {
			t.Errorf("%s stored %d bytes, over the published %d",
				stream.name, stream.log.Stored, limits.KeptBytesPerStream)
		}
		if !stream.log.Truncated {
			t.Errorf("%s was not recorded as truncated after a flood: %+v", stream.name, stream.log)
		}
		raw, err := h.service.Logs(finished.ID, stream.name, true)
		if err != nil {
			t.Fatalf("reading the raw %s log: %v", stream.name, err)
		}
		if int64(len(raw)) > limits.MaxLogFileBytes {
			t.Errorf("the raw %s log is %d bytes, over the published %d",
				stream.name, len(raw), limits.MaxLogFileBytes)
		}
		// 'q' and 'z' appear nowhere in the elision marker, so the marker's own
		// prose cannot be mistaken for the other stream's bytes and there is
		// nothing to strip before looking.
		if idx := indexOfByte(raw, stream.other); idx >= 0 {
			t.Errorf("the %s log contains a %q at offset %d: the streams were mixed",
				stream.name, stream.other, idx)
		}
	}
}

// TestTwoNoisyJobsKeepSeparateRecordsAndLogs runs two floods at once.
//
// Concurrency is bounded at two by default, both jobs write hard, and each has
// its own directory. What is asserted is that neither job's record or log
// picked up the other's bytes — the failure a shared buffer or a shared log
// name would produce, and one that only appears when two noisy jobs overlap.
func TestTwoNoisyJobsKeepSeparateRecordsAndLogs(t *testing.T) {
	stdoutFlood := modeAction("mine", "flood", "2097152")
	stdoutFlood.Timeout = 180
	stderrFlood := modeAction("theirs", "flood-stderr", "2097152")
	stderrFlood.Timeout = 180
	h := newHarness(t, fixtureProfile(t, "test.stress.parallel", stdoutFlood, stderrFlood))

	var wg sync.WaitGroup
	finished := make([]*Job, 2)
	for index, action := range []string{"mine", "theirs"} {
		wg.Add(1)
		go func(index int, action string) {
			defer wg.Done()
			submitted, err := h.service.Submit(h.helperRequest("test.stress.parallel", action))
			if err != nil {
				t.Errorf("submitting %s: %v", action, err)
				return
			}
			finished[index] = h.waitFor(submitted.ID)
		}(index, action)
	}
	wg.Wait()

	mine, theirs := finished[0], finished[1]
	if mine == nil || theirs == nil {
		t.Fatal("one of the two jobs did not finish")
	}
	if mine.ID == theirs.ID {
		t.Fatal("both jobs were given the same id")
	}
	// Each wrote to one stream only, which is what makes crossover detectable
	// in the record itself rather than only in the bytes.
	if mine.Stdout.Bytes == 0 || mine.Stderr.Bytes != 0 {
		t.Errorf("the stdout job recorded stdout %d / stderr %d, want output on stdout only",
			mine.Stdout.Bytes, mine.Stderr.Bytes)
	}
	if theirs.Stderr.Bytes == 0 || theirs.Stdout.Bytes != 0 {
		t.Errorf("the stderr job recorded stdout %d / stderr %d, want output on stderr only",
			theirs.Stdout.Bytes, theirs.Stderr.Bytes)
	}

	limits := RetentionLimits()
	for _, j := range []*Job{mine, theirs} {
		if j.Stdout.Stored > limits.KeptBytesPerStream || j.Stderr.Stored > limits.KeptBytesPerStream {
			t.Errorf("job %s stored %d/%d, over the published %d",
				j.ID, j.Stdout.Stored, j.Stderr.Stored, limits.KeptBytesPerStream)
		}
	}
	// The letters differ per action, and neither appears in the elision marker,
	// so one job's log holding the other's letter is a crossover rather than a
	// coincidence or the marker's own prose.
	if body := h.mustLog(mine.ID, "stdout"); indexOfByte([]byte(body), 'z') >= 0 {
		t.Error("the stdout job's log contains the other job's letter")
	}
	if body := h.mustLog(theirs.ID, "stderr"); indexOfByte([]byte(body), 'x') >= 0 {
		t.Error("the stderr job's log contains the other job's letter")
	}
}

// TestASlowFloodCrossesTheRetentionBoundariesAndStaysTrue is the fourth of the
// prompt's five shapes: output arriving in bursts over time rather than in one
// go, crossing the head/tail boundary and the tail's compaction point while the
// reader is idle in between.
//
// A burst-and-pause producer is the one that finds a capture whose bookkeeping
// is right only when reads are back to back.
func TestASlowFloodCrossesTheRetentionBoundariesAndStaysTrue(t *testing.T) {
	if testing.Short() {
		t.Skip("the pauses make this one slow by construction")
	}
	const flooded = 4 << 20
	action := modeAction("slow", "flood-slow", "4194304", "40")
	action.Timeout = 180
	h := newHarness(t, fixtureProfile(t, "test.stress.slow", action))

	finished := h.runToEnd(h.helperRequest("test.stress.slow", "slow"))
	if finished.State != Succeeded {
		t.Fatalf("state = %s, want succeeded (error: %s)", finished.State, finished.Error)
	}
	if finished.Stdout.Bytes < flooded {
		t.Errorf("counted %d bytes, want at least %d", finished.Stdout.Bytes, flooded)
	}

	limits := RetentionLimits()
	if finished.Stdout.Stored > limits.KeptBytesPerStream {
		t.Errorf("stored %d, over the published %d", finished.Stdout.Stored, limits.KeptBytesPerStream)
	}
	// Bytes = Stored + Dropped, exactly. An accounting that drifted across the
	// pauses would show up here and nowhere else.
	if sum := finished.Stdout.Stored + finished.Stdout.Dropped; sum != finished.Stdout.Bytes {
		t.Errorf("stored %d + dropped %d = %d, but the program wrote %d",
			finished.Stdout.Stored, finished.Stdout.Dropped, sum, finished.Stdout.Bytes)
	}
	raw, err := h.service.Logs(finished.ID, "stdout", true)
	if err != nil {
		t.Fatalf("reading the raw log: %v", err)
	}
	if int64(len(raw)) > limits.MaxLogFileBytes {
		t.Errorf("the raw log is %d bytes, over the published %d", len(raw), limits.MaxLogFileBytes)
	}
	// The head is the start of the run and the tail is the end of it. Both
	// halves of a truncated log have to be the real thing, or the elision has
	// eaten something it should have kept.
	if !strings.HasPrefix(string(raw), strings.Repeat("s", 99)) {
		t.Error("the head of the log is not the first thing the program wrote")
	}
	if !strings.HasSuffix(string(raw), strings.Repeat("s", 99)+"\n") {
		t.Error("the tail of the log is not the last thing the program wrote")
	}
}

// TestAProcessTreeThatIgnoresSIGTERMIsStillStopped is the fifth shape, and the
// half of [execution.supervise] no fixture had run before `229`.
//
// Every cancellation test until now used the `sleep` mode, which dies on the
// polite signal — so the grace period elapsing and the forceful pass actually
// reaching a whole group were both untested. This fixture traps SIGTERM at
// three levels of a process tree and none of them leaves voluntarily.
//
// The assertion is about the process GROUP rather than a list of pids. The
// executor puts each job in a group of its own, so "the group is gone" is
// exactly the question worth asking: a stop that reached the leader and left a
// grandchild holding the workspace would leave the group alive, and no field of
// the job record would say so.
func TestAProcessTreeThatIgnoresSIGTERMIsStillStopped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("there is no SIGTERM to ignore; proc_windows.go stops a tree through the job object")
	}
	// Far longer than the test is willing to wait, so a pass cannot be the
	// program having finished on its own. Depth 2: leader, child, grandchild.
	action := modeAction("defiant", "defiant", "600", "2")
	action.Timeout = 300
	h := newHarness(t, fixtureProfile(t, "test.stress.defiant", action))

	submitted, err := h.service.Submit(h.helperRequest("test.stress.defiant", "defiant"))
	if err != nil {
		t.Fatalf("submitting: %v", err)
	}
	running := waitForState(t, h, submitted.ID, Running)
	leader := leaderPID(t, running)
	if !processGroupAlive(leader) {
		t.Fatalf("the job's process group %d is not alive while the job is running", leader)
	}
	// The tree has to actually exist before it is worth stopping. Counting the
	// group's members is Linux-only; elsewhere the group's liveness is all this
	// can honestly claim, and the test says which it checked.
	if members := groupMembers(leader); members >= 0 {
		deadline := time.Now().Add(20 * time.Second)
		for groupMembers(leader) < 3 && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if got := groupMembers(leader); got < 3 {
			t.Fatalf("the tree has %d process(es), want 3 (leader, child, grandchild)", got)
		}
	} else {
		t.Logf("this platform cannot count process-group members; "+
			"the check is that group %d is alive before and gone after", leader)
	}

	started := time.Now()
	if _, err := h.service.Cancel(submitted.ID); err != nil {
		t.Fatalf("cancelling: %v", err)
	}
	finished := h.waitFor(submitted.ID)
	elapsed := time.Since(started)

	if finished.State != Cancelled {
		t.Fatalf("state = %s, want cancelled (error: %s)", finished.State, finished.Error)
	}
	// SIGTERM, terminateGrace, SIGKILL. A stop that landed before the grace
	// period could have elapsed would mean the fixture is not ignoring the
	// polite signal, and the forceful pass is not what this measured.
	if elapsed < terminateGrace {
		t.Errorf("the tree stopped in %s, inside the %s grace period: "+
			"the fixture is not actually ignoring SIGTERM", elapsed, terminateGrace)
	}
	if elapsed > terminateGrace+30*time.Second {
		t.Errorf("the tree took %s to stop; the grace period is %s", elapsed, terminateGrace)
	}

	// Nothing survives. A grandchild left holding the workspace is the failure
	// this whole mechanism exists to prevent, and it is invisible in the record.
	deadline := time.Now().Add(15 * time.Second)
	for processGroupAlive(leader) {
		if time.Now().After(deadline) {
			t.Fatalf("the job's process group %d still has members after cancellation "+
				"(%d counted): the tree outlived the job", leader, groupMembers(leader))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if processAlive(leader) {
		t.Errorf("the leader %d is still running after cancellation", leader)
	}
}

// leaderPID reads the child's pid out of the record's own history.
//
// The executor records it as the note on the transition to Running — `pid N` —
// which is the only place the supervised process's pid is written down. The
// record is saved on every transition, so this is readable while the job runs,
// unlike the logs, which are written once the streams have closed.
func leaderPID(t *testing.T, j *Job) int {
	t.Helper()
	for _, event := range j.History {
		if event.State != Running {
			continue
		}
		var pid int
		if _, err := fmtSscan(strings.TrimPrefix(event.Note, "pid "), &pid); err == nil && pid > 0 {
			return pid
		}
	}
	t.Fatalf("the record has no `pid N` note on its transition to running: %+v", j.History)
	return 0
}

func indexOfByte(data []byte, want byte) int {
	for index, value := range data {
		if value == want {
			return index
		}
	}
	return -1
}

// fmtSscan is fmt.Sscan behind a name that does not need the import in every
// caller. Kept trivial deliberately.
func fmtSscan(text string, target *int) (int, error) {
	value := 0
	for _, digit := range text {
		if digit < '0' || digit > '9' {
			return 0, errNotANumber
		}
		value = value*10 + int(digit-'0')
	}
	if text == "" {
		return 0, errNotANumber
	}
	*target = value
	return 1, nil
}

var errNotANumber = os.ErrInvalid
