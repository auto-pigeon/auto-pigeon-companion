// Package tools resolves, downloads, verifies, and runs the external map-building
// tools AUL drives.
//
// # The licensing boundary this package exists to enforce
//
// The external map-building tools are GPL-2.0. This repository is MIT. Those
// two licenses coexist here on exactly one condition: the tools are separate
// programs, invoked as separate OS processes through os/exec, downloaded as
// independent prebuilt binaries. They are never compiled into this module,
// never statically or dynamically linked against it, and never vendored into
// this repository.
//
// Concretely, and non-negotiably:
//
//   - No Go dependency in go.mod may pull a GPL-2.0 tool's source or object
//     code into this module.
//   - No tool binary is committed to this repository or embedded with
//     //go:embed.
//   - Communication with a tool is process-level only: argv, environment,
//     working directory, stdin/stdout/stderr, exit status, and files on disk.
//     Nothing in this package may grow an in-process calling convention.
//   - Each tool keeps its own license and copyright. This repository's MIT
//     license never applies to a downloaded binary.
//
// See THIRD_PARTY_NOTICES.md, which is the user-facing statement of the same
// boundary and the place per-tool license text goes once the tools are chosen.
//
// # State of this package
//
// Scaffolding. The Manager interface, the download-and-verify path, and the
// process runner are real; the registry of actual tools is empty on purpose.
// noop_tool.go provides a fake tool so the whole pipeline — resolve, download,
// verify, run, stream output — is exercised end to end without a real binary.
//
// TODO(andrea): which GPL-2.0 tool(s), and which versions. Needed before
// Registry can be populated and before a real Resolve implementation can exist.
// Also unresolved: whether tools are downloaded on first run (what this code
// assumes) or bundled into release archives, which would change both this file
// and the packaging scripts. See THIRD_PARTY_NOTICES.md.
package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ToolRef identifies one downloadable build of one external tool.
type ToolRef struct {
	// Name is the tool's short name, e.g. the compiler or vis stage's binary
	// name, without any platform extension.
	Name string
	// Version is the upstream release identifier.
	Version string
	// GOOS and GOARCH are the platform this build is for, in Go's spelling.
	GOOS   string
	GOARCH string
	// URL is where the prebuilt binary or archive is fetched from.
	URL string
	// SHA256 is the hex digest of the bytes at URL. Downloads that do not match
	// are discarded — a tool binary is executed, so an unverified download is
	// arbitrary code execution.
	SHA256 string
	// License names the tool's own license, carried alongside the reference so
	// no code path can handle a tool without it being visible. Not consulted at
	// runtime; present so THIRD_PARTY_NOTICES.md can be generated from the
	// registry rather than maintained by hand.
	License string
}

// ExecutableName is the on-disk filename for this reference on its target
// platform.
func (r ToolRef) ExecutableName() string {
	if r.GOOS == "windows" {
		return r.Name + ".exe"
	}
	return r.Name
}

// String identifies the reference in errors and logs.
func (r ToolRef) String() string {
	return fmt.Sprintf("%s@%s (%s/%s)", r.Name, r.Version, r.GOOS, r.GOARCH)
}

// Manager resolves, downloads, verifies, and runs external GPL-2.0 map-building
// tools as separate OS processes. It never imports or links tool source/object
// code into this binary — see the package comment and THIRD_PARTY_NOTICES.md.
type Manager interface {
	// Resolve maps a tool name and version to a concrete downloadable
	// reference for the current platform.
	Resolve(name, version string) (ToolRef, error)
	// EnsureDownloaded returns the local path to the tool's executable,
	// downloading and checksum-verifying it if it is not already cached.
	EnsureDownloaded(ctx context.Context, ref ToolRef) (path string, err error)
	// Run executes the tool as a separate process, streaming its output.
	Run(ctx context.Context, path string, args []string, stdout, stderr io.Writer) error
}

// ErrUnknownTool reports that no registry entry matches the requested tool.
var ErrUnknownTool = errors.New("tools: unknown tool")

// ErrChecksumMismatch reports that a download did not match its expected
// digest. It is never retried automatically: a mismatch is either corruption or
// tampering, and both call for a human.
var ErrChecksumMismatch = errors.New("tools: checksum mismatch")

// Registry is the set of known tools, keyed by name then version then
// "GOOS/GOARCH".
//
// Empty by design. Populating it is the follow-up task that turns this package
// from scaffolding into a real implementation, and it cannot happen before the
// tools are chosen.
var Registry = map[string]map[string]map[string]ToolRef{}

// downloadTimeout bounds a whole fetch. Tool binaries are single-digit
// megabytes; anything past this is a stall, not a slow link.
const downloadTimeout = 5 * time.Minute

// cacheManager is the real Manager: a registry lookup, an HTTP download into a
// content-verified cache, and os/exec.
type cacheManager struct {
	cacheDir   string
	httpClient *http.Client
	goos       string
	goarch     string
}

// New returns a Manager caching downloaded tools under cacheDir.
//
// The platform is fixed to the running one. A cross-platform Resolve would be
// meaningless here: AUL runs the tool it downloads on the machine it is
// running on.
func New(cacheDir string) Manager {
	return &cacheManager{
		cacheDir:   cacheDir,
		httpClient: &http.Client{Timeout: downloadTimeout},
		goos:       runtime.GOOS,
		goarch:     runtime.GOARCH,
	}
}

