// Package publish is the two acts that connect a profile document to other
// people: offering one, and taking one.
//
// # Why they are one package
//
// They are the same boundary crossed in two directions, and both are gated on
// the same fact — a document's exact bytes and the digest over them. Publishing
// is *validate, canonicalize, show the person what they are about to disclose,
// and only then send*. Installing is *fetch, re-canonicalize, prove the digest,
// validate, diff against what is already here, show what is being asked for, and
// only then write*. Splitting them would put the same digest arithmetic in two
// places.
//
// # Nothing here runs anything
//
// This package acquires no tool, starts no process and reads no executable. It
// writes exactly two things: a profile document into the profiles directory, and
// a local binding. `internal/job` is still the only thing that runs, which is
// what makes "importing is inert until a person has reviewed it" a property of
// the code rather than of a page's flow.
//
// # The trust rule, which is the important one
//
// A profile installed from a deployment's catalog is **community**, always.
//
// AUB has a trust state of its own — `community`, `verified`, `builtin` — awarded
// by that deployment's operator. It is shown to the user, because it is real
// information about what somebody with a stake in that deployment thinks. It is
// NOT this machine's trust state, and it never becomes one:
//
//   - `profile.TrustBuiltin` means *compiled into this build of the Companion*,
//     and `profile.Authorize` returns nil for it without a grant. Mapping a
//     deployment's `builtin` onto it would let anybody who runs an AUB hand out
//     the one state that skips this machine's review.
//   - `profile.TrustVerified` means *signed by an Auto-Pigeon catalogue key whose
//     signature this build verified*. No signature was verified here. Claiming it
//     would be this program vouching for something it did not check.
//
// So the badge travels as information and the authorization stays local: a grant
// against one exact digest, made by the person whose machine it is.
package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Disclosure is one thing a publication would make public.
//
// Path is where it is in the document; Value is what it says. A person about to
// publish is entitled to the list, not to a summary of it: the accident this
// exists to prevent is somebody's own name, a colleague's URL or a sentence
// written for one reader going out to everybody, and none of those is something
// a machine can rank.
type Disclosure struct {
	Path  string `json:"path"`
	Value string `json:"value"`
	// Kind is `identity`, `publisher`, `text`, `network` or `environment` — what
	// sort of thing this is, so a preview can group rather than list 200 lines.
	Kind string `json:"kind"`
}

// Disclosure kinds.
const (
	DisclosureIdentity    = "identity"
	DisclosurePublisher   = "publisher"
	DisclosureText        = "text"
	DisclosureNetwork     = "network"
	DisclosureEnvironment = "environment"
)

// Preview is what a publication would do, computed without doing any of it.
type Preview struct {
	// Canonical is the exact bytes that would be published, and Digest names
	// them. Both come from `profile.Export`, which is the one encoder — a profile
	// written any other way would be a profile whose digest depended on who wrote
	// it out.
	Canonical []byte `json:"-"`
	Digest    string `json:"digest"`
	Bytes     int    `json:"bytes"`

	ProfileID string `json:"profile_id"`
	Kind      string `json:"kind"`
	Version   string `json:"version"`
	Name      string `json:"name"`

	// License is the DESCRIBED PROGRAM's licence. Publishing a profile does not
	// relicense that program and grants nothing its own licence does not; the
	// preview says so in `LicenceNote` so the sentence is in front of the person
	// at the moment they decide.
	License     string `json:"license_spdx"`
	LicenceNote string `json:"licence_note"`

	// Disclosures is every free-text value, URL, host and environment variable
	// name in the document.
	Disclosures []Disclosure `json:"disclosures"`

	// Permissions is what the document asks a machine that installs it for. It is
	// in the preview because a publisher should see what they are asking other
	// people to agree to, in the same words those people will be shown.
	Permissions []profile.Permission `json:"permissions"`
}

// LicenceNote is the sentence a publisher and an installer are both shown.
//
// Written here as well as served by AUB so that `companion profile publish
// --dry-run` says it without a network round trip. It is the same claim, not a
// second one: a program under one licence must never be the thing that says what
// another one is licensed as, and neither this preview nor AUB's copy states any
// licence — both point at the document's own declaration.
const LicenceNote = "A profile is configuration for an independent program. Publishing, installing " +
	"or running one changes nothing about that program's licence, does not relicense it, and grants " +
	"nothing beyond what its own licence grants. `license_spdx` is the described program's."

// ErrNotConfirmed reports a publication that has not been confirmed.
var ErrNotConfirmed = errors.New("publish: nothing was sent, because the preview was not confirmed")

