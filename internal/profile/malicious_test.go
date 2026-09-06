package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The hostile corpus.
//
// Every file under testdata/malicious is a document that must be refused, and
// every entry below names the sentence a user should get. The second half
// matters as much as the first: a validator that refuses everything with
// "invalid profile" is a validator nobody can write a profile against, and the
// person who has to act on the message is usually not the person who wrote the
// document.
//
// The table is checked against the directory, so a fixture added without an
// expectation — or an expectation left behind after a fixture is deleted —
// fails rather than silently passing.
func TestMaliciousFixturesAreRefusedWithAnActionableMessage(t *testing.T) {
	cases := map[string]struct {
		want string
		why  string
	}{
		"shell-command-substitution.tool.json": {
			want: "command substitution",
			why:  "argv never reaches a shell, so this is inert here — and a profile that contains it was written against a shell it will not get",
		},
		"shell-chaining.tool.json": {
			want: "a shell command separator",
			why:  "a reviewer should not have to reason about whether `; rm -rf` in an argument is inert",
		},
		"shell-pipe.tool.json": {
			want: "a shell pipe",
			why:  "same rule, the other spelling",
		},
		"path-traversal-output.tool.json": {
			want: "contains `..`",
			why:  "an output path may not leave the root it is declared under",
		},
		"traversal-executable.tool.json": {
			want: "contains `..`",
			why:  `"run the program this profile installed" must not be able to mean /bin/sh`,
		},
		"absolute-path-leak.tool.json": {
			want: "which is a path on one machine and wrong on every other",
			why:  "a shared document that names somebody's home directory is wrong everywhere it is read",
		},
		"home-path-leak.tool.json": {
			want: "a home directory that differs per user",
			why:  "same rule, the `~` spelling",
		},
		"token-leak.tool.json": {
			want: "a JSON Web Token",
			why:  "a session token pasted into a description is disclosed the moment the profile is shared",
		},
		"loopback-host.tool.json": {
			want: "the loopback address",
			why:  "on somebody else's machine, 127.0.0.1 is their machine — the mistake this workspace has already paid for once",
		},
		"private-host.tool.json": {
			want: "the private address",
			why:  "a LAN address in a shared document is a location, not a name",
		},
		"unknown-member.tool.json": {
			want: "is not a member this build understands",
			why:  "an install script is exactly the member this format refuses to have",
		},
		"duplicate-member.tool.json": {
			want: "is named twice in the same object",
			why:  "two parsers disagree about which value wins, so the reviewed document and the executed one differ",
		},
		"env-ld-preload.tool.json": {
			want: "may not be inherited or set",
			why:  "LD_PRELOAD changes what is loaded into the process, so it would make `run this program` mean something else",
		},
		"env-secret.tool.json": {
			want: "reads like a credential",
			why:  "a profile may not ask the Companion to hand a token to a subprocess",
		},
		"regex-diagnostic.tool.json": {
			want: "there is no regular-expression match",
			why:  "an untrusted regex is a denial-of-service primitive and no diagnostic needs one",
		},
		"undeclared-placeholder.tool.json": {
			want: "refers to the undeclared option",
			why:  "a placeholder with no declaration would resolve to nothing at run time",
		},
		"unknown-schema-version.tool.json": {
			want: "which this build of the Companion cannot read",
			why:  "a format this build has never seen is where a partial read is worse than no read",
		},
		"text-option-escape.tool.json": {
			want: "a text option may contain letters, digits",
			why:  "the narrow character set is why an option cannot turn a contained output path into an escape",
		},
		"game-profile-record-id.tool.json": {
			want: "is not a member this build understands",
			why:  "a record id identifies a row in one AUB deployment and means nothing in a document that travels",
		},
		"undeclared-executable.tool.json": {
			want: "names the undeclared executable",
			why:  "an action may only run one of the programs the profile declares",
		},
		"oversized-text-option.tool.json": {
			want: "it must be between 1 and 1024",
			why:  "a 4 KB free-text option is an `extra arguments` box with extra steps",
		},
		"bidi-override-argument.tool.json": {
			want: "bidirectional control character",
			why:  "an override makes the rendered argument differ from the stored one, so the reviewer approves a command they did not read",
		},
		"engine-session-role-lie.engine.json": {
			want: `always starts a dedicated_server`,
			why:  "a public server labelled as a single-player session is the failure the closed action vocabulary exists to prevent",
		},
		"engine-invented-action.engine.json": {
			want: "which is not an engine action",
			why:  "an invented action is a button the Companion has no permission summary for",
		},
		"pipeline-forward-wire.pipeline.json": {
			want: "which is not declared before this point",
			why:  "a wire may only reach backwards, which makes a cycle unrepresentable rather than merely detected",
		},
	}

	entries, err := os.ReadDir(filepath.Join("testdata", "malicious"))
	if err != nil {
		t.Fatalf("reading the corpus: %v", err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		seen[name] = true
		expectation, known := cases[name]
		if !known {
			t.Errorf("%s has no expectation in this table; say what a user should be told about it", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "malicious", name))
			if err != nil {
				t.Fatalf("%v", err)
			}
			_, err = Decode(data)
			if err == nil {
				t.Fatalf("was accepted. %s", expectation.why)
			}
			if !strings.Contains(err.Error(), expectation.want) {
				t.Errorf("the refusal does not say %q (%s)\ngot:\n%v", expectation.want, expectation.why, err)
			}
		})
	}
	for name := range cases {
		if !seen[name] {
			t.Errorf("the table expects %s, which is not in the corpus", name)
		}
	}
}

// Validation reports everything it finds. Somebody repairing a profile against
// a validator that stops at the first fault learns that the document is wrong
// six times instead of learning what is wrong with it.
func TestValidationReportsEveryFaultAtOnce(t *testing.T) {
	data := []byte(`{
      "schema_version": "aucom.profile/1.0",
      "kind": "tool",
      "id": "NOT-NAMESPACED",
      "version": "one",
      "name": "",
      "summary": "",
      "publisher": {"name": ""},
      "license": {"spdx": ""},
      "tool_version": "1.0.0",
      "platforms": [],
      "acquisition": [],
      "executables": [],
      "actions": []
    }`)
	_, err := Decode(data)
	if err == nil {
		t.Fatal("a thoroughly invalid document was accepted")
	}
	problems, ok := err.(Problems)
	if !ok {
		t.Fatalf("the error is not a Problems list: %T", err)
	}
	if len(problems) < 8 {
		t.Errorf("only %d problems were reported; validation should collect rather than stop:\n%v", len(problems), err)
	}
	paths := map[string]bool{}
	for _, p := range problems {
		paths[p.Path] = true
	}
	for _, want := range []string{"id", "version", "name", "summary", "platforms", "acquisition", "executables", "actions"} {
		if !paths[want] {
			t.Errorf("nothing was reported at %q:\n%v", want, err)
		}
	}
}
