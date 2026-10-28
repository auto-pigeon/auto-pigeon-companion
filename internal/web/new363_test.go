package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetsync"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle"
)

// NEW_363 closes NEW_360's findings F1–F3 inside Build & Run, and lets the page
// build a saved map without running it. These are the server's halves; the
// page's are the browser journeys.

// F2/F3: the category is decided from the status and AUB's reason code, never
// from the English of a message — so a Terms refusal is not an expired sign-in,
// a 503 is not a reason to sign in, and damage is not an outage.
func TestAFailureIsClassifiedByItsStatusAndReasonNeverItsWords(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"expired sign-in", &aub.APIError{StatusCode: 401, Message: "The request requires valid record authorization token."}, codeSignInRequired},
		{"Terms changed", &aub.APIError{StatusCode: 403, Reason: "legal_acceptance_required",
			Message: "The Terms or the notices changed. Review and accept the current version to carry on."}, codeLegalReview},
		// The same words with no reason code are not the legal gate.
		{"unrelated 403 that mentions Terms", &aub.APIError{StatusCode: 403,
			Message: "The Terms or the notices changed."}, codeRefused},
		{"no access to the map", &aub.APIError{StatusCode: 403, Reason: aub.ReasonMapAccessDenied}, codeMapAccessDenied},
		{"server away", &aub.APIError{StatusCode: 503, Message: "Service Unavailable"}, codeServerUnavailable},
		{"rate limited", &aub.APIError{StatusCode: 429}, codeServerUnavailable},
		{"texture export away", fmt.Errorf("wrapped: %w", &aub.TextureExportRefusal{Status: 503, Detail: "Service Unavailable"}), codeServerUnavailable},
		{"texture export for an older revision", &aub.TextureExportRefusal{Status: 409, Reason: aub.ReasonRevisionNotExportable}, codeRevisionChanged},
		{"texture export refused to this account", &aub.TextureExportRefusal{Status: 403, Reason: aub.ReasonMapAccessDenied}, codeMapAccessDenied},
		{"connection refused", fmt.Errorf("aub: downloading the texture export: %w",
			&url.Error{Op: "Get", URL: "http://example.invalid", Err: &net.OpError{Op: "dial", Err: errors.New("connection refused")}}), codeServerUnavailable},
		{"timed out", fmt.Errorf("asking: %w", context.DeadlineExceeded), codeServerUnavailable},
		{"map bytes not what was declared", fmt.Errorf("syncing: %w", assetsync.ErrDigestMismatch), codeDownloadDamaged},
		{"bundle member not what was declared", fmt.Errorf("publishing: %w", texturebundle.ErrDamaged), codeDownloadDamaged},
		{"something else", errors.New("the extractor said no"), ""},
	}
	for _, c := range cases {
		if got := aubFailureCode(c.err); got != c.want {
			t.Errorf("%s: code %q, want %q", c.name, got, c.want)
		}
	}
	// The ordinary words carry no internal prefix and no empty parentheses.
	refusal := &aub.TextureExportRefusal{Status: 503, Detail: "Service Unavailable"}
	if strings.Contains(refusal.Error(), "()") {
		t.Errorf("a refusal without a reason prints empty parentheses: %q", refusal.Error())
	}
	for code, words := range downloadWords {
		for _, sentence := range words {
			if strings.Contains(sentence, "aub:") || strings.Contains(sentence, "()") {
				t.Errorf("%s: %q is not a sentence for a person", code, sentence)
			}
		}
	}
}

