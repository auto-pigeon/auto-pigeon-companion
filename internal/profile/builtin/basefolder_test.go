package builtin

import (
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// NEW_330: Build & Run names the game's own data folder from the engine's
// declaration, so every built-in engine must declare the one its game really
// uses — and an engine never declares another family's.
func TestEveryBuiltinEngineDeclaresItsGamesOwnDataFolder(t *testing.T) {
	entries, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"quake1": "id1", "quake2": "baseq2", "quake3": "baseq3"}
	engines := 0
	for _, entry := range entries {
		document, isEngine := entry.Profile.(*profile.EngineProfile)
		if !isEngine {
			continue
		}
		engines++
		family := document.GameProfile.EngineFamily
		if got := document.BaseFolder(); got != want[family] {
			t.Errorf("%s (%s) declares base folder %q, want %q", document.Meta.ID, family, got, want[family])
		}
	}
	if engines == 0 {
		t.Fatal("no built-in engine was checked")
	}
}
