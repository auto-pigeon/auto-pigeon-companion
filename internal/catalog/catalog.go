package catalog

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// SchemaVersion is the catalogue document format.
const SchemaVersion = "aucom.catalog/1.0"

// Archive kinds. Closed, because the extractor has to know what it is looking
// at before it opens anything, and "guess from the file name" is how an
// extractor ends up running the wrong parser on attacker-chosen bytes.
const (
	// KindFile is a single executable, downloaded as-is.
	KindFile = "file"
	// KindZip is a zip archive.
	KindZip = "zip"
	// KindTarGz is a gzip-compressed tar archive.
	KindTarGz = "tar.gz"
)

var artifactKinds = []string{KindFile, KindZip, KindTarGz}

// digestPattern is the only digest spelling this package accepts. Naming the
// algorithm is what keeps every stored digest unambiguous the day there is a
// second one.
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// idPattern is what a package id may look like.
//
// The same shape a profile's `catalog_package` member is checked against, and
// deliberately so: that member *is* this id, and two vocabularies that were
// nearly the same would differ first in the case nobody tested. Namespaced —
// at least two dot-separated segments — because a flat name space shared by
// everyone who publishes a catalogue entry is a name space with a land grab in
// it.
var idPattern = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9]+(-[a-z0-9]+)*)+$`)

// maxArtifactSize bounds any single download this program will ever make.
//
// A ceiling in the code as well as a size in the catalogue, because the
// catalogue's number is only trustworthy after the catalogue has been verified,
// and because a signed mistake is still a mistake. Map compilers are single- to
// double-digit megabytes; an engine with its assets is larger; a gigabyte is
// not a tool.
const maxArtifactSize = 1 << 30

// Artifact is one downloadable build of one package for one platform.
type Artifact struct {
	Platform profile.Platform `json:"platform"`
	// URL is immutable: the bytes at it never change, because a digest is
	// recorded here and a changed file simply stops verifying. Publishing a new
	// build means a new URL and a new catalogue entry, never a rewritten one.
	URL string `json:"url"`
	// Kind says what the bytes are, so the extractor never has to guess.
	Kind string `json:"kind"`
	// Size is exact, in bytes. Checked before the digest and enforced while
	// reading: it is what stops a download from being unbounded.
	Size int64 `json:"size"`
	// SHA256 is `sha256:<hex>` of the downloaded bytes.
	SHA256 string `json:"sha256"`
	// Signer names the catalogue key that vouches for this entry, and must be
	// one of the keys that actually signed the catalogue. A catalogue signed by
	// key A cannot smuggle in an entry attributed to key B.
	Signer string `json:"signer"`
	// UnpackedSize bounds extraction for an archive. Together with the entry
	// count and the per-entry checks it is what an archive bomb runs into.
	UnpackedSize int64 `json:"unpacked_size,omitempty"`
	// Root is the directory inside the archive that becomes the profile's
	// `tool_root`. Empty means the archive's own top level. It exists because
	// upstream archives almost always unpack into a versioned directory, and
	// the alternative — every profile spelling `qbsp-1.2.3/bin/qbsp` in its own
	// executable paths — would tie a profile to a release's packaging.
	Root string `json:"root,omitempty"`
	// File is the name the download is given inside `tool_root`, for the
	// `file` kind. A bare binary arrives with whatever name the URL gave it,
	// and that is not something a profile can depend on.
	File string `json:"file,omitempty"`
	// Executables lists paths, relative to Root, that must end up executable
	// whatever the archive said. A zip built on Windows carries no permission
	// bits at all, and a toolchain whose compiler is not executable is a
	// download that fails at the point the user has already waited.
	//
	// It is also the set re-checked every time the entry is used: these are
	// the files this program hands to the operating system.
	Executables []string `json:"executables"`
}

func (a Artifact) validate(pkg string, signers map[string]bool) error {
	where := fmt.Sprintf("catalog: %s on %s", pkg, a.Platform)
	if a.Platform.OS == "" || a.Platform.Arch == "" {
		return fmt.Errorf("catalog: %s has an artifact with no platform", pkg)
	}
	switch {
	case !contains(artifactKinds, a.Kind):
		return fmt.Errorf("%s has the kind %q; the kinds are %s", where, a.Kind, strings.Join(artifactKinds, ", "))
	case !digestPattern.MatchString(a.SHA256):
		return fmt.Errorf("%s has the digest %q; it must be sha256:<64 hex digits>", where, a.SHA256)
	case a.Size <= 0:
		return fmt.Errorf("%s has the size %d; an exact positive size is required, and it is what bounds the download", where, a.Size)
	case a.Size > maxArtifactSize:
		return fmt.Errorf("%s is %d bytes, over this build's %d-byte ceiling for a single artifact", where, a.Size, int64(maxArtifactSize))
	case strings.TrimSpace(a.Signer) == "":
		return fmt.Errorf("%s names no signer", where)
	case !signers[a.Signer]:
		return fmt.Errorf("%s is attributed to key %s, which did not sign this catalogue", where, a.Signer)
	case len(a.Executables) == 0:
		return fmt.Errorf("%s names no executables, so nothing could ever use it", where)
	}
	if err := checkArtifactURL(a.URL); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	seen := map[string]bool{}
	for _, path := range a.Executables {
		if err := checkArchivePath(path); err != nil {
			return fmt.Errorf("%s names the executable %q, which %w", where, path, err)
		}
		if seen[path] {
			return fmt.Errorf("%s names the executable %q twice", where, path)
		}
		seen[path] = true
	}
	switch a.Kind {
	case KindFile:
		switch {
		case a.File == "":
			return fmt.Errorf("%s is a single file and must say what to call it", where)
		case len(a.Executables) != 1 || a.Executables[0] != a.File:
			return fmt.Errorf("%s is a single file, so its only executable is %q", where, a.File)
		case a.Root != "":
			return fmt.Errorf("%s is a single file and has no directory inside it to use as a root", where)
		case a.UnpackedSize != 0:
			return fmt.Errorf("%s is a single file and must not declare an unpacked size", where)
		}
		if err := checkArchivePath(a.File); err != nil {
			return fmt.Errorf("%s is to be installed as %q, which %w", where, a.File, err)
		}
	default:
		if a.File != "" {
			return fmt.Errorf("%s is an archive, so `file` does not apply to it", where)
		}
		if a.Root != "" {
			if err := checkArchivePath(a.Root); err != nil {
				return fmt.Errorf("%s has the root %q, which %w", where, a.Root, err)
			}
		}
		if a.UnpackedSize <= 0 {
			return fmt.Errorf("%s is an archive and must declare unpacked_size; without it there is no bound to extract against", where)
		}
		if a.UnpackedSize > MaxUnpackedSize {
			return fmt.Errorf("%s declares %d unpacked bytes, over this build's %d-byte ceiling", where, a.UnpackedSize, int64(MaxUnpackedSize))
		}
	}
	return nil
}

// ExecutablePaths is where the declared executables sit relative to the
// extraction root, which is what the install record stores. Joining Root in
// here rather than at every use is what keeps "relative to what" from being a
// question anybody has to answer twice.
func (a Artifact) ExecutablePaths() []string {
	out := make([]string, 0, len(a.Executables))
	for _, name := range a.Executables {
		out = append(out, a.joinRoot(name))
	}
	return out
}

// RootPath is the extracted directory that becomes `tool_root`, relative to the
// extraction root.
func (a Artifact) RootPath() string { return a.Root }

func (a Artifact) joinRoot(name string) string {
	if a.Root == "" {
		return name
	}
	return a.Root + "/" + name
}

// checkArtifactURL refuses anything but an absolute https URL with no
// credentials in it.
//
// https and nothing else: the digest already makes tampering detectable, but a
// plaintext fetch tells anyone on the path exactly which tool and version a
// machine is installing, and downgrade-to-http is the first move against a
// mirror. No userinfo, because a URL with a password in it is a credential this
// program would then have to keep out of every log and every error, and the
// cheapest way to do that is to refuse to hold one.
func checkArtifactURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("has a URL that cannot be parsed")
	}
	switch {
	case parsed.Scheme != "https":
		return fmt.Errorf("has the URL scheme %q; a managed download is https, and nothing else", parsed.Scheme)
	case parsed.Host == "":
		return errors.New("has a URL with no host")
	case parsed.User != nil:
		return errors.New("has a URL with credentials in it; a catalogue URL carries no secret")
	}
	return nil
}

// Package is one acquirable program at one version.
type Package struct {
	// ID is what a profile's acquisition option names.
	ID string `json:"id"`
	// Version is the upstream release, exactly as upstream spells it. Not a
	// semantic version: upstream's numbering is upstream's.
	Version string `json:"version"`
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
	// Program is the upstream project's own name, when the package is only
	// part of it.
	Program string `json:"program,omitempty"`
	// Source is the upstream project — where the program comes from, not where
	// this catalogue does.
	Source profile.Source `json:"source"`
	// License is the downloaded program's own licence. This repository's is
	// MIT and has nothing to do with it; see [Package.Aggregation].
	License profile.License `json:"license"`
	// RequiresAcceptance marks a licence whose terms require that the user be
	// shown a notice before the program is obtained.
	RequiresAcceptance bool       `json:"requires_acceptance,omitempty"`
	Artifacts          []Artifact `json:"artifacts"`
}

// Aggregation is the sentence shown wherever a downloaded program is described.
//
// It is a fixed string rather than something a catalogue entry can write,
// because it is a statement about this program's relationship to the downloaded
// one and a catalogue must not be able to restate it.
const Aggregation = "Downloaded programs are separate works, obtained from their own publishers and run as " +
	"separate processes. They are not part of Auto-Pigeon Companion, are not covered by its MIT licence, " +
	"and keep their own licence and copyright."

// ArtifactFor returns the build for a platform.
func (p Package) ArtifactFor(platform profile.Platform) (Artifact, bool) {
	for _, artifact := range p.Artifacts {
		if artifact.Platform == platform {
			return artifact, true
		}
	}
	return Artifact{}, false
}

// Platforms lists the platforms this package has a build for, for the error a
// user sees when theirs is not one of them.
func (p Package) Platforms() []string {
	out := make([]string, 0, len(p.Artifacts))
	for _, artifact := range p.Artifacts {
		out = append(out, artifact.Platform.String())
	}
	return out
}

func (p Package) validate(signers map[string]bool) error {
	switch {
	case !idPattern.MatchString(p.ID):
		return fmt.Errorf("catalog: %q is not a package id; ids are namespaced and lower-case, such as `example.qbsp`", p.ID)
	case strings.TrimSpace(p.Version) == "":
		return fmt.Errorf("catalog: %s has no version", p.ID)
	case strings.TrimSpace(p.Name) == "":
		return fmt.Errorf("catalog: %s has no name", p.ID)
	case strings.TrimSpace(p.License.SPDX) == "":
		return fmt.Errorf("catalog: %s does not say what licence it is under; a program this build would download and run always does", p.ID)
	case len(p.Artifacts) == 0:
		return fmt.Errorf("catalog: %s has no artifacts", p.ID)
	}
	if p.Source.Homepage == "" && p.Source.Repository == "" {
		return fmt.Errorf("catalog: %s names no upstream source; a user approving a download is entitled to the project it came from", p.ID)
	}
	if requiresCorrespondingSource(p.License.SPDX) && strings.TrimSpace(p.License.CorrespondingSource) == "" {
		return fmt.Errorf("catalog: %s is under %s and offers no corresponding source; distributing a binary under that licence requires one",
			p.ID, p.License.SPDX)
	}
	seen := map[string]bool{}
	for _, artifact := range p.Artifacts {
		if err := artifact.validate(p.ID+" "+p.Version, signers); err != nil {
			return err
		}
		key := artifact.Platform.String()
		if seen[key] {
			return fmt.Errorf("catalog: %s has two artifacts for %s", p.ID, key)
		}
		seen[key] = true
	}
	return nil
}

// copyleft is the set of licence identifiers that require a corresponding
// source offer alongside a distributed binary.
//
// A prefix list rather than a full SPDX table: the identifiers that matter here
// are the ones the map-building tools actually use, and a check that is
// deliberately narrow and says so is better than one that pretends to know
// every licence in existence.
var copyleft = []string{"GPL-", "LGPL-", "AGPL-", "GPL-2.0", "GPL-3.0"}

func requiresCorrespondingSource(spdx string) bool {
	for _, prefix := range copyleft {
		if strings.HasPrefix(spdx, prefix) {
			return true
		}
	}
	return false
}

// Revocation withdraws one artifact by digest.
//
// By digest and not by package or version, because the thing being withdrawn is
// a specific set of bytes: a republished build under the same version is a
// different artifact, and a revocation keyed by version would take it down too.
type Revocation struct {
	Digest string    `json:"digest"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

