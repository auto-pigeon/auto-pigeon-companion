package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/acquire"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// AUCOM/AUT 246I: a build blocked on a missing compiler used to say "This
// stage's program is not set up on this machine yet. Say where it is, once, in
// Profiles." — and there was no route here at all, so the page could not have
// offered to fetch it even though the Companion knows how.

// TestAcquireOfferNamesTheMissingTrustAnchorAndKeepsTheFolderRoute is the case
// the Windows host of 246I actually met.
//
// `companion acquire plan ericw-tools.q1` refused there with "no catalogue trust
// anchor is configured", which means an honest page must NOT offer "Download and
// set up": the answer is a concrete condition plus the route that still works.
func TestAcquireOfferNamesTheMissingTrustAnchorAndKeepsTheFolderRoute(t *testing.T) {
	m := newMachine(t)

	status, body := m.call(http.MethodGet, "/api/v1/profiles/auto-pigeon.ericw-tools.q1/acquire", nil)
	if status != http.StatusOK {
		t.Fatalf("offer = %d %v", status, body)
	}
	if body["available"] != false {
		t.Errorf("a machine with no trust anchor offered a verified download: %v", body)
	}
	reason, _ := body["reason"].(string)
	if !strings.Contains(strings.ToLower(reason), "trust anchor") {
		t.Errorf("reason does not name the condition: %q", reason)
	}
	// The route that needs no network, no catalogue and no anchor has to
	// survive, or the refusal above leaves the user with nothing to do.
	if body["folder"] != true {
		t.Errorf("the folder route was not offered: %v", body)
	}
	if hint, _ := body["hint"].(string); hint == "" {
		t.Error("the folder route was offered without saying what to point at")
	}
	// The profile DOES publish a windows/amd64 build, so the package is named
	// even though this machine cannot verify one. A reason of "no build for
	// this platform" here would be a different, wrong diagnosis.
	if body["package"] != "ericw-tools.q1" {
		t.Errorf("package = %v, want ericw-tools.q1", body["package"])
	}
	// Nothing about an unverified fallback, ever.
	if _, leaked := body["url"]; leaked {
		t.Errorf("the offer carried a download URL: %v", body)
	}
}

// TestAcquireRefusesToDownloadWithoutATrustAnchorAndSaysWhatToDoInstead:
// asking anyway is refused, and the refusal is not a dead end.
func TestAcquireRefusesToDownloadWithoutATrustAnchorAndSaysWhatToDoInstead(t *testing.T) {
	m := newMachine(t)

	status, body := m.call(http.MethodPost, "/api/v1/profiles/auto-pigeon.ericw-tools.q1/acquire",
		map[string]any{})
	if status != http.StatusConflict {
		t.Fatalf("install without an anchor = %d %v, want 409", status, body)
	}
	message, _ := body["error"].(string)
	if !strings.Contains(strings.ToLower(message), "trust anchor") {
		t.Errorf("refusal does not name the condition: %q", message)
	}
	if !strings.Contains(strings.ToLower(message), "folder") {
		t.Errorf("refusal does not point at the route that still works: %q", message)
	}
	// And it wrote nothing: a refused acquisition is not a binding.
	set, err := binding.LoadFile(m.bindings)
	if err == nil {
		if _, exists := set.Find("auto-pigeon.ericw-tools.q1"); exists {
			t.Error("a refused download recorded a binding")
		}
	}
}

// TestAcquireRefusesAProfileThatHasNoProgramsToObtain keeps the route honest
// about what it is for: a pipeline declares steps, not executables.
func TestAcquireRefusesAProfileThatHasNoProgramsToObtain(t *testing.T) {
	m := newMachine(t)

	status, body := m.call(http.MethodGet, "/api/v1/profiles/auto-pigeon.q1.fast-preview/acquire", nil)
	if status < 400 {
		t.Fatalf("a pipeline was offered an acquisition: %d %v", status, body)
	}
}

// TestAnAcquisitionRecordsWhereTheProgramsAreAndGrantsNothing is the invariant
// internal/approval owns: resolving an acquisition says where a program is, and
// says nothing about whether the user approved what it does.
//
// It drives recordAcquisition directly because the refusals above mean no real
// download happens on a machine without an anchor, and this rule has to hold
// regardless of how the resolution was reached.
func TestAnAcquisitionRecordsWhereTheProgramsAreAndGrantsNothing(t *testing.T) {
	m := newMachine(t)
	entry, _, err := m.server.profileEntry("auto-pigeon.ericw-tools.q1")
	if err != nil {
		t.Fatal(err)
	}

	written, err := m.server.recordAcquisition(entry.Profile, &acquire.Result{
		Mode:        profile.AcquireManagedDownload,
		ToolRoot:    m.dir,
		Executables: map[string]string{"qbsp": m.dir + "/bin/qbsp", "vis": m.dir + "/bin/vis"},
		Description: "a verified download",
	})
	if err != nil {
		t.Fatal(err)
	}
	if written.Grant != nil {
		t.Error("acquiring a tool granted it permission to run")
	}
	if written.Acquisition != profile.AcquireManagedDownload {
		t.Errorf("acquisition = %q", written.Acquisition)
	}
	if written.Executables["qbsp"] == "" {
		t.Error("the resolved executables were not recorded")
	}
	if written.Roots[profile.RootToolInstall] != m.dir {
		t.Errorf("tool root = %q, want %q", written.Roots[profile.RootToolInstall], m.dir)
	}

	// And it survives a restart, which is the whole point of recording it.
	set, err := binding.LoadFile(m.bindings)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, exists := set.Find("auto-pigeon.ericw-tools.q1")
	if !exists {
		t.Fatal("the binding did not survive being written")
	}
	if reloaded.Executables["vis"] == "" || reloaded.Grant != nil {
		t.Errorf("reloaded binding = %+v", reloaded)
	}
}
