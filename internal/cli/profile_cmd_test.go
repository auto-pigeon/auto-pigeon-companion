package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func fixture(name string) string {
	return filepath.Join("..", "profile", "testdata", filepath.FromSlash(name))
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return data
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func TestProfileValidateAcceptsAValidDocument(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run(env, []string{"profile", "validate", fixture("valid/minimal.tool.json")}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	for _, want := range []string{"valid tool profile", "example.minimal", "sha256:"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output does not contain %q:\n%s", want, stdout)
		}
	}
}

func TestProfileValidateReportsEveryFaultAndFailsWithOne(t *testing.T) {
	env, _, stderr := testEnv(t)
	code := Run(env, []string{"profile", "validate", fixture("malicious/shell-command-substitution.tool.json")})
	if code != 1 {
		t.Fatalf("exit code = %d; an invalid document is a failed operation, not a bad invocation", code)
	}
	if !strings.Contains(stderr.String(), "command substitution") {
		t.Errorf("the refusal does not explain itself:\n%s", stderr)
	}
	if !strings.Contains(stderr.String(), "actions[0].args[0].value") {
		t.Errorf("the refusal does not locate the fault:\n%s", stderr)
	}
}

func TestProfileValidateWithNoFileIsABadInvocation(t *testing.T) {
	env, _, _ := testEnv(t)
	if code := Run(env, []string{"profile", "validate"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestProfileShowSaysNothingIsGrantedYet(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run(env, []string{"profile", "show", fixture("community/user-q1-toolchain.tool.json")}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	out := stdout.String()
	for _, want := range []string{
		"Community —",
		"If you approve it, it may:",
		"as a program on your computer",
		"Importing a profile does not let it do any of the above",
		"digest: sha256:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}

// A built-in document is reported as built in only when its bytes match what
// shipped. Claiming a built-in id is not the same as being one.
func TestProfileShowTrustsABuiltinDocumentByItsBytes(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	path := filepath.Join("..", "profile", "builtin", "ericw-tools-q1.tool.json")
	if code := Run(env, []string{"profile", "show", path}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout.String(), "Built in —") {
		t.Errorf("the built-in document was not recognised:\n%s", stdout)
	}

	// The same id with different bytes is not the built-in document.
	altered := filepath.Join(t.TempDir(), "claims-to-be-builtin.json")
	writeAltered(t, path, altered)
	env, stdout, stderr = testEnv(t)
	if code := Run(env, []string{"profile", "show", altered}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	if strings.Contains(stdout.String(), "Built in —") {
		t.Errorf("a modified document claiming a built-in id was reported as built in:\n%s", stdout)
	}
}

func writeAltered(t *testing.T, from, to string) {
	t.Helper()
	data := readFile(t, from)
	var tree map[string]any
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatalf("%v", err)
	}
	tree["summary"] = "The same id, different bytes."
	altered, err := json.Marshal(tree)
	if err != nil {
		t.Fatalf("%v", err)
	}
	writeFile(t, to, altered)
}

func TestProfileCanonicalizeIsStableAndDigestible(t *testing.T) {
	env, first, stderr := testEnv(t)
	path := fixture("community/user-q1-toolchain.tool.json")
	if code := Run(env, []string{"profile", "canonicalize", path}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	env, second, _ := testEnv(t)
	if code := Run(env, []string{"profile", "canonicalize", path}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if first.String() != second.String() {
		t.Error("canonicalizing the same file twice produced different bytes")
	}
	var tree map[string]any
	if err := json.Unmarshal([]byte(first.String()), &tree); err != nil {
		t.Errorf("the canonical output is not valid JSON: %v", err)
	}

	env, digestOut, _ := testEnv(t)
	if code := Run(env, []string{"profile", "digest", path}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.HasPrefix(digestOut.String(), "sha256:") {
		t.Errorf("digest output is %q", digestOut)
	}
}

func TestProfileDiffNamesTheEscalation(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	code := Run(env, []string{"profile", "diff",
		fixture("community/user-q1-toolchain.tool.json"),
		fixture("community/user-q1-toolchain-v2.tool.json")})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	out := stdout.String()
	for _, want := range []string{
		"actions[0].network",
		"It now asks to:",
		"needs your decision again",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
	// One line per change: a change list that wraps is one nobody reads.
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 400 {
			t.Errorf("a change line is %d characters long:\n%s", len(line), line)
		}
	}
}

func TestProfileListShowsWhatShipsWithThisBuild(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run(env, []string{"profile", "list"}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	out := stdout.String()
	for _, want := range []string{"tool", "engine", "pipeline", "builtin",
		"auto-pigeon.ericw-tools.q1", "auto-pigeon.engine.quakespasm", "auto-pigeon.q1.normal"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}

func TestProfileSchemaListsAndPrints(t *testing.T) {
	env, stdout, stderr := testEnv(t)
	if code := Run(env, []string{"profile", "schema"}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	names := strings.Fields(stdout.String())
	if len(names) < 4 {
		t.Fatalf("expected the published schemas, got %v", names)
	}

	env, stdout, stderr = testEnv(t)
	if code := Run(env, []string{"profile", "schema", names[0]}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &doc); err != nil {
		t.Errorf("the printed schema is not valid JSON: %v", err)
	}

	env, _, stderr = testEnv(t)
	if code := Run(env, []string{"profile", "schema", "not-a-schema.json"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "published schemas:") {
		t.Errorf("the error does not list what is available:\n%s", stderr)
	}
}

func TestProfileWithNoSubcommandIsABadInvocation(t *testing.T) {
	env, _, stderr := testEnv(t)
	if code := Run(env, []string{"profile"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "companion profile validate") {
		t.Errorf("the usage text is missing:\n%s", stderr)
	}
	env, _, stderr = testEnv(t)
	if code := Run(env, []string{"profile", "nonsense"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `unknown profile command "nonsense"`) {
		t.Errorf("the error does not name the mistake:\n%s", stderr)
	}
}

// The README's toolchain examples are executable, and this is what keeps them
// honest: the commands it shows are the commands that exist.
//
// The canonical spelling since AUP/AUCOM 200F §D. The retired one is asserted
// separately below — it has to stay documented, and it has to stay documented
// as retired, which is a different claim from being an example.
func TestReadmeToolchainExamplesNameRealSubcommands(t *testing.T) {
	readme := string(readFile(t, filepath.Join("..", "..", "README.md")))
	for _, command := range []string{
		"companion toolchain list",
		"companion toolchain validate",
		"companion toolchain show",
		"companion toolchain diff",
		"companion toolchain schema",
	} {
		if !strings.Contains(readme, command) {
			t.Errorf("the README does not show %q", command)
		}
	}
}

// The retired spelling is documented, and it resolves.
//
// Both halves matter and neither implies the other: an alias nobody documents
// is a trap for the reader who has the old link, and a documented alias that
// stopped resolving is worse than one that was removed.
func TestTheRetiredProfileSpellingIsDocumentedAndResolves(t *testing.T) {
	readme := string(readFile(t, filepath.Join("..", "..", "README.md")))
	if !strings.Contains(readme, "companion profile list") {
		t.Error("the README does not show the retired spelling at all")
	}
	if !strings.Contains(readme, "### `companion profile …` still works") {
		t.Error("the README does not mark the retired spelling as retired")
	}
	if got := Aliases("toolchain"); len(got) != 1 || got[0] != "profile" {
		t.Fatalf("toolchain aliases = %v, want [profile]", got)
	}
	// Registered names are what this build calls things; an alias is not one.
	for _, name := range Names() {
		if name == "profile" {
			t.Error("the retired spelling is in Names(), so it would need a README commands row")
		}
	}
}

// Both usage blocks list the same verbs — AUP/AUCOM 200F §D.
//
// They are two hand-written literals (see toolchainUsage for why), and two
// hand-written listings of one command set drift the first time somebody adds a
// verb to only one. This is the thing that makes them not drift.
func TestBothSpellingsListTheSameVerbs(t *testing.T) {
	verbs := func(text, group string) []string {
		var found []string
		prefix := "  companion " + group + " "
		for _, line := range strings.Split(text, "\n") {
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			rest := strings.TrimPrefix(line, prefix)
			if fields := strings.Fields(rest); len(fields) > 0 {
				found = append(found, fields[0])
			}
		}
		return found
	}
	legacy := verbs(profileUsage, "profile")
	canonical := verbs(toolchainUsage, "toolchain")
	if len(legacy) == 0 {
		t.Fatal("no verbs parsed out of profileUsage; the parser or the block changed shape")
	}
	if !slices.Equal(legacy, canonical) {
		t.Errorf("the two spellings list different verbs:\n  profile:   %v\n  toolchain: %v", legacy, canonical)
	}
	// Both blocks also have to cover the dispatcher, or a verb exists and is
	// reachable and nothing tells anybody about it.
	for _, verb := range []string{
		"validate", "show", "canonicalize", "digest", "diff", "list", "schema",
		"review", "grant", "withdraw",
		"preview", "publish", "catalog", "published", "install", "yank", "report",
	} {
		if !slices.Contains(canonical, verb) {
			t.Errorf("toolchainUsage does not name %q", verb)
		}
	}
}

// The alias is byte-for-byte the canonical command — AUP/AUCOM 200F §F9.
//
// stdout and the exit status, over every verb that can answer without a network
// or a decision. Group `--help` is deliberately NOT in this set and deliberately
// does differ: it is the one output whose whole job is to name the group the
// reader typed, and printing the canonical spelling to somebody who typed the
// retired one would be the rename finishing in the wrong place.
func TestTheAliasIsByteIdenticalOnStdout(t *testing.T) {
	document := filepath.Join(t.TempDir(), "example.profile.json")
	if err := os.WriteFile(document, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := [][]string{
		{"list"},
		{"schema"},
		{"validate", document},
		{"show", document},
		{"digest", document},
		{"nonsense"},
		{"review"},
	}
	for _, argv := range cases {
		legacyEnv, legacyOut, _ := testEnv(t)
		canonicalEnv, canonicalOut, _ := testEnv(t)
		legacyCode := Run(legacyEnv, append([]string{"profile"}, argv...))
		canonicalCode := Run(canonicalEnv, append([]string{"toolchain"}, argv...))
		if legacyCode != canonicalCode {
			t.Errorf("%v: exit %d through profile, %d through toolchain", argv, legacyCode, canonicalCode)
		}
		if legacyOut.String() != canonicalOut.String() {
			t.Errorf("%v: stdout differs\n  profile:   %q\n  toolchain: %q", argv, legacyOut, canonicalOut)
		}
	}
}
