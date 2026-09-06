package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	path := filepath.Join("..", "profile", "builtin", "sample-q1-toolchain.tool.json")
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
	for _, want := range []string{"tool", "engine", "pipeline", "builtin", "auto-pigeon.sample."} {
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

// The README's profile examples are executable, and this is what keeps them
// honest: the commands it shows are the commands that exist.
func TestReadmeProfileExamplesNameRealSubcommands(t *testing.T) {
	readme := string(readFile(t, filepath.Join("..", "..", "README.md")))
	for _, command := range []string{
		"companion profile list",
		"companion profile validate",
		"companion profile show",
		"companion profile diff",
		"companion profile schema",
	} {
		if !strings.Contains(readme, command) {
			t.Errorf("the README does not show %q", command)
		}
	}
}
