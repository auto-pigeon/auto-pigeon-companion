// Package lockfile is the Companion's one writer for mutable local state.
//
// # The failure this exists to stop
//
// Every state file this program writes is written atomically: a temporary file
// in the destination directory, then a rename. That stops a *torn* write — a
// half-written `config.json` after a crash or a full disk — and it is the right
// thing to do. It does nothing at all about the other failure, which is a *lost
// update*.
//
// Two Companion processes is not a hypothetical: the GUI server is one process
// and `companion job run` in another terminal is a second, and the installer's
// final page starts the app while a shell may already have it open. Both read
// `config.json`, both change a different field, both write. The second rename
// wins and the first change is gone, with no error anywhere. On `config.json`
// that costs a setting. On `catalog-state.json` it costs a revocation, which is
// the file's whole reason to exist.
//
// # A lock file, changed only under the operating system's lock
//
// The lock is a RECORD file created with `O_CREATE|O_EXCL`: exactly one caller
// creates it, it names its holder, and every Companion — released ones
// included — reads it the same way. What a record cannot do is let go when its
// holder dies, so an abandoned one has to be judged and broken. And a judgement
// about a pathname goes stale the moment it is made: two processes that both
// judged a lock abandoned used to each remove whatever then occupied the path —
// the second removing the live lock the first had just created — and an old
// holder's release or heartbeat could likewise remove or refresh its
// replacement (NEW_368, post-push review; reproduced with barriers before this
// was changed). Renaming before removing, or reading again before removing,
// narrows that window and never closes it.
//
// So every change to the record — creating it, judging and breaking an
// abandoned one, refreshing it, releasing it — is made holding the GUARD: the
// operating system's exclusive lock ([oslock]: flock, or LockFileEx on
// Windows) on a sibling file, `<lock>.guard`, that is never removed. The kernel
// lets go of the guard when its holder dies, so the guard itself never needs
// breaking. It is held for a few file operations and never through the
// caller's work; nothing waits for it longer than its own deadline.
//
// # The decisions
//
//  1. **The holder is recorded, in the lock file.** A person who is told
//     "somebody else is writing this" can be told who, since when, and on which
//     machine. A lock that only says "busy" is one people delete.
//  2. **A stale lock is refreshed, not re-written.** The holder bumps the lock
//     file's modification time every [Options.Stale] quarter — with the guard,
//     and only while the record there is still its own, so an old heartbeat
//     never keeps a replacement looking alive. Nothing rewrites the bytes.
//  3. **An abandoned lock is broken with the guard held**, judged and removed
//     in one step, so no other Companion can publish a lock in between.
//  4. **Release checks the nonce it wrote, with the guard held.** A process
//     whose own lock was broken while it was descheduled reports that it lost
//     the lock, and deletes nothing.
//  5. **Work whose lock was lost is not reported as protected.** [With] returns
//     [ErrLost] when the body succeeded but the release found another holder.
//     That cannot undo what the body wrote; it says so.
//
// # What is assumed, and where it stops
//
// The guard is per host and advisory: the Companion's configuration, catalog
// and update folders are on a local file system of this machine. Across
// machines, or on a network file system where flock may be emulated or
// ignored, nothing here promises exclusion. A platform with no operating-system
// lock wired up (none of the six release targets) refuses to take a lock.
//
// Companions released before this (v1.332 and earlier) do not take the guard.
// They still read and respect a live record, and a new Companion still reads
// and respects theirs, but between an old and a new process the old one's
// break is not excluded. The supported upgrade does not depend on it: the
// self-update starts the new Companion after the old one has finished its own
// locked steps, and a manual upgrade closes the old Companion (and any
// `companion` command from it) first. Exclusion is promised only among
// Companions that take the guard.
//
// # What it is not
//
// It is not a security boundary. Any process running as this user can delete
// the lock file, and can also read `config.json` directly, so there is nothing
// here for a lock to defend. It is a correctness mechanism between cooperating
// instances of one program, and its failure mode is a refusal, never a
// corrupted file.
package lockfile

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/fsshare"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/oslock"
)

