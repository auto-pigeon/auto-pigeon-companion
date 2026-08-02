// Package aub is a small REST client for AUB (auto-pigeon-backend), a
// Pocketbase instance.
//
// There is no official Pocketbase Go SDK, and the handful of endpoints AUC
// needs do not justify a third-party one, so this is stdlib net/http with a
// thin request helper. Only what AUC actually calls is modelled; this is not a
// general Pocketbase client.
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

// DefaultTimeout bounds a single AUB request. GUI mode makes these calls from
// an HTTP handler, so an unbounded request would hang the page with no
// feedback.
const DefaultTimeout = 30 * time.Second

// Client talks to one AUB instance.
//
// A Client is safe for concurrent use once constructed; Token is the one
// mutable field, so it is guarded by the caller (the server sets it at login
// and on logout, both under its own lock).
type Client struct {
	BaseURL string
	HTTP    *http.Client
	// Token is the Pocketbase auth token sent as the Authorization header.
	// Empty means unauthenticated.
	Token string
}

// New returns a Client for baseURL with AUC's default timeout.
func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: DefaultTimeout},
	}
}

// APIError is a non-2xx response from AUB. Pocketbase returns a JSON body with
// `code`, `message`, and per-field `data`; the message is what a user should
// see, and the status is what a caller should branch on.
type APIError struct {
	Status  int
	Message string
	Body    string
}

func (err *APIError) Error() string {
	if err.Message != "" {
		return fmt.Sprintf("AUB request failed (%d): %s", err.Status, err.Message)
	}
	return fmt.Sprintf("AUB request failed (%d)", err.Status)
}

// do performs one request against path (e.g. "/api/health"), encoding body as
// JSON when non-nil and decoding a JSON response into result when non-nil.
func (client *Client) do(ctx context.Context, method, path string, body, result any) error {
	if client.BaseURL == "" {
		return fmt.Errorf("AUB base URL is not configured")
	}
	endpoint, err := url.JoinPath(client.BaseURL, path)
	if err != nil {
		return fmt.Errorf("cannot build AUB URL for %s: %w", path, err)
	}

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("cannot encode request body: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, endpoint, payload)
	if err != nil {
		return fmt.Errorf("cannot build AUB request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	if client.Token != "" {
		request.Header.Set("Authorization", client.Token)
	}

	httpClient := client.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultTimeout}
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("cannot reach AUB at %s: %w", client.BaseURL, err)
	}
	defer response.Body.Close()

	// 1 MiB is far above anything these endpoints return; the cap is only
	// there so a misconfigured base URL pointing at something huge cannot
	// exhaust memory.
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("cannot read AUB response: %w", err)
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &APIError{Status: response.StatusCode, Message: errorMessage(raw), Body: string(raw)}
	}
	if result == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, result); err != nil {
		return fmt.Errorf("cannot parse AUB response: %w", err)
	}
	return nil
}

// errorMessage pulls Pocketbase's human-readable message out of an error body,
// falling back to nothing when the body is not the expected shape.
func errorMessage(raw []byte) string {
	var envelope struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ""
	}
	return envelope.Message
}

// Health checks that the configured base URL is reachable and is a Pocketbase
// instance. `/api/health` is stable across Pocketbase versions and needs no
// auth, which makes it the right probe for "is this URL right".
func (client *Client) Health(ctx context.Context) error {
	return client.do(ctx, http.MethodGet, "/api/health", nil, nil)
}
