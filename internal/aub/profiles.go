package aub

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// The published profile catalog — AUB's third surface, and the one three
// programs read.
//
// It is deliberately NOT under `/api/companion/v1`. That prefix is the
// Companion's asset door and answers *may this person read this asset of mine*;
// this catalog answers *what has anybody published, and may I install it*, and
// AUG and AUP read it too. A path named for one client would be wrong for the
// other two.
//
// Nothing here decides anything about a document. It fetches bytes and metadata;
// deciding whether the bytes are what they claim to be, whether they validate,
// what changed and what is being asked for is `internal/publish`'s, against
// `internal/profile`. Keeping that split is what stops a network client
// acquiring an opinion about the profile format.

// ProfileCatalogPrefix is where the catalog lives.
const ProfileCatalogPrefix = "/api/companion-profiles"

// ProfileCatalogSchema is the contract this client is written against. Checked
// on every answer that carries one, for the reason [CompanionAPIVersion] gives.
const ProfileCatalogSchema = "aub-companion-profile-catalog/1.0"

// Published visibility states.
const (
	PublishedPrivate  = "private"
	PublishedUnlisted = "unlisted"
	PublishedPublic   = "public"
)

// PublishedPublisher is who published a listing.
//
// `Nickname` is the account AUB knows; `DeclaredName` is what the DOCUMENT says.
// The two are separate because the second is a claim the document makes about
// itself, and a document claiming to be from somebody it is not is exactly what
// a reader should be able to notice.
type PublishedPublisher struct {
	UserID       string `json:"user_id"`
	Nickname     string `json:"nickname,omitempty"`
	DeclaredName string `json:"declared_name,omitempty"`
}

// PublishedPlatform is one target and what the document claims about it.
type PublishedPlatform struct {
	Platform string `json:"platform"`
	Status   string `json:"status"`
	Note     string `json:"note,omitempty"`
}

// PublishedPermissions is AUB's summary of what a version's document DECLARES.
//
// A summary for a listing, never an authorization. What decides is
// `profile.Authorize` here, against the document, at one exact digest.
type PublishedPermissions struct {
	ReadRoots    []string `json:"read_roots,omitempty"`
	WriteRoots   []string `json:"write_roots,omitempty"`
	Network      bool     `json:"network"`
	NetworkHosts []string `json:"network_hosts,omitempty"`
	Environment  []string `json:"environment,omitempty"`
	Acquisition  []string `json:"acquisition,omitempty"`
}

