package tooluse

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 02_NEW_368: what replacing the bundle waits for. A parent's death is not
// the end of its compiler, and what cannot be read is not nothing.

func TestARecordThatCannotBeReadHoldsForAStalePeriod(t *testing.T) {
	root := t.TempDir()
	registry := open(t, root)
	gate, err := registry.Switch("companion", "update")
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Release()
	path := filepath.Join(registry.dir, "use-torn.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if holders, err := gate.Holders(); err != nil || len(holders) != 1 {
		t.Fatalf("a torn record: %+v %v", holders, err)
	}
	old := time.Now().Add(-2 * staleAfter)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if holders, err := gate.Holders(); err != nil || len(holders) != 0 {
		t.Fatalf("a record stale for a whole period still holds: %+v %v", holders, err)
	}
}

func TestACoordinationFolderThatCannotBeReadIsAnError(t *testing.T) {
	root := t.TempDir()
	registry := open(t, root)
	gate, err := registry.Switch("companion", "update")
	if err != nil {
		t.Fatal(err)
	}
	moved := registry.dir + ".moved"
	if err := os.Rename(registry.dir, moved); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Rename(moved, registry.dir)
		gate.Release()
	}()
	if holders, err := gate.Holders(); err == nil {
		t.Fatalf("nothing could be read and it says %+v", holders)
	}
}
