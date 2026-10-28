// Package selfupdate puts a verified Companion bundle where the running one
// is, and takes it back out again when it does not start (NEW_368).
//
// It is the one fixed-function writer of the Companion's own folder. It does
// nothing a caller can steer with a path, a command or a URL: the folder it
// writes is the running bundle's own (internal/managedtools.InspectSelf
// resolves it from the executable, never from a file), the files it writes
// are the members a verified release bundle lists, each re-hashed against the
// digests the release verification recorded, and what it runs afterwards is
// the Companion it just put in place, with a fixed argument list.
//
// # The transaction
//
//	<root>/.companion-update/lock           one replacement at a time
//	<root>/.companion-update/journal.json   what was done, step by step
//	<root>/.companion-update/incoming/      the new files, copied and checked
//	<root>/.companion-update/previous/      the replaced files, kept for rollback
//	<root>/.companion-update/failed/        a rolled-back candidate, until next time
//
// Only files the old or the new bundle manifest lists are moved. A
// config.json beside the program, a user's notes, anything else in the folder
// is never touched, and the folder is never deleted or emptied.
//
// Every file is moved by rename within the folder's own filesystem. On Linux
// and macOS the old file is first hard-linked into previous/ and the new one
// renamed over it, so the program's path never stops existing. On Windows a
// running executable cannot be replaced but can be renamed, so the old file is
// renamed into previous/ first: the process that is ending keeps running from
// its renamed image, and no helper process has to wait for it.
package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/acquire"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/lockfile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
)

// DirName is the transaction folder inside the bundle's root.
const DirName = ".companion-update"

// JournalSchema versions journal.json.
const JournalSchema = "aucom.self-update-journal/1"

// State is where a replacement is.
type State string

const (
	// StateActivating: files are being moved. Found at a start, it is an
	// interrupted replacement, and it is rolled back.
	StateActivating State = "activating"
	// StateActivated: the new files are in place and their proving start
	// passed; the new Companion has not yet started and said it is ready.
	StateActivated State = "activated"
	// StateVerified: the new Companion started from the folder and served.
	StateVerified State = "verified"
	// StateRolledBack: the previous files are back.
	StateRolledBack State = "rolled_back"
)

// Step is one file of the replacement.
type Step struct {
	Path string `json:"path"`
	// New is false for a file only the old bundle had.
	New bool `json:"new"`
	// Aside: the old file is in previous/. Placed: the new one is in place.
	Aside  bool `json:"aside,omitempty"`
	Placed bool `json:"placed,omitempty"`
}

// Journal is the durable record of one replacement.
type Journal struct {
	Schema     string    `json:"schema"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	FromSHA256 string    `json:"from_sha256"`
	ToSHA256   string    `json:"to_sha256"`
	Companion  string    `json:"companion"`
	State      State     `json:"state"`
	Steps      []Step    `json:"steps"`
	Starts     int       `json:"starts,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	Started    time.Time `json:"started"`
	Updated    time.Time `json:"updated"`
}

// Request is one replacement. Every field comes from the running process's
// own inspection and from a verified installation record, never from a file
// a browser or a release note could have written.
type Request struct {
	// Root is the running bundle's folder; Companion the program's path in
	// it, relative; Current the running bundle's members (its manifest's).
	Root      string
	Companion string
	From      string
	Current   []managedtools.BundleMember
	// To, Staged and Members are the verified bundle in the tools folder,
	// which StagedRoot is: Staged must be inside it.
	To         string
	Staged     string
	StagedRoot string
	Members    []managedtools.BundleMember
	// Prove runs the program at exe, now in Root, through its bounded
	// proving start.
	Prove func(ctx context.Context, exe string) error
	// FreeSpace overrides the disk-space probe; -1 is unknown.
	FreeSpace func(string) int64
	Logf      func(string, ...any)
	// Fault, in tests, fails the transaction at a named point.
	Fault func(point string) error
}

// Errors a caller tells apart.
var (
	// ErrRefused: nothing was moved; the old bundle is untouched.
	ErrRefused = errors.New("selfupdate: the replacement was refused")
	// ErrRolledBack: files were moved and are back as they were.
	ErrRolledBack = errors.New("selfupdate: the new bundle was taken back out")
)

// margin is left free beyond the new files.
const margin = 32 << 20

