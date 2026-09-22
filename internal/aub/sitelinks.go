package aub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// SiteLinksPath is AUB's public answer to "where are this deployment's other
// public pages" — today the gallery, whose News page the footer links to. The
// Companion knows exactly one address, AUB's; every other one it is told.
const SiteLinksPath = "/api/site-links"

// SiteLinks is `GET /api/site-links`.
type SiteLinks struct {
	// GalleryURL is the gallery's public origin, or empty when the deployment
	// has not said.
	GalleryURL string `json:"gallery_url"`
}

// SiteLinks asks AUB where the gallery is. No session is presented: the
// route is public, and a signed-out footer needs the answer too. A value that
// is not an absolute http(s) URL is dropped rather than turned into a link.
func (c *Client) SiteLinks(ctx context.Context) (SiteLinks, error) {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + SiteLinksPath
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return SiteLinks{}, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return SiteLinks{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxErrorBody))
		return SiteLinks{}, &APIError{StatusCode: response.StatusCode, Path: SiteLinksPath}
	}
	var links SiteLinks
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&links); err != nil {
		return SiteLinks{}, err
	}
	links.GalleryURL = strings.TrimRight(strings.TrimSpace(links.GalleryURL), "/")
	if parsed, err := url.Parse(links.GalleryURL); err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		links.GalleryURL = ""
	}
	return links, nil
}
