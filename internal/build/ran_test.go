package build

import (
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
)

// The precedence of what a run's record can say about the program that ran
// (AUCOM #49, NEW_633), and what it never says: the profile's tool_version.
func TestRanTakesTheProgramsOwnWordThenTheInstallThenTheSetupProbe(t *testing.T) {
	doc := DocumentRef{ID: "p", Version: "1.0.0", Name: "EricW tools, Auto-Pigeon build 1.4556"}
	const written = "2.0.0+auto-pigeon.1.4556"
	cases := []struct {
		name   string
		tool   ToolRecord
		path   string
		source string
		label  string
	}{
		{"probe and install", ToolRecord{Profile: doc, ToolVersion: written,
			Managed:     &binding.ManagedInstall{Tool: "ericw-tools", Version: "1.4560"},
			Executables: []ExecutableRecord{{Name: "qbsp", Path: "/m/qbsp", Identity: "2.0.0+auto-pigeon.1.4560"}}},
			"/m/qbsp", RanFromExecutable, "ericw-tools 1.4560 (2.0.0+auto-pigeon.1.4560)"},
		{"install only", ToolRecord{Profile: doc, ToolVersion: written,
			Managed: &binding.ManagedInstall{Tool: "ericw-tools", Version: "1.4560"}},
			"", RanFromManagedInstall, "ericw-tools 1.4560"},
		{"probe only", ToolRecord{Profile: doc, ToolVersion: written,
			Executables: []ExecutableRecord{{Name: "qbsp", Path: "/u/qbsp", Identity: "0.18.1"}}},
			"", RanFromExecutable, "0.18.1"},
		{"setup probe only", ToolRecord{Profile: doc, ToolVersion: written, ResolvedVersion: "0.18.1"},
			"", RanFromSetupProbe, "0.18.1 (reported when it was set up)"},
		{"nothing recorded", ToolRecord{Profile: doc, ToolVersion: written,
			Executables: []ExecutableRecord{{Name: "qbsp", Path: "/u/qbsp", SHA256: "aa"}}},
			"/u/qbsp", RanNotRecorded, "version not recorded for this run"},
		// The step started vis, which was never asked: qbsp's word is not
		// borrowed for it.
		{"another program's word is not borrowed", ToolRecord{Profile: doc, ToolVersion: written,
			Executables: []ExecutableRecord{{Name: "qbsp", Path: "/u/qbsp", Identity: "0.18.1"}, {Name: "vis", Path: "/u/vis"}}},
			"/u/vis", RanNotRecorded, "version not recorded for this run"},
		// A path the record lists no executable at (an older manifest) takes
		// the first identity there is.
		{"unlisted path", ToolRecord{Profile: doc, ToolVersion: written,
			Executables: []ExecutableRecord{{Name: "qbsp", Path: "/u/qbsp", Identity: "0.18.1"}}},
			"/elsewhere/qbsp", RanFromExecutable, "0.18.1"},
	}
	for _, tc := range cases {
		ran := tc.tool.Ran(tc.path)
		if ran.Source != tc.source || ran.Label != tc.label {
			t.Errorf("%s: Ran = %q / %q, want %q / %q", tc.name, ran.Source, ran.Label, tc.source, tc.label)
		}
		if ran.Profile != doc.Name+" 1.0.0, written for "+written {
			t.Errorf("%s: the profile reads %q", tc.name, ran.Profile)
		}
	}
}
