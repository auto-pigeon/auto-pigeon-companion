// The Companion API — AUB's versioned read surface, and the reason this package
// no longer guesses.
//
// # What replaced the guess
//
// The first version of this client assumed the `users` auth collection, assumed
// PocketBase's documented two-week token, and carried a `ListRecords` helper
// whose own comment said "the collection names and schemas the Companion reads
// are not confirmed". All three were the same mistake: a collection listing
// answers with whatever columns a collection happens to have, so a program
// written against it is written against AUB's SCHEMA rather than against its
// contract.
//
// `/api/companion/v1` is that contract. A fixed vocabulary of asset types, a
// fixed shape per answer, a version string in every response, server-side
// authorization on every list and every byte — and a capability document that
// states the auth collection and the token lifetime this deployment actually
// configured, so nothing here has to estimate either.
//
// # What this package still does not do
//
// It does not write to disk, and it does not decide what to keep. Fetching a
// revision returns a reader and its declared identity; verifying the bytes and
// publishing them atomically is internal/assetsync's, which is what keeps the
// verification in one place instead of at every call site.

package aub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CompanionAPIVersion is the contract this client is written against.
//
// Checked on every capability read. A deployment answering a different version
// is REFUSED rather than parsed optimistically: the shapes below are this
// version's, and a client that guessed its way through a later one would be back
// where this package started.
const CompanionAPIVersion = "aub-companion-api/1.0"

// CompanionPrefix is the route prefix. The version is in the path as well as in
// the body, so a client that got the wrong one gets a 404 rather than a
// successful answer in a shape it cannot read.
const CompanionPrefix = "/api/companion/v1"

// The asset types AUB serves. A closed vocabulary, shared with the Offline
// workspace contract; `capabilities` says which of them a given deployment
// declares.
const (
	AssetTypeMap             = "map"
	AssetTypeTextureSource   = "texture_source"
	AssetTypeEntityCatalogue = "entity_catalogue"
	AssetTypeGameProfile     = "game_profile"
	AssetTypePrefabPackage   = "prefab_package"
)

// The authorization paths a catalog is listed along.
const (
	ScopeOwned     = "owned"
	ScopeMember    = "member"
	ScopeWorkspace = "workspace"
	ScopePublic    = "public"
)

// How a type's revisions can be addressed.
const (
	// AddressingRevisionRows means a revision id names those bytes for ever.
	AddressingRevisionRows = "revision_rows"

	// AddressingCurrentOnly means the type keeps a counter and no per-version
	// row, so only `current` resolves and what it resolves to changes when the
	// asset is edited.
	AddressingCurrentOnly = "current_only"
)

// CurrentRevision is the revision id meaning "whatever is current".
const CurrentRevision = "current"

// ErrVersionMismatch is a deployment speaking a contract this build does not.
var ErrVersionMismatch = errors.New("aub: this backend speaks a different Companion API version")

// Capabilities is what a deployment offers.
type Capabilities struct {
	APIVersion string           `json:"api_version"`
	Session    SessionContract  `json:"session"`
	Download   DownloadContract `json:"download"`
	Types      []TypeCapability `json:"asset_types"`
	Page       PageLimits       `json:"page"`
	ServerTime string           `json:"server_time"`
}

// SessionContract is how long a token lasts and where to renew it.
type SessionContract struct {
	AuthCollection       string `json:"auth_collection"`
	TokenLifetimeSeconds int64  `json:"token_lifetime_seconds"`
	LoginPath            string `json:"login_path"`
	RefreshPath          string `json:"refresh_path"`

	// Revocable is false at every AUB. Published so this client can say
	// "signed out locally" and mean it, rather than implying a revocation that
	// did not happen.
	Revocable bool `json:"revocable"`
}

// Lifetime is the configured token lifetime, or zero when the deployment could
// not resolve one. Zero means "refresh when a request is rejected", never
// "already expired".
func (s SessionContract) Lifetime() time.Duration {
	if s.TokenLifetimeSeconds <= 0 {
		return 0
	}

	return time.Duration(s.TokenLifetimeSeconds) * time.Second
}

