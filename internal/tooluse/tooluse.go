// Package tooluse is how separate Companion processes that share one tools
// folder agree on when a managed toolchain may switch version and when an
// installation may be removed (01_NEW_368 §2).
//
// The in-process gate (web's toolGate) and Host.Busy see only the work of
// their own process; a `companion build run` in a terminal is another
// process, and a Go mutex cannot see it. Everything here is a file in the
// tools folder itself — `<tools>/.in-use/` — so whichever configuration a
// process runs with, sharing the tools folder means sharing the protection,
// and two unrelated tools folders never block each other. The folder is named
// by its canonical path: two spellings of one folder are one folder.
//
// # The protocol
//
//   - A pipeline, before it resolves a single program, takes a USE lease
//     naming the toolchains its stages run (use-<nonce>.json). It then freezes
//     the programs and records the installation folders it pinned, and holds
//     the lease until every stage's process has ended.
//   - Switching a toolchain's version takes its SWITCH gate
//     (switch-<tool>.json), exclusive among switchers, and then waits until no
//     live lease names the toolchain. It keeps the gate while it waits.
//   - A lease is admitted only while no gate for its toolchains is held, and
//     the check is made twice: before the lease is written, and after. The
//     switcher writes its gate and then reads the leases. Of a lease and a
//     gate written at the same moment, at least one side sees the other — the
//     lease withdraws, or the switch waits — so no stage ever starts in an
//     unprotected interval.
//   - A gate that is held keeps NEW leases out (they wait a bounded time and
//     are then refused, retryably). That is the writer preference: once the
//     work that existed when the switch was asked for has ended, the switch
//     happens, however much new work is asked for meanwhile.
//
// # Liveness, and a surviving child
//
// A record names its writer by pid AND start time. On this machine that is
// checked against the kernel (internal/job.ProcessRunning), never a bare pid;
// where a platform cannot tell, a heartbeat — the record's modification time,
// refreshed every [heartbeat] — stands in. A lease whose writer has died is
// not "idle": the compiler it started may outlive it. Removal of an
// installation therefore also reads the job records the lease named and
// treats a job whose process is still verifiably that process — or one it
// cannot verify, for up to [abandonAfter] — as still using the folder.
// Switching versions does not wait for an orphan: it rewrites only bindings,
// and the orphan runs from absolute paths that stay on disk.
package tooluse

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
)

// DirName is the coordination folder inside a tools folder.
const DirName = ".in-use"

// Schema versions a record.
const Schema = "aucom.tool-use/1"

const (
	// heartbeat is how often a holder refreshes its record.
	heartbeat = 15 * time.Second
	// staleAfter: a record not refreshed for this long, whose writer this
	// machine cannot verify, is not live.
	staleAfter = time.Minute
	// abandonAfter bounds how long a dead lease with jobs this machine cannot
	// verify keeps an installation from being removed.
	abandonAfter = 48 * time.Hour
	// poll is how often a waiting lease looks again.
	poll = 100 * time.Millisecond
)

// Record is a lease or a gate, as written.
type Record struct {
	Schema string   `json:"schema"`
	Kind   string   `json:"kind"` // "use" or "switch"
	Tools  []string `json:"tools"`
	Dirs   []string `json:"dirs,omitempty"`
	// Stores are the job stores the pipeline submits its stages to.
	Stores     []string  `json:"stores,omitempty"`
	Program    string    `json:"program,omitempty"`
	Host       string    `json:"host"`
	PID        int       `json:"pid"`
	StartTicks uint64    `json:"start_ticks,omitempty"`
	Since      time.Time `json:"since"`
}

func (r Record) describe() string {
	who := r.Program
	if who == "" {
		who = "a Companion"
	}
	return fmt.Sprintf("%s (process %d, since %s)", who, r.PID, r.Since.Local().Format("15:04:05"))
}

// Registry is one tools folder's coordination.
type Registry struct {
	root string
	dir  string
	host string
	self job.ProcessIdentity
}

// Open is the registry of the tools folder root, creating its coordination
// folder.
func Open(root string) (*Registry, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	canonical, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}
	dir := filepath.Join(canonical, DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	pid := os.Getpid()
	return &Registry{root: canonical, dir: dir, host: host,
		self: job.ProcessIdentity{PID: pid, StartTicks: job.ProcessStartTicks(pid)}}, nil
}

