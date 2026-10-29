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
//	<root>/.companion-update/journal.json   what the replacement is, written first
//	<root>/.companion-update/incoming/      the new files, copied and checked
//	<root>/.companion-update/previous/      the replaced files, kept for rollback
//	<root>/.companion-update/failed/        a rolled-back candidate, kept until the next update
//
// Only files the old or the new bundle manifest lists are moved. A
// config.json beside the program, a user's notes, anything else in the folder
// is never touched, and the folder is never deleted or emptied.
//
// # Write-ahead, then reconcile by the bytes (01_NEW_368)
//
// The journal is written once, before the first file moves, and it names
// every owned path with the SHA-256 of the file that was there (Old) and of
// the file that is to be there (New); "" is "no file". Nothing during the
// swap is recorded as done: what has been done is read back from the files
// themselves. The forward and the reverse passes are each idempotent — every
// step first looks at what is at the destination, in previous/ and in
// incoming/, and does only what the bytes say is still to do — so a process
// killed between any rename and the next, or one whose journal write failed,
// leaves a folder the next pass finishes from wherever it stopped. A file
// whose bytes are neither the old nor the new one is never overwritten or
// moved: that is a "recovery needed" with the path named, and the folder
// stays as it is until a person or a later pass can resolve it.
//
// Every file is moved by rename within the folder's own filesystem. On Linux
// and macOS the old file is first hard-linked into previous/ and the new one
// renamed over it, so the program's path never stops existing. On Windows a
// running executable cannot be replaced but can be renamed, so the old file is
// renamed into previous/ first; a process killed between that rename and the
// next leaves no program at the path, and the copy in previous/ is what runs
// the recovery (`selfupdate recover`, [RecoveryRoot]).
//
// # Durability
//
// Each new file is fsynced before it is renamed, the journal is fsynced
// before its rename, and on POSIX the directories a rename changed are
// fsynced after it; on Windows renames ask for MOVEFILE_WRITE_THROUGH. What is
// PROVEN, by the tests that kill a child process at every boundary, is
// recovery after an abrupt process death. Power loss is argued from that
// ordering, not tested: an atomic rename is not by itself a power-loss proof,
// and no test here pulls a plug.
package selfupdate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// JournalSchema versions journal.json. legacySchema is the 1.323 journal,
// which this code still reads — the update from 1.323 is written by 1.323's
// own updater and confirmed by this one — and writes back in its own schema.
const (
	JournalSchema = "aucom.self-update-journal/2"
	legacySchema  = "aucom.self-update-journal/1"
)

// State is where a replacement is.
type State string

const (
	// StateActivating: the journal is written and files may be moving. Found
	// at a start, it is an interrupted replacement, and it is rolled back.
	StateActivating State = "activating"
	// StateActivated: the new files are in place and their proving start
	// passed; the new Companion has not yet started and said it is ready.
	StateActivated State = "activated"
	// StateVerified: the new Companion started from the folder and served.
	StateVerified State = "verified"
	// StateRollingBack: the previous files are being put back.
	StateRollingBack State = "rolling_back"
	// StateRolledBack: every owned file is the previous bundle's again,
	// checked by its bytes.
	StateRolledBack State = "rolled_back"
	// StateRecoveryNeeded: putting the previous files back did not finish.
	// Nothing more is replaced, and nothing kept is removed, until a pass
	// finishes it.
	StateRecoveryNeeded State = "recovery_needed"
)

func (s State) known() bool {
	switch s {
	case StateActivating, StateActivated, StateVerified, StateRollingBack, StateRolledBack, StateRecoveryNeeded:
		return true
	}
	return false
}

// terminal states leave nothing to finish.
func (s State) terminal() bool { return s == StateVerified || s == StateRolledBack }

// Member is one owned path of a replacement: the bytes that were there and
// the bytes that are to be there, "" for no file.
type Member struct {
	Path string `json:"path"`
	Old  string `json:"old_sha256,omitempty"`
	New  string `json:"new_sha256,omitempty"`
}

