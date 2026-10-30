package converter

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
	"slices"
	"strconv"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// RequiredProtocol is the invocation protocol this build drives: the bundled
// converter must speak the same major and at least this minor.
const RequiredProtocol = "1.0"

// ExecutableBase is the converter's program name, and what it must call itself in the handshake.
const ExecutableBase = "auto-pigeon-mapio"

// RequiredDirections are the conversions a build runs. A converter that does not offer all of them is
// refused in the handshake, before a map is ever handed to it.
var RequiredDirections = []string{"apmap-to-q1map", "apmap-to-q2map", "apmap-to-q3map"}

// BundleManifestName is the file a release bundle lists its members in, with
// each one's SHA-256 (build/bundle-manifest.py).
const BundleManifestName = "bundle-manifest.json"

// DependenciesDir is the folder beside the Companion's executable that a
// Linux or Windows release bundle puts the converter and its bundle manifest
// in, so the archive root holds only the program a person starts (operator
// decision, 2026-09-25). A Companion inside a macOS .app looks beside itself in
// Contents/MacOS, as before. There is no fallback between the two: the
// Companion runs beside the layout its own release built.
const DependenciesDir = "dependencies"

// ExecutableName is the bundled converter's file name on a platform.
func ExecutableName(platform profile.Platform) string {
	return ExecutableBase + platform.ExeSuffix()
}

