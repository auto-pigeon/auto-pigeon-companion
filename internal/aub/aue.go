package aub

// The authorized extractor distribution.
//
// Auto-Pigeon Extractor is a separate program under its own licence. When the
// publisher keeps its builds behind an account rather than on an open mirror,
// AUB is where the bytes come from — and fetching one needs a short-lived
// capability minted for that exact artifact.
//
// **None of this verifies anything.** The digest, the size and the signature
// chain are the signed catalogue's, checked by `internal/acquire` against what
// actually arrived. A grant answers *may I fetch these bytes*; whether they are
// the right bytes is a different question with a different answer, and keeping
// the two apart is what stops a compromised backend from being able to make
// this Companion RUN something.

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// AUEReleasePrefix is where the extractor distribution lives on the Companion
// surface.
const AUEReleasePrefix = "/api/companion/v1/releases/aue"

// AUEDownloadGrant is what AUB issues for one artifact.
type AUEDownloadGrant struct {
	Grant     string `json:"grant"`
	ExpiresAt string `json:"expires_at"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Version   string `json:"version"`
	Platform  string `json:"platform"`
	// DownloadPath is a path, not a URL: this client already knows the origin
	// it is talking to, and a URL minted by a server would be a compiled-in
	// address of the kind AGENTS.md forbids.
	DownloadPath string `json:"download_path"`
	Note         string `json:"note"`
}

// AUEReleaseCatalog is what this deployment distributes.
type AUEReleaseCatalog struct {
	SchemaVersion string       `json:"schema_version"`
	Configured    bool         `json:"configured"`
	Releases      []AUERelease `json:"releases"`
	Problems      []string     `json:"problems,omitempty"`
}

// AUERelease is one version of the extractor, as AUB holds it.
type AUERelease struct {
	Version  string `json:"version"`
	BuildID  string `json:"build_id,omitempty"`
	Protocol string `json:"protocol"`
	License  struct {
		SPDX                string `json:"spdx"`
		CorrespondingSource string `json:"corresponding_source"`
	} `json:"license"`
	Artifacts []AUEReleaseArtifact `json:"artifacts"`
}

// AUEReleaseArtifact is one downloadable build.
type AUEReleaseArtifact struct {
	Platform struct {
		OS   string `json:"os"`
		Arch string `json:"arch"`
	} `json:"platform"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	GrantPath    string `json:"grant_path"`
	DownloadPath string `json:"download_path"`
}

// AUEReleases lists what this deployment distributes.
func (c *Client) AUEReleases(ctx context.Context) (AUEReleaseCatalog, error) {
	var catalog AUEReleaseCatalog
	if err := c.do(ctx, "GET", AUEReleasePrefix, nil, nil, &catalog); err != nil {
		return AUEReleaseCatalog{}, err
	}

	return catalog, nil
}

// AUEDownload mints a grant for one artifact and returns the URL to fetch it
// from.
//
// The two halves are one call because they are one decision: a caller that
// holds a grant and no URL has to compose one, and a caller that composes one
// is a caller that can compose it against a different origin.
func (c *Client) AUEDownload(ctx context.Context, digest string) (string, AUEDownloadGrant, error) {
	digest = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(digest)), "sha256:")
	if digest == "" {
		return "", AUEDownloadGrant{}, fmt.Errorf("aub: no artifact digest to authorize")
	}

	var grant AUEDownloadGrant
	if err := c.do(ctx, "POST", AUEReleasePrefix+"/"+digest+"/grant", nil, nil, &grant); err != nil {
		return "", AUEDownloadGrant{}, err
	}
	if strings.TrimSpace(grant.Grant) == "" || strings.TrimSpace(grant.DownloadPath) == "" {
		return "", AUEDownloadGrant{}, fmt.Errorf("aub: the download grant is incomplete")
	}
	// Composed against THIS client's base URL and never against anything in the
	// response. A server that could name the host its own artifacts come from
	// could name any host.
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + grant.DownloadPath
	endpoint.RawQuery = url.Values{"grant": []string{grant.Grant}}.Encode()

	return endpoint.String(), grant, nil
}

// HostsArtifact reports whether an artifact URL is served by this AUB.
//
// Scheme, host and port, and nothing else: a path prefix comparison would let a
// catalogue point at `https://aub.example/../elsewhere` and still be treated as
// this backend's. Used to decide whether a download needs a grant at all — a
// catalogue entry on a public mirror is fetched exactly as it always was.
func (c *Client) HostsArtifact(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return false
	}

	return strings.EqualFold(parsed.Scheme, c.baseURL.Scheme) && strings.EqualFold(parsed.Host, c.baseURL.Host)
}
