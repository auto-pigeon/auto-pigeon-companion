package profile

import (
	"encoding/json"
	"strings"
	"testing"
)

// CheckPortable is the content half of "nothing machine-local gets published".
// The structural half is the package graph (see schema_test.go); this is the
// half that catches a path pasted into a description.
func TestCheckPortableRefusesWhatIsTrueOnOneMachineOnly(t *testing.T) {
	cases := []struct {
		value string
		want  string
		why   string
	}{
		{"/home/andrea/quake/id1", "absolute path", "a POSIX path from somebody's home directory"},
		{"/var/lib/companion/tools", "absolute path", "a POSIX system path"},
		{`C:\Games\Quake\id1`, "absolute path", "a Windows drive-letter path"},
		{`\\fileserver\share\quake`, "network path", "a UNC share"},
		{"~/quake", "home directory", "a home-relative path"},
		{"http://127.0.0.1:8789/api", "loopback address", "loopback means the reader's own machine"},
		{"the server at 192.168.0.33:9190", "private address", "a LAN address is a location, not a name"},
		{"reach it at localhost:5174", "loopback name", "the name form of the same mistake"},
		{"see http://203.0.113.5/tools", "IP address", "a public literal used as a location is still a location"},
		{"reach 203.0.113.5:9190", "IP address", "so is one with a port on it"},
		{"Authorization: Bearer abc123", "authorization header", "a credential pasted into text"},
		{"token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.c2lnbmF0dXJlLWhlcmU", "JSON Web Token", "AUB issues these"},
		{"https://user:pw@example.com/x", "URL with credentials", "credentials in a URL"},
		{"-----BEGIN OPENSSH PRIVATE KEY-----", "PEM private key", "a key in a document that gets shared"},
	}
	for _, c := range cases {
		err := CheckPortable(map[string]any{"summary": c.value})
		if err == nil {
			t.Errorf("%q was accepted (%s)", c.value, c.why)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q was refused without saying %q (%s):\n%v", c.value, c.want, c.why, err)
		}
	}
}

// The rule has to leave ordinary prose alone, or it becomes a rule people work
// around rather than one they obey.
func TestCheckPortableAcceptsOrdinaryText(t *testing.T) {
	for _, value := range []string{
		"Compile a Quake 1 map: qbsp, then vis, then light.",
		"See https://ericwa.github.io/ericw-tools/ for the upstream documentation.",
		"GPL-2.0-or-later",
		"{root.game_root}/id1",
		"maps/level.bsp",
		"--threads 4",
		"a/b",
		"https://github.com/ericwa/ericw-tools/releases",
		"quake1",
		// A four-part version parses as an IPv4 address, and is not one. A
		// validator that refused `engine_version: "1.2.3.4"` would be a
		// validator people worked around.
		"1.2.3.4",
		"build 10.0.19045.1",
		"0.18.1",
	} {
		if err := CheckPortable(map[string]any{"summary": value}); err != nil {
			t.Errorf("ordinary text was refused: %q\n%v", value, err)
		}
	}
}

func TestCheckPortableLocatesTheOffendingMember(t *testing.T) {
	err := CheckPortable(map[string]any{
		"actions": []any{map[string]any{
			"roots": []any{map[string]any{"purpose": "writes into /home/andrea/quake/id1"}},
		}},
	})
	if err == nil {
		t.Fatal("a leaked path was accepted")
	}
	if !strings.Contains(err.Error(), "actions[0].roots[0].purpose") {
		t.Errorf("the error does not locate the member:\n%v", err)
	}
}

// Export is the only way a document leaves this program, and it validates
// first, so nothing unportable can be written out through it.
func TestExportRefusesAnUnportableDocument(t *testing.T) {
	var tree map[string]any
	if err := json.Unmarshal(readFixture(t, "valid/minimal.tool.json"), &tree); err != nil {
		t.Fatalf("%v", err)
	}
	// Build a valid profile, then leak into it the way a person would: by
	// typing a real path into a description.
	p := decodeFixture(t, "valid/minimal.tool.json")
	tool := p.(*ToolProfile)
	tool.Description = "Install it under /home/andrea/tools/qbsp and it will be found."

	if _, err := Export(tool); err == nil {
		t.Fatal("a document with a machine path in it was exported")
	}
	if _, err := Digest(tool); err == nil {
		t.Fatal("a document with a machine path in it was digested")
	}
}

// A refused document still has to be showable: "here is what was wrong with it"
// is the only useful thing to say about an import that failed.
func TestARefusedDocumentCanStillBeDiffed(t *testing.T) {
	before := decodeFixture(t, "valid/minimal.tool.json")
	after := decodeFixture(t, "valid/minimal.tool.json")
	after.(*ToolProfile).Description = "Install it under /home/andrea/tools/qbsp."

	diff, err := DiffProfiles(before, after)
	if err != nil {
		t.Fatalf("a refused document could not be diffed: %v", err)
	}
	if len(diff.Changes) == 0 {
		t.Error("the diff of a refused document is empty; the user would be told nothing")
	}
}

func TestPublishedProfilesCarryNoEmptyObjects(t *testing.T) {
	// A zero-valued struct that serializes as `{}` is a member that is present,
	// says nothing, and changes the digest — and, where the member has a
	// required enum inside it, produces a canonical document that the published
	// schema would reject.
	for _, path := range []string{
		"valid/minimal.tool.json",
		"community/user-q1-toolchain.tool.json",
		"../builtin/sample-q1-toolchain.tool.json",
		"../builtin/sample-q1-engine.engine.json",
		"../builtin/sample-q1-normal.pipeline.json",
	} {
		t.Run(path, func(t *testing.T) {
			canonical, err := Canonical(decodeFixture(t, path))
			if err != nil {
				t.Fatalf("%v", err)
			}
			if strings.Contains(string(canonical), `:{}`) || strings.Contains(string(canonical), `:[]`) {
				t.Errorf("the canonical form contains an empty object or list:\n%s", canonical)
			}
		})
	}
}
