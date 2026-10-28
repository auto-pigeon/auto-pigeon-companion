package managedtools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Channel is which releases an installation follows.
type Channel string

const (
	// ChannelStable takes only releases GitHub does not mark as prereleases.
	ChannelStable Channel = "stable"
	// ChannelBeta also takes the official prereleases. It is the shipped
	// default while the Companion is itself in beta, because every official
	// release of both tools is, today, a prerelease — and a "stable" default
	// would make them silently invisible.
	ChannelBeta Channel = "beta"
)

// DefaultChannel is the channel a Companion follows when nobody chose one.
const DefaultChannel = ChannelBeta

// ParseChannel reads a configured channel; empty is the default.
func ParseChannel(value string) (Channel, error) {
	switch Channel(strings.TrimSpace(value)) {
	case "":
		return DefaultChannel, nil
	case ChannelStable:
		return ChannelStable, nil
	case ChannelBeta:
		return ChannelBeta, nil
	}
	return "", fmt.Errorf("%q is not a tools channel; use stable or beta", value)
}

// ManifestAssetName is the release file that describes a release.
const ManifestAssetName = "release-manifest.json"

// Reason is why a release is not one this Companion installs. Codes, so the
// page and the CLI word them and a test can name them.
type Reason string

const (
	ReasonEligible            Reason = ""
	ReasonDraft               Reason = "draft"
	ReasonChannel             Reason = "prerelease_on_stable"
	ReasonTagFamily           Reason = "not_this_tools_tag"
	ReasonBelowBaseline       Reason = "older_than_baseline"
	ReasonNoManifest          Reason = "no_manifest"
	ReasonManifestUnreadable  Reason = "manifest_unreadable"
	ReasonManifestUnsupported Reason = "manifest_schema_unsupported"
	ReasonManifestMismatch    Reason = "manifest_disagrees"
	ReasonNoTarget            Reason = "no_build_for_this_platform"
	ReasonNotQualified        Reason = "not_qualified_for_this_platform"
	ReasonNoDigest            Reason = "no_digest"
	ReasonDigestDisagrees     Reason = "digests_disagree"
	ReasonRefusedIdentity     Reason = "refused_by_profile"
	ReasonNotAccepted         Reason = "not_accepted_by_profile"
	ReasonNoSidecar           Reason = "no_bundled_extractor"
)

