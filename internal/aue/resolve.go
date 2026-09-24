package aue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// RequiredProtocol is the invocation protocol this build drives: the bundled
// extractor must speak the same major and at least this minor.
const RequiredProtocol = "1.0"

// BundleManifestName is the file a release bundle lists its members in, with
// each one's SHA-256 (build/bundle-manifest.py).
const BundleManifestName = "bundle-manifest.json"

// ExecutableName is the bundled extractor's file name on a platform.
func ExecutableName(platform profile.Platform) string {
	return "auto-pigeon-extractor" + platform.ExeSuffix()
}

// Resolver finds the extractor this Companion may run.
type Resolver struct {
	// Override is the developer override path. Empty means none; when it is
	// set, Resolve returns an override runner and looks nowhere else.
	Override string

	// Dir is where the bundled extractor is looked for. Empty means the
	// directory this program's own executable is in, which is where a release
	// bundle puts it.
	Dir string

	// Platform is the machine. Zero means the running one.
	Platform profile.Platform
}

func (r *Resolver) platform() profile.Platform {
	if !r.Platform.Zero() {
		return r.Platform
	}

	return profile.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

func (r *Resolver) dir() (string, error) {
	if strings.TrimSpace(r.Dir) != "" {
		return r.Dir, nil
	}
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("aue: finding this program's own directory: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	return filepath.Dir(self), nil
}

// BundledPath is where a bundled extractor would be.
func (r *Resolver) BundledPath() (string, error) {
	dir, err := r.dir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, ExecutableName(r.platform())), nil
}

// Status is what this Companion can say about its extractor without running it.
type Status struct {
	Mode      string `json:"mode"`
	Available bool   `json:"available"`
	Verified  bool   `json:"verified"`
	Path      string `json:"path,omitempty"`
	// Reason says why nothing is available, and is empty when something is.
	Reason string `json:"reason,omitempty"`
	// Note is the warning on an override or an unlisted bundled copy.
	Note string `json:"note,omitempty"`
}

// Status answers without running anything.
func (r *Resolver) Status() Status {
	if path := strings.TrimSpace(r.Override); path != "" {
		status := Status{Mode: ModeDeveloperOverride, Available: true, Path: path, Note: UnverifiedNote}
		if err := checkExecutable(path); err != nil {
			status.Available, status.Reason = false, err.Error()
		}

		return status
	}
	path, err := r.BundledPath()
	status := Status{Mode: ModeBundled, Path: path}
	if err == nil {
		err = checkExecutable(path)
	}
	if err != nil {
		status.Reason = fmt.Sprintf("no extractor was shipped beside this Companion (%s); a development build "+
			"can name one with %s", lookedFor(path, err), EnvBinaryOverride)

		return status
	}
	digest, listed, err := r.listedDigest(path)
	switch {
	case err != nil:
		status.Reason = err.Error()
	case listed == "":
		status.Available, status.Note = true, UnlistedNote
	case listed != digest:
		status.Reason = mismatch(path, listed, digest)
	default:
		status.Available, status.Verified = true, true
	}

	return status
}

// Resolve produces a runner for an executable this Companion may run.
//
// # The two ways, and the absence of a third
//
// A developer override short-circuits everything: nothing is looked up, no
// handshake is made, and the runner it returns says it is unverified. It is
// taken only because the user set an environment variable, and never as a
// consequence of something else failing.
//
// Otherwise the extractor is the one shipped beside this program. When the
// release's bundle manifest lists it, its bytes must hash to the listed digest;
// and whether listed or not, it is asked what protocol it speaks before it is
// handed to anybody. Nothing is downloaded, ever (operator, 2026-09-23).
func (r *Resolver) Resolve(ctx context.Context) (*ProcessRunner, error) {
	if path := strings.TrimSpace(r.Override); path != "" {
		if err := checkExecutable(path); err != nil {
			return nil, err
		}

		return NewOverrideRunner(path), nil
	}

	path, err := r.BundledPath()
	if err != nil {
		return nil, err
	}
	if err := checkExecutable(path); err != nil {
		return nil, fmt.Errorf("%w (%s)", ErrNoExtractor, lookedFor(path, err))
	}
	digest, listed, err := r.listedDigest(path)
	if err != nil {
		return nil, err
	}
	provenance := Provenance{Mode: ModeBundled, Path: path, MinProtocol: RequiredProtocol}
	switch {
	case listed == "":
		provenance.Note = UnlistedNote
	case listed != digest:
		return nil, errors.New(mismatch(path, listed, digest))
	default:
		provenance.Verified, provenance.Digest = true, digest
	}

	runner := &ProcessRunner{path: path, provenance: provenance}
	if err := handshake(ctx, runner, RequiredProtocol); err != nil {
		return nil, err
	}

	return runner, nil
}

// lookedFor says where the bundled extractor was expected, and — when a file is
// there but cannot be run — why it was not used.
func lookedFor(path string, err error) string {
	if _, statErr := os.Lstat(path); errors.Is(statErr, fs.ErrNotExist) {
		return "looked for " + path + ", where a release bundle carries it"
	}

	return err.Error()
}

func mismatch(path, listed, digest string) string {
	return fmt.Sprintf("aue: %s hashes to %s and this release's bundle manifest lists %s; it is not the "+
		"extractor this release shipped, and it is not run", path, digest, listed)
}

// bundleManifest is the part of build/bundle-manifest.py's document read here.
type bundleManifest struct {
	Members []struct {
		Path    string `json:"path"`
		Product string `json:"product"`
		SHA256  string `json:"sha256"`
	} `json:"members"`
}

// bundleManifestFor says where a release's bundle manifest is for an extractor
// at path, and the member path it lists the extractor under.
//
// Beside the executable on Linux and Windows. On macOS the Companion runs from
// `<Name>.app/Contents/MacOS/`, so that is where its extractor is too, and the
// manifest — whose member paths are relative to the archive root — is in
// `<Name>.app/Contents/Resources/`, inside the app so that dragging the app to
// /Applications keeps it verifiable (NEW_247A: before this, a macOS bundle put
// both at the archive root, where a Companion in a .app never looked).
func bundleManifestFor(path string) (manifest, member string) {
	dir := filepath.Dir(path)
	contents := filepath.Dir(dir)
	app := filepath.Dir(contents)
	if filepath.Base(dir) == "MacOS" && filepath.Base(contents) == "Contents" &&
		strings.HasSuffix(strings.ToLower(app), ".app") {
		return filepath.Join(contents, "Resources", BundleManifestName),
			filepath.Base(app) + "/Contents/MacOS/" + filepath.Base(path)
	}

	return filepath.Join(dir, BundleManifestName), filepath.Base(path)
}

// listedDigest returns the executable's digest and the digest the bundle
// manifest lists for it ("" when there is no manifest or it does not list
// the file). A manifest that is there and unreadable is an error: a release
// whose inventory cannot be read is not one to vouch for.
func (r *Resolver) listedDigest(path string) (digest, listed string, err error) {
	manifestPath, name := bundleManifestFor(path)
	data, err := os.ReadFile(manifestPath)
	if errors.Is(err, fs.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("aue: reading the bundle manifest: %w", err)
	}
	var manifest bundleManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", "", fmt.Errorf("aue: the bundle manifest beside this Companion is not readable: %w", err)
	}
	for _, member := range manifest.Members {
		if member.Path == name {
			listed = member.SHA256
		}
	}
	if listed == "" {
		return "", "", nil
	}
	digest, err = FileDigest(path)

	return digest, listed, err
}

