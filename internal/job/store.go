package job

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/fsshare"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The store, and the two questions it exists to answer after a crash.
//
// A job is a process on somebody's machine. When the Companion goes away
// mid-build — a crash, a laptop lid, a kill -9 — two things are true at once:
// the record says `running`, and nothing is running. A supervisor that reads
// that record on the next start and believes it is a supervisor that will
// report a build as in progress forever.
//
// The wrong repair is to re-run whatever was unfinished. "Silently re-running
// commands" is how one interrupted publish becomes two, and the Companion does
// not know whether the tool got far enough to matter. So the record is changed
// to say the true thing — [Interrupted], which means *nobody knows how this
// ended* — and running it again is a new job the user asks for.
//
// Telling a crashed job from one another process is supervising right now is a
// heartbeat: the owner touches a file beside the record every few seconds, and
// a job whose heartbeat has stopped is one whose owner has. Pids are not used
// for this. A pid is reused, and "process 4212 exists" is not evidence that it
// is the Companion that started this job.

// heartbeatInterval is how often an owner refreshes its claim.
const heartbeatInterval = 2 * time.Second

// heartbeatStale is how old a claim has to be before recovery takes the job.
// Fifteen intervals: long enough that a machine under load, or one whose clock
// jumped, does not lose a running build to a false positive.
const heartbeatStale = 30 * time.Second

// recoverEveryTicks is how many heartbeat intervals pass between the recovery
// passes a running service makes: one stale period.
const recoverEveryTicks = int(heartbeatStale / heartbeatInterval)

const (
	recordName    = "job.json"
	heartbeatName = "heartbeat"
	cancelName    = "cancel"
	stdoutLogName = "stdout.log"
	stderrLogName = "stderr.log"
)

// ErrNotFound reports that no job has an id.
var ErrNotFound = errors.New("job: no such job")

// Store is the on-disk set of jobs. Every job is one directory: its record, its
// logs, its published artifacts and, while it runs, its workspace.
//
// One directory per job rather than one index file is what makes two processes
// — a server and a `companion job` invocation — able to use the same store
// without a lock protocol. They write different files.
type Store struct {
	root string
	mu   sync.Mutex
}

// OpenStore prepares a store rooted at dir, creating it if needed.
//
// It does not recover: that is [Store.Recover], called by whatever is about to
// take ownership of jobs. A read-only command must not interrupt the jobs a
// running server is supervising, and separating the two is what stops it.
func OpenStore(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("job: the job store needs a directory")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("job: resolving the job directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("job: creating %s: %w", absolute, err)
	}
	return &Store{root: absolute}, nil
}

// Root is the directory the store lives in.
func (s *Store) Root() string { return s.root }

// layout is one job's directory tree.
func (s *Store) layout(id string) layout { return layoutFor(s.root, id) }

// dirFor is the job's directory, refusing an id that is not one this package
// mints. The id arrives from an HTTP path segment and a command line, and it is
// about to be joined onto a filesystem path.
func (s *Store) dirFor(id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("%w: %q is not a job id", ErrNotFound, id)
	}
	return filepath.Join(s.root, id), nil
}

// Save writes a job record atomically.
func (s *Store) Save(j *Job) error {
	dir, err := s.dirFor(j.ID)
	if err != nil {
		return err
	}
	j.SchemaVersion = SchemaVersion
	encoded, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return fmt.Errorf("job: encoding %s: %w", j.ID, err)
	}
	encoded = append(encoded, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("job: creating %s: %w", dir, err)
	}
	return writeFileAtomic(filepath.Join(dir, recordName), encoded, 0o600)
}

// writeFileAtomic writes through a temporary file in the destination directory.
// A record truncated by a crash would be indistinguishable from a corrupt one,
// and the record is the only thing that says whether a job ran.
func writeFileAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".write-*")
	if err != nil {
		return fmt.Errorf("job: creating a temporary file in %s: %w", dir, err)
	}
	name := temp.Name()
	defer os.Remove(name) // No-op after a successful rename.

	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return fmt.Errorf("job: securing %s: %w", name, err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("job: writing %s: %w", name, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("job: closing %s: %w", name, err)
	}
	if err := fsshare.Replace(name, path); err != nil {
		return fmt.Errorf("job: replacing %s: %w", path, err)
	}
	return nil
}

