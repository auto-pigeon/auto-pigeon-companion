// Package aue drives AUE (auto-pigeon-extractor) as a subprocess.
//
// # Why a subprocess and not a library
//
// AUE's packages all live under internal/, and Go's internal-package rule
// blocks a different module from importing them. That is not an obstacle to
// route around: AUE's CLI is its supported public surface, and its subcommands
// already print JSON. So AUC shells out and reads stdout, and AUE never
// appears in go.mod.
//
// # Why an interface
//
// AUE also has a `serve` subcommand that exposes the same operations over
// local HTTP. Nothing here is built for that today, but every caller depends
// on the Runner interface rather than on process spawning, so a future
// HTTPRunner can be substituted without touching call sites.
//
// # Where the binary comes from
//
// The platform-matching AUE binary is embedded into the companion executable
// at build time (see embed.go for the build ordering constraint). On first use
// EmbeddedRunner extracts it to a temp directory, marks it executable, and
// execs it from there; Close removes the directory.
//
// TODO(confirm-aue-embed-decision): the embed-rather-than-download approach is
// this session's assumption, not a confirmed decision. If it changes to a
// runtime download, only this package changes.
package aue

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

// EnvBinaryOverride names an on-disk AUE binary to use instead of the embedded
// one. It exists for development (a plain `go build` embeds nothing) and for
// support cases where a user must be moved onto a patched AUE without a new
// companion release.
const EnvBinaryOverride = "AUC_AUE_BINARY"

// ErrNoEmbeddedBinary is returned when the companion binary was built without
// an AUE binary staged in internal/aue/embedded/ and no override is set.
var ErrNoEmbeddedBinary = errors.New("no AUE binary is embedded in this build and " + EnvBinaryOverride + " is not set")

// Runner invokes one AUE subcommand and returns its stdout.
//
// Implementations must be safe for concurrent use: the local HTTP server
// handles requests on many goroutines.
type Runner interface {
	Run(ctx context.Context, subcommand string, args ...string) ([]byte, error)
}

// ExitError describes an AUE invocation that ran but failed. AUE's exit codes
// are meaningful (1 = the operation failed, 2 = the invocation was wrong), so
// they are surfaced rather than collapsed into a generic error.
type ExitError struct {
	Subcommand string
	ExitCode   int
	Stderr     string
}

func (err *ExitError) Error() string {
	message := fmt.Sprintf("auto-pigeon-extractor %s failed with exit code %d", err.Subcommand, err.ExitCode)
	if err.Stderr != "" {
		message += ": " + err.Stderr
	}
	return message
}

// EmbeddedRunner execs the embedded AUE binary, extracting it to a temp
// directory on first use. The zero value is not usable; call NewEmbeddedRunner.
type EmbeddedRunner struct {
	// override, when non-empty, is an on-disk binary used instead of the
	// embedded one and never extracted or deleted.
	override string

	once    sync.Once
	dir     string
	path    string
	extract error
}

// NewEmbeddedRunner returns a Runner backed by the embedded AUE binary, or by
// the EnvBinaryOverride path when that environment variable is set.
//
// Extraction is deferred to the first Run, so constructing a runner is free
// and a companion process that never touches AUE never writes to disk.
func NewEmbeddedRunner() *EmbeddedRunner {
	return &EmbeddedRunner{override: os.Getenv(EnvBinaryOverride)}
}

// binaryPath resolves the executable path, extracting the embedded binary once.
func (runner *EmbeddedRunner) binaryPath() (string, error) {
	runner.once.Do(func() {
		if runner.override != "" {
			runner.path = runner.override
			return
		}
		runner.extract = runner.extractEmbedded()
	})
	if runner.extract != nil {
		return "", runner.extract
	}
	return runner.path, nil
}

