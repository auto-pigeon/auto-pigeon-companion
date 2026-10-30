package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/oslock"
)

// A Quake III conversion is two files, a `.map` and the manifest describing it, and renaming each one
// atomically does not make the pair atomic. So publication is a small transaction inside the output
// directory, and what it promises is exact:
//
//   - exit 0: every file of the result is at its path, read back and matching, and a manifest describes
//     the bytes beside it. An earlier result of the same name has been replaced.
//   - a returned error or a cancellation: whatever stood at those paths before stands there again, byte
//     for byte. Nothing else in the directory is touched, and nothing of this run is left behind. If the
//     rollback itself is stopped, the error says so and names the folder holding the previous files.
//   - the process dies part-way (SIGKILL, a crash, a Windows kill after the grace period): there is NO
//     cross-file atomicity. For a moment the directory can hold a new `.map` with no manifest or with the
//     old one. The manifest is published last, and it names the `.map` by digest, so that state is one a
//     reader refuses (internal/assetref does), and a `.mapio-commit-*` folder is left standing. The next
//     conversion into that directory finishes the rollback before it does anything else.
//   - power loss is not promised. Files and directories are flushed in order where the system lets them
//     be, which is an argument and not a test.
//
// The lock serialises conversions that share an output directory, and a file of its name stays there:
// removing a lock file is how two processes come to hold two different locks (internal/oslock).
const (
	commitPrefix  = ".mapio-commit-"
	commitLock    = ".mapio-commit.lock"
	commitJournal = "journal.json"
	journalSchema = "auto-pigeon-mapio-commit/1"
)

// lockWait bounds how long a conversion waits for another one publishing into the same directory.
var lockWait = 30 * time.Second

// resultFile is one file of a result, by its name inside the output directory.
type resultFile struct {
	Name string
	Data []byte
}

// journal is written once, before anything moves. While it exists the commit has not happened, and
// removing it is the commit: that one step is what a later run tells the two outcomes apart by.
type journal struct {
	Schema string        `json:"schema"`
	Files  []journalFile `json:"files"`
}

type journalFile struct {
	Name     string `json:"name"`
	SHA256   string `json:"sha256"`
	Previous bool   `json:"previous"`
}

func stagedName(index int) string { return "new-" + strconv.Itoa(index) }
func keptName(index int) string   { return "old-" + strconv.Itoa(index) }

// commitBoundary is nil outside tests. It is told each step of a commit before it happens, which is the
// one place a test can stop or fail one, and its error is treated as the step's own.
var commitBoundary func(step string) error

// commitResult publishes files into directory, in the order given: a manifest goes last.
func commitResult(ctx context.Context, directory string, files []resultFile) error {
	for _, file := range files {
		if err := checkResultName(file.Name); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	unlock, err := lockDirectory(ctx, directory)
	if err != nil {
		return err
	}
	defer unlock()
	if err := recoverCommits(directory); err != nil {
		return err
	}

	staging, err := os.MkdirTemp(directory, commitPrefix+"*")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(staging)
		}
	}()
	record, err := stage(ctx, directory, staging, files)
	if err != nil {
		return err
	}
	if err := writeJournal(staging, record); err != nil {
		return err
	}
	if err := publish(ctx, directory, staging, record); err != nil {
		if undo := rollBack(directory, staging, record); undo != nil {
			keep = true
			return fmt.Errorf("%w; the rollback did not finish, and the previous files are in %s: %v", err, staging, undo)
		}
		return err
	}
	// The commit. Before this line a later run undoes the result, after it the result stands.
	if err := os.Remove(filepath.Join(staging, commitJournal)); err != nil {
		if undo := rollBack(directory, staging, record); undo != nil {
			keep = true
			return fmt.Errorf("%w; the rollback did not finish, and the previous files are in %s: %v", err, staging, undo)
		}
		return err
	}
	syncDirectory(staging)
	if commitBoundary != nil {
		_ = commitBoundary("committed")
	}
	return nil
}

// checkResultName refuses anything but a plain name in the directory, and the names commits use.
func checkResultName(name string) error {
	if !safeResultName(name) {
		return fmt.Errorf("%q cannot be written into the output directory", name)
	}
	return nil
}

