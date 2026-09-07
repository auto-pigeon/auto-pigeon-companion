package publish_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile/builtin"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/publish"
)

// ---- a stub AUB, so these tests are about this package -------------------------------------------

// stub serves the three catalog routes this package uses. It is deliberately
// simple-minded: what is under test here is what the Companion does with what it
// is served, and AUB's own rules are contested in AUB's own suite against its
// real collections.
type stub struct {
	server *httptest.Server

	// document is what the version route serves, and digest is what it claims.
	// They are separate fields ON PURPOSE, so a test can make them disagree.
	document []byte
	digest   string
	trust    string
	yanked   bool
	yankWhy  string

	// published records what a publication actually sent.
	published map[string]any
}

func newStub(t testing.TB, document []byte) *stub {
	t.Helper()

	sum := sha256.Sum256(document)
	s := &stub{
		document: document,
		digest:   "sha256:" + hex.EncodeToString(sum[:]),
		trust:    "community",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/companion-profiles", func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}
		s.published = body
		write(w, map[string]any{
			"schema_version": aub.ProfileCatalogSchema,
			"created":        true,
			"profile":        map[string]any{"id": "listing1", "profile_id": "x", "trust": "community"},
			"version":        map[string]any{"version": "1.0.0", "digest": body["digest"]},
		})
	})
	mux.HandleFunc("GET /api/companion-profiles/{id}", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"schema_version": aub.ProfileCatalogSchema,
			"profile": map[string]any{
				"id": "listing1", "profile_id": "x", "kind": "tool",
				"trust": s.trust, "latest_version": "1.0.0", "visibility": "public",
			},
			"versions": []any{map[string]any{"version": "1.0.0", "digest": s.digest}},
		})
	})
	mux.HandleFunc("GET /api/companion-profiles/{id}/versions/{version}",
		func(w http.ResponseWriter, r *http.Request) {
			write(w, map[string]any{
				"schema_version": aub.ProfileCatalogSchema,
				"profile_id":     "x",
				"kind":           "tool",
				"trust":          s.trust,
				"version": map[string]any{
					"version": r.PathValue("version"), "digest": s.digest,
					"document": string(s.document), "yanked": s.yanked,
					"yank_reason": s.yankWhy,
				},
			})
		})
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)

	return s
}

func write(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}

func (s *stub) client(t testing.TB) *aub.Client {
	t.Helper()

	client, err := aub.New(s.server.URL, s.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.SetToken("test-token")

	return client
}

// canonicalBuiltin is a real curated document, under a community id, in its
// canonical form — the same bytes AUCOM would publish and AUB would store.
//
// The id is changed because a built-in's id is already claimed by this build, and
// the local catalog refuses a directory document that shadows one. That refusal is
// correct and is `internal/job`'s to make; what these tests are about is a
// document somebody else published, which is exactly what a community id is.
func canonicalBuiltin(t testing.TB) ([]byte, profile.Profile) {
	t.Helper()

	entries, err := builtin.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		tool, ok := entry.Profile.(*profile.ToolProfile)
		if !ok {
			continue
		}
		community := *tool
		community.ID = "community.example.q1-tools"
		canonical, err := profile.Export(&community)
		if err != nil {
			t.Fatal(err)
		}

		return canonical, &community
	}
	t.Fatal("no built-in tool profile")

	return nil, nil
}

func paths(t testing.TB) publish.InstallPaths {
	t.Helper()

	root := t.TempDir()

	return publish.InstallPaths{
		Profiles: filepath.Join(root, "profiles"),
		Bindings: filepath.Join(root, "bindings.json"),
	}
}

// ---- the export gate -----------------------------------------------------------------------------