// DownloadContract is what the server promises about fetching bytes.
type DownloadContract struct {
	// RangeRequests is false at every AUB today. A client reads it rather than
	// probing, and RESTARTS an interrupted download rather than resuming it —
	// resuming against a server that ignores Range produces a file that is the
	// tail of one attempt glued to the whole of another.
	RangeRequests bool `json:"range_requests"`

	ETag                bool   `json:"etag"`
	ConditionalRequests bool   `json:"conditional_requests"`
	SignedURLs          bool   `json:"signed_urls"`
	DigestAlgorithm     string `json:"digest_algorithm"`
}

// TypeCapability is one asset type as a deployment serves it.
type TypeCapability struct {
	AssetType          string   `json:"asset_type"`
	RevisionAddressing string   `json:"revision_addressing"`
	History            bool     `json:"history"`
	Scopes             []string `json:"scopes"`
	HasVisibility      bool     `json:"has_visibility"`
}

// PageLimits is what a client may ask for.
type PageLimits struct {
	CatalogDefault int `json:"catalog_default_limit"`
	CatalogMax     int `json:"catalog_max_limit"`
	HistoryDefault int `json:"history_default_limit"`
	HistoryMax     int `json:"history_max_limit"`
}

// RevisionSummary is one version of one asset, without its files.
type RevisionSummary struct {
	// ID names exactly one version for ever. Empty for an `current_only` type,
	// which has no per-version row — empty means there is nothing to pin, not
	// that the pin failed.
	ID string `json:"revision_id"`

	Number int `json:"revision"`

	// Immutable reports whether ID will resolve to these same bytes later.
	Immutable bool `json:"immutable"`

	// ContentSHA256 is empty when the server recorded none. Empty means NOT
	// RECORDED — never "no content".
	ContentSHA256 string `json:"content_sha256"`

	AuthorUserID string `json:"author_user_id"`
	CreatedAt    string `json:"created_at"`
	Kind         string `json:"kind"`
	BaseRevision int    `json:"base_revision"`
}

// File is one file of one revision.
type File struct {
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256"`
}

// Asset is one catalog entry.
type Asset struct {
	AssetType          string           `json:"asset_type"`
	AssetID            string           `json:"asset_id"`
	DisplayName        string           `json:"display_name"`
	Game               string           `json:"game"`
	Visibility         string           `json:"visibility"`
	OwnerUserID        string           `json:"owner_user_id"`
	AccessPath         string           `json:"access_path"`
	Role               string           `json:"role"`
	CreatedAt          string           `json:"created_at"`
	RevisionAddressing string           `json:"revision_addressing"`
	CurrentRevision    *RevisionSummary `json:"current_revision"`
}

