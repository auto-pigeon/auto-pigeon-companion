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

// APIError is a non-2xx response from AUB, carrying enough to tell the user
// what went wrong without dumping a raw body into the UI.
type APIError struct {
	StatusCode int
	// Message is PocketBase's "message" field when the body parsed as its
	// standard error envelope, otherwise a truncated raw body.
	Message string
	Path    string
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

	// PocketBase's error envelope: {"code":400,"message":"...","data":{...}}.
	var envelope struct {
		Message string `json:"message"`
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
	return &APIError{StatusCode: response.StatusCode, Message: message, Path: path}
}

// ListRecords fetches one page of a PocketBase collection into out, which must
// point at a struct with an "items" field of the record type.
//
// TODO(andrea): the collection names and schemas the Companion reads are not
// confirmed —
// see internal/launch/config.go. This is the transport those calls will use;
// it is not itself schema-specific.
func (c *Client) ListRecords(ctx context.Context, collection string, query url.Values, out any) error {
	if collection == "" {
		return fmt.Errorf("aub: collection name is empty")
	}
	return c.do(ctx, http.MethodGet, "/api/collections/"+url.PathEscape(collection)+"/records", query, nil, out)
}
