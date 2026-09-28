package job

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// NEW_265: a running job's output is readable while it runs, from the file its
// tool actually fills.

// sidecarAction is EricW's shape: the transcript is a declared `.log` output
// and stdout carries nothing.
func sidecarAction(id string, delayMS, lines, pauseMS, exit string, fatal ...any) fixtureAction {
	args := []any{helperFlag, "sidecar", "{output.log}", delayMS, lines, pauseMS, exit}
	args = append(args, fatal...)
	return fixtureAction{
		ID:         id,
		Title:      "Compile the map",
		Executable: "helper",
		Args:       args,
		Outputs: []map[string]any{
			{"name": "log", "title": "Compiler log", "role": "test.compile.log", "path": "level.log", "optional": true},
		},
		Roots: []map[string]any{
			{"role": "workspace", "access": "read_write", "purpose": "write the log"},
		},
		Timeout: 60,
	}
}

// follow reads a job's output the way the page does — auto source, cursor
// carried forward — until the job has stopped and been read to the end, and
// reports what it saw while the job was still running.
func follow(t *testing.T, h *harness, id string) (all string, whileRunning string, sources []string) {
	t.Helper()
	var text strings.Builder
	from := ""
	var cursor int64 = -1
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		chunk, err := h.service.Output(id, OutputRequest{Source: "auto", From: from, Offset: cursor})
		if err != nil {
			t.Fatalf("reading the output of %s: %v", id, err)
		}
		if chunk.Reset {
			text.Reset()
		}
		if from != chunk.Source.ID {
			sources = append(sources, chunk.Source.ID)
		}
		from = chunk.Source.ID
		text.WriteString(chunk.Text)
		cursor = chunk.Next
		if !chunk.Terminal {
			whileRunning = text.String()
		}
		if chunk.Complete {
			return text.String(), whileRunning, sources
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("the output of %s never completed; so far: %q", id, text.String())
	return "", "", nil
}

func TestADelayedSidecarOnlyToolIsReadWhileItRunsAndItsFatalLineAfterItFails(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.output.sidecar",
		sidecarAction("compile", "400", "8", "150", "1", "FATAL:", "the", "map", "leaks")))

	submitted, err := h.service.Submit(h.helperRequest("test.output.sidecar", "compile"))
	if err != nil {
		t.Fatal(err)
	}
	// Before the tool has created its log, the view says it is waiting for
	// that file rather than that nothing was printed.
	waitForState(t, h, submitted.ID, Running)
	early, err := h.service.Output(submitted.ID, OutputRequest{Offset: -1})
	if err != nil {
		t.Fatal(err)
	}
	if early.Source.Kind != OutputSidecar || early.Source.ID != "log:log" {
		t.Errorf("before the log exists the source is %+v, want the declared log", early.Source)
	}
	if !strings.Contains(early.Source.Label, "level.log") || !strings.Contains(early.Source.Label, "Compiler log") {
		t.Errorf("the source label %q does not name the file and its title", early.Source.Label)
	}

	all, running, sources := follow(t, h, submitted.ID)
	if !strings.Contains(running, "sidecar line 1") || !strings.Contains(running, "sidecar line 3") {
		t.Errorf("while the job ran the view held %q; real lines must appear before it stops", running)
	}
	if strings.Contains(running, "sidecar line 8") && strings.Contains(running, "FATAL") {
		t.Log("the whole transcript was read before the job was recorded finished; still correct")
	}
	for i := 1; i <= 8; i++ {
		if strings.Count(all, "sidecar line "+itoa(i)+"\n") != 1 {
			t.Errorf("line %d appears %d times in %q; every line exactly once", i, strings.Count(all, "sidecar line "+itoa(i)+"\n"), all)
		}
	}
	if !strings.Contains(all, "FATAL: the map leaks") {
		t.Errorf("the final fatal line is missing after the failure: %q", all)
	}
	if len(sources) != 1 || sources[0] != "log:log" {
		t.Errorf("sources followed = %v, want only the transcript", sources)
	}

	finished := h.waitFor(submitted.ID)
	if finished.State != Failed {
		t.Fatalf("state = %s, want failed", finished.State)
	}
	if len(finished.SidecarLogs) != 1 || finished.SidecarLogs[0].Name != "log" {
		t.Errorf("the record's sidecar logs = %+v", finished.SidecarLogs)
	}
	// After the job, the same source is the collected artifact.
	again, err := h.service.Output(submitted.ID, OutputRequest{Source: "log:log", Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !again.Complete || !strings.Contains(again.Text, "FATAL: the map leaks") {
		t.Errorf("after the job the transcript reads %+v", again)
	}
}

func TestAStdoutOnlyToolIsReadWhileItRunsAndTheCursorSurvivesTheEnd(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.output.stdout",
		modeAction("print", "stdout-slow", "10", "120")))
	submitted, err := h.service.Submit(h.helperRequest("test.output.stdout", "print"))
	if err != nil {
		t.Fatal(err)
	}
	all, running, sources := follow(t, h, submitted.ID)
	if !strings.Contains(running, "stdout line 2") {
		t.Errorf("while the job ran the view held %q", running)
	}
	for i := 1; i <= 10; i++ {
		if strings.Count(all, "stdout line "+itoa(i)+"\n") != 1 {
			t.Errorf("line %d appears %d times; the live cursor and the stored log must line up: %q",
				i, strings.Count(all, "stdout line "+itoa(i)+"\n"), all)
		}
	}
	if len(sources) != 1 || sources[0] != "stdout" {
		t.Errorf("sources followed = %v, want stdout", sources)
	}
}

