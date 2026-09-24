package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/web"
)

// The README's invariant, made executable: everything the page can do, the CLI
// can do too, through the same services.
//
// AUT/AUCOM 219 found the one place it was false — a tool profile could be
// approved through `POST /api/v1/profiles/{id}/grant` and by no command at all.
// The repair was not a second implementation of the grant but one
// [approval.Service] that both hold, and the way to prove that is to run both
// surfaces against two identical machines and compare what each wrote.

// serverFor is the local API, pointed at the same state a CLI invocation with
// this config file would use.
func serverFor(t *testing.T, env *Env) (*web.Server, string) {
	t.Helper()
	dir := filepath.Dir(env.ConfigPath)
	token, err := web.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	server, err := web.NewServer(web.Options{
		Version: "test-version",
		Config:  config.Config{},
		Token:   token,
		Paths: web.Paths{
			Profiles: filepath.Join(dir, "profiles"),
			Bindings: filepath.Join(dir, "bindings.json"),
			Builds:   filepath.Join(dir, "builds"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return server, token.Value()
}

// call drives one API route the way the page does: loopback host, this run's
// token, JSON in and JSON out.
func call(t *testing.T, server *web.Server, token, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader *strings.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(encoded))
	} else {
		reader = strings.NewReader("")
	}
	request := httptest.NewRequest(method, path, reader)
	request.Host = "127.0.0.1:9099"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-AUCOM-Token", token)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	var decoded map[string]any
	if recorder.Body.Len() > 0 {
		_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	}
	return recorder.Code, decoded
}

// Two machines in the same state. One is approved from the command line and the
// other through the API, and the binding files must then say the same thing —
// not "an equivalent thing", the same thing, because they are written by the
// same code.
func TestTheCommandLineAndThePageRecordTheSameApproval(t *testing.T) {
	byCLI, _, _ := testEnv(t)
	digest := writeUngrantedFixture(t, byCLI, "parity")

	byAPI, _, _ := testEnv(t)
	if digestAPI := writeUngrantedFixture(t, byAPI, "parity"); digestAPI != digest {
		t.Fatalf("the two machines have different documents: %s and %s", digest, digestAPI)
	}

	if code, _, stderr := run(t, byCLI, "profile", "grant", grantFixtureID,
		"--digest="+digest, "--approve"); code != 0 {
		t.Fatalf("the CLI approval exited %d: %s", code, stderr)
	}

	server, token := serverFor(t, byAPI)
	status, body := call(t, server, token, http.MethodPost,
		"/api/v1/profiles/"+grantFixtureID+"/grant", map[string]any{"digest": digest})
	if status != http.StatusOK {
		t.Fatalf("the API approval returned %d: %v", status, body)
	}
	if body["authorized"] != true {
		t.Errorf("the API says the profile is not authorized: %v", body)
	}

	fromCLI := storedBinding(t, byCLI, grantFixtureID)
	fromAPI := storedBinding(t, byAPI, grantFixtureID)
	if fromCLI.Grant == nil || fromAPI.Grant == nil {
		t.Fatalf("one of the two recorded no grant: cli=%+v api=%+v", fromCLI.Grant, fromAPI.Grant)
	}
	// Everything but the clock. Two approvals a moment apart are the same
	// decision recorded twice, and the timestamp is the one field that is
	// supposed to differ.
	cli, api := *fromCLI.Grant, *fromAPI.Grant
	cli.GrantedAt = api.GrantedAt
	if cli.ProfileID != api.ProfileID || cli.Version != api.Version ||
		cli.Digest != api.Digest || cli.Trust != api.Trust ||
		strings.Join(cli.Granted, ",") != strings.Join(api.Granted, ",") {
		t.Errorf("the two surfaces recorded different grants:\n  cli %+v\n  api %+v", cli, api)
	}
	if fromCLI.Trust != fromAPI.Trust || fromCLI.Acquisition != fromAPI.Acquisition {
		t.Errorf("the two surfaces recorded different bindings:\n  cli %+v\n  api %+v", fromCLI, fromAPI)
	}

	// And withdrawal, the same way round.
	if code, _, stderr := run(t, byCLI, "profile", "withdraw", grantFixtureID, "--confirm"); code != 0 {
		t.Fatalf("the CLI withdrawal exited %d: %s", code, stderr)
	}
	if status, body := call(t, server, token, http.MethodPost,
		"/api/v1/profiles/"+grantFixtureID+"/withdraw", nil); status != http.StatusOK {
		t.Fatalf("the API withdrawal returned %d: %v", status, body)
	}
	if storedBinding(t, byCLI, grantFixtureID).Grant != nil ||
		storedBinding(t, byAPI, grantFixtureID).Grant != nil {
		t.Error("a withdrawal left a grant behind on one of the two")
	}
	// The setup survived on both. This is the property that makes a withdrawal
	// safe to take, and it must not hold on only one surface.
	for name, local := range map[string]string{
		"cli": storedBinding(t, byCLI, grantFixtureID).Executables["helper"],
		"api": storedBinding(t, byAPI, grantFixtureID).Executables["helper"],
	} {
		if local != selfPath(t) {
			t.Errorf("the %s withdrawal forgot where the program is: %q", name, local)
		}
	}
}

// The refusals match too. A page and a script that disagree about what is
// refused is a program with two security models.
func TestBothSurfacesRefuseTheSameApprovals(t *testing.T) {
	env, _, _ := testEnv(t)
	digest := writeUngrantedFixture(t, env, "refusals")
	server, token := serverFor(t, env)
	stale := "sha256:" + strings.Repeat("11", 32)

	// No digest.
	if status, _ := call(t, server, token, http.MethodPost,
		"/api/v1/profiles/"+grantFixtureID+"/grant", map[string]any{}); status != http.StatusBadRequest {
		t.Errorf("the API accepted an approval with no digest: %d", status)
	}
	if code, _, _ := run(t, env, "profile", "grant", grantFixtureID, "--approve"); code == 0 {
		t.Error("the CLI accepted an approval with no digest")
	}

	// A digest that is not this document.
	status, body := call(t, server, token, http.MethodPost,
		"/api/v1/profiles/"+grantFixtureID+"/grant", map[string]any{"digest": stale})
	if status != http.StatusConflict {
		t.Errorf("the API returned %d for a stale digest, want 409", status)
	}
	if message, _ := body["error"].(string); !strings.Contains(message, digest) {
		t.Errorf("the API refusal does not name the document that is here: %v", body)
	}
	if code, _, stderr := run(t, env, "profile", "grant", grantFixtureID,
		"--digest="+stale, "--approve"); code == 0 || !strings.Contains(stderr, digest) {
		t.Errorf("the CLI refusal is exit %d:\n%s", code, stderr)
	}

	// Neither wrote anything.
	if _, err := os.Stat(filepath.Join(filepath.Dir(env.ConfigPath), "bindings.json")); err == nil {
		if local := storedBinding(t, env, grantFixtureID); local.Grant != nil {
			t.Error("a refused approval recorded a grant")
		}
	}
}

// Binding through the API records where a program is and grants nothing, and an
// approval sent with it still has to name the document. The bind route shares
// the same service for that half, so `approve` on a bind cannot be a weaker
// approval than `grant` is.
func TestBindingThroughTheAPIGrantsNothingAndItsApprovalNamesTheDocument(t *testing.T) {
	env, _, _ := testEnv(t)
	digest := writeUngrantedFixture(t, env, "bound")
	server, token := serverFor(t, env)
	route := "/api/v1/profiles/" + grantFixtureID + "/bind"

	status, body := call(t, server, token, http.MethodPost, route,
		map[string]any{"executables": map[string]string{"helper": selfPath(t)}})
	if status != http.StatusOK {
		t.Fatalf("bind returned %d: %v", status, body)
	}
	if body["authorized"] != false {
		t.Error("binding authorized the profile")
	}
	if local := storedBinding(t, env, grantFixtureID); local.Grant != nil {
		t.Error("binding recorded a grant")
	}

	// An approval with no digest at all is refused, where it used to be taken
	// at face value against whatever was on disk.
	if status, _ := call(t, server, token, http.MethodPost, route,
		map[string]any{"approve": true}); status != http.StatusBadRequest {
		t.Errorf("an approval with no digest returned %d, want 400", status)
	}
	if status, _ := call(t, server, token, http.MethodPost, route,
		map[string]any{"approve": true, "digest": "sha256:" + strings.Repeat("22", 32)}); status != http.StatusConflict {
		t.Errorf("an approval for another document returned %d, want 409", status)
	}
	if local := storedBinding(t, env, grantFixtureID); local.Grant != nil {
		t.Fatal("a refused approval on the bind route recorded a grant")
	}

	status, body = call(t, server, token, http.MethodPost, route,
		map[string]any{"approve": true, "digest": digest})
	if status != http.StatusOK {
		t.Fatalf("bind with an approval returned %d: %v", status, body)
	}
	if body["authorized"] != true {
		t.Errorf("the approved profile is not authorized: %v", body)
	}
	// And the CLI sees it, because there is one binding file and one grant in it.
	if code, stdout, _ := run(t, env, "profile", "review", grantFixtureID); code != 0 ||
		!strings.Contains(stdout, "approved: ") || strings.Contains(stdout, "approved: no") {
		t.Errorf("the CLI does not see the approval the page recorded:\n%s", stdout)
	}
}