func refuse(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, args...))
}

func (r Request) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

func (r Request) fault(point string) error {
	if r.Fault != nil {
		return r.Fault(point)
	}
	return nil
}

func dirOf(root string) string        { return filepath.Join(root, DirName) }
func journalPath(root string) string  { return filepath.Join(dirOf(root), "journal.json") }
func area(root, name string) string   { return filepath.Join(dirOf(root), name) }
func under(base, rel string) string   { return filepath.Join(base, filepath.FromSlash(rel)) }
func lockPath(root string) string     { return filepath.Join(dirOf(root), "lock") }
func now() time.Time                  { return time.Now().UTC() }
func errorsIsNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }
func samePath(a, b string) bool       { return filepath.Clean(a) == filepath.Clean(b) }

// Apply replaces the running bundle's files with the staged bundle's, proves
// the result, and rolls back if it does not prove. It returns the journal.
func Apply(ctx context.Context, request Request) (*Journal, error) {
	if err := request.validateShape(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dirOf(request.Root), 0o700); err != nil {
		return nil, refuse("the Companion's folder %s cannot hold its update folder: %v", request.Root, err)
	}
	lock, err := lockfile.Acquire(lockPath(request.Root), lockfile.Options{Timeout: 5 * time.Second,
		Program: "auto-pigeon-companion replacing itself"})
	if err != nil {
		return nil, refuse("another replacement holds %s: %v", lockPath(request.Root), err)
	}
	defer lock.Release()
	if journal, _ := readJournal(request.Root); journal != nil && journal.State == StateActivating {
		// An earlier replacement was interrupted: finish taking it back first.
		request.logf("self update: an interrupted replacement %s -> %s is rolled back first", journal.From, journal.To)
		_ = rollback(request.Root, journal, "interrupted before it finished; the previous files were put back")
	}
	if err := request.validateFiles(); err != nil {
		return nil, err
	}
	journal := &Journal{Schema: JournalSchema, From: request.From, To: request.To, Companion: request.Companion,
		State: StateActivating, Started: now()}
	for _, member := range request.Current {
		if member.Path == request.Companion {
			journal.FromSHA256 = member.SHA256
		}
	}
	listed := map[string]bool{}
	for _, member := range request.Members {
		listed[member.Path] = true
		journal.Steps = append(journal.Steps, Step{Path: member.Path, New: true})
		if member.Path == request.Companion {
			journal.ToSHA256 = member.SHA256
		}
	}
	for _, member := range request.Current {
		if !listed[member.Path] {
			if _, err := os.Lstat(under(request.Root, member.Path)); err == nil {
				journal.Steps = append(journal.Steps, Step{Path: member.Path})
			}
		}
	}
	// The Companion itself moves last: until then, the folder still starts
	// the old one.
	sort.SliceStable(journal.Steps, func(i, j int) bool {
		return journal.Steps[i].Path != request.Companion && journal.Steps[j].Path == request.Companion
	})
	if err := request.prepare(); err != nil {
		return nil, err
	}
	if err := request.fault("prepared"); err != nil {
		return nil, refuse("%v", err)
	}
	if err := writeJournal(request.Root, journal); err != nil {
		return nil, refuse("the journal cannot be written: %v", err)
	}
	incoming, previous := area(request.Root, "incoming"), area(request.Root, "previous")
	for i := range journal.Steps {
		step := &journal.Steps[i]
		dest := under(request.Root, step.Path)
		err := func() error {
			if _, err := os.Lstat(dest); err == nil {
				aside := under(previous, step.Path)
				if err := os.MkdirAll(filepath.Dir(aside), 0o700); err != nil {
					return err
				}
				if err := moveAside(dest, aside, step.New); err != nil {
					return fmt.Errorf("moving %s aside: %w", step.Path, err)
				}
				step.Aside = true
				if err := writeJournal(request.Root, journal); err != nil {
					return err
				}
			}
			if err := request.fault("aside:" + step.Path); err != nil {
				return err
			}
			if !step.New {
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			if err := os.Rename(under(incoming, step.Path), dest); err != nil {
				return fmt.Errorf("putting %s in place: %w", step.Path, err)
			}
			step.Placed = true
			if err := writeJournal(request.Root, journal); err != nil {
				return err
			}
			return request.fault("placed:" + step.Path)
		}()
		if err != nil {
			_ = rollback(request.Root, journal, "replacing the files failed: "+err.Error())
			return journal, fmt.Errorf("%w: %v", ErrRolledBack, err)
		}
	}
	if request.Prove != nil {
		if err := request.Prove(ctx, under(request.Root, request.Companion)); err != nil {
			_ = rollback(request.Root, journal, "the new Companion did not pass its proving start: "+err.Error())
			return journal, fmt.Errorf("%w: the new Companion did not pass its proving start: %v", ErrRolledBack, err)
		}
	}
	journal.State, journal.Detail = StateActivated, ""
	if err := writeJournal(request.Root, journal); err != nil {
		return journal, err
	}
	_ = os.RemoveAll(incoming)
	request.logf("self update: %s -> %s in place in %s; waiting for the new Companion to start", request.From, request.To, request.Root)
	return journal, nil
}

// validateShape checks what needs no file system.
func (r Request) validateShape() error {
	switch {
	case !filepath.IsAbs(r.Root) || !filepath.IsAbs(r.Staged) || !filepath.IsAbs(r.StagedRoot):
		return refuse("the bundle, the staged copy and the tools folder must be absolute paths")
	case !within(r.StagedRoot, r.Staged) || samePath(r.StagedRoot, r.Staged):
		return refuse("the staged copy %s is not in the Companion's tools folder", r.Staged)
	case within(r.Root, r.Staged) || within(r.Staged, r.Root):
		return refuse("the staged copy and the bundle overlap")
	case len(r.Members) == 0 || r.Companion == "":
		return refuse("the staged bundle lists no files")
	}
	found := false
	for _, member := range append(append([]managedtools.BundleMember(nil), r.Members...), r.Current...) {
		if err := acquire.CheckRelativePath(member.Path); err != nil || strings.HasPrefix(member.Path, DirName) {
			return refuse("a bundle names %q", member.Path)
		}
		if member.Path == r.Companion {
			found = true
		}
	}
	if !found {
		return refuse("the staged bundle does not contain %s", r.Companion)
	}
	return nil
}

// validateFiles checks every file before anything moves: the staged copies
// are still the verified bytes, and nothing in the bundle folder that would
// be replaced is a link, a folder or somebody else's.
func (r Request) validateFiles() error {
	if info, err := os.Lstat(r.Staged); err != nil || !info.IsDir() {
		return refuse("the staged copy %s is not a folder", r.Staged)
	}
	var total int64
	for _, member := range r.Members {
		path := under(r.Staged, member.Path)
		if err := plainAncestors(r.Staged, path); err != nil {
			return refuse("%v", err)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return refuse("the staged %s is missing or not a plain file", member.Path)
		}
		if info.Size() != member.Size {
			return refuse("the staged %s changed since it was verified (%d bytes, %d expected)", member.Path, info.Size(), member.Size)
		}
		digest, err := managedtools.FileSHA256(path)
		if err != nil || digest != member.SHA256 {
			return refuse("the staged %s changed since it was verified", member.Path)
		}
		total += member.Size
	}
	for _, member := range append(append([]managedtools.BundleMember(nil), r.Members...), r.Current...) {
		dest := under(r.Root, member.Path)
		if err := plainAncestors(r.Root, dest); err != nil {
			return refuse("%v", err)
		}
		info, err := os.Lstat(dest)
		if errorsIsNotExist(err) {
			continue
		}
		if err != nil {
			return refuse("%s cannot be examined: %v", dest, err)
		}
		if !info.Mode().IsRegular() {
			return refuse("%s is not a plain file (a link, a folder or a reparse point); nothing is replaced", dest)
		}
		if !ownedByCurrentUser(info) {
			return refuse("%s belongs to another user; nothing is replaced", dest)
		}
	}
	free := managedtools.FreeBytes(r.Root)
	if r.FreeSpace != nil {
		free = r.FreeSpace(r.Root)
	}
	if free >= 0 && free < total+margin {
		return refuse("the Companion's folder needs about %d MB free and has %d MB", (total+margin)>>20, free>>20)
	}
	return nil
}

// plainAncestors refuses a path whose folders between base and it are links.
func plainAncestors(base, path string) error {
	relative, err := filepath.Rel(base, filepath.Dir(path))
	if err != nil || relative == "." {
		return err
	}
	current := base
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errorsIsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a plain folder (a link or a reparse point)", current)
		}
	}
	return nil
}

