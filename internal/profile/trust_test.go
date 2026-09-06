package profile

import (
	"strings"
	"testing"
	"time"
)

func TestTrustTransitions(t *testing.T) {
	cases := []struct {
		from  Trust
		event Event
		want  Trust
		fails bool
		why   string
	}{
		{"", EventInstallBuiltin, TrustBuiltin, false, "the release's own documents arrive built in"},
		{"", EventImportSigned, TrustVerified, false, "a catalogue signature over this digest"},
		{"", EventImportUnsigned, TrustCommunity, false, "a file from anywhere else"},
		{"", EventAuthorLocally, TrustLocal, false, "written here, or found in the profile folder"},

		{TrustVerified, EventEdit, TrustLocal, false,
			"a signature covers bytes; changing the bytes does not produce a differently-signed document"},
		{TrustCommunity, EventEdit, TrustLocal, false, "same rule"},
		{TrustLocal, EventEdit, TrustLocal, false, "editing a local profile leaves it local"},

		{TrustCommunity, EventSignatureVerified, TrustVerified, false, "a signature can arrive later"},
		{TrustVerified, EventSignatureLost, TrustCommunity, false, "a revoked key demotes, visibly"},
		{TrustCommunity, EventSignatureLost, "", true, "only a verified profile has a signature to lose"},

		{TrustBuiltin, EventEdit, "", true, "a built-in profile is part of the build"},
		{TrustBuiltin, EventImportSigned, "", true, "and cannot be re-provenanced at run time"},
		{TrustBuiltin, EventSignatureLost, "", true, "nor demoted"},
		{TrustBuiltin, EventFork, TrustLocal, false, "editing a built-in forks it; the original stays in the build"},

		{TrustCommunity, EventInstallBuiltin, "", true, "nothing is promoted to built in, ever"},
		{TrustLocal, EventInstallBuiltin, "", true, "built in is a fact about the release"},
	}
	for _, c := range cases {
		got, err := Transition(c.from, c.event)
		switch {
		case c.fails && err == nil:
			t.Errorf("%q + %q was allowed and became %q; %s", c.from, c.event, got, c.why)
		case !c.fails && err != nil:
			t.Errorf("%q + %q was refused (%v); %s", c.from, c.event, err, c.why)
		case !c.fails && got != c.want:
			t.Errorf("%q + %q became %q, want %q; %s", c.from, c.event, got, c.want, c.why)
		}
	}
}

func TestRefusedTransitionsExplainThemselves(t *testing.T) {
	_, err := Transition(TrustBuiltin, EventEdit)
	if err == nil {
		t.Fatal("editing a built-in profile was allowed")
	}
	if !strings.Contains(err.Error(), "fork it instead") {
		t.Errorf("the refusal does not say what to do instead: %v", err)
	}
}

// `local` is not the friendly state. A file in the profile folder was put there
// by something, and "something" includes an installer, a sync client and an
// extracted archive.
func TestLocalIsNotTreatedAsVouchedFor(t *testing.T) {
	if TrustLocal.Vouched() {
		t.Error("a local profile reports itself as vouched for")
	}
	if TrustCommunity.Vouched() {
		t.Error("a community profile reports itself as vouched for")
	}
	if !TrustBuiltin.Vouched() || !TrustVerified.Vouched() {
		t.Error("a built-in or verified profile does not report itself as vouched for")
	}
	if !strings.Contains(TrustLocal.Describe(), "cannot tell who put it there") {
		t.Errorf("the description of `local` does not say what it does not know: %q", TrustLocal.Describe())
	}
}

