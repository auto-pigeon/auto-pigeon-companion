package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	roomMap      = "room.map"
	roomManifest = "room.q3map-manifest.json"
	// dieAtEnv makes this test binary a conversion that kills itself at one commit step.
	dieAtEnv = "MAPIO_TEST_DIE_AT"
)

// The real helper process for TestAProcessKilledAtAnyStepIsUndoneByTheNextRun: a whole `convert` that
// dies, without any cleanup, the moment its commit reaches the named step.
func TestMain(m *testing.M) {
	if step := os.Getenv(dieAtEnv); step != "" {
		commitBoundary = func(reached string) error {
			if reached == step {
				process, err := os.FindProcess(os.Getpid())
				if err == nil {
					_ = process.Kill()
				}
				time.Sleep(time.Minute)
			}
			return nil
		}
		os.Exit(run(context.Background(), strings.Split(os.Getenv("MAPIO_TEST_ARGS"), "\n"), os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// q3Source copies one of go/mapio's Quake III fixtures to directory/room.apmap, so two different
// documents have one output name.
func q3Source(t *testing.T, directory, fixture string) string {
	t.Helper()
	command := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/auto-pigeon/auto-pigeon-libraries")
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(output)), "go", "mapio", "testdata", "q3map", fixture))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "room.apmap")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func convertQ3(t *testing.T, ctx context.Context, source, out string) (int, string) {
	t.Helper()
	code, _, stderr := invoke(t, ctx, "convert", "--apmap-to-q3map", "--input", source, "--output", out, "--json")
	return code, stderr
}

// pair is the two files of a result, read from a directory.
type pair struct{ mapText, manifest []byte }

func readPair(t *testing.T, directory string) pair {
	t.Helper()
	mapText, err := os.ReadFile(filepath.Join(directory, roomMap))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(directory, roomManifest))
	if err != nil {
		t.Fatal(err)
	}
	return pair{mapText, manifest}
}

// describes is the check a reader makes: this manifest names these bytes.
func describes(t *testing.T, result pair) bool {
	t.Helper()
	var document struct {
		Output struct {
			SHA256 string `json:"sha256"`
		} `json:"output"`
	}
	if err := json.Unmarshal(result.manifest, &document); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(result.mapText)
	return document.Output.SHA256 == hex.EncodeToString(sum[:])
}

func assertPair(t *testing.T, directory string, want pair) {
	t.Helper()
	got := readPair(t, directory)
	if !bytes.Equal(got.mapText, want.mapText) || !bytes.Equal(got.manifest, want.manifest) {
		t.Fatalf("the pair in %s is not the expected one", directory)
	}
	if !describes(t, got) {
		t.Fatalf("the manifest in %s does not describe its map", directory)
	}
}

// assertHolds says what a directory holds, leaving out the lock, which stays.
func assertHolds(t *testing.T, directory string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, entry := range entries {
		if entry.Name() != commitLock {
			found = append(found, entry.Name())
		}
	}
	sort.Strings(names)
	if strings.Join(found, ",") != strings.Join(names, ",") {
		t.Fatalf("%s holds %v, want %v", directory, found, names)
	}
}

// fixtureResults converts both documents once, for a test to compare against.
func fixtureResults(t *testing.T) (first, second string, older, newer pair) {
	t.Helper()
	root := t.TempDir()
	first = q3Source(t, filepath.Join(root, "first"), "q3004-room.apmap")
	second = q3Source(t, filepath.Join(root, "second"), "q3004-room-brushdef.apmap")
	for _, source := range []string{first, second} {
		if code, stderr := convertQ3(t, context.Background(), source, filepath.Join(filepath.Dir(source), "out")); code != exitOK {
			t.Fatalf("exit %d: %s", code, stderr)
		}
	}
	older, newer = readPair(t, filepath.Join(filepath.Dir(first), "out")), readPair(t, filepath.Join(filepath.Dir(second), "out"))
	if bytes.Equal(older.mapText, newer.mapText) {
		t.Fatal("the two fixtures make one map, so nothing here could tell them apart")
	}
	return first, second, older, newer
}