// TestNothingLocalCanReachAPreview is the regression the acceptance asks for.
//
// Not "the publish route refuses it" — the document cannot be PREVIEWED, which
// means there is no path from a local path, a token, an environment secret, a
// download credential or a machine identifier to a publication, because the only
// thing that produces the bytes to publish is the function that refuses them.
func TestNothingLocalCanReachAPreview(t *testing.T) {
	_, document := canonicalBuiltin(t)
	tool, ok := document.(*profile.ToolProfile)
	if !ok {
		t.Fatal("the built-in tool profile is not a *ToolProfile")
	}

	for name, leak := range map[string]string{
		"a local path":            "/home/vera/quake/id1",
		"a home directory":        "~/quake",
		"a loopback address":      "http://127.0.0.1:9190/api/status",
		"a private LAN address":   "192.168.0.33:5174",
		"a session token":         "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6ImFiY2RlZmdoIn0.c2lnbmF0dXJlX2hlcmU",
		"a download credential":   "https://vera:hunter2@example.invalid/qbsp.zip",
		"an authorization header": "Authorization: Bearer abcdefghij",
	} {
		t.Run(name, func(t *testing.T) {
			leaked := *tool
			leaked.Summary = leak

			if _, err := publish.PreviewOf(&leaked); err == nil {
				t.Fatal("a document naming one machine was previewed, and could therefore be published")
			}
			// And the same document cannot be exported either, which is the same
			// refusal reached from the other direction — there is one encoder.
			if _, err := profile.Export(&leaked); err == nil {
				t.Fatal("it was exportable")
			}
		})
	}
}

// TestABindingsOwnValuesCannotBePublished.
//
// The type system already makes a binding unrepresentable inside a profile; this
// is the content half — somebody copying the paths OUT of their binding and
// pasting them into a description.
func TestABindingsOwnValuesCannotBePublished(t *testing.T) {
	local := binding.LocalBinding{
		ProfileID:   "ericw.tools",
		Executables: map[string]string{"qbsp": "/home/vera/tools/qbsp"},
		Roots:       map[string]string{"tool_root": "/home/vera/tools"},
	}
	_, document := canonicalBuiltin(t)
	tool := document.(*profile.ToolProfile)

	for _, value := range append(values(local.Executables), values(local.Roots)...) {
		leaked := *tool
		leaked.Description = value
		if _, err := publish.PreviewOf(&leaked); err == nil {
			t.Fatalf("a binding's own path (%s) reached a publication", value)
		}
	}
}

func values(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, value := range m {
		out = append(out, value)
	}

	return out
}

// TestAPreviewSaysWhatWouldBeDisclosedAndSendsNothing.
func TestAPreviewSaysWhatWouldBeDisclosedAndSendsNothing(t *testing.T) {
	canonical, document := canonicalBuiltin(t)
	stub := newStub(t, canonical)

	preview, err := publish.PreviewOf(document)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(preview.Canonical, canonical) {
		t.Fatal("the preview's bytes are not the canonical form")
	}
	if len(preview.Disclosures) == 0 {
		t.Fatal("a preview that discloses nothing is a preview nobody should trust")
	}
	if len(preview.Permissions) == 0 {
		t.Fatal("a compiler profile asks for nothing?")
	}
	for _, phrase := range []string{"does not relicense", "independent program"} {
		if !strings.Contains(preview.LicenceNote, phrase) {
			t.Fatalf("the licence note does not say %q", phrase)
		}
	}

	// Nothing has been sent, and an unconfirmed publish still sends nothing.
	if _, err := publish.Publish(context.Background(), stub.client(t), preview, "public", false); !errors.Is(err, publish.ErrNotConfirmed) {
		t.Fatalf("err = %v, want ErrNotConfirmed", err)
	}
	if stub.published != nil {
		t.Fatal("an unconfirmed publish reached the network")
	}
}