// Step is one file of a 1.323 (legacy schema) replacement. Its flags were
// written after each rename and are not trusted: a legacy journal is
// recovered from where the files are.
type Step struct {
	Path   string `json:"path"`
	New    bool   `json:"new"`
	Aside  bool   `json:"aside,omitempty"`
	Placed bool   `json:"placed,omitempty"`
}

// Journal is the durable record of one replacement.
type Journal struct {
	Schema string `json:"schema"`
	// ID names this replacement; the folders it discards carry it.
	ID         string    `json:"id,omitempty"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	FromSHA256 string    `json:"from_sha256"`
	ToSHA256   string    `json:"to_sha256"`
	Companion  string    `json:"companion"`
	State      State     `json:"state"`
	Members    []Member  `json:"members,omitempty"`
	Steps      []Step    `json:"steps,omitempty"`
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
	Prove func(ctx context.Context, exe string) error `json:"-"`
	// FreeSpace overrides the disk-space probe; -1 is unknown.
	FreeSpace func(string) int64   `json:"-"`
	Logf      func(string, ...any) `json:"-"`
	// Fault, in tests, fails the transaction at a named point.
	Fault func(point string) error `json:"-"`
}

// Errors a caller tells apart.
var (
	// ErrRefused: nothing was moved; the old bundle is untouched.
	ErrRefused = errors.New("selfupdate: the replacement was refused")
	// ErrRolledBack: files were moved and every one is back as it was,
	// checked by its bytes.
	ErrRolledBack = errors.New("selfupdate: the new bundle was taken back out")
	// ErrRecoveryNeeded: putting the previous files back did not finish. The
	// folder may hold some of each bundle; it must not be started as either
	// until a recovery pass finishes.
	ErrRecoveryNeeded = errors.New("selfupdate: the Companion's folder needs recovery")
)

// RecoverCommand is what a person runs to finish a recovery, with the
// program that is there — or, when an interruption left none, the copy kept
// in previous/.
const RecoverCommand = "selfupdate recover"

// margin is left free beyond the new files.
const margin = 32 << 20

// testFault, in tests, fails or kills a recovery pass at a named point.
var testFault func(point string) error

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

// areas are the transaction's folders, each owned by the journal.
var areas = []string{"incoming", "previous", "failed"}

// Apply replaces the running bundle's files with the staged bundle's, proves
// the result, and rolls back if it does not prove. It returns the journal.
func Apply(ctx context.Context, request Request) (*Journal, error) {
	if err := request.validateShape(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dirOf(request.Root), 0o700); err != nil {
		return nil, refuse("the Companion's folder %s cannot hold its update folder: %v", request.Root, err)
	}
	lock, err := lockfile.Acquire(lockPath(request.Root), lockfile.Options{Timeout: 5 * time.Second, BreakDeadHolder: true,
		Program: "auto-pigeon-companion replacing itself"})
	if err != nil {
		return nil, refuse("another replacement holds %s: %v", lockPath(request.Root), err)
	}
	defer lock.Release()
	if err := request.settleEarlier(); err != nil {
		return nil, err
	}
	if err := request.validateFiles(); err != nil {
		return nil, err
	}
	journal, err := request.intent()
	if err != nil {
		return nil, err
	}
	if err := request.clearAreas(journal.ID); err != nil {
		return nil, err
	}
	if err := request.fault("journal:" + string(StateActivating)); err != nil {
		return nil, refuse("the journal cannot be written: %v", err)
	}
	if err := writeJournal(request.Root, journal); err != nil {
		return nil, refuse("the journal cannot be written: %v", err)
	}
	tx := transaction{root: request.Root, journal: journal, fault: request.fault, logf: request.logf}
	if err := request.fault("intent"); err != nil {
		return journal, tx.abandon("stopped before the new files were copied: " + err.Error())
	}
	if err := request.copyIncoming(); err != nil {
		return journal, tx.abandon(err.Error())
	}
	if err := request.fault("prepared"); err != nil {
		return journal, tx.abandon("stopped before any file moved: " + err.Error())
	}
	if err := tx.forward(); err != nil {
		return journal, tx.abandon("replacing the files failed: " + err.Error())
	}
	if err := request.fault("swapped"); err != nil {
		return journal, tx.abandon("stopped after the files moved: " + err.Error())
	}
	if request.Prove != nil {
		if err := request.Prove(ctx, under(request.Root, request.Companion)); err != nil {
			return journal, tx.abandon("the new Companion did not pass its proving start: " + err.Error())
		}
	}
	if err := request.fault("proved"); err != nil {
		return journal, tx.abandon("stopped after the proving start: " + err.Error())
	}
	journal.State, journal.Detail = StateActivated, ""
	err = request.fault("journal:" + string(StateActivated))
	if err == nil {
		err = writeJournal(request.Root, journal)
	}
	if err != nil {
		// Nothing durable says the new files are proved: put the old ones
		// back rather than leave a start to guess.
		return journal, tx.abandon("the journal could not record the new files as proved: " + err.Error())
	}
	_ = os.RemoveAll(area(request.Root, "incoming"))
	request.logf("self update: %s -> %s in place in %s; waiting for the new Companion to start", request.From, request.To, request.Root)
	return journal, nil
}

// settleEarlier refuses to start over an earlier replacement that is not
// finished, after trying once to finish it. Nothing kept by an earlier
// replacement is removed until its journal says it is over.
func (r Request) settleEarlier() error {
	journal, err := readJournal(r.Root)
	if err != nil {
		return refuse("the last update's journal cannot be used (%v); nothing is replaced, and nothing it kept is removed, until it is recovered: run `%s %s`",
			err, r.Companion, RecoverCommand)
	}
	if journal == nil {
		for _, name := range areas {
			if _, err := os.Lstat(area(r.Root, name)); err == nil {
				return refuse("%s exists but no journal says which update it belongs to; it is left alone and nothing is replaced", area(r.Root, name))
			}
		}
		return nil
	}
	switch {
	case journal.State.terminal():
		return nil
	case journal.State == StateActivated:
		return refuse("the update to %s is in place and its first start has not confirmed it yet", journal.To)
	}
	r.logf("self update: the replacement %s -> %s was not finished (%s); putting the previous files back first", journal.From, journal.To, journal.State)
	tx := transaction{root: r.Root, journal: journal, fault: r.fault, logf: r.logf}
	if err := tx.rollback("interrupted before it finished; the previous files were put back"); err != nil {
		return fmt.Errorf("%w: an earlier update is not finished, and finishing it failed: %v", ErrRecoveryNeeded, err)
	}
	return nil
}

// intent is the journal of this replacement, with the bytes at every owned
// path read now — not taken from a manifest — so a later pass can tell the
// old file from the new one, and both from anything else.
func (r Request) intent() (*Journal, error) {
	id, err := newID()
	if err != nil {
		return nil, refuse("%v", err)
	}
	journal := &Journal{Schema: JournalSchema, ID: id, From: r.From, To: r.To, Companion: r.Companion,
		State: StateActivating, Started: now()}
	seen := map[string]int{}
	add := func(path string) int {
		if index, ok := seen[path]; ok {
			return index
		}
		seen[path] = len(journal.Members)
		journal.Members = append(journal.Members, Member{Path: path})
		return seen[path]
	}
	for _, member := range r.Members {
		journal.Members[add(member.Path)].New = member.SHA256
	}
	for _, member := range r.Current {
		add(member.Path)
	}
	kept := journal.Members[:0]
	for _, member := range journal.Members {
		old, err := sumOf(under(r.Root, member.Path))
		if err != nil {
			return nil, refuse("%v", err)
		}
		member.Old = old
		if member.Old == "" && member.New == "" {
			continue // listed by the old manifest, and not there
		}
		kept = append(kept, member)
	}
	journal.Members = kept
	// The Companion itself moves last: until then, the folder still starts
	// the old one.
	sort.SliceStable(journal.Members, func(i, j int) bool {
		return journal.Members[i].Path != r.Companion && journal.Members[j].Path == r.Companion
	})
	for _, member := range journal.Members {
		if member.Path == r.Companion {
			journal.FromSHA256, journal.ToSHA256 = member.Old, member.New
		}
	}
	if err := journal.validate(); err != nil {
		return nil, refuse("%v", err)
	}
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
		if err := checkMemberPath(member.Path); err != nil {
			return refuse("a bundle names %q: %v", member.Path, err)
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

// checkMemberPath is what a member path must be wherever it comes from — a
// manifest, or a journal read back from disk.
func checkMemberPath(path string) error {
	if err := acquire.CheckRelativePath(path); err != nil {
		return err
	}
	if first, _, _ := strings.Cut(path, "/"); strings.EqualFold(first, DirName) {
		return errors.New("is inside the update's own folder")
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

// clearAreas removes what the last, finished, replacement kept: settled
// before this runs, so nothing in them is needed. Each is renamed out of the
// way first, so a folder still in use (a running program in previous/ on
// Windows) refuses whole instead of being half deleted.
func (r Request) clearAreas(id string) error {
	entries, _ := os.ReadDir(dirOf(r.Root))
	for _, entry := range entries {
		// Discards an earlier clear did not finish removing.
		if strings.Contains(entry.Name(), ".discard-") {
			_ = os.RemoveAll(filepath.Join(dirOf(r.Root), entry.Name()))
		}
	}
	for _, name := range areas {
		path := area(r.Root, name)
		if _, err := os.Lstat(path); errorsIsNotExist(err) {
			continue
		}
		discard := path + ".discard-" + id
		if err := renameDurable(path, discard); err != nil {
			if name == "previous" {
				return refuse("the bundle kept from the last update is still in use (%v); close every Companion and try again", err)
			}
			return refuse("an earlier update's %s files cannot be removed: %v", name, err)
		}
		_ = os.RemoveAll(discard)
	}
	return nil
}

// copyIncoming copies the staged files into incoming/, on the bundle's own
// filesystem, checking each again as it is written.
func (r Request) copyIncoming() error {
	incoming := area(r.Root, "incoming")
	for _, member := range r.Members {
		target := under(incoming, member.Path)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if member.Executable {
			mode = 0o755
		}
		if err := copyChecked(under(r.Staged, member.Path), target, mode, member.SHA256); err != nil {
			return fmt.Errorf("copying %s into the Companion's folder: %v", member.Path, err)
		}
		syncDir(filepath.Dir(target))
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

// sumOf is the SHA-256 of the plain file at path, "" when there is none. A
// link, a folder, a reparse point or another user's file is an error: no pass
// reads through one or moves one.
func sumOf(path string) (string, error) {
	info, err := os.Lstat(path)
	if errorsIsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a plain file (a link, a folder or a reparse point)", path)
	}
	if !ownedByCurrentUser(info) {
		return "", fmt.Errorf("%s belongs to another user", path)
	}
	return managedtools.FileSHA256(path)
}

// linkAside: keep the old file's path in existence by hard-linking it aside
// (POSIX). Tests turn it off to exercise Windows' rename-aside on Linux.
var linkAside = runtime.GOOS != "windows"

// moveAside puts the file at dest into aside. Where the file is about to be
// replaced and links are possible, a hard link keeps dest in existence until
// the new file is renamed over it.
func moveAside(dest, aside string, replaced bool) error {
	if replaced && linkAside {
		if err := os.Link(dest, aside); err == nil {
			syncDir(filepath.Dir(aside))
			return nil
		}
	}
	return renameDurable(dest, aside)
}

// transaction is one journal's passes.
type transaction struct {
	root    string
	journal *Journal
	fault   func(string) error
	logf    func(string, ...any)
}

func (t transaction) at(point string) error {
	if t.fault != nil {
		return t.fault(point)
	}
	return nil
}

func (t transaction) paths(rel string) (dest, previous, incoming string, err error) {
	dest = under(t.root, rel)
	previous, incoming = under(area(t.root, "previous"), rel), under(area(t.root, "incoming"), rel)
	for _, check := range []struct{ base, path string }{
		{t.root, dest}, {area(t.root, "previous"), previous}, {area(t.root, "incoming"), incoming},
	} {
		if err := plainAncestors(check.base, check.path); err != nil {
			return "", "", "", err
		}
	}
	return dest, previous, incoming, nil
}

// forward brings every member to its new bytes, from wherever an earlier
// pass stopped.
func (t transaction) forward() error {
	for _, member := range t.journal.Members {
		dest, previous, incoming, err := t.paths(member.Path)
		if err != nil {
			return err
		}
		destSum, err := sumOf(dest)
		if err != nil {
			return err
		}
		if member.Old != "" && destSum != member.New {
			previousSum, err := sumOf(previous)
			if err != nil {
				return err
			}
			if previousSum != member.Old {
				switch {
				case previousSum != "":
					return fmt.Errorf("%s in previous/ is not the file this update set aside", member.Path)
				case destSum != member.Old:
					return fmt.Errorf("%s is no longer the file the update recorded", member.Path)
				}
				if err := os.MkdirAll(filepath.Dir(previous), 0o700); err != nil {
					return err
				}
				if err := moveAside(dest, previous, member.New != ""); err != nil {
					return fmt.Errorf("moving %s aside: %w", member.Path, err)
				}
				if err := t.at("aside:" + member.Path); err != nil {
					return err
				}
			}
			if destSum, err = sumOf(dest); err != nil {
				return err
			}
		}
		if member.New == "" {
			if destSum != "" {
				// Only a link can leave it: the old file is in previous/.
				if err := os.Remove(dest); err != nil {
					return err
				}
				syncDir(filepath.Dir(dest))
			}
			continue
		}
		if destSum == member.New {
			continue
		}
		if destSum != "" && destSum != member.Old {
			return fmt.Errorf("%s holds a file that is neither the old nor the new one", member.Path)
		}
		incomingSum, err := sumOf(incoming)
		if err != nil {
			return err
		}
		if incomingSum != member.New {
			return fmt.Errorf("the incoming %s is not the verified file", member.Path)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := renameDurable(incoming, dest); err != nil {
			return fmt.Errorf("putting %s in place: %w", member.Path, err)
		}
		if err := t.at("placed:" + member.Path); err != nil {
			return err
		}
	}
	return nil
}

// abandon rolls the replacement back and says how that ended.
func (t transaction) abandon(detail string) error {
	if err := t.rollback(detail); err != nil {
		return fmt.Errorf("%w: %s; then: %v", ErrRecoveryNeeded, detail, err)
	}
	return fmt.Errorf("%w: %s", ErrRolledBack, detail)
}

// rollback puts every member back to its old bytes and checks them all. On
// anything short of that it records StateRecoveryNeeded and says exactly
// what is where; it never records StateRolledBack it has not checked.
func (t transaction) rollback(detail string) error {
	journal := t.journal
	if journal.Schema == legacySchema {
		return t.legacyRollback(detail)
	}
	journal.State, journal.Detail = StateRollingBack, detail
	if err := t.at("journal:" + string(StateRollingBack)); err == nil {
		// A failure here is not fatal: activating and rolling_back are both
		// "put the previous files back" to the next pass.
		_ = writeJournal(t.root, journal)
	}
	var problems []string
	for i := len(journal.Members) - 1; i >= 0; i-- {
		if problem := t.restore(journal.Members[i]); problem != "" {
			problems = append(problems, problem)
		}
	}
	if len(problems) == 0 {
		problems = t.checkOld()
	}
	return t.settle(detail, problems)
}

// restore brings one member back to its old bytes.
func (t transaction) restore(member Member) string {
	if err := t.at("restore:" + member.Path); err != nil {
		return fmt.Sprintf("%s: %v", member.Path, err)
	}
	dest, previous, _, err := t.paths(member.Path)
	if err != nil {
		return fmt.Sprintf("%s: %v", member.Path, err)
	}
	destSum, err := sumOf(dest)
	if err != nil {
		return fmt.Sprintf("%s: %v", member.Path, err)
	}
	if destSum == member.Old {
		return ""
	}
	if destSum != "" && destSum != member.New {
		return fmt.Sprintf("%s holds a file that is neither the old nor the new one; it is left as it is", member.Path)
	}
	if member.Old == "" {
		// Only the new bundle had it.
		if err := t.park(dest, member.Path, false); err != nil {
			return fmt.Sprintf("%s: %v", member.Path, err)
		}
		return t.restored(member)
	}
	previousSum, err := sumOf(previous)
	if err != nil {
		return fmt.Sprintf("%s: %v", member.Path, err)
	}
	if previousSum != member.Old {
		return fmt.Sprintf("the old %s is neither in place nor in %s", member.Path, area(t.root, "previous"))
	}
	if destSum != "" {
		if err := t.park(dest, member.Path, linkAside); err != nil {
			return fmt.Sprintf("%s: %v", member.Path, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Sprintf("%s: %v", member.Path, err)
	}
	if err := renameDurable(previous, dest); err != nil {
		return fmt.Sprintf("%s: %v", member.Path, err)
	}
	return t.restored(member)
}

func (t transaction) restored(member Member) string {
	if err := t.at("restored:" + member.Path); err != nil {
		return fmt.Sprintf("%s: %v", member.Path, err)
	}
	return ""
}

// park keeps a rolled-back candidate's file in failed/, under a name no
// earlier pass used. link keeps dest in existence (POSIX) until the old file
// is renamed over it.
func (t transaction) park(dest, rel string, link bool) error {
	target := under(area(t.root, "failed"), rel)
	for n := 1; ; n++ {
		if _, err := os.Lstat(target); errorsIsNotExist(err) {
			break
		}
		target = under(area(t.root, "failed"), rel) + fmt.Sprintf(".%d", n)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if link {
		if err := os.Link(dest, target); err == nil {
			syncDir(filepath.Dir(target))
			return nil
		}
	}
	return renameDurable(dest, target)
}

// checkOld reads every owned path back.
func (t transaction) checkOld() []string {
	var problems []string
	for _, member := range t.journal.Members {
		sum, err := sumOf(under(t.root, member.Path))
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s: %v", member.Path, err))
		case sum != member.Old && member.Old == "":
			problems = append(problems, member.Path+" is still there; only the new bundle had it")
		case sum != member.Old:
			problems = append(problems, member.Path+" is not the previous file")
		}
	}
	return problems
}

// settle records how a rollback ended.
func (t transaction) settle(detail string, problems []string) error {
	journal := t.journal
	if len(problems) > 0 {
		journal.State = StateRecoveryNeeded
		journal.Detail = detail + "; NOT everything could be put back (" + strings.Join(problems, "; ") +
			"): the replaced files are kept in " + area(t.root, "previous") + " and " + area(t.root, "failed") +
			"; run `" + journal.Companion + " " + RecoverCommand + "` once the cause is fixed"
		var werr error
		if werr = t.at("journal:" + string(StateRecoveryNeeded)); werr == nil {
			werr = writeJournal(t.root, journal)
		}
		if werr != nil {
			return fmt.Errorf("%s (and the journal could not record it: %v)", journal.Detail, werr)
		}
		return errors.New(journal.Detail)
	}
	journal.State, journal.Detail = StateRolledBack, detail
	werr := t.at("journal:" + string(StateRolledBack))
	if werr == nil {
		werr = writeJournal(t.root, journal)
	}
	if werr != nil {
		journal.State = StateRecoveryNeeded
		journal.Detail = detail + "; the previous files are back, but the journal could not record it (" + werr.Error() + ")"
		return errors.New(journal.Detail)
	}
	if t.logf != nil {
		t.logf("self update: %s -> %s rolled back; every file is the previous one again", journal.From, journal.To)
	}
	return nil
}

// legacyRollback undoes a 1.323 journal. It has no per-file digests, so it
// goes by where the files are — the renames are what moved them, and a
// rename leaves no file in two places — not by its flags: a new file whose
// incoming copy is gone was placed; an old file still in previous/ was set
// aside. The one digest it has, the old Companion's, is checked at the end.
func (t transaction) legacyRollback(detail string) error {
	journal := t.journal
	var problems []string
	for i := len(journal.Steps) - 1; i >= 0; i-- {
		step := journal.Steps[i]
		if err := t.at("restore:" + step.Path); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", step.Path, err))
			continue
		}
		dest, previous, incoming, err := t.paths(step.Path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", step.Path, err))
			continue
		}
		destSum, destErr := sumOf(dest)
		previousSum, previousErr := sumOf(previous)
		incomingSum, incomingErr := sumOf(incoming)
		if err := errors.Join(destErr, previousErr, incomingErr); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", step.Path, err))
			continue
		}
		if previousSum == "" {
			continue // never set aside, or already back
		}
		if destSum != "" && (incomingSum == "" || destSum != previousSum) && step.New {
			if err := t.park(dest, step.Path, linkAside); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", step.Path, err))
				continue
			}
		} else if destSum != "" && destSum != previousSum {
			problems = append(problems, step.Path+" holds a file this update did not place; it is left as it is")
			continue
		}
		if err := renameDurable(previous, dest); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", step.Path, err))
		}
	}
	if len(problems) == 0 {
		if sum, err := sumOf(under(t.root, journal.Companion)); err != nil || sum != journal.FromSHA256 {
			problems = append(problems, journal.Companion+" is not the previous Companion")
		}
	}
	return t.settle(detail, problems)
}

// Rollback takes an activated replacement back out: the new Companion did
// not start and say it was ready. The caller has made sure it is not running.
func Rollback(root, detail string) (*Journal, error) {
	lock, err := lockfile.Acquire(lockPath(root), lockfile.Options{Timeout: 5 * time.Second, BreakDeadHolder: true,
		Program: "auto-pigeon-companion undoing its update"})
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	journal, err := readJournal(root)
	if err != nil || journal == nil {
		if err == nil {
			err = errors.New("selfupdate: there is no update to take back")
		}
		return journal, err
	}
	if journal.State.terminal() {
		return journal, nil
	}
	tx := transaction{root: root, journal: journal, fault: testFault}
	return journal, tx.rollback(detail)
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
	// NeedsRecovery: an unfinished replacement could not be undone, or its
	// journal cannot be read. The folder may hold parts of two bundles.
	NeedsRecovery Outcome = "recovery_needed"
)

// maxUnreadyStarts is how many starts of a replaced Companion may go by
// without one of them becoming ready before the replacement is undone.
const maxUnreadyStarts = 3

// Recover looks at the journal as a Companion starts from root. An
// interrupted replacement is rolled back. An activated one is this process's
// to confirm — unless this is the third start that has not, in which case it
// is undone, so a candidate that cannot serve does not stay in place.
//
// runningSHA "" is the recovery command: an activated replacement is a
// complete, proved bundle, and is left for its own start to confirm.
//
// Nothing is ever removed here: what an unfinished replacement kept is the
// way back, and what a finished one kept is cleared only by the next one.
func Recover(root, runningSHA string, logf func(string, ...any)) (*Journal, Outcome, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if _, err := os.Lstat(journalPath(root)); errorsIsNotExist(err) {
		return nil, Nothing, nil
	}
	lock, err := lockfile.Acquire(lockPath(root), lockfile.Options{Timeout: 5 * time.Second, BreakDeadHolder: true,
		Program: "auto-pigeon-companion checking its last update"})
	if err != nil {
		return nil, NeedsRecovery, err
	}
	defer lock.Release()
	journal, err := readJournal(root)
	if err != nil || journal == nil {
		return journal, NeedsRecovery, err
	}
	tx := transaction{root: root, journal: journal, fault: testFault, logf: logf}
	switch journal.State {
	case StateActivating, StateRollingBack, StateRecoveryNeeded:
		logf("self update: the replacement %s -> %s was not finished (%s); putting the previous files back", journal.From, journal.To, journal.State)
		if err := tx.rollback("interrupted before it finished; the previous files were put back"); err != nil {
			return journal, NeedsRecovery, err
		}
		return journal, RolledBack, nil
	case StateActivated:
		if runningSHA == "" || runningSHA != journal.ToSHA256 {
			return journal, Nothing, nil
		}
		journal.Starts++
		if journal.Starts >= maxUnreadyStarts {
			logf("self update: %s has started %d times without becoming ready; putting %s back", journal.To, journal.Starts, journal.From)
			if err := tx.rollback(fmt.Sprintf("version %s started %d times without becoming ready", journal.To, journal.Starts)); err != nil {
				return journal, NeedsRecovery, err
			}
			return journal, RolledBack, nil
		}
		return journal, Pending, writeJournal(root, journal)
	}
	return journal, Nothing, nil
}

// RecoveryRoot is the bundle folder a recovery started from exe works on:
// the folder exe is in, or — for the copy an update kept in previous/, the
// one program there is when an interruption left none at its own path — the
// bundle folder that copy belongs to.
func RecoveryRoot(exe, companion string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	exe, err := filepath.Abs(exe)
	if err != nil {
		return "", err
	}
	if err := checkMemberPath(companion); err != nil {
		return "", err
	}
	depth := strings.Count(companion, "/") + 1
	base := exe
	for i := 0; i < depth; i++ {
		base = filepath.Dir(base)
	}
	if filepath.Base(base) == "previous" && filepath.Base(filepath.Dir(base)) == DirName {
		return filepath.Dir(filepath.Dir(base)), nil
	}
	return base, nil
}

// MarkVerified records that the new Companion started and serves.
func MarkVerified(root, runningSHA string) (*Journal, error) {
	lock, err := lockfile.Acquire(lockPath(root), lockfile.Options{Timeout: 5 * time.Second, BreakDeadHolder: true,
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

// maxJournalBytes bounds what is read as a journal.
const maxJournalBytes = 1 << 20

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
	if info.Size() > maxJournalBytes {
		return nil, fmt.Errorf("selfupdate: %s is %d bytes, more than a journal is", path, info.Size())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var journal Journal
	if err := json.Unmarshal(raw, &journal); err != nil {
		return nil, fmt.Errorf("selfupdate: %s is not readable: %w", path, err)
	}
	if err := journal.validate(); err != nil {
		return nil, fmt.Errorf("selfupdate: %s cannot be used: %w", path, err)
	}
	return &journal, nil
}

// validate is what a journal must be before any pass writes a file because
// of it: it is the authority on which paths a recovery moves.
func (j *Journal) validate() error {
	if !j.State.known() {
		return fmt.Errorf("state %q is not one this program knows", j.State)
	}
	if j.Companion == "" {
		return errors.New("it names no Companion")
	}
	if err := checkMemberPath(j.Companion); err != nil {
		return fmt.Errorf("its Companion %q %v", j.Companion, err)
	}
	seen := map[string]bool{}
	path := func(p string) error {
		if err := checkMemberPath(p); err != nil {
			return fmt.Errorf("it names %q, which %v", p, err)
		}
		// Case-folded: on Windows and macOS two spellings are one file.
		if key := strings.ToLower(p); seen[key] {
			return fmt.Errorf("it names %q twice", p)
		} else {
			seen[key] = true
		}
		return nil
	}
	switch j.Schema {
	case JournalSchema:
		if len(j.Members) == 0 || len(j.Steps) != 0 {
			return errors.New("it lists no files")
		}
		companion := false
		for _, member := range j.Members {
			if err := path(member.Path); err != nil {
				return err
			}
			if !digestOrEmpty(member.Old) || !digestOrEmpty(member.New) || (member.Old == "" && member.New == "") {
				return fmt.Errorf("its digests for %q are not SHA-256", member.Path)
			}
			if member.Path == j.Companion {
				companion = member.New != "" && member.Old == j.FromSHA256 && member.New == j.ToSHA256
			}
		}
		if !companion {
			return errors.New("its Companion entry does not match its own record")
		}
	case legacySchema:
		if len(j.Steps) == 0 || len(j.Members) != 0 {
			return errors.New("it lists no files")
		}
		for _, step := range j.Steps {
			if err := path(step.Path); err != nil {
				return err
			}
		}
		if !digestOrEmpty(j.FromSHA256) || !digestOrEmpty(j.ToSHA256) {
			return errors.New("its Companion digests are not SHA-256")
		}
	default:
		return fmt.Errorf("schema %q is not one this program reads", j.Schema)
	}
	return nil
}

func digestOrEmpty(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func writeJournal(root string, journal *Journal) error {
	if journal.Schema == "" {
		journal.Schema = JournalSchema
	}
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
	return renameDurable(name, journalPath(root))
}

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func within(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