// Root is the canonical tools folder.
func (r *Registry) Root() string { return r.root }

// ErrSwitching is a lease refused because a toolchain it needs is switching
// version. Nothing was started; the same request may be made again.
var ErrSwitching = errors.New("tooluse: a toolchain is switching version")

// SwitchingError says which toolchain, and who is switching it.
type SwitchingError struct {
	Tool   string
	Holder string
}

func (e *SwitchingError) Error() string {
	return fmt.Sprintf("the %s toolchain is switching to its new version and waits for the builds that use it (%s); nothing was started — try again once it has switched, or cancel the pending update",
		e.Tool, e.Holder)
}

func (e *SwitchingError) Is(target error) bool { return target == ErrSwitching }

// ErrSwitchBusy is a switch refused because another process is switching
// the same toolchain.
var ErrSwitchBusy = errors.New("tooluse: another Companion is switching this toolchain")

// Test seams: a deterministic barrier between writing one's own record and
// reading the other side's.
var (
	afterLeaseWritten func()
	afterGateWritten  func()
)

func (r *Registry) gatePath(tool string) string { return filepath.Join(r.dir, "switch-"+tool+".json") }

// --- leases -------------------------------------------------------------------

// Lease is a pipeline's hold on the toolchains it runs.
type Lease struct {
	r    *Registry
	path string

	mu       sync.Mutex
	record   Record
	released bool
	stop     chan struct{}
}

// Use takes a lease on tools, waiting at most wait while one of them is
// switching, and refusing with a [SwitchingError] after that.
func (r *Registry) Use(tools []string, program string, wait time.Duration) (*Lease, error) {
	tools = normalTools(tools)
	deadline := time.Now().Add(wait)
	for {
		if gate, held := r.switching(tools); held {
			if !time.Now().Before(deadline) {
				return nil, &SwitchingError{Tool: strings.Join(gate.Tools, ", "), Holder: gate.describe()}
			}
			time.Sleep(poll)
			continue
		}
		nonce, err := newNonce()
		if err != nil {
			return nil, err
		}
		lease := &Lease{r: r, path: filepath.Join(r.dir, "use-"+nonce+".json"), stop: make(chan struct{}),
			record: Record{Schema: Schema, Kind: "use", Tools: tools, Program: program, Host: r.host,
				PID: r.self.PID, StartTicks: r.self.StartTicks, Since: time.Now().UTC()}}
		if err := lease.write(); err != nil {
			return nil, fmt.Errorf("tooluse: recording that %s uses %s: %w", program, strings.Join(tools, ", "), err)
		}
		if afterLeaseWritten != nil {
			afterLeaseWritten()
		}
		if gate, held := r.switching(tools); held {
			lease.Release()
			if !time.Now().Before(deadline) {
				return nil, &SwitchingError{Tool: strings.Join(gate.Tools, ", "), Holder: gate.describe()}
			}
			time.Sleep(poll)
			continue
		}
		go lease.refresh()
		return lease, nil
	}
}

// Pin records the installation folders the lease's pipeline froze.
func (l *Lease) Pin(dirs []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.record.Dirs = normalTools(append(l.record.Dirs, dirs...))
	return l.writeLocked()
}

// AddStore records the job store the pipeline submits to, before any stage
// is submitted: if this process dies, the records of the jobs it launched
// there say whether their programs still run.
func (l *Lease) AddStore(store string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.record.Stores = normalTools(append(l.record.Stores, store))
	return l.writeLocked()
}

// Release ends the lease. Call it once every process the pipeline started
// has ended. Repeating it does nothing.
func (l *Lease) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return
	}
	l.released = true
	close(l.stop)
	_ = os.Remove(l.path)
}

func (l *Lease) write() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.writeLocked()
}

func (l *Lease) writeLocked() error {
	if l.released {
		return nil
	}
	return writeRecord(l.path, l.record, false)
}

func (l *Lease) refresh() {
	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			now := time.Now()
			_ = os.Chtimes(l.path, now, now)
		}
	}
}

