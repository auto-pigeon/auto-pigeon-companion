package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Q2_008: `holds_any` on a root is a short list of relative paths, refused on
// the workspace and refused when it could reach outside the folder.
func TestHoldsAnyIsValidatedAsRelativePathsInsideTheRoot(t *testing.T) {
	base := decodeFixture(t, "community/user-q1-toolchain.tool.json").(*ToolProfile)
	for _, c := range []struct {
		name    string
		role    string
		holds   []string
		refused string
	}{
		{"relative paths are fine", RootGame, []string{"pak0.pak", "pics/colormap.pcx"}, ""},
		{"an absolute path", RootGame, []string{"/etc/passwd"}, "relative path"},
		{"a parent step", RootGame, []string{"../baseq2/pak0.pak"}, "relative path"},
		{"a backslash", RootGame, []string{`pics\colormap.pcx`}, "relative path"},
		{"an empty entry", RootGame, []string{""}, "empty"},
		{"on the workspace", RootWorkspace, []string{"pak0.pak"}, "workspace"},
		{"too many", RootGame, []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}, "at most 8"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tree, err := json.Marshal(base)
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(tree, &document); err != nil {
				t.Fatal(err)
			}
			action := document["actions"].([]any)[0].(map[string]any)
			action["roots"] = []any{
				map[string]any{"role": RootWorkspace, "access": "read_write", "purpose": "the job's own folder"},
				map[string]any{"role": c.role, "access": "read", "holds_any": c.holds},
			}
			if c.role == RootWorkspace {
				action["roots"] = []any{map[string]any{"role": RootWorkspace, "access": "read_write", "purpose": "the job's own folder", "holds_any": c.holds}}
			}
			data, _ := json.Marshal(document)
			_, err = Decode(data)
			switch {
			case c.refused == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case c.refused != "" && (err == nil || !strings.Contains(err.Error(), c.refused)):
				t.Fatalf("got %v, want a refusal mentioning %q", err, c.refused)
			}
		})
	}
}

func TestHoldsAnyIsAnsweredByTheFolderItself(t *testing.T) {
	dir := t.TempDir()
	ref := RootRef{Role: RootGame, Access: AccessRead, HoldsAny: []string{"pak0.pak", "pics/colormap.pcx"}}
	stat := func(p string) error { _, err := os.Stat(p); return err }
	if _, holds := ref.Holds(dir, stat); holds {
		t.Fatal("an empty folder holds the game")
	}
	if err := os.MkdirAll(filepath.Join(dir, "pics"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pics", "colormap.pcx"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if found, holds := ref.Holds(dir, stat); !holds || found != "pics/colormap.pcx" {
		t.Fatalf("holds %q %v", found, holds)
	}
	if _, holds := (RootRef{Role: RootGame}).Holds(filepath.Join(dir, "nowhere"), stat); !holds {
		t.Fatal("a root that names nothing must hold what it needs")
	}
}
