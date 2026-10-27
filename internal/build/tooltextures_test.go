package build

import (
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// NEW_357: ericw-tools 2.0 reports `skip` on every Quake 1 map, with every WAD
// found. That line is information, kept word for word; a real missing texture
// is still a warning, and an error is still an error.
func TestAToolTextureLineIsInformationAndAMissingTextureIsStillAWarning(t *testing.T) {
	findings := []job.Diagnostic{
		{RuleID: "texture_not_found", Severity: profile.SeverityWarning, Raw: "WARNING: unable to find texture skip",
			Message: "A texture the map uses is not in any WAD that was opened."},
		{RuleID: "texture_not_found", Severity: profile.SeverityWarning, Raw: "WARNING: unable to find texture n357_floor"},
		{RuleID: "texture_not_found", Severity: profile.SeverityWarning, Raw: "Couldn't locate texture for e1u1/clip"},
		{RuleID: "texture_missing", Severity: profile.SeverityError, Raw: "WARNING 16: texture skip not found"},
		{RuleID: "wad_not_found", Severity: profile.SeverityWarning, Raw: "addArchive: WARNING: archive 'skip' not found"},
	}
	got := expectedToolTextures(findings)
	want := []profile.Severity{profile.SeverityInfo, profile.SeverityWarning, profile.SeverityInfo,
		profile.SeverityError, profile.SeverityWarning}
	for i, d := range got {
		if d.Severity != want[i] {
			t.Errorf("%q: %s, want %s", d.Raw, d.Severity, want[i])
		}
		if d.Raw != findings[i].Raw {
			t.Errorf("the compiler's line changed: %q", d.Raw)
		}
	}
	if findings[0].Severity != profile.SeverityWarning {
		t.Error("the job's own record was rewritten")
	}
	manifest := Manifest{Steps: []Step{{Diagnostics: got}}}
	if n := manifest.Warnings(); n != 2 {
		t.Errorf("%d warnings, want the missing texture and the missing WAD", n)
	}
}
