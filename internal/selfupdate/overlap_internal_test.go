package selfupdate

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
)

// NEW_508C: a portable bundle keeps its build tools — and stages its own
// update — in <bundle>/build-tools. That folder is inside the bundle's folder
// and is no member of it, so the swap never touches it; it was refused as an
// overlap, and every portable self-update failed and put the old version back.
// A staged copy under a member's folder, under the journal's folder, or
// holding the bundle, is still refused.
func TestAStagedCopyBesideTheMembersInsideTheBundleIsAccepted(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Auto Pigeon Companion 1.900")
	members := []managedtools.BundleMember{
		{Path: "companion", SHA256: strings.Repeat("a", 64)},
		{Path: "dependencies/auto-pigeon-mapio", SHA256: strings.Repeat("b", 64)},
		{Path: "dependencies/bundle-manifest.json", SHA256: strings.Repeat("c", 64)},
		{Path: "SHA256SUMS", SHA256: strings.Repeat("d", 64)},
	}
	request := func(toolsRoot, staged string) Request {
		return Request{Root: root, Companion: "companion", From: "1.900", To: "1.901", Current: members, Members: members,
			StagedRoot: toolsRoot, Staged: staged}
	}
	beside := filepath.Join(root, managedtools.BuildToolsDirName)
	cases := []struct {
		name      string
		toolsRoot string
		staged    string
		refused   bool
	}{
		{"in build-tools beside the members", beside, filepath.Join(beside, "companion", "1.901-7fb8f98d"), false},
		{"in a tools folder outside the bundle", filepath.Join(t.TempDir(), "tools"), filepath.Join(t.TempDir(), "tools", "companion", "1.901"), true}, // not in its tools folder
		{"under a member's folder", filepath.Join(root, "dependencies"), filepath.Join(root, "dependencies", "x", "1.901"), true},
		{"under the journal's folder", filepath.Join(root, DirName), filepath.Join(root, DirName, "x", "1.901"), true},
		{"holding the bundle", filepath.Dir(root), filepath.Dir(root), true},
	}
	for _, c := range cases {
		err := request(c.toolsRoot, c.staged).validateShape()
		overlap := err != nil
		if overlap != c.refused {
			t.Errorf("%s: %v; refused=%v wanted", c.name, err, c.refused)
		}
	}
	outside := filepath.Join(t.TempDir(), "tools")
	if err := request(outside, filepath.Join(outside, "companion", "1.901")).validateShape(); err != nil {
		t.Errorf("a staged copy in a tools folder outside the bundle: %v", err)
	}
}
