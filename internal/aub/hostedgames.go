package aub

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The hosted-game lifecycle — AUB's fourth surface, and the one with a clock on
// it.
//
// It is deliberately not under `/api/companion/v1` and not under the profile
// catalog. The asset door answers *may this person read this asset of mine*; the
// catalog answers *what has anybody published*; this answers *who is hosting a
// game right now, and may I join it*. Two programs read it — this one hosts and
// joins, AUG draws the listing — so it has a path of its own.
//
// Nothing here starts, stops or watches a process. It registers a lease, renews
// it, ends it and resolves a join link; deciding WHEN to do any of that is
// `internal/hostgame`'s, against a job this Companion is supervising. Keeping
// that split is what stops a network client acquiring an opinion about whether a
// server is healthy.

// HostedGamePrefix is where the lifecycle lives.
const HostedGamePrefix = "/api/hosted-games"

// HostedGameSchema is the contract this client is written against. Checked on
// every answer that carries one, for the reason [CompanionAPIVersion] gives.
const HostedGameSchema = "aub-hosted-game-lifecycle/1.0"

// Modes a game can be run in.
const (
	ModeListen    = "listen"
	ModeDedicated = "dedicated"
)

// Visibility states, which are the same three words the profile catalog uses and
// mean the same things.
const (
	GamePrivate  = "private"
	GameUnlisted = "unlisted"
	GamePublic   = "public"
)

// Lifecycle reasons this program may report.
//
// Four, and the two AUB owns — `heartbeat_missed` and `superseded` — are absent
// on purpose: they are conclusions the server draws, and a client that could send
// either would be able to write a history that did not happen.
const (
	ReasonHostStopped    = "host_stopped"
	ReasonHostCrashed    = "host_crashed"
	ReasonOwnerSignedOut = "owner_signed_out"
	ReasonNetworkLost    = "network_lost"
)