// Importing is inert: a community profile can be read, validated, digested and
// shown, and can do nothing at all until a grant exists.
func TestImportingIsInert(t *testing.T) {
	p := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	digest, err := Digest(p)
	if err != nil {
		t.Fatalf("%v", err)
	}

	err = Authorize(p, TrustCommunity, digest, nil)
	if err == nil {
		t.Fatal("a freshly imported profile was authorized with no grant")
	}
	notGranted, ok := err.(*NotGrantedError)
	if !ok {
		t.Fatalf("the error is not a NotGrantedError: %T", err)
	}
	if notGranted.Reason != "not_reviewed" {
		t.Errorf("reason is %q, want not_reviewed", notGranted.Reason)
	}
	if len(notGranted.Missing) == 0 {
		t.Error("the refusal does not say what would have to be granted")
	}

	// The same document, once granted, runs.
	grant := &Grant{
		ProfileID: p.Metadata().ID, Version: p.Metadata().Version, Digest: digest,
		Trust: TrustCommunity, Granted: PermissionIDs(p), GrantedAt: time.Now().UTC(),
	}
	if err := Authorize(p, TrustCommunity, digest, grant); err != nil {
		t.Fatalf("a granted profile was refused: %v", err)
	}

	// A grant is against a digest, not a version. Change the bytes and it stops
	// applying, whatever the version number says.
	if err := Authorize(p, TrustCommunity, "sha256:"+strings.Repeat("0", 64), grant); err == nil {
		t.Error("a grant covered a document it was not taken against")
	}
}

func TestBuiltinIsAuthorizedByHavingBeenInstalled(t *testing.T) {
	p := decodeFixture(t, "valid/minimal.tool.json")
	digest, _ := Digest(p)
	if err := Authorize(p, TrustBuiltin, digest, nil); err != nil {
		t.Errorf("a built-in profile needed a grant: %v", err)
	}
}

// The update case: a profile that asks for more than was approved is refused
// until the user has seen the new sentences, and the refusal lists only the new
// ones — a second review long enough to be read.
func TestCapabilityEscalationIsRefusedAndTheDiffNamesIt(t *testing.T) {
	before := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	after := decodeFixture(t, "community/user-q1-toolchain-v2.tool.json")

	beforeDigest, _ := Digest(before)
	afterDigest, _ := Digest(after)
	if beforeDigest == afterDigest {
		t.Fatal("the two fixtures have the same digest")
	}

	grant := &Grant{
		ProfileID: before.Metadata().ID, Version: before.Metadata().Version, Digest: afterDigest,
		Trust: TrustCommunity, Granted: PermissionIDs(before), GrantedAt: time.Now().UTC(),
	}
	err := Authorize(after, TrustCommunity, afterDigest, grant)
	if err == nil {
		t.Fatal("the widened profile was authorized against the old grant")
	}
	notGranted, ok := err.(*NotGrantedError)
	if !ok {
		t.Fatalf("the error is not a NotGrantedError: %T", err)
	}
	if notGranted.Reason != "permissions_widened" {
		t.Errorf("reason is %q, want permissions_widened", notGranted.Reason)
	}
	// Three, not two: write access to the game folder implies read access to
	// it, and the review says both. A summary that mentioned only the write
	// would be understating what was being approved.
	wantNew := map[string]bool{PermWrite(RootGame): true, PermRead(RootGame): true, PermNetwork: true}
	if len(notGranted.Missing) != len(wantNew) {
		t.Errorf("expected %d new permissions, got %d: %+v", len(wantNew), len(notGranted.Missing), notGranted.Missing)
	}
	for _, permission := range notGranted.Missing {
		if !wantNew[permission.ID] {
			t.Errorf("an already-granted permission was reported as missing: %+v", permission)
		}
	}

	diff, err := DiffProfiles(before, after)
	if err != nil {
		t.Fatalf("DiffProfiles: %v", err)
	}
	if !diff.Escalates() {
		t.Error("the diff does not report the update as an escalation")
	}
	if len(diff.PermissionsAdded) != len(wantNew) {
		t.Errorf("the diff reports %d added permissions, want %d: %+v", len(diff.PermissionsAdded), len(wantNew), diff.PermissionsAdded)
	}
	rendered := diff.String()
	for _, want := range []string{"It now asks to:", "updates.example.com", "game folder"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the rendered diff does not mention %q:\n%s", want, rendered)
		}
	}
	if !strings.Contains(rendered, "actions[0].network") {
		t.Errorf("the change list does not locate the new network member:\n%s", rendered)
	}
}