// PreviewOf builds the publication preview for one document.
//
// It runs the whole export gate: `profile.Export` validates and canonicalizes,
// and canonicalization runs `CheckPortable`, so a document naming an absolute
// path, a home directory, a network address or anything shaped like a credential
// cannot reach a preview at all — let alone a publication. That is why there is
// no separate "redact" step here: the redaction is a refusal, and a refusal that
// could be skipped would not be one.
func PreviewOf(p profile.Profile) (Preview, error) {
	canonical, err := profile.Export(p)
	if err != nil {
		return Preview{}, err
	}
	meta := p.Metadata()
	sum := sha256.Sum256(canonical)

	preview := Preview{
		Canonical:   canonical,
		Digest:      "sha256:" + hex.EncodeToString(sum[:]),
		Bytes:       len(canonical),
		ProfileID:   meta.ID,
		Kind:        string(meta.Kind),
		Version:     meta.Version,
		Name:        meta.Name,
		License:     meta.License.SPDX,
		LicenceNote: LicenceNote,
		Permissions: p.Permissions(),
	}
	preview.Disclosures, err = disclosuresOf(canonical)
	if err != nil {
		return Preview{}, err
	}

	return preview, nil
}

// Publish sends a previewed document.
//
// `confirmed` is not a formality and is not defaulted: the prompt this was built
// for asks for an explicit preview, and a function that could publish without one
// would make the preview a thing a caller may skip. Callers pass the answer a
// person gave.
func Publish(ctx context.Context, client *aub.Client, preview Preview, visibility string,
	confirmed bool,
) (aub.PublishProfileResult, error) {
	if !confirmed {
		return aub.PublishProfileResult{}, ErrNotConfirmed
	}
	if len(preview.Canonical) == 0 {
		return aub.PublishProfileResult{}, errors.New("publish: this preview holds no document")
	}

	return client.PublishProfile(ctx, preview.Canonical, preview.Digest, visibility)
}

// disclosuresOf walks the canonical form and reports every string a reader would
// see.
//
// Over the canonical tree rather than over the Go value, for the reason
// `profile.CheckPortable` gives about its own walk: a member added to a struct
// next year is covered without anybody remembering to add it here.
func disclosuresOf(canonical []byte) ([]Disclosure, error) {
	tree, err := decodeTree(canonical)
	if err != nil {
		return nil, err
	}
	var out []Disclosure
	walk("", tree, &out)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return kindOrder(out[i].Kind) < kindOrder(out[j].Kind)
		}

		return out[i].Path < out[j].Path
	})

	return out, nil
}

func kindOrder(kind string) int {
	for index, known := range []string{
		DisclosureIdentity, DisclosurePublisher, DisclosureNetwork,
		DisclosureEnvironment, DisclosureText,
	} {
		if known == kind {
			return index
		}
	}

	return 99
}

func walk(path string, value any, out *[]Disclosure) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			walk(join(path, key), typed[key], out)
		}
	case []any:
		for index, item := range typed {
			walk(fmt.Sprintf("%s[%d]", path, index), item, out)
		}
	case string:
		if kind := classify(path, typed); kind != "" {
			*out = append(*out, Disclosure{Path: path, Value: typed, Kind: kind})
		}
	}
}

func join(path, key string) string {
	if path == "" {
		return key
	}

	return path + "." + key
}

// textMembers is the set of member names whose value is prose somebody wrote.
//
// A list rather than "every string", because a document is mostly identifiers,
// argument literals and enum values, and a preview that showed four hundred of
// those would be a preview nobody reads — which is the same as no preview.
var textMembers = map[string]bool{
	"summary": true, "description": true, "note": true, "notes": true,
	"purpose": true, "hint": true, "title": true, "message": true, "name": true,
	"last_qualified": true, "corresponding_source": true, "spdx": true,
}

func classify(path, value string) string {
	if value == "" {
		return ""
	}
	member := path
	if index := strings.LastIndexAny(path, "."); index >= 0 {
		member = path[index+1:]
	}
	if index := strings.IndexByte(member, '['); index >= 0 {
		member = member[:index]
	}
	switch {
	case path == "id" || path == "version" || path == "kind" ||
		path == "tool_version" || path == "engine_version" || path == "runtime":
		return DisclosureIdentity
	case strings.HasPrefix(path, "publisher."):
		return DisclosurePublisher
	case strings.HasPrefix(value, "https://"), strings.HasPrefix(value, "http://"),
		member == "hosts", strings.HasPrefix(path, "source."), member == "url":
		return DisclosureNetwork
	case strings.Contains(path, "environment.inherit"), strings.Contains(path, "environment.set"):
		return DisclosureEnvironment
	case textMembers[member]:
		return DisclosureText
	}

	return ""
}
