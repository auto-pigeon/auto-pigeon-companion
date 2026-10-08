package web

import (
	"io/fs"
	"strings"
	"testing"
)

// The Build page's "Texture WAD" row asks for a FOLDER (`source_kind` textures,
// AUCOM/AUT 246I) and the server takes a folder as a root — `roots.content_root`,
// checked by resolveRoots — while every `inputs` entry is one FILE. The page sent
// the folder as `inputs.wad` all the same: the check answered "Everything is in
// place", and Build then failed with `the input "wad": … is not a regular file`
// (NEW_322, building beta1.coarse4 with its four WADs). The page is the only
// caller that can put the folder in the right half of the request, so this
// reads what it sends.
func TestBuildPageSendsTheTextureFolderAsARootNotAnInput(t *testing.T) {
	source, err := fs.ReadFile(assetsFS(), "build.js")
	if err != nil {
		t.Fatalf("%v", err)
	}
	start := strings.Index(string(source), "function requestBody()")
	if start < 0 {
		t.Fatal("build.js has no requestBody(): this test reads the request the Build page sends")
	}
	body := string(source)[start:]
	if end := strings.Index(body, "\n  }\n"); end > 0 {
		body = body[:end]
	}
	for _, want := range []string{
		`if (row.kind === "textures") roots.content_root = row.file.input.value.trim();`,
		`roots: Object.keys(roots).length ? roots : undefined,`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("requestBody() does not send a textures folder as the build's content_root; missing:\n%s", want)
		}
	}
	// And never both ways: the folder must not also be written into `inputs`.
	if strings.Contains(body, "} else if (row.file.input.value.trim()) {\n        inputs[name] = row.file.input.value.trim();") {
		t.Error("requestBody() still writes every local path into `inputs`, a folder included")
	}
}