func TestAnEmptyStageCompletesWithNothingAndSaysSo(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.output.empty", modeAction("quiet", "exit", "0")))
	// `exit 0` prints one line; an action that prints nothing at all is the
	// write action with its output file, which says `wrote` — so use stderr
	// only, which leaves stdout empty and still has to be found.
	finished := h.runToEnd(h.helperRequest("test.output.empty", "quiet"))
	chunk, err := h.service.Output(finished.ID, OutputRequest{Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !chunk.Complete || !chunk.Terminal {
		t.Errorf("a finished job's output is not complete: %+v", chunk)
	}

	h2 := newHarness(t, fixtureProfile(t, "test.output.none", sidecarAction("compile", "0", "0", "0", "0")))
	none := h2.runToEnd(h2.helperRequest("test.output.none", "compile"))
	empty, err := h2.service.Output(none.ID, OutputRequest{Offset: -1})
	if err != nil {
		t.Fatal(err)
	}
	if !empty.Complete || empty.Text != "" || empty.Size != 0 {
		t.Errorf("an empty stage reads %+v; want complete and empty", empty)
	}
}

func TestARewrittenLogResetsTheReaderRatherThanSkippingIt(t *testing.T) {
	action := fixtureAction{
		ID: "rewrite", Title: "Rewrite", Executable: "helper",
		Args: []any{helperFlag, "sidecar-rewrite", "{output.log}", "700"},
		Outputs: []map[string]any{
			{"name": "log", "title": "Log", "role": "test.run.log", "path": "run.log", "optional": true},
		},
		Roots:   []map[string]any{{"role": "workspace", "access": "read_write", "purpose": "write"}},
		Timeout: 60,
	}
	h := newHarness(t, fixtureProfile(t, "test.output.rewrite", action))
	submitted, err := h.service.Submit(h.helperRequest("test.output.rewrite", "rewrite"))
	if err != nil {
		t.Fatal(err)
	}
	// The transcript is declared on the record once the job has resolved.
	waitForState(t, h, submitted.ID, Running)
	var cursor int64
	deadline := time.Now().Add(20 * time.Second)
	sawFirst, sawReset := false, false
	for time.Now().Before(deadline) {
		chunk, err := h.service.Output(submitted.ID, OutputRequest{Source: "log:log", Offset: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(chunk.Text, "first pass") {
			sawFirst = true
		}
		if chunk.Reset && sawFirst {
			sawReset = true
			if !strings.HasPrefix(chunk.Text, "second pass") && chunk.Text != "" {
				t.Errorf("after a reset the text starts %q", chunk.Text)
			}
		}
		cursor = chunk.Next
		if chunk.Complete {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !sawFirst || !sawReset {
		t.Errorf("first pass seen = %v, reset seen = %v; a shorter file must restart the reader", sawFirst, sawReset)
	}
}

func TestTheOutputViewRedactsCredentialsAndRefusesALinkOutOfTheJob(t *testing.T) {
	secret := "aub-session-token-0123456789abcdef"
	h := newHarness(t, fixtureProfile(t, "test.output.secret",
		sidecarAction("compile", "0", "1", "0", "0", "token", secret)),
		func(o *Options) { o.Secrets = func() []string { return []string{secret} } })
	finished := h.runToEnd(h.helperRequest("test.output.secret", "compile"))
	chunk, err := h.service.Output(finished.ID, OutputRequest{Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(chunk.Text, secret) || !strings.Contains(chunk.Text, "token") {
		t.Errorf("the view is not redacted: %q", chunk.Text)
	}

	// A transcript replaced by a link to a file outside the job is refused, not
	// followed.
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("not the job's\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	j, _ := h.service.Get(finished.ID)
	sidecar := SidecarLog{Name: "log", Path: filepath.Join(h.store.layout(j.ID).Workspace, "linked.log")}
	if err := os.MkdirAll(filepath.Dir(sidecar.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, sidecar.Path); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
	running := *j
	running.State = Running
	if _, err := h.service.sidecarPath(&running, sidecar); err == nil {
		t.Error("a symbolic link out of the job was accepted as its transcript")
	}
	sidecar.Path = outside
	if _, err := h.service.sidecarPath(&running, sidecar); err == nil {
		t.Error("a transcript path outside the job's directory was accepted")
	}
}

func TestAnUnknownSourceIsRefusedByName(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.output.unknown", modeAction("print", "echo", "hi")))
	finished := h.runToEnd(h.helperRequest("test.output.unknown", "print"))
	if _, err := h.service.Output(finished.ID, OutputRequest{Source: "log:../../etc/passwd"}); err == nil ||
		!strings.Contains(err.Error(), "stdout") {
		t.Errorf("an undeclared source answered %v; want a refusal naming what exists", err)
	}
}

// The capture's absolute offsets and the stored log agree across a flood: a
// cursor handed out while the program ran continues on the file it left.
func TestCaptureOffsetsAndTheStoredLogAgreeAcrossADroppedMiddle(t *testing.T) {
	c := newCapture("stdout", nil, nil)
	var written bytes.Buffer
	line := []byte(strings.Repeat("a", 99) + "\n")
	for written.Len() < headBytes+tailBytes+300_000 {
		c.write(line)
		written.Write(line)
	}
	total := int64(written.Len())

	head, start, got, gap := c.readFrom(0, 100)
	if start != 0 || got != total || gap != 0 || !bytes.Equal(head, written.Bytes()[:100]) {
		t.Fatalf("the start of the stream reads wrong: start %d total %d gap %d", start, got, gap)
	}
	middle := int64(headBytes + 1000)
	chunk, start, _, gap := c.readFrom(middle, 64)
	if gap == 0 || start <= middle {
		t.Fatalf("a cursor in the dropped middle was not moved to the tail: start %d gap %d", start, gap)
	}
	if !bytes.Equal(chunk, written.Bytes()[start:start+64]) {
		t.Fatal("the tail chunk is not the bytes at that offset")
	}

	stored := c.bytesKept()
	summary := c.summary(stdoutLogName)
	// A cursor into what the stored log still holds reads the same bytes from
	// the file as it did from memory.
	near := total - 5000
	live, liveStart, _, _ := c.readFrom(near, 64)
	var out OutputChunk
	from := storedStreamRead(stored, summary, near, 64, &out)
	if out.Offset != liveStart || !bytes.Equal(from, live) || !bytes.Equal(from, written.Bytes()[near:near+64]) {
		t.Errorf("the stored log at offset %d reads different bytes from the live capture", near)
	}
	// A cursor into what the running capture held and the stored log dropped
	// (the tail is kept at twice its bound while the program runs) is moved
	// forward and the skip is reported, never read from the wrong place.
	var moved OutputChunk
	skipped := storedStreamRead(stored, summary, start, 64, &moved)
	if moved.Offset < start || (moved.Offset > start && moved.Gap != moved.Offset-start) ||
		!bytes.Equal(skipped, written.Bytes()[moved.Offset:moved.Offset+64]) {
		t.Errorf("a cursor the stored log no longer holds read offset %d gap %d", moved.Offset, moved.Gap)
	}
	var tail OutputChunk
	last := storedStreamRead(stored, summary, total-10, 64, &tail)
	if !bytes.Equal(last, written.Bytes()[total-10:]) || tail.Size != total {
		t.Errorf("the end of the stored log reads %q (size %d)", last, tail.Size)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