// HostedGame is one lease as AUB serves it.
//
// Two fields carry their own provenance and nothing here may lose it.
// `PlayersSource` is `host` and never anything else — a count that arrived in a
// request body — and `Reachability` distinguishes an address something answered
// at from one nobody has checked. A caller rendering either without the other is
// making a claim this program has no basis for.
type HostedGame struct {
	ID    string `json:"id"`
	Title string `json:"title"`

	HostNickname string `json:"host_nickname,omitempty"`
	OwnedByMe    bool   `json:"owned_by_me"`
	AccessPath   string `json:"access_path"`
	Visibility   string `json:"visibility"`
	WorkspaceID  string `json:"workspace_id,omitempty"`

	State  string `json:"state"`
	Reason string `json:"lifecycle_reason"`

	MapID       string `json:"map_id"`
	MapName     string `json:"map_name,omitempty"`
	MapRevision int    `json:"map_revision"`
	MapDigest   string `json:"map_content_sha256,omitempty"`
	MapPublic   bool   `json:"map_public"`
	PackageSHA  string `json:"package_sha256,omitempty"`

	GameFamily       string `json:"game_family"`
	GameSlug         string `json:"game_slug,omitempty"`
	EngineRuntime    string `json:"engine_runtime"`
	EngineVersion    string `json:"engine_version,omitempty"`
	EngineProfileID  string `json:"engine_profile_id,omitempty"`
	EngineProfileRef string `json:"engine_profile_ref,omitempty"`
	EngineProfileVer string `json:"engine_profile_version,omitempty"`

	Mode   string `json:"mode"`
	Region string `json:"region,omitempty"`

	Endpoint         string `json:"endpoint,omitempty"`
	EndpointScope    string `json:"endpoint_scope"`
	EndpointWithheld bool   `json:"endpoint_withheld,omitempty"`
	Reachability     string `json:"reachability"`

	PlayersCurrent    int    `json:"players_current"`
	PlayersMax        int    `json:"players_max"`
	PlayersObservable bool   `json:"players_observable"`
	PlayersSource     string `json:"players_source"`

	CreatedAt     time.Time  `json:"created_at"`
	HeartbeatAt   time.Time  `json:"last_heartbeat_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	StaleForMS    int64      `json:"stale_for_ms"`
	HeartbeatSecs int        `json:"heartbeat_interval_seconds"`
	Heartbeats    int        `json:"heartbeat_count"`

	Joinable bool `json:"joinable"`

	// JoinContent is what a joiner needs beyond their own installation (244F).
	JoinContent JoinContent `json:"join_content"`
}

// Live reports whether AUB called this game live when it answered.
func (g HostedGame) Live() bool { return g.State == "live" }

// HeartbeatInterval is the cadence AUB expects, as a duration.
//
// Read from the answer rather than chosen here. A client that picked its own
// number would pick a different one from the next client, and a beat slower than
// the server's tolerance is indistinguishable from a departed host.
func (g HostedGame) HeartbeatInterval() time.Duration {
	if g.HeartbeatSecs <= 0 {
		return 30 * time.Second
	}

	return time.Duration(g.HeartbeatSecs) * time.Second
}

// HostedGameVocabulary is the closed enums, the clocks and the two notes.
type HostedGameVocabulary struct {
	SchemaVersion string `json:"schema_version"`

	Modes          []string `json:"modes"`
	Visibility     []string `json:"visibility"`
	States         []string `json:"states"`
	ClientReasons  []string `json:"client_reportable_reasons"`
	EndpointScopes []string `json:"endpoint_scopes"`
	Regions        []string `json:"regions"`

	HeartbeatIntervalSeconds int `json:"heartbeat_interval_seconds"`
	LeaseTTLSeconds          int `json:"lease_ttl_seconds"`
	MaxLeaseLifetimeSeconds  int `json:"max_lease_lifetime_seconds"`
	JoinTicketTTLSeconds     int `json:"join_ticket_ttl_seconds"`
	PlayerCeiling            int `json:"player_ceiling"`
	MaxLeasesPerAccount      int `json:"max_live_games_per_account"`

	JoinScheme string `json:"join_link_scheme"`
	JoinFormat string `json:"join_link_format"`

	OccupancyNote    string `json:"occupancy_note"`
	ReachabilityNote string `json:"reachability_note"`
}

// HostedGameRegistration is what this program tells AUB about a server it is
// running.
//
// Everything here is a CLAIM except the map and the revision, which AUB looks up
// under this account's own read authority. `ConfirmExposure` is the user having
// seen the preview, and it is a separate field from anything else for that
// reason: it is not a setting, it is a record that somebody looked.
type HostedGameRegistration struct {
	Title       string `json:"title"`
	MapID       string `json:"map_id"`
	MapRevision int    `json:"map_revision,omitempty"`
	PackageSHA  string `json:"package_sha256,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`

	GameFamily       string `json:"game_family"`
	GameSlug         string `json:"game_slug,omitempty"`
	EngineRuntime    string `json:"engine_runtime"`
	EngineVersion    string `json:"engine_version,omitempty"`
	EngineProfileID  string `json:"engine_profile_id,omitempty"`
	EngineProfileRef string `json:"engine_profile_ref,omitempty"`
	EngineProfileVer string `json:"engine_profile_version,omitempty"`

	Mode         string `json:"mode"`
	EndpointHost string `json:"endpoint_host"`
	EndpointPort int    `json:"endpoint_port"`
	Region       string `json:"region,omitempty"`

	PlayersCurrent    int  `json:"players_current,omitempty"`
	PlayersMax        int  `json:"players_max,omitempty"`
	PlayersObservable bool `json:"players_observable,omitempty"`

	Visibility string `json:"visibility"`

	// ProcessIdentity is this program's evidence that a registration comes from
	// the same running process as the last one. It is what makes a restart reclaim
	// a lease rather than leave a second listing beside one nobody can stop, and
	// AUB accepts it only WITHIN this account's own leases — a value a client
	// chooses can never be an authority.
	ProcessIdentity string `json:"process_identity,omitempty"`
	ClientVersion   string `json:"client_version,omitempty"`

	// ContentRequirement is `package` or `none`; ContentIdentity names the content
	// a `none` game runs on. Empty with a package digest means `package`.
	ContentRequirement string `json:"content_requirement,omitempty"`
	ContentIdentity    string `json:"content_identity,omitempty"`

	ConfirmExposure bool `json:"confirm_exposure"`
}

