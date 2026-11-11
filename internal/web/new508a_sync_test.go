package web

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// profileCatalog is a fake of AUB's `/api/companion-profiles` for one account:
// the listings it holds, every publish it accepted, and a hook that runs
// between two reads so a test can change the world in the middle of a request.
type profileCatalog struct {
	mu        sync.Mutex
	listings  map[string]*catalogListing // by listing id
	published []map[string]any
	// beforeDetail runs before a listing's detail is served.
	beforeDetail func()
}

type catalogListing struct {
	id, profileID, kind, name string
	versions                  []catalogVersion
}

type catalogVersion struct {
	version  string
	document []byte
}

func (c *catalogListing) latest() catalogVersion { return c.versions[len(c.versions)-1] }

func (c *profileCatalog) serve(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, aub.ProfileCatalogPrefix) {
		return false
	}
	write := func(body any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, aub.ProfileCatalogPrefix), "/")
	describe := func(listing *catalogListing) map[string]any {
		return map[string]any{
			"id": listing.id, "profile_id": listing.profileID, "kind": listing.kind, "name": listing.name,
			"trust": "community", "latest_version": listing.latest().version, "visibility": "private",
			"publisher": map[string]any{"user_id": "user1"},
		}
	}
	version := func(v catalogVersion, withDocument bool) map[string]any {
		out := map[string]any{"version": v.version, "digest": profile.DigestBytes(v.document), "bytes": len(v.document)}
		if withDocument {
			out["document"] = string(v.document)
		}
		return out
	}
	c.mu.Lock()
	hook := c.beforeDetail
	c.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && rest == "":
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		c.mu.Lock()
		c.published = append(c.published, body)
		c.mu.Unlock()
		write(map[string]any{"schema_version": aub.ProfileCatalogSchema, "created": true,
			"profile": map[string]any{"id": "new", "profile_id": "x", "trust": "community"},
			"version": map[string]any{"version": "1.0.0", "digest": body["digest"]}})
	case r.Method == http.MethodGet && rest == "":
		c.mu.Lock()
		rows := []any{}
		for _, listing := range c.listings {
			rows = append(rows, describe(listing))
		}
		c.mu.Unlock()
		write(map[string]any{"schema_version": aub.ProfileCatalogSchema, "profiles": rows})
	default:
		if hook != nil {
			hook()
		}
		id, tail, _ := strings.Cut(rest, "/")
		c.mu.Lock()
		defer c.mu.Unlock()
		listing := c.listings[id]
		if listing == nil {
			w.WriteHeader(http.StatusNotFound)
			write(map[string]any{"message": "not found"})
			return true
		}
		if tail == "" {
			versions := []any{}
			for _, v := range listing.versions {
				versions = append(versions, version(v, false))
			}
			write(map[string]any{"schema_version": aub.ProfileCatalogSchema, "profile": describe(listing), "versions": versions})
			return true
		}
		want := strings.TrimPrefix(tail, "versions/")
		for _, v := range listing.versions {
			if v.version == want {
				write(map[string]any{"schema_version": aub.ProfileCatalogSchema, "profile_id": listing.profileID,
					"kind": listing.kind, "trust": "community", "version": version(v, true)})
				return true
			}
		}
		w.WriteHeader(http.StatusNotFound)
		write(map[string]any{"message": "no such version"})
	}
	return true
}

func (c *profileCatalog) publishes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.published)
}