// SchemaVersion versions the holder record inside the lock file.
const SchemaVersion = "aucom.lock/1.0"

// Suffix is appended to the guarded file's name to make its lock's name.
//
// Beside the file it guards rather than in a shared lock directory: the guarded
// file's own directory is the one a caller already has to be able to write, and
// a second directory would be a second thing to create, to make private, and to
// get wrong on a read-only volume.
const Suffix = ".lock"

// Defaults. Wait ten seconds for another instance to finish a read-modify-write
// that should take milliseconds; treat a lock nobody has refreshed for a minute
// as abandoned. The gap between the two is deliberate and large: a machine
// under load, or one that was asleep, must not lose its lock to a false
// positive, and the cost of waiting is that a person sees a message naming the
// other holder.
const (
	DefaultTimeout = 10 * time.Second
	DefaultStale   = time.Minute
	DefaultPoll    = 20 * time.Millisecond
)

// The guard: how long a release waits for it, how long a heartbeat does
// before skipping a beat, and how often a wait looks again. A guard is held
// for a few file operations, so a wait of seconds is another process stuck,
// not another process working.
const (
	guardWait     = 10 * time.Second
	heartbeatWait = time.Second
	guardPoll     = 2 * time.Millisecond
)

// GuardSuffix is appended to a lock file's name to make its guard's name.
const GuardSuffix = ".guard"

// Hooks are deterministic barriers for tests — in this package, and in the
// packages built on it whose own exclusion is this lock (the self-update's
// concurrent start). Nil in production.
var Hooks struct {
	// BeforeBreak runs after a lock was judged abandoned, before it is broken.
	BeforeBreak func(path string)
	// BeforeReleaseRemove runs after a release found its own record, before
	// it removes it.
	BeforeReleaseRemove func(path string)
	// BeforeRefresh runs when a heartbeat is due, before it touches the lock.
	BeforeRefresh func(path string)
	// GuardContended reports that taking the lock's guard has to wait.
	GuardContended func(path string)
}

// ProcessGone reports that no process with pid exists on this machine. A pid
// that exists, whoever it now is, is not gone.
func ProcessGone(pid int) bool { return pid > 0 && processGone(pid) }

// ErrBusy reports that another holder has the lock and did not release it
// within the timeout. It is a refusal: nothing was read and nothing was
// written.
var ErrBusy = errors.New("lockfile: another instance is writing this file")

// ErrLost reports that a lock was broken by somebody else while it was held —
// the release found a different holder's record where its own had been.
// Whatever the caller wrote may have raced with that holder.
var ErrLost = errors.New("lockfile: this lock was broken by another instance")

// Holder is who holds a lock. It is the whole content of the lock file.
type Holder struct {
	SchemaVersion string `json:"schema_version"`
	PID           int    `json:"pid"`
	Host          string `json:"host,omitempty"`
	// Program is a short label for what took the lock, so a message can say
	// "the GUI server" rather than a pid.
	Program string `json:"program,omitempty"`
	// Nonce distinguishes two holders with the same pid, which happens when a
	// pid is reused and when a test runs both in one process.
	Nonce string    `json:"nonce"`
	Since time.Time `json:"since"`
}

// Describe renders a holder for a person reading a refusal.
func (h Holder) Describe() string {
	who := "process " + fmt.Sprint(h.PID)
	if h.Program != "" {
		who = h.Program + " (process " + fmt.Sprint(h.PID) + ")"
	}
	if h.Host != "" {
		who += " on " + h.Host
	}
	if !h.Since.IsZero() {
		who += ", since " + h.Since.UTC().Format(time.RFC3339)
	}
	return who
}

