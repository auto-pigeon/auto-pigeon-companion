package playrun_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
)

// When a run is readable (NEW_624, issue #45).
//
// A run id this Companion hands out — the one Start returns, and the one a
// refused duplicate press is told about so the page can follow it — is a
// promise that GET /api/v1/play/runs/<id> answers with that run. Windows build
// 43190334060 broke it: a run being moved from `compiling` to `installing` read
// back as "no such run", because a read overlapped the rename that replaces
// the record, and on Windows that read can be told the file is not there.
//
// Each request to the server opens its own Store on the same directory, so
// these tests use two stores on one root, as the server does. They hold the
// write at the replace and the first save of a claimed run, and look while it
// is held; they never sleep an interleaving into existence. The one bounded
// wait in each is how long they watch for something that must NOT happen.

// notWithin is how long a test watches for a read that must not happen while
// a write is held. It bounds a negative observation only: no outcome depends on
// the work finishing inside it.
const notWithin = 300 * time.Millisecond

func visibleRecord(id string, state playrun.State) *playrun.Record {
	return &playrun.Record{
		SchemaVersion: playrun.SchemaVersion, ID: id, Request: goodRequest(),
		State: state, CreatedAt: time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC),
	}
}

// T1: a record is never opened while it is being replaced, by this store or
// by any other store this process has on the same directory.
func TestARecordIsNeverReadWhileItIsBeingReplaced(t *testing.T) {
	root := t.TempDir()
	writer, err := playrun.OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := playrun.OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	const id = "20261122T000000Z-visible0000000a"
	if err = writer.Save(visibleRecord(id, playrun.Compiling)); err != nil {
		t.Fatal(err)
	}

	replacing, release := make(chan struct{}), make(chan struct{})
	var once, releasing sync.Once
	letGo := func() { releasing.Do(func() { close(release) }) }
	t.Cleanup(letGo)
	playrun.HoldStoreReplace(writer, func(*playrun.Record) {
		once.Do(func() {
			close(replacing)
			<-release
		})
	})
	reading := make(chan struct{}, 1)
	playrun.ObserveStoreReads(reader, func() {
		select {
		case reading <- struct{}{}:
		default:
		}
	})

	saved := make(chan error, 1)
	go func() { saved <- writer.Save(visibleRecord(id, playrun.Installing)) }()
	waitFor(t, "the replace", replacing)

	type loaded struct {
		record *playrun.Record
		err    error
	}
	read := make(chan loaded, 1)
	go func() {
		record, err := reader.Load(id)
		read <- loaded{record, err}
	}()
	select {
	case <-reading:
		t.Error("the record was opened while it was being replaced: on Windows that read can answer \"no such run\"")
	case <-time.After(notWithin):
	}
	letGo()

	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	got := <-read
	if got.err != nil {
		t.Fatalf("a run being saved read back as %v", got.err)
	}
	if got.record.State != playrun.Installing {
		t.Errorf("the read waited for the replace and still saw %s, not the record written", got.record.State)
	}
}

// T1 on the real file system: one store rewriting a record through every
// state while others read it. On Linux and macOS a rename never hides its
// target and this is a control; on Windows it is the native regression test
// for #45.
func TestARecordBeingRewrittenIsAlwaysFound(t *testing.T) {
	root := t.TempDir()
	writer, err := playrun.OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	const id = "20261122T000000Z-visible0000000b"
	if err = writer.Save(visibleRecord(id, playrun.Queued)); err != nil {
		t.Fatal(err)
	}
	states := []playrun.State{playrun.DownloadingMap, playrun.Compiling, playrun.Installing, playrun.Launching}
	const saves = 200

	done := make(chan struct{})
	var group sync.WaitGroup
	var mu sync.Mutex
	var failures []string
	for reader := range 4 {
		store, err := playrun.OpenStore(root)
		if err != nil {
			t.Fatal(err)
		}
		group.Add(1)
		go func() {
			defer group.Done()
			for reads := 0; ; reads++ {
				select {
				case <-done:
					return
				default:
				}
				if _, err := store.Load(id); err != nil {
					mu.Lock()
					failures = append(failures, fmt.Sprintf("reader %d, read %d: %v", reader, reads, err))
					mu.Unlock()
					return
				}
			}
		}()
	}
	for i := range saves {
		if err := writer.Save(visibleRecord(id, states[i%len(states)])); err != nil {
			close(done)
			group.Wait()
			t.Fatalf("save %d: %v", i, err)
		}
	}
	close(done)
	group.Wait()
	if len(failures) > 0 {
		t.Fatalf("a record being rewritten was not found: %v", failures)
	}
}

// T1: a press refused as a duplicate is told the running run's id, and the
// page follows that id at once. It must already be readable — even when the
// press lands while the run's first record is still being written.
func TestARefusedPressNamesARunThatCanBeRead(t *testing.T) {
	h := newHarness(t)
	// Another request's coordinator: its own Store on the same directory, the
	// process's one registry. That is how two presses reach the server.
	otherStore, err := playrun.OpenStore(h.store.Root())
	if err != nil {
		t.Fatal(err)
	}
	other, err := playrun.NewService(otherStore, h.deps)
	if err != nil {
		t.Fatal(err)
	}
	compiling, letCompile := h.holdCompile()

	saving, proceed := make(chan struct{}), make(chan struct{})
	var once, releasing sync.Once
	release := func() { releasing.Do(func() { close(proceed) }) }
	t.Cleanup(release)
	playrun.HoldStoreSaves(h.store, func(record *playrun.Record) error {
		if record.State == playrun.Queued {
			once.Do(func() {
				close(saving)
				<-proceed
			})
		}
		return nil
	})

	type started struct {
		record *playrun.Record
		err    error
	}
	first := make(chan started, 1)
	go func() {
		record, err := h.service.Start(goodRequest())
		first <- started{record, err}
	}()
	waitFor(t, "the first press's record being written", saving)

	type refusal struct {
		runID   string
		err     error
		readErr error
	}
	refused := make(chan refusal, 1)
	go func() {
		_, err := other.Start(goodRequest())
		var running *playrun.AlreadyRunningError
		if !errors.As(err, &running) {
			refused <- refusal{err: fmt.Errorf("the duplicate press was not refused as running: %v", err)}
			return
		}
		_, readErr := other.Get(running.RunID)
		refused <- refusal{runID: running.RunID, readErr: readErr}
	}()
	var got refusal
	answered := false
	select {
	case got = <-refused:
		answered = true
	case <-time.After(notWithin):
	}
	release()
	if !answered {
		got = <-refused
	}
	winner := <-first
	if winner.err != nil {
		t.Fatal(winner.err)
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.runID != winner.record.ID {
		t.Errorf("the duplicate was told %s, not the running %s", got.runID, winner.record.ID)
	}
	if got.readErr != nil {
		t.Errorf("the run a refused press was told about read back as %v (answered while its record was held: %v)", got.readErr, answered)
	}

	waitFor(t, "the compile", compiling)
	letCompile()
	h.await(winner.record.ID)
	h.idle()
}