// FileDigest is `sha256:<hex>` over a file's bytes, the form a bundle manifest
// lists.
func FileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// protocolDocument is the part of the extractor's `protocol --json` this
// Companion reads.
//
// A transcription, because the extractor is a separate module and neither
// imports the other. Unknown members are IGNORED rather than refused: a minor
// protocol bump may add a field, and refusing one would make every additive
// change breaking, which is exactly what the major/minor split exists to avoid.
type protocolDocument struct {
	SchemaVersion string `json:"schema_version"`
	Protocol      string `json:"protocol"`
	Version       string `json:"version"`
	License       struct {
		SPDX                string `json:"spdx"`
		CorrespondingSource string `json:"corresponding_source"`
	} `json:"license"`
}

// handshake asks the executable what contract it speaks and refuses it if this
// build cannot drive it.
func handshake(ctx context.Context, runner *ProcessRunner, minProtocol string) error {
	var document protocolDocument
	if err := runner.RunJSON(ctx, &document, "protocol", "--json"); err != nil {
		return fmt.Errorf("aue: asking %s which invocation protocol it speaks: %w", ProgramName, err)
	}
	if strings.TrimSpace(document.Protocol) == "" {
		return fmt.Errorf("aue: %s declares no invocation protocol, so nothing can tell whether this "+
			"Companion may drive it", ProgramName)
	}
	if err := ProtocolSatisfies(document.Protocol, minProtocol); err != nil {
		return fmt.Errorf("aue: %s %s: %w", ProgramName, document.Version, err)
	}
	runner.provenance.Protocol = document.Protocol
	runner.provenance.Version = document.Version
	runner.provenance.License = document.License.SPDX
	runner.provenance.Source = document.License.CorrespondingSource

	return nil
}

