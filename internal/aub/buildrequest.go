package aub

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// BuildRequestPath is AUB's store of the editor's normal build requests. The
// Companion reads one with its own session and says what it is doing with it
// under `<id>/status`; the editor reads that. It is progress, never evidence.
const BuildRequestPath = "/api/companion/v1/build-requests/"

// BuildRequestStatusSchema versions the status document.
const BuildRequestStatusSchema = "aucom.build-request-status/1.0"

// The states AUB's build-request relay holds. The first is AUB's own; the
// Companion posts the others.
const (
	BuildRequestPending   = "pending"
	BuildRequestReceived  = "received"
	BuildRequestReviewing = "reviewing"
	BuildRequestBuilding  = "building"
	BuildRequestCompleted = "completed"
	BuildRequestFailed    = "failed"
	BuildRequestCancelled = "cancelled"
	BuildRequestBlocked   = "blocked"
)

// BuildRequestTerminal reports a state after which AUB stores nothing more.
func BuildRequestTerminal(state string) bool {
	return state == BuildRequestCompleted || state == BuildRequestFailed || state == BuildRequestCancelled
}

// The bounds AUB enforces on a status, which the Companion keeps too.
const (
	BuildRequestStageLimit = 120
	BuildRequestIDLimit    = 128
)

// BuildRequest is one request as AUB answers it. Every field is untrusted
// input: [Client.BuildRequest] refuses an answer that is not this shape.
type BuildRequest struct {
	RequestID     string    `json:"request_id"`
	Intent        string    `json:"intent"`
	MapID         string    `json:"map_id"`
	Revision      int       `json:"revision"`
	RevisionID    string    `json:"revision_id"`
	ContentSHA256 string    `json:"content_sha256"`
	Game          string    `json:"game"`
	MapName       string    `json:"map_name"`
	State         string    `json:"state"`
	Stage         string    `json:"stage,omitempty"`
	BuildID       string    `json:"build_id,omitempty"`
	RunID         string    `json:"run_id,omitempty"`
	Sequence      int64     `json:"sequence"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// BuildRequestStatus is one line of progress on a build request.
type BuildRequestStatus struct {
	SchemaVersion string `json:"schema_version"`
	State         string `json:"state"`
	Stage         string `json:"stage,omitempty"`
	BuildID       string `json:"build_id,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	// Sequence orders one request's statuses: AUB ignores a lower one than it
	// holds, so a POST that was held up cannot put the editor back a step.
	Sequence int64 `json:"sequence"`
}

// BuildRequestStatusAnswer is AUB's answer to a status. Superseded: AUB did
// not store it, because the request already ended or holds a later sequence.
type BuildRequestStatusAnswer struct {
	RequestID  string `json:"request_id"`
	State      string `json:"state"`
	Superseded bool   `json:"superseded"`
}

var (
	buildRevisionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	buildOpaqueID   = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
)

// BuildRequest reads one build request with this client's session. A 404 is
// AUB's one answer for unknown, expired, another account's, and a map this
// account can no longer read — on purpose, indistinguishable.
func (c *Client) BuildRequest(ctx context.Context, requestID string) (BuildRequest, error) {
	if !buildRequestID.MatchString(requestID) {
		return BuildRequest{}, &APIError{Path: BuildRequestPath, Message: "invalid build request id"}
	}
	var out BuildRequest
	if err := c.do(ctx, http.MethodGet, BuildRequestPath+requestID, nil, nil, &out); err != nil {
		return BuildRequest{}, err
	}
	if err := out.validate(requestID); err != nil {
		return BuildRequest{}, err
	}
	return out, nil
}

// validate refuses an answer that is not about the request asked for, or that
// carries anything but the documented shape. Nothing here is a path, an argv
// or an address, and nothing that looks like one is let through.
func (r *BuildRequest) validate(requestID string) error {
	refuse := func(why string) error {
		return fmt.Errorf("aub: the account server's answer about build request %s is not usable (%s)", requestID, why)
	}
	switch {
	case r.RequestID != requestID:
		return refuse("it is about another request")
	case !ValidBuildIntent(r.Intent):
		return refuse("unknown intent")
	case !leakAssetID.MatchString(r.MapID):
		return refuse("bad map id")
	case r.Revision < 1 || r.Revision > MaxLeakTestRevision:
		return refuse("bad revision number")
	case !buildRevisionID.MatchString(r.RevisionID):
		return refuse("bad revision id")
	case !leakSHA256.MatchString(r.ContentSHA256):
		return refuse("bad content digest")
	case r.Game != "" && !leakProfile.MatchString(r.Game):
		return refuse("bad game")
	case !buildRequestState(r.State):
		return refuse("unknown state")
	case r.Sequence < 0:
		return refuse("bad sequence")
	case utf8.RuneCountInString(r.Stage) > BuildRequestStageLimit || !utf8.ValidString(r.Stage):
		return refuse("bad stage")
	case r.BuildID != "" && !buildOpaqueID.MatchString(r.BuildID),
		r.RunID != "" && !buildOpaqueID.MatchString(r.RunID):
		return refuse("bad build or run id")
	}
	if !PlainBuildMapName(r.MapName) {
		// A name is only shown. One that is not plain text is dropped, not
		// refused: the revision's own file name is shown instead.
		r.MapName = ""
	}
	return nil
}

// PlainBuildMapName reports a map name fit to show as it is: short, valid
// text, with no control characters.
func PlainBuildMapName(name string) bool {
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > BuildRequestStageLimit ||
		strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func buildRequestState(state string) bool {
	switch state {
	case BuildRequestPending, BuildRequestReceived, BuildRequestReviewing, BuildRequestBuilding,
		BuildRequestCompleted, BuildRequestFailed, BuildRequestCancelled, BuildRequestBlocked:
		return true
	}
	return false
}

// PublishBuildRequestStatusAs posts one status under the session the caller
// names — the status worker's one reading of it, for the reason
// [Client.PublishLeakStatusAs] gives. An empty token is refused here: a status
// is never anonymous.
func (c *Client) PublishBuildRequestStatusAs(ctx context.Context, token, requestID string, status BuildRequestStatus) (BuildRequestStatusAnswer, error) {
	path := BuildRequestPath + requestID + "/status"
	if !buildRequestID.MatchString(requestID) {
		return BuildRequestStatusAnswer{}, &APIError{Path: BuildRequestPath, Message: "invalid build request id"}
	}
	if token == "" {
		return BuildRequestStatusAnswer{}, &APIError{StatusCode: http.StatusUnauthorized, Path: path, Message: "no session to send this status under"}
	}
	if status.State == BuildRequestPending || !buildRequestState(status.State) || status.Sequence < 1 ||
		utf8.RuneCountInString(status.Stage) > BuildRequestStageLimit ||
		len(status.BuildID) > BuildRequestIDLimit || len(status.RunID) > BuildRequestIDLimit {
		return BuildRequestStatusAnswer{}, &APIError{Path: path, Message: "invalid build request status"}
	}
	status.SchemaVersion = BuildRequestStatusSchema
	var out BuildRequestStatusAnswer
	if err := c.doAs(ctx, token, http.MethodPost, path, nil, status, &out); err != nil {
		return BuildRequestStatusAnswer{}, err
	}
	return out, nil
}