// TestPublishingSendsTheCanonicalBytesAndTheirDigest.
func TestPublishingSendsTheCanonicalBytesAndTheirDigest(t *testing.T) {
	canonical, document := canonicalBuiltin(t)
	stub := newStub(t, canonical)
	preview, err := publish.PreviewOf(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publish.Publish(context.Background(), stub.client(t), preview, "public", true); err != nil {
		t.Fatal(err)
	}
	sent, _ := stub.published["document"].(string)
	if sent != string(canonical) {
		t.Fatal("what was sent is not byte-identical to the canonical form")
	}
	if stub.published["digest"] != preview.Digest {
		t.Fatalf("digest = %v, want %s", stub.published["digest"], preview.Digest)
	}
	// And the body carries no trust or moderation member for a server to have to
	// refuse.
	for _, forbidden := range []string{"trust", "moderation_state", "verified", "publisher_id"} {
		if _, present := stub.published[forbidden]; present {
			t.Fatalf("the publication sent %q", forbidden)
		}
	}
}

// ---- installing ----------------------------------------------------------------------------------

// TestInstallingRoundTripsTheDocumentByteForByte.
func TestInstallingRoundTripsTheDocumentByteForByte(t *testing.T) {
	canonical, _ := canonicalBuiltin(t)
	stub := newStub(t, canonical)
	where := paths(t)

	plan, err := publish.PlanInstall(context.Background(), stub.client(t), nil, "listing1", "")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Canonical {
		t.Fatal("a canonical document was not recognised as one")
	}
	if plan.Digest != plan.Announced {
		t.Fatalf("digest = %s, announced = %s", plan.Digest, plan.Announced)
	}
	if !plan.FirstInstall {
		t.Fatal("nothing was installed, and this is not reported as a first install")
	}

	local, err := publish.Apply(plan, where, true)
	if err != nil {
		t.Fatal(err)
	}
	name, err := publish.DocumentFileName(plan.Profile().Metadata())
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(filepath.Join(where.Profiles, name))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, canonical) {
		t.Fatal("the document on disk is not the document that was published")
	}
	if local.ProfileDigest != plan.Digest || local.Grant == nil || local.Grant.Digest != plan.Digest {
		t.Fatalf("the binding does not record the digest that was reviewed: %+v", local)
	}

	// And the local catalog now finds it, at the same digest.
	entry, err := job.NewCatalog(where.Profiles).Lookup(plan.Profile().Metadata().ID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Digest != plan.Digest {
		t.Fatalf("catalog digest = %s, want %s", entry.Digest, plan.Digest)
	}
}

// TestADeploymentCannotHandOutTrustThisMachineDidNotCheck.
//
// The rule the package doc turns on. An AUB operator may award `builtin`, which
// in this program's own vocabulary is the ONE state `profile.Authorize` accepts
// without a grant. If that word crossed the boundary, anybody who runs an AUB
// could hand out an unreviewed run.
func TestADeploymentCannotHandOutTrustThisMachineDidNotCheck(t *testing.T) {
	canonical, _ := canonicalBuiltin(t)
	where := paths(t)

	for _, awarded := range []string{"builtin", "verified", "community"} {
		t.Run(awarded, func(t *testing.T) {
			stub := newStub(t, canonical)
			stub.trust = awarded

			plan, err := publish.PlanInstall(context.Background(), stub.client(t), nil, "listing1", "")
			if err != nil {
				t.Fatal(err)
			}
			if plan.DeploymentTrust != awarded {
				t.Fatalf("the deployment's own word was lost: %q", plan.DeploymentTrust)
			}
			if plan.Trust != profile.TrustCommunity {
				t.Fatalf("this machine recorded %q for a deployment that said %q",
					plan.Trust, awarded)
			}

			local, err := publish.Apply(plan, where, true)
			if err != nil {
				t.Fatal(err)
			}
			if local.Trust != profile.TrustCommunity {
				t.Fatalf("the binding recorded %q", local.Trust)
			}

			// And without the grant this install recorded, it would still not run:
			// the authorization is this machine's, at this digest.
			if err := profile.Authorize(plan.Profile(), local.Trust, plan.Digest, nil); err == nil {
				t.Fatal("it would run with no grant at all")
			}
			if err := profile.Authorize(plan.Profile(), local.Trust, plan.Digest, local.Grant); err != nil {
				t.Fatalf("the grant this install recorded does not authorize it: %v", err)
			}
		})
	}
}

// TestTamperedBytesAreInstalledNowhere.
func TestTamperedBytesAreInstalledNowhere(t *testing.T) {
	canonical, _ := canonicalBuiltin(t)
	stub := newStub(t, canonical)
	// The digest stays the real one; the bytes change. This is exactly the shape
	// of a backend that served the wrong document.
	stub.document = bytes.Replace(canonical, []byte(`"qbsp`), []byte(`"QBSP`), 1)
	if bytes.Equal(stub.document, canonical) {
		t.Fatal("the fixture was not modified")
	}

	_, err := publish.PlanInstall(context.Background(), stub.client(t), nil, "listing1", "")
	if !errors.Is(err, publish.ErrDigestMismatch) {
		t.Fatalf("err = %v, want ErrDigestMismatch", err)
	}
}

// TestANonCanonicalEncodingIsRefused.
//
// The check AUB deliberately does not perform. A digest over a non-canonical
// encoding names bytes nobody else would produce for the same document, so two
// people canonicalizing it would disagree about what they had.
func TestANonCanonicalEncodingIsRefused(t *testing.T) {
	canonical, _ := canonicalBuiltin(t)
	var tree map[string]any
	if err := json.Unmarshal(canonical, &tree); err != nil {
		t.Fatal(err)
	}
	// Re-encode with indentation: the same document, different bytes.
	pretty, err := json.MarshalIndent(tree, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	stub := newStub(t, pretty)

	_, err = publish.PlanInstall(context.Background(), stub.client(t), nil, "listing1", "")
	if !errors.Is(err, publish.ErrNotCanonical) {
		t.Fatalf("err = %v, want ErrNotCanonical", err)
	}
}

// TestNothingIsWrittenWithoutAnApproval.
func TestNothingIsWrittenWithoutAnApproval(t *testing.T) {
	canonical, _ := canonicalBuiltin(t)
	stub := newStub(t, canonical)
	where := paths(t)

	plan, err := publish.PlanInstall(context.Background(), stub.client(t), nil, "listing1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publish.Apply(plan, where, false); !errors.Is(err, publish.ErrRefusedByReview) {
		t.Fatalf("err = %v, want ErrRefusedByReview", err)
	}
	if _, err := os.Stat(where.Profiles); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused install created the profiles directory")
	}
	if _, err := os.Stat(where.Bindings); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused install wrote a binding")
	}
}

