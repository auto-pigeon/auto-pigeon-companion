package aub

import (
	"context"
	"regexp"
)

const LeakResultPath = "/api/companion/v1/leak-results/"

var leakReturnID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// PublishLeakResult sends one bounded, revision-pinned result to AUB's private
// handoff. AUP polls the same request id from the account that initiated it.
func (c *Client) PublishLeakResult(ctx context.Context, requestID string, result any) error {
	if !leakReturnID.MatchString(requestID) {
		return &APIError{Path: LeakResultPath, Message: "invalid leak request id"}
	}
	return c.do(ctx, "POST", LeakResultPath+requestID, nil, result, nil)
}

// LeakStatusPath is AUB's relay for what the Companion is doing with a request.
const LeakStatusPath = "/api/companion/v1/leak-status/"

// LeakStatusSchema versions the status document.
const LeakStatusSchema = "aucom.leak-status/1.0"

// LeakStatus is one line of progress on a leak request, for the editor tab
// that asked. It names the same immutable revision the link named; the state is
// from AUB's closed list and the stage says what that state is doing.
type LeakStatus struct {
	SchemaVersion string `json:"schema_version"`
	MapID         string `json:"map_id"`
	Revision      int    `json:"revision"`
	ContentSHA256 string `json:"content_sha256"`
	State         string `json:"state"`
	Stage         string `json:"stage,omitempty"`
	BuildID       string `json:"build_id,omitempty"`
}

// PublishLeakStatus replaces this request's status on AUB. The editor cannot
// ask this program directly — a page may not call the reader's loopback — so
// the account's own server carries the line. It is progress, never evidence.
func (c *Client) PublishLeakStatus(ctx context.Context, requestID string, status LeakStatus) error {
	if !leakReturnID.MatchString(requestID) {
		return &APIError{Path: LeakStatusPath, Message: "invalid leak request id"}
	}
	status.SchemaVersion = LeakStatusSchema
	return c.do(ctx, "POST", LeakStatusPath+requestID, nil, status, nil)
}
