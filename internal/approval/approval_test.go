package approval

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// The profile these tests approve: a hand-written TOOL profile, which is the
// document AUT/AUCOM 219 found could be validated, shown, digested and bound
// from the command line and then only ever RUN through the local HTTP API.
const harmless = "example.local.harmless"

// machine is a throwaway one: a profile directory, a binding file, and the
// service over both.
type machine struct {
	t        *testing.T
	dir      string
	profiles string
	bindings string
	service  Service
}

func newMachine(t *testing.T, documents ...string) *machine {
	t.Helper()
	dir := t.TempDir()
	profiles := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(profiles, 0o700); err != nil {
		t.Fatal(err)
	}
	m := &machine{
		t: t, dir: dir, profiles: profiles,
		bindings: filepath.Join(dir, "bindings.json"),
	}
	for _, name := range documents {
		m.install(name)
	}
	m.service = Service{
		Catalog:      job.NewCatalog(profiles),
		BindingsPath: m.bindings,
	}
	return m
}

// install copies one testdata document into the machine's profile directory,
// which is exactly what dropping a file in there by hand does.
func (m *machine) install(name string) {
	m.t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		m.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.profiles, "document.json"), raw, 0o600); err != nil {
		m.t.Fatal(err)
	}
}

func (m *machine) review(id string) Decision {
	m.t.Helper()
	decision, err := m.service.Review(id)
	if err != nil {
		m.t.Fatalf("Review(%s): %v", id, err)
	}
	return decision
}

// Importing is inert, and so is binding. A document in the profile directory is
// visible, describable and digestible, and it may not run: that separation is
// the whole trust model, and a route that granted on the way past would delete
// it.
func TestImportingAndBindingGrantNothing(t *testing.T) {
	m := newMachine(t, "local-tool.tool.json")

	decision := m.review(harmless)
	if decision.Authorized {
		t.Fatal("a document that was merely dropped into the profile directory is authorized")
	}
	var notGranted *profile.NotGrantedError
	if !errors.As(decision.AuthorizationError, &notGranted) || notGranted.Reason != "not_reviewed" {
		t.Errorf("the refusal is %v, and should say nothing has been reviewed", decision.AuthorizationError)
	}
	if decision.Binding.Grant != nil {
		t.Error("a profile nobody approved has a grant")
	}

	// And a binding — where the program is on this machine — still grants
	// nothing. This is the case `acquire resolve --bind` produces.
	if _, err := binding.Update(m.bindings, func(set *binding.Set) error {
		return set.Put(binding.LocalBinding{
			ProfileID: harmless, ProfileVersion: "1.0.0",
			ProfileDigest: decision.Entry.Digest, Trust: profile.TrustLocal,
			Acquisition: profile.AcquireUserPath,
			Executables: map[string]string{"harmless": filepath.Join(m.dir, "harmless")},
		})
	}); err != nil {
		t.Fatal(err)
	}
	if bound := m.review(harmless); bound.Authorized {
		t.Error("binding a profile authorized it; binding says where a program is, not that it may run")
	}
}

// An approval names the document it approves. Not "whatever is on disk when the
// request arrives" — that would be an approval of something nobody read.
func TestAnApprovalNamesTheExactDocumentItApproves(t *testing.T) {
	m := newMachine(t, "local-tool.tool.json")
	current := m.review(harmless).Entry.Digest

	if _, err := m.service.Grant(harmless, ""); !errors.Is(err, ErrDigestRequired) {
		t.Errorf("granting with no digest returned %v, want ErrDigestRequired", err)
	}
	var stale *StaleDigestError
	_, err := m.service.Grant(harmless, "sha256:"+strings.Repeat("00", 32))
	if !errors.As(err, &stale) {
		t.Fatalf("granting against a digest that is not this document returned %v", err)
	}
	if stale.Current != current || stale.Reviewed == current {
		t.Errorf("the refusal names %q and %q; it should name what was reviewed and what is here now",
			stale.Reviewed, stale.Current)
	}
	if !strings.Contains(stale.Error(), current) || !strings.Contains(stale.Error(), harmless) {
		t.Errorf("the message does not give a person what to look at: %s", stale)
	}
	// Nothing was written by either refusal.
	if _, err := os.Stat(m.bindings); !errors.Is(err, os.ErrNotExist) {
		t.Error("a refused approval wrote a binding file")
	}

	decision, err := m.service.Grant(harmless, current)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if !decision.Authorized {
		t.Fatalf("the profile is still not authorized: %v", decision.AuthorizationError)
	}
	grant := decision.Binding.Grant
	if grant == nil || grant.Digest != current {
		t.Fatalf("the stored grant is %+v, and should cover %s", grant, current)
	}
	if want := profile.PermissionIDs(decision.Entry.Profile); len(grant.Granted) != len(want) {
		t.Errorf("the grant lists %d permissions and the document asks for %d", len(grant.Granted), len(want))
	}
}

