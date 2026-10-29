package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/lockfile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
)

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// bundle writes files under dir and returns them as members.
func bundle(t *testing.T, dir string, files map[string]string) []managedtools.BundleMember {
	t.Helper()
	var members []managedtools.BundleMember
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		members = append(members, managedtools.BundleMember{Path: name, SHA256: digest([]byte(body)), Size: int64(len(body)),
			Executable: !strings.HasSuffix(name, ".json") && name != "SHA256SUMS"})
	}
	return members
}

type rig struct {
	root, tools, staged string
	request             Request
	userFiles           map[string]string
}

const companion = "companion"

// newRig is an installed 1.318 bundle with a person's own files beside it,
// and a verified 1.320 bundle staged in the tools folder. The paths have
// spaces and non-ASCII letters in them.
func newRig(t *testing.T) *rig {
	t.Helper()
	base := filepath.Join(t.TempDir(), "Auto Pigeon — prova ünï")
	r := &rig{
		root:   filepath.Join(base, "auto-pigeon-companion-1.318-linux-amd64"),
		tools:  filepath.Join(base, "data", "tools"),
		staged: filepath.Join(base, "data", "tools", "companion", "1.320-abcdef12"),
		userFiles: map[string]string{
			"config.json":             `{"aub_base_url":"https://beta.example"}`,
			"my notes.txt":            "keep me",
			"dependencies/custom.lua": "a person's own file in the dependencies folder",
		},
	}
	current := bundle(t, r.root, map[string]string{
		companion:                            "old companion 1.318",
		"dependencies/auto-pigeon-extractor": "old extractor 1.245",
		"dependencies/bundle-manifest.json":  `{"version":"1.318"}`,
		"SHA256SUMS":                         "old sums",
		"dependencies/old-only.dat":          "a file only 1.318 shipped",
	})
	for name, body := range r.userFiles {
		path := filepath.Join(r.root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	members := bundle(t, r.staged, map[string]string{
		companion:                            "new companion 1.320",
		"dependencies/auto-pigeon-extractor": "new extractor 1.246",
		"dependencies/bundle-manifest.json":  `{"version":"1.320"}`,
		"SHA256SUMS":                         "new sums",
		"dependencies/new-only.dat":          "a file only 1.320 ships",
	})
	r.request = Request{
		Root: r.root, Companion: companion, From: "1.318", Current: current,
		To: "1.320", Staged: r.staged, StagedRoot: r.tools, Members: members,
		Prove:     func(context.Context, string) error { return nil },
		FreeSpace: func(string) int64 { return 1 << 40 },
	}
	return r
}

func (r *rig) read(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(name)))
	if err != nil {
		return "<missing: " + err.Error() + ">"
	}
	return string(raw)
}

// assertOld checks the 1.318 bundle is exactly as it was, and the person's
// files with it.
func (r *rig) assertOld(t *testing.T) {
	t.Helper()
	for name, want := range map[string]string{
		companion:                            "old companion 1.318",
		"dependencies/auto-pigeon-extractor": "old extractor 1.245",
		"dependencies/bundle-manifest.json":  `{"version":"1.318"}`,
		"SHA256SUMS":                         "old sums",
		"dependencies/old-only.dat":          "a file only 1.318 shipped",
	} {
		if got := r.read(t, name); got != want {
			t.Fatalf("%s is %q, want the old %q", name, got, want)
		}
	}
	if _, err := os.Lstat(filepath.Join(r.root, "dependencies", "new-only.dat")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a file only the new bundle has is still in place after a rollback")
	}
	r.assertUserFiles(t)
}

func (r *rig) assertUserFiles(t *testing.T) {
	t.Helper()
	for name, want := range r.userFiles {
		if got := r.read(t, name); got != want {
			t.Fatalf("a person's file %s is %q, want %q", name, got, want)
		}
	}
}

