package aub

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// GameProfileSummary is one AUB Game Profile, as a listing describes it.
//
// A Game Profile describes a project's game and map dialect. It is NOT the game,
// it is not an engine profile, and nothing about it says where anything is on
// this machine — which is why this client only ever READS one, and why a join
// never installs a local copy of it.
type GameProfileSummary struct {
	ProfileID    string `json:"profile_id"`
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Visibility   string `json:"visibility"`
	SystemOwned  bool   `json:"system_owned"`
	EngineFamily string `json:"engine_family"`
	MapDialect   string `json:"map_dialect"`
}

// ErrGameProfileNotFound is a slug no listing this account can read carries.
var ErrGameProfileNotFound = errors.New("aub: no game profile this account can read has that slug")

// GameProfileBySlug resolves a slug among the built-in, public and own profiles.
func (c *Client) GameProfileBySlug(ctx context.Context, slug string) (GameProfileSummary, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return GameProfileSummary{}, ErrGameProfileNotFound
	}
	for _, path := range []string{"/api/game-profiles/system", "/api/game-profiles/public", "/api/game-profiles"} {
		var page struct {
			Items []GameProfileSummary `json:"items"`
		}
		query := url.Values{"q": []string{slug}, "limit": []string{"50"}}
		if err := c.do(ctx, http.MethodGet, path, query, nil, &page); err != nil {
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
				continue
			}

			return GameProfileSummary{}, err
		}
		for _, item := range page.Items {
			if strings.EqualFold(item.Slug, slug) {
				return item, nil
			}
		}
	}

	return GameProfileSummary{}, ErrGameProfileNotFound
}
