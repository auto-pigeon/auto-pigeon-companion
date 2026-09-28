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
