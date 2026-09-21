// Package aub is the Companion's HTTP client for auto-pigeon-backend (AUB), a
// PocketBase instance.
//
// # One client, not two
//
// The Launcher and the Companion each carried a client of this shape, and the
// duplication was defended on the grounds that they were independently released
// apps. They are now one app, so there is one client: this file, the better
// covered of the two.
//
// # Stdlib only
//
// net/http and encoding/json. There is no PocketBase Go SDK dependency, for the
// same reason there is no CLI framework: the surface the Companion needs is two
// auth endpoints and one collection listing.
package aub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout bounds a single AUB request. The GUI runs these on a user
// gesture, so a request that hangs has to fail visibly rather than leave a
// spinner running forever.
const DefaultTimeout = 30 * time.Second

// Client talks to one AUB instance.
//
// The zero value is not usable; construct with New.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client

	// token is the current auth token, sent as the Authorization header. It is
	// held in memory here; persisting it across runs is internal/config's job,
	// which is what keeps this package free of any file access.
	token string

	// collection and tokenLifetime are the DEPLOYMENT's own answers, learned from
	// its capability document. Empty and zero until something has read one, which
	// is why both have a documented meaning in that state rather than a default
	// that pretends to be knowledge.
	collection    string
	tokenLifetime time.Duration
}

// AdoptCapabilities records what a deployment said about its own sessions.
//
// Called once a capability document has been read, and it is the whole of how
// this client stops guessing: the auth collection and the token lifetime come
// from the server that will validate the token, rather than from PocketBase's
// documentation.
func (c *Client) AdoptCapabilities(capabilities Capabilities) {
	if capabilities.Session.AuthCollection != "" {
		c.collection = capabilities.Session.AuthCollection
	}
	c.tokenLifetime = capabilities.Session.Lifetime()
}

// authCollection is the collection to authenticate against: the deployment's own
// when one has been read, PocketBase's default until then.
func (c *Client) authCollection() string {
	if c.collection != "" {
		return c.collection
	}

	return DefaultAuthCollection
}

// New builds a client for baseURL. A nil httpClient means a fresh one with
// DefaultTimeout.
func New(baseURL string, httpClient *http.Client) (*Client, error) {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return nil, fmt.Errorf("aub: base URL is empty")
	}
	parsed, err := url.Parse(strings.TrimRight(trimmed, "/"))
	if err != nil {
		return nil, fmt.Errorf("aub: parsing base URL %q: %w", baseURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("aub: base URL %q must be http or https", baseURL)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultTimeout}
	}
	return &Client{baseURL: parsed, httpClient: httpClient}, nil
}

// BaseURL is the instance this client talks to.
func (c *Client) BaseURL() string { return c.baseURL.String() }

// Token returns the current auth token, empty when unauthenticated.
func (c *Client) Token() string { return c.token }

// SetToken installs a token obtained elsewhere — typically one loaded from
// local config at startup, so a returning user is not asked to log in again.
func (c *Client) SetToken(token string) { c.token = token }

// Authenticated reports whether a token is set. It says nothing about whether
// AUB still accepts it; only a request can establish that.
func (c *Client) Authenticated() bool { return c.token != "" }

// SessionExpired reports whether the stored session token says it has expired.
//
// Authenticated only knows a token is present. `AUCOM/AUE/AUB 246I1.1` found
// the page saying "signed in" on a token that had expired the day before, and
// every AUB call then refused it. This reads the token's own `exp` claim —
// no signature is checked, because AUB checks that; it only answers "is it
// worth sending". A token that is not a JWT, or carries no `exp`, is not
// reported expired: the server remains the authority on those.
func (c *Client) SessionExpired(now time.Time) bool {
	return TokenExpired(c.token, now)
}

// TokenExpired is SessionExpired for a bare token.
func TokenExpired(token string, now time.Time) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return false
	}

	return !now.Before(time.Unix(claims.Exp, 0))
}

