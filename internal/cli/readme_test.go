package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readme is README.md, read from the repository root.
func readme(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	return string(raw)
}

// The README prints `companion --help` as a console block. A command added to
// the registry and not to that block is a command nobody reading the README
// knows exists — and one removed from the registry leaves the README promising
// something that exits 2.
func TestTheReadmeHelpBlockListsExactlyTheRegisteredCommands(t *testing.T) {
	body := readme(t)
	start := strings.Index(body, "$ companion --help")
	if start < 0 {
		t.Fatal("README.md no longer shows `companion --help`")
	}
	end := strings.Index(body[start:], "\n```")
	if end < 0 {
		t.Fatal("the --help block in README.md is unterminated")
	}
	block := body[start : start+end]

	for _, name := range Names() {
		// At the start of a line in the commands listing, so `release` does not
		// match the word in another command's summary.
		if !strings.Contains(block, "\n  "+name+" ") && !strings.Contains(block, "\n  "+name+"\n") {
			t.Errorf("README.md's --help block does not list the %q command", name)
		}
	}

	// And nothing in the block that is not a command any more.
	registered := map[string]bool{}
	for _, name := range Names() {
		registered[name] = true
	}
	inCommands := false
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "commands:") {
			inCommands = true
			continue
		}
		if !inCommands || !strings.HasPrefix(line, "  ") {
			continue
		}
		name := strings.Fields(strings.TrimSpace(line))
		if len(name) == 0 {
			continue
		}
		if !registered[name[0]] {
			t.Errorf("README.md's --help block lists %q, which is not a registered command", name[0])
		}
	}
}

// Every command the README documents with an executable example has to exist.
// The README rule in AGENTS.md asks for an example per command; this is what
// stops one drifting into naming a subcommand that was renamed.
func TestTheReadmeExamplesNameRealSubcommands(t *testing.T) {
	body := readme(t)
	documented := map[string][]string{
		"uri":       {"status", "register", "unregister"},
		"security":  {"matrix", "residual", "audit"},
		"release":   {"sbom", "checksums"},
		"uninstall": nil,
	}
	registered := map[string]bool{}
	for _, name := range Names() {
		registered[name] = true
	}
	for command, subcommands := range documented {
		if !registered[command] {
			t.Errorf("README.md documents `companion %s`, which is not registered", command)
			continue
		}
		if !strings.Contains(body, "companion "+command) {
			t.Errorf("README.md has no executable example for `companion %s`", command)
		}
		for _, sub := range subcommands {
			if !strings.Contains(body, "companion "+command+" "+sub) {
				t.Errorf("README.md has no example for `companion %s %s`", command, sub)
			}
		}
	}
}

// The two claims a reader most needs to be able to rely on, and the two most
// likely to be quietly softened: that nothing is signed, and that a link starts
// nothing.
func TestTheReadmeStaysTruthfulAboutSigningAndAboutLinks(t *testing.T) {
	body := readme(t)
	for _, want := range []string{
		"There is no Apple Developer ID and no Authenticode certificate",
		"which is not a\nsignature",
		"The command is `game join` with **no",
		"starts nothing",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("README.md no longer says %q", want)
		}
	}
}