// ExposedField is one thing a listing will say, and to whom.
type ExposedField struct {
	Field    string `json:"field"`
	Value    string `json:"value"`
	SeenBy   string `json:"seen_by"`
	Withheld bool   `json:"withheld,omitempty"`
}

// HostedGamePreview is what a person is shown before they confirm.
//
// The same computation the registration performs, run without writing anything,
// which is what makes it a review rather than a second rendering that could
// disagree with what gets published.
type HostedGamePreview struct {
	SchemaVersion string `json:"schema_version"`

	Endpoint          string   `json:"endpoint,omitempty"`
	EndpointScope     string   `json:"endpoint_scope"`
	EndpointPublished bool     `json:"endpoint_published"`
	Reachability      string   `json:"reachability"`
	Guidance          []string `json:"network_guidance,omitempty"`

	Visibility string `json:"visibility"`
	Audience   string `json:"audience"`

	MapName    string `json:"map_name,omitempty"`
	MapPublic  bool   `json:"map_public"`
	MapWarning string `json:"map_warning,omitempty"`

	JoinContent JoinContent `json:"join_content"`

	Exposed []ExposedField `json:"exposed_fields"`
}

// HostedGameResult is what a registration, a heartbeat or a stop answers with.
type HostedGameResult struct {
	SchemaVersion string     `json:"schema_version"`
	Reclaimed     bool       `json:"reclaimed"`
	Game          HostedGame `json:"game"`
	NextBeatSecs  int        `json:"next_heartbeat_in_seconds,omitempty"`
	ServerTime    time.Time  `json:"server_time,omitempty"`
}

// HostedGamePage is one listing page.
type HostedGamePage struct {
	SchemaVersion string       `json:"schema_version"`
	Games         []HostedGame `json:"games"`
	NextCursor    string       `json:"next_cursor,omitempty"`
	ServerTime    time.Time    `json:"server_time"`
}

// HostedGameDetail is one game's page.
type HostedGameDetail struct {
	SchemaVersion string         `json:"schema_version"`
	Game          HostedGame     `json:"game"`
	ServerTime    time.Time      `json:"server_time"`
	Guidance      []string       `json:"network_guidance,omitempty"`
	Assets        *AssetProspect `json:"assets,omitempty"`
}

