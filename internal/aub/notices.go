package aub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Operational notices and readiness: the two AUB routes the Companion reads
// WITHOUT necessarily presenting its session.

// NoticesPath is AUB's public operational-notice read route. A valid account
// token makes the caller `authenticated`; without one the caller is served the
// public notices only.
const NoticesPath = "/api/operational-notices"

// NoticeSurface is the surface this program asks for.
const NoticeSurface = "aucom"

// ServerTimeHeader is where AUB states its own clock on the notice route.
const ServerTimeHeader = "X-Auto-Pigeon-Server-Time"

// maxNoticeBody bounds a notice response. The contract caps a response at 20
// notices of at most 80+600 characters each; this is several times that.
const maxNoticeBody = 256 << 10

// NoticeResult is one notice fetch, relayed rather than interpreted: the page
// parses the body through the shared contract, so there is one parser.
type NoticeResult struct {
	// Status is 200 or 304.
	Status     int
	Body       []byte
	ETag       string
	ServerTime string
}

// OperationalNotices fetches `GET /api/operational-notices?surface=aucom`.
//
// authenticated decides whether the session token is presented: only then may
// AUB include authenticated-only notices. ifNoneMatch is sent as
// If-None-Match; a 304 comes back with an empty body. Any other status is an
// *APIError.
func (c *Client) OperationalNotices(ctx context.Context, authenticated bool, ifNoneMatch string) (NoticeResult, error) {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + NoticesPath
	endpoint.RawQuery = url.Values{"surface": {NoticeSurface}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return NoticeResult{}, fmt.Errorf("aub: building the notices request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if authenticated && c.token != "" {
		request.Header.Set("Authorization", c.token)
	}
	if tag := strings.TrimSpace(ifNoneMatch); tag != "" && len(tag) <= 128 {
		request.Header.Set("If-None-Match", tag)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return NoticeResult{}, fmt.Errorf("aub: GET %s: %w", NoticesPath, err)
	}
	defer response.Body.Close()
	result := NoticeResult{
		Status:     response.StatusCode,
		ETag:       response.Header.Get("ETag"),
		ServerTime: response.Header.Get(ServerTimeHeader),
	}
	switch response.StatusCode {
	case http.StatusNotModified:
		return result, nil
	case http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(response.Body, maxNoticeBody+1))
		if err != nil {
			return NoticeResult{}, fmt.Errorf("aub: reading the notices response: %w", err)
		}
		if len(body) > maxNoticeBody {
			return NoticeResult{}, errors.New("aub: the notices response is larger than any valid one")
		}
		result.Body = body
		return result, nil
	default:
		return NoticeResult{}, newAPIError(NoticesPath, response)
	}
}

// ReadinessPath is PocketBase's own health route, which AUB serves unchanged.
// Public, cheap, and answered before any collection is read, so it measures
// "is the backend there" and nothing about an account.
const ReadinessPath = "/api/health"

// ReadinessBound is how long the Companion waits for its AUB link to answer
// before it reports that it could not become ready.
const ReadinessBound = 10 * time.Second

// Ready asks AUB whether it is answering, within the context's deadline. No
// session is presented.
func (c *Client) Ready(ctx context.Context) error {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + ReadinessPath
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxErrorBody))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &APIError{StatusCode: response.StatusCode, Path: ReadinessPath}
	}
	return nil
}