// Load reads one job.
func (s *Store) Load(id string) (*Job, error) {
	dir, err := s.dirFor(id)
	if err != nil {
		return nil, err
	}
	return readRecord(filepath.Join(dir, recordName))
}

func readRecord(path string) (*Job, error) {
	raw, err := fsshare.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, filepath.Base(filepath.Dir(path)))
		}
		return nil, fmt.Errorf("job: reading %s: %w", path, err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	// Refused rather than half-read: a record with members this build does not
	// understand is one whose state it cannot honestly report.
	decoder.DisallowUnknownFields()
	var j Job
	if err := decoder.Decode(&j); err != nil {
		return nil, fmt.Errorf("job: reading %s: %w", path, err)
	}
	if !SchemaSupported(j.SchemaVersion) {
		return nil, fmt.Errorf("job: %s is %q; this build reads %s", path, j.SchemaVersion,
			strings.Join(SupportedSchemaVersions, " and "))
	}
	if !j.State.Valid() {
		return nil, fmt.Errorf("job: %s is in the state %q, which this build does not know", path, j.State)
	}
	return &j, nil
}

// List returns every stored job, newest first.
//
// Newest first because the id sorts by time and the question a user has is
// almost always about the last thing they ran.
func (s *Store) List() ([]*Job, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("job: listing %s: %w", s.root, err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && ValidID(entry.Name()) {
			ids = append(ids, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))

	jobs := make([]*Job, 0, len(ids))
	for _, id := range ids {
		j, err := readRecord(filepath.Join(s.root, id, recordName))
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				// A directory that is being created right now by another
				// process. Not an error; it will be there next time.
				continue
			}
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

// Heartbeat records that this process is still supervising a job.
func (s *Store) Heartbeat(id string, now time.Time) error {
	dir, err := s.dirFor(id)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, heartbeatName),
		[]byte(strconv.FormatInt(now.UTC().Unix(), 10)+"\n"), 0o600)
}

// heartbeatAge is how long ago a job's owner last said it was alive. A job with
// no heartbeat file returns (0, false).
func (s *Store) heartbeatAge(id string, now time.Time) (time.Duration, bool) {
	dir, err := s.dirFor(id)
	if err != nil {
		return 0, false
	}
	raw, err := fsshare.ReadFile(filepath.Join(dir, heartbeatName))
	if err != nil {
		return 0, false
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, false
	}
	age := now.UTC().Sub(time.Unix(seconds, 0).UTC())
	if age < 0 {
		// A clock that went backwards. Treating it as fresh is the safe way to
		// be wrong: the worst case is that recovery waits another cycle.
		age = 0
	}
	return age, true
}

// Recover marks every abandoned job interrupted and returns their ids.
//
// A job is abandoned when it is in a non-terminal state and its owner has
// stopped saying it is alive. Every non-terminal state counts, not only
// [Running]: a job that was still [Queued] never started, but re-running it
// without being asked is exactly the behaviour this refuses, and a user who
// wants it has [Service.Retry].
//
// supervised names jobs the caller is supervising itself, which are never taken
// whatever their heartbeat says: a laptop that slept longer than heartbeatStale
// wakes with its own claims stale, and its own running build is not abandoned.
func (s *Store) Recover(now time.Time, supervised ...string) ([]string, error) {
	jobs, err := s.List()
	if err != nil {
		return nil, err
	}
	mine := make(map[string]bool, len(supervised))
	for _, id := range supervised {
		mine[id] = true
	}
	var recovered []string
	for _, j := range jobs {
		if !j.State.Active() || mine[j.ID] {
			continue
		}
		if age, found := s.heartbeatAge(j.ID, now); found && age < heartbeatStale {
			// Somebody else is supervising this right now.
			continue
		}
		note := fmt.Sprintf("the Companion stopped while this job was %s; nothing here knows how it ended", j.State)
		// A Companion killed outright cannot take its job's process tree down,
		// and the tree goes on running with nobody supervising it (NEW_244D:
		// a kill -9 left the compiler and its child running after restart).
		// Stopped here only when the kernel still has the very process this
		// job started — same pid, same start time — so a reused pid is never
		// signalled. Nothing is run again.
		if j.Process.PID > 0 && j.Process.StartTicks != 0 && processStartTicks(j.Process.PID) == j.Process.StartTicks {
			if err := killAbandonedTree(j.Process.PID); err == nil {
				note += "; its programs were still running with nobody supervising them, and were stopped"
			}
		}
		j.History = append(j.History, Event{State: Interrupted, At: now.UTC(), Note: note})
		j.State = Interrupted
		j.Error = note
		if j.FinishedAt.IsZero() {
			j.FinishedAt = now.UTC()
		}
		j.Owner = Owner{}
		if err := s.Save(j); err != nil {
			return recovered, err
		}
		// The workspace of an interrupted job is left alone. It may hold a
		// half-written output somebody wants to look at, and removing it would
		// be this package deciding that on their behalf.
		recovered = append(recovered, j.ID)
	}
	return recovered, nil
}

