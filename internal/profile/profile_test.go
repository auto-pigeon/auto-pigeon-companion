package profile

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden canonical encodings")

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	return data
}

func decodeFixture(t *testing.T, path string) Profile {
	t.Helper()
	p, err := Decode(readFixture(t, path))
	if err != nil {
		t.Fatalf("%s should be valid:\n%v", path, err)
	}
	return p
}

// Round-tripping is the property a digest depends on: a document that is
// decoded, re-encoded and decoded again must be the same document. If it is
// not, then a grant recorded against a digest is a grant against whatever the
// encoder happened to produce that day.
func TestRoundTripIsStable(t *testing.T) {
	for _, path := range []string{
		"valid/minimal.tool.json",
		"community/user-q1-toolchain.tool.json",
	} {
		t.Run(path, func(t *testing.T) {
			first := decodeFixture(t, path)
			encoded, err := Export(first)
			if err != nil {
				t.Fatalf("Export: %v", err)
			}
			second, err := Decode(encoded)
			if err != nil {
				t.Fatalf("the exported document does not decode:\n%v", err)
			}
			reEncoded, err := Export(second)
			if err != nil {
				t.Fatalf("Export (second): %v", err)
			}
			if !bytes.Equal(encoded, reEncoded) {
				t.Errorf("canonical encoding is not stable across a round trip\n  first:  %s\n  second: %s", encoded, reEncoded)
			}
			d1, _ := Digest(first)
			d2, _ := Digest(second)
			if d1 != d2 {
				t.Errorf("digest changed across a round trip: %s -> %s", d1, d2)
			}
		})
	}
}

// Formatting is not content. A document reindented, with its members reordered
// and its unconditional arguments written the long way, is the same document
// and must have the same digest — otherwise saving a file from an editor
// revokes the grant against it.
func TestCanonicalIgnoresSpellingButNotContent(t *testing.T) {
	original := readFixture(t, "valid/minimal.tool.json")
	first, err := Decode(original)
	if err != nil {
		t.Fatalf("%v", err)
	}

	var tree map[string]any
	if err := json.Unmarshal(original, &tree); err != nil {
		t.Fatalf("%v", err)
	}
	// Re-spell the document: different member order (a JSON object has none, so
	// this is really about the encoder), different indentation, and the
	// argument written in its object form rather than as a bare string.
	actions := tree["actions"].([]any)
	action := actions[0].(map[string]any)
	action["args"] = []any{map[string]any{"value": "--help"}}
	respelled, err := json.MarshalIndent(tree, "        ", "\t")
	if err != nil {
		t.Fatalf("%v", err)
	}
	second, err := Decode(respelled)
	if err != nil {
		t.Fatalf("the re-spelled document does not decode:\n%v", err)
	}

	d1, err := Digest(first)
	if err != nil {
		t.Fatalf("%v", err)
	}
	d2, err := Digest(second)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if d1 != d2 {
		t.Errorf("re-spelling changed the digest:\n  %s\n  %s", d1, d2)
	}

	// Changing what an argument says must change it.
	action["args"] = []any{"--version"}
	changed, err := json.Marshal(tree)
	if err != nil {
		t.Fatalf("%v", err)
	}
	third, err := Decode(changed)
	if err != nil {
		t.Fatalf("%v", err)
	}
	d3, _ := Digest(third)
	if d3 == d1 {
		t.Errorf("changing an argument did not change the digest")
	}
}

// Argument order is meaning. A canonicalizer that sorted arrays would call two
// different commands the same document.
func TestCanonicalPreservesArrayOrder(t *testing.T) {
	a, err := Canonical(map[string]any{"args": []string{"-threads", "4"}})
	if err != nil {
		t.Fatalf("%v", err)
	}
	b, err := Canonical(map[string]any{"args": []string{"4", "-threads"}})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if bytes.Equal(a, b) {
		t.Errorf("two different argument orders canonicalized identically: %s", a)
	}
}

