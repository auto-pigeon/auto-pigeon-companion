package publish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/fsshare"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// Installing: five checks, in an order that matters.
//
//  1. The bytes are what the catalog said they were — recomputed here, never
//     taken from the answer that carried them.
//  2. They are the CANONICAL form of the document they encode. This is the check
//     AUB deliberately does not perform, because RFC 8785 is this package's
//     algorithm, and a second implementation over there would refuse legitimate
//     documents over a string-escaping difference. Here it is exact.
//  3. The document is valid, by the same decoder a pasted file goes through.
//  4. What changed since whatever is already installed, normalized.
//  5. What it asks this machine for.
//
// Only then is anything written, and only with a decision a person made.

// ErrDigestMismatch reports bytes that are not what they were named as.
var ErrDigestMismatch = errors.New("publish: the document does not match the digest it was served under")

// ErrNotCanonical reports a document that is not in the canonical form its digest
// is supposed to be over.
var ErrNotCanonical = errors.New("publish: the served bytes are not the document's canonical form")

// ErrRefusedByReview reports an install stopped because nobody approved it.
var ErrRefusedByReview = errors.New("publish: nothing was installed, because the review was not approved")

// ErrVersionMoved reports a published version whose content differs from the one
// already installed under the same id and version.
//
// A distinct error rather than an ordinary update, because it is not one: a
// version is a claim the author made and a digest is a fact about the bytes, so
// two different documents under one version means one of them is not what
// somebody reviewed. `profile.CheckVersionImmutable` is what says so.
var ErrVersionMoved = errors.New("publish: that version is already installed with different content")

// ErrIdentityConflict reports an install refused because what is already here
// under that id is not this publication's: a built-in, a document written here,
// another listing or publisher, or an install whose origin was never recorded.
var ErrIdentityConflict = errors.New("publish: that id is already installed from somewhere else")

// Plan is everything an install would do, computed without doing any of it.
type Plan struct {
	Listing aub.PublishedProfile `json:"listing"`
	Version aub.PublishedVersion `json:"version"`

	// Document is the exact bytes served. Written verbatim if the install
	// proceeds: a document re-encoded on the way to disk is a document whose
	// digest no longer names it.
	Document []byte `json:"-"`

	// Digest is recomputed HERE over Document. Announced is what the catalog
	// said. They are separate members so a mismatch is visible rather than
	// implied by an error.
	Digest    string `json:"digest"`
	Announced string `json:"announced_digest"`

	// Canonical reports that Document is the canonical encoding of what it
	// decodes to — the check AUB does not make.
	Canonical bool `json:"canonical"`

	// DeploymentTrust is what the AUB deployment says: `community`, `verified` or
	// `builtin`. Shown, never adopted — see the package doc.
	DeploymentTrust string `json:"deployment_trust"`

	// Trust is what this machine will record, and it is always `community`.
	Trust profile.Trust `json:"trust"`

	// Diff is the normalized difference against what is installed, and
	// FirstInstall says there was nothing to diff against.
	Diff         profile.Diff `json:"diff"`
	FirstInstall bool         `json:"first_install"`

	// Permissions is what the incoming document asks for, worst first.
	Permissions []profile.Permission `json:"permissions"`

	// Yanked and YankReason travel with the plan rather than blocking it. A
	// withdrawn version is installable ON PURPOSE — reproducing a build that used
	// it is a legitimate reason to want one — and what must not happen is
	// installing one without being told.
	Yanked     bool   `json:"yanked"`
	YankReason string `json:"yank_reason,omitempty"`

	// Superseded names the version that replaces a withdrawn one, when the
	// publisher said.
	Superseded string `json:"superseded_by_version,omitempty"`

	decoded   profile.Profile
	installed profile.Profile
}

// Profile is the decoded incoming document.
func (p Plan) Profile() profile.Profile { return p.decoded }

// Escalates reports that this install asks for more than what is installed was
// granted. The caller must not proceed on a stale approval.
func (p Plan) Escalates() bool { return p.Diff.Escalates() }

