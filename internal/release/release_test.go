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

// Every external program is in the component list with a licence, and the
// Companion is the only thing marked as being inside the artifact.
func TestOnlyTheCompanionIsInTheArtifact(t *testing.T) {
	components, err := Components("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(components) < 5 {
		t.Fatalf("only %d components; the built-in profiles are not being read", len(components))
	}
	inArtifact := 0
	for _, component := range components {
		if component.Distribution == InArtifact {
			inArtifact++
			if component.SPDX != License {
				t.Errorf("%s is in the artifact under %q, not %s", component.Name, component.SPDX, License)
			}
		}
	}
	if inArtifact != 1 {
		t.Errorf("%d components claim to be in the artifact; only the Companion may", inArtifact)
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