// RequestCancel asks for a job to stop, from any process.
//
// A marker file rather than a signal: the process supervising the job may not
// be this one, and a `companion job cancel` typed in a terminal has to reach a
// job the GUI server started. The owner notices the marker within a second and
// takes the tree down; a job nobody owns is cancelled by [Service.Cancel]
// directly.
func (s *Store) RequestCancel(id string) error {
	dir, err := s.dirFor(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, recordName)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return err
	}
	return writeFileAtomic(filepath.Join(dir, cancelName), []byte("cancel\n"), 0o600)
}

// CancelRequested reports whether a stop was asked for.
func (s *Store) CancelRequested(id string) bool {
	dir, err := s.dirFor(id)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, cancelName))
	return err == nil
}

// ClearCancel removes the marker, so a retry does not inherit it.
func (s *Store) ClearCancel(id string) error {
	dir, err := s.dirFor(id)
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, cancelName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// WriteLog stores one stream's raw bytes.
func (s *Store) WriteLog(id, name string, data []byte) error {
	dir, err := s.dirFor(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, name), data, 0o600)
}

// ReadLog returns one stream's raw bytes. A job that produced nothing on a
// stream has no file, which is not an error.
func (s *Store) ReadLog(id, name string) ([]byte, error) {
	dir, err := s.dirFor(id)
	if err != nil {
		return nil, err
	}
	if name != stdoutLogName && name != stderrLogName {
		return nil, fmt.Errorf("job: %q is not a log this store keeps", name)
	}
	raw, err := fsshare.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("job: reading the %s log of %s: %w", name, id, err)
	}
	return raw, nil
}

// ArtifactPath resolves one published artifact by job id and output name,
// checking that the result is inside the job's own artifact directory.
//
// Both halves arrive from a URL. The containment check is what makes a request
// for `../../../etc/passwd` a 404 instead of a file.
func (s *Store) ArtifactPath(id, name string) (string, error) {
	j, err := s.Load(id)
	if err != nil {
		return "", err
	}
	for _, artifact := range j.Artifacts {
		if artifact.Name != name || artifact.Missing || artifact.Path == "" {
			continue
		}
		if err := within(s.layout(id).Artifacts, artifact.Path); err != nil {
			return "", err
		}
		if _, err := os.Stat(artifact.Path); err != nil {
			return "", fmt.Errorf("job: the artifact %q of %s is recorded but not on disk: %w", name, id, err)
		}
		return artifact.Path, nil
	}
	return "", fmt.Errorf("%w: %s has no artifact named %q", ErrNotFound, id, name)
}

// Remove deletes a job and everything it produced.
//
// Only ever the job's own directory, which contains nothing the user did not
// get a copy of: inputs were copied in, outputs were copied out to wherever the
// user asked for them. Nothing here can reach a source file.
func (s *Store) Remove(id string) error {
	dir, err := s.dirFor(id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("job: removing %s: %w", id, err)
	}
	return nil
}