// prepare copies the staged files into incoming/, on the bundle's own
// filesystem, checking each again as it is written; and clears what an
// earlier replacement left.
func (r Request) prepare() error {
	for _, name := range []string{"incoming", "failed"} {
		if err := os.RemoveAll(area(r.Root, name)); err != nil {
			return refuse("an earlier update's %s files cannot be removed: %v", name, err)
		}
	}
	// One previous bundle is kept, the one this replacement sets aside: the
	// one before it goes now. On Windows a program still running from it
	// keeps it from going, and then nothing is replaced.
	if err := os.RemoveAll(area(r.Root, "previous")); err != nil {
		return refuse("the bundle kept from the last update is still in use (%v); close every Companion and try again", err)
	}
	incoming := area(r.Root, "incoming")
	for _, member := range r.Members {
		target := under(incoming, member.Path)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return refuse("%v", err)
		}
		mode := os.FileMode(0o644)
		if member.Executable {
			mode = 0o755
		}
		if err := copyChecked(under(r.Staged, member.Path), target, mode, member.SHA256); err != nil {
			_ = os.RemoveAll(incoming)
			return refuse("copying %s into the Companion's folder: %v", member.Path, err)
		}
	}
	return nil
}

func copyChecked(from, to string, mode os.FileMode, digest string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(to, mode); err != nil {
		return err
	}
	actual, err := managedtools.FileSHA256(to)
	if err != nil {
		return err
	}
	if actual != digest {
		return fmt.Errorf("the copy is not the verified file")
	}
	return nil
}