func safeResultName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`) &&
		name == filepath.Base(name) && !strings.HasPrefix(name, commitPrefix) && name != commitLock
}

// stage writes every file into the commit's own folder and says what stands at each target, touching
// no target. Anything that could refuse early does so here, while nothing has moved.
func stage(ctx context.Context, directory, staging string, files []resultFile) (journal, error) {
	record := journal{Schema: journalSchema}
	for index, file := range files {
		digest, err := writeDurable(filepath.Join(staging, stagedName(index)), file.Data)
		if err != nil {
			return journal{}, err
		}
		info, err := os.Lstat(filepath.Join(directory, file.Name))
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return journal{}, err
		case !info.Mode().IsRegular():
			return journal{}, fmt.Errorf("%s is in the way: it is not a regular file, and it is left alone", file.Name)
		}
		record.Files = append(record.Files, journalFile{Name: file.Name, SHA256: digest, Previous: err == nil})
	}
	syncDirectory(staging)
	if err := checkpoint(ctx, "staged"); err != nil {
		return journal{}, err
	}
	return record, nil
}

// publish moves the staged files into place, then reads them back. Cancellation is checked at every
// boundary, and the last one is after the read-back: a run cancelled here has not committed.
func publish(ctx context.Context, directory, staging string, record journal) error {
	for index, file := range record.Files {
		target := filepath.Join(directory, file.Name)
		if err := checkpoint(ctx, "publish:"+file.Name); err != nil {
			return err
		}
		if file.Previous {
			if err := os.Rename(target, filepath.Join(staging, keptName(index))); err != nil {
				return err
			}
		}
		if err := checkpoint(ctx, "place:"+file.Name); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(staging, stagedName(index)), target); err != nil {
			return err
		}
		syncDirectory(directory)
	}
	for _, file := range record.Files {
		digest, err := digestAt(filepath.Join(directory, file.Name))
		if err != nil {
			return err
		}
		if digest != file.SHA256 {
			return fmt.Errorf("%s does not hold the bytes just published", file.Name)
		}
	}
	return checkpoint(ctx, "commit")
}

func checkpoint(ctx context.Context, step string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if commitBoundary != nil {
		return commitBoundary(step)
	}
	return nil
}

// rollBack puts the directory back as the journal found it, deciding from what is actually at each
// path, so it can run again after a rollback that was itself interrupted. It removes only bytes the
// journal says this result wrote, and overwrites nothing else.
func rollBack(directory, staging string, record journal) error {
	for index := len(record.Files) - 1; index >= 0; index-- {
		file := record.Files[index]
		target := filepath.Join(directory, file.Name)
		present, err := regularDigest(target)
		if err != nil {
			return err
		}
		kept := filepath.Join(staging, keptName(index))
		info, err := os.Lstat(kept)
		switch {
		case err == nil && info.Mode().IsRegular():
			if present != "" && present != file.SHA256 {
				return fmt.Errorf("%s holds bytes that are neither result, and it is left alone", file.Name)
			}
			if err := os.Rename(kept, target); err != nil {
				return err
			}
		case err != nil && !errors.Is(err, os.ErrNotExist):
			return err
		case !file.Previous && present == file.SHA256:
			if err := os.Remove(target); err != nil {
				return err
			}
		}
	}
	syncDirectory(directory)
	return nil
}

// recoverCommits finishes what a dead process left in directory, and runs under the lock, so every
// commit folder it finds is abandoned. One with a journal is undone. One without either never reached
// it or is past the commit, and holds nothing anybody needs.
func recoverCommits(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), commitPrefix) {
			continue
		}
		staging := filepath.Join(directory, entry.Name())
		record, err := readJournal(staging)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.RemoveAll(staging); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("an interrupted conversion left %s, which cannot be recovered: %w", entry.Name(), err)
		}
		if err := rollBack(directory, staging, record); err != nil {
			return fmt.Errorf("recovering the interrupted conversion in %s: %w", entry.Name(), err)
		}
		if err := os.RemoveAll(staging); err != nil {
			return err
		}
	}
	return nil
}

func writeJournal(staging string, record journal) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	// Whole or absent: a folder with no journal is one nothing has moved out of.
	temporary := filepath.Join(staging, "journal.tmp")
	if _, err := writeDurable(temporary, data); err != nil {
		return err
	}
	if err := os.Rename(temporary, filepath.Join(staging, commitJournal)); err != nil {
		return err
	}
	syncDirectory(staging)
	return nil
}

func readJournal(staging string) (journal, error) {
	data, err := os.ReadFile(filepath.Join(staging, commitJournal))
	if err != nil {
		return journal{}, err
	}
	var record journal
	if err := json.Unmarshal(data, &record); err != nil {
		return journal{}, err
	}
	if record.Schema != journalSchema {
		return journal{}, fmt.Errorf("its journal is %q", record.Schema)
	}
	for _, file := range record.Files {
		if !safeResultName(file.Name) {
			return journal{}, fmt.Errorf("its journal names %q", file.Name)
		}
	}
	return record, nil
}

// lockDirectory waits, for a bounded time, for the directory's commit lock. A directory whose file
// system has no lock is refused: the commit it would protect is the thing that must not go unprotected.
func lockDirectory(ctx context.Context, directory string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(directory, commitLock), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		held, err := oslock.TryLock(file)
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("the output directory cannot be locked: %w", err)
		}
		if held {
			return func() {
				_ = oslock.Unlock(file)
				file.Close()
			}, nil
		}
		if time.Now().After(deadline) {
			file.Close()
			return nil, fmt.Errorf("another conversion is still publishing into %s", directory)
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func writeDurable(path string, data []byte) (string, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	if err := errors.Join(writeErr, syncErr, file.Close()); err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// regularDigest is the digest at path, "" when nothing is there, and something no digest equals when
// what is there is not a file.
func regularDigest(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "not a regular file", nil
	}
	return digestAt(path)
}

func digestAt(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// syncDirectory flushes a directory's entries where the system can be asked to. Windows cannot, and
// that is one reason power loss is not on the list above.
func syncDirectory(directory string) {
	if handle, err := os.Open(directory); err == nil {
		_ = handle.Sync()
		handle.Close()
	}
}
