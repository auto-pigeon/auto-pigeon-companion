package managedtools

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

// The production source's whole address policy.
func TestTheProductionSourceGoesOnlyToGitHubReleaseHostsOverHTTPS(t *testing.T) {
	source := NewGitHubSource("test").(*githubSource)
	for address, allowed := range map[string]bool{
		"https://api.github.com/repos/auto-pigeon/q3map2/releases":    true,
		"https://github.com/auto-pigeon/q3map2/releases/download/a/b": true,
		"https://release-assets.githubusercontent.com/x?sig=1":        true,
		"https://objects.githubusercontent.com/x":                     true,
		"http://objects.githubusercontent.com/x":                      false,
		"https://user:pass@github.com/x":                              false,
		"https://evil.example/x":                                      false,
		"https://github.com.evil.example/x":                           false,
		"https://127.0.0.1/x":                                         false,
	} {
		parsed, _ := url.Parse(address)
		if err := source.allowed(parsed); (err == nil) != allowed {
			t.Errorf("%s: allowed=%v, err=%v", address, allowed, err)
		}
	}
	if source.api != "https://api.github.com" || source.downloads != "https://github.com" {
		t.Fatalf("the production source points at %s and %s", source.api, source.downloads)
	}
}

func TestANetworkErrorNeverCarriesASignedQueryString(t *testing.T) {
	err := redact(&url.Error{Op: "Get", URL: "https://release-assets.githubusercontent.com/a?X-Amz-Signature=secret&token=t", Err: errors.New("reset")})
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token") || !strings.Contains(err.Error(), "reset") {
		t.Fatalf("redacted: %v", err)
	}
}

// The compiled-in allowlist is the two official repositories and nothing
// else; nothing outside this package can add one.
func TestTheAllowlistIsExactlyTheTwoOfficialRepositories(t *testing.T) {
	var got []string
	for _, tool := range Tools {
		got = append(got, tool.Owner+"/"+tool.Repo+" "+tool.TagPrefix)
	}
	want := "auto-pigeon/ericw-tools auto-pigeon-ericw-v|auto-pigeon/q3map2 auto-pigeon-q3map2-v"
	if strings.Join(got, "|") != want {
		t.Fatalf("allowlist %v", got)
	}
	// The Companion's own release is the one other official target
	// (NEW_368), and it is not a toolchain a profile can be bound to.
	if Self.Owner+"/"+Self.Repo+" "+Self.TagPrefix != "auto-pigeon/auto-pigeon-companion v" || Self.Kind != KindApplication || len(Self.Profiles) != 0 {
		t.Fatalf("self target %+v", Self)
	}
}

// What follows a release manifest is not a harmless suffix: the digest covers it and no reader looks at
// it, so a document with anything after it is not the document it appears to be.
func TestAReleaseManifestWithAnythingAfterItIsUnreadable(t *testing.T) {
	manifest := `{"schema":"aucom.release-manifest/2.0","product":"auto-pigeon-companion","version":"1.400","tag":"v1.400"}`
	for name, tail := range map[string]string{"a second document": " {}", "a stray brace": "}", "garbage": " x"} {
		e := evaluator{tool: Self, target: "linux-amd64", fetch: func(context.Context, Release, string, int64) ([]byte, error) {
			return []byte(manifest + tail), nil
		}}
		release := Release{Tag: "v1.400", Assets: []Asset{{Name: ManifestAssetName, Size: int64(len(manifest) + len(tail))}}}
		if _, reason, detail := e.companionCandidate(context.Background(), release, "1.400"); reason != ReasonManifestUnreadable {
			t.Errorf("%s: reason %q (%s)", name, reason, detail)
		}
	}
}