// PublishedProfile is one catalog entry.
type PublishedProfile struct {
	ID        string             `json:"id"`
	ProfileID string             `json:"profile_id"`
	Kind      string             `json:"kind"`
	Publisher PublishedPublisher `json:"publisher"`

	Name        string `json:"name"`
	Summary     string `json:"summary,omitempty"`
	LicenseSPDX string `json:"license_spdx,omitempty"`
	Source      struct {
		Homepage   string `json:"homepage,omitempty"`
		Repository string `json:"repository,omitempty"`
	} `json:"source"`

	GameFamilies []string            `json:"game_families"`
	GameSlugs    []string            `json:"game_slugs"`
	Capabilities []string            `json:"capabilities"`
	Platforms    []string            `json:"platforms"`
	Support      []PublishedPlatform `json:"platform_support"`

	Visibility string `json:"visibility"`
	Trust      string `json:"trust"`
	VerifiedAt string `json:"verified_at,omitempty"`

	Moderation     string `json:"moderation_state"`
	ModerationNote string `json:"moderation_note,omitempty"`

	Maintenance     string `json:"maintenance"`
	MaintenanceNote string `json:"maintenance_note,omitempty"`
	SupersededBy    string `json:"superseded_by,omitempty"`

	LatestVersion string `json:"latest_version,omitempty"`
	VersionCount  int    `json:"version_count"`
	AccessPath    string `json:"access_path"`

	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// PublishedVersion is one published version. `Document` is present only on the
// single-version route.
type PublishedVersion struct {
	Version         string `json:"version"`
	Digest          string `json:"digest"`
	Bytes           int    `json:"bytes"`
	SchemaVersion   string `json:"schema_version,omitempty"`
	Kind            string `json:"kind"`
	UpstreamVersion string `json:"upstream_version,omitempty"`
	LicenseSPDX     string `json:"license_spdx,omitempty"`

	Platforms    []string             `json:"platforms"`
	Support      []PublishedPlatform  `json:"platform_support"`
	Capabilities []string             `json:"capabilities"`
	GameFamilies []string             `json:"game_families"`
	Permissions  PublishedPermissions `json:"permissions"`

	PublishedBy string `json:"published_by,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`

	Yanked              bool   `json:"yanked"`
	YankedAt            string `json:"yanked_at,omitempty"`
	YankReason          string `json:"yank_reason,omitempty"`
	SupersededByVersion string `json:"superseded_by_version,omitempty"`

	Document string `json:"document,omitempty"`
}

// ProfileCatalogPage is one page of the catalog.
type ProfileCatalogPage struct {
	SchemaVersion string             `json:"schema_version"`
	Profiles      []PublishedProfile `json:"profiles"`
	NextCursor    string             `json:"next_cursor,omitempty"`
}

// ProfileVocabulary is the closed vocabularies and the two notes.
//
// Read rather than hard-coded, so a client cannot invent a state — and so the
// two sentences a catalog of other people's tools must say arrive from one
// place rather than being retyped in four.
type ProfileVocabulary struct {
	SchemaVersion string   `json:"schema_version"`
	Kinds         []string `json:"kinds"`
	Visibility    []string `json:"visibility"`
	Trust         []struct {
		ID    string `json:"id"`
		Means string `json:"means"`
	} `json:"trust"`
	Moderation  []string `json:"moderation_states"`
	Maintenance []string `json:"maintenance_states"`
	Support     []struct {
		ID    string `json:"id"`
		Means string `json:"means"`
	} `json:"platform_support"`
	Reports           []string `json:"report_categories"`
	MaxDocumentBytes  int      `json:"max_document_bytes"`
	MaxVersions       int      `json:"max_versions_per_profile"`
	DefaultPageLimit  int      `json:"default_page_limit"`
	MaximumPageLimit  int      `json:"maximum_page_limit"`
	ServerAwardedList []string `json:"server_awarded_trust"`
	LicenceNote       string   `json:"licence_note"`
	EndorsementNote   string   `json:"endorsement_note"`
}

// ProfileQuery narrows a catalog page. Every member is optional; an empty query
// is discovery's first page.
type ProfileQuery struct {
	// Mine lists the caller's own listings at every visibility, rather than the
	// public catalog.
	Mine       bool
	Kind       string
	Game       string
	Platform   string
	Capability string
	Trust      string
	// Maintained is a tri-state: nil is "either".
	Maintained *bool
	Text       string
	Cursor     string
	Limit      int
}

func (q ProfileQuery) values() url.Values {
	values := url.Values{}
	if q.Mine {
		values.Set("scope", "mine")
	}
	for key, value := range map[string]string{
		"kind": q.Kind, "game": q.Game, "platform": q.Platform,
		"capability": q.Capability, "trust": q.Trust, "q": q.Text, "cursor": q.Cursor,
	} {
		if value != "" {
			values.Set(key, value)
		}
	}
	if q.Maintained != nil {
		values.Set("maintained", strconv.FormatBool(*q.Maintained))
	}
	if q.Limit > 0 {
		values.Set("limit", strconv.Itoa(q.Limit))
	}

	return values
}

// ProfileVocabulary reads the closed vocabularies and the two notes.
func (c *Client) ProfileVocabulary(ctx context.Context) (ProfileVocabulary, error) {
	var out ProfileVocabulary
	if err := c.do(ctx, http.MethodGet, ProfileCatalogPrefix+"/vocabulary", nil, nil, &out); err != nil {
		return ProfileVocabulary{}, err
	}

	return out, checkProfileSchema(out.SchemaVersion)
}

// ProfileCatalog reads one page.
func (c *Client) ProfileCatalog(ctx context.Context, query ProfileQuery) (ProfileCatalogPage, error) {
	var out ProfileCatalogPage
	if err := c.do(ctx, http.MethodGet, ProfileCatalogPrefix, query.values(), nil, &out); err != nil {
		return ProfileCatalogPage{}, err
	}

	return out, checkProfileSchema(out.SchemaVersion)
}

// ProfileCatalogAll walks the cursor to at most max listings.
//
// Bounded, deliberately: an unbounded walk of somebody else's catalog is a
// client that hangs on a deployment with ten thousand listings in it.
func (c *Client) ProfileCatalogAll(ctx context.Context, query ProfileQuery, max int) ([]PublishedProfile, error) {
	if max <= 0 {
		max = 200
	}
	var out []PublishedProfile
	for len(out) < max {
		page, err := c.ProfileCatalog(ctx, query)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Profiles...)
		if page.NextCursor == "" || len(page.Profiles) == 0 {
			break
		}
		query.Cursor = page.NextCursor
	}
	if len(out) > max {
		out = out[:max]
	}

	return out, nil
}

// PublishedProfileDetail is one listing with its version metadata.
type PublishedProfileDetail struct {
	SchemaVersion string             `json:"schema_version"`
	Profile       PublishedProfile   `json:"profile"`
	Versions      []PublishedVersion `json:"versions"`
}

// PublishedProfileByID reads one listing and its versions.
func (c *Client) PublishedProfileByID(ctx context.Context, listingID string) (PublishedProfileDetail, error) {
	var out PublishedProfileDetail
	path := ProfileCatalogPrefix + "/" + url.PathEscape(listingID)
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return PublishedProfileDetail{}, err
	}

	return out, checkProfileSchema(out.SchemaVersion)
}

// PublishedVersionDetail is one version, with its exact bytes.
type PublishedVersionDetail struct {
	SchemaVersion string           `json:"schema_version"`
	ProfileID     string           `json:"profile_id"`
	Kind          string           `json:"kind"`
	Trust         string           `json:"trust"`
	Version       PublishedVersion `json:"version"`
}