// Candidate is one release this Companion could install, with every fact the
// installer will check the bytes against.
type Candidate struct {
	Tool       ToolID    `json:"tool"`
	Tag        string    `json:"tag"`
	Version    string    `json:"version"`
	Prerelease bool      `json:"prerelease"`
	ReleaseID  int64     `json:"release_id"`
	NotesURL   string    `json:"notes_url"`
	Published  time.Time `json:"published_at"`
	Banner     string    `json:"version_banner"`
	Commit     string    `json:"commit"`
	Target     string    `json:"target"`
	// Asset is the archive for Target; AssetID, Size and SHA256 identify the
	// exact bytes. GitHubDigest is GitHub's own digest of them, when it
	// publishes one; it has already been compared with SHA256.
	Asset        string `json:"asset"`
	AssetID      int64  `json:"asset_id"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	GitHubDigest string `json:"github_digest,omitempty"`
	// ManifestSHA256 is the digest of the release manifest these facts came
	// from.
	ManifestSHA256 string `json:"manifest_sha256"`
	// Qualification is what the release says it tested on this target.
	Qualification string `json:"qualification"`
	// Signed is what the release says; always false today, and shown as such.
	Signed bool `json:"signed"`
	// Sidecar is the extractor a Companion release bundles, as its release
	// manifest names it; empty for a toolchain.
	Sidecar *Sidecar `json:"sidecar,omitempty"`
	// PlatformState is the release's own native-support state for Target
	// (native_pass, manual_pending, build_only …), quoted, never widened.
	PlatformState string `json:"platform_state,omitempty"`
}

// Sidecar is the extractor a Companion bundle carries.
type Sidecar struct {
	Version  string `json:"version"`
	Protocol string `json:"protocol"`
	Commit   string `json:"commit"`
	License  string `json:"license,omitempty"`
}

// Identity is the exact bytes a candidate names: what a skipped version and
// an integrity check compare.
func (c Candidate) Identity() BytesIdentity {
	return BytesIdentity{Version: c.Version, AssetID: c.AssetID, SHA256: c.SHA256}
}

// BytesIdentity is a version plus the immutable asset it was published as.
type BytesIdentity struct {
	Version string `json:"version"`
	AssetID int64  `json:"asset_id"`
	SHA256  string `json:"sha256"`
}

// Verdict is what was decided about one release.
type Verdict struct {
	Tag     string `json:"tag"`
	Version string `json:"version,omitempty"`
	Reason  Reason `json:"reason,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// Evaluation is one check of one tool's releases.
type Evaluation struct {
	// Best is the newest eligible release, or nil.
	Best *Candidate
	// Newer lists the releases newer than Best that were not eligible, and
	// why: what the page says when the newest release is not offered.
	Newer []Verdict
	// Ignored lists drafts and tags of this family whose version does not
	// parse: never ordered, never installed.
	Ignored []Verdict
}

// maxManifestsPerCheck bounds how many release manifests one check reads
// before it gives up looking for an eligible release.
const maxManifestsPerCheck = 6

// evaluator holds what an evaluation needs besides the release list.
type evaluator struct {
	tool    Tool
	target  string
	channel Channel
	// probe is the built-in profile's compatibility rule for this tool's
	// programs: its accepted identities and its refused ones. A release the
	// profile would refuse is never installed.
	probe *profile.CompatibilityProbe
	// fetch reads a release file, bounded.
	fetch func(ctx context.Context, release Release, name string, limit int64) ([]byte, error)
}

// evaluate picks the newest eligible release from a release list.
func (e evaluator) evaluate(ctx context.Context, releases []Release) (Evaluation, error) {
	baseline, err := e.tool.Version(e.tool.Baseline)
	if err != nil {
		return Evaluation{}, err
	}
	type ordered struct {
		release  Release
		version  string
		sequence Sequence
	}
	var listed []ordered
	var out Evaluation
	for _, release := range releases {
		version, ok := e.tool.VersionFromTag(release.Tag)
		switch {
		case !ok:
			// Another tag family in the same repository is not this tool's at
			// all; a tag of this family whose version does not parse cannot
			// be ordered, so it is never "newer" than anything.
			if strings.HasPrefix(release.Tag, e.tool.TagPrefix) {
				out.Ignored = append(out.Ignored, Verdict{Tag: release.Tag, Reason: ReasonTagFamily,
					Detail: "the version in this tag is not one this tool's releases use"})
			}
			continue
		case release.Draft:
			out.Ignored = append(out.Ignored, Verdict{Tag: release.Tag, Version: version, Reason: ReasonDraft})
			continue
		}
		sequence, _ := e.tool.Version(version)
		listed = append(listed, ordered{release, version, sequence})
	}
	// Newest first by the tool's own version order, never by date or by text.
	sort.SliceStable(listed, func(i, j int) bool { return listed[i].sequence.Compare(listed[j].sequence) > 0 })
	read := 0
	for _, item := range listed {
		verdict := Verdict{Tag: item.release.Tag, Version: item.version}
		switch {
		case item.sequence.Compare(baseline) < 0:
			// Everything from here on is older than the baseline: stop.
			return out, nil
		case item.release.Prerelease && e.channel != ChannelBeta:
			verdict.Reason = ReasonChannel
			verdict.Detail = "a prerelease, and this Companion follows stable releases"
			out.Newer = append(out.Newer, verdict)
			continue
		}
		if read >= maxManifestsPerCheck {
			return out, nil
		}
		read++
		candidate, reason, detail := e.candidate(ctx, item.release, item.version)
		if reason == ReasonEligible {
			out.Best = candidate
			return out, nil
		}
		verdict.Reason, verdict.Detail = reason, detail
		out.Newer = append(out.Newer, verdict)
	}
	return out, nil
}

// releaseManifest is the part of a release manifest this package reads. The
// manifests carry much more (test counts, regression cases, build runs); it
// is kept, unread, in the bytes the digest covers.
type releaseManifest struct {
	Schema        string                    `json:"schema"`
	Tag           string                    `json:"tag"`
	Version       string                    `json:"version"`
	VersionBanner string                    `json:"version_banner"`
	Commit        string                    `json:"commit"`
	Prerelease    *bool                     `json:"prerelease"`
	Signed        bool                      `json:"signed"`
	Qualification string                    `json:"qualification"`
	Targets       map[string]manifestTarget `json:"targets"`
	Assets        []manifestAsset           `json:"assets"`
}

type manifestTarget struct {
	Asset    string `json:"asset"`
	Status   string `json:"status"`
	Required *bool  `json:"required"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

type manifestAsset struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Platform   string `json:"platform"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Acceptance *struct {
		Verdict string `json:"verdict"`
	} `json:"acceptance"`
}

var hexDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// candidate reads one release's manifest and decides whether this machine
// can install it.
func (e evaluator) candidate(ctx context.Context, release Release, version string) (*Candidate, Reason, string) {
	if e.tool.Kind == KindApplication {
		return e.companionCandidate(ctx, release, version)
	}
	listed, ok := release.asset(ManifestAssetName)
	if !ok {
		return nil, ReasonNoManifest, "the release publishes no " + ManifestAssetName
	}
	if listed.Size <= 0 || listed.Size > maxManifestBytes {
		return nil, ReasonManifestUnreadable, fmt.Sprintf("its %s is %d bytes", ManifestAssetName, listed.Size)
	}
	raw, err := e.fetch(ctx, release, ManifestAssetName, listed.Size)
	if err != nil {
		return nil, ReasonManifestUnreadable, err.Error()
	}
	sum := sha256.Sum256(raw)
	manifestDigest := hex.EncodeToString(sum[:])
	if digest, published := githubDigest(listed); published && digest != manifestDigest {
		return nil, ReasonDigestDisagrees, "the release manifest's bytes are not the ones GitHub lists"
	}
	var manifest releaseManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&manifest); err != nil {
		return nil, ReasonManifestUnreadable, "the release manifest is not JSON: " + err.Error()
	}
	if !contains(e.tool.ManifestSchemas, manifest.Schema) {
		return nil, ReasonManifestUnsupported, fmt.Sprintf("its manifest is %q, which this Companion cannot read; "+
			"a newer Companion may, or use the release by hand from %s", manifest.Schema, e.tool.ReleasesURL())
	}
	switch {
	case manifest.Tag != release.Tag:
		return nil, ReasonManifestMismatch, fmt.Sprintf("the manifest names the tag %q, the release is %q", manifest.Tag, release.Tag)
	case manifest.Version != version:
		return nil, ReasonManifestMismatch, fmt.Sprintf("the manifest names the version %q, the tag says %q", manifest.Version, version)
	case manifest.Prerelease != nil && *manifest.Prerelease != release.Prerelease:
		return nil, ReasonManifestMismatch, "the manifest and GitHub disagree about whether this is a prerelease"
	case strings.TrimSpace(manifest.VersionBanner) == "":
		return nil, ReasonManifestMismatch, "the manifest names no version banner"
	}
	target, published := manifest.Targets[e.target]
	if !published || target.Status != "published" || target.Asset == "" {
		return nil, ReasonNoTarget, fmt.Sprintf("the release publishes no %s build", e.target)
	}
	var described *manifestAsset
	for i := range manifest.Assets {
		if manifest.Assets[i].Name == target.Asset {
			described = &manifest.Assets[i]
		}
	}
	switch {
	case described == nil || described.Kind != "toolchain":
		return nil, ReasonNoTarget, fmt.Sprintf("the manifest lists %s as the %s build and does not describe it", target.Asset, e.target)
	case described.Platform != "" && described.Platform != e.target:
		return nil, ReasonManifestMismatch, fmt.Sprintf("%s is described as a %s build", target.Asset, described.Platform)
	case described.Acceptance == nil || described.Acceptance.Verdict != "pass":
		return nil, ReasonNotQualified, fmt.Sprintf("the release records no passing acceptance of its %s build", e.target)
	case !hexDigest.MatchString(described.SHA256) || described.Size <= 0:
		return nil, ReasonNoDigest, fmt.Sprintf("the manifest gives no SHA-256 and size for %s", target.Asset)
	case target.SHA256 != "" && target.SHA256 != described.SHA256,
		target.Size != 0 && target.Size != described.Size:
		return nil, ReasonManifestMismatch, "the manifest's target and asset entries disagree about " + target.Asset
	}
	archive, carried := release.asset(target.Asset)
	if !carried {
		return nil, ReasonNoTarget, fmt.Sprintf("the manifest names %s and the release does not carry it", target.Asset)
	}
	if archive.Size != described.Size {
		return nil, ReasonDigestDisagrees, fmt.Sprintf("GitHub lists %s as %d bytes; the manifest says %d", target.Asset, archive.Size, described.Size)
	}
	digest, hasDigest := githubDigest(archive)
	if hasDigest && digest != described.SHA256 {
		return nil, ReasonDigestDisagrees, fmt.Sprintf("GitHub's digest of %s is not the manifest's", target.Asset)
	}
	if e.probe != nil {
		if refused, isRefused := e.probe.Refused(manifest.VersionBanner); isRefused {
			return nil, ReasonRefusedIdentity, refused.Reason
		}
		if !e.probe.Accepts(manifest.VersionBanner) {
			return nil, ReasonNotAccepted, fmt.Sprintf("its programs print %q, which the built-in profile does not accept (%s)",
				manifest.VersionBanner, e.probe.AcceptText())
		}
	}
	candidate := &Candidate{
		Tool:           e.tool.ID,
		Tag:            release.Tag,
		Version:        version,
		Prerelease:     release.Prerelease,
		ReleaseID:      release.ID,
		NotesURL:       e.tool.ReleasesURL() + "/tag/" + release.Tag,
		Published:      release.PublishedAt,
		Banner:         manifest.VersionBanner,
		Commit:         manifest.Commit,
		Target:         e.target,
		Asset:          target.Asset,
		AssetID:        archive.ID,
		Size:           described.Size,
		SHA256:         described.SHA256,
		ManifestSHA256: manifestDigest,
		Qualification:  qualification(manifest, target, e.target),
		Signed:         manifest.Signed,
	}
	if hasDigest {
		candidate.GitHubDigest = "sha256:" + digest
	}
	return candidate, ReasonEligible, ""
}

