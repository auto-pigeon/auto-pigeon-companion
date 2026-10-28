package aub

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// BuildRequestLinkPrefix is the whole of what a build-request link looks like
// before its id: `autopigeon://build/request/<id>?intent=build|build_run`.
//
// The editor's Build button asks the account server for an expiring, opaque
// normal build request and opens this link. Unlike a leak-test link it names
// no map, no revision and no digest: the id is the request, AUB holds what it
// is about, and the Companion asks AUB with its own session. So the link is
// worth nothing to a person it was not written for — another account's id is
// a 404 — and it carries no path, no program and no address to go to.
//
// It is NOT a leak test. It has its own prefix, its own parser, its own intent
// file and its own AUB routes; a leak-test link is never accepted here, and
// this link is never accepted by [ParseLeakTestLink].
const BuildRequestLinkPrefix = JoinLinkScheme + "://build/request/"

// The two things the editor may ask for. A build-only request never starts an
// engine.
const (
	BuildIntentBuild    = "build"
	BuildIntentBuildRun = "build_run"
)

// BuildRequestLink is a parsed build-request link.
type BuildRequestLink struct {
	RequestID string `json:"request_id"`
	Intent    string `json:"intent"`
}

var buildRequestID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// IsBuildRequestLink reports whether raw is shaped like a build-request link at
// all, so the scheme handler can tell it from a join or leak-test link before
// parsing any of them.
func IsBuildRequestLink(raw string) bool {
	return strings.HasPrefix(strings.TrimSpace(raw), BuildRequestLinkPrefix)
}

// ValidBuildIntent reports whether intent is one the editor may ask for.
func ValidBuildIntent(intent string) bool {
	return intent == BuildIntentBuild || intent == BuildIntentBuildRun
}

// RunAfter reports whether the editor asked for the map to be played once it
// is built.
func (l BuildRequestLink) RunAfter() bool { return l.Intent == BuildIntentBuildRun }

// Link is the canonical spelling of the link, the one the editor writes.
func (l BuildRequestLink) Link() string {
	return BuildRequestLinkPrefix + l.RequestID + "?intent=" + l.Intent
}

// ParseBuildRequestLink validates a build-request link and nothing else.
//
// As strict as [ParseLeakTestLink], and stricter in one way: the query must be
// spelled exactly as the editor spells it, so a percent-escaped or `;`-split
// variant of a valid link is refused rather than decoded into one. The id is
// sent to AUB and the intent decides whether an engine may start, so nothing
// but the one shape is recorded.
func ParseBuildRequestLink(raw string) (BuildRequestLink, error) {
	value := strings.TrimSpace(raw)
	refuse := func(why string) (BuildRequestLink, error) {
		return BuildRequestLink{}, fmt.Errorf("aub: %q is not a build-request link (%s). One looks like %s<id>?intent=build or ?intent=build_run",
			raw, why, BuildRequestLinkPrefix)
	}
	rest, found := strings.CutPrefix(value, BuildRequestLinkPrefix)
	if !found {
		return refuse("wrong prefix")
	}
	if strings.ContainsAny(rest, "#@\\ \t") {
		return refuse("unexpected character")
	}
	id, query, hasQuery := strings.Cut(rest, "?")
	if !buildRequestID.MatchString(id) {
		return refuse("bad request id")
	}
	if !hasQuery {
		return refuse("no intent")
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return refuse("unreadable query")
	}
	if len(values) != 1 || len(values["intent"]) == 0 {
		return refuse("it must carry one intent and nothing else")
	}
	if len(values["intent"]) != 1 {
		return refuse("a repeated field")
	}
	intent := values.Get("intent")
	if !ValidBuildIntent(intent) {
		return refuse("unknown intent")
	}
	if query != "intent="+intent {
		return refuse("not spelled as the editor writes it")
	}
	return BuildRequestLink{RequestID: id, Intent: intent}, nil
}