// moveAside puts the file at dest into aside. Where the file is about to be
// replaced and links are possible, a hard link keeps dest in existence until
// the new file is renamed over it.
func moveAside(dest, aside string, replaced bool) error {
	if replaced && runtime.GOOS != "windows" {
		if err := os.Link(dest, aside); err == nil {
			return nil
		}
	}
	return os.Rename(dest, aside)
}

// rollback undoes a journal's steps, last first, and records why.
func rollback(root string, journal *Journal, detail string) error {
	previous, failed := area(root, "previous"), area(root, "failed")
	var problems []string
	for i := len(journal.Steps) - 1; i >= 0; i-- {
		step := &journal.Steps[i]
		dest := under(root, step.Path)
		if step.Placed {
			parked := under(failed, step.Path)
			_ = os.MkdirAll(filepath.Dir(parked), 0o700)
			if step.Aside && runtime.GOOS != "windows" {
				// The old file is renamed back over the new one below; on
				// Windows a running new program must be renamed out first.
			} else if err := os.Rename(dest, parked); err != nil && !errorsIsNotExist(err) {
				problems = append(problems, fmt.Sprintf("%s: %v", step.Path, err))
				continue
			}
			step.Placed = false
		}
		if step.Aside {
			if err := os.Rename(under(previous, step.Path), dest); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", step.Path, err))
				continue
			}
			step.Aside = false
		}
	}
	journal.State, journal.Detail = StateRolledBack, detail
	if len(problems) > 0 {
		journal.Detail += "; NOT everything could be put back (" + strings.Join(problems, "; ") +
			"): the replaced files are in " + previous
	}
	_ = writeJournal(root, journal)
	if len(problems) > 0 {
		return errors.New(journal.Detail)
	}
	return nil
}

// Rollback takes an activated replacement back out: the new Companion did
// not start and say it was ready. The caller has made sure it is not running.
func Rollback(root, detail string) (*Journal, error) {
	lock, err := lockfile.Acquire(lockPath(root), lockfile.Options{Timeout: 5 * time.Second,
		Program: "auto-pigeon-companion undoing its update"})
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	journal, err := readJournal(root)
	if err != nil || journal == nil {
		return journal, err
	}
	if journal.State != StateActivated && journal.State != StateActivating {
		return journal, nil
	}
	return journal, rollback(root, journal, detail)
}

// Outcome is what [Recover] found at a start.
type Outcome string