func TestDiffOfAnUnchangedDocumentIsEmpty(t *testing.T) {
	p := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	same := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	diff, err := DiffProfiles(p, same)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !diff.Empty() {
		t.Errorf("a document differs from itself:\n%s", diff.String())
	}
	if diff.Escalates() {
		t.Error("an unchanged document reports an escalation")
	}
}

func TestDiffOfADifferentProfileIsNotAnUpdate(t *testing.T) {
	before := decodeFixture(t, "valid/minimal.tool.json")
	after := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	diff, err := DiffProfiles(before, after)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !diff.IdentityChanged {
		t.Error("a document with a different id was presented as an update")
	}
	if !diff.Escalates() {
		t.Error("a changed identity does not require a fresh decision")
	}
}

func TestFirstImportIsNotRenderedAsAChangeList(t *testing.T) {
	p := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	diff, err := DiffProfiles(nil, p)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !diff.FirstImport {
		t.Error("a first import was not marked as one")
	}
	if len(diff.Changes) != 0 {
		t.Errorf("a first import produced a change list of %d entries", len(diff.Changes))
	}
	if len(diff.PermissionsAdded) == 0 {
		t.Error("a first import lists no permissions to approve")
	}
}

// A pipeline asks for nothing on its own account: every permission belongs to
// the tool that resolves one of its steps and is granted there.
func TestAPipelineGrantsNothing(t *testing.T) {
	p := decodeFixture(t, "../builtin/sample-q1-normal.pipeline.json")
	if len(p.Permissions()) != 0 {
		t.Errorf("a pipeline asked for %d permissions: %+v", len(p.Permissions()), p.Permissions())
	}
}

// A published version is immutable. Changing what a version says, rather than
// publishing a new one, is how a reviewed and approved profile silently becomes
// a different program — and the version number, which is what a catalogue, a
// changelog and a person all refer to, would keep pointing at both.
func TestRepublishingAVersionIsRefusedRatherThanTreatedAsAnUpdate(t *testing.T) {
	installed := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	incoming := decodeFixture(t, "community/user-q1-toolchain-v2.tool.json")

	// As shipped, the two fixtures are an ordinary update: different versions.
	if err := CheckVersionImmutable(installed, incoming); err != nil {
		t.Fatalf("an ordinary update was reported as a republication: %v", err)
	}

	// Now the same version number over different bytes.
	incoming.(*ToolProfile).Version = installed.Metadata().Version
	err := CheckVersionImmutable(installed, incoming)
	if err == nil {
		t.Fatal("a republished version was accepted")
	}
	republished, ok := err.(*RepublishedError)
	if !ok {
		t.Fatalf("the error is not a RepublishedError: %T", err)
	}
	if republished.Installed == republished.Incoming {
		t.Error("the error reports the same digest twice")
	}
	if !strings.Contains(err.Error(), "immutable") {
		t.Errorf("the error does not state the rule: %v", err)
	}

	diff, err := DiffProfiles(installed, incoming)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !diff.VersionRepublished {
		t.Error("the diff does not report the republication")
	}
	if !diff.Escalates() {
		t.Error("a republished version does not require a fresh decision")
	}
	if !strings.Contains(diff.String(), "should have been a new version") {
		t.Errorf("the rendered diff does not explain what went wrong:\n%s", diff.String())
	}
}

// A profile compared with itself is not a republication, and two unrelated
// profiles that happen to share a version number are not either.
func TestVersionImmutabilityDoesNotFireOnUnrelatedDocuments(t *testing.T) {
	p := decodeFixture(t, "community/user-q1-toolchain.tool.json")
	if err := CheckVersionImmutable(p, p); err != nil {
		t.Errorf("a document compared with itself was called a republication: %v", err)
	}
	other := decodeFixture(t, "valid/minimal.tool.json")
	other.(*ToolProfile).Version = p.Metadata().Version
	if err := CheckVersionImmutable(p, other); err != nil {
		t.Errorf("two different profiles sharing a version number were called a republication: %v", err)
	}
	if err := CheckVersionImmutable(nil, p); err != nil {
		t.Errorf("a first import was called a republication: %v", err)
	}
}
