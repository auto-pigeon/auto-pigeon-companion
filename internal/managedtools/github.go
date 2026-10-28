package managedtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Release is what GitHub's release API says about one release. Only the
// members this package reads.
type Release struct {
	ID          int64     `json:"id"`
	Tag         string    `json:"tag_name"`
	Name        string    `json:"name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Assets      []Asset   `json:"assets"`
}

// Asset is one file of a release, as the API lists it.
type Asset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	// Digest is GitHub's own "sha256:<hex>" for the asset, when it publishes
	// one. Compared with the release manifest's, never trusted instead of it.
	Digest string `json:"digest"`
	State  string `json:"state"`
}

// asset is the release's asset with this name.
func (r Release) asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// ReleaseList is one answer from the release API.
type ReleaseList struct {
	Releases []Release
	// ETag is what to send as If-None-Match next time.
	ETag string
	// NotModified is a 304: nothing changed since ETag, which is a successful
	// check with nothing new in it.
	NotModified bool
}

// Source is where releases and their files come from. One production
// implementation ([NewGitHubSource]); tests use [NewLoopbackSource], which
// still goes through every check this file makes.
type Source interface {
	ListReleases(ctx context.Context, tool Tool, etag string) (ReleaseList, error)
	// Download writes one release file to w, and refuses more than limit
	// bytes. It returns how many it wrote.
	Download(ctx context.Context, tool Tool, tag, name string, limit int64, w io.Writer) (int64, error)
}

// RateLimitError is GitHub saying "not now": a 429, or a 403 with the rate
// limit exhausted. After is a Retry-After in seconds, relative to whenever
// the caller's clock says the answer came; RetryAt is an absolute time GitHub
// named instead (a reset time, or an HTTP date).
type RateLimitError struct {
	Status  int
	After   time.Duration
	RetryAt time.Time
}

// Until is when to come back, by the caller's clock.
func (e *RateLimitError) Until(now time.Time) time.Time {
	if e.After > 0 || e.RetryAt.IsZero() {
		return now.Add(e.After)
	}
	return e.RetryAt
}

func (e *RateLimitError) Error() string {
	if e.After > 0 || e.RetryAt.IsZero() {
		return fmt.Sprintf("GitHub asked the Companion to wait %s (HTTP %d)", e.After, e.Status)
	}
	return fmt.Sprintf("GitHub asked the Companion to wait (HTTP %d) until %s",
		e.Status, e.RetryAt.UTC().Format(time.RFC3339))
}

// Bounds on everything that crosses the network.
const (
	// releasesPerPage is the one page asked for. Releases come newest first,
	// and the newest thirty are far more than the newest eligible one needs.
	releasesPerPage = 30
	// maxListBytes bounds the release list's body.
	maxListBytes = 8 << 20
	// maxManifestBytes bounds a release manifest. Today's are 4 and 26 KB.
	maxManifestBytes = 1 << 20
	// maxRedirects bounds the hops an asset download may take. GitHub uses one.
	maxRedirects = 4
	// metadataTimeout bounds one API request; downloadTimeout one file.
	metadataTimeout = 30 * time.Second
	downloadTimeout = 15 * time.Minute
)

// githubHosts are the only hosts a request or a redirect may reach: the API,
// the release download address, and the two hosts GitHub redirects release
// assets to.
var githubHosts = map[string]bool{
	"api.github.com":                        true,
	"github.com":                            true,
	"objects.githubusercontent.com":         true,
	"release-assets.githubusercontent.com":  true,
	"github-releases.githubusercontent.com": true,
}

type githubSource struct {
	api       string // https://api.github.com
	downloads string // https://github.com
	client    *http.Client
	hosts     map[string]bool
	plainHTTP bool
	userAgent string
}

// NewGitHubSource is the production source: GitHub's API and release
// downloads, over HTTPS, to the hosts above and nowhere else. No token is
// sent: these are public releases and the anonymous limit is ample for one
// check an hour.
func NewGitHubSource(userAgent string) Source {
	s := &githubSource{
		api:       "https://api.github.com",
		downloads: "https://github.com",
		hosts:     githubHosts,
		userAgent: userAgent,
	}
	s.client = s.newClient(nil)
	return s
}

// NewLoopbackSource points the same code at a server on this machine, for
// tests and for a task's fixture transport. Anything but a loopback address
// is refused, so it cannot become a way to fetch from somewhere else. Plain
// HTTP is accepted only here, and only because the address is loopback.
func NewLoopbackSource(base string, client *http.Client) (Source, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if !(host == "localhost" || (ip != nil && ip.IsLoopback())) {
		return nil, fmt.Errorf("managedtools: %s is not a loopback address", host)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("managedtools: %s is not http or https", base)
	}
	base = strings.TrimRight(base, "/")
	s := &githubSource{
		api:       base + "/api",
		downloads: base + "/dl",
		hosts:     map[string]bool{parsed.Host: true, host: true},
		plainHTTP: parsed.Scheme == "http",
		userAgent: "auto-pigeon-companion-test",
	}
	s.client = s.newClient(client)
	return s, nil
}

func (s *githubSource) newClient(base *http.Client) *http.Client {
	client := &http.Client{}
	if base != nil {
		client.Transport = base.Transport
	}
	if client.Transport == nil {
		client.Transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       30 * time.Second,
			MaxIdleConns:          4,
		}
	}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("more than %d redirects", maxRedirects)
		}
		return s.allowed(request.URL)
	}
	return client
}

// allowed is the whole policy for where a request may go.
func (s *githubSource) allowed(target *url.URL) error {
	switch {
	case target.User != nil:
		return fmt.Errorf("a redirect to an address carrying credentials is refused")
	case target.Scheme != "https" && !(s.plainHTTP && target.Scheme == "http"):
		return fmt.Errorf("a redirect to %s:// is refused; only https is followed", target.Scheme)
	case !s.hosts[target.Host] && !s.hosts[target.Hostname()]:
		return fmt.Errorf("a redirect to %s is refused; it is not one of GitHub's release hosts", target.Hostname())
	}
	return nil
}

func (s *githubSource) request(ctx context.Context, address, accept string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	if err := s.allowed(request.URL); err != nil {
		return nil, err
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("User-Agent", s.userAgent)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return request, nil
}

func (s *githubSource) ListReleases(ctx context.Context, tool Tool, etag string) (ReleaseList, error) {
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	address := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=%d", s.api, tool.Owner, tool.Repo, releasesPerPage)
	request, err := s.request(ctx, address, "application/vnd.github+json")
	if err != nil {
		return ReleaseList{}, err
	}
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return ReleaseList{}, redact(err)
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusNotModified:
		return ReleaseList{ETag: etag, NotModified: true}, nil
	case response.StatusCode == http.StatusOK:
	default:
		if limited := rateLimited(response); limited != nil {
			return ReleaseList{}, limited
		}
		return ReleaseList{}, fmt.Errorf("GitHub answered the release list with HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxListBytes+1))
	if err != nil {
		return ReleaseList{}, redact(err)
	}
	if len(body) > maxListBytes {
		return ReleaseList{}, fmt.Errorf("GitHub's release list is larger than %d bytes", maxListBytes)
	}
	var releases []Release
	if err := json.Unmarshal(body, &releases); err != nil {
		return ReleaseList{}, fmt.Errorf("GitHub's release list is not the release API's shape: %w", err)
	}
	return ReleaseList{Releases: releases, ETag: response.Header.Get("ETag")}, nil
}

// Download fetches a release file by the address GitHub documents for it,
// built here from the tool's own repository, the tag and the name, never
// from a URL anybody supplied.
func (s *githubSource) Download(ctx context.Context, tool Tool, tag, name string, limit int64, w io.Writer) (int64, error) {
	if strings.ContainsAny(tag+name, "/\\?#%") || tag == "" || name == "" {
		return 0, fmt.Errorf("managedtools: %q / %q is not a release file name", tag, name)
	}
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	address := fmt.Sprintf("%s/%s/%s/releases/download/%s/%s", s.downloads, tool.Owner, tool.Repo,
		url.PathEscape(tag), url.PathEscape(name))
	request, err := s.request(ctx, address, "application/octet-stream")
	if err != nil {
		return 0, err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return 0, redact(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if limited := rateLimited(response); limited != nil {
			return 0, limited
		}
		return 0, fmt.Errorf("GitHub answered the download of %s with HTTP %d", name, response.StatusCode)
	}
	if response.ContentLength > limit {
		return 0, fmt.Errorf("%s is %d bytes; the release says %d", name, response.ContentLength, limit)
	}
	written, err := io.Copy(w, io.LimitReader(response.Body, limit+1))
	if err != nil {
		return written, redact(err)
	}
	if written > limit {
		return written, fmt.Errorf("%s is longer than the %d bytes the release says", name, limit)
	}
	return written, nil
}

// rateLimited reads GitHub's two ways of saying "later".
func rateLimited(response *http.Response) *RateLimitError {
	if response.StatusCode != http.StatusTooManyRequests && response.StatusCode != http.StatusForbidden {
		return nil
	}
	if after := strings.TrimSpace(response.Header.Get("Retry-After")); after != "" {
		if seconds, err := strconv.Atoi(after); err == nil && seconds >= 0 {
			return &RateLimitError{Status: response.StatusCode, After: time.Duration(seconds) * time.Second}
		}
		if at, err := http.ParseTime(after); err == nil {
			return &RateLimitError{Status: response.StatusCode, RetryAt: at}
		}
	}
	if response.Header.Get("X-RateLimit-Remaining") == "0" {
		if reset, err := strconv.ParseInt(response.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			return &RateLimitError{Status: response.StatusCode, RetryAt: time.Unix(reset, 0)}
		}
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return &RateLimitError{Status: response.StatusCode, After: time.Hour}
	}
	return nil
}

// redact keeps a network error's cause and drops its URL's query, which for
// a release asset is a signed, short-lived credential nobody should log.
func redact(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		shown := urlErr.URL
		if parsed, parseErr := url.Parse(shown); parseErr == nil {
			parsed.RawQuery, parsed.Fragment, parsed.User = "", "", nil
			shown = parsed.String()
		} else if cut, _, found := strings.Cut(shown, "?"); found {
			shown = cut
		}
		return fmt.Errorf("%s %s: %w", urlErr.Op, shown, urlErr.Err)
	}
	return err
}