// S2/S6: a valid bundle replaces exactly the files the bundles list, keeps
// the old ones for rollback, leaves a person's files alone, and is complete
// only when the new Companion confirms it.
func TestAValidBundleReplacesOnlyItsOwnFilesAndIsConfirmedByTheNewCompanion(t *testing.T) {
	r := newRig(t)
	var proved string
	r.request.Prove = func(_ context.Context, exe string) error { proved = exe; return nil }
	journal, err := Apply(context.Background(), r.request)
	if err != nil {
		t.Fatal(err)
	}
	if journal.State != StateActivated || proved != filepath.Join(r.root, companion) {
		t.Fatalf("state %s, proved %q", journal.State, proved)
	}
	for name, want := range map[string]string{
		companion:                            "new companion 1.320",
		"dependencies/auto-pigeon-extractor": "new extractor 1.246",
		"dependencies/bundle-manifest.json":  `{"version":"1.320"}`,
		"SHA256SUMS":                         "new sums",
	} {
		if got := r.read(t, name); got != want {
			t.Fatalf("%s is %q, want %q", name, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(r.root, "dependencies", "old-only.dat")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a file only the old bundle listed is still in place")
	}
	r.assertUserFiles(t)
	if raw, _ := os.ReadFile(filepath.Join(r.root, DirName, "previous", companion)); string(raw) != "old companion 1.318" {
		t.Fatalf("the previous Companion is not kept: %q", raw)
	}
	// POSIX permissions: Windows reports its own mode bits for any file.
	if info, err := os.Stat(journalPath(r.root)); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("journal %v %v", info, err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(filepath.Join(r.root, companion)); info.Mode().Perm()&0o111 == 0 {
			t.Fatal("the new Companion is not executable")
		}
	}
	// The old one, still running, finds nothing to confirm; the new one does.
	newSHA := digest([]byte("new companion 1.320"))
	if _, outcome, err := Recover(r.root, digest([]byte("old companion 1.318")), nil); err != nil || outcome != Nothing {
		t.Fatalf("old side: %s %v", outcome, err)
	}
	if _, outcome, err := Recover(r.root, newSHA, nil); err != nil || outcome != Pending {
		t.Fatalf("new side: %s %v", outcome, err)
	}
	confirmed, err := MarkVerified(r.root, newSHA)
	if err != nil || confirmed.State != StateVerified {
		t.Fatalf("confirm: %+v %v", confirmed, err)
	}
}

// S5: a candidate that fails its proving start is taken back out, and the
// old bundle is byte for byte what it was.
func TestACandidateThatDoesNotProveIsRolledBack(t *testing.T) {
	r := newRig(t)
	r.request.Prove = func(context.Context, string) error { return errors.New("it did not answer on loopback") }
	journal, err := Apply(context.Background(), r.request)
	if !errors.Is(err, ErrRolledBack) || journal.State != StateRolledBack || !strings.Contains(journal.Detail, "loopback") {
		t.Fatalf("err %v journal %+v", err, journal)
	}
	r.assertOld(t)
	if raw, _ := os.ReadFile(filepath.Join(r.root, DirName, "failed", companion)); runtime.GOOS == "windows" && string(raw) != "new companion 1.320" {
		t.Fatalf("the failed candidate is not parked: %q", raw)
	}
}

// S5: a failure at every step of the swap — after each file is set aside,
// after each is put in place — leaves the old bundle exactly as it was.
func TestAFailureAtAnyStepOfTheSwapLeavesTheOldBundle(t *testing.T) {
	points := []string{"prepared"}
	for _, name := range []string{"dependencies/auto-pigeon-extractor", "dependencies/bundle-manifest.json", "SHA256SUMS", companion} {
		points = append(points, "aside:"+name, "placed:"+name)
	}
	points = append(points, "aside:dependencies/old-only.dat")
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			r := newRig(t)
			r.request.Fault = func(at string) error {
				if at == point {
					return errors.New("injected at " + at)
				}
				return nil
			}
			_, err := Apply(context.Background(), r.request)
			if err == nil {
				t.Fatal("no error")
			}
			r.assertOld(t)
		})
	}
}

// S5: a replacement interrupted by a kill or a power cut — the journal says
// "activating" — is rolled back by the next start, whichever side it is.
func TestAnInterruptedSwapIsRolledBackAtTheNextStart(t *testing.T) {
	r := newRig(t)
	// Do the first steps by hand, as a killed process would have left them.
	r.request.Fault = func(at string) error {
		if at == "placed:SHA256SUMS" {
			panic("killed")
		}
		return nil
	}
	func() {
		defer func() { _ = recover() }()
		_, _ = Apply(context.Background(), r.request)
	}()
	journal, err := readJournal(r.root)
	if err != nil || journal == nil || journal.State != StateActivating {
		t.Fatalf("the simulated kill left %+v %v", journal, err)
	}
	// The lock of the killed process is released (its process is gone).
	_, outcome, err := Recover(r.root, digest([]byte("old companion 1.318")), nil)
	if err != nil || outcome != RolledBack {
		t.Fatalf("recover: %s %v", outcome, err)
	}
	r.assertOld(t)
	// And a new attempt afterwards succeeds from a clean state.
	r.request.Fault = nil
	if _, err := Apply(context.Background(), r.request); err != nil {
		t.Fatal(err)
	}
}

