package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/selfupdate"
)

func sumOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func writeMembers(t *testing.T, dir string, files map[string]string) []managedtools.BundleMember {
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
		members = append(members, managedtools.BundleMember{Path: name, SHA256: sumOf(body), Size: int64(len(body))})
	}
	return members
}

// 01_NEW_368 §1.1/§1.3: a start that finds the folder part way through an
// update it could not put back does not serve — not as the old version and
// not as the new — and says how to finish; once the cause is gone, the next
// start finishes the rollback and serves.
func TestAStartAfterAnUnfinishedRollbackRefusesToServeUntilRecovered(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	base := filepath.Join(t.TempDir(), "Apps ünï")
	root := filepath.Join(base, "auto-pigeon-companion-1.900-linux-amd64")
	staged := filepath.Join(base, "data", "tools", "companion", "1.901")
	current := writeMembers(t, root, map[string]string{
		"auto-pigeon-companion":          "old",
		"dependencies/auto-pigeon-mapio": "old aue",
	})
	members := writeMembers(t, staged, map[string]string{
		"auto-pigeon-companion":          "new",
		"dependencies/auto-pigeon-mapio": "new aue",
	})
	dependencies := filepath.Join(root, "dependencies")
	defer os.Chmod(dependencies, 0o755)
	_, err := selfupdate.Apply(context.Background(), selfupdate.Request{
		Root: root, Companion: "auto-pigeon-companion", From: "1.900", Current: current,
		To: "1.901", Staged: staged, StagedRoot: filepath.Join(base, "data", "tools"), Members: members,
		Prove:     func(context.Context, string) error { return errors.New("did not serve") },
		FreeSpace: func(string) int64 { return 1 << 40 },
		Fault: func(at string) error {
			if at == "restore:dependencies/auto-pigeon-mapio" {
				return os.Chmod(dependencies, 0o555)
			}
			return nil
		},
	})
	if !errors.Is(err, selfupdate.ErrRecoveryNeeded) {
		t.Fatalf("apply: %v", err)
	}
	self := managedtools.SelfInstall{Version: "1.900", Mode: managedtools.SelfPortable, Root: root,
		Executable: filepath.Join(root, "auto-pigeon-companion"), Members: current}
	log := &selfUpdateLog{path: filepath.Join(t.TempDir(), SelfUpdateLogName)}
	var results []managedtools.SelfResult
	record := func(result managedtools.SelfResult) { results = append(results, result) }

	confirm, blocked := recoverSelfAtStart(self, log, record)
	if blocked == nil || confirm != nil || !strings.Contains(blocked.Error(), selfupdate.RecoverCommand) {
		t.Fatalf("a start over an unfinished rollback was allowed: %v", blocked)
	}
	if len(results) != 1 || results[0].Outcome != "recovery_needed" {
		t.Fatalf("results %+v", results)
	}

	_ = os.Chmod(dependencies, 0o755)
	confirm, blocked = recoverSelfAtStart(self, log, record)
	if blocked != nil || confirm == nil {
		t.Fatalf("after the cause was fixed the start still refused: %v", blocked)
	}
	if results[len(results)-1].Outcome != "rolled_back" {
		t.Fatalf("results %+v", results)
	}
	for name, want := range map[string]string{"auto-pigeon-companion": "old", "dependencies/auto-pigeon-mapio": "old aue"} {
		if raw, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(name))); string(raw) != want {
			t.Fatalf("%s is %q", name, raw)
		}
	}
}

// recoveryBundle is a Companion folder at 1.900 and a verified 1.901 staged
// for it, with an Apply to run between them.
type recoveryBundle struct {
	root    string
	request selfupdate.Request
	self    managedtools.SelfInstall
}

func newRecoveryBundle(t *testing.T) *recoveryBundle {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "auto-pigeon-companion-1.900-linux-amd64")
	staged := filepath.Join(base, "data", "tools", "companion", "1.901")
	current := writeMembers(t, root, map[string]string{"auto-pigeon-companion": "old", "dependencies/auto-pigeon-mapio": "old aue"})
	members := writeMembers(t, staged, map[string]string{"auto-pigeon-companion": "new", "dependencies/auto-pigeon-mapio": "new aue"})
	return &recoveryBundle{
		root: root,
		request: selfupdate.Request{
			Root: root, Companion: "auto-pigeon-companion", From: "1.900", Current: current,
			To: "1.901", Staged: staged, StagedRoot: filepath.Join(base, "data", "tools"), Members: members,
			Prove:     func(context.Context, string) error { return nil },
			FreeSpace: func(string) int64 { return 1 << 40 },
		},
		// The program at the path now: the new one, once the swap has happened.
		self: managedtools.SelfInstall{Version: "1.901", Mode: managedtools.SelfPortable, Root: root,
			Executable: filepath.Join(root, "auto-pigeon-companion")},
	}
}

