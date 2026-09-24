package web

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
)

// siteLinksCache holds AUB's answer to "where is the gallery" for a while, so
// /api/status — which the page asks often — does not ask AUB every time. An
// answer is kept per AUB address: choosing another server asks again.
type siteLinksCache struct {
	mu      sync.Mutex
	baseURL string
	gallery string
	until   time.Time
}

// siteLinksTTL is how long one answer is used. A failure is remembered for
// less, so a server that comes back is noticed soon.
const (
	siteLinksTTL      = 10 * time.Minute
	siteLinksRetry    = 30 * time.Second
	siteLinksDeadline = 2 * time.Second
)

func (c *siteLinksCache) galleryURL(ctx context.Context, client *aub.Client) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.baseURL == client.BaseURL() && now.Before(c.until) {
		return c.gallery
	}
	ctx, cancel := context.WithTimeout(ctx, siteLinksDeadline)
	defer cancel()
	links, err := client.SiteLinks(ctx)
	c.baseURL = client.BaseURL()
	if err != nil {
		c.gallery, c.until = "", now.Add(siteLinksRetry)
		return ""
	}
	c.gallery, c.until = links.GalleryURL, now.Add(siteLinksTTL)
	return c.gallery
}

// siteLinksRoutes is the page's one question about where other public pages
// are. Its own route rather than a field of /api/status, which is asked often
// and answers from this machine alone.
func (s *Server) siteLinksRoutes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/site-links": s.handleSiteLinks,
	}
}

// handleSiteLinks answers {gallery_url}: where the Auto-Pigeon server in use
// says the gallery is, or empty when it has not said, cannot be reached, or no
// server is chosen. Never guessed.
func (s *Server) handleSiteLinks(w http.ResponseWriter, r *http.Request) {
	gallery := ""
	if client := s.aubClient(); client != nil {
		gallery = s.siteLinks.galleryURL(r.Context(), client)
	}
	writeJSON(w, http.StatusOK, map[string]string{"gallery_url": gallery})
}
