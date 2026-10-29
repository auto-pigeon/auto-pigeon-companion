package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/lockfile"
)

// NEW_368 (post-push review): a replacement killed holding the update lock is
// recovered at once (its process is gone), and two Companions that start an
// update at that moment never both get past it. Forced with barriers: the
// first stops between judging the dead holder's lock and breaking it; the
// second meanwhile reaches the activation step; then the first resumes.
func TestTwoUpdatesThatFindADeadHoldersLockNeverBothActivate(t *testing.T) {
	r := newRig(t)
	if err := os.MkdirAll(dirOf(r.root), 0o700); err != nil {
		t.Fatal(err)
	}
	gone := exec.Command(os.Args[0], "-test.run=^$")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	raw, _ := json.Marshal(lockfile.Holder{SchemaVersion: lockfile.SchemaVersion, PID: gone.ProcessState.Pid(), Host: host,
		Program: "a replacement that was killed", Nonce: "dead", Since: time.Now().UTC()})
	if err := os.WriteFile(lockfile.PathFor(lockPath(r.root)), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	paused, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	lockfile.Hooks.BeforeBreak = func(string) {
		first := false
		once.Do(func() { first = true })
		if first {
			close(paused)
			<-resume
		}
	}
	contended := make(chan struct{})
	var contendedOnce sync.Once
	lockfile.Hooks.GuardContended = func(string) { contendedOnce.Do(func() { close(contended) }) }
	t.Cleanup(func() { lockfile.Hooks.BeforeBreak, lockfile.Hooks.GuardContended = nil, nil })

	var inside, most atomic.Int32
	entered := make(chan string, 2)
	release := make(chan struct{})
	start := func(name string) chan error {
		request := r.request
		request.Fault = func(at string) error {
			if at != "journal:"+string(StateActivating) {
				return nil
			}
			now := inside.Add(1)
			for {
				seen := most.Load()
				if now <= seen || most.CompareAndSwap(seen, now) {
					break
				}
			}
			entered <- name
			<-release
			inside.Add(-1)
			return nil
		}
		out := make(chan error, 1)
		go func() { _, err := Apply(context.Background(), request); out <- err }()
		return out
	}

	a := start("A")
	<-paused // A judged the dead holder's lock abandoned
	b := start("B")
	entries := 0
	select {
	case <-entered:
		entries++ // B is activating
	case <-contended:
	}
	close(resume)
	results := map[string]error{}
	deadline := time.After(20 * time.Second)
	for entries < 2 && len(results) == 0 {
		select {
		case <-entered:
			entries++
		case err := <-a:
			results["A"] = err
		case err := <-b:
			results["B"] = err
		case <-deadline:
			t.Fatal("neither a second activation nor a refusal")
		}
	}
	close(release)
	if _, ok := results["A"]; !ok {
		results["A"] = <-a
	}
	if _, ok := results["B"]; !ok {
		results["B"] = <-b
	}
	if most.Load() != 1 {
		t.Fatalf("%d updates activated at once; results %v", most.Load(), results)
	}
	succeeded := 0
	for name, err := range results {
		switch {
		case err == nil:
			succeeded++
		case !errors.Is(err, ErrRefused):
			t.Fatalf("%s: %v; want success or a refusal", name, err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("results %v; want one update and one refusal", results)
	}
}
