package cli

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