func (runner *EmbeddedRunner) extractEmbedded() error {
	name, err := embeddedBinaryName()
	if err != nil {
		return err
	}
	payload, err := embedded.ReadFile("embedded/" + name)
	if err != nil {
		return fmt.Errorf("cannot read embedded AUE binary: %w", err)
	}

	dir, err := os.MkdirTemp("", "auc-aue-")
	if err != nil {
		return fmt.Errorf("cannot create temporary directory for AUE: %w", err)
	}
	target := filepath.Join(dir, name)
	if runtime.GOOS == "windows" && filepath.Ext(target) == "" {
		target += ".exe"
	}
	// 0700: the extracted binary is this user's, and a world-writable copy of
	// an executable this process is about to run would be a local privilege
	// escalation waiting to happen.
	if err := os.WriteFile(target, payload, 0o700); err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("cannot write %s: %w", target, err)
	}
	runner.dir = dir
	runner.path = target
	return nil
}

// embeddedBinaryName finds the single staged binary inside embedded/.
func embeddedBinaryName() (string, error) {
	entries, err := fs.ReadDir(embedded, "embedded")
	if err != nil {
		return "", fmt.Errorf("cannot list embedded AUE directory: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == ".gitkeep" {
			continue
		}
		names = append(names, entry.Name())
	}
	switch len(names) {
	case 0:
		return "", ErrNoEmbeddedBinary
	case 1:
		return names[0], nil
	default:
		// Guessing here would ship the wrong platform's binary to users. The
		// build script's contract is exactly one file; say so instead.
		return "", fmt.Errorf("expected exactly one embedded AUE binary, found %d: %v", len(names), names)
	}
}

// Run executes one AUE subcommand and returns its stdout.
func (runner *EmbeddedRunner) Run(ctx context.Context, subcommand string, args ...string) ([]byte, error) {
	path, err := runner.binaryPath()
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, path, append([]string{subcommand}, args...)...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	if err := command.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.Bytes(), &ExitError{
				Subcommand: subcommand,
				ExitCode:   exitErr.ExitCode(),
				Stderr:     trimmed(stderr.String()),
			}
		}
		return nil, fmt.Errorf("cannot run auto-pigeon-extractor %s: %w", subcommand, err)
	}
	return stdout.Bytes(), nil
}

// Close removes the extracted binary. It is safe to call on a runner that
// never extracted anything.
func (runner *EmbeddedRunner) Close() error {
	if runner.dir == "" {
		return nil
	}
	dir := runner.dir
	runner.dir = ""
	return os.RemoveAll(dir)
}

// Available reports whether this build can run AUE at all, without executing
// it. The server uses this to tell the frontend up front rather than failing
// on the user's first real operation.
func (runner *EmbeddedRunner) Available() bool {
	_, err := runner.binaryPath()
	return err == nil
}

// Available reports whether runner can run AUE at all, without executing it.
//
// It is a free function rather than a Runner method because availability is
// interesting to a caller holding any Runner — including a nil one, which is
// what a build with no extractor and no override produces — while a Runner
// implementation that always works (a future HTTPRunner) should not have to
// carry the method. A runner that does not implement the optional interface is
// reported available: it exists, so it can be tried.
func Available(runner Runner) bool {
	if runner == nil {
		return false
	}
	if checker, ok := runner.(interface{ Available() bool }); ok {
		return checker.Available()
	}
	return true
}

func trimmed(text string) string {
	return string(bytes.TrimSpace([]byte(text)))
}

// KnownSubcommands is AUE's subcommand list as of this bootstrap, recorded so
// the frontend has something to show before any real feature exists.
//
// It is a snapshot, not a contract: AUE's internal/cli/cli.go is authoritative
// and has been growing. Nothing in AUC validates against this list before
// spawning — AUE rejects an unknown subcommand with exit code 2 and a clear
// message, which is a better error than one derived from a stale copy.
var KnownSubcommands = []string{
	"entities",
	"targets",
	"compat-stats",
	"summarize",
	"roundtrip",
	"wad-textures",
	"model-footprints",
	"apmap-validate",
}
