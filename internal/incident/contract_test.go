package incident

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// aulibsIncidentDir is the authority, when a sibling checkout is present. A
// release build needs none: that is why the files are embedded.
const aulibsIncidentDir = "../../../auto-pigeon-libraries/ts/incident-contract"

// The embedded contract data must be byte-for-byte what AULIBS says it is.
// Skipped, LOUDLY, when the sibling is absent: a skip says "not checked here",
// a pass would say "checked and identical", which would be a lie.
func TestEmbeddedContractIsExactlyAULIBS(t *testing.T) {
	for _, name := range []string{"incident-codes.json", "redaction-rules.json"} {
		authority := filepath.Join(aulibsIncidentDir, "schema", name)
		want, err := os.ReadFile(authority)
		if err != nil {
			t.Skipf("no auto-pigeon-libraries checkout beside this one (%v); "+
				"the embedded copy of %s was NOT compared against the authority", err, name)
		}
		got, err := os.ReadFile(filepath.Join("contract", name))
		if err != nil {
			t.Fatalf("the embedded %s is missing: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("internal/incident/contract/%s has drifted from %s.\nRe-copy it; do not edit either by hand.", name, authority)
		}
	}
}

// The Companion raises exactly the codes the taxonomy gives it.
func TestTheCompanionRaisesOnlyItsOwnCodes(t *testing.T) {
	for _, code := range []string{CodeJobFailed, CodeReadinessFailed} {
		entry, ok := Lookup(code)
		if !ok {
			t.Errorf("%s is raised here and is not in the taxonomy", code)
			continue
		}
		if entry.Component != Component {
			t.Errorf("%s is attributed to %s, not %s", code, entry.Component, Component)
		}
	}
	if _, ok := Lookup(CodeTelemetryUnavailable); !ok {
		t.Errorf("%s is not in the taxonomy", CodeTelemetryUnavailable)
	}
}

// Every value pattern compiles in RE2 after the `\s` translation.
func TestEveryContractPatternCompilesInRE2(t *testing.T) {
	loadRules()
	if len(skipped) > 0 {
		t.Fatalf("contract patterns that did not compile: %v", skipped)
	}
	if len(patterns) == 0 {
		t.Fatal("the contract carries no value patterns")
	}
}

// redactionCorpus is every kind of thing the rules exist for, plus the
// whitespace cases where JavaScript's `\s` and RE2's differ.
var redactionCorpus = []string{
	"plain words and 0123456789abcdef0123456789abcdef stay",
	"https://key:secret@glitchtip.example.test/3 is a DSN",
	"visit https://auto-pigeon.example.test/api/maps/abc?token=zzz now",
	"file:///home/alice/maps/e1m1.map",
	"reach 192.168.0.33:9190 or 10.0.0.1",
	"mail alice@example.test about it",
	"Authorization: Bearer abcdefghijklmnop",
	"cookie pb_auth=eyJabc; sessionid=xyz",
	"token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.c2lnbmF0dXJlLXZhbHVl",
	"a key ghp_abcdefghijklmnopqrstuvwxyz0123 and AKIAABCDEFGHIJKLMNOP",
	"~/secret/notes.txt and /home/bob/quake/id1/pak0.pak and C:\\Users\\bob\\quake",
	"/tmp/aucom-fault-123/workspace/out",
	"no\u00a0break https://host.test/a\u00a0b and /home/x\u2003y",
	"ideographic\u3000space /Users/eve/a\u3000b http://h.test/p\u2028q",
	"zero width \ufeffhttps://h.test/p\ufeffq",
}

// Go's redaction must agree with AULIBS' JavaScript on every string, and in
// particular on Unicode whitespace, where RE2's `\s` is narrower than
// JavaScript's. Run against the real reference implementation with node;
// skipped, loudly, when either is absent.
func TestRedactionMatchesTheJavaScriptReference(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine; Go redaction was NOT compared against AULIBS' JavaScript")
	}
	reference, err := filepath.Abs(filepath.Join(aulibsIncidentDir, "src", "redact.mjs"))
	if err == nil {
		_, err = os.Stat(reference)
	}
	if err != nil {
		t.Skipf("no auto-pigeon-libraries checkout beside this one (%v); Go redaction was NOT compared", err)
	}
	script := `import { redactText } from ` + jsString("file://"+filepath.ToSlash(reference)) + `;
let input = ""; process.stdin.on("data", (c) => (input += c));
process.stdin.on("end", () => process.stdout.write(JSON.stringify(JSON.parse(input).map(redactText))));`
	encoded, _ := json.Marshal(redactionCorpus)
	command := exec.Command(node, "--input-type=module", "-e", script)
	command.Stdin = bytes.NewReader(encoded)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("running the JavaScript reference: %v\n%s", err, stderr.String())
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("the reference answered %q: %v", output, err)
	}
	for i, input := range redactionCorpus {
		if got := RedactString(input); got != want[i] {
			t.Errorf("redaction differs from AULIBS for %q:\n  go: %q\n  js: %q", input, got, want[i])
		}
	}
}

func jsString(s string) string {
	encoded, _ := json.Marshal(s)
	return string(encoded)
}

// Without the translation, RE2 would stop a `[^\s…]` run at an ASCII space
// only; this pins that the translation is what makes the difference.
func TestTheWhitespaceTranslationIsJavaScripts(t *testing.T) {
	in := "see /home/x\u00a0tail"
	if got := RedactString(in); !strings.HasSuffix(got, "\u00a0tail") {
		t.Errorf("RedactString(%q) = %q; a no-break space ends a path in JavaScript", in, got)
	}
	if got := translateJS(`[^\s/]+\s`); !strings.Contains(got, `\x{3000}`) || strings.Contains(got, `\s`) {
		t.Errorf("translateJS left RE2's ASCII \\s in place: %s", got)
	}
}