// harmlessTool is a canonical tool document under a given version, from the
// approval package's fixture: a profile the account holds and this machine
// does not.
func harmlessTool(t *testing.T, version string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "approval", "testdata", "local-tool.tool.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := profile.Decode([]byte(strings.Replace(string(raw), `"version": "1.0.0"`, `"version": "`+version+`"`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := profile.Export(document)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

// syncMachine is a signed-in machine whose account holds one profile this
// computer does not (a download), beside the fixture profiles written here
// (uploads).
func syncMachine(t *testing.T) (*machine, *profileCatalog) {
	t.Helper()
	m := newMachine(t)
	catalog := &profileCatalog{listings: map[string]*catalogListing{
		"listing-harmless": {id: "listing-harmless", profileID: "example.local.harmless", kind: "tool",
			name: "A tool profile somebody wrote", versions: []catalogVersion{{"1.0.0", harmlessTool(t, "1.0.0")}}},
	}}
	m.backend.extra = catalog.serve
	m.signIn()
	return m, catalog
}

func review(t *testing.T, m *machine) (string, []any) {
	t.Helper()
	status, body := m.call(http.MethodPost, "/api/v1/account/sync", map[string]any{"dry_run": true})
	if status != http.StatusOK {
		t.Fatalf("the review: %d %v", status, body)
	}
	reviewed, _ := body["reviewed"].(string)
	outcomes, _ := body["outcomes"].([]any)
	if len(reviewed) != 64 || len(outcomes) == 0 {
		t.Fatalf("the review has no identity or no operations: %v", body)
	}
	return reviewed, outcomes
}

func outcomeOf(outcomes []any, id string) map[string]any {
	for _, raw := range outcomes {
		if outcome, _ := raw.(map[string]any); outcome["id"] == id {
			return outcome
		}
	}
	return nil
}

// installedHere reports whether the account's profile was written to this
// machine's profile folder.
func (m *machine) installedHere(id string) bool {
	entries, _ := os.ReadDir(m.profiles)
	for _, entry := range entries {
		raw, _ := os.ReadFile(filepath.Join(m.profiles, entry.Name()))
		if strings.Contains(string(raw), `"`+id+`"`) {
			return true
		}
	}
	return false
}

// NEW_508A §2. A confirmation is for the review it names — the exact
// operations: direction, version, content digest, permissions, disclosures.
func TestASyncConfirmationIsBoundToTheExactReviewedOperations(t *testing.T) {
	t.Run("the review shows the plans, and the same review confirmed runs them", func(t *testing.T) {
		m, catalog := syncMachine(t)
		reviewed, outcomes := review(t, m)
		download := outcomeOf(outcomes, "example.local.harmless")
		if download == nil || download["action"] != "download" || download["version"] != "1.0.0" ||
			download["digest"] != profile.DigestBytes(harmlessTool(t, "1.0.0")) || len(download["permissions"].([]any)) == 0 {
			t.Fatalf("the download in the review is not the exact served document with its permissions: %v", download)
		}
		upload := outcomeOf(outcomes, "aucom.fixture.pipeline")
		if upload == nil || upload["action"] != "upload" || upload["digest"] == nil || upload["version"] == nil {
			t.Fatalf("the upload in the review does not name its bytes: %v", upload)
		}
		// Reading the review again is the same review.
		if again, _ := review(t, m); again != reviewed {
			t.Fatal("two reviews of an unchanged world have different identities")
		}
		if catalog.publishes() != 0 || m.installedHere("example.local.harmless") {
			t.Fatal("a review sent or installed something")
		}

		status, body := m.call(http.MethodPost, "/api/v1/account/sync", map[string]any{"confirmed": true, "reviewed": reviewed})
		if status != http.StatusOK {
			t.Fatalf("confirming the review that was read: %d %v", status, body)
		}
		if catalog.publishes() == 0 {
			t.Error("nothing was published")
		}
		// What was published is what was reviewed, byte for byte.
		for _, sent := range catalog.published {
			if sent["digest"] != profile.DigestBytes([]byte(sent["document"].(string))) {
				t.Errorf("a published document is not the bytes its digest names: %v", sent["digest"])
			}
		}
		if !m.installedHere("example.local.harmless") {
			t.Errorf("the reviewed download was not installed: %v", body["outcomes"])
		}
	})

	t.Run("no review named: nothing happens, and the answer is the review", func(t *testing.T) {
		m, catalog := syncMachine(t)
		status, body := m.call(http.MethodPost, "/api/v1/account/sync", map[string]any{"confirmed": true})
		if status != http.StatusConflict || body["changed"] != true || body["review_required"] != true || body["reviewed"] == "" {
			t.Fatalf("a confirmation with no review answered %d %v", status, body)
		}
		if catalog.publishes() != 0 || m.installedHere("example.local.harmless") {
			t.Fatal("a confirmation with no review sent or installed something")
		}
	})

	t.Run("an upload edited under the same id and version after the review", func(t *testing.T) {
		m, catalog := syncMachine(t)
		reviewed, before := review(t, m)
		// The authored document's bytes change; its id and version do not.
		path := filepath.Join(m.profiles, "aucom.fixture.pipeline.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err = json.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		document["summary"] = "Edited after the review was read."
		edited, _ := json.Marshal(document)
		m.writeProfile("aucom.fixture.pipeline.json", edited)

		status, body := m.call(http.MethodPost, "/api/v1/account/sync", map[string]any{"confirmed": true, "reviewed": reviewed})
		if status != http.StatusConflict || body["changed"] != true || body["reviewed"] == reviewed {
			t.Fatalf("a stale review was accepted: %d %v", status, body)
		}
		after := outcomeOf(body["outcomes"].([]any), "aucom.fixture.pipeline")
		was := outcomeOf(before, "aucom.fixture.pipeline")
		if after == nil || after["version"] != was["version"] || after["digest"] == was["digest"] {
			t.Errorf("the new review does not show the edited bytes under the same version: was %v, now %v", was, after)
		}
		if catalog.publishes() != 0 || m.installedHere("example.local.harmless") {
			t.Fatalf("a stale review sent %d document(s) or installed a profile", catalog.publishes())
		}
		// The new review, read and confirmed, goes through.
		if status, body = m.call(http.MethodPost, "/api/v1/account/sync", map[string]any{"confirmed": true, "reviewed": body["reviewed"]}); status != http.StatusOK {
			t.Fatalf("confirming the new review: %d %v", status, body)
		}
	})

	t.Run("a newer version published after the review is never installed unreviewed", func(t *testing.T) {
		m, catalog := syncMachine(t)
		reviewed, _ := review(t, m)
		catalog.mu.Lock()
		listing := catalog.listings["listing-harmless"]
		listing.versions = append(listing.versions, catalogVersion{"2.0.0", harmlessTool(t, "2.0.0")})
		catalog.mu.Unlock()

		status, body := m.call(http.MethodPost, "/api/v1/account/sync", map[string]any{"confirmed": true, "reviewed": reviewed})
		if status != http.StatusConflict || body["changed"] != true {
			t.Fatalf("a review of 1.0.0 was accepted after 2.0.0 was published: %d %v", status, body)
		}
		if now := outcomeOf(body["outcomes"].([]any), "example.local.harmless"); now == nil || now["version"] != "2.0.0" {
			t.Errorf("the new review does not show 2.0.0: %v", now)
		}
		if catalog.publishes() != 0 || m.installedHere("example.local.harmless") {
			t.Fatal("a stale review sent or installed something")
		}
	})

	t.Run("a version published between the catalog lookup and the install plan", func(t *testing.T) {
		m, catalog := syncMachine(t)
		// The version appears AFTER the account's listing was read and BEFORE
		// the plan asks for the listing's detail — inside one request.
		once := sync.Once{}
		catalog.mu.Lock()
		catalog.beforeDetail = func() {
			once.Do(func() {
				catalog.mu.Lock()
				listing := catalog.listings["listing-harmless"]
				listing.versions = append(listing.versions, catalogVersion{"2.0.0", harmlessTool(t, "2.0.0")})
				catalog.mu.Unlock()
			})
		}
		catalog.mu.Unlock()
		_, outcomes := review(t, m)
		// The plan is for the version the lookup named, not for "latest".
		if planned := outcomeOf(outcomes, "example.local.harmless"); planned == nil || planned["version"] != "1.0.0" ||
			planned["digest"] != profile.DigestBytes(harmlessTool(t, "1.0.0")) {
			t.Fatalf("the plan resolved latest again instead of the version it was made for: %v", planned)
		}
	})

	t.Run("signed out, and another account's review", func(t *testing.T) {
		m, catalog := syncMachine(t)
		reviewed, _ := review(t, m)
		if status, _ := m.call(http.MethodPost, "/api/auth/logout", nil); status != http.StatusOK {
			t.Fatalf("sign out: %d", status)
		}
		if status, body := m.call(http.MethodPost, "/api/v1/account/sync", map[string]any{"confirmed": true, "reviewed": reviewed}); status != http.StatusUnauthorized {
			t.Fatalf("signed out, a confirmation answered %d %v", status, body)
		}
		// Signed in as somebody else, the first account's review is not theirs.
		m.backend.mu.Lock()
		m.backend.email = "someone-else@example.test"
		m.backend.mu.Unlock()
		m.signIn()
		status, body := m.call(http.MethodPost, "/api/v1/account/sync", map[string]any{"confirmed": true, "reviewed": reviewed})
		if status != http.StatusConflict || body["changed"] != true {
			t.Fatalf("another account's review was accepted: %d %v", status, body)
		}
		if catalog.publishes() != 0 || m.installedHere("example.local.harmless") {
			t.Fatal("a review from another account sent or installed something")
		}
	})
}

// NEW_508A §3: the leak outcome is read from evidence, by one function the
// headline and the sentence share (testdata/leakoutcome.check.mjs).
func TestALeakOutcomeIsReadFromEvidenceNotFromAnExitStatus(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	out, err := exec.Command(node, "testdata/leakoutcome.check.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("leakoutcome.check.mjs: %v\n%s", err, out)
	}
}
