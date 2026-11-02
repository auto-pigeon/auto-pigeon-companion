// Package retiredfixture keeps the upstream tool documents the Companion shipped
// until NEW_380, for tests and for nothing else.
//
// Auto-Pigeon's own builds of ericw-tools and q3map2 are the only compilers the
// Companion offers now. These documents remain because what they recorded is
// still evidence: each fork is held to do what its predecessor did, and the
// compatibility machinery is exercised with a document that accepts a program
// the fork documents refuse. Only a test may import this package —
// TestNothingButATestReadsTheRetiredDocuments in internal/profile/builtin says so
// — so no catalogue, resolver or setup page can reach them.
package retiredfixture

import (
	"embed"
	"fmt"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

//go:embed *.json
var files embed.FS

// The ids these documents declare, kept only here.
const (
	EricwQ1 = "auto-pigeon.ericw-tools.q1"
	EricwQ2 = "auto-pigeon.ericw-tools.q2"
	Q3Map2  = "auto-pigeon.q3map2"
)

var fileOf = map[string]string{
	EricwQ1: "ericw-tools-q1.tool.json",
	EricwQ2: "ericw-tools-q2.tool.json",
	Q3Map2:  "q3map2.tool.json",
}

// Tool decodes one retired document through the ordinary decoder.
func Tool(id string) (*profile.ToolProfile, error) {
	raw, err := Raw(id)
	if err != nil {
		return nil, err
	}
	document, err := profile.Decode(raw)
	if err != nil {
		return nil, err
	}
	tool, ok := document.(*profile.ToolProfile)
	if !ok {
		return nil, fmt.Errorf("retiredfixture: %s is not a tool profile", id)
	}
	return tool, nil
}

// Raw is one retired document's bytes.
func Raw(id string) ([]byte, error) {
	name, ok := fileOf[id]
	if !ok {
		return nil, fmt.Errorf("retiredfixture: no document has the id %q", id)
	}
	return files.ReadFile(name)
}
