package managedtools

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/acquire"
)

// The Companion's own update (NEW_368, ADR-0010).
//
// The Companion is the second kind of official target this package installs.
// It is checked by the same coordinator, through the same release source and
// bounds, and offered with the same three choices, but an "installation" of it
// is a verified copy of a complete release bundle — the Companion and the
// extractor beside it, with the bundle manifest that pairs them — staged in
// the tools folder. Putting that bundle where the running one is, is not done
// here: internal/selfupdate does it, at a boundary where nothing is running,
// from the staged copy this file verified. This file decides only WHETHER and
// WHAT; it never writes outside the tools folder.

// MacAppName is the application folder a macOS bundle carries.
const MacAppName = "Auto-Pigeon Companion.app"

// BundleManifestName is the file that pairs a Companion with its extractor.
const BundleManifestName = "bundle-manifest.json"

// SelfMode is how this Companion can be updated.
type SelfMode string

const (
	// SelfPortable: an official bundle in a folder this user can write. The
	// update replaces the bundle's own files and nothing else.
	SelfPortable SelfMode = "portable"
	// SelfManual: an official bundle the Companion does not replace itself —
	// a folder it cannot write, or a macOS application. The update is
	// downloaded and verified; a person puts it in place.
	SelfManual SelfMode = "manual"
	// SelfDevelopment: not a release bundle (no bundle manifest, a program
	// that is not the one it lists, an unstamped version). Never replaced,
	// never "downgraded" to a release; Settings offers the release by hand.
	SelfDevelopment SelfMode = "development"
)

// SelfInstall is what this process knows about the bundle it runs from.
type SelfInstall struct {
	Version    string `json:"version"`
	Commit     string `json:"commit,omitempty"`
	Executable string `json:"executable"`
	// Root is the bundle's folder: the one the release archive unpacks to.
	Root     string   `json:"root"`
	Platform string   `json:"platform"`
	Mode     SelfMode `json:"mode"`
	// Reason says why Mode is not portable.
	Reason string `json:"reason,omitempty"`
	// Members are the files the running bundle's manifest lists, relative
	// to Root, plus the manifest itself: the only files an update replaces.
	Members          []BundleMember `json:"-"`
	ExtractorVersion string         `json:"extractor_version,omitempty"`
}

// BundleMember is one file of a release bundle.
type BundleMember struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Executable bool   `json:"executable,omitempty"`
}

// BundlePlatform is a release bundle's name for a platform: linux-amd64,
// windows-amd64, darwin-arm64 (Go's names, unlike the toolchains').
func BundlePlatform(goos, goarch string) string { return goos + "-" + goarch }

// BundleLayout is where a bundle for platform keeps its manifest and its
// Companion, relative to the bundle's root.
func BundleLayout(platform string) (manifest, companion string) {
	if strings.HasPrefix(platform, "darwin-") {
		return MacAppName + "/Contents/Resources/" + BundleManifestName, MacAppName + "/Contents/MacOS/companion"
	}
	return "dependencies/" + BundleManifestName, "companion" + exeSuffix(platform)
}

type bundleManifest struct {
	Schema    string           `json:"schema_version"`
	Product   string           `json:"product"`
	Version   string           `json:"version"`
	Platform  string           `json:"platform"`
	Companion string           `json:"companion"`
	Members   []bundleMember   `json:"members"`
	Extractor *bundleExtractor `json:"extractor"`
}