// Handshake performs the protocol check against an already-built runner.
//
// Exported for the override path, where it is deliberately NOT automatic: an
// override exists so somebody can drive a build that does not exist yet, and
// refusing to run it for speaking tomorrow's protocol would defeat the only
// reason it exists. A caller that wants the check asks for it.
func Handshake(ctx context.Context, runner *ProcessRunner, minProtocol string) error {
	return handshake(ctx, runner, minProtocol)
}

// ProtocolSatisfies reports whether a build's declared protocol meets a
// required minimum: the majors must be EQUAL and the build's minor at least
// the required one. Not ">= major", because a later major is defined as
// breaking — running one would be running against a contract that has been
// replaced.
func ProtocolSatisfies(speaks, minimum string) error {
	speaksMajor, speaksMinor, err := parseProtocol(speaks)
	if err != nil {
		return err
	}
	wantMajor, wantMinor, err := parseProtocol(minimum)
	if err != nil {
		return err
	}
	switch {
	case speaksMajor != wantMajor:
		return fmt.Errorf("this build speaks invocation protocol %s and %s is required; "+
			"major %d and major %d are different contracts, and a later one is not a newer version of an earlier one",
			speaks, minimum, speaksMajor, wantMajor)
	case speaksMinor < wantMinor:
		return fmt.Errorf("this build speaks invocation protocol %s and %s is required", speaks, minimum)
	}

	return nil
}

// parseProtocol reads `major.minor`, two non-negative integers and nothing else.
func parseProtocol(text string) (major, minor int, err error) {
	parts := strings.Split(strings.TrimSpace(text), ".")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%q is not a protocol version (want major.minor)", text)
	}
	for index, part := range parts {
		number, convErr := strconv.Atoi(part)
		if convErr != nil || number < 0 || part != strconv.Itoa(number) {
			return 0, 0, fmt.Errorf("%q is not a protocol version (want major.minor)", text)
		}
		if index == 0 {
			major = number
		} else {
			minor = number
		}
	}

	return major, minor, nil
}

// checkExecutable is the last thing before an exec: the path is a real, regular
// file and the operating system will run it.
func checkExecutable(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("aue: %s is not there", path)
		}

		return fmt.Errorf("aue: checking %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("aue: %s is not a regular file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("aue: %s is not executable", path)
	}

	return nil
}
