package aub

import (
	"context"
	"net/http"
)

// A handoff acknowledgement says which Companion received an editor's build
// or leak request, and what it is (NEW_368; AUB `handoff-acks`). The editor
// reads it to tell a Companion that received a request from one that cannot
// do it and from none at all. It authorizes nothing.

// HandoffAckPath is AUB's route, followed by the request id.
const HandoffAckPath = CompanionPrefix + "/handoff-acks/"

// HandoffAckSchema versions the document.
const HandoffAckSchema = "aucom.handoff-ack/1.0"

// Kinds of handoff.
const (
	HandoffBuild    = "build"
	HandoffLeakTest = "leak_test"
)

// HandoffCompanion is what the serving Companion says about itself.
type HandoffCompanion struct {
	Version           string   `json:"version"`
	Protocol          int      `json:"protocol"`
	Capabilities      []string `json:"capabilities"`
	InstanceID        string   `json:"instance_id"`
	LaunchedByVersion string   `json:"launched_by_version,omitempty"`
}

// HandoffAck is the document.
type HandoffAck struct {
	SchemaVersion string           `json:"schema_version"`
	Kind          string           `json:"kind"`
	MapID         string           `json:"map_id"`
	Revision      int              `json:"revision"`
	ContentSHA256 string           `json:"content_sha256"`
	Companion     HandoffCompanion `json:"companion"`
}

// PublishHandoffAckAs posts one acknowledgement under exactly token.
func (c *Client) PublishHandoffAckAs(ctx context.Context, token, requestID string, ack HandoffAck) error {
	path := HandoffAckPath + requestID
	if !buildRequestID.MatchString(requestID) {
		return &APIError{Path: HandoffAckPath, Message: "invalid request id"}
	}
	if token == "" {
		return &APIError{StatusCode: http.StatusUnauthorized, Path: path, Message: "no session to send this acknowledgement under"}
	}
	ack.SchemaVersion = HandoffAckSchema
	var out map[string]any
	return c.doAs(ctx, token, http.MethodPost, path, nil, ack, &out)
}