// Options tune one acquisition. The zero value is the documented defaults.
type Options struct {
	// Timeout is how long to wait for a live holder. Zero means DefaultTimeout.
	Timeout time.Duration
	// Stale is how long a lock may go unrefreshed before it is broken. Zero
	// means DefaultStale. A negative value never breaks a lock.
	Stale time.Duration
	// Poll is how often to re-check a held lock. Zero means DefaultPoll.
	Poll time.Duration
	// Program labels this holder in the lock file.
	Program string
	// BreakDeadHolder breaks a lock at once when its holder is a process on
	// this machine that no longer exists, instead of waiting for it to go
	// stale. For a lock a crash is expected to leave behind — the
	// Companion's self-update, whose next start must recover without a
	// minute's wait (01_NEW_368). A pid that exists, whoever it now is,
	// still waits for staleness.
	BreakDeadHolder bool
	// Now supplies the clock, for tests.
	Now func() time.Time
}

func (o Options) timeout() time.Duration {
	if o.Timeout <= 0 {
		return DefaultTimeout
	}
	return o.Timeout
}

func (o Options) stale() time.Duration {
	if o.Stale == 0 {
		return DefaultStale
	}
	return o.Stale
}

func (o Options) poll() time.Duration {
	if o.Poll <= 0 {
		return DefaultPoll
	}
	return o.Poll
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}

// Lock is a held lock. Release it exactly once.
type Lock struct {
	path   string
	holder Holder
	broke  *Holder

	mu       sync.Mutex
	released bool
	stop     chan struct{}
	done     chan struct{}
}

// PathFor is the lock file guarding a given file.
func PathFor(guarded string) string {
	dir, name := filepath.Split(guarded)
	return filepath.Join(dir, "."+name+Suffix)
}

// Acquire takes the lock guarding path, waiting for a live holder and breaking
// an abandoned one.
//
// The lock file is created beside the guarded file, so the guarded file's
// directory must exist and be writable. That is not a limitation worth working
// around: a caller that cannot create a lock beside a file cannot write the
// file either, and reporting it here — before anything is read — is better than
// discovering it after a read-modify-write has already happened in memory.
func Acquire(guarded string, options Options) (*Lock, error) {
	path := PathFor(guarded)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("lockfile: creating %s: %w", filepath.Dir(path), err)
	}

	nonce, err := newNonce()
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	holder := Holder{
		SchemaVersion: SchemaVersion,
		PID:           os.Getpid(),
		Host:          host,
		Program:       options.Program,
		Nonce:         nonce,
		Since:         options.now(),
	}
	encoded, err := json.Marshal(holder)
	if err != nil {
		return nil, fmt.Errorf("lockfile: encoding the holder record: %w", err)
	}
	encoded = append(encoded, '\n')

	deadline := time.Now().Add(options.timeout())
	var broke *Holder
	for {
		unlock, err := guard(path, deadline)
		if errors.Is(err, errGuardBusy) {
			// Another Companion has been changing this lock for the whole
			// wait. Say who holds it, if a record says.
			if current, _, readErr := inspect(path, options.now()); readErr == nil && current.PID > 0 {
				return nil, fmt.Errorf("%w: %s holds %s. Wait for it to finish, or stop it", ErrBusy, current.Describe(), path)
			}
			return nil, fmt.Errorf("%w: %s is in use. Wait for it to finish, or stop it", ErrBusy, path)
		}
		if err != nil {
			return nil, err
		}
		taken, current, err := acquireGuarded(path, encoded, host, options)
		unlock()
		if err != nil {
			return nil, err
		}
		if taken.broke != nil {
			broke = taken.broke
		}
		if taken.held {
			lock := &Lock{path: path, holder: holder, broke: broke,
				stop: make(chan struct{}), done: make(chan struct{})}
			go lock.refresh(options.stale() / 4)
			return lock, nil
		}
		if time.Now().After(deadline) {
			if current == nil {
				return nil, fmt.Errorf("%w: %s is in use. Wait for it to finish, or stop it", ErrBusy, path)
			}
			return nil, fmt.Errorf("%w: %s holds %s. Wait for it to finish, or stop it",
				ErrBusy, current.Describe(), path)
		}
		time.Sleep(options.poll())
	}
}

type attempt struct {
	held  bool
	broke *Holder
}