// Resolver finds the converter this Companion may run.
type Resolver struct {
	// Override is the developer override path. Empty means none; when it is
	// set, Resolve returns an override runner and looks nowhere else.
	Override string

	// Dir is the directory the Companion's executable is in; the bundled
	// converter is looked for in its DependenciesDir, or beside it inside a
	// macOS .app. Empty means this program's own directory.
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
		return "", fmt.Errorf("converter: finding this program's own directory: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	return filepath.Dir(self), nil
}

// BundledPath is where a bundled converter would be.
func (r *Resolver) BundledPath() (string, error) {
	dir, err := r.dir()
	if err != nil {
		return "", err
	}

	if inMacApp(dir) {
		return filepath.Join(dir, ExecutableName(r.platform())), nil
	}

	return filepath.Join(dir, DependenciesDir, ExecutableName(r.platform())), nil
}

// inMacApp reports whether dir is a macOS app bundle's Contents/MacOS.
func inMacApp(dir string) bool {
	contents := filepath.Dir(dir)

	return filepath.Base(dir) == "MacOS" && filepath.Base(contents) == "Contents" &&
		strings.HasSuffix(strings.ToLower(filepath.Dir(contents)), ".app")
}

// Status is what this Companion can say about its converter without running it.
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
		status.Reason = fmt.Sprintf("no converter was shipped beside this Companion (%s); a development build "+
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
// Otherwise the converter is the one shipped beside this program. When the
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
	r.restoreBridgeMode(path)
	if err := checkExecutable(path); err != nil {
		return nil, fmt.Errorf("%w (%s)", ErrNoConverter, lookedFor(path, err))
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

// restoreBridgeMode gives a migration bridge's converter back its execute bit. An older updater marks
// executable only the programs its own schema names, so it writes this one as a plain file, and nothing
// else will ever put the bit back. Only after the bytes are the ones the bridge's manifest lists; any
// failure leaves the file as it is, for checkExecutable to report.
func (r *Resolver) restoreBridgeMode(path string) {
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 != 0 {
		return
	}
	manifestPath, _ := bundleManifestFor(path)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return
	}
	var manifest bundleManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.Bridge == nil || manifest.Bridge.Kind != BridgeKind {
		return
	}
	if digest, listed, err := r.listedDigest(path); err == nil && listed != "" && digest == listed {
		_ = os.Chmod(path, 0o755)
	}
}

// lookedFor says where the bundled converter was expected, and — when a file is
// there but cannot be run — why it was not used.
func lookedFor(path string, err error) string {
	if _, statErr := os.Lstat(path); errors.Is(statErr, fs.ErrNotExist) {
		return "looked for " + path + ", where a release bundle carries it"
	}

	return err.Error()
}

func mismatch(path, listed, digest string) string {
	return fmt.Sprintf("converter: %s hashes to %s and this release's bundle manifest lists %s; it is not the "+
		"converter this release shipped, and it is not run", path, digest, listed)
}

// BridgeKind names a migration bridge's manifest (ADR-0011).
const BridgeKind = "aucom.migration-bridge/1"

// bundleManifest is the part of build/bundle-manifest.py's document read here.
type bundleManifest struct {
	Bridge *struct {
		Kind string `json:"kind"`
	} `json:"migration_bridge"`
	Members []struct {
		Path    string `json:"path"`
		Product string `json:"product"`
		SHA256  string `json:"sha256"`
	} `json:"members"`
}

// bundleManifestFor says where a release's bundle manifest is for an converter
// at path, and the member path it lists the converter under.
//
// Beside the converter, in DependenciesDir, on Linux and Windows; the member
// path is relative to the archive root, so it is `dependencies/<name>`. On macOS the Companion runs from
// `<Name>.app/Contents/MacOS/`, so that is where its converter is too, and the
// manifest — whose member paths are relative to the archive root — is in
// `<Name>.app/Contents/Resources/`, inside the app so that dragging the app to
// /Applications keeps it verifiable (NEW_247A: before this, a macOS bundle put
// both at the archive root, where a Companion in a .app never looked).
func bundleManifestFor(path string) (manifest, member string) {
	dir := filepath.Dir(path)
	if inMacApp(dir) {
		contents := filepath.Dir(dir)

		return filepath.Join(contents, "Resources", BundleManifestName),
			filepath.Base(filepath.Dir(contents)) + "/Contents/MacOS/" + filepath.Base(path)
	}

	return filepath.Join(dir, BundleManifestName), DependenciesDir + "/" + filepath.Base(path)
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
		return "", "", fmt.Errorf("converter: reading the bundle manifest: %w", err)
	}
	var manifest bundleManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", "", fmt.Errorf("converter: the bundle manifest beside this Companion is not readable: %w", err)
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

// protocolDocument is the part of the converter's `protocol --json` this Companion reads.
//
// Unknown members are IGNORED rather than refused: a minor protocol bump may add a field, and refusing
// one would make every additive change breaking, which is exactly what the major/minor split exists to
// avoid.
type protocolDocument struct {
	SchemaVersion string `json:"schema_version"`
	Program       string `json:"program"`
	Protocol      string `json:"protocol"`
	Version       string `json:"version"`
	License       struct {
		SPDX   string `json:"spdx"`
		Source string `json:"source"`
	} `json:"license"`
	Library struct {
		Version string `json:"version"`
	} `json:"library"`
	Capabilities struct {
		Directions    []string `json:"directions"`
		APMapCurrent  string   `json:"apmap_current"`
		APMapReadable []string `json:"apmap_readable"`
	} `json:"capabilities"`
}

// handshake asks the executable what contract it speaks and refuses it if this
// build cannot drive it.
func handshake(ctx context.Context, runner *ProcessRunner, minProtocol string) error {
	var document protocolDocument
	if err := runner.RunJSON(ctx, &document, "protocol", "--json"); err != nil {
		return fmt.Errorf("converter: asking %s which invocation protocol it speaks: %w", ProgramName, err)
	}
	// Who answered, before what it says it speaks: another program's protocol number means nothing.
	if document.Program != ExecutableBase {
		return fmt.Errorf("converter: the program at %s calls itself %q, not %s, and it is not run",
			runner.path, document.Program, ExecutableBase)
	}
	if strings.TrimSpace(document.Protocol) == "" {
		return fmt.Errorf("converter: %s declares no invocation protocol, so nothing can tell whether this "+
			"Companion may drive it", ProgramName)
	}
	if err := ProtocolSatisfies(document.Protocol, minProtocol); err != nil {
		return fmt.Errorf("converter: %s %s: %w", ProgramName, document.Version, err)
	}
	for _, required := range RequiredDirections {
		if !slices.Contains(document.Capabilities.Directions, required) {
			return fmt.Errorf("converter: %s %s offers no %s, which a build needs", ProgramName, document.Version, required)
		}
	}
	if document.Capabilities.APMapCurrent == "" || len(document.Capabilities.APMapReadable) == 0 {
		return fmt.Errorf("converter: %s %s names no APMap version it reads or writes", ProgramName, document.Version)
	}
	runner.provenance.Program = document.Program
	runner.provenance.Protocol = document.Protocol
	runner.provenance.Version = document.Version
	runner.provenance.License = document.License.SPDX
	runner.provenance.Source = document.License.Source
	runner.provenance.Library = document.Library.Version
	runner.provenance.APMapCurrent = document.Capabilities.APMapCurrent
	runner.provenance.APMapReadable = document.Capabilities.APMapReadable

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
			return fmt.Errorf("converter: %s is not there", path)
		}

		return fmt.Errorf("converter: checking %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("converter: %s is not a regular file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("converter: %s is not executable", path)
	}

	return nil
}
