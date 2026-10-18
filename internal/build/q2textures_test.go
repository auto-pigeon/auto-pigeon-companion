package build

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// q2Steps is one compile step whose action reads the base game and a content
// folder, as EricW in Quake II mode does.
func q2Steps() []profile.ResolvedStep {
	return []profile.ResolvedStep{{
		Step:    profile.PipelineStep{ID: "compile"},
		Profile: &profile.ToolProfile{Meta: profile.Meta{ID: "test.q2-tool"}},
		Action: profile.Action{ID: "compile", Roots: []profile.RootRef{
			{Role: profile.RootGame, Access: profile.AccessRead},
			{Role: profile.RootContent, Access: profile.AccessRead},
		}},
	}}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// treeDigest is every file under root, by path, with its bytes' digest — what
// "the user's folder was not changed" is checked against.
func treeDigest(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		rel, _ := filepath.Rel(root, path)
		lines = append(lines, filepath.ToSlash(rel)+" "+hex.EncodeToString(sum[:]))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func digestOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// q2Export is a verified export's content directory with two account WALs,
// one in a mixed-case namespace AUB admits.
func q2Export(t *testing.T, dir string) *Q2Textures {
	t.Helper()
	files := map[string]string{
		"aup687697486/red_7a099681.wal": "account red",
		"AupMixed/Blue_Case.wal":        "account blue",
	}
	writeTree(t, filepath.Join(dir, "content"), files)
	writeTree(t, dir, map[string]string{"LICENSES.md": "# Licences\n"})
	textures := &Q2Textures{
		Bundle:   &BundleRef{Schema: "aub-map-texture-export/1.2", MapID: "map1", Revision: 5, Digest: strings.Repeat("b", 64)},
		Dir:      filepath.Join(dir, "content"),
		Licenses: filepath.Join(dir, "LICENSES.md"),
	}
	for _, name := range []string{"aup687697486/red_7a099681.wal", "AupMixed/Blue_Case.wal"} {
		textures.Files = append(textures.Files, BundleFile{Path: name, Source: strings.Split(name, "/")[0],
			SHA256: digestOf(files[name]), Bytes: int64(len(files[name]))})
	}
	textures.Bundle.Files = textures.Files
	return textures
}

// The account textures are laid over the content folder, win a path both hold,
// leave everything else of the content folder visible, and change nothing in
// the user's own folder; the manifest says what the root was made of.
func TestQuake2AccountTexturesAreLaidOverTheContentFolder(t *testing.T) {
	dir := t.TempDir()
	content := filepath.Join(dir, "my content")
	writeTree(t, content, map[string]string{
		"textures/mine/own.wal":                  "my own",
		"textures/aup687697486/red_7a099681.wal": "an older copy of mine",
		"textures/aup687697486/extra.wal":        "only mine",
		"sound/world/amb1.wav":                   "a sound",
	})
	before := treeDigest(t, content)
	textures := q2Export(t, filepath.Join(dir, "cache"))
	layout, err := createLayout(filepath.Join(dir, "build"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := &Manifest{EngineFamily: "quake2", Roots: []RootRecord{{Role: profile.RootContent, Path: content}}}
	runner := &Runner{}
	staged, err := runner.stageQ2Textures(Request{Q2Textures: textures,
		Roots: map[string]string{profile.RootContent: content}}, q2Steps(), layout, manifest)
	if err != nil {
		t.Fatal(err)
	}
	gamedir := staged.Roots[profile.RootContent]
	if gamedir == content || !strings.HasPrefix(gamedir, layout.Dir) {
		t.Fatalf("the compiler's content root is %s; it must be the build's own directory", gamedir)
	}
	for path, want := range map[string]string{
		"textures/aup687697486/red_7a099681.wal": "account red", // the account's revision wins
		"textures/AupMixed/Blue_Case.wal":        "account blue",
		"textures/aup687697486/extra.wal":        "only mine",
		"textures/mine/own.wal":                  "my own",
		"sound/world/amb1.wav":                   "a sound",
	} {
		data, err := os.ReadFile(filepath.Join(gamedir, filepath.FromSlash(path)))
		if err != nil || string(data) != want {
			t.Errorf("%s in the compiler's game directory: %q, %v; want %q", path, data, err, want)
		}
	}
	if after := treeDigest(t, content); after != before {
		t.Errorf("the content folder changed:\nbefore\n%s\nafter\n%s", before, after)
	}
	if len(manifest.Roots) != 1 {
		t.Fatalf("roots = %+v", manifest.Roots)
	}
	root := manifest.Roots[0]
	if root.Path != gamedir || root.Underlay != content || root.Source == nil ||
		root.Source.Kind != RootFromQ2TextureOverlay || root.Source.Bundle != textures.Bundle ||
		root.Overlay != filepath.Join(layout.Dir, q2TexturesDirName, "overlay") {
		t.Errorf("the manifest's content root = %+v", root)
	}
	if source := staged.RootSources[profile.RootContent]; source.Kind != RootFromQ2TextureOverlay {
		t.Errorf("root source = %+v", source)
	}
	if data, err := os.ReadFile(filepath.Join(layout.Dir, q2TexturesDirName, Q2LicensesName)); err != nil || string(data) != "# Licences\n" {
		t.Errorf("the export's licences beside the overlay: %q, %v", data, err)
	}
	// Removing the build removes the links and never what they point at.
	if err := os.RemoveAll(layout.Dir); err != nil {
		t.Fatal(err)
	}
	if after := treeDigest(t, content); after != before {
		t.Error("removing the build changed the content folder")
	}
}

// With no content folder bound, the overlay itself is the game directory.
func TestQuake2AccountTexturesWithoutAContentFolder(t *testing.T) {
	dir := t.TempDir()
	textures := q2Export(t, filepath.Join(dir, "cache"))
	layout, err := createLayout(filepath.Join(dir, "build"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := &Manifest{EngineFamily: "quake2"}
	staged, err := (&Runner{}).stageQ2Textures(Request{Q2Textures: textures}, q2Steps(), layout, manifest)
	if err != nil {
		t.Fatal(err)
	}
	gamedir := staged.Roots[profile.RootContent]
	if data, err := os.ReadFile(filepath.Join(gamedir, "textures", "aup687697486", "red_7a099681.wal")); err != nil || string(data) != "account red" {
		t.Errorf("%q, %v", data, err)
	}
	if len(manifest.Roots) != 1 || manifest.Roots[0].Underlay != "" || manifest.Roots[0].Overlay != gamedir {
		t.Errorf("roots = %+v", manifest.Roots)
	}
}

// A file that is not the one the export verified is refused, and so is a
// build whose steps read no content folder or that is not a Quake II build.
func TestQuake2AccountTexturesAreRefusedWhenTheyCannotBeTrusted(t *testing.T) {
	dir := t.TempDir()
	textures := q2Export(t, filepath.Join(dir, "cache"))
	changed := *textures
	changed.Files = append([]BundleFile(nil), textures.Files...)
	changed.Files[0].SHA256 = strings.Repeat("0", 64)
	escaping := *textures
	escaping.Files = []BundleFile{{Path: "../outside.wal", SHA256: strings.Repeat("0", 64), Bytes: 1}}
	noContent := q2Steps()
	noContent[0].Action.Roots = noContent[0].Action.Roots[:1]
	for name, tc := range map[string]struct {
		textures *Q2Textures
		family   string
		steps    []profile.ResolvedStep
		want     string
	}{
		"digest mismatch":   {&changed, "quake2", q2Steps(), "is not the file the export verified"},
		"escaping path":     {&escaping, "quake2", q2Steps(), "not a plain path"},
		"no content folder": {textures, "quake2", noContent, "reads a content folder"},
		"not Quake II":      {textures, "quake1", q2Steps(), "Quake II"},
	} {
		t.Run(name, func(t *testing.T) {
			layout, err := createLayout(filepath.Join(t.TempDir(), "build"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = (&Runner{}).stageQ2Textures(Request{Q2Textures: tc.textures}, tc.steps, layout,
				&Manifest{EngineFamily: tc.family})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if class := failure.Of(err); class != failure.ContentRefused {
				t.Errorf("failure class = %q", class)
			}
			if _, statErr := os.Stat(filepath.Join(filepath.Dir(layout.Dir), "outside.wal")); !errors.Is(statErr, os.ErrNotExist) {
				t.Error("a file was written outside the build")
			}
		})
	}
}