// The half a permission-set comparison would get wrong.
//
// The replacement document asks for STRICTLY LESS than the approved one did:
// everything in it was already approved. It still may not run, because a grant
// is against bytes, not against a set of capabilities — and "the author only
// ever removes things" is not something a reader can check.
func TestAChangedDocumentInvalidatesTheGrantEvenWhenItAsksForLess(t *testing.T) {
	m := newMachine(t, "local-tool.tool.json")
	before := m.review(harmless)
	if _, err := m.service.Grant(harmless, before.Entry.Digest); err != nil {
		t.Fatal(err)
	}

	m.install("local-tool-narrower.tool.json")
	after := m.review(harmless)
	if after.Entry.Digest == before.Entry.Digest {
		t.Fatal("the fixtures are the same document; this test proves nothing")
	}
	// The narrower claim, asserted rather than assumed: if the second document
	// ever grows a permission, this test stops being about what it says it is.
	wider := map[string]bool{}
	for _, id := range profile.PermissionIDs(before.Entry.Profile) {
		wider[id] = true
	}
	for _, id := range profile.PermissionIDs(after.Entry.Profile) {
		if !wider[id] {
			t.Fatalf("the replacement asks for %q, which the approved document did not; it is not narrower", id)
		}
	}

	if after.Authorized {
		t.Fatal("a document that changed after it was approved is still authorized")
	}
	var notGranted *profile.NotGrantedError
	if !errors.As(after.AuthorizationError, &notGranted) || notGranted.Reason != "document_changed" {
		t.Errorf("the refusal is %v, and should say the document changed", after.AuthorizationError)
	}
	if grant := after.Binding.Grant; grant == nil || grant.Digest != before.Entry.Digest {
		t.Error("the old approval was rewritten rather than left covering the document it covered")
	}

	// And the new one is approvable, as itself.
	if _, err := m.service.Grant(harmless, before.Entry.Digest); !errors.As(err, new(*StaleDigestError)) {
		t.Errorf("the digest that was approved before is still accepted: %v", err)
	}
	granted, err := m.service.Grant(harmless, after.Entry.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if !granted.Authorized {
		t.Errorf("approving the document that is here did not authorize it: %v", granted.AuthorizationError)
	}
}

// Withdrawing is about the decision and nothing else. A withdrawal that also
// forgot where the user's programs are would be one people are afraid to use,
// and a decision people are afraid to take back is not really a decision.
func TestWithdrawingTakesTheDecisionBackAndLeavesTheSetupAlone(t *testing.T) {
	m := newMachine(t, "local-tool.tool.json")
	digest := m.review(harmless).Entry.Digest
	where := filepath.Join(m.dir, "harmless")
	if _, err := binding.Update(m.bindings, func(set *binding.Set) error {
		return set.Put(binding.LocalBinding{
			ProfileID: harmless, ProfileVersion: "1.0.0", ProfileDigest: digest,
			Trust: profile.TrustLocal, Acquisition: profile.AcquireUserPath,
			Executables: map[string]string{"harmless": where},
			Roots:       map[string]string{profile.RootContent: m.dir},
		})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.service.Grant(harmless, digest); err != nil {
		t.Fatal(err)
	}

	after, err := m.service.Withdraw(harmless)
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if after.Authorized {
		t.Error("a withdrawn approval still authorizes the profile")
	}
	if after.Binding.Grant != nil {
		t.Error("the grant survived the withdrawal")
	}
	if after.Binding.Executables["harmless"] != where {
		t.Errorf("withdrawing forgot where the program is: %q", after.Binding.Executables["harmless"])
	}
	if after.Binding.Roots[profile.RootContent] != m.dir {
		t.Error("withdrawing forgot a root the user chose")
	}

	// Withdrawing what was never granted is the state the caller asked for, not
	// a failure — a script that runs it twice must not exit non-zero.
	if _, err := m.service.Withdraw(harmless); err != nil {
		t.Errorf("withdrawing twice failed: %v", err)
	}
}

// Two instances is the normal case: the GUI server is one process and a
// `companion` command in a terminal is another. Under that, a grant and a
// withdrawal racing each other must not take an unrelated profile's setup with
// them — which an unlocked read-modify-write over one JSON file would.
func TestConcurrentGrantsAndWithdrawalsLoseNoUnrelatedBinding(t *testing.T) {
	m := newMachine(t, "local-tool.tool.json")
	digest := m.review(harmless).Entry.Digest

	// The bystander: a profile nobody in this test touches, whose binding must
	// come out the other side byte for byte.
	const bystander = "example.local.untouched"
	where := filepath.Join(m.dir, "engine")
	if _, err := binding.Update(m.bindings, func(set *binding.Set) error {
		return set.Put(binding.LocalBinding{
			ProfileID: bystander, ProfileVersion: "2.0.0",
			ProfileDigest: "sha256:" + strings.Repeat("ab", 32),
			Trust:         profile.TrustLocal, Acquisition: profile.AcquireUserPath,
			Executables: map[string]string{"engine": where},
		})
	}); err != nil {
		t.Fatal(err)
	}

	const rounds = 12
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for n := 0; n < rounds; n++ {
				var err error
				if (worker+n)%2 == 0 {
					_, err = m.service.Grant(harmless, digest)
				} else {
					_, err = m.service.Withdraw(harmless)
				}
				if err != nil {
					t.Errorf("worker %d round %d: %v", worker, n, err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()

	set, err := binding.LoadFile(m.bindings)
	if err != nil {
		t.Fatalf("the binding file did not survive: %v", err)
	}
	untouched, found := set.Find(bystander)
	if !found {
		t.Fatal("an unrelated binding was lost")
	}
	if untouched.Executables["engine"] != where || untouched.ProfileVersion != "2.0.0" {
		t.Errorf("an unrelated binding was corrupted: %+v", untouched)
	}
	// Whatever the last writer decided, the file says one coherent thing about
	// it: a grant, if there is one, covers the document that is here.
	local, _ := set.Find(harmless)
	if local.Grant != nil && local.Grant.Digest != digest {
		t.Errorf("the surviving grant covers %s and the document is %s", local.Grant.Digest, digest)
	}
}

// A built-in document is authorized by having been installed, and an approval
// does not change what it is. The trust states are what stop a file that
// appeared in a directory being treated as one that shipped with the program.
func TestABuiltinKeepsItsTrustAndNeedsNoGrant(t *testing.T) {
	m := newMachine(t)
	const builtinID = "auto-pigeon.ericw-tools.q1"

	decision := m.review(builtinID)
	if decision.Entry.Trust != profile.TrustBuiltin {
		t.Fatalf("the built-in profile is %q", decision.Entry.Trust)
	}
	if !decision.Authorized || decision.Binding.Grant != nil {
		t.Error("a built-in document either is not authorized or needed a grant to be")
	}
	granted, err := m.service.Grant(builtinID, decision.Entry.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if granted.Entry.Trust != profile.TrustBuiltin || granted.Binding.Trust != profile.TrustBuiltin {
		t.Error("approving a built-in document changed its trust state")
	}
	// And withdrawing does not make a built-in unrunnable: it was never the
	// grant that authorized it.
	after, err := m.service.Withdraw(builtinID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Authorized {
		t.Error("withdrawing an approval of a built-in document made it unrunnable")
	}
}

// A pipeline is several jobs. Approving it approves the pipeline, and each tool
// it drives is still a separate document with a separate decision — which is
// what stops "approve the build" being a way to approve every compiler in it.
func TestApprovingAPipelineGrantsNothingToTheToolsItRuns(t *testing.T) {
	m := newMachine(t, "local-tool.tool.json")
	const pipeline = "auto-pigeon.q1.normal"

	decision := m.review(pipeline)
	granted, err := m.service.Grant(pipeline, decision.Entry.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if !granted.Authorized {
		t.Fatalf("the pipeline is not authorized: %v", granted.AuthorizationError)
	}
	if tool := m.review(harmless); tool.Authorized {
		t.Error("approving a pipeline authorized a tool profile")
	}
	for _, id := range profile.PermissionIDs(granted.Entry.Profile) {
		if strings.Contains(id, "execute") {
			t.Errorf("the pipeline's own permissions include %q; a pipeline runs nothing itself", id)
		}
	}
}

// A document that is not a valid profile never becomes one this can approve.
// The catalog refuses to list a directory holding it, by name, so there is no
// state in which a malicious document is present and grantable.
func TestAnInvalidDocumentCannotBeReviewedOrGranted(t *testing.T) {
	m := newMachine(t)
	malicious := filepath.Join("..", "profile", "testdata", "malicious", "shell-chaining.tool.json")
	raw, err := os.ReadFile(malicious)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.profiles, "malicious.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct {
		name string
		run  func() error
	}{
		{"Review", func() error { _, err := m.service.Review(harmless); return err }},
		{"Grant", func() error { _, err := m.service.Grant(harmless, "sha256:x"); return err }},
		{"Withdraw", func() error { _, err := m.service.Withdraw(harmless); return err }},
	} {
		err := call.run()
		if err == nil {
			t.Errorf("%s succeeded with an invalid document in the profile directory", call.name)
			continue
		}
		if !strings.Contains(err.Error(), "malicious.json") {
			t.Errorf("%s did not say which file is the problem: %v", call.name, err)
		}
	}
	if _, err := os.Stat(m.bindings); !errors.Is(err, os.ErrNotExist) {
		t.Error("something was written while the catalog could not be read")
	}
}

// The clock is a field so a record can be asserted rather than approximated.
func TestTheGrantRecordsWhenTheDecisionWasTaken(t *testing.T) {
	m := newMachine(t, "local-tool.tool.json")
	when := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	m.service.Now = func() time.Time { return when }

	digest := m.review(harmless).Entry.Digest
	decision, err := m.service.Grant(harmless, digest)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Binding.Grant.GrantedAt.Equal(when) {
		t.Errorf("the grant is stamped %s, want %s", decision.Binding.Grant.GrantedAt, when)
	}
}