// F2: the map list's refusal says which one it was, by code, and a legal
// refusal is not answered as a lost sign-in.
func TestTheMapListSaysWhichRefusalItWas(t *testing.T) {
	m := newMachine(t)
	m.signIn()
	cases := []struct {
		status          int
		reason, message string
		wantStatus      int
		wantCode        string
	}{
		{401, "", "The request requires valid record authorization token.", http.StatusUnauthorized, codeSignInRequired},
		{403, "legal_acceptance_required", "The Terms or the notices changed. Review and accept the current version to carry on.",
			http.StatusForbidden, codeLegalReview},
		{403, "email_verification_required", "Confirm your e-mail address to use Auto-Pigeon.", http.StatusUnauthorized, codeRefused},
		{503, "", "Service Unavailable", http.StatusBadGateway, codeServerUnavailable},
	}
	for _, c := range cases {
		m.backend.mu.Lock()
		m.backend.catalogFault.status, m.backend.catalogFault.reason, m.backend.catalogFault.message = c.status, c.reason, c.message
		m.backend.mu.Unlock()
		status, body := m.call(http.MethodGet, "/api/v1/library/catalog?type=map", nil)
		if status != c.wantStatus || body["code"] != c.wantCode {
			t.Errorf("AUB %d %q: the page got %d %v, want %d %q", c.status, c.reason, status, body["code"], c.wantStatus, c.wantCode)
		}
		// The server's own words stay, for Technical details.
		if !strings.Contains(fmt.Sprint(body["error"]), fmt.Sprint(c.status)) {
			t.Errorf("AUB %d: the exact answer is not kept: %v", c.status, body["error"])
		}
	}
	m.backend.mu.Lock()
	m.backend.catalogFault.status = 0
	m.backend.mu.Unlock()
	if status, body := m.call(http.MethodGet, "/api/v1/library/catalog?type=map", nil); status != http.StatusOK || body["code"] != nil {
		t.Errorf("after the fault the list is %d %v", status, body)
	}
}

// F3: a 503 while downloading the textures is said as the server being away —
// not as a sign-in or a newer revision — nothing is compiled, and Try again
// uses the same pinned revision, once, however often it is pressed.
func TestATextureOutageIsSaidAsOneAndTriedAgainOnTheSameRevision(t *testing.T) {
	m := newMachine(t)
	m.preparePlay(t)
	m.backend.mu.Lock()
	m.backend.textureOutages = 1
	m.backend.mu.Unlock()

	status, started := m.call(http.MethodPost, "/api/v1/play/runs", playBody(m))
	if status != http.StatusAccepted {
		t.Fatalf("starting = %d: %v", status, started["error"])
	}
	run := m.awaitPlay(t, started["id"].(string))
	if run["state"] != "failed" || run["failed_at"] != "downloading_textures" {
		t.Fatalf("state %v at %v: %v", run["state"], run["failed_at"], run["error"])
	}
	if run["failure_code"] != codeServerUnavailable {
		t.Errorf("failure_code = %v, want %q", run["failure_code"], codeServerUnavailable)
	}
	summary, _ := run["error_summary"].(string)
	remedy, _ := run["remedy"].(string)
	if strings.Contains(summary, "aub:") || strings.Contains(summary, "()") || !strings.Contains(summary, "temporarily unavailable") {
		t.Errorf("summary = %q", summary)
	}
	if strings.Contains(strings.ToLower(remedy), "sign") || strings.Contains(remedy, "reload") {
		t.Errorf("an outage is answered with sign-in or reload advice: %q", remedy)
	}
	if run["build_id"] != nil {
		t.Errorf("a build was started after an incomplete download: %v", run["build_id"])
	}
	if !strings.Contains(fmt.Sprint(run["error"]), "503") {
		t.Errorf("the exact answer is not kept for Technical details: %v", run["error"])
	}

	// Try again, pressed twice (two tabs): one new run, on the same revision.
	status, retried := m.call(http.MethodPost, "/api/v1/play/runs/"+started["id"].(string)+"/retry", nil)
	if status != http.StatusAccepted && status != http.StatusOK {
		t.Fatalf("retry = %d: %v", status, retried["error"])
	}
	_, again := m.call(http.MethodPost, "/api/v1/play/runs/"+started["id"].(string)+"/retry", nil)
	if again["id"] != retried["id"] {
		t.Errorf("a second Try again started another run: %v and %v", retried["id"], again["id"])
	}
	done := m.awaitPlay(t, retried["id"].(string))
	if done["state"] != "succeeded" || fmt.Sprint(done["revision"]) != fmt.Sprint(m.backend.asset.revision) {
		t.Errorf("the retry %v on revision %v: %v", done["state"], done["revision"], done["error"])
	}
	_, list := m.call(http.MethodGet, "/api/v1/play/runs", nil)
	if items, _ := list["items"].([]any); len(items) != 2 {
		t.Errorf("%d runs, want the failed one and one retry", len(items))
	}
}