// S5: a new Companion that keeps starting without ever serving is taken back
// out on its third start, so it cannot stay in place.
func TestACandidateThatNeverBecomesReadyIsRolledBackAfterThreeStarts(t *testing.T) {
	r := newRig(t)
	if _, err := Apply(context.Background(), r.request); err != nil {
		t.Fatal(err)
	}
	newSHA := digest([]byte("new companion 1.320"))
	for i := 1; i < maxUnreadyStarts; i++ {
		if _, outcome, err := Recover(r.root, newSHA, nil); err != nil || outcome != Pending {
			t.Fatalf("start %d: %s %v", i, outcome, err)
		}
	}
	journal, outcome, err := Recover(r.root, newSHA, nil)
	if err != nil || outcome != RolledBack || !strings.Contains(journal.Detail, "without becoming ready") {
		t.Fatalf("third start: %s %v %+v", outcome, err, journal)
	}
	r.assertOld(t)
}

// S2/S5: everything is refused before a file moves — a staged copy changed
// after verification, a link where a program should be, a staged copy
// outside the tools folder, a member path that escapes, a full disk, a
// second replacement at once.
func TestEveryUnsafeReplacementIsRefusedBeforeAFileMoves(t *testing.T) {
	cases := map[string]func(t *testing.T, r *rig){
		"staged copy changed": func(t *testing.T, r *rig) {
			_ = os.WriteFile(filepath.Join(r.staged, companion), []byte("new companion 1.32X"), 0o755)
		},
		"a link in the bundle": func(t *testing.T, r *rig) {
			target := filepath.Join(r.root, "dependencies", "auto-pigeon-extractor")
			_ = os.Remove(target)
			if err := os.Symlink("/bin/sh", target); err != nil {
				t.Skip("no symlinks here")
			}
		},
		"staged outside the tools folder": func(t *testing.T, r *rig) {
			r.request.StagedRoot = filepath.Join(r.tools, "elsewhere")
		},
		"a member that escapes": func(t *testing.T, r *rig) {
			r.request.Members = append(r.request.Members, managedtools.BundleMember{Path: "../outside", SHA256: digest(nil)})
		},
		"a member in the update folder": func(t *testing.T, r *rig) {
			r.request.Members = append(r.request.Members, managedtools.BundleMember{Path: DirName + "/journal.json", SHA256: digest(nil)})
		},
		"a full disk": func(t *testing.T, r *rig) {
			r.request.FreeSpace = func(string) int64 { return 1 << 20 }
		},
		"another replacement at once": func(t *testing.T, r *rig) {
			_ = os.MkdirAll(dirOf(r.root), 0o700)
			held, err := lockfile.Acquire(lockPath(r.root), lockfile.Options{Program: "another replacement"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = held.Release() })
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			change(t, r)
			_, err := Apply(context.Background(), r.request)
			if !errors.Is(err, ErrRefused) {
				t.Fatalf("err %v; want a refusal", err)
			}
			if name == "a link in the bundle" {
				return
			}
			r.assertOld(t)
		})
	}
}

// An unwritable bundle folder refuses, and the old bundle stays runnable.
func TestAnUnwritableBundleFolderIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	r := newRig(t)
	if err := os.Chmod(r.root, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(r.root, 0o755)
	if _, err := Apply(context.Background(), r.request); !errors.Is(err, ErrRefused) {
		t.Fatalf("err %v", err)
	}
	r.assertOld(t)
}

// A journal that is not this user's plain file is never read.
func TestAJournalThatIsALinkIsNotRead(t *testing.T) {
	r := newRig(t)
	_ = os.MkdirAll(dirOf(r.root), 0o700)
	elsewhere := filepath.Join(t.TempDir(), "journal.json")
	_ = os.WriteFile(elsewhere, []byte(`{"schema":"`+JournalSchema+`","state":"activating"}`), 0o600)
	if err := os.Symlink(elsewhere, journalPath(r.root)); err != nil {
		t.Skip("no symlinks here")
	}
	if _, err := readJournal(r.root); err == nil {
		t.Fatal("a linked journal was read")
	}
}