// --- switch gates --------------------------------------------------------------

// Gate is a held switch gate for one toolchain.
type Gate struct {
	r    *Registry
	tool string
	path string

	once sync.Once
	stop chan struct{}
}

// Switch takes the switch gate of tool. Another live switcher of the same
// toolchain is [ErrSwitchBusy]; a dead one's gate is broken.
func (r *Registry) Switch(tool, program string) (*Gate, error) {
	path := r.gatePath(tool)
	record := Record{Schema: Schema, Kind: "switch", Tools: []string{tool}, Program: program, Host: r.host,
		PID: r.self.PID, StartTicks: r.self.StartTicks, Since: time.Now().UTC()}
	for attempt := 0; attempt < 2; attempt++ {
		err := writeRecord(path, record, true)
		if err == nil {
			if afterGateWritten != nil {
				afterGateWritten()
			}
			gate := &Gate{r: r, tool: tool, path: path, stop: make(chan struct{})}
			go gate.refresh()
			return gate, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		current, info, readErr := readRecord(path)
		if readErr == nil && r.live(current, info.ModTime()) {
			return nil, fmt.Errorf("%w: %s", ErrSwitchBusy, current.describe())
		}
		r.breakRecord(path)
	}
	return nil, fmt.Errorf("%w: its gate could not be taken", ErrSwitchBusy)
}

// Use is one live lease a switch waits for.
type Use struct {
	Label string
	// Here: the lease is this process's own.
	Here bool
}

// Users is the live leases on the gate's toolchain: the work a switch waits
// for. It is read after the gate is written, every time it is asked.
func (g *Gate) Users() []Use {
	var out []Use
	for _, lease := range g.r.leases() {
		if contains(lease.record.Tools, g.tool) && g.r.live(lease.record, lease.modTime) {
			here := lease.record.Host == g.r.host && lease.record.PID == g.r.self.PID
			out = append(out, Use{Label: "a build in " + lease.record.describe(), Here: here})
		}
	}
	return out
}

// Release gives the gate up. Repeating it does nothing.
func (g *Gate) Release() {
	g.once.Do(func() {
		close(g.stop)
		_ = os.Remove(g.path)
	})
}

func (g *Gate) refresh() {
	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-g.stop:
			return
		case <-ticker.C:
			now := time.Now()
			_ = os.Chtimes(g.path, now, now)
		}
	}
}

// --- removal ------------------------------------------------------------------

// References names what may still be using dir, an installation of tool:
// every live lease that pinned it or has not pinned anything yet, and every
// dead one whose jobs' programs still run or cannot be verified not to. A
// dead lease that is verifiably over is removed here.
func (r *Registry) References(tool, dir string) []string {
	var out []string
	for _, lease := range r.leases() {
		record := lease.record
		if !contains(record.Tools, tool) || (len(record.Dirs) > 0 && !containsPath(record.Dirs, dir)) {
			continue
		}
		if r.live(record, lease.modTime) {
			out = append(out, "a build in "+record.describe())
			continue
		}
		switch r.orphans(record) {
		case orphanRunning:
			out = append(out, fmt.Sprintf("a compiler started by %s, which has ended, is still running", record.describe()))
		case orphanUnknown:
			if time.Since(lease.modTime) < abandonAfter {
				out = append(out, fmt.Sprintf("a compiler started by %s, which has ended, may still be running", record.describe()))
				continue
			}
			_ = os.Remove(lease.path)
		default:
			_ = os.Remove(lease.path)
		}
	}
	return out
}

type orphanState int

const (
	orphanNone orphanState = iota
	orphanRunning
	orphanUnknown
)

// orphans reads, in the stores a dead lease named, the jobs its writer
// launched after it took the lease: a job id begins with the time it was
// minted, so only those are read.
func (r *Registry) orphans(record Record) orphanState {
	state := orphanNone
	from := record.Since.Add(-time.Second).UTC().Format("20060102T150405Z")
	for _, root := range record.Stores {
		entries, err := os.ReadDir(root)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				state = orphanUnknown
			}
			continue
		}
		store, err := job.OpenStore(root)
		if err != nil {
			state = orphanUnknown
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || !job.ValidID(entry.Name()) || entry.Name() < from {
				continue
			}
			j, err := store.Load(entry.Name())
			if err != nil || j.State.Terminal() || j.Process.PID == 0 || !launchedBy(j, record) {
				continue
			}
			running, known := job.ProcessRunning(j.Process)
			switch {
			case !known:
				state = orphanUnknown
			case running:
				return orphanRunning
			}
		}
	}
	return state
}