const (
	// Nothing: no replacement is open.
	Nothing Outcome = ""
	// Pending: this process is the new Companion; [MarkVerified] once it serves.
	Pending Outcome = "pending"
	// RolledBack: an interrupted or never-ready replacement was undone.
	RolledBack Outcome = "rolled_back"
)

// maxUnreadyStarts is how many starts of a replaced Companion may go by
// without one of them becoming ready before the replacement is undone.
const maxUnreadyStarts = 3

// Recover looks at the journal as a Companion starts from root. An
// interrupted replacement is rolled back. An activated one is this process's
// to confirm — unless this is the third start that has not, in which case it
// is undone, so a candidate that cannot serve does not stay in place.
func Recover(root, runningSHA string, logf func(string, ...any)) (*Journal, Outcome, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if _, err := os.Stat(journalPath(root)); errorsIsNotExist(err) {
		return nil, Nothing, nil
	}
	lock, err := lockfile.Acquire(lockPath(root), lockfile.Options{Timeout: 5 * time.Second,
		Program: "auto-pigeon-companion checking its last update"})
	if err != nil {
		return nil, Nothing, err
	}
	defer lock.Release()
	journal, err := readJournal(root)
	if err != nil || journal == nil {
		return journal, Nothing, err
	}
	switch journal.State {
	case StateActivating:
		logf("self update: the replacement %s -> %s was interrupted; putting the previous files back", journal.From, journal.To)
		err := rollback(root, journal, "interrupted before it finished; the previous files were put back")
		return journal, RolledBack, err
	case StateActivated:
		if runningSHA != journal.ToSHA256 {
			return journal, Nothing, nil
		}
		journal.Starts++
		if journal.Starts >= maxUnreadyStarts {
			logf("self update: %s has started %d times without becoming ready; putting %s back", journal.To, journal.Starts, journal.From)
			err := rollback(root, journal, fmt.Sprintf("version %s started %d times without becoming ready", journal.To, journal.Starts))
			return journal, RolledBack, err
		}
		journal.Updated = now()
		return journal, Pending, writeJournal(root, journal)
	default:
		_ = os.RemoveAll(area(root, "incoming"))
		_ = os.RemoveAll(area(root, "failed"))
		return journal, Nothing, nil
	}
}

// MarkVerified records that the new Companion started and serves.
func MarkVerified(root, runningSHA string) (*Journal, error) {
	lock, err := lockfile.Acquire(lockPath(root), lockfile.Options{Timeout: 5 * time.Second,
		Program: "auto-pigeon-companion confirming its update"})
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	journal, err := readJournal(root)
	if err != nil || journal == nil {
		return journal, err
	}
	if journal.State != StateActivated || journal.ToSHA256 != runningSHA {
		return journal, fmt.Errorf("selfupdate: no replacement to %s is waiting to be confirmed", runningSHA)
	}
	journal.State, journal.Detail = StateVerified, ""
	return journal, writeJournal(root, journal)
}

// Read is the last replacement's journal, if there is one.
func Read(root string) (*Journal, error) { return readJournal(root) }

// WaitVerified waits until the journal says verified, the child exits, or
// the time is up.
func WaitVerified(ctx context.Context, root string, exited <-chan struct{}) bool {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if journal, err := readJournal(root); err == nil && journal != nil && journal.State == StateVerified {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-exited:
			journal, err := readJournal(root)
			return err == nil && journal != nil && journal.State == StateVerified
		case <-ticker.C:
		}
	}
}

func readJournal(root string) (*Journal, error) {
	path := journalPath(root)
	info, err := os.Lstat(path)
	if errorsIsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !ownedByCurrentUser(info) {
		return nil, fmt.Errorf("selfupdate: %s is not this user's plain file; it is not read", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var journal Journal
	if err := json.Unmarshal(raw, &journal); err != nil {
		return nil, fmt.Errorf("selfupdate: %s is not readable: %w", path, err)
	}
	if journal.Schema != JournalSchema {
		return nil, fmt.Errorf("selfupdate: %s is %q", path, journal.Schema)
	}
	return &journal, nil
}

func writeJournal(root string, journal *Journal) error {
	journal.Schema = JournalSchema
	journal.Updated = now()
	encoded, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	dir := dirOf(root)
	temp, err := os.CreateTemp(dir, "journal-*.json")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err := temp.Write(append(encoded, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	return os.Rename(name, journalPath(root))
}

func within(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
