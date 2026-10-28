package aub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var buildID = strings.Repeat("a1", 16)

func TestABuildRequestLinkRoundTrips(t *testing.T) {
	for _, intent := range []string{BuildIntentBuild, BuildIntentBuildRun} {
		raw := "autopigeon://build/request/" + buildID + "?intent=" + intent
		link, err := ParseBuildRequestLink(raw)
		if err != nil {
			t.Fatal(err)
		}
		if link != (BuildRequestLink{RequestID: buildID, Intent: intent}) || link.Link() != raw || !IsBuildRequestLink(raw) {
			t.Fatalf("parsed %+v, spelled %q", link, link.Link())
		}
		if link.RunAfter() != (intent == BuildIntentBuildRun) {
			t.Fatalf("%s: run after = %v", intent, link.RunAfter())
		}
	}
}

// Every refusal goes through the production parser: the id is sent to AUB and
// the intent decides whether an engine may start.
func TestABuildRequestLinkRefusesEverythingElse(t *testing.T) {
	base := "autopigeon://build/request/" + buildID
	for _, raw := range []string{
		"",
		base,
		base + "?",
		"https://build/request/" + buildID + "?intent=build",
		"autopigeon://user:pw@build/request/" + buildID + "?intent=build",
		"autopigeon://build/request/user@" + buildID + "?intent=build",
		base + "?intent=build&exe=/bin/sh",
		base + "?intent=build&argv=-x",
		base + "?intent=build&url=https://example.test",
		"autopigeon://build/request/../" + buildID + "?intent=build",
		"autopigeon://build/request/" + buildID + "/../x?intent=build",
		"autopigeon://build/request//etc/passwd?intent=build",
		"autopigeon://build/request/C:/Windows?intent=build",
		"autopigeon://build/request/" + strings.ToUpper(buildID) + "?intent=build",
		"autopigeon://build/request/" + buildID[:31] + "?intent=build",
		"autopigeon://build/request/" + buildID + "a?intent=build",
		base + "?intent=",
		base + "?intent=run",
		base + "?intent=BUILD",
		base + "?intent=leak_test",
		base + "?intent=build&intent=build_run",
		base + "?intent=build&intent=build",
		base + "?intent=build#x",
		base + "?intent=build_%72un",
		base + "?intent=build;x=1",
		// Surrounding white space is the operating system's and is trimmed, as
		// for every link; inside the link it is refused.
		base + "? intent=build",
		base + "?intent=\tbuild",
		base + "?intent=build%20",
		base + "?intent=build\\",
		// A leak-test link is not a build request.
		"autopigeon://leaktest/map/abc?revision=1&sha256=" + leakDigest,
	} {
		if _, err := ParseBuildRequestLink(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	// And a build request is not a leak test.
	for _, intent := range []string{BuildIntentBuild, BuildIntentBuildRun} {
		raw := base + "?intent=" + intent
		if IsLeakTestLink(raw) {
			t.Errorf("%q is shaped like a leak-test link", raw)
		}
		if _, err := ParseLeakTestLink(raw); err == nil {
			t.Errorf("the leak-test parser accepted %q", raw)
		}
	}
	if IsBuildRequestLink("autopigeon://leaktest/map/abc?revision=1&sha256=" + leakDigest) {
		t.Error("a leak-test link is shaped like a build request")
	}
}

// AUB's answer is untrusted input too: an answer about another request, or one
// carrying anything but the documented shape, is refused before anyone reads it.
func TestABuildRequestAnswerIsValidatedBeforeUse(t *testing.T) {
	good := map[string]any{
		"request_id": buildID, "intent": "build_run", "map_id": "map-e1m1", "revision": 4,
		"revision_id": "rev-000004", "content_sha256": leakDigest, "game": "quake1",
		"map_name": "First Coast", "state": "pending", "sequence": 0,
		"created_at": "2026-10-28T10:00:00Z", "updated_at": "2026-10-28T10:00:00Z", "expires_at": "2026-10-28T10:30:00Z",
	}
	answer := good
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != BuildRequestPath+buildID || r.Header.Get("Authorization") != "token" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(answer)
	}))
	defer server.Close()
	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	client.SetToken("token")
	request, err := client.BuildRequest(context.Background(), buildID)
	if err != nil || request.RevisionID != "rev-000004" || request.Intent != "build_run" || request.MapName != "First Coast" {
		t.Fatalf("good answer: %+v %v", request, err)
	}
	with := func(key string, value any) map[string]any {
		copy := map[string]any{}
		for k, v := range good {
			copy[k] = v
		}
		copy[key] = value
		return copy
	}
	for _, bad := range []map[string]any{
		with("request_id", strings.Repeat("b", 32)),
		with("intent", "leak_test"),
		with("map_id", "../x"),
		with("revision", 0),
		with("revision_id", "rev/../x"),
		with("content_sha256", strings.ToUpper(leakDigest)),
		with("game", "../quake1"),
		with("state", "running"),
		with("sequence", -1),
		with("stage", strings.Repeat("x", 121)),
		with("build_id", "/tmp/x"),
	} {
		answer = bad
		if got, err := client.BuildRequest(context.Background(), buildID); err == nil {
			t.Errorf("accepted %+v", got)
		}
	}
	answer = with("map_name", "line\nbreak")
	if got, err := client.BuildRequest(context.Background(), buildID); err != nil || got.MapName != "" {
		t.Errorf("a name that is not plain text was kept: %+v %v", got, err)
	}
	if _, err := client.BuildRequest(context.Background(), "../"+buildID); err == nil {
		t.Error("asked AUB about a malformed id")
	}
}
