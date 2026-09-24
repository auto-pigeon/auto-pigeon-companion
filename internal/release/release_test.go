package release

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The claim the whole licensing argument rests on, checked against the binary
// rather than against go.mod: nothing external is linked into this program.
//
// A dependency is not forbidden for ever. What is forbidden is one arriving
// without anybody deciding, and this is where that decision gets made: adding a
// module means updating this test and THIRD_PARTY_NOTICES.md in the same
// change, because an MIT artifact that quietly contains somebody else's code is
// an artifact whose licence is wrong.
func TestThisProgramLinksNoExternalModule(t *testing.T) {
	modules, ok := Dependencies()
	if !ok {
		t.Skip("this binary carries no build information")
	}
	if len(modules) != 0 {
		names := make([]string, 0, len(modules))
		for _, module := range modules {
			names = append(names, module.Path+"@"+module.Version)
		}
		t.Fatalf("this build links %d external module(s): %s.\n"+
			"Adding one is a decision about the artifact's licence and its supply chain. "+
			"Record it in THIRD_PARTY_NOTICES.md and in internal/threat's matrix, then update this test.",
			len(modules), strings.Join(names, ", "))
	}
}

// A test binary's main module is the test binary, not the program, so its path
// is empty here. What must never happen is it naming a DIFFERENT module — that
// would mean this package is describing somebody else's build.
func TestSelfNeverNamesAnotherModule(t *testing.T) {
	self, ok := Self()
	if !ok {
		t.Skip("this binary carries no build information")
	}
	if self.Path != "" && self.Path != ModulePath {
		t.Errorf("the main module is %q, want %q or empty in a test binary", self.Path, ModulePath)
	}
}

// Every external program is in the component list with a licence, and the only
// things marked as being inside the artifact are the Companion (MIT) and the
// auto-pigeon-libraries contract files compiled into it (Apache-2.0). Anything
// else inside it, or either of those under another licence, is a binary whose
// licence statement is wrong.
func TestTheArtifactHoldsOnlyTheCompanionAndItsAULIBSContracts(t *testing.T) {
	components, err := Components("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(components) < 5 {
		t.Fatalf("only %d components; the built-in profiles are not being read", len(components))
	}
	companion, aulibs := 0, 0
	for _, component := range components {
		if component.Distribution != InArtifact {
			continue
		}
		switch {
		case component.Name == "auto-pigeon-companion":
			companion++
			if component.SPDX != License || License != "MIT" {
				t.Errorf("the Companion is in the artifact under %q, not MIT", component.SPDX)
			}
		case strings.HasPrefix(component.Name, "@auto-pigeon/") && component.Repository == AULIBSRepository:
			aulibs++
			if component.SPDX != "Apache-2.0" {
				t.Errorf("%s is AULIBS material and is listed as %q, not Apache-2.0", component.Name, component.SPDX)
			}
		default:
			t.Errorf("%s claims to be in the artifact; only the Companion and its AULIBS contracts may", component.Name)
		}
	}
	if companion != 1 || aulibs != len(AULIBSContracts) || aulibs == 0 {
		t.Errorf("in the artifact: %d Companion, %d AULIBS contract(s); want 1 and %d", companion, aulibs,
			len(AULIBSContracts))
	}
}

