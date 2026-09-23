package aub

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// The map texture export, as AUB serves it.
//
// # Why this is a typed method and not a generic proxy
//
// The browser never sees the AUB session token and never receives a private
// download URL: a route that handed either to a page would move the Companion's
// whole authentication boundary into JavaScript. So the bundle is fetched HERE,
// by the one authenticated [Client] the rest of the program already uses, and
// the page is told only the sanitized facts internal/texturebundle publishes.
//
// # Why the revision travels with the request
//
// A one-click build downloads an exact map revision and then that revision's
// textures. Those are two requests, and a map saved between them would pair new
// WADs with an old map. `AUCOM/AUE/AUT 246I1` added `?revision=` to the AUB
// route for that reason: the server confirms the number or refuses, so there is
// no window in which the pairing can be wrong and no "download current twice"
// race to lose.
const (
	// TextureExportMediaType is what the route serves.
	TextureExportMediaType = "application/zip"

	// TextureExportSchema is the manifest schema version this client was
	// written against. internal/texturebundle checks it; the constant lives
	// here because it is part of the route's contract.
	TextureExportSchema = "aub-map-texture-export/1.1"
)

// TextureExportRefusal is a refusal the texture-export route answers with,
// distinguished from a transport failure so the Companion can say which of them
// happened and what the user should do about it.
type TextureExportRefusal struct {
	Status int
	Reason string
	Detail string
}

func (r *TextureExportRefusal) Error() string {
	if r.Detail != "" {
		return fmt.Sprintf("aub: the texture export was refused (%s): %s", r.Reason, r.Detail)
	}

	return fmt.Sprintf("aub: the texture export was refused (%s)", r.Reason)
}

// The refusal reasons this client names. Others are passed through as their own
// code rather than flattened, so a deployment that adds one is not misreported.
const (
	// ReasonRevisionNotExportable: the map has been saved since the caller
	// chose its revision, so the export would not be that revision's.
	ReasonRevisionNotExportable = "revision_not_exportable"
	// ReasonNoTextures: the map genuinely references no texture at all.
	ReasonNoTextures = "map_requires_no_textures"
	// ReasonMapAccessDenied: the signed-in account may not read this map.
	ReasonMapAccessDenied = "map_access_denied"
)

// StaleRevision reports the refusal a Companion retries by re-reading the map.
func (r *TextureExportRefusal) StaleRevision() bool {
	return r.Reason == ReasonRevisionNotExportable
}

// TextureExport downloads the bundle of original texture assets a map revision
// needs.
//
// `revision` is the map revision NUMBER the caller selected. Zero or negative
// means "whatever is current", which is the pre-246I1 behaviour and is only
// correct for a caller that has not pinned a revision — a build has, and passes
// it.
//
// The whole bundle is read into memory deliberately: it is a ZIP that has to be
// verified in full before a single member is trusted, and a streaming extractor
// that validated as it went would be an extractor that had already written half
// a hostile archive to disk by the time it found the member that made it
// hostile. [MaxTextureExportBytes] is the cap.
func (c *Client) TextureExport(ctx context.Context, mapID string, revision int) ([]byte, error) {
	if strings.TrimSpace(mapID) == "" {
		return nil, fmt.Errorf("aub: a texture export needs a map id")
	}
	if !c.Authenticated() {
		return nil, fmt.Errorf("aub: downloading a map's textures needs a signed-in account")
	}

	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/maps/" + url.PathEscape(mapID) +
		"/texture-export"
	if revision > 0 {
		query := url.Values{}
		query.Set("revision", strconv.Itoa(revision))
		endpoint.RawQuery = query.Encode()
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("aub: building the texture-export request: %w", err)
	}
	request.Header.Set("Authorization", c.token)
	request.Header.Set("Accept", TextureExportMediaType)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("aub: downloading the texture export: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, textureRefusal(endpoint.Path, response)
	}

	// One byte over the cap is read on purpose, so a bundle exactly at the
	// limit passes and one above it is refused rather than silently truncated.
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxTextureExportBytes+1))
	if err != nil {
		return nil, fmt.Errorf("aub: reading the texture export: %w", err)
	}
	if int64(len(body)) > MaxTextureExportBytes {
		return nil, fmt.Errorf("aub: this map's texture export is larger than the %d MiB the Companion will accept",
			MaxTextureExportBytes>>20)
	}

	return body, nil
}

// MaxTextureExportBytes caps the compressed bundle.
//
// A bundle is original WAD and WAL files, and a generous Quake 1 map's whole
// declared set is a few tens of megabytes. 512 MiB is far above any real one and
// far below a number that would let a compromised or misconfigured deployment
// exhaust this machine's memory.
const MaxTextureExportBytes int64 = 512 << 20

// textureRefusal turns a non-2xx answer into a typed refusal, reusing the
// client's own error decoding so a 401 is still recognised as one by
// [APIError.Unauthorized] — that is the sign-in remedy, and it must not stop
// being detectable because this route wraps its errors.
func textureRefusal(path string, response *http.Response) error {
	apiErr := newAPIError(path, response)
	api, ok := apiErr.(*APIError)
	if !ok {
		return apiErr
	}
	if api.Unauthorized() {
		return api
	}

	return &TextureExportRefusal{Status: api.StatusCode, Reason: api.Reason, Detail: api.Message}
}

// MapTextures is the part of AUB's `GET /api/maps/{id}/texture-requirements`
// a map's card shows: the WADs its worldspawn declares, in declaration order,
// and how many distinct textures it uses. Read, never downloaded: the bundle
// itself is [Client.TextureExport], which a build fetches.
type MapTextures struct {
	MapID        string   `json:"map_id"`
	Revision     int      `json:"revision"`
	WADState     string   `json:"wad_state,omitempty"`
	WADsDeclared []string `json:"wads_declared"`
	TextureCount int      `json:"texture_count"`
}

// TextureRequirements asks AUB which WADs a map declares and how many
// textures it uses, at its latest revision.
func (c *Client) TextureRequirements(ctx context.Context, mapID string) (MapTextures, error) {
	if strings.TrimSpace(mapID) == "" {
		return MapTextures{}, fmt.Errorf("aub: a texture question needs a map id")
	}
	var out MapTextures
	err := c.do(ctx, http.MethodGet, "/api/maps/"+url.PathEscape(mapID)+"/texture-requirements", nil, nil, &out)
	if out.WADsDeclared == nil {
		out.WADsDeclared = []string{}
	}
	return out, err
}
