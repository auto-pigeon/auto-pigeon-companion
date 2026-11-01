package job

import (
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/fsshare"
)

// holdExclusively opens path sharing nothing with anybody, as a tool opening
// its log without sharing or a scanner may.
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

// NEW_374 L7a, the path only Windows has: a log held for a moment is waited
// out, and one that stays held is said, after the bounded wait.
func TestALogHeldExclusivelyIsWaitedOutAndThenSaid(t *testing.T) {
	h, stale, source := endedRewriteJob(t, true)
	path := stale.SidecarLogs[0].Path

	held := holdExclusively(t, path)
	go func() {
		time.Sleep(fsshare.Patience / 4)
		_ = syscall.CloseHandle(held)
	}()
	chunk := &OutputChunk{}
	if raw, err := h.service.readSidecar(stale, source, 0, DefaultOutputChunk, chunk); err != nil || !strings.HasPrefix(string(raw), "second pass") {
		t.Fatalf("a log held for a moment reads %q, %v", raw, err)
	}

	held = holdExclusively(t, path)
	defer syscall.CloseHandle(held)
	began := time.Now()
	chunk = &OutputChunk{}
	if raw, err := h.service.readSidecar(stale, source, 0, DefaultOutputChunk, chunk); err == nil {
		t.Fatalf("a log nobody can open reads %q", raw)
	} else if waited := time.Since(began); waited < fsshare.Patience {
		t.Fatalf("it gave up after %s: %v", waited, err)
	}
}