func qualification(manifest releaseManifest, target manifestTarget, name string) string {
	text := "the release's own acceptance of its " + name + " build passed"
	if target.Required != nil && !*target.Required {
		text += "; this platform is optional for the release, so it is not one of the targets it must pass"
	}
	if manifest.Qualification != "" {
		text += "; " + manifest.Qualification
	}
	return text
}

// githubDigest is GitHub's own sha256 for an asset, when it lists one.
func githubDigest(asset Asset) (string, bool) {
	digest, ok := strings.CutPrefix(asset.Digest, "sha256:")
	if !ok || !hexDigest.MatchString(digest) {
		return "", false
	}
	return digest, true
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// Describe words a reason for a person.
func (r Reason) Describe() string {
	switch r {
	case ReasonDraft:
		return "a draft"
	case ReasonChannel:
		return "a prerelease, and this Companion follows stable releases"
	case ReasonBelowBaseline:
		return "older than the release this Companion was built for"
	case ReasonNoManifest, ReasonManifestUnreadable:
		return "its release manifest could not be read"
	case ReasonManifestUnsupported:
		return "its release manifest is a format this Companion cannot read"
	case ReasonManifestMismatch:
		return "its release manifest disagrees with the release"
	case ReasonNoTarget:
		return "it publishes no build for this computer"
	case ReasonNotQualified:
		return "its build for this computer has no passing acceptance"
	case ReasonNoDigest:
		return "it gives no SHA-256 for this computer's build"
	case ReasonDigestDisagrees:
		return "GitHub's digest and the release manifest disagree"
	case ReasonRefusedIdentity:
		return "the built-in profile refuses that release"
	case ReasonNotAccepted:
		return "the built-in profile does not accept that release"
	case ReasonNoSidecar:
		return "it ships no extractor for this computer"
	}
	return string(r)
}