// PlanInstall fetches one published version and works out everything about it.
//
// `catalog` is the local profile catalog, consulted for what is already here.
// Nil is allowed and means "nothing is installed", which is what a caller with no
// state directory has.
func PlanInstall(ctx context.Context, client *aub.Client, catalog job.Catalog,
	listingID, version string,
) (Plan, error) {
	detail, err := client.PublishedProfileByID(ctx, listingID)
	if err != nil {
		return Plan{}, err
	}
	if version == "" {
		version = detail.Profile.LatestVersion
	}
	if version == "" {
		return Plan{}, fmt.Errorf("publish: %s has no published version", detail.Profile.ProfileID)
	}
	served, err := client.PublishedProfileVersion(ctx, listingID, version)
	if err != nil {
		return Plan{}, err
	}

	document := []byte(served.Version.Document)
	sum := sha256.Sum256(document)
	plan := Plan{
		Listing:         detail.Profile,
		Version:         served.Version,
		Document:        document,
		Digest:          "sha256:" + hex.EncodeToString(sum[:]),
		Announced:       served.Version.Digest,
		DeploymentTrust: detail.Profile.Trust,

		// Always. See the package doc: a deployment's badge is information, and
		// this machine's authorization stays this machine's.
		Trust: profile.TrustCommunity,

		Yanked:     served.Version.Yanked,
		YankReason: served.Version.YankReason,
		Superseded: served.Version.SupersededByVersion,
	}
	if plan.Announced != "" && !strings.EqualFold(plan.Announced, plan.Digest) {
		return plan, fmt.Errorf("%w: served as %s, and these bytes are %s",
			ErrDigestMismatch, plan.Announced, plan.Digest)
	}

	decoded, err := profile.Decode(document)
	if err != nil {
		return plan, err
	}
	plan.decoded = decoded
	plan.Permissions = decoded.Permissions()

	// The canonical check. `Export` is the one encoder; if what it produces is not
	// what arrived, the digest names bytes that are not the document's canonical
	// form, and a second publisher canonicalizing the same document would get a
	// different answer.
	canonical, err := profile.Export(decoded)
	if err != nil {
		return plan, err
	}
	plan.Canonical = bytes.Equal(canonical, document)
	if !plan.Canonical {
		return plan, fmt.Errorf("%w: re-encoding it gives %d bytes against the %d served",
			ErrNotCanonical, len(canonical), len(document))
	}

	if catalog != nil {
		if entry, lookupErr := catalog.Lookup(decoded.Metadata().ID); lookupErr == nil {
			plan.installed = entry.Profile
		}
	}
	plan.FirstInstall = plan.installed == nil
	if err := profile.CheckVersionImmutable(plan.installed, decoded); err != nil {
		return plan, fmt.Errorf("%w: %v", ErrVersionMoved, err)
	}
	plan.Diff, err = profile.DiffProfiles(plan.installed, decoded)
	if err != nil {
		return plan, err
	}

	return plan, nil
}

// InstallPaths says where an install writes.
type InstallPaths struct {
	// Profiles is the directory of profile documents the local catalog reads.
	Profiles string
	// Bindings is the file recording what is installed on this machine.
	Bindings string
	// OwnAccount says the caller knows the listing is the signed-in account's
	// own (the account sync reads it from the account's own listings), so it may
	// replace a document this person wrote here under the same id. Nothing else
	// sets it.
	OwnAccount bool
}

// Apply writes the document and, when the review was approved, the binding.
//
// `approved` is the person's decision about the permission review and the diff.
// Without it nothing is written at all — not even the document — because a
// document on disk is a document the local catalog lists, and listing something
// nobody agreed to is how a review becomes a formality.
func Apply(plan Plan, paths InstallPaths, approved bool) (binding.LocalBinding, error) {
	if plan.decoded == nil {
		return binding.LocalBinding{}, errors.New("publish: this plan holds no document")
	}
	if !plan.Canonical || !strings.EqualFold(plan.Digest, digestOf(plan.Document)) {
		return binding.LocalBinding{}, ErrDigestMismatch
	}
	if !approved {
		return binding.LocalBinding{}, ErrRefusedByReview
	}
	if paths.Profiles == "" || paths.Bindings == "" {
		return binding.LocalBinding{}, errors.New("publish: no profiles directory or bindings file was given")
	}
	if err := os.MkdirAll(paths.Profiles, 0o700); err != nil {
		return binding.LocalBinding{}, err
	}

	meta := plan.decoded.Metadata()
	name, err := DocumentFileName(meta)
	if err != nil {
		return binding.LocalBinding{}, err
	}
	origin := binding.InstallOrigin{ListingID: plan.Listing.ID, PublisherID: plan.Listing.Publisher.UserID}
	if origin.ListingID == "" || origin.PublisherID == "" {
		return binding.LocalBinding{}, fmt.Errorf("%w: the catalog did not say which listing and which account %s comes from, "+
			"so nothing could tell a later update from somebody else's document under the same id", ErrIdentityConflict, meta.ID)
	}
	if _, err := builtin.Find(meta.ID); err == nil {
		return binding.LocalBinding{}, fmt.Errorf("%w: %s is a profile built into this program; a publication cannot replace it. "+
			"Ask its publisher to publish it under an id of their own", ErrIdentityConflict, meta.ID)
	}

	// Read, changed and written inside one cross-process lock, so an install
	// running beside a GUI server cannot discard a grant or an engine path the
	// other recorded. See [binding.Update]. The identity is decided inside it
	// too, before the document is written: a refusal leaves the document, the
	// paths, the arguments and the approval exactly as they were.
	var local binding.LocalBinding
	if _, err := binding.Update(paths.Bindings, func(set *binding.Set) error {
		existing, installed := set.Find(meta.ID)
		if err := checkIdentity(meta.ID, paths, existing, installed, origin); err != nil {
			return err
		}
		if err := writeAtomically(filepath.Join(paths.Profiles, name), plan.Document); err != nil {
			return err
		}
		local = existing
		local.SchemaVersion = binding.SchemaVersion
		local.ProfileID = meta.ID
		local.ProfileVersion = meta.Version
		local.ProfileDigest = plan.Digest
		local.Trust = plan.Trust
		local.Origin = &origin
		if local.Acquisition == "" {
			local.Acquisition = profile.AcquireUserPath
		}

		// The grant is against THIS digest, so a later version asking for more is a
		// new decision rather than something the old approval covers.
		local.Grant = profile.NewGrant(plan.decoded, plan.Trust, plan.Digest, time.Now())
		local.UpdatedAt = time.Now().UTC()
		return set.Put(local)
	}); err != nil {
		return binding.LocalBinding{}, err
	}

	return local, nil
}