// acquireGuarded makes one attempt at the lock, the guard held: publish this
// holder's record, or judge the one there and, when it is abandoned, remove
// it and publish. It reports who holds the lock when it is not this caller
// (nil when that cannot be read: still held).
func acquireGuarded(path string, encoded []byte, host string, options Options) (attempt, *Holder, error) {
	var result attempt
	// A record removed by a Companion that does not take the guard (one
	// released before it) between the create and the read is tried again —
	// a few times, and then left to the next poll.
	for try := 0; try < 3; try++ {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if _, err := file.Write(encoded); err != nil {
				file.Close()
				os.Remove(path)
				return result, nil, fmt.Errorf("lockfile: writing %s: %w", path, err)
			}
			if err := file.Close(); err != nil {
				os.Remove(path)
				return result, nil, fmt.Errorf("lockfile: closing %s: %w", path, err)
			}
			result.held = true
			return result, nil, nil
		}
		// On Windows a lock file somebody else is reading, or is deleting,
		// answers "in use" rather than "exists". Either way it is held.
		if fsshare.IsBusy(err) {
			return result, nil, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return result, nil, fmt.Errorf("lockfile: creating %s: %w", path, err)
		}
		current, age, readErr := inspect(path, options.now())
		switch {
		case readErr == nil:
		case errors.Is(readErr, fs.ErrNotExist):
			continue
		case fsshare.IsBusy(readErr):
			return result, nil, nil
		default:
			return result, nil, readErr
		}
		dead := options.BreakDeadHolder && current.PID > 0 && current.PID != os.Getpid() &&
			current.Host != "" && current.Host == host && processGone(current.PID)
		if limit := options.stale(); !dead && (limit <= 0 || age <= limit) {
			return result, &current, nil
		}
		if Hooks.BeforeBreak != nil {
			Hooks.BeforeBreak(path)
		}
		// Judged and removed with the guard held: no Companion that takes
		// the guard can have published a lock in between.
		if err := fsshare.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return result, nil, fmt.Errorf("lockfile: breaking the abandoned %s: %w", path, err)
		}
		result.broke = &current
	}
	return result, nil, nil
}

// guardPath is the file whose operating-system lock serializes every change
// of the lock at path. It is never removed.
func guardPath(path string) string { return path + GuardSuffix }

// errGuardBusy: the guard did not come free before the deadline.
var errGuardBusy = errors.New("lockfile: the lock's guard stayed taken")

// guard takes the guard of the lock at path, waiting until deadline, and
// returns its release.
func guard(path string, deadline time.Time) (func(), error) {
	file, err := os.OpenFile(guardPath(path), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lockfile: opening the guard of %s: %w", path, err)
	}
	for waited := false; ; waited = true {
		locked, err := oslock.TryLock(file)
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("lockfile: locking the guard of %s: %w", path, err)
		}
		if locked {
			return func() {
				_ = oslock.Unlock(file)
				file.Close()
			}, nil
		}
		if !waited && Hooks.GuardContended != nil {
			Hooks.GuardContended(path)
		}
		if !time.Now().Before(deadline) {
			file.Close()
			return nil, errGuardBusy
		}
		time.Sleep(guardPoll)
	}
}

// ownedBy reports whether the record at path is the holder's with nonce.
func ownedBy(path, nonce string) (bool, error) {
	raw, err := fsshare.ReadFile(path)
	if err != nil {
		return false, err
	}
	var current Holder
	return json.Unmarshal(raw, &current) == nil && current.Nonce == nonce, nil
}

// inspect reads the holder record and how long ago the lock was refreshed.
func inspect(path string, now time.Time) (Holder, time.Duration, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Holder{}, 0, err
	}
	age := now.Sub(info.ModTime().UTC())
	if age < 0 {
		// A clock that went backwards, or a file stamped in the future. Treat
		// it as fresh: breaking a live lock is the worse mistake.
		age = 0
	}
	var holder Holder
	raw, err := fsshare.ReadFile(path)
	if err != nil {
		return Holder{}, age, err
	}
	// A lock file that will not parse is still a lock. It is only ever broken
	// on age, and a caller is told what little is known.
	_ = json.Unmarshal(raw, &holder)
	return holder, age, nil
}