// CatalogPage is one page of the catalog.
type CatalogPage struct {
	APIVersion string  `json:"api_version"`
	Scope      string  `json:"scope"`
	Items      []Asset `json:"items"`
	NextCursor string  `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}

// AssetDetail is one asset with its revision count.
type AssetDetail struct {
	APIVersion    string `json:"api_version"`
	Asset         Asset  `json:"asset"`
	RevisionCount int    `json:"revision_count"`
}

// HistoryPage is one page of an asset's revisions.
type HistoryPage struct {
	APIVersion         string            `json:"api_version"`
	AssetType          string            `json:"asset_type"`
	AssetID            string            `json:"asset_id"`
	RevisionAddressing string            `json:"revision_addressing"`
	Items              []RevisionSummary `json:"items"`
	Total              int               `json:"total"`
	Offset             int               `json:"offset"`
	HasMore            bool              `json:"has_more"`
	Retention          string            `json:"retention"`
}

// Revision is one version with its files.
type Revision struct {
	RevisionSummary

	APIVersion string `json:"api_version"`
	AssetType  string `json:"asset_type"`
	AssetID    string `json:"asset_id"`

	// ManifestSHA256 identifies the whole revision: a digest over its ordered
	// file list. One string a build manifest carries and a later run compares,
	// without re-fetching anything.
	ManifestSHA256 string `json:"manifest_sha256"`

	Files      []File `json:"files"`
	TotalBytes int64  `json:"total_bytes"`
}

// FileByPath finds one declared file.
func (r Revision) FileByPath(path string) (File, bool) {
	for _, file := range r.Files {
		if file.Path == path {
			return file, true
		}
	}

	return File{}, false
}

// Capabilities reads what this deployment offers.
//
// The version is checked here rather than at each call site, because this is the
// call every session makes first and refusing early is the difference between
// one clear error and five confusing ones.
func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var out Capabilities
	if err := c.do(ctx, http.MethodGet, CompanionPrefix+"/capabilities", nil, nil, &out); err != nil {
		return Capabilities{}, err
	}
	if out.APIVersion != CompanionAPIVersion {
		return Capabilities{}, fmt.Errorf("%w: it speaks %q, this build speaks %q",
			ErrVersionMismatch, out.APIVersion, CompanionAPIVersion)
	}

	return out, nil
}

// CatalogQuery is one catalog request.
type CatalogQuery struct {
	// Scope is the authorization path being listed. Empty means the server's
	// default, which is `owned`.
	Scope string

	// Types narrows the walk. Empty means every type the deployment serves.
	Types []string

	Game string
	Name string

	// CreatedSince drops assets created before it.
	CreatedSince time.Time

	Cursor string
	Limit  int
}

func (q CatalogQuery) values() url.Values {
	values := url.Values{}
	if q.Scope != "" {
		values.Set("scope", q.Scope)
	}
	for _, assetType := range q.Types {
		values.Add("type", assetType)
	}
	if q.Game != "" {
		values.Set("game", q.Game)
	}
	if q.Name != "" {
		values.Set("name", q.Name)
	}
	if !q.CreatedSince.IsZero() {
		values.Set("created_since", q.CreatedSince.UTC().Format(time.RFC3339))
	}
	if q.Cursor != "" {
		values.Set("cursor", q.Cursor)
	}
	if q.Limit > 0 {
		values.Set("limit", strconv.Itoa(q.Limit))
	}

	return values
}

// Catalog reads one page.
func (c *Client) Catalog(ctx context.Context, query CatalogQuery) (CatalogPage, error) {
	var out CatalogPage
	if err := c.do(ctx, http.MethodGet, CompanionPrefix+"/catalog",
		query.values(), nil, &out); err != nil {
		return CatalogPage{}, err
	}

	return out, nil
}

// CatalogAll walks every page of one query.
//
// Bounded by `max` because an unbounded walk of an unbounded catalog is how a
// listing becomes a hang; zero means the server's own page default, once.
func (c *Client) CatalogAll(ctx context.Context, query CatalogQuery, max int) ([]Asset, error) {
	assets := []Asset{}
	for {
		page, err := c.Catalog(ctx, query)
		if err != nil {
			return nil, err
		}
		assets = append(assets, page.Items...)
		if !page.HasMore || page.NextCursor == "" {
			return assets, nil
		}
		if max > 0 && len(assets) >= max {
			return assets[:max], nil
		}
		query.Cursor = page.NextCursor
	}
}

// Asset reads one asset.
func (c *Client) Asset(ctx context.Context, assetType, assetID string) (AssetDetail, error) {
	var out AssetDetail
	if err := c.do(ctx, http.MethodGet, assetPath(assetType, assetID), nil, nil, &out); err != nil {
		return AssetDetail{}, err
	}

	return out, nil
}

// History reads one page of an asset's revisions.
func (c *Client) History(ctx context.Context, assetType, assetID string, limit, offset int) (HistoryPage, error) {
	values := url.Values{}
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		values.Set("offset", strconv.Itoa(offset))
	}

	var out HistoryPage
	if err := c.do(ctx, http.MethodGet, assetPath(assetType, assetID)+"/revisions",
		values, nil, &out); err != nil {
		return HistoryPage{}, err
	}

	return out, nil
}

// Revision reads one exact revision, with its files.
func (c *Client) Revision(ctx context.Context, assetType, assetID, revisionID string) (Revision, error) {
	if revisionID == "" {
		revisionID = CurrentRevision
	}

	var out Revision
	if err := c.do(ctx, http.MethodGet,
		assetPath(assetType, assetID)+"/revisions/"+url.PathEscape(revisionID),
		nil, nil, &out); err != nil {
		return Revision{}, err
	}

	return out, nil
}

// Download is one file's bytes, open.
//
// The caller closes Body. Nothing here verifies the digest — internal/assetsync
// does, once, on the way to disk, because a verification at each call site is one
// somebody eventually forgets.
type Download struct {
	// Body is the file's logical bytes.
	Body io.ReadCloser

	// ContentLength is what the server declared, or -1 when it declared nothing.
	ContentLength int64

	// ETag is the server's, with its quotes stripped: the file's logical SHA-256.
	ETag string

	// Revision names the version that was actually served, which is how a client
	// that asked for `current` learns which one it got without a second request.
	RevisionID     string
	Revision       int
	Immutable      bool
	ManifestSHA256 string

	// NotModified reports that the caller's `If-None-Match` matched and no bytes
	// were sent. Body is then an empty reader rather than nil, so a caller that
	// closes unconditionally is correct.
	NotModified bool
}

// DownloadFile fetches one file of one revision.
//
// `ifNoneMatch` is a digest the caller already holds; an empty string asks
// unconditionally. A matching digest answers NotModified with no bytes, which is
// what makes re-syncing an unchanged asset cost a few hundred bytes.
func (c *Client) DownloadFile(ctx context.Context, assetType, assetID, revisionID, path,
	ifNoneMatch string) (*Download, error) {
	if revisionID == "" {
		revisionID = CurrentRevision
	}
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + assetPath(assetType, assetID) +
		"/revisions/" + url.PathEscape(revisionID) + "/files/" + escapePath(path)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("aub: building the download request: %w", err)
	}
	if c.token != "" {
		request.Header.Set("Authorization", c.token)
	}
	if ifNoneMatch != "" {
		request.Header.Set("If-None-Match", `"`+ifNoneMatch+`"`)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("aub: downloading %s: %w", path, err)
	}
	if response.StatusCode == http.StatusNotModified {
		response.Body.Close()

		return &Download{
			Body:        io.NopCloser(strings.NewReader("")),
			NotModified: true,
			ETag:        strings.Trim(response.Header.Get("ETag"), `"`),
		}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()

		return nil, newAPIError(endpoint.Path, response)
	}

	revision, _ := strconv.Atoi(response.Header.Get("X-Companion-Revision"))
	immutable := response.Header.Get("X-Companion-Revision-Immutable") == "true"

	return &Download{
		Body:           response.Body,
		ContentLength:  response.ContentLength,
		ETag:           strings.Trim(response.Header.Get("ETag"), `"`),
		RevisionID:     response.Header.Get("X-Companion-Revision-Id"),
		Revision:       revision,
		Immutable:      immutable,
		ManifestSHA256: response.Header.Get("X-Companion-Manifest-Sha256"),
	}, nil
}

func assetPath(assetType, assetID string) string {
	return CompanionPrefix + "/assets/" + url.PathEscape(assetType) + "/" + url.PathEscape(assetID)
}

// escapePath escapes each segment of a declared file path.
//
// A revision's file paths may contain a separator — `e1u1/pow12_1.wal` is an
// ordinary Quake II texture — and the route's trailing wildcard accepts one, so
// the SEPARATORS are kept and everything else is escaped. Escaping the whole
// string would turn a legitimate path into one the server holds no file at.
func escapePath(path string) string {
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}

	return strings.Join(segments, "/")
}