// PublishedProfileVersion reads one version and the document it names.
func (c *Client) PublishedProfileVersion(ctx context.Context, listingID, version string) (
	PublishedVersionDetail, error,
) {
	var out PublishedVersionDetail
	path := ProfileCatalogPrefix + "/" + url.PathEscape(listingID) +
		"/versions/" + url.PathEscape(version)
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return PublishedVersionDetail{}, err
	}

	return out, checkProfileSchema(out.SchemaVersion)
}

// PublishProfileResult is what a publication answered.
type PublishProfileResult struct {
	SchemaVersion string           `json:"schema_version"`
	Created       bool             `json:"created"`
	Profile       PublishedProfile `json:"profile"`
	Version       PublishedVersion `json:"version"`
}

// PublishProfile sends one canonical document.
//
// `document` is bytes and travels as a JSON STRING, which is the contract's own
// shape: a nested object would be re-serialized by whichever encoder handled it,
// and the digest is over exact bytes. The digest is sent as well and the server
// recomputes it — belt and braces on the one fact that has to survive the trip.
func (c *Client) PublishProfile(ctx context.Context, document []byte, digest, visibility string) (
	PublishProfileResult, error,
) {
	body := map[string]any{"document": string(document), "digest": digest}
	if visibility != "" {
		body["visibility"] = visibility
	}
	var out PublishProfileResult
	if err := c.do(ctx, http.MethodPost, ProfileCatalogPrefix, nil, body, &out); err != nil {
		return PublishProfileResult{}, err
	}

	return out, checkProfileSchema(out.SchemaVersion)
}

// UpdatePublishedListing changes a listing's mutable metadata.
//
// The map is passed through so that "not mentioned" and "set to empty" stay
// different requests, which is what the server's pointer members are for. There
// is no `trust` or `moderation_state` key to send and this client must never
// grow one: they are the deployment operator's, and a client that offered the
// field would be offering something that can only be refused.
func (c *Client) UpdatePublishedListing(ctx context.Context, listingID string, patch map[string]any) (
	PublishedProfile, error,
) {
	var out struct {
		Profile PublishedProfile `json:"profile"`
	}
	path := ProfileCatalogPrefix + "/" + url.PathEscape(listingID)
	if err := c.do(ctx, http.MethodPatch, path, nil, patch, &out); err != nil {
		return PublishedProfile{}, err
	}

	return out.Profile, nil
}

// YankPublishedVersion withdraws one version, keeping it readable.
func (c *Client) YankPublishedVersion(ctx context.Context, listingID, version, reason, supersededBy string) (
	PublishedVersion, error,
) {
	body := map[string]any{"reason": reason}
	if supersededBy != "" {
		body["superseded_by_version"] = supersededBy
	}
	var out struct {
		Version PublishedVersion `json:"version"`
	}
	path := ProfileCatalogPrefix + "/" + url.PathEscape(listingID) +
		"/versions/" + url.PathEscape(version) + "/yank"
	if err := c.do(ctx, http.MethodPost, path, nil, body, &out); err != nil {
		return PublishedVersion{}, err
	}

	return out.Version, nil
}

// ReportPublishedProfile files one moderation report.
func (c *Client) ReportPublishedProfile(ctx context.Context, listingID, category, version, detail string) (string, error) {
	body := map[string]any{"category": category}
	if version != "" {
		body["version"] = version
	}
	if detail != "" {
		body["detail"] = detail
	}
	var out struct {
		ReportID string `json:"report_id"`
	}
	path := ProfileCatalogPrefix + "/" + url.PathEscape(listingID) + "/reports"
	if err := c.do(ctx, http.MethodPost, path, nil, body, &out); err != nil {
		return "", err
	}

	return out.ReportID, nil
}

func checkProfileSchema(got string) error {
	if got == "" || got == ProfileCatalogSchema {
		return nil
	}

	return fmt.Errorf("aub: this deployment serves %s and this build reads %s",
		got, ProfileCatalogSchema)
}

// ModeratedReason is the refusal a hidden listing produces. A caller shows the
// note rather than reporting the listing as missing: somebody holding a copy is
// exactly who the hiding is a message to.
const ModeratedReason = "companion_profile_moderated"

// ProfileNotFoundReason is absent-or-not-yours, which are deliberately one
// answer.
const ProfileNotFoundReason = "companion_profile_not_found"

// IsProfileNotFound reports the catalog's absent-or-forbidden answer.
func IsProfileNotFound(err error) bool {
	var apiErr *APIError
	if !asAPIError(err, &apiErr) {
		return false
	}

	return apiErr.Reason == ProfileNotFoundReason ||
		(apiErr.Reason == "" && apiErr.StatusCode == http.StatusNotFound)
}

func asAPIError(err error, target **APIError) bool {
	for err != nil {
		if apiErr, ok := err.(*APIError); ok {
			*target = apiErr

			return true
		}
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}

	return false
}