// APIError is a non-2xx response from AUB, carrying enough to tell the user
// what went wrong without dumping a raw body into the UI.
type APIError struct {
	StatusCode int
	// Message is PocketBase's "message" field when the body parsed as its
	// standard error envelope, otherwise a truncated raw body.
	Message string
	Path    string

	// Reason is AUB's own machine-readable refusal code, from
	// `data.reason.code`. Empty when the answer carried none — PocketBase's own
	// validation errors do not, and neither does a proxy in front of it — so a
	// caller branches on it only where it is present and falls back to the
	// status. A code is a fact a program can act on; a sentence is for a person.
	Reason string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("aub: %s: HTTP %d", e.Path, e.StatusCode)
	}
	return fmt.Sprintf("aub: %s: HTTP %d: %s", e.Path, e.StatusCode, e.Message)
}

// Unauthorized reports whether the error is AUB rejecting the credentials or
// token, which is the one case callers act on differently — by clearing the
// stored session and asking for a login.
func (e *APIError) Unauthorized() bool {
	return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
}

// maxErrorBody caps how much of a failed response is read. A misconfigured base
// URL pointing at some unrelated server can return megabytes of HTML, and none
// of it belongs in an error string.
const maxErrorBody = 8 << 10

// do issues one request against path (rooted at the base URL), encoding body as
// JSON when non-nil and decoding a 2xx response into out when non-nil.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	if len(query) > 0 {
		endpoint.RawQuery = query.Encode()
	}

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("aub: encoding the %s %s request: %w", method, path, err)
		}
		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return fmt.Errorf("aub: building the %s %s request: %w", method, path, err)
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		request.Header.Set("Authorization", c.token)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("aub: %s %s: %w", method, path, err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return newAPIError(path, response)
	}
	if out == nil {
		// Drain so the connection can be reused rather than closed mid-body.
		io.Copy(io.Discard, io.LimitReader(response.Body, maxErrorBody))
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		return fmt.Errorf("aub: decoding the %s %s response: %w", method, path, err)
	}
	return nil
}

func newAPIError(path string, response *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))

	// PocketBase's error envelope: {"code":400,"message":"...","data":{...}},
	// with this service's own reason code nested under `data.reason`.
	var envelope struct {
		Message string `json:"message"`
		Data    struct {
			Reason struct {
				Code string `json:"code"`
			} `json:"reason"`
		} `json:"data"`
	}
	message := ""
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Message != "" {
		message = envelope.Message
	} else {
		message = strings.TrimSpace(string(raw))
		if len(message) > 200 {
			message = message[:200] + "…"
		}
	}
	return &APIError{
		StatusCode: response.StatusCode, Message: message, Path: path,
		Reason: envelope.Data.Reason.Code,
	}
}

// ListRecords fetches one page of a PocketBase collection into out, which must
// point at a struct with an "items" field of the record type.
//
// **Not the door for asset data.** PocketBase's record API answers with whatever
// columns a collection happens to have, so anything read through it is read
// against AUB's schema rather than against a contract — which is exactly the
// mistake `/api/companion/v1` was built to end. The catalog, an asset, its
// revisions and its bytes all go through the Companion API above.
//
// This stays for the collections that genuinely are plain PocketBase records and
// have no Companion route, and for a deployment older than that surface.
func (c *Client) ListRecords(ctx context.Context, collection string, query url.Values, out any) error {
	if collection == "" {
		return fmt.Errorf("aub: collection name is empty")
	}
	return c.do(ctx, http.MethodGet, "/api/collections/"+url.PathEscape(collection)+"/records", query, nil, out)
}

// WithTimeout is a copy of this client whose requests are bounded by d instead
// of DefaultTimeout — for the few transfers that are megabytes rather than a
// JSON answer. The session and the deployment's answers are shared.
func (c *Client) WithTimeout(d time.Duration) *Client {
	copied := *c
	transport := c.httpClient.Transport
	copied.httpClient = &http.Client{Timeout: d, Transport: transport}
	return &copied
}