func (m *cacheManager) Resolve(name, version string) (ToolRef, error) {
	versions, ok := Registry[name]
	if !ok {
		return ToolRef{}, fmt.Errorf("%w: %q", ErrUnknownTool, name)
	}
	platforms, ok := versions[version]
	if !ok {
		return ToolRef{}, fmt.Errorf("%w: %q has no version %q", ErrUnknownTool, name, version)
	}
	platform := m.goos + "/" + m.goarch
	ref, ok := platforms[platform]
	if !ok {
		return ToolRef{}, fmt.Errorf("%w: %s@%s has no build for %s", ErrUnknownTool, name, version, platform)
	}
	return ref, nil
}

// cachePath is where a reference's executable lives once downloaded. Version
// and platform are in the path so two versions can coexist and a cache copied
// between machines cannot present the wrong build.
func (m *cacheManager) cachePath(ref ToolRef) string {
	return filepath.Join(m.cacheDir, ref.Name, ref.Version, ref.GOOS+"-"+ref.GOARCH, ref.ExecutableName())
}

func (m *cacheManager) EnsureDownloaded(ctx context.Context, ref ToolRef) (string, error) {
	if ref.Name == "" || ref.Version == "" {
		return "", fmt.Errorf("tools: incomplete tool reference %+v", ref)
	}
	if ref.SHA256 == "" {
		// Refusing here rather than downloading unverified is the whole point
		// of the field: this binary gets executed.
		return "", fmt.Errorf("tools: %s has no expected checksum; refusing to download an unverified executable", ref)
	}

	path := m.cachePath(ref)
	switch verified, err := verifyFile(path, ref.SHA256); {
	case err != nil:
		return "", err
	case verified:
		return path, nil
	}

	if ref.URL == "" {
		return "", fmt.Errorf("tools: %s is not cached and has no download URL", ref)
	}
	if err := m.download(ctx, ref, path); err != nil {
		return "", err
	}
	return path, nil
}

// verifyFile reports whether path exists and matches digest. A missing file is
// (false, nil); a present file with the wrong digest is also (false, nil) so
// the caller re-downloads over it, since a truncated earlier download is the
// common cause and is self-healing.
func verifyFile(path, digest string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("tools: opening the cached %s: %w", path, err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return false, fmt.Errorf("tools: reading the cached %s: %w", path, err)
	}
	return strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), digest), nil
}

// download fetches ref into path, verifying the digest before the file is ever
// visible at its final name.
//
// The order matters and is the security property: bytes land in a temporary
// file, the digest is checked, and only a match gets renamed into place. A
// download that fails verification is removed and never becomes an executable
// anything can run.
func (m *cacheManager) download(ctx context.Context, ref ToolRef, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("tools: creating %s: %w", dir, err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, ref.URL, nil)
	if err != nil {
		return fmt.Errorf("tools: building the request for %s: %w", ref, err)
	}
	response, err := m.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("tools: downloading %s: %w", ref, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("tools: downloading %s: HTTP %d", ref, response.StatusCode)
	}

	temp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return fmt.Errorf("tools: creating a temporary file in %s: %w", dir, err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName) // No-op after a successful rename.

	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temp, hash), response.Body); err != nil {
		temp.Close()
		return fmt.Errorf("tools: writing %s: %w", tempName, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("tools: closing %s: %w", tempName, err)
	}

	got := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(got, ref.SHA256) {
		return fmt.Errorf("%w for %s: expected %s, got %s", ErrChecksumMismatch, ref, ref.SHA256, got)
	}

	// Executable only after it has been verified.
	if err := os.Chmod(tempName, 0o755); err != nil {
		return fmt.Errorf("tools: making %s executable: %w", tempName, err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("tools: installing %s: %w", path, err)
	}
	return nil
}

// Run executes the tool at path as a separate process.
//
// This function is the licensing boundary in code: an external tool is reached
// only through exec.CommandContext, and its interface to AUL is argv plus two
// output streams. Nothing here loads tool code into this process, and nothing
// added here ever may.
func (m *cacheManager) Run(ctx context.Context, path string, args []string, stdout, stderr io.Writer) error {
	return RunProcess(ctx, path, args, "", stdout, stderr)
}

// RunProcess runs an external program, streaming its output to the given
// writers. dir is the working directory; empty means the caller's.
//
// Exported because internal/launch needs the same process semantics for game
// executables, and having one implementation means the stream handling and the
// exit-code reporting cannot drift between "run a tool" and "run a game".
func RunProcess(ctx context.Context, path string, args []string, dir string, stdout, stderr io.Writer) error {
	if path == "" {
		return fmt.Errorf("tools: no executable path")
	}
	command := exec.CommandContext(ctx, path, args...)
	command.Dir = dir
	command.Stdout = stdout
	command.Stderr = stderr

	if err := command.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// The exit status is the useful part; the wrapped error adds
			// nothing a user can act on.
			return fmt.Errorf("tools: %s exited with status %d", filepath.Base(path), exitErr.ExitCode())
		}
		return fmt.Errorf("tools: running %s: %w", path, err)
	}
	return nil
}