// launchedBy: the job's own launch record names the lease's writer.
func launchedBy(j *job.Job, record Record) bool {
	if j.Launch != nil && j.Launch.Supervisor.PID != 0 {
		supervisor := j.Launch.Supervisor
		return supervisor.PID == record.PID && (supervisor.StartTicks == 0 || record.StartTicks == 0 || supervisor.StartTicks == record.StartTicks)
	}
	return j.Owner.PID == record.PID
}

// --- records ------------------------------------------------------------------

type leaseFile struct {
	path    string
	record  Record
	modTime time.Time
}

func (r *Registry) leases() []leaseFile {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return nil
	}
	var out []leaseFile
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "use-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(r.dir, name)
		record, info, err := readRecord(path)
		if err != nil {
			continue
		}
		out = append(out, leaseFile{path: path, record: record, modTime: info.ModTime()})
	}
	return out
}

// Switching is nil, or the [SwitchingError] a lease on tools would get now.
func (r *Registry) Switching(tools []string) error {
	if gate, held := r.switching(normalTools(tools)); held {
		return &SwitchingError{Tool: strings.Join(gate.Tools, ", "), Holder: gate.describe()}
	}
	return nil
}

// switching reports a live gate on one of tools, breaking dead ones.
func (r *Registry) switching(tools []string) (Record, bool) {
	for _, tool := range tools {
		path := r.gatePath(tool)
		record, info, err := readRecord(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			// A gate being written, or unreadable: held, to be safe.
			return Record{Tools: []string{tool}, Program: "a Companion"}, true
		}
		if r.live(record, info.ModTime()) {
			return record, true
		}
		r.breakRecord(path)
	}
	return Record{}, false
}

// live reports whether a record's writer is still running: by its process
// identity on this machine, else by its heartbeat.
func (r *Registry) live(record Record, modTime time.Time) bool {
	if record.Host == r.host {
		if record.PID == r.self.PID && record.StartTicks == r.self.StartTicks {
			return true
		}
		if running, known := job.ProcessRunning(job.ProcessIdentity{PID: record.PID, StartTicks: record.StartTicks}); known {
			return running
		}
	}
	return time.Since(modTime) < staleAfter
}

// breakRecord moves a dead record away first, so of two processes that both
// judged it dead exactly one removes it.
func (r *Registry) breakRecord(path string) {
	nonce, err := newNonce()
	if err != nil {
		return
	}
	aside := path + ".dead-" + nonce
	if os.Rename(path, aside) == nil {
		_ = os.Remove(aside)
	}
}

func readRecord(path string) (Record, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Record{}, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return Record{}, nil, fmt.Errorf("tooluse: %s is not a record", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Record{}, nil, err
	}
	var record Record
	if err := json.Unmarshal(raw, &record); err != nil || record.Schema != Schema {
		return Record{}, nil, fmt.Errorf("tooluse: %s is not a record", path)
	}
	return record, info, nil
}

// writeRecord writes a record whole: a temporary file, then a rename — or,
// for a gate (exclusive), a hard link, which fails if the gate exists.
func writeRecord(path string, record Record, exclusive bool) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".record-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err := temp.Write(encoded); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if exclusive {
		if err := os.Link(name, path); err != nil {
			if _, statErr := os.Lstat(path); statErr == nil {
				return fs.ErrExist
			}
			// No hard links on this volume: O_EXCL, then the bytes.
			file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			if _, err := file.Write(encoded); err != nil {
				file.Close()
				os.Remove(path)
				return err
			}
			return file.Close()
		}
		return nil
	}
	return os.Rename(name, path)
}

func newNonce() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func normalTools(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsPath(values []string, want string) bool {
	for _, value := range values {
		if filepath.Clean(value) == filepath.Clean(want) {
			return true
		}
	}
	return false
}