// Build & Run with "Run after build" off: the same coordinator, nothing
// installed, no engine, and the review says so.
func TestBuildWithoutRunningStartsNoEngine(t *testing.T) {
	m := newMachine(t)
	m.preparePlay(t)
	body := playBody(m)
	for _, key := range []string{"engine", "action", "mod"} {
		delete(body, key)
	}
	body["build_only"] = true

	status, plan := m.call(http.MethodPost, "/api/v1/play/plan", body)
	if status != http.StatusOK {
		t.Fatalf("plan = %d: %v", status, plan["error"])
	}
	if plan["build_only"] != true || plan["launch"] != nil || plan["writes"] != nil {
		t.Errorf("a build-only plan names a launch or writes: %v", plan)
	}
	status, started := m.call(http.MethodPost, "/api/v1/play/runs", body)
	if status != http.StatusAccepted {
		t.Fatalf("starting = %d: %v", status, started["error"])
	}
	run := m.awaitPlay(t, started["id"].(string))
	if run["state"] != "succeeded" || run["build_only"] != true || run["launch"] != nil || run["installed_dir"] != nil {
		t.Errorf("build only ended %v, launch %v, installed %v: %v", run["state"], run["launch"], run["installed_dir"], run["error"])
	}

	// An engine named with it is a choice the run would ignore: refused.
	body["engine"] = "aucom.fixture.q1-engine"
	if status, refused := m.call(http.MethodPost, "/api/v1/play/runs", body); status != http.StatusBadRequest {
		t.Errorf("build only with an engine = %d: %v", status, refused)
	}
}

// F1: automatic builds are switched on only with a profile that can build now —
// the same readiness the manual form uses — and a profile that loses it is said,
// still chosen.
func TestAutoBuildIsSwitchedOnOnlyWithAReadyProfile(t *testing.T) {
	m := newMachine(t)
	m.signIn()
	asset := m.backend.asset.assetID
	enable := map[string]any{"pipeline": "aucom.fixture.pipeline", "display_name": "First Coast"}
	status, refused := m.call(http.MethodPost, "/api/v1/autobuild/"+asset+"/enable", enable)
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(refused["error"]), "needs setup") {
		t.Fatalf("switching on with a profile that needs setup = %d: %v", status, refused["error"])
	}

	m.preparePlay(t)
	if status, body := m.call(http.MethodPost, "/api/v1/autobuild/"+asset+"/enable", enable); status != http.StatusOK || body["enabled"] != true {
		t.Fatalf("switching on with a ready profile = %d: %v", status, body["error"])
	}

	// The program's path removed in Profiles, as the page does it.
	if status, body := m.call(http.MethodPost, "/api/v1/profiles/aucom.fixture.toolchain/bind",
		map[string]any{"executables": map[string]string{"tool": ""}}); status != http.StatusOK {
		t.Fatalf("unbinding the tool = %d: %v", status, body["error"])
	}
	_, view := m.call(http.MethodGet, "/api/v1/autobuild/"+asset, nil)
	if view["pipeline_id"] != "aucom.fixture.pipeline" || !strings.Contains(fmt.Sprint(view["unready"]), "needs setup") {
		t.Errorf("a profile that lost its readiness: pipeline %v, unready %v", view["pipeline_id"], view["unready"])
	}
	if status, body := m.call(http.MethodPost, "/api/v1/autobuild/"+asset+"/pipeline",
		map[string]any{"pipeline": "aucom.fixture.pipeline"}); status != http.StatusBadRequest {
		t.Errorf("applying a profile that needs setup = %d: %v", status, body)
	}
}
