package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// NoopToolName is the fake tool's name.
const NoopToolName = "noop"

// NoopToolVersion is the fake tool's only version.
const NoopToolVersion = "0.0.0-fake"

// noopManager is a Manager backed by a fake tool.
//
// # Why this exists
//
// Which GPL-2.0 map-building tools the Companion will drive is not decided, so
// there is no real binary to resolve or download. Without a stand-in, the whole
// acquisition path would be untested code that first executes on the day a real
// tool is wired in.
//
// # What is real and what is simulated
//
// Real: the cache layout, the SHA-256 verification, the atomic install, and the
// re-use of an already-cached and still-matching file.
//
// Simulated: the HTTP fetch, because there is no URL to fetch from — the
// payload is generated locally and then verified by exactly the same digest
// check a downloaded file goes through.
//
// Nothing here runs anything. It used to: this file carried a Run that printed
// the fake tool's output instead of starting a process, which meant the one
// path a user could exercise was the one that never spawned. Execution is
// internal/job's now, and its fixtures re-execute the test binary, so what is
// covered is a real process with a real exit status.
//
// Nothing about this file weakens the licensing boundary: the fake tool is
// this repository's own code, and it exists precisely so that no real GPL-2.0 binary has to
// be vendored to make the pipeline exercisable.
type noopManager struct {
	cacheDir string
}

// NewNoop returns a Manager for the fake tool, caching under cacheDir.
func NewNoop(cacheDir string) Manager { return &noopManager{cacheDir: cacheDir} }

// noopPayload is the fake tool's "binary" content. Fixed bytes, so its digest
// is fixed, so the cache-hit path is reached on the second call.
const noopPayload = "auto-pigeon-companion fake tool: not a real program, not GPL-2.0, no external code\n"

func noopDigest() string {
	sum := sha256.Sum256([]byte(noopPayload))
	return hex.EncodeToString(sum[:])
}

// NoopRef is the reference the fake tool resolves to on the running platform.
func NoopRef() ToolRef {
	return ToolRef{
		Name:    NoopToolName,
		Version: NoopToolVersion,
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
		// No URL: nothing is fetched. EnsureDownloaded below generates the
		// payload instead, which is why this reference never reaches
		// cacheManager.download.
		SHA256:  noopDigest(),
		License: "MIT (this repository's own code — the fake tool is not a third-party program)",
	}
}

func (m *noopManager) Resolve(name, version string) (ToolRef, error) {
	if name != NoopToolName {
		return ToolRef{}, fmt.Errorf("%w: the fake tool manager only serves %q, got %q", ErrUnknownTool, NoopToolName, name)
	}
	if version != "" && version != NoopToolVersion {
		return ToolRef{}, fmt.Errorf("%w: %s has no version %q", ErrUnknownTool, NoopToolName, version)
	}
	return NoopRef(), nil
}

func (m *noopManager) EnsureDownloaded(ctx context.Context, ref ToolRef) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if ref.Name != NoopToolName {
		return "", fmt.Errorf("%w: %s", ErrUnknownTool, ref)
	}

	path := filepath.Join(m.cacheDir, ref.Name, ref.Version, ref.GOOS+"-"+ref.GOARCH, ref.ExecutableName())
	switch verified, err := verifyFile(path, ref.SHA256); {
	case err != nil:
		return "", err
	case verified:
		return path, nil
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("tools: creating %s: %w", dir, err)
	}
	temp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", fmt.Errorf("tools: creating a temporary file in %s: %w", dir, err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)

	if _, err := io.WriteString(temp, noopPayload); err != nil {
		temp.Close()
		return "", fmt.Errorf("tools: writing %s: %w", tempName, err)
	}
	if err := temp.Close(); err != nil {
		return "", fmt.Errorf("tools: closing %s: %w", tempName, err)
	}

	// The same verification a real download gets, on the same helper. A fake
	// that skipped it would leave the check itself unexercised.
	verified, err := verifyFile(tempName, ref.SHA256)
	if err != nil {
		return "", err
	}
	if !verified {
		return "", fmt.Errorf("%w for %s", ErrChecksumMismatch, ref)
	}
	if err := os.Chmod(tempName, 0o755); err != nil {
		return "", fmt.Errorf("tools: making %s executable: %w", tempName, err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return "", fmt.Errorf("tools: installing %s: %w", path, err)
	}
	return path, nil
}
