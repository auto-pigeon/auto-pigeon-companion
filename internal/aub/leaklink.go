package aub

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// LeakTestLinkPrefix is the whole of what a leak-test link looks like before its
// map id: `autopigeon://leaktest/map/<asset id>?revision=<n>&sha256=<hex>`.
//
// The Auto-Pigeon editor writes one for the saved revision of the map it has
// open (`AUP 264`). It names a map this account can already read through the
// Companion catalog, the revision NUMBER the editor saved, and that revision's
// content digest — so the Companion can refuse a revision whose bytes are not
// the ones the editor meant. It carries no session, no address and no return
// path: nothing in it is worth anything to a person it was not written for.
const LeakTestLinkPrefix = JoinLinkScheme + "://leaktest/map/"

// LeakTestLink is a parsed leak-test link.
type LeakTestLink struct {
	AssetID       string `json:"asset_id"`
	Revision      int    `json:"revision"`
	ContentSHA256 string `json:"content_sha256"`
	RequestID     string `json:"request_id,omitempty"`
	// Profile is the game the editor says the map is for (`Q3_018`). A HINT:
	// the Companion reads the game out of the pinned revision's own bytes and
	// refuses a link whose hint disagrees. Absent from a link written before
	// the field existed, and from every Quake 1 link, which stays byte for
	// byte what it was.
	Profile string `json:"profile,omitempty"`
}

// MaxLeakTestRevision bounds the revision number a link may name. Far above any
// real map; its only job is to keep a hostile number out of arithmetic.
const MaxLeakTestRevision = 1_000_000_000

var (
	leakAssetID   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	leakSHA256    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	leakRequestID = regexp.MustCompile(`^[0-9a-f]{32}$`)
	leakProfile   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

// IsLeakTestLink reports whether raw is shaped like a leak-test link at all, so
// the scheme handler can tell it from a join link before parsing either.
func IsLeakTestLink(raw string) bool {
	return strings.HasPrefix(strings.TrimSpace(raw), LeakTestLinkPrefix)
}

// Link is the canonical spelling of the link, the one the editor writes.
func (l LeakTestLink) Link() string {
	value := LeakTestLinkPrefix + l.AssetID + "?revision=" + strconv.Itoa(l.Revision) + "&sha256=" + l.ContentSHA256
	if l.RequestID != "" {
		value += "&request=" + l.RequestID
	}
	if l.Profile != "" {
		value += "&profile=" + l.Profile
	}
	return value
}

// ParseLeakTestLink validates a leak-test link and nothing else.
//
// Strict where ParseJoinLink is lenient, because this link is not an opaque
// ticket: every part of it is later used to ask AUB for something, so every part
// is checked here, before the handler records it. Only the named fields, no
// fragment, no user info, no other query key, no repeated key.
func ParseLeakTestLink(raw string) (LeakTestLink, error) {
	value := strings.TrimSpace(raw)
	refuse := func(why string) (LeakTestLink, error) {
		return LeakTestLink{}, fmt.Errorf("aub: %q is not a leak-test link (%s). One looks like %s<map id>?revision=<n>&sha256=<digest>",
			raw, why, LeakTestLinkPrefix)
	}
	rest, found := strings.CutPrefix(value, LeakTestLinkPrefix)
	if !found {
		return refuse("wrong prefix")
	}
	if strings.ContainsAny(rest, "#@\\ \t") {
		return refuse("unexpected character")
	}
	id, query, hasQuery := strings.Cut(rest, "?")
	if !hasQuery {
		return refuse("no revision")
	}
	if !leakAssetID.MatchString(id) {
		return refuse("bad map id")
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return refuse("unreadable query")
	}
	known := 0
	for key, each := range values {
		switch key {
		case "revision", "sha256", "request", "profile":
			if len(each) != 1 {
				return refuse("a repeated field")
			}
			known++
		}
	}
	if known != len(values) || len(values["revision"]) != 1 || len(values["sha256"]) != 1 {
		return refuse("it must carry revision, sha256 and optionally one request id and one profile")
	}
	revision, err := strconv.Atoi(values.Get("revision"))
	if err != nil || revision < 1 || revision > MaxLeakTestRevision {
		return refuse("bad revision number")
	}
	digest := values.Get("sha256")
	if !leakSHA256.MatchString(digest) {
		return refuse("bad content digest")
	}

	requestID := values.Get("request")
	if requestID != "" && !leakRequestID.MatchString(requestID) {
		return refuse("bad request id")
	}
	if _, named := values["request"]; named && requestID == "" {
		return refuse("empty request id")
	}
	profile := values.Get("profile")
	if _, named := values["profile"]; named && !leakProfile.MatchString(profile) {
		return refuse("bad profile")
	}
	return LeakTestLink{AssetID: id, Revision: revision, ContentSHA256: digest, RequestID: requestID, Profile: profile}, nil
}
