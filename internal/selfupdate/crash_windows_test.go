package selfupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// holdExclusively opens path sharing nothing with anybody, as a scanner or
// another program may: on Windows that is what stops a rename, where POSIX
// permissions cannot be used.
func holdExclusively(t *testing.T, path string) syscall.Handle {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("holding %s: %v", path, err)
	}
	return handle
}

// The Windows form of TestABlockedRestoreIsRecoveryNeededAndBlocksTheNextUpdateUntilFinished,
// which needs POSIX permissions: a kept file another program holds open cannot
// be moved back (02_NEW_368).
func TestAKeptFileHeldOpenIsRecoveryNeededAndBlocksTheNextUpdateUntilFinished(t *testing.T) {
	r := newRig(t)
	kept := func(name string) string { return filepath.Join(r.root, DirName, "previous", filepath.FromSlash(name)) }
	var held []syscall.Handle
	release := func() {
		for _, handle := range held {
			_ = syscall.CloseHandle(handle)
		}
		held = nil
	}
	defer release()
	r.request.Prove = func(context.Context, string) error { return errors.New("did not answer") }
	r.request.Fault = func(at string) error {
		if at == "restore:dependencies/bundle-manifest.json" {
			held = append(held,
				holdExclusively(t, kept("dependencies/bundle-manifest.json")),
				holdExclusively(t, kept("dependencies/auto-pigeon-mapio")))
		}
		return nil
	}
	journal, err := Apply(context.Background(), r.request)
	if !errors.Is(err, ErrRecoveryNeeded) || journal.State != StateRecoveryNeeded {
		t.Fatalf("err %v journal %+v", err, journal)
	}
	if !strings.Contains(journal.Detail, RecoverCommand) {
		t.Fatalf("the detail does not say what to do: %s", journal.Detail)
	}
	if got := r.journalState(t); got != StateRecoveryNeeded {
		t.Fatalf("on disk: %s", got)
	}
	r.request.Fault, r.request.Prove = nil, func(context.Context, string) error { return nil }
	if _, outcome, err := Recover(r.root, digest([]byte("old companion 1.318")), nil); outcome != NeedsRecovery || err == nil {
		t.Fatalf("a start while it is held: %s %v", outcome, err)
	}
	if _, err := Apply(context.Background(), r.request); !errors.Is(err, ErrRecoveryNeeded) {
		t.Fatalf("the next update: %v", err)
	}
	for _, name := range []string{"dependencies/auto-pigeon-mapio", "dependencies/bundle-manifest.json"} {
		if _, err := os.Lstat(kept(name)); err != nil {
			t.Fatalf("a kept file was removed: %v", err)
		}
	}
	release()
	if _, outcome, err := Recover(r.root, "", nil); err != nil || outcome != RolledBack {
		t.Fatalf("recover: %s %v", outcome, err)
	}
	r.assertOld(t)
	if _, err := Apply(context.Background(), r.request); err != nil {
		t.Fatal(err)
	}
	r.assertNew(t)
}