type bundleMember struct {
	Path     string `json:"path"`
	Product  string `json:"product"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

type bundleExtractor struct {
	Product      string `json:"product"`
	Version      string `json:"version"`
	Platform     string `json:"platform"`
	File         string `json:"file"`
	SourceCommit string `json:"source_commit"`
}

const companionProduct = "auto-pigeon-companion"

// parseBundleManifest checks a bundle manifest's shape and returns its
// members with bare hex digests. It does not look at any file.
func parseBundleManifest(raw []byte, version, platform string) (*bundleManifest, []BundleMember, error) {
	var manifest bundleManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, nil, fmt.Errorf("its %s is not JSON: %v", BundleManifestName, err)
	}
	manifestPath, companion := BundleLayout(platform)
	switch {
	case !contains(Self.ArchiveSchemas, manifest.Schema):
		return nil, nil, fmt.Errorf("its %s is %q, which this Companion cannot read", BundleManifestName, manifest.Schema)
	case manifest.Product != companionProduct:
		return nil, nil, fmt.Errorf("its %s describes %q", BundleManifestName, manifest.Product)
	case manifest.Version != version:
		return nil, nil, fmt.Errorf("its %s describes version %s, not %s", BundleManifestName, manifest.Version, version)
	case manifest.Platform != platform:
		return nil, nil, fmt.Errorf("it is a %s bundle and this computer needs %s", manifest.Platform, platform)
	case manifest.Companion != companion:
		return nil, nil, fmt.Errorf("its %s names %q as the Companion; a %s bundle has it at %q", BundleManifestName, manifest.Companion, platform, companion)
	case len(manifest.Members) == 0:
		return nil, nil, fmt.Errorf("its %s lists no files", BundleManifestName)
	case manifest.Extractor == nil || manifest.Extractor.File == "":
		return nil, nil, fmt.Errorf("its %s names no extractor: a Companion without one cannot convert a map", BundleManifestName)
	}
	var members []BundleMember
	seen := map[string]bool{}
	for _, member := range manifest.Members {
		digest, _ := strings.CutPrefix(member.SHA256, "sha256:")
		switch {
		case acquire.CheckRelativePath(member.Path) != nil:
			return nil, nil, fmt.Errorf("its %s names %q", BundleManifestName, member.Path)
		case member.Path == manifestPath:
			return nil, nil, fmt.Errorf("its %s lists itself", BundleManifestName)
		case seen[strings.ToLower(member.Path)]:
			return nil, nil, fmt.Errorf("its %s names %s twice", BundleManifestName, member.Path)
		case !hexDigest.MatchString(digest) || member.Size < 0:
			return nil, nil, fmt.Errorf("its %s gives no SHA-256 or size for %s", BundleManifestName, member.Path)
		}
		seen[strings.ToLower(member.Path)] = true
		executable := member.Path == companion || member.Path == manifest.Extractor.File ||
			strings.Contains(member.Path, "/Contents/MacOS/")
		members = append(members, BundleMember{Path: member.Path, SHA256: digest, Size: member.Size, Executable: executable})
	}
	var companionListed, extractorListed bool
	for _, member := range manifest.Members {
		switch member.Path {
		case companion:
			companionListed = member.Product == companionProduct
		case manifest.Extractor.File:
			extractorListed = member.Product == "auto-pigeon-extractor" && member.Version == manifest.Extractor.Version
		}
	}
	if !companionListed {
		return nil, nil, fmt.Errorf("its %s does not list the Companion %s", BundleManifestName, companion)
	}
	if !extractorListed {
		return nil, nil, fmt.Errorf("its %s does not list the extractor %s %s", BundleManifestName, manifest.Extractor.File, manifest.Extractor.Version)
	}
	return &manifest, members, nil
}

// InspectSelf decides what kind of installation this process runs from. It
// reads the bundle manifest beside the program and hashes the program; it
// writes nothing but one temporary file to learn whether the folder is
// writable, and removes it.
func InspectSelf(executable, version, commit, goos, goarch string) SelfInstall {
	platform := BundlePlatform(goos, goarch)
	self := SelfInstall{Version: version, Commit: commit, Platform: platform, Mode: SelfDevelopment}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	if absolute, err := filepath.Abs(executable); err == nil {
		executable = absolute
	}
	self.Executable = executable
	manifestPath, companion := BundleLayout(platform)
	self.Root = filepath.Dir(executable)
	if goos == "darwin" {
		// <root>/Auto-Pigeon Companion.app/Contents/MacOS/companion
		self.Root = filepath.Dir(filepath.Dir(filepath.Dir(self.Root)))
	}
	if _, err := parseCompanionVersion(version); err != nil {
		self.Reason = fmt.Sprintf("this is a development build (version %q), not a release", version)
		return self
	}
	if filepath.Clean(filepath.Join(self.Root, filepath.FromSlash(companion))) != filepath.Clean(executable) {
		self.Reason = "this program is not where a release bundle keeps its Companion"
		return self
	}
	raw, err := readBounded(filepath.Join(self.Root, filepath.FromSlash(manifestPath)), maxManifestBytes)
	if err != nil {
		self.Reason = "there is no readable " + BundleManifestName + " beside this program: " + err.Error()
		return self
	}
	manifest, members, err := parseBundleManifest(raw, version, platform)
	if err != nil {
		self.Reason = "the bundle beside this program is not a release bundle: " + err.Error()
		return self
	}
	for _, member := range members {
		if member.Path != companion {
			continue
		}
		actual, err := fileSHA256(executable)
		if err != nil || actual != member.SHA256 {
			self.Reason = "this program is not the one its bundle manifest lists"
			return self
		}
	}
	sum := sha256.Sum256(raw)
	self.Members = append(members, BundleMember{Path: manifestPath, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(raw))})
	self.ExtractorVersion = manifest.Extractor.Version
	switch {
	case goos == "darwin":
		self.Mode = SelfManual
		self.Reason = "a macOS application is replaced by moving the new one into place yourself"
	case !writable(self.Root) || !writable(filepath.Join(self.Root, "dependencies")):
		self.Mode = SelfManual
		self.Reason = "the Companion cannot write to its own folder " + self.Root
	default:
		self.Mode, self.Reason = SelfPortable, ""
	}
	return self
}

func writable(dir string) bool {
	file, err := os.CreateTemp(dir, ".aucom-write-check-*")
	if err != nil {
		return false
	}
	name := file.Name()
	file.Close()
	return os.Remove(name) == nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, limit)
	}
	return raw, nil
}

// FileSHA256 is the hex SHA-256 of a file.
func FileSHA256(path string) (string, error) { return fileSHA256(path) }

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// --- eligibility ---------------------------------------------------------------

type companionReleaseManifest struct {
	Schema     string `json:"schema"`
	Product    string `json:"product"`
	Version    string `json:"version"`
	Tag        string `json:"tag"`
	Commit     string `json:"commit"`
	Prerelease *bool  `json:"prerelease"`
	Signed     bool   `json:"signed"`
	AUE        *struct {
		Version  string `json:"version"`
		Protocol string `json:"protocol"`
		Commit   string `json:"commit"`
		License  string `json:"license"`
	} `json:"aue"`
	Targets []struct {
		Target     string `json:"target"`
		Verdict    string `json:"verdict"`
		Reason     string `json:"reason"`
		State      string `json:"aucom_state"`
		NativeInCI bool   `json:"native_acceptance_in_ci"`
		Archive    string `json:"archive"`
		Size       int64  `json:"size"`
		SHA256     string `json:"sha256"`
	} `json:"targets"`
}

// companionCandidate reads one Companion release's manifest and decides
// whether this machine can take its bundle.
func (e evaluator) companionCandidate(ctx context.Context, release Release, version string) (*Candidate, Reason, string) {
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
	var manifest companionReleaseManifest
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&manifest); err != nil {
		return nil, ReasonManifestUnreadable, "the release manifest is not JSON: " + err.Error()
	}
	if !contains(e.tool.ManifestSchemas, manifest.Schema) {
		return nil, ReasonManifestUnsupported, fmt.Sprintf("its manifest is %q, which this Companion cannot read; "+
			"download it by hand from %s", manifest.Schema, e.tool.ReleasesURL())
	}
	switch {
	case manifest.Product != companionProduct:
		return nil, ReasonManifestMismatch, fmt.Sprintf("the manifest describes %q", manifest.Product)
	case manifest.Tag != release.Tag:
		return nil, ReasonManifestMismatch, fmt.Sprintf("the manifest names the tag %q, the release is %q", manifest.Tag, release.Tag)
	case manifest.Version != version:
		return nil, ReasonManifestMismatch, fmt.Sprintf("the manifest names the version %q, the tag says %q", manifest.Version, version)
	case manifest.Prerelease != nil && *manifest.Prerelease != release.Prerelease:
		return nil, ReasonManifestMismatch, "the manifest and GitHub disagree about whether this is a prerelease"
	case manifest.AUE == nil || manifest.AUE.Version == "":
		return nil, ReasonNoSidecar, "the release names no bundled extractor"
	case !strings.HasPrefix(manifest.AUE.Protocol, "1."):
		return nil, ReasonManifestUnsupported, fmt.Sprintf("its extractor speaks protocol %q, which this Companion does not know", manifest.AUE.Protocol)
	}
	slash := strings.Replace(e.target, "-", "/", 1)
	for _, target := range manifest.Targets {
		if target.Target != slash {
			continue
		}
		digest, _ := strings.CutPrefix(target.SHA256, "sha256:")
		switch {
		case target.Verdict != "bundled_release":
			return nil, ReasonNotQualified, fmt.Sprintf("the release lists its %s build as %s: %s", slash, target.Verdict, target.Reason)
		case target.Archive == "" || !hexDigest.MatchString(digest) || target.Size <= 0:
			return nil, ReasonNoDigest, fmt.Sprintf("the manifest gives no archive, SHA-256 and size for %s", slash)
		}
		archive, carried := release.asset(target.Archive)
		if !carried {
			return nil, ReasonNoTarget, fmt.Sprintf("the manifest names %s and the release does not carry it", target.Archive)
		}
		if archive.Size != target.Size {
			return nil, ReasonDigestDisagrees, fmt.Sprintf("GitHub lists %s as %d bytes; the manifest says %d", target.Archive, archive.Size, target.Size)
		}
		github, hasDigest := githubDigest(archive)
		if hasDigest && github != digest {
			return nil, ReasonDigestDisagrees, fmt.Sprintf("GitHub's digest of %s is not the manifest's", target.Archive)
		}
		ci := "not run in CI"
		if target.NativeInCI {
			ci = "passed in CI on a hosted runner"
		}
		candidate := &Candidate{
			Tool:           e.tool.ID,
			Tag:            release.Tag,
			Version:        version,
			Prerelease:     release.Prerelease,
			ReleaseID:      release.ID,
			NotesURL:       e.tool.ReleasesURL() + "/tag/" + release.Tag,
			Published:      release.PublishedAt,
			Banner:         version,
			Commit:         manifest.Commit,
			Target:         e.target,
			Asset:          target.Archive,
			AssetID:        archive.ID,
			Size:           target.Size,
			SHA256:         digest,
			ManifestSHA256: manifestDigest,
			Qualification: fmt.Sprintf("the release bundles its %s Companion with extractor %s; native acceptance %s; "+
				"this platform's support state in that release: %s", slash, manifest.AUE.Version, ci, target.State),
			Signed: manifest.Signed,
			Sidecar: &Sidecar{Version: manifest.AUE.Version, Protocol: manifest.AUE.Protocol,
				Commit: manifest.AUE.Commit, License: manifest.AUE.License},
			PlatformState: target.State,
		}
		if hasDigest {
			candidate.GitHubDigest = "sha256:" + github
		}
		return candidate, ReasonEligible, ""
	}
	return nil, ReasonNoTarget, fmt.Sprintf("the release publishes no %s build", slash)
}

// --- the staged bundle ---------------------------------------------------------

// unpackedBundle is what [unpackBundle] verified.
type unpackedBundle struct {
	programs map[string]string // name -> path relative to the bundle root
	members  []BundleMember
}

// unpackBundle unpacks a verified Companion archive into destination and
// checks every file against the bundle manifest inside it, and that manifest
// against the release's: the right Companion, the extractor the release
// names, and nothing the manifest does not list.
func unpackBundle(archivePath, destination string, candidate Candidate, limits Limits) (*unpackedBundle, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, refuse("it is not a readable zip archive: %v", err)
	}
	defer reader.Close()
	manifestPath, companion := BundleLayout(candidate.Target)
	_, entries, manifestFile, err := scanArchive(reader, limits, manifestPath)
	if err != nil {
		return nil, err
	}
	if manifestFile == nil {
		return nil, refuse("it carries no %s, so nothing pairs the Companion with its extractor", manifestPath)
	}
	if manifestFile.UncompressedSize64 > maxManifestBytes {
		return nil, refuse("its %s is larger than %d bytes", BundleManifestName, maxManifestBytes)
	}
	opened, err := manifestFile.Open()
	if err != nil {
		return nil, refuse("its %s cannot be read: %v", BundleManifestName, err)
	}
	raw, err := io.ReadAll(io.LimitReader(opened, maxManifestBytes+1))
	opened.Close()
	if err != nil {
		return nil, refuse("its %s cannot be read: %v", BundleManifestName, err)
	}
	manifest, members, err := parseBundleManifest(raw, candidate.Version, candidate.Target)
	if err != nil {
		return nil, refuse("%v", err)
	}
	if sidecar := candidate.Sidecar; sidecar != nil {
		switch {
		case manifest.Extractor.Version != sidecar.Version:
			return nil, refuse("it bundles extractor %s and its release names %s", manifest.Extractor.Version, sidecar.Version)
		case sidecar.Commit != "" && manifest.Extractor.SourceCommit != "" && manifest.Extractor.SourceCommit != sidecar.Commit:
			return nil, refuse("its extractor was built from %s and its release names %s", manifest.Extractor.SourceCommit, sidecar.Commit)
		}
	}
	listed := map[string]BundleMember{}
	for _, member := range members {
		listed[member.Path] = member
	}
	present := map[string]bool{}
	for _, item := range entries {
		if item.dir {
			continue
		}
		if _, ok := listed[item.relative]; !ok {
			return nil, refuse("it contains %s, which its bundle manifest does not list", item.relative)
		}
		present[item.relative] = true
	}
	for name := range listed {
		if !present[name] {
			return nil, refuse("its bundle manifest lists %s and the archive does not contain it", name)
		}
	}
	for _, item := range entries {
		target := filepath.Join(destination, filepath.FromSlash(item.relative))
		if !within(destination, target) {
			return nil, refuse("%s resolves outside the bundle", item.relative)
		}
		if item.dir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return nil, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		member := listed[item.relative]
		mode := "644"
		if member.Executable {
			mode = "755"
		}
		if err := writeEntry(item.file, target, archiveFile{Path: member.Path, SHA256: member.SHA256, Size: member.Size, Mode: mode}, limits); err != nil {
			return nil, err
		}
	}
	manifestTarget := filepath.Join(destination, filepath.FromSlash(manifestPath))
	if err := os.MkdirAll(filepath.Dir(manifestTarget), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(manifestTarget, raw, 0o644); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	members = append(members, BundleMember{Path: manifestPath, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(raw))})
	return &unpackedBundle{
		programs: map[string]string{"companion": companion, "auto-pigeon-extractor": manifest.Extractor.File},
		members:  members,
	}, nil
}

// --- the coordinator's part ------------------------------------------------------

// SelfHost is what the Companion around the coordinator does for its own
// update: prove a staged bundle, say what is running, and begin a restart.
type SelfHost interface {
	// Prove runs the staged Companion's proving start: its version, its own
	// extractor's handshake, this configuration read, a loopback listener.
	// Bounded, and it changes nothing.
	Prove(ctx context.Context, record InstallRecord) error
	// SelfBusy names everything running that replacing the bundle could
	// break: builds, conversions, downloads, running games. Empty is idle.
	SelfBusy() []string
	// RestartForUpdate closes admission, checks again that nothing runs and
	// stops the process so it can be replaced and started again. It returns
	// what is running instead, when something started meanwhile.
	RestartForUpdate(record InstallRecord) ([]string, error)
}

// SelfResult is the last time this Companion's bundle was replaced, or a
// replacement was refused or undone.
type SelfResult struct {
	From    string    `json:"from"`
	To      string    `json:"to"`
	Outcome string    `json:"outcome"` // updated, rolled_back, failed
	Detail  string    `json:"detail,omitempty"`
	At      time.Time `json:"at"`
}

// SelfView is the Companion's own row, beyond what a toolchain's says.
type SelfView struct {
	SelfInstall
	// Manual is the newest release for a Companion that is not updated
	// automatically: a development build, offered only by hand.
	Manual *Candidate `json:"manual,omitempty"`
	// Last is the last replacement.
	Last *SelfResult `json:"last,omitempty"`
}

func (m *Manager) targets() []Tool {
	if m.opts.Self == nil {
		return Tools
	}
	return append(append([]Tool(nil), Tools...), Self)
}

// SelfInstall is what this coordinator knows of the running bundle.
func (m *Manager) SelfInstall() (SelfInstall, bool) {
	if m.opts.Self == nil {
		return SelfInstall{}, false
	}
	return *m.opts.Self, true
}

// selfTool is [Self] with the running version as its baseline: nothing older
// is ever a candidate.
func (m *Manager) selfTool() Tool {
	tool := Self
	tool.Baseline = "1.0"
	if _, err := parseCompanionVersion(m.opts.Self.Version); err == nil {
		tool.Baseline = m.opts.Self.Version
	}
	return tool
}

// advanceSelf moves the Companion's own update toward what was chosen. It
// never installs anything unasked: there is no first install of oneself.
func (m *Manager) advanceSelf(ctx context.Context, tool Tool) error {
	self := m.opts.Self
	state, err := LoadState(m.opts.StatePath)
	if err != nil {
		return err
	}
	ts := state.Tool(tool.ID)
	best := ts.Check.Best
	if ts.Intent == IntentNone || ts.IntentFor == nil {
		m.setLive(tool.ID, func(l *liveState) { l.Waiting = nil })
		return m.pruneSelf(tool, ts)
	}
	if self.Mode == SelfDevelopment || best == nil || best.Identity() != *ts.IntentFor || !m.newer(tool, best.Version, self.Version) {
		_, err := UpdateState(m.opts.StatePath, func(state *State) error {
			current := state.Tool(tool.ID)
			current.Intent, current.IntentFor = IntentNone, nil
			return nil
		})
		return err
	}
	if m.liveOf(tool.ID).Phase == "restarting" {
		return nil
	}
	staged, isStaged := ts.installed(*ts.IntentFor)
	if !isStaged {
		now := m.now()
		if ts.Failure != nil && ts.Failure.Version == best.Version && now.Before(ts.Failure.NextAttempt) {
			return nil
		}
		record, err := m.installVerified(ctx, tool, *best)
		if err != nil {
			return m.recordFailure(tool, best.Version, err)
		}
		staged = record.Ref()
	}
	if self.Mode != SelfPortable || ts.Intent != IntentNow || m.opts.SelfHost == nil {
		return nil
	}
	record, err := ReadInstall(staged.Dir)
	if err != nil {
		return m.recordFailure(tool, staged.Version, err)
	}
	// A toolchain this coordinator is installing, or was asked to switch
	// now, is work too: the restart waits for it rather than cutting a
	// download off (found live, NEW_368).
	if busy := m.toolWork(); len(busy) > 0 {
		m.setLive(tool.ID, func(l *liveState) { l.Waiting = busy; l.Version = staged.Version })
		return nil
	}
	busy, err := m.opts.SelfHost.RestartForUpdate(record)
	switch {
	case len(busy) > 0:
		m.setLive(tool.ID, func(l *liveState) { l.Waiting = busy; l.Version = staged.Version })
		return nil
	case err != nil:
		return m.recordFailure(tool, staged.Version, err)
	}
	m.setLive(tool.ID, func(l *liveState) { l.Waiting, l.Phase, l.Version = nil, "restarting", staged.Version })
	m.opts.Logf("self update: restarting to replace %s with %s (%s)", self.Version, staged.Version, staged.Dir)
	return nil
}

// toolWork names the toolchain installs and switches in progress.
func (m *Manager) toolWork() []string {
	var out []string
	state, _ := LoadState(m.opts.StatePath)
	for _, tool := range Tools {
		live := m.liveOf(tool.ID)
		switch live.Phase {
		case "downloading", "verifying", "installed":
			out = append(out, fmt.Sprintf("installing %s %s", tool.Title, live.Version))
			continue
		}
		if state == nil {
			continue
		}
		ts := state.Tool(tool.ID)
		if ts.Intent == IntentNow && ts.IntentFor != nil && (ts.Failure == nil || ts.Failure.Version != ts.IntentFor.Version) {
			out = append(out, fmt.Sprintf("updating %s to %s", tool.Title, ts.IntentFor.Version))
		}
	}
	return out
}

// pruneSelf removes staged bundles nothing chose any more.
func (m *Manager) pruneSelf(tool Tool, ts *ToolState) error {
	if len(ts.Installed) == 0 {
		return nil
	}
	toolDir := m.installer.toolDir(tool)
	var removed []string
	for _, installed := range ts.Installed {
		if !within(toolDir, installed.Dir) || filepath.Clean(installed.Dir) == filepath.Clean(toolDir) {
			continue
		}
		if err := os.RemoveAll(installed.Dir); err != nil {
			m.opts.Logf("self update: removing %s: %v", installed.Dir, err)
			continue
		}
		removed = append(removed, installed.Dir)
	}
	if len(removed) == 0 {
		return nil
	}
	_, err := UpdateState(m.opts.StatePath, func(state *State) error {
		current := state.Tool(tool.ID)
		kept := current.Installed[:0]
		for _, installed := range current.Installed {
			if !contains(removed, installed.Dir) {
				kept = append(kept, installed)
			}
		}
		current.Installed = kept
		return nil
	})
	return err
}

// SelfAtExit is the staged bundle to put in place as this process ends: the
// one chosen for the close, or — restart true — the one an "Update now" is
// restarting for. Only a portable bundle is replaced.
func (m *Manager) SelfAtExit(restart bool) (InstallRecord, bool) {
	if m.opts.Self == nil || m.opts.Self.Mode != SelfPortable {
		return InstallRecord{}, false
	}
	m.workMu.Lock()
	defer m.workMu.Unlock()
	state, err := LoadState(m.opts.StatePath)
	if err != nil {
		return InstallRecord{}, false
	}
	ts := state.Tool(Companion)
	want := IntentOnExit
	if restart {
		want = IntentNow
	}
	if ts.Intent != want || ts.IntentFor == nil {
		return InstallRecord{}, false
	}
	staged, ok := ts.installed(*ts.IntentFor)
	if !ok || !m.newer(Self, staged.Version, m.opts.Self.Version) {
		return InstallRecord{}, false
	}
	record, err := ReadInstall(staged.Dir)
	return record, err == nil
}

// RecordSelfResult records how a replacement ended. Either way the choice is
// over: a candidate that failed is never applied again by itself, at the
// next close or the next start; it is offered again, with its failure shown.
func (m *Manager) RecordSelfResult(result SelfResult) error {
	_, err := UpdateState(m.opts.StatePath, func(state *State) error {
		ts := state.Tool(Companion)
		copied := result
		ts.SelfLast = &copied
		ts.Intent, ts.IntentFor = IntentNone, nil
		if result.Outcome == "updated" {
			ts.Failure, ts.Dismissed = nil, nil
			return nil
		}
		ts.Failure = &SetupFailure{Version: result.To, Error: result.Detail, At: result.At, Attempts: 1, NextAttempt: result.At}
		return nil
	})
	return err
}

// selfStatus is the Companion's own row.
func (m *Manager) selfStatus(ts *ToolState, live liveState, now time.Time) ToolStatus {
	self := *m.opts.Self
	tool := m.selfTool()
	view := ToolStatus{
		ID: Companion, Kind: KindApplication, Title: tool.Title, ReleasesURL: tool.ReleasesURL(),
		Newer: ts.Check.Newer, Skipped: ts.Skipped, Integrity: ts.Integrity, Failure: ts.Failure,
		Check: CheckView{
			LastAttempt: ts.Check.LastAttempt, LastSuccess: ts.Check.LastSuccess,
			NextAttempt: ts.Check.NextAttempt, LastError: ts.Check.LastError,
			RateLimitedUntil: ts.Check.RateLimitedUntil,
		},
		Phase: PhaseReady, Version: self.Version,
		Active: &InstallRecord{Tool: Companion, Version: self.Version, Commit: self.Commit, Dir: self.Root,
			Target: self.Platform, Repository: tool.Owner + "/" + tool.Repo},
		Self: &SelfView{SelfInstall: self, Last: ts.SelfLast},
	}
	switch live.Phase {
	case "checking":
		view.Phase = PhaseChecking
	case "downloading", "installed":
		view.Phase, view.Version, view.Done, view.Total = PhaseDownloading, live.Version, live.Done, live.Total
	case "verifying":
		view.Phase, view.Version = PhaseVerifying, live.Version
	}
	best := ts.Check.Best
	if best == nil {
		return view
	}
	if self.Mode == SelfDevelopment {
		copied := *best
		view.Self.Manual = &copied
		return view
	}
	if !m.newer(tool, best.Version, self.Version) {
		return view
	}
	identity := best.Identity()
	switch {
	case ts.Intent != IntentNone && ts.IntentFor != nil && *ts.IntentFor == identity:
		staged, isStaged := ts.installed(identity)
		view.Pending = &Pending{Intent: ts.Intent, Version: best.Version, Staged: isStaged, NotesURL: best.NotesURL,
			Restarting: live.Phase == "restarting"}
		if ts.Intent == IntentNow {
			view.Pending.Waiting = live.Waiting
		}
		if isStaged && self.Mode == SelfManual {
			view.Pending.Manual = fmt.Sprintf("Version %s is downloaded and verified in %s. %s. Close the Companion, "+
				"then replace the contents of %s with that folder's contents (or unzip %s from the release page there).",
				best.Version, staged.Dir, upperFirst(self.Reason), self.Root, best.Asset)
		}
	case ts.Integrity != nil && ts.Integrity.Seen == identity:
	case ts.skipped(identity):
	case ts.Dismissed != nil && ts.Dismissed.BytesIdentity == identity &&
		ts.Dismissed.Session == m.session && now.Sub(ts.Dismissed.At) < dismissalWindow:
	default:
		copied := *best
		view.Offer = &copied
	}
	return view
}

func upperFirst(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

// errNoSelf is a self-update call to a coordinator without a running bundle.
var errNoSelf = errors.New("managedtools: this coordinator does not update the Companion")

// FreeBytes is how many bytes may be written in dir; -1 is unknown.
func FreeBytes(dir string) int64 { return freeBytes(dir) }