func (r Revocation) validate() error {
	switch {
	case !digestPattern.MatchString(r.Digest):
		return fmt.Errorf("catalog: the revocation of %q does not name a sha256 digest", r.Digest)
	case strings.TrimSpace(r.Reason) == "":
		return fmt.Errorf("catalog: the revocation of %s gives no reason; a revocation nobody can act on is not one", r.Digest)
	case r.At.IsZero():
		return fmt.Errorf("catalog: the revocation of %s does not say when", r.Digest)
	}
	return nil
}

// Catalog is the signed map from packages to downloadable bytes.
type Catalog struct {
	SchemaVersion string `json:"schema_version"`
	CatalogID     string `json:"catalog_id"`
	// Serial increases with every publication. See [Keyring.Serial].
	Serial    int64     `json:"serial"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	// Packages is every acquirable build. A package id may appear more than
	// once, at different versions: keeping old versions listed is what lets a
	// pinned build stay installable.
	Packages []Package `json:"packages"`
	// Revocations withdraws artifacts. Carried in the catalogue rather than in
	// a separate document so that a client fetching the catalogue cannot fetch
	// the good half and skip this one.
	Revocations []Revocation `json:"revocations,omitempty"`
}

// Validate checks the document against itself and against the keys that signed
// it. The signer set is a parameter because "this entry is vouched for by a key
// that signed this catalogue" is not a property of the document alone.
func (c *Catalog) Validate(signers map[string]bool) error {
	switch {
	case c.SchemaVersion != SchemaVersion:
		return fmt.Errorf("catalog: the catalogue is %q; this build reads %q", c.SchemaVersion, SchemaVersion)
	case strings.TrimSpace(c.CatalogID) == "":
		return errors.New("catalog: the catalogue has no catalog_id")
	case c.Serial < 1:
		return fmt.Errorf("catalog: the catalogue's serial is %d; serials start at 1 and only increase", c.Serial)
	case c.IssuedAt.IsZero() || c.ExpiresAt.IsZero():
		return errors.New("catalog: the catalogue must say both issued_at and expires_at")
	case !c.ExpiresAt.After(c.IssuedAt):
		return errors.New("catalog: the catalogue expires at or before it was issued")
	}
	seen := map[string]bool{}
	for _, pkg := range c.Packages {
		if err := pkg.validate(signers); err != nil {
			return err
		}
		key := pkg.ID + "@" + pkg.Version
		if seen[key] {
			return fmt.Errorf("catalog: %s is listed twice", key)
		}
		seen[key] = true
	}
	revoked := map[string]bool{}
	for _, revocation := range c.Revocations {
		if err := revocation.validate(); err != nil {
			return err
		}
		if revoked[revocation.Digest] {
			return fmt.Errorf("catalog: %s is revoked twice", revocation.Digest)
		}
		revoked[revocation.Digest] = true
	}
	return nil
}

// Find returns one package at one version. An empty version means the highest
// non-revoked version listed, which is what a user who did not pin gets.
func (c *Catalog) Find(id, version string) (Package, bool) {
	var best Package
	found := false
	for _, pkg := range c.Packages {
		if pkg.ID != id {
			continue
		}
		if version != "" {
			if pkg.Version == version {
				return pkg, true
			}
			continue
		}
		if !found || laterVersion(pkg.Version, best.Version) {
			best, found = pkg, true
		}
	}
	return best, found
}

// Versions lists every version of a package the catalogue carries, newest
// first.
func (c *Catalog) Versions(id string) []string {
	var versions []string
	for _, pkg := range c.Packages {
		if pkg.ID == id {
			versions = append(versions, pkg.Version)
		}
	}
	sortVersionsDescending(versions)
	return versions
}

// Revoked reports whether a digest has been withdrawn by this catalogue, and
// why.
func (c *Catalog) Revoked(digest string) (Revocation, bool) {
	for _, revocation := range c.Revocations {
		if revocation.Digest == digest {
			return revocation, true
		}
	}
	return Revocation{}, false
}