// AssetProspect is whether this account may download the map being played.
type AssetProspect struct {
	Readable bool   `json:"readable"`
	Path     string `json:"access_path,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// HostedGameTicket is a minted join link.
type HostedGameTicket struct {
	ID        string    `json:"ticket_id"`
	Link      string    `json:"link"`
	GameID    string    `json:"game_id"`
	ExpiresAt time.Time `json:"expires_at"`
	TTLSecs   int       `json:"expires_in_seconds"`
}

// HostedGameJoin is everything needed to join, and nothing else.
type HostedGameJoin struct {
	SchemaVersion string `json:"schema_version"`

	GameID string `json:"game_id"`
	Title  string `json:"title"`
	Host   string `json:"host_nickname,omitempty"`

	Endpoint     string `json:"endpoint"`
	EndpointHost string `json:"endpoint_host"`
	EndpointPort int    `json:"endpoint_port"`
	Reachability string `json:"reachability"`
	Mode         string `json:"mode"`

	GameFamily       string `json:"game_family"`
	GameSlug         string `json:"game_slug,omitempty"`
	EngineRuntime    string `json:"engine_runtime"`
	EngineVersion    string `json:"engine_version,omitempty"`
	EngineProfileID  string `json:"engine_profile_id,omitempty"`
	EngineProfileRef string `json:"engine_profile_ref,omitempty"`
	EngineProfileVer string `json:"engine_profile_version,omitempty"`

	MapID       string `json:"map_id"`
	MapName     string `json:"map_name,omitempty"`
	MapRevision int    `json:"map_revision"`
	MapDigest   string `json:"map_content_sha256,omitempty"`
	PackageSHA  string `json:"package_sha256,omitempty"`

	Assets AssetProspect `json:"assets"`

	// JoinContent carries this reader's answer, and its package digest is the one
	// the ticket was minted against.
	JoinContent JoinContent `json:"join_content"`
	// EndpointKey is AUB's normalized `host:port`, the spelling a setup compares.
	EndpointKey string `json:"endpoint_key,omitempty"`

	// Action is the engine action to run, named by AUB so a client cannot invent
	// one. It is always `join_server`.
	Action string `json:"engine_action"`

	Warnings []string `json:"warnings,omitempty"`
}

// HostedGameQuery narrows a listing.
type HostedGameQuery struct {
	Scope      string
	GameFamily string
	Region     string
	Mode       string
	Cursor     string
	Limit      int
}

func (q HostedGameQuery) values() url.Values {
	values := url.Values{}
	for key, value := range map[string]string{
		"scope": q.Scope, "game_family": q.GameFamily, "region": q.Region,
		"mode": q.Mode, "cursor": q.Cursor,
	} {
		if value != "" {
			values.Set(key, value)
		}
	}
	if q.Limit > 0 {
		values.Set("limit", strconv.Itoa(q.Limit))
	}

	return values
}

func checkHostedGameSchema(version string) error {
	if version == "" || version == HostedGameSchema {
		return nil
	}

	return fmt.Errorf("aub: this deployment serves %s and this Companion is written against %s",
		version, HostedGameSchema)
}

// HostedGameVocabularyDoc reads the closed vocabularies and the clocks.
func (c *Client) HostedGameVocabularyDoc(ctx context.Context) (HostedGameVocabulary, error) {
	var out HostedGameVocabulary
	if err := c.do(ctx, http.MethodGet, HostedGamePrefix+"/vocabulary", nil, nil, &out); err != nil {
		return HostedGameVocabulary{}, err
	}

	return out, checkHostedGameSchema(out.SchemaVersion)
}

// HostedGames reads one listing page.
func (c *Client) HostedGames(ctx context.Context, query HostedGameQuery) (HostedGamePage, error) {
	var out HostedGamePage
	if err := c.do(ctx, http.MethodGet, HostedGamePrefix, query.values(), nil, &out); err != nil {
		return HostedGamePage{}, err
	}

	return out, checkHostedGameSchema(out.SchemaVersion)
}

// HostedGameByID reads one game.
func (c *Client) HostedGameByID(ctx context.Context, gameID string) (HostedGameDetail, error) {
	var out HostedGameDetail
	path := HostedGamePrefix + "/" + url.PathEscape(gameID)
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return HostedGameDetail{}, err
	}

	return out, checkHostedGameSchema(out.SchemaVersion)
}

// PreviewHostedGame asks what a registration would publish, without writing.
//
// `ConfirmExposure` is forced true on the way out: a preview is the review, so it
// must never demand the confirmation it exists to inform.
func (c *Client) PreviewHostedGame(ctx context.Context, registration HostedGameRegistration) (
	HostedGamePreview, error,
) {
	registration.ConfirmExposure = true
	var out HostedGamePreview
	if err := c.do(ctx, http.MethodPost, HostedGamePrefix+"/preview", nil, registration, &out); err != nil {
		return HostedGamePreview{}, err
	}

	return out, checkHostedGameSchema(out.SchemaVersion)
}

// RegisterHostedGame lists a game, or reclaims this program's own lease.
//
// It REFUSES to send an unconfirmed registration rather than letting AUB refuse
// it. The server's check is the authority; this one exists so that a caller who
// forgot the review gets a message naming the review rather than an HTTP error
// naming a field.
func (c *Client) RegisterHostedGame(ctx context.Context, registration HostedGameRegistration) (
	HostedGameResult, error,
) {
	if !registration.ConfirmExposure {
		return HostedGameResult{}, fmt.Errorf(
			"aub: a game is listed after its host has seen what the listing will say. " +
				"Read the preview first, then register with the confirmation")
	}
	var out HostedGameResult
	if err := c.do(ctx, http.MethodPost, HostedGamePrefix, nil, registration, &out); err != nil {
		return HostedGameResult{}, err
	}

	return out, checkHostedGameSchema(out.SchemaVersion)
}

// HeartbeatBody is what a beat may carry.
//
// Pointers, because "I have nothing to report" and "zero players" are different
// sentences: AUB leaves the counts alone for the first and writes them for the
// second, and a struct of plain ints could only ever say the second.
type HeartbeatBody struct {
	PlayersCurrent    *int  `json:"players_current,omitempty"`
	PlayersMax        *int  `json:"players_max,omitempty"`
	PlayersObservable *bool `json:"players_observable,omitempty"`
}

// HeartbeatHostedGame renews a lease.
func (c *Client) HeartbeatHostedGame(ctx context.Context, gameID string, body HeartbeatBody) (
	HostedGameResult, error,
) {
	var out HostedGameResult
	path := HostedGamePrefix + "/" + url.PathEscape(gameID) + "/heartbeat"
	if err := c.do(ctx, http.MethodPost, path, nil, body, &out); err != nil {
		return HostedGameResult{}, err
	}

	return out, checkHostedGameSchema(out.SchemaVersion)
}

// StopHostedGame ends a lease with a reason this program observed.
func (c *Client) StopHostedGame(ctx context.Context, gameID, reason string) (HostedGameResult, error) {
	var out HostedGameResult
	path := HostedGamePrefix + "/" + url.PathEscape(gameID) + "/stop"
	body := map[string]string{"reason": reason}
	if err := c.do(ctx, http.MethodPost, path, nil, body, &out); err != nil {
		return HostedGameResult{}, err
	}

	return out, checkHostedGameSchema(out.SchemaVersion)
}

// MintJoinLink asks for a join link for this account.
func (c *Client) MintJoinLink(ctx context.Context, gameID string) (HostedGameTicket, error) {
	var out struct {
		SchemaVersion string           `json:"schema_version"`
		Ticket        HostedGameTicket `json:"ticket"`
	}
	path := HostedGamePrefix + "/" + url.PathEscape(gameID) + "/join"
	if err := c.do(ctx, http.MethodPost, path, nil, map[string]any{}, &out); err != nil {
		return HostedGameTicket{}, err
	}
	if err := checkHostedGameSchema(out.SchemaVersion); err != nil {
		return HostedGameTicket{}, err
	}

	return out.Ticket, nil
}

// ResolveJoinLink redeems a join link and answers with what to do.
//
// One redemption. A second presentation of the same link is refused by AUB, which
// is what makes replay a bounded fact rather than a promise this program makes.
func (c *Client) ResolveJoinLink(ctx context.Context, ticketID string) (HostedGameJoin, error) {
	var out HostedGameJoin
	path := HostedGamePrefix + "/join-tickets/" + url.PathEscape(ticketID) + "/resolve"
	if err := c.do(ctx, http.MethodPost, path, nil, map[string]any{}, &out); err != nil {
		return HostedGameJoin{}, err
	}

	return out, checkHostedGameSchema(out.SchemaVersion)
}

// JoinLinkScheme is the deep link's scheme.
const JoinLinkScheme = "autopigeon"

// joinLinkPrefix is the whole of what a join link looks like before its id.
const joinLinkPrefix = JoinLinkScheme + "://join/"

// ParseJoinLink extracts the opaque ticket id from a deep link.
//
// It accepts the bare id too, because a person copying a link out of a browser's
// address bar sometimes copies only the last segment, and refusing that would be
// pedantry rather than safety: the id is opaque and worthless to anybody it was
// not minted for, so there is nothing here for a stricter parser to protect.
//
// What it does NOT accept is another scheme. A program that followed
// `https://…/join/x` because it looked similar would be resolving a link
// somebody else chose the host of.
func ParseJoinLink(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("aub: no join link given")
	}
	if rest, found := strings.CutPrefix(value, joinLinkPrefix); found {
		value = rest
	} else if strings.Contains(value, "://") || strings.Contains(value, "/") {
		return "", fmt.Errorf("aub: %q is not a join link. One looks like %sxxxxxxxx", raw, joinLinkPrefix)
	}
	value = strings.TrimSpace(strings.Trim(value, "/"))
	if value == "" || strings.ContainsAny(value, "?#&= \t") {
		return "", fmt.Errorf("aub: %q is not a join link. One looks like %sxxxxxxxx", raw, joinLinkPrefix)
	}

	return value, nil
}
