package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
)

// A hosted engine's output is kept only as far as its opening lines, however
// long the game runs, and handed over once.
func TestAnEngineOpeningIsBoundedAndTakenOnce(t *testing.T) {
	var h hostingState
	opening := &engineOpening{}
	h.keepOpening("job1", opening)

	_, _ = opening.Write([]byte("Initializing vkQuake 1.36.0\n"))
	for i := 0; i < 100; i++ {
		_, _ = opening.Write([]byte(strings.Repeat("x", 4096)))
	}
	if got := len(opening.String()); got != engineOpeningBytes {
		t.Fatalf("kept %d bytes, want the first %d", got, engineOpeningBytes)
	}
	if !strings.HasPrefix(opening.String(), "Initializing vkQuake 1.36.0\n") {
		t.Fatal("the opening lost its first line")
	}
	if h.takeOpening("job1") != opening || h.takeOpening("job1") != nil {
		t.Fatal("an opening must be handed over exactly once")
	}
}

// A version read from the program's own output is used for a later listing
// only while the program file is the one it was read from.
func TestARecordedEngineVersionIsDroppedWhenTheProgramChanges(t *testing.T) {
	dir := t.TempDir()
	s := &Server{}
	s.paths.Bindings = filepath.Join(dir, "bindings.json")
	exe := filepath.Join(dir, "vkquake")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(exe, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := binding.Update(s.paths.Bindings, func(set *binding.Set) error {
		return set.Put(binding.LocalBinding{
			ProfileID: "auto-pigeon.engine.vkquake", ProfileDigest: "sha256:" + strings.Repeat("0", 64),
			Executables:     map[string]string{"engine": exe},
			ResolvedVersion: "1.36.0", VersionCheckedAt: time.Now().UTC(),
		})
	}); err != nil {
		t.Fatal(err)
	}

	if got, ok := s.recordedEngineVersion("auto-pigeon.engine.vkquake", exe); !ok || got != "1.36.0" {
		t.Fatalf("recorded version = %q, %v; want 1.36.0", got, ok)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(exe, later, later); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.recordedEngineVersion("auto-pigeon.engine.vkquake", exe); ok {
		t.Fatalf("a program modified after its version was read is still listed as %q", got)
	}
}
