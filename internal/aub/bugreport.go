package aub

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
)

// The bug-report route (`241`): AUB files a user's bug report as an issue in
// the public bug-reports repository, after the user confirmed it is published.
// The document itself is built in the page by the shared incident contract,
// byte for byte AUG's; the Companion only carries it, and carries AUB's answer
// back unchanged, so the page reads every outcome the way AUG's dialog does.
const (
	BugReportPath       = "/api/bug-reports"
	BugReportStatusPath = "/api/bug-reports/status"
	maxBugReportBody    = 64 << 10
)

// BugReportRelay sends one request to the bug-report route and returns AUB's
// status and body as they came. The status route is public; the submission
// carries the session, since AUB files a report only for a signed-in account.
func (c *Client) BugReportRelay(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if path == BugReportPath && c.Token() != "" {
		request.Header.Set("Authorization", c.Token())
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(response.Body, maxBugReportBody))
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, answer, nil
}