// The extractor is proprietary (NEW_247G). The component list, the SBOM and
// the audit must never call it MIT — that would describe an archive carrying it
// as entirely MIT — nor copyleft, which it no longer is, and they must not
// pretend to offer a source it does not have.
func TestTheExtractorIsListedAsProprietaryNeverMITOrCopyleft(t *testing.T) {
	if Extractor.SPDX != "LicenseRef-Auto-Pigeon-Proprietary" ||
		Extractor.LicenseName != "Auto-Pigeon Proprietary Software License" {
		t.Errorf("the extractor is listed as %q / %q", Extractor.SPDX, Extractor.LicenseName)
	}
	if Extractor.CorrespondingSource != "" {
		t.Errorf("the extractor offers corresponding source %q; it is proprietary and offers none",
			Extractor.CorrespondingSource)
	}
	for _, forbidden := range []string{"MIT", "AGPL", "GPL", "Apache"} {
		if strings.Contains(Extractor.SPDX, forbidden) || strings.Contains(Extractor.LicenseName, forbidden) {
			t.Errorf("the extractor's licence mentions %s: %q / %q", forbidden, Extractor.SPDX, Extractor.LicenseName)
		}
	}
	if !strings.Contains(Extractor.Notice, "Andrea D'Intino") || !strings.Contains(Extractor.Notice, "authorization") {
		t.Errorf("the extractor's notice does not name its owner and the authorization it needs: %q", Extractor.Notice)
	}

	document, err := BuildSBOM("1.2.3", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range document.Components {
		if entry.Name != "auto-pigeon-extractor" {
			continue
		}
		found = true
		if len(entry.Licenses) != 1 || entry.Licenses[0].Expression != "LicenseRef-Auto-Pigeon-Proprietary" ||
			entry.Licenses[0].License != nil {
			t.Errorf("the SBOM lists the extractor's licence as %+v", entry.Licenses)
		}
		for _, property := range entry.Properties {
			if property.Name == "aucom:corresponding-source" {
				t.Errorf("the SBOM offers the proprietary extractor's source: %s", property.Value)
			}
		}
	}
	if !found {
		t.Error("the SBOM does not list the extractor")
	}
	for _, license := range document.Metadata.Component.Licenses {
		if license.License == nil || license.License.ID != "MIT" {
			t.Errorf("the SBOM's own component is licensed %+v, want MIT", license)
		}
	}
}

// The AULIBS contract files are in the SBOM as Apache-2.0 libraries, so the
// document does not describe the binary as MIT and nothing else.
func TestTheSBOMListsTheAULIBSContractsAsApache(t *testing.T) {
	document, err := BuildSBOM("1.2.3", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, entry := range document.Components {
		if !strings.HasPrefix(entry.Name, "@auto-pigeon/") {
			continue
		}
		seen++
		if entry.Type != "library" || len(entry.Licenses) != 1 || entry.Licenses[0].License == nil ||
			entry.Licenses[0].License.ID != "Apache-2.0" {
			t.Errorf("%s is in the SBOM as %s %+v", entry.Name, entry.Type, entry.Licenses)
		}
	}
	if seen != len(AULIBSContracts) {
		t.Errorf("the SBOM lists %d AULIBS contract(s); want %d", seen, len(AULIBSContracts))
	}
}

// A copyleft program this project ships has to carry a corresponding-source
// offer; this states the rule over what a release DESCRIBES.
func TestEveryShippedCopyleftComponentOffersItsSource(t *testing.T) {
	components, err := Components("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, component := range components {
		if component.Distribution != ShippedBeside {
			continue
		}
		seen++
		if !strings.HasPrefix(component.SPDX, "GPL") && !strings.HasPrefix(component.SPDX, "AGPL") &&
			!strings.HasPrefix(component.SPDX, "LGPL") {
			continue
		}
		if component.CorrespondingSource == "" {
			t.Errorf("%s is %s and is shipped beside this program, but offers no corresponding source",
				component.Name, component.SPDX)
		}
	}
	if seen == 0 {
		t.Error("no component is shipped beside the Companion; the extractor is missing from the list")
	}
}

// Every component says which of the three relationships it has. "It is
// mentioned somewhere" is not the same statement as "its bytes are in here",
// and a release document that blurred them would be the misleading one.
func TestEveryComponentDeclaresHowItReachesTheUser(t *testing.T) {
	components, err := Components("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range components {
		switch component.Distribution {
		case InArtifact, ShippedBeside, UserSupplied:
		default:
			t.Errorf("%s has distribution %q", component.Name, component.Distribution)
		}
		if component.SPDX == "" {
			t.Errorf("%s declares no licence", component.Name)
		}
	}
}

func TestTheSBOMIsValidJSONAndNamesEveryExternalProgram(t *testing.T) {
	document, err := BuildSBOM("1.2.3", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := document.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var round map[string]any
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("the SBOM is not valid JSON: %v", err)
	}
	if round["bomFormat"] != "CycloneDX" || round["specVersion"] != "1.5" {
		t.Errorf("the SBOM is not a CycloneDX 1.5 document: %v %v", round["bomFormat"], round["specVersion"])
	}
	for _, want := range []string{"auto-pigeon.ericw-tools.q1", "auto-pigeon.q3map2", "corresponding-source"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the SBOM does not mention %q", want)
		}
	}
}

// Two SBOMs of one build are the same bytes. A document that changed every time
// it was generated could not be published beside a checksum.
func TestTheSBOMIsByteIdenticalWithoutATimestamp(t *testing.T) {
	first, err := BuildSBOM("1.2.3", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildSBOM("1.2.3", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := first.Encode()
	b, _ := second.Encode()
	if string(a) != string(b) {
		t.Error("two SBOMs of one build differ")
	}
	if strings.Contains(string(a), "timestamp") {
		t.Error("a document with no timestamp carries a timestamp member")
	}

	stamped, err := BuildSBOM("1.2.3", time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := stamped.Encode()
	if !strings.Contains(string(raw), "2026-09-07T12:00:00Z") {
		t.Error("a stamped document does not carry the stamp")
	}
}

func TestChecksumsDigestEveryReleasedFileAndNotItself(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"companion-linux-amd64": "one",
		"companion-windows.exe": "two",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A bundle directory is published as its .zip; walking into one would fill
	// the file with paths nobody downloads.
	if err := os.MkdirAll(filepath.Join(dir, "Companion.app", "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ChecksumFileName), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sums, err := Checksums(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 {
		t.Fatalf("digested %d files, want 2: %+v", len(sums), sums)
	}
	for _, sum := range sums {
		if sum.Name == ChecksumFileName {
			t.Error("the checksum file digested itself")
		}
		if len(sum.SHA256) != 64 {
			t.Errorf("%s has digest %q", sum.Name, sum.SHA256)
		}
	}
	// sha256sum's own format, so `sha256sum -c SHA256SUMS` works.
	formatted := string(FormatChecksums(sums))
	for _, line := range strings.Split(strings.TrimSpace(formatted), "\n") {
		if len(line) < 67 || line[64:66] != "  " {
			t.Errorf("%q is not sha256sum's format", line)
		}
	}
	// The known digest of "one", so a change to how the file is read is caught.
	if !strings.Contains(formatted, "7692c3ad3540bb803c020b3aee66cd8887123234ea0c6e7143c0add73ff431ed") {
		t.Errorf("the digest of a known file is wrong:\n%s", formatted)
	}
}

func TestChecksumsReportAMissingDirectoryByName(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "not-built")
	if _, err := Checksums(absent); err == nil || !strings.Contains(err.Error(), absent) {
		t.Errorf("Checksums returned %v", err)
	}
}