func withBoundary(t *testing.T, boundary func(string) error) {
	t.Helper()
	commitBoundary = boundary
	t.Cleanup(func() { commitBoundary = nil })
}

// existingResult is an output directory holding a finished older result and somebody's own file.
func existingResult(t *testing.T, source string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	if code, stderr := convertQ3(t, context.Background(), source, out); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if err := os.WriteFile(filepath.Join(out, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAResultIsPublishedWholeInAFreshDirectory(t *testing.T) {
	first, _, older, _ := fixtureResults(t)
	out := filepath.Join(t.TempDir(), "fresh")
	if code, stderr := convertQ3(t, context.Background(), first, out); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	assertPair(t, out, older)
	assertHolds(t, out, roomManifest, roomMap)
}

func TestAResultReplacesThePairBeforeItAndNothingElse(t *testing.T) {
	first, second, _, newer := fixtureResults(t)
	out := existingResult(t, first)
	if code, stderr := convertQ3(t, context.Background(), second, out); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	assertPair(t, out, newer)
	assertHolds(t, out, "notes.txt", roomManifest, roomMap)
	if mine, err := os.ReadFile(filepath.Join(out, "notes.txt")); err != nil || string(mine) != "mine" {
		t.Fatalf("somebody's own file: %q, %v", mine, err)
	}
}

// A nonempty directory where the manifest must land is the failure this code was found with. It is
// seen before anything moves, on a real file system.
func TestSomethingInTheWayIsRefusedBeforeAnythingMoves(t *testing.T) {
	first, second, older, _ := fixtureResults(t)

	fresh := t.TempDir()
	blocked := filepath.Join(fresh, roomManifest)
	if err := os.MkdirAll(filepath.Join(blocked, "occupied"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, stderr := convertQ3(t, context.Background(), first, fresh); code != exitFailed ||
		!strings.Contains(stderr, `"reason":"io_error"`) || !strings.Contains(stderr, "in the way") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	assertHolds(t, fresh, roomManifest)

	replaced := existingResult(t, first)
	manifest := filepath.Join(replaced, roomManifest)
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(manifest, "occupied"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _ := convertQ3(t, context.Background(), second, replaced); code != exitFailed {
		t.Fatalf("exit %d", code)
	}
	if got, err := os.ReadFile(filepath.Join(replaced, roomMap)); err != nil || !bytes.Equal(got, older.mapText) {
		t.Fatalf("the map beside the blocked path changed: %v", err)
	}
}

// Every step after the first file moved, on both an empty directory and a finished older result.
func TestAFailureAfterOneFileMovedPutsEverythingBack(t *testing.T) {
	first, second, older, _ := fixtureResults(t)
	for _, step := range []string{"place:" + roomMap, "publish:" + roomManifest, "place:" + roomManifest, "commit"} {
		t.Run("fresh/"+step, func(t *testing.T) {
			withBoundary(t, func(reached string) error {
				if reached == step {
					return errors.New("injected")
				}
				return nil
			})
			out := t.TempDir()
			code, stderr := convertQ3(t, context.Background(), first, out)
			if code != exitFailed || !strings.Contains(stderr, `"reason":"io_error"`) {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			assertHolds(t, out)
		})
		t.Run("replacing/"+step, func(t *testing.T) {
			out := existingResult(t, first)
			withBoundary(t, func(reached string) error {
				if reached == step {
					return errors.New("injected")
				}
				return nil
			})
			if code, stderr := convertQ3(t, context.Background(), second, out); code != exitFailed {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			assertPair(t, out, older)
			assertHolds(t, out, "notes.txt", roomManifest, roomMap)
		})
	}
}

// The file system itself refuses part-way, after the `.map` is already in place: nothing here mocks a
// rename, the error is the operating system's.
func TestARealRefusalMidCommitPutsEverythingBack(t *testing.T) {
	first, second, older, _ := fixtureResults(t)

	t.Run("fresh", func(t *testing.T) {
		out := t.TempDir()
		withBoundary(t, func(reached string) error {
			if reached == "place:"+roomManifest {
				return os.MkdirAll(filepath.Join(out, roomManifest, "arrived"), 0o755)
			}
			return nil
		})
		code, stderr := convertQ3(t, context.Background(), first, out)
		if code != exitFailed || !strings.Contains(stderr, `"reason":"io_error"`) {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		assertHolds(t, out, roomManifest)
		if _, err := os.Stat(filepath.Join(out, roomManifest, "arrived")); err != nil {
			t.Fatalf("what arrived was removed: %v", err)
		}
	})
	t.Run("replacing", func(t *testing.T) {
		out := existingResult(t, first)
		withBoundary(t, func(reached string) error {
			if reached != "publish:"+roomManifest {
				return nil
			}
			folders, err := filepath.Glob(filepath.Join(out, commitPrefix+"*"))
			if err != nil || len(folders) != 1 {
				t.Fatalf("commit folders: %v, %v", folders, err)
			}
			// A nonempty directory where the old manifest is set aside to.
			return os.MkdirAll(filepath.Join(folders[0], keptName(1), "occupied"), 0o755)
		})
		if code, stderr := convertQ3(t, context.Background(), second, out); code != exitFailed {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		assertPair(t, out, older)
		assertHolds(t, out, "notes.txt", roomManifest, roomMap)
	})
}

// A cancellation seen at any boundary is reported as one, even with the whole result in place, because
// the commit has not happened. Each step only cancels: the check at the next boundary has to notice.
func TestACancellationAtAnyBoundaryPublishesNothingAndSaysSo(t *testing.T) {
	first, second, older, _ := fixtureResults(t)
	for _, step := range []string{"staged", "publish:" + roomMap, "place:" + roomMap, "publish:" + roomManifest, "place:" + roomManifest} {
		t.Run(step, func(t *testing.T) {
			out := existingResult(t, first)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			withBoundary(t, func(reached string) error {
				if reached == step {
					cancel()
				}
				return nil
			})
			code, stderr := convertQ3(t, ctx, second, out)
			if code != exitFailed || !strings.Contains(stderr, `"reason":"cancelled"`) {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			assertPair(t, out, older)
			assertHolds(t, out, "notes.txt", roomManifest, roomMap)
		})
	}
}

// The context ending between the conversion and the commit, seen by the commit's first check.
func TestAContextEndedBeforeTheCommitReachesNoFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := filepath.Join(t.TempDir(), "out")
	if err := commitResult(ctx, out, []resultFile{{Name: roomMap, Data: []byte("x")}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a cancelled commit created its directory: %v", err)
	}
}

// A real process, really killed, at every step. Until a later run recovers, what it leaves is either
// the old pair or a state a reader refuses, and the next conversion into the directory undoes it
// without being told to.
func TestAProcessKilledAtAnyStepIsUndoneByTheNextRun(t *testing.T) {
	first, second, older, newer := fixtureResults(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "cube.map")
	if err := os.WriteFile(other, []byte(cubeMap), 0o644); err != nil {
		t.Fatal(err)
	}
	die := func(t *testing.T, step, source, out string) {
		t.Helper()
		child := exec.Command(self)
		child.Env = append(os.Environ(), dieAtEnv+"="+step,
			"MAPIO_TEST_ARGS="+strings.Join([]string{"convert", "--apmap-to-q3map", "--input", source, "--output", out}, "\n"))
		if output, err := child.CombinedOutput(); err == nil {
			t.Fatalf("the child reached no step %q:\n%s", step, output)
		}
		if folders, _ := filepath.Glob(filepath.Join(out, commitPrefix+"*")); len(folders) != 1 {
			t.Fatalf("a killed commit left %v", folders)
		}
	}
	// recoverWith is an unrelated conversion into the same directory.
	recoverWith := func(t *testing.T, out string) {
		t.Helper()
		code, _, stderr := invoke(t, context.Background(), "convert", "--q1map-to-apmap", "--input", other, "--output", out)
		if code != exitOK {
			t.Fatalf("recovery: exit %d: %s", code, stderr)
		}
	}
	steps := []string{"staged", "publish:" + roomMap, "place:" + roomMap, "publish:" + roomManifest, "place:" + roomManifest, "commit"}
	for _, step := range steps {
		t.Run("fresh/"+step, func(t *testing.T) {
			out := t.TempDir()
			die(t, step, first, out)
			recoverWith(t, out)
			assertHolds(t, out, "cube.apmap")
			if code, stderr := convertQ3(t, context.Background(), first, out); code != exitOK {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			assertPair(t, out, older)
		})
		t.Run("replacing/"+step, func(t *testing.T) {
			out := existingResult(t, first)
			die(t, step, second, out)
			if step == "place:"+roomManifest || step == "publish:"+roomManifest {
				// The new map, and the old or no manifest: never a pair a reader takes.
				got, err := os.ReadFile(filepath.Join(out, roomMap))
				if err != nil {
					t.Fatal(err)
				}
				manifest, err := os.ReadFile(filepath.Join(out, roomManifest))
				if err == nil && describes(t, pair{got, manifest}) {
					t.Fatal("a killed commit left a pair that agrees")
				}
			}
			recoverWith(t, out)
			assertPair(t, out, older)
			assertHolds(t, out, "cube.apmap", "notes.txt", roomManifest, roomMap)
			if code, stderr := convertQ3(t, context.Background(), second, out); code != exitOK {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			assertPair(t, out, newer)
		})
	}
	// Past the commit the new pair stands, and only the empty folder is left to clear.
	t.Run("replacing/committed", func(t *testing.T) {
		out := existingResult(t, first)
		die(t, "committed", second, out)
		assertPair(t, out, newer)
		recoverWith(t, out)
		assertPair(t, out, newer)
		assertHolds(t, out, "cube.apmap", "notes.txt", roomManifest, roomMap)
	})
}

// A rollback that cannot finish says where the previous files are, and a later run refuses rather than
// guess. Bytes that belong to neither result are nobody's to overwrite.
func TestARollbackThatCannotFinishKeepsThePreviousFilesAndSaysWhere(t *testing.T) {
	first, second, older, _ := fixtureResults(t)
	out := existingResult(t, first)
	foreign := []byte("somebody wrote this meanwhile")
	withBoundary(t, func(reached string) error {
		if reached == "publish:"+roomManifest {
			return os.WriteFile(filepath.Join(out, roomMap), foreign, 0o644)
		}
		if reached == "commit" {
			t.Fatal("foreign bytes reached the commit")
		}
		return nil
	})
	code, stderr := convertQ3(t, context.Background(), second, out)
	if code != exitFailed || !strings.Contains(stderr, "the previous files are in") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got, err := os.ReadFile(filepath.Join(out, roomMap)); err != nil || !bytes.Equal(got, foreign) {
		t.Fatalf("the foreign file was replaced: %v", err)
	}
	folders, err := filepath.Glob(filepath.Join(out, commitPrefix+"*"))
	if err != nil || len(folders) != 1 {
		t.Fatalf("commit folders: %v, %v", folders, err)
	}
	if kept, err := os.ReadFile(filepath.Join(folders[0], keptName(0))); err != nil || !bytes.Equal(kept, older.mapText) {
		t.Fatalf("the previous map is not kept: %v", err)
	}
	commitBoundary = nil
	if code, stderr := convertQ3(t, context.Background(), first, out); code != exitFailed || !strings.Contains(stderr, "recovering") {
		t.Fatalf("a later run went ahead: exit %d: %s", code, stderr)
	}
}

func TestAJournalNobodyCanReadIsRefusedAndTouchesNothing(t *testing.T) {
	first, second, older, _ := fixtureResults(t)
	for name, body := range map[string]string{
		"not json":     "{",
		"other schema": `{"schema":"another/1","files":[]}`,
		"a path":       `{"schema":"` + journalSchema + `","files":[{"name":"../outside","sha256":"00"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			out := existingResult(t, first)
			abandoned := filepath.Join(out, commitPrefix+"abandoned")
			if err := os.Mkdir(abandoned, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(abandoned, commitJournal), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if code, stderr := convertQ3(t, context.Background(), second, out); code != exitFailed ||
				!strings.Contains(stderr, "cannot be recovered") {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			assertPair(t, out, older)
		})
	}
}

func TestANameThatLeavesTheDirectoryIsRefused(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../up.map", "a/b.map", `a\b.map`, commitPrefix + "x", commitLock} {
		out := t.TempDir()
		if err := commitResult(context.Background(), out, []resultFile{{Name: name, Data: []byte("x")}}); err == nil {
			t.Errorf("%q was published", name)
		}
	}
}

func TestADirectoryThatCannotBeWrittenIsAnIOError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("permission bits do not stop this user here")
	}
	first, _, _, _ := fixtureResults(t)
	out := t.TempDir()
	if err := os.Chmod(out, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(out, 0o755) })
	code, stderr := convertQ3(t, context.Background(), first, out)
	if code != exitFailed || !strings.Contains(stderr, `"reason":"io_error"`) {
		t.Fatalf("exit %d: %s", code, stderr)
	}
}

func TestConversionsInSeparateDirectoriesDoNotMeet(t *testing.T) {
	first, second, older, newer := fixtureResults(t)
	var wait sync.WaitGroup
	root := t.TempDir()
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			source, out := first, filepath.Join(root, fmt.Sprintf("out-%d", index))
			if index%2 == 1 {
				source = second
			}
			var stdout, stderr bytes.Buffer
			if code := run(context.Background(), []string{"convert", "--apmap-to-q3map", "--input", source, "--output", out}, &stdout, &stderr); code != exitOK {
				t.Errorf("%s: exit %d: %s", out, code, stderr.String())
			}
		}()
	}
	wait.Wait()
	for index := 0; index < 8; index++ {
		want := older
		if index%2 == 1 {
			want = newer
		}
		assertPair(t, filepath.Join(root, fmt.Sprintf("out-%d", index)), want)
	}
}

// Competing conversions of one name into one directory take turns: whichever finishes last, the pair
// is one result, never one's map with the other's manifest.
func TestConversionsIntoOneDirectoryTakeTurns(t *testing.T) {
	first, second, older, newer := fixtureResults(t)
	out := filepath.Join(t.TempDir(), "shared")
	var wait sync.WaitGroup
	for index := 0; index < 12; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			source := first
			if index%2 == 1 {
				source = second
			}
			var stdout, stderr bytes.Buffer
			if code := run(context.Background(), []string{"convert", "--apmap-to-q3map", "--input", source, "--output", out}, &stdout, &stderr); code != exitOK {
				t.Errorf("exit %d: %s", code, stderr.String())
			}
		}()
	}
	wait.Wait()
	got := readPair(t, out)
	if !describes(t, got) || (!bytes.Equal(got.mapText, older.mapText) && !bytes.Equal(got.mapText, newer.mapText)) {
		t.Fatal("the shared directory holds no one result")
	}
	assertHolds(t, out, roomManifest, roomMap)
}

func TestAConversionWaitsForTheLockForABoundedTime(t *testing.T) {
	out := t.TempDir()
	unlock, err := lockDirectory(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	saved := lockWait
	lockWait = 50 * time.Millisecond
	defer func() { lockWait = saved }()
	files := []resultFile{{Name: roomMap, Data: []byte("x")}}
	if err := commitResult(context.Background(), out, files); err == nil || !strings.Contains(err.Error(), "still publishing") {
		t.Fatalf("err = %v", err)
	}
	lockWait = time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := commitResult(ctx, out, files); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	assertHolds(t, out)
}
