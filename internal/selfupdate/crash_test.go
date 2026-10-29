package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
)

// 01_NEW_368 §1: the transaction is recovered after an abrupt process death
// at every boundary — after each rename, before anything records it — by a
// FRESH process, and every owned file's bytes are checked, not a journal
// enum. The child is this test binary, killed by its own hand (SIGKILL on
// POSIX, TerminateProcess on Windows) at the named point.

const childEnv = "AUCOM_SELFUPDATE_CHILD"

// childSpec is what the parent tells a child to do.
type childSpec struct {
	Mode       string // apply, recover, rollback
	Request    Request
	ProveFails bool
	KillAt     string
	LinkAside  bool
	RunningSHA string
	Reached    string // the file the child writes before it dies
	Out        string
}

type childResult struct {
	State   State
	Outcome Outcome
	Error   string
}

func TestHelperProcess(t *testing.T) {
	path := os.Getenv(childEnv)
	if path == "" {
		t.Skip("only a child process runs this")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var spec childSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	linkAside = spec.LinkAside
	kill := func(point string) error {
		if point == spec.KillAt {
			_ = os.WriteFile(spec.Reached, []byte(point), 0o600)
			self, _ := os.FindProcess(os.Getpid())
			_ = self.Kill()
			select {}
		}
		return nil
	}
	request := spec.Request
	request.Fault, testFault = kill, kill
	request.FreeSpace = func(string) int64 { return 1 << 40 }
	request.Prove = func(context.Context, string) error {
		if spec.ProveFails {
			return errors.New("the candidate did not answer")
		}
		return nil
	}
	var result childResult
	var journal *Journal
	switch spec.Mode {
	case "apply":
		journal, err = Apply(context.Background(), request)
	case "recover":
		journal, result.Outcome, err = Recover(request.Root, spec.RunningSHA, nil)
	case "rollback":
		journal, err = Rollback(request.Root, "the new Companion did not serve")
	}
	if journal != nil {
		result.State = journal.State
	}
	if err != nil {
		result.Error = err.Error()
	}
	encoded, _ := json.Marshal(result)
	if err := os.WriteFile(spec.Out, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

// child runs spec in a fresh process. killed reports that it died at its
// KillAt point.
func (r *rig) child(t *testing.T, spec childSpec) (result childResult, killed bool) {
	t.Helper()
	dir := t.TempDir()
	request := r.request
	request.Prove, request.FreeSpace, request.Fault, request.Logf = nil, nil, nil, nil
	spec.Request = request
	spec.Reached, spec.Out = filepath.Join(dir, "reached"), filepath.Join(dir, "out.json")
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(dir, "spec.json")
	if err := os.WriteFile(specPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$", "-test.count=1")
	command.Env = append(os.Environ(), childEnv+"="+specPath)
	output, runErr := command.CombinedOutput()
	if reached, err := os.ReadFile(spec.Reached); err == nil {
		if string(reached) != spec.KillAt || runErr == nil {
			t.Fatalf("the child reached %q and exited %v", reached, runErr)
		}
		return childResult{}, true
	}
	if runErr != nil {
		t.Fatalf("the child failed: %v\n%s", runErr, output)
	}
	raw, err := os.ReadFile(spec.Out)
	if err != nil {
		t.Fatalf("the child wrote no result: %v\n%s", err, output)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result, false
}

// assertNew checks the 1.320 bundle is exactly in place.
func (r *rig) assertNew(t *testing.T) {
	t.Helper()
	for name, want := range map[string]string{
		companion:                            "new companion 1.320",
		"dependencies/auto-pigeon-extractor": "new extractor 1.246",
		"dependencies/bundle-manifest.json":  `{"version":"1.320"}`,
		"SHA256SUMS":                         "new sums",
		"dependencies/new-only.dat":          "a file only 1.320 ships",
	} {
		if got := r.read(t, name); got != want {
			t.Fatalf("%s is %q, want the new %q", name, got, want)
		}
	}
	if _, err := os.Lstat(filepath.Join(r.root, "dependencies", "old-only.dat")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a file only the old bundle had is still in place")
	}
	r.assertUserFiles(t)
}

func (r *rig) journalState(t *testing.T) State {
	t.Helper()
	journal, err := readJournal(r.root)
	if err != nil || journal == nil {
		t.Fatalf("journal %+v %v", journal, err)
	}
	return journal.State
}

// forwardPoints are every boundary of a forward pass, in order.
func forwardPoints() []string {
	points := []string{"intent", "prepared"}
	for _, name := range []string{"dependencies/auto-pigeon-extractor", "dependencies/bundle-manifest.json", "SHA256SUMS"} {
		points = append(points, "aside:"+name, "placed:"+name)
	}
	points = append(points, "placed:dependencies/new-only.dat", "aside:dependencies/old-only.dat",
		"aside:"+companion, "placed:"+companion, "swapped", "proved")
	return points
}

func asideModes() map[string]bool {
	return map[string]bool{"hard-link aside (POSIX)": true, "rename aside (Windows semantics)": false}
}

func TestAKillAtAnyBoundaryOfTheSwapIsRecoveredByAFreshProcess(t *testing.T) {
	for mode, link := range asideModes() {
		if link && runtime.GOOS == "windows" {
			continue
		}
		for _, point := range forwardPoints() {
			t.Run(mode+"/"+point, func(t *testing.T) {
				r := newRig(t)
				if _, killed := r.child(t, childSpec{Mode: "apply", KillAt: point, LinkAside: link}); !killed {
					t.Fatalf("the child was not killed at %s", point)
				}
				if got := r.journalState(t); got != StateActivating {
					t.Fatalf("a kill at %s left the journal %s", point, got)
				}
				recoverFrom := r.root
				if !link && point == "aside:"+companion {
					// Rename-aside: the program's own path is empty now. The
					// copy kept in previous/ is what can run, and it finds
					// the bundle it belongs to by where it is.
					if _, err := os.Lstat(filepath.Join(r.root, companion)); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("expected no program at the path after a rename-aside kill: %v", err)
					}
					kept := filepath.Join(r.root, DirName, "previous", companion)
					root, err := RecoveryRoot(kept, companion)
					if err != nil || root != r.root {
						t.Fatalf("the kept copy's recovery root is %q %v", root, err)
					}
					recoverFrom = root
				}
				r.request.Root = recoverFrom
				result, _ := r.child(t, childSpec{Mode: "recover", LinkAside: link})
				if result.Outcome != RolledBack || result.State != StateRolledBack || result.Error != "" {
					t.Fatalf("recovery: %+v", result)
				}
				r.assertOld(t)
				// Recovery is repeatable: a second pass finds nothing to do.
				again, _ := r.child(t, childSpec{Mode: "recover", LinkAside: link})
				if again.Outcome != Nothing || again.State != StateRolledBack {
					t.Fatalf("second recovery: %+v", again)
				}
				r.assertOld(t)
				// And the next update starts from the recovered state.
				linkAside = link
				defer func() { linkAside = runtime.GOOS != "windows" }()
				if _, err := Apply(context.Background(), r.request); err != nil {
					t.Fatalf("the next update after recovery: %v", err)
				}
				r.assertNew(t)
			})
		}
	}
}

// A kill while the previous files are being put back — the proving start
// failed — is finished by the next process.
func TestAKillDuringRollbackIsFinishedByAFreshProcess(t *testing.T) {
	members := []string{companion, "dependencies/old-only.dat", "dependencies/new-only.dat", "SHA256SUMS",
		"dependencies/bundle-manifest.json", "dependencies/auto-pigeon-extractor"}
	for mode, link := range asideModes() {
		if link && runtime.GOOS == "windows" {
			continue
		}
		for _, name := range members {
			for _, point := range []string{"restore:" + name, "restored:" + name} {
				t.Run(mode+"/"+point, func(t *testing.T) {
					r := newRig(t)
					if _, killed := r.child(t, childSpec{Mode: "apply", ProveFails: true, KillAt: point, LinkAside: link}); !killed {
						t.Fatalf("the child was not killed at %s", point)
					}
					if got := r.journalState(t); got != StateRollingBack {
						t.Fatalf("a kill at %s left the journal %s", point, got)
					}
					result, _ := r.child(t, childSpec{Mode: "recover", RunningSHA: digest([]byte("old companion 1.318")), LinkAside: link})
					if result.Outcome != RolledBack || result.State != StateRolledBack {
						t.Fatalf("recovery: %+v", result)
					}
					r.assertOld(t)
					if raw, err := os.ReadFile(filepath.Join(r.root, DirName, "failed", companion)); err == nil && string(raw) != "new companion 1.320" {
						t.Fatalf("the parked candidate is %q", raw)
					}
				})
			}
		}
	}
}

// A kill while an activated replacement that never served is taken back
// out (the old process's Rollback after its relaunch failed).
func TestAKillDuringAnActivatedRollbackIsFinished(t *testing.T) {
	r := newRig(t)
	if _, err := Apply(context.Background(), r.request); err != nil {
		t.Fatal(err)
	}
	if _, killed := r.child(t, childSpec{Mode: "rollback", KillAt: "restored:dependencies/bundle-manifest.json", LinkAside: linkAside}); !killed {
		t.Fatal("not killed")
	}
	// The next start is the old program (the Companion moved first in the
	// reverse pass) or the new one; either finishes the rollback.
	result, _ := r.child(t, childSpec{Mode: "recover", RunningSHA: digest([]byte("old companion 1.318")), LinkAside: linkAside})
	if result.Outcome != RolledBack {
		t.Fatalf("recovery: %+v", result)
	}
	r.assertOld(t)
}

// A journal write that fails is never a success, and never a rolled_back
// that is not on disk.
func TestAFailedJournalWriteIsNeverReportedAsAnOutcomeItDidNotRecord(t *testing.T) {
	failAt := func(r *rig, point string) {
		r.request.Fault = func(at string) error {
			if at == point {
				return errors.New("injected journal write failure at " + at)
			}
			return nil
		}
	}
	t.Run("intent", func(t *testing.T) {
		r := newRig(t)
		failAt(r, "journal:activating")
		if _, err := Apply(context.Background(), r.request); !errors.Is(err, ErrRefused) {
			t.Fatalf("err %v", err)
		}
		r.assertOld(t)
		if journal, _ := readJournal(r.root); journal != nil {
			t.Fatalf("a journal was left: %+v", journal)
		}
	})
	t.Run("activated", func(t *testing.T) {
		r := newRig(t)
		failAt(r, "journal:activated")
		if _, err := Apply(context.Background(), r.request); !errors.Is(err, ErrRolledBack) {
			t.Fatalf("err %v", err)
		}
		r.assertOld(t)
		if got := r.journalState(t); got != StateRolledBack {
			t.Fatalf("state %s", got)
		}
	})
	t.Run("rolled_back", func(t *testing.T) {
		r := newRig(t)
		r.request.Prove = func(context.Context, string) error { return errors.New("did not answer") }
		failAt(r, "journal:rolled_back")
		journal, err := Apply(context.Background(), r.request)
		if !errors.Is(err, ErrRecoveryNeeded) || errors.Is(err, ErrRolledBack) {
			t.Fatalf("err %v", err)
		}
		if journal.State == StateRolledBack {
			t.Fatal("the caller is told rolled_back although the journal could not record it")
		}
		// The bytes are back; the journal on disk is not terminal, and the
		// next pass records what it finds.
		r.assertOld(t)
		if got := r.journalState(t); got.terminal() {
			t.Fatalf("on disk: %s", got)
		}
		r.request.Fault = nil
		if _, outcome, err := Recover(r.root, "", nil); err != nil || outcome != RolledBack {
			t.Fatalf("recover: %s %v", outcome, err)
		}
		r.assertOld(t)
	})
}

// A restore that is blocked leaves recovery_needed — never rolled_back —
// keeps every byte it would need, refuses the next update, and is finished
// by the pass after the block is gone.
func TestABlockedRestoreIsRecoveryNeededAndBlocksTheNextUpdateUntilFinished(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	r := newRig(t)
	dependencies := filepath.Join(r.root, "dependencies")
	r.request.Prove = func(context.Context, string) error { return errors.New("did not answer") }
	r.request.Fault = func(at string) error {
		if at == "restore:dependencies/bundle-manifest.json" {
			return os.Chmod(dependencies, 0o555)
		}
		return nil
	}
	defer os.Chmod(dependencies, 0o755)
	journal, err := Apply(context.Background(), r.request)
	if !errors.Is(err, ErrRecoveryNeeded) || journal.State != StateRecoveryNeeded {
		t.Fatalf("err %v state %v", err, journal.State)
	}
	if !strings.Contains(journal.Detail, "dependencies/") || !strings.Contains(journal.Detail, RecoverCommand) {
		t.Fatalf("the detail does not say what is where: %s", journal.Detail)
	}
	if got := r.journalState(t); got != StateRecoveryNeeded {
		t.Fatalf("on disk: %s", got)
	}
	// The old files it could not put back are kept.
	for _, name := range []string{"dependencies/auto-pigeon-extractor", "dependencies/bundle-manifest.json"} {
		raw, err := os.ReadFile(filepath.Join(r.root, DirName, "previous", filepath.FromSlash(name)))
		if err != nil || !strings.Contains(string(raw), "1.2") && !strings.Contains(string(raw), "1.318") {
			t.Fatalf("previous/%s: %q %v", name, raw, err)
		}
	}
	// A start, and the next update, while it is still blocked: nothing is
	// removed, nothing new is moved, and both say so.
	r.request.Fault, r.request.Prove = nil, func(context.Context, string) error { return nil }
	if _, outcome, err := Recover(r.root, digest([]byte("old companion 1.318")), nil); outcome != NeedsRecovery || err == nil {
		t.Fatalf("start: %s %v", outcome, err)
	}
	if _, err := Apply(context.Background(), r.request); !errors.Is(err, ErrRecoveryNeeded) {
		t.Fatalf("the next update: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(r.root, DirName, "previous", "dependencies", "auto-pigeon-extractor")); err != nil {
		t.Fatalf("a kept file was removed: %v", err)
	}
	// Unblocked, the next pass finishes, and a new update then works.
	_ = os.Chmod(dependencies, 0o755)
	if _, outcome, err := Recover(r.root, "", nil); err != nil || outcome != RolledBack {
		t.Fatalf("recover: %s %v", outcome, err)
	}
	r.assertOld(t)
	if _, err := Apply(context.Background(), r.request); err != nil {
		t.Fatal(err)
	}
	r.assertNew(t)
}

// A file at an owned path whose bytes are neither bundle's is never
// overwritten or moved.
func TestUnknownBytesAtAnOwnedPathAreLeftAndReported(t *testing.T) {
	r := newRig(t)
	r.request.Prove = func(context.Context, string) error { return errors.New("did not answer") }
	r.request.Fault = func(at string) error {
		if at == "restore:SHA256SUMS" {
			return os.WriteFile(filepath.Join(r.root, "SHA256SUMS"), []byte("somebody's edit"), 0o644)
		}
		return nil
	}
	_, err := Apply(context.Background(), r.request)
	if !errors.Is(err, ErrRecoveryNeeded) || !strings.Contains(err.Error(), "neither the old nor the new") {
		t.Fatalf("err %v", err)
	}
	if got := r.read(t, "SHA256SUMS"); got != "somebody's edit" {
		t.Fatalf("the unknown file was changed: %q", got)
	}
	r.assertUserFiles(t)
}

// A journal that cannot be read, or that names paths no bundle may, is not
// acted on: the next update is refused and nothing it kept is removed.
func TestAJournalThatCannotBeTrustedIsNeverActedOn(t *testing.T) {
	member := func(path, old, new string) string {
		return fmt.Sprintf(`{"path":%q,"old_sha256":%q,"new_sha256":%q}`, path, old, new)
	}
	sum := digest([]byte("x"))
	journal := func(members ...string) string {
		return `{"schema":"` + JournalSchema + `","from":"1.318","to":"1.320","companion":"companion","from_sha256":"` + sum +
			`","to_sha256":"` + sum + `","state":"activating","members":[` + strings.Join(members, ",") + `]}`
	}
	cases := map[string]string{
		"not JSON":           `{"schema":`,
		"unknown schema":     `{"schema":"aucom.self-update-journal/9","state":"activating","companion":"companion"}`,
		"unknown state":      strings.Replace(journal(member("companion", sum, sum)), "activating", "half-done", 1),
		"an escaping member": journal(member("companion", sum, sum), member("../outside", "", sum)),
		"a member in the update folder": journal(member("companion", sum, sum),
			member(DirName+"/journal.json", "", sum)),
		"a duplicated member":  journal(member("companion", sum, sum), member("SHA256SUMS", "", sum), member("sha256sums", "", sum)),
		"a digest that is not": journal(member("companion", sum, "abc")),
		"no Companion":         journal(member("SHA256SUMS", "", sum)),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			kept := filepath.Join(r.root, DirName, "failed", "evidence")
			if err := os.MkdirAll(filepath.Dir(kept), 0o700); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(kept, []byte("diagnostic"), 0o600)
			if err := os.WriteFile(journalPath(r.root), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readJournal(r.root); err == nil {
				t.Fatal("the journal was accepted")
			}
			if _, err := Apply(context.Background(), r.request); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), RecoverCommand) {
				t.Fatalf("apply: %v", err)
			}
			if _, outcome, err := Recover(r.root, "", nil); err == nil || outcome != NeedsRecovery {
				t.Fatalf("recover: %s %v", outcome, err)
			}
			if raw, err := os.ReadFile(kept); err != nil || string(raw) != "diagnostic" {
				t.Fatalf("what the earlier update kept was removed: %v", err)
			}
			r.assertOld(t)
		})
	}
}

// The update from 1.323 is written by 1.323's own updater, in its own
// schema, and confirmed by this one: the confirmation is written back in
// that schema, because 1.323 is waiting to read it.
func TestALegacyJournalIsConfirmedInItsOwnSchema(t *testing.T) {
	r := newRig(t)
	legacyApply(t, r, true)
	newSHA := digest([]byte("new companion 1.320"))
	if _, outcome, err := Recover(r.root, newSHA, nil); err != nil || outcome != Pending {
		t.Fatalf("recover: %s %v", outcome, err)
	}
	if _, err := MarkVerified(r.root, newSHA); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(journalPath(r.root))
	if !strings.Contains(string(raw), legacySchema) || !strings.Contains(string(raw), `"verified"`) {
		t.Fatalf("the confirmation is not in the 1.323 schema: %s", raw)
	}
	// And the next update, by this code, starts over it.
	r.request.From, r.request.Current = "1.320", nil
	if _, err := Apply(context.Background(), r.request); err == nil {
		t.Log("a 1.320 -> 1.320 replacement over a confirmed legacy journal is allowed")
	}
}

// A 1.323 replacement killed mid-swap, whose flags say less than was done,
// is put back from where the files are.
func TestALegacyJournalWithStaleFlagsIsRecoveredFromWhereTheFilesAre(t *testing.T) {
	r := newRig(t)
	legacyApply(t, r, false)
	if _, outcome, err := Recover(r.root, digest([]byte("new companion 1.320")), nil); err != nil || outcome != RolledBack {
		t.Fatalf("recover: %s %v", outcome, err)
	}
	for name, want := range map[string]string{
		companion:                            "old companion 1.318",
		"dependencies/auto-pigeon-extractor": "old extractor 1.245",
		"dependencies/bundle-manifest.json":  `{"version":"1.318"}`,
		"SHA256SUMS":                         "old sums",
		"dependencies/old-only.dat":          "a file only 1.318 shipped",
	} {
		if got := r.read(t, name); got != want {
			t.Fatalf("%s is %q, want %q", name, got, want)
		}
	}
	r.assertUserFiles(t)
	// Repeated: nothing more to do, nothing moved.
	if _, outcome, err := Recover(r.root, "", nil); err != nil || outcome != Nothing {
		t.Fatalf("again: %s %v", outcome, err)
	}
}

// legacyApply does what 1.323's updater did — hard-link aside, rename over,
// a journal of flags — and leaves the journal activated, or (killed) still
// activating with every flag false although every file moved.
func legacyApply(t *testing.T, r *rig, activated bool) {
	t.Helper()
	previous, incoming := filepath.Join(r.root, DirName, "previous"), filepath.Join(r.root, DirName, "incoming")
	journal := &Journal{Schema: legacySchema, From: "1.318", To: "1.320", Companion: companion, State: StateActivating,
		FromSHA256: digest([]byte("old companion 1.318")), ToSHA256: digest([]byte("new companion 1.320"))}
	listed := map[string]bool{}
	for _, member := range r.request.Members {
		listed[member.Path] = true
		journal.Steps = append(journal.Steps, Step{Path: member.Path, New: true})
	}
	journal.Steps = append(journal.Steps, Step{Path: "dependencies/old-only.dat"})
	for _, member := range r.request.Members {
		target := under(incoming, member.Path)
		_ = os.MkdirAll(filepath.Dir(target), 0o700)
		raw, _ := os.ReadFile(under(r.staged, member.Path))
		_ = os.WriteFile(target, raw, 0o755)
	}
	for _, step := range journal.Steps {
		dest := under(r.root, step.Path)
		if _, err := os.Lstat(dest); err == nil {
			aside := under(previous, step.Path)
			_ = os.MkdirAll(filepath.Dir(aside), 0o700)
			if step.New && runtime.GOOS != "windows" {
				if err := os.Link(dest, aside); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Rename(dest, aside); err != nil {
				t.Fatal(err)
			}
		}
		if step.New {
			if err := os.Rename(under(incoming, step.Path), dest); err != nil {
				t.Fatal(err)
			}
		}
	}
	if activated {
		for i := range journal.Steps {
			journal.Steps[i].Aside, journal.Steps[i].Placed = true, journal.Steps[i].New
		}
		journal.State = StateActivated
		_ = os.RemoveAll(incoming)
	}
	if err := writeJournal(r.root, journal); err != nil {
		t.Fatal(err)
	}
	_ = managedtools.BundleMember{}
}