// refresh keeps the lock's modification time current while it is held.
func (l *Lock) refresh(every time.Duration) {
	defer close(l.done)
	if every <= 0 {
		every = DefaultStale / 4
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			if Hooks.BeforeRefresh != nil {
				Hooks.BeforeRefresh(l.path)
			}
			if !l.touch() {
				// Another holder's record is at the path: this lock was
				// broken. Nothing of theirs is refreshed; Release says so.
				return
			}
		}
	}
}

// touch refreshes the lock's modification time, with the guard held and only
// while the record there is this holder's. False when it is somebody else's,
// or gone. A guard that does not come free within a beat, or a touch that
// fails, skips the beat: the lock may then be judged abandoned, which is the
// safe direction.
func (l *Lock) touch() bool {
	unlock, err := guard(l.path, time.Now().Add(heartbeatWait))
	if err != nil {
		return true
	}
	defer unlock()
	mine, err := ownedBy(l.path, l.holder.Nonce)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !mine) {
		return false
	}
	if err == nil {
		now := time.Now()
		_ = os.Chtimes(l.path, now, now)
	}
	return true
}

// Holder is who this lock says holds it.
func (l *Lock) Holder() Holder { return l.holder }

// Path is the lock file.
func (l *Lock) Path() string { return l.path }

// BrokeStale reports the abandoned holder this acquisition displaced, when it
// displaced one. Callers surface it: a user whose previous run was killed
// should be told that is what happened, not have it happen silently.
func (l *Lock) BrokeStale() (Holder, bool) {
	if l == nil || l.broke == nil {
		return Holder{}, false
	}
	return *l.broke, true
}

// Release gives up the lock. It is safe to call twice; the second call is a
// no-op. It returns ErrLost when the lock file no longer holds this holder's
// record, which means somebody broke it while it was held.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	l.released = true
	close(l.stop)
	<-l.done

	// The check and the removal are one step under the guard: the record
	// removed is the one just found to be this holder's.
	unlock, err := guard(l.path, time.Now().Add(guardWait))
	if err != nil {
		return fmt.Errorf("lockfile: releasing %s: %w", l.path, err)
	}
	defer unlock()
	mine, err := ownedBy(l.path, l.holder.Nonce)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s is gone", ErrLost, l.path)
		}
		return fmt.Errorf("lockfile: reading %s: %w", l.path, err)
	}
	if !mine {
		return fmt.Errorf("%w: %s is now held by somebody else", ErrLost, l.path)
	}
	if Hooks.BeforeReleaseRemove != nil {
		Hooks.BeforeReleaseRemove(l.path)
	}
	if err := fsshare.Remove(l.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("lockfile: removing %s: %w", l.path, err)
	}
	return nil
}

// With runs fn holding the lock guarding path, and releases it afterwards
// whatever fn did.
//
// This is the shape every caller should use. A caller that acquires and
// releases by hand has two places to get an early return wrong, and the one it
// gets wrong leaves a lock held for a minute.
//
// fn's error is returned as it is. When fn succeeded but the release found
// that the lock had been broken meanwhile, that is returned instead
// ([ErrLost]): the work happened, and was not protected all the way through.
// Nothing here can undo what fn wrote; the caller is told rather than left to
// believe it was exclusive. A release that fails otherwise leaves a lock to
// age out, and does not turn finished work into a failure.
func With(guarded string, options Options, fn func() error) error {
	lock, err := Acquire(guarded, options)
	if err != nil {
		return err
	}
	err = fn()
	if released := lock.Release(); err == nil && errors.Is(released, ErrLost) {
		return fmt.Errorf("%w; the work itself finished, and what it wrote may have raced with that holder", released)
	}
	return err
}

func newNonce() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("lockfile: reading random bytes: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}