// checkIdentity decides whether a publication from `origin` may be installed
// under `id` (NEW_353A).
//
// A profile id is the publisher's own name for a document, and AUB keys a
// listing on (publisher, id), so the id alone says nothing about whose a
// document is. What is installed under it may be replaced only by the same
// listing of the same account. A document written or pasted here is the
// person's own, replaced only by their own account's publication of it (the
// account sync says so). An install whose origin was never recorded — one made
// before bindings carried it — is never replaced by a matching id: the person
// removes it and installs again, deliberately.
func checkIdentity(id string, paths InstallPaths, existing binding.LocalBinding, installed bool, origin binding.InstallOrigin) error {
	if !installed {
		// No binding, but perhaps a document: one written or pasted here and
		// never set up, or a stray file. Either way it is not this listing's.
		_, err := job.NewCatalog(paths.Profiles).Lookup(id)
		switch {
		case err == nil && paths.OwnAccount:
			return nil
		case err == nil:
			return fmt.Errorf("%w: a document with the id %s is already here and did not come from this listing", ErrIdentityConflict, id)
		case !errors.Is(err, job.ErrNoProfile):
			return err
		}
		return nil
	}
	// The recorded origin decides, not the binding's trust: only an install
	// records an origin, and approving the profile or saving its program paths
	// here rewrites the trust from the catalog, which calls every document in
	// the profile folder `local` (NEW_353A live acceptance).
	switch {
	case existing.Origin != nil && *existing.Origin == origin:
		return nil
	case existing.Origin != nil:
		return fmt.Errorf("%w: %s is installed from another listing or another account; this publication cannot replace it",
			ErrIdentityConflict, id)
	case existing.Trust == profile.TrustCommunity:
		return fmt.Errorf("%w: %s was installed before this Companion recorded where installs come from, so it cannot "+
			"tell whether this publication is the same one; remove it and install this one if you mean to replace it", ErrIdentityConflict, id)
	case existing.Trust == profile.TrustLocal && paths.OwnAccount:
		return nil
	default:
		return fmt.Errorf("%w: %s is %s on this computer; a publication cannot replace it", ErrIdentityConflict, id, existing.Trust)
	}
}

// DocumentFileName is the name a published document is stored under.
//
// `<id>.<kind>.json`, which is the convention the built-in documents and the
// GUI's import already use, so a document that arrived from a catalog and one a
// person pasted are indistinguishable on disk. The id is checked for path
// separators first: a profile id is validated by `internal/profile`, and this is
// the second check because this is the function that turns one into a path.
func DocumentFileName(meta profile.Meta) (string, error) {
	id := meta.ID
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return "", fmt.Errorf("publish: %q is not usable as a file name", id)
	}

	return id + "." + string(meta.Kind) + ".json", nil
}

func digestOf(document []byte) string {
	sum := sha256.Sum256(document)

	return "sha256:" + hex.EncodeToString(sum[:])
}

// writeAtomically stages beside the target and renames, so an interrupted write
// leaves the previous document rather than half of a new one.
func writeAtomically(path string, data []byte) error {
	staged, err := os.CreateTemp(filepath.Dir(path), ".staged-*")
	if err != nil {
		return err
	}
	defer os.Remove(staged.Name())
	if _, err := staged.Write(data); err != nil {
		staged.Close()

		return err
	}
	if err := staged.Chmod(0o600); err != nil {
		staged.Close()

		return err
	}
	if err := staged.Close(); err != nil {
		return err
	}

	return fsshare.Replace(staged.Name(), path)
}

// decodeTree reads canonical bytes into a plain tree, refusing anything after the
// document. Shared with the preview's walk.
func decodeTree(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var tree any
	if err := decoder.Decode(&tree); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("publish: there is more than one document here")
	}

	return tree, nil
}