func TestCanonicalSortsObjectMembers(t *testing.T) {
	got, err := Canonical(map[string]any{"b": 1, "a": 2, "C": 3, "á": 4})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if want := `{"C":3,"a":2,"b":1,"á":4}`; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestCanonicalDropsNullMembers(t *testing.T) {
	withNull, err := Canonical(map[string]any{"a": 1, "b": nil})
	if err != nil {
		t.Fatalf("%v", err)
	}
	without, err := Canonical(map[string]any{"a": 1})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !bytes.Equal(withNull, without) {
		t.Errorf("an explicit null and an absent member canonicalized differently: %s vs %s", withNull, without)
	}
}

// The number rule that lets this canonicalizer avoid ECMAScript float
// formatting: there are no fractional numbers in a profile, so there is nothing
// for two implementations to disagree about.
func TestCanonicalRefusesFractionalNumbers(t *testing.T) {
	_, err := Canonical(map[string]any{"threads": 1.5})
	if err == nil {
		t.Fatal("a fractional number was canonicalized")
	}
	if !strings.Contains(err.Error(), "not an integer") {
		t.Errorf("the error does not explain the rule: %v", err)
	}
}

func TestCanonicalEscapesMinimally(t *testing.T) {
	got, err := Canonical(map[string]any{"s": "a\"b\\c\td\u00e9"})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if want := `{"s":"a\"b\\c\td` + "\u00e9" + `"}`; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// Golden encodings: the exact bytes a digest is taken over, checked in so that
// a change to the canonicalizer is visible in a diff rather than only as a
// changed hash. Regenerate with `go test ./internal/profile -update`.
func TestGoldenCanonicalEncodings(t *testing.T) {
	cases := []struct{ fixture, golden string }{
		{"valid/minimal.tool.json", "minimal.tool.canonical.json"},
		{"community/user-q1-toolchain.tool.json", "user-q1-toolchain.tool.canonical.json"},
	}
	for _, c := range cases {
		t.Run(c.golden, func(t *testing.T) {
			p := decodeFixture(t, c.fixture)
			got, err := Canonical(p)
			if err != nil {
				t.Fatalf("Canonical: %v", err)
			}
			path := filepath.Join("testdata", "golden", c.golden)
			if *update {
				if err := os.WriteFile(path, append(got, '\n'), 0o644); err != nil {
					t.Fatalf("writing the golden file: %v", err)
				}
				t.Logf("updated %s (%s)", path, DigestBytes(got))
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading the golden file (run with -update to create it): %v", err)
			}
			if !bytes.Equal(got, bytes.TrimRight(want, "\n")) {
				t.Errorf("canonical encoding changed\n  got:  %s\n  want: %s", got, bytes.TrimRight(want, "\n"))
			}
		})
	}
}

func TestDigestNamesItsAlgorithm(t *testing.T) {
	digest, err := Digest(decodeFixture(t, "valid/minimal.tool.json"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		t.Errorf("digest %q is not `sha256:<64 hex>`", digest)
	}
}

func TestVersionParsingAndOrdering(t *testing.T) {
	for _, bad := range []string{"", "1", "1.2", "1.2.3.4", "01.2.3", "1.2.3+build", "v1.2.3", "1.2.x", "1.2.3-"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("ParseVersion(%q) was accepted", bad)
		}
	}
	ordered := []string{"0.1.0", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0", "1.0.1", "1.1.0", "2.0.0"}
	for i := 1; i < len(ordered); i++ {
		lower, err := ParseVersion(ordered[i-1])
		if err != nil {
			t.Fatalf("%v", err)
		}
		higher, err := ParseVersion(ordered[i])
		if err != nil {
			t.Fatalf("%v", err)
		}
		if lower.Compare(higher) >= 0 {
			t.Errorf("%s should sort below %s", ordered[i-1], ordered[i])
		}
		if higher.String() != ordered[i] {
			t.Errorf("%s round-trips as %s", ordered[i], higher.String())
		}
	}
	alpha, _ := ParseVersion("2.0.0-alpha")
	if !alpha.Prerelease() {
		t.Error("a pre-release did not report itself as one")
	}
}

func TestVersionRangeIncludes(t *testing.T) {
	r := VersionRange{AtLeast: "1.0.0", Below: "2.0.0"}
	for _, c := range []struct {
		version string
		want    bool
	}{{"0.9.9", false}, {"1.0.0", true}, {"1.9.9", true}, {"2.0.0", false}, {"2.0.1", false}} {
		v, err := ParseVersion(c.version)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if got := r.Includes(v); got != c.want {
			t.Errorf("%s in %s: got %v, want %v", c.version, r, got, c.want)
		}
	}
}

func TestUnknownMemberIsReportedWithItsPath(t *testing.T) {
	_, err := Decode(readFixture(t, "malicious/unknown-member.tool.json"))
	if err == nil {
		t.Fatal("an unknown member was accepted")
	}
	problems, ok := err.(Problems)
	if !ok {
		t.Fatalf("the error is not a Problems list: %T", err)
	}
	found := false
	for _, p := range problems {
		if p.Path == "install_script" {
			found = true
			if p.Fix == "" {
				t.Error("the problem has no fix")
			}
		}
	}
	if !found {
		t.Errorf("no problem names `install_script`: %v", err)
	}
}

func TestMissingRequiredMemberIsReportedWithItsPath(t *testing.T) {
	var tree map[string]any
	if err := json.Unmarshal(readFixture(t, "valid/minimal.tool.json"), &tree); err != nil {
		t.Fatalf("%v", err)
	}
	action := tree["actions"].([]any)[0].(map[string]any)
	delete(action, "executable")
	data, _ := json.Marshal(tree)

	_, err := Decode(data)
	if err == nil {
		t.Fatal("a document missing a required member was accepted")
	}
	if !strings.Contains(err.Error(), "actions[0].executable") {
		t.Errorf("the error does not locate the missing member:\n%v", err)
	}
}

func TestWrongTypeIsReportedInWordsAUserCanAct(t *testing.T) {
	var tree map[string]any
	if err := json.Unmarshal(readFixture(t, "valid/minimal.tool.json"), &tree); err != nil {
		t.Fatalf("%v", err)
	}
	tree["name"] = []any{"Minimal"}
	data, _ := json.Marshal(tree)
	_, err := Decode(data)
	if err == nil {
		t.Fatal("a list where text belongs was accepted")
	}
	if !strings.Contains(err.Error(), "name") || !strings.Contains(err.Error(), "text is expected") {
		t.Errorf("unhelpful error:\n%v", err)
	}
}

func TestOptionValuesAreCheckedAgainstTheirType(t *testing.T) {
	max := 8
	specs := map[string]OptionSpec{
		"flag":    {Name: "flag", Type: OptionBool},
		"threads": {Name: "threads", Type: OptionInteger, Minimum: ptr(int64(1)), Maximum: ptr(int64(64))},
		"quality": {Name: "quality", Type: OptionEnum, Values: []EnumValue{{Value: "fast"}, {Value: "final"}}},
		"label":   {Name: "label", Type: OptionText, MaxLength: &max},
	}
	good := map[string]string{"flag": "true", "threads": "4", "quality": "final", "label": "level-1"}
	for name, value := range good {
		if err := specs[name].Check(value); err != nil {
			t.Errorf("%s=%q was refused: %v", name, value, err)
		}
	}
	bad := map[string]string{
		"flag":    "yes",
		"threads": "0",
		"quality": "medium",
		"label":   "../escape",
	}
	for name, value := range bad {
		if err := specs[name].Check(value); err == nil {
			t.Errorf("%s=%q was accepted", name, value)
		}
	}
	if err := specs["threads"].Check("not-a-number"); err == nil {
		t.Error("an integer option accepted text")
	}
	if err := specs["label"].Check("aaaaaaaaaaaaaaaaa"); err == nil {
		t.Error("a text option accepted a value over its length limit")
	}
}

func ptr[T any](v T) *T { return &v }

func TestDiagnosticRulesMatchLiterally(t *testing.T) {
	rule := DiagnosticRule{ID: "leak", Match: "leaked", Severity: SeverityError}
	if !rule.Matches("stdout", "*** MAP LEAKED ***  leaked") {
		t.Error("a substring match did not match")
	}
	if rule.Matches("stdout", "watertight") {
		t.Error("a non-match matched")
	}
	prefix := DiagnosticRule{ID: "p", Match: "---", Kind: MatchPrefix, Severity: SeverityInfo, Stream: "stderr"}
	if prefix.Matches("stdout", "--- phase") {
		t.Error("a stderr rule matched stdout")
	}
	if !prefix.Matches("stderr", "--- phase") {
		t.Error("a prefix rule did not match")
	}
}

func TestVersionProbeExtract(t *testing.T) {
	cases := []struct {
		probe  VersionProbe
		output string
		want   string
	}{
		{VersionProbe{Parse: VersionParse{Kind: "first_line"}}, "\nqbsp 0.18.1\nmore\n", "qbsp 0.18.1"},
		{VersionProbe{Parse: VersionParse{Kind: "line_containing", Marker: "vis"}}, "qbsp 1\nvis 0.18.1\n", "vis 0.18.1"},
		{VersionProbe{Parse: VersionParse{Kind: "after_marker", Marker: "version "}}, "tool version 2.3.4 (x86)\n", "2.3.4"},
	}
	for _, c := range cases {
		got, ok := c.probe.Extract(c.output)
		if !ok || got != c.want {
			t.Errorf("%s: got %q (%v), want %q", c.probe.Parse.Kind, got, ok, c.want)
		}
	}
	if _, ok := (VersionProbe{Parse: VersionParse{Kind: "line_containing", Marker: "nope"}}).Extract("abc"); ok {
		t.Error("a probe reported a version it did not find")
	}
}

func TestPermissionsAreOrderedWithTheWorstFirst(t *testing.T) {
	p := decodeFixture(t, "community/user-q1-toolchain-v2.tool.json")
	permissions := p.Permissions()
	if len(permissions) < 3 {
		t.Fatalf("expected several permissions, got %d", len(permissions))
	}
	if permissions[0].Risk != RiskHigh {
		t.Errorf("the first permission is %q risk, not high: %+v", permissions[0].Risk, permissions[0])
	}
	seenLower := false
	for _, permission := range permissions {
		if permission.Risk != RiskHigh {
			seenLower = true
		} else if seenLower {
			t.Errorf("a high-risk permission appears after a lower one: %+v", permission)
		}
		if permission.Summary == "" {
			t.Errorf("permission %q has no summary", permission.ID)
		}
		if !strings.HasSuffix(permission.Summary, ".") {
			t.Errorf("permission %q's summary is not a sentence: %q", permission.ID, permission.Summary)
		}
	}
}

func TestProfileReportNamesThePublisherAndTheLicence(t *testing.T) {
	p := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	report := ProfileReport(p, TrustCommunity)
	for _, want := range []string{"A Companion user", "GPL-2.0-or-later", "Community"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}
	// The permission list is gone from every review (operator, 2026-09-23).
	for _, gone := range []string{"If you approve it", "Run "} {
		if strings.Contains(report, gone) {
			t.Errorf("the report still recites permissions (%q):\n%s", gone, report)
		}
	}
	for _, want := range []string{} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}
}
