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
// # Why a lock FILE and not the operating system's lock
//
// `syscall.Flock` is not on Windows and `LockFileEx` is not in the standard
// library's `syscall` for the platforms that do have it, so a real advisory
// lock across all six supported targets means either cgo or a dependency. This
// program has neither — `go.mod` declares no requirements at all — and adding
// one to take a lock on a file in the user's own config directory would be a
// poor trade.
//
// `O_CREATE|O_EXCL` is the one primitive every target has with the same
// meaning: exactly one caller creates the file. What that costs is that the
// lock is not released by the kernel when a holder dies, so this package has to
// decide when a lock is abandoned, and that decision is made explicitly below
// rather than guessed at each call site.
//
// # The four decisions
//
//  1. **The holder is recorded, in the lock file.** A person who is told
//     "somebody else is writing this" can be told who, since when, and on which
//     machine. A lock that only says "busy" is one people delete.
//  2. **A stale lock is refreshed, not re-written.** The holder bumps the lock
//     file's modification time with [os.Chtimes] every [Options.Stale] quarter.
//     Nothing rewrites the bytes, so a reader never sees half a record.
//  3. **Breaking a stale lock goes through a rename.** Two processes that both
//     judge a lock abandoned must not both then create it. Renaming it away
//     first means exactly one of them succeeds and proceeds; the other gets
//     ENOENT and simply retries, which is the correct thing for it to do.
//  4. **Release checks the nonce it wrote.** A process whose own lock was
//     broken while it was descheduled must not delete the lock the next holder
//     is now holding. It reports that it lost the lock instead.
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
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if _, err := file.Write(encoded); err != nil {
				file.Close()
				os.Remove(path)
				return nil, fmt.Errorf("lockfile: writing %s: %w", path, err)
			}
			if err := file.Close(); err != nil {
				os.Remove(path)
				return nil, fmt.Errorf("lockfile: closing %s: %w", path, err)
			}
			lock := &Lock{path: path, holder: holder, broke: broke,
				stop: make(chan struct{}), done: make(chan struct{})}
			go lock.refresh(options.stale() / 4)
			return lock, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("lockfile: creating %s: %w", path, err)
		}

		current, age, readErr := inspect(path, options.now())
		if readErr != nil {
			if errors.Is(readErr, fs.ErrNotExist) {
				continue // The holder released it between the create and the read.
			}
			return nil, readErr
		}
		if limit := options.stale(); limit > 0 && age > limit {
			// Rename first, so that of two processes which both judged this
			// lock abandoned exactly one goes on to create it.
			if broken := breakStale(path); broken {
				broke = &current
			}
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: %s holds %s. Wait for it to finish, or stop it",
				ErrBusy, current.Describe(), path)
		}
		time.Sleep(options.poll())
	}
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
	raw, err := os.ReadFile(path)
	if err != nil {
		return Holder{}, age, err
	}
	// A lock file that will not parse is still a lock. It is only ever broken
	// on age, and a caller is told what little is known.
	_ = json.Unmarshal(raw, &holder)
	return holder, age, nil
}

// breakStale moves an abandoned lock out of the way and deletes it. It reports
// whether this caller is the one that did it.
func breakStale(path string) bool {
	nonce, err := newNonce()
	if err != nil {
		return false
	}
	aside := path + ".stale-" + nonce
	if err := os.Rename(path, aside); err != nil {
		return false // Somebody else got there first, or the holder released it.
	}
	os.Remove(aside)
	return true
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
			now := time.Now()
			// Best effort. A failure here means the lock will eventually be
			// judged abandoned, which is the safe direction: Release still
			// checks the nonce before deleting anything.
			_ = os.Chtimes(l.path, now, now)
		}
	}
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

	raw, err := os.ReadFile(l.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s is gone", ErrLost, l.path)
		}
		return fmt.Errorf("lockfile: reading %s: %w", l.path, err)
	}
	var current Holder
	if err := json.Unmarshal(raw, &current); err != nil || current.Nonce != l.holder.Nonce {
		return fmt.Errorf("%w: %s is now held by somebody else", ErrLost, l.path)
	}
	if err := os.Remove(l.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
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
func With(guarded string, options Options, fn func() error) error {
	lock, err := Acquire(guarded, options)
	if err != nil {
		return err
	}
	defer lock.Release()
	return fn()
}

func newNonce() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("lockfile: reading random bytes: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}