// TestAWithdrawnVersionIsInstallableAndSaysSo.
//
// Installable on purpose — reproducing a build that used it is a legitimate
// reason to want one — and never silently.
func TestAWithdrawnVersionIsInstallableAndSaysSo(t *testing.T) {
	canonical, _ := canonicalBuiltin(t)
	stub := newStub(t, canonical)
	stub.yanked = true
	stub.yankWhy = "It passes -noskip, which corrupts water brushes."

	plan, err := publish.PlanInstall(context.Background(), stub.client(t), nil, "listing1", "")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Yanked || plan.YankReason != stub.yankWhy {
		t.Fatalf("the withdrawal did not travel with the plan: %+v", plan)
	}
	if _, err := publish.Apply(plan, paths(t), true); err != nil {
		t.Fatalf("a withdrawn version could not be installed deliberately: %v", err)
	}
}

// TestAnUpdateThatAsksForMoreIsReportedAsAnEscalation.
func TestAnUpdateThatAsksForMoreIsReportedAsAnEscalation(t *testing.T) {
	canonical, document := canonicalBuiltin(t)
	where := paths(t)

	// Install what is published today.
	first := newStub(t, canonical)
	plan, err := publish.PlanInstall(context.Background(), first.client(t), nil, "listing1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publish.Apply(plan, where, true); err != nil {
		t.Fatal(err)
	}

	// Publish a version that reaches the network where the installed one did not.
	tool := document.(*profile.ToolProfile)
	greedier := *tool
	greedier.Version = "99.0.0"
	greedier.Actions = append([]profile.Action(nil), tool.Actions...)
	greedier.Actions[0].Network = &profile.NetworkNeed{
		Required: true, Purpose: "fetches a texture pack while compiling",
	}
	updated, err := profile.Export(&greedier)
	if err != nil {
		t.Fatal(err)
	}
	second := newStub(t, updated)

	next, err := publish.PlanInstall(context.Background(), second.client(t),
		job.NewCatalog(where.Profiles), "listing1", "99.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if next.FirstInstall {
		t.Fatal("an update was reported as a first install")
	}
	if !next.Escalates() {
		t.Fatal("a version that newly reaches the network is not reported as an escalation")
	}
}

// TestAVersionThatMovedIsRefusedRatherThanTreatedAsAnUpdate.
func TestAVersionThatMovedIsRefusedRatherThanTreatedAsAnUpdate(t *testing.T) {
	canonical, document := canonicalBuiltin(t)
	where := paths(t)

	first := newStub(t, canonical)
	plan, err := publish.PlanInstall(context.Background(), first.client(t), nil, "listing1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publish.Apply(plan, where, true); err != nil {
		t.Fatal(err)
	}

	// The same version, different content — which is exactly what a published
	// version may never become, and what an installed one may never be replaced by
	// without somebody noticing.
	tool := document.(*profile.ToolProfile)
	edited := *tool
	edited.Summary = "Something else entirely."
	moved, err := profile.Export(&edited)
	if err != nil {
		t.Fatal(err)
	}
	second := newStub(t, moved)

	_, err = publish.PlanInstall(context.Background(), second.client(t),
		job.NewCatalog(where.Profiles), "listing1", tool.Version)
	if !errors.Is(err, publish.ErrVersionMoved) {
		t.Fatalf("err = %v, want ErrVersionMoved", err)
	}
}
