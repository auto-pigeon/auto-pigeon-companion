package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// desktopShellADR is the one decision record for the deferred desktop shell.
// `AUCOM/AUE/AUB 246I1.1` found the routed module citing `0001-aucom-desktop-shell.md`,
// which never existed, while the README cited `0026-*.md`, a glob.
const desktopShellADR = "0026-the-companion-desktop-shell-is-deferred-behind-a-measured-cgo-blocker.md"

var adrReference = regexp.MustCompile(`\$MAPPER_ROOT/LLM/docs/adr/([^` + "`" + `\s)]+)`)

// adrCitingDocuments is README.md and every routed module.
func adrCitingDocuments(t *testing.T) map[string]string {
	t.Helper()
	documents := map[string]string{"README.md": readme(t)}
	modules, err := filepath.Glob(filepath.Join("..", "..", "docs", "agent", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range modules {
		raw, readErr := os.ReadFile(module)
		if readErr != nil {
			t.Fatal(readErr)
		}
		documents[filepath.ToSlash(filepath.Join("docs", "agent", filepath.Base(module)))] = string(raw)
	}

	return documents
}

// Every shared-ADR citation names one exact file — never a glob — and, where
// the mapper root is present, that file exists.
func TestEveryADRCitationNamesOneExistingFile(t *testing.T) {
	exact := regexp.MustCompile(`^[0-9]{4}-[a-z0-9-]+\.md$`)
	adrDir := ""
	if root := os.Getenv("MAPPER_ROOT"); root != "" {
		if info, err := os.Stat(filepath.Join(root, "LLM", "docs", "adr")); err == nil && info.IsDir() {
			adrDir = filepath.Join(root, "LLM", "docs", "adr")
		}
	}
	if adrDir == "" {
		t.Log("MAPPER_ROOT/LLM/docs/adr is not present; checking the citations' shape only")
	}
	for name, body := range adrCitingDocuments(t) {
		for _, match := range adrReference.FindAllStringSubmatch(body, -1) {
			cited := strings.TrimRight(match[1], ".,;:")
			if !exact.MatchString(cited) {
				t.Errorf("%s cites %q, which is not one exact ADR file", name, cited)
				continue
			}
			if adrDir == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(adrDir, cited)); err != nil {
				t.Errorf("%s cites %s, which does not exist under %s", name, cited, adrDir)
			}
		}
	}
}

// The desktop-shell module and the README cite the same decision record.
func TestTheDesktopShellCitesADR0026(t *testing.T) {
	documents := adrCitingDocuments(t)
	for _, name := range []string{"README.md", "docs/agent/aucom.desktop-shell.md"} {
		if !strings.Contains(documents[name], "$MAPPER_ROOT/LLM/docs/adr/"+desktopShellADR) {
			t.Errorf("%s does not cite $MAPPER_ROOT/LLM/docs/adr/%s", name, desktopShellADR)
		}
	}
}