// killedAt applies the update and stops dead at a fault point: no rollback, no
// journal write after it, as a kill or a power cut leaves things.
func (b *recoveryBundle) killedAt(point string) {
	type killed struct{}
	defer func() {
		if recover() == nil {
			panic("the fault point " + point + " was not reached")
		}
	}()
	b.request.Fault = func(at string) error {
		if at == point {
			panic(killed{})
		}
		return nil
	}
	_, _ = selfupdate.Apply(context.Background(), b.request)
}

// serveOnce is one real `serve` of this bundle: what a person sees, what the
// update log keeps, and the exit code.
func (b *recoveryBundle) serveOnce(t *testing.T) (int, string, string) {
	t.Helper()
	defer func(saved func(*Env) managedtools.SelfInstall) { serveSelf = saved }(serveSelf)
	serveSelf = func(*Env) managedtools.SelfInstall { return b.self }
	var stdout, stderr bytes.Buffer
	config := filepath.Join(t.TempDir(), "config.json")
	code := Run(&Env{Stdout: &stdout, Stderr: &stderr, Version: b.self.Version, ConfigPath: config,
		Lookenv: func(string) (string, bool) { return "", false }, URIRegistrar: isolatedRegistrar(t)}, []string{"serve"})
	logged, err := os.ReadFile(filepath.Join(filepath.Dir(config), SelfUpdateLogName))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("exit %d; stderr: %s; update log: %s", code, stderr.String(), logged)
	return code, stderr.String(), string(logged)
}

// NEW_374 L6, seen on Windows: "taken back out after 0 starts that never
// became ready" of an update that was interrupted. The start says what the
// journal says happened, with the count.
func TestAStartSaysWhyTheUpdateWasTakenBackOut(t *testing.T) {
	cases := map[string]struct {
		arrange  func(t *testing.T, b *recoveryBundle)
		says     []string
		saysNot  []string
		recorded string
	}{
		"interrupted before a proving start": {
			arrange:  func(t *testing.T, b *recoveryBundle) { b.killedAt("swapped") },
			says:     []string{"after 0 recorded start(s)", "interrupted before it finished"},
			saysNot:  []string{"ready", "proving"},
			recorded: "interrupted before it finished",
		},
		"started three times and never served": {
			arrange: func(t *testing.T, b *recoveryBundle) {
				if _, err := selfupdate.Apply(context.Background(), b.request); err != nil {
					t.Fatal(err)
				}
				for start := 1; start < 3; start++ {
					if _, outcome, err := selfupdate.Recover(b.root, sumOf("new"), nil); err != nil || outcome != selfupdate.Pending {
						t.Fatalf("start %d: %s %v", start, outcome, err)
					}
				}
			},
			says:     []string{"after 3 recorded start(s)", "started 3 times without becoming ready"},
			saysNot:  []string{"interrupted"},
			recorded: "started 3 times without becoming ready",
		},
		"failed its proving start, and the rollback was interrupted": {
			arrange: func(t *testing.T, b *recoveryBundle) {
				b.request.Prove = func(context.Context, string) error { return errors.New("no answer on loopback") }
				b.killedAt("restore:auto-pigeon-companion")
			},
			says:     []string{"after 0 recorded start(s)", "did not pass its proving start: no answer on loopback"},
			saysNot:  []string{"interrupted", "never became ready"},
			recorded: "did not pass its proving start: no answer on loopback",
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			b := newRecoveryBundle(t)
			test.arrange(t, b)
			code, stderr, logged := b.serveOnce(t)
			if code != 1 {
				t.Fatalf("exit %d, stderr %s", code, stderr)
			}
			for _, want := range append(test.says, "version 1.901 was taken back out", "start the Companion again to run 1.900") {
				if !strings.Contains(stderr, want) {
					t.Errorf("the start does not say %q: %s", want, stderr)
				}
			}
			for _, wrong := range test.saysNot {
				if strings.Contains(stderr, wrong) {
					t.Errorf("the start says %q: %s", wrong, stderr)
				}
			}
			if !strings.Contains(logged, test.recorded) || !strings.Contains(logged, "1.900 -> 1.901 rolled back") {
				t.Errorf("the update log does not keep it: %s", logged)
			}
			if raw, _ := os.ReadFile(b.self.Executable); string(raw) != "old" {
				t.Errorf("the program is %q after the rollback", raw)
			}
		})
	}
}

// The copy an update keeps for rollback is never an installation to update.
func TestTheKeptCopyIsNotAnInstallation(t *testing.T) {
	root := t.TempDir()
	kept := filepath.Join(root, selfupdate.DirName, "previous", "auto-pigeon-companion")
	writeMembers(t, filepath.Dir(kept), map[string]string{"auto-pigeon-companion": "old"})
	self := managedtools.InspectSelf(kept, "1.900", "", "linux", "amd64")
	if self.Mode != managedtools.SelfDevelopment || !strings.Contains(self.Reason, "kept for rollback") {
		t.Fatalf("%+v", self)
	}
}
