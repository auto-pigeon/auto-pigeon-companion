package aue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// writeFakeAUE writes a tiny executable that echoes its first argument, so the
// subprocess path can be exercised without a real extractor binary.
func writeFakeAUE(t *testing.T, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake extractor is a shell script")
	}
	path := filepath.Join(t.TempDir(), "fake-aue")
	script := "#!/bin/sh\necho \"$1\"\necho 'boom' >&2\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunReturnsStdoutFromTheOverrideBinary(t *testing.T) {
	t.Setenv(EnvBinaryOverride, writeFakeAUE(t, 0))

	runner := NewEmbeddedRunner()
	defer runner.Close()

	stdout, err := runner.Run(context.Background(), "summarize", "map.map")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(stdout) != "summarize\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "summarize\n")
	}
}

func TestRunSurfacesTheExtractorExitCode(t *testing.T) {
	// AUE's 1-versus-2 split is meaningful, so it must not be flattened.
	t.Setenv(EnvBinaryOverride, writeFakeAUE(t, 2))

	runner := NewEmbeddedRunner()
	defer runner.Close()

	_, err := runner.Run(context.Background(), "summarize")
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v, want *ExitError", err)
	}
	if exitErr.ExitCode != 2 || exitErr.Subcommand != "summarize" {
		t.Fatalf("exitErr = %+v", exitErr)
	}
	if exitErr.Stderr != "boom" {
		t.Fatalf("stderr = %q, want %q", exitErr.Stderr, "boom")
	}
}

// A development build embeds nothing (the directory holds only .gitkeep), and
// that must be a clear message rather than a mysterious failure at exec time.
func TestNoEmbeddedBinaryIsReportedClearly(t *testing.T) {
	t.Setenv(EnvBinaryOverride, "")

	runner := NewEmbeddedRunner()
	defer runner.Close()

	if runner.Available() {
		t.Skip("this build has an AUE binary staged in internal/aue/embedded/")
	}
	_, err := runner.Run(context.Background(), "version")
	if !errors.Is(err, ErrNoEmbeddedBinary) {
		t.Fatalf("err = %v, want ErrNoEmbeddedBinary", err)
	}
}

func TestAvailableIsTrueWithAnOverride(t *testing.T) {
	t.Setenv(EnvBinaryOverride, writeFakeAUE(t, 0))

	runner := NewEmbeddedRunner()
	defer runner.Close()

	if !runner.Available() {
		t.Fatal("Available() = false, want true with an override set")
	}
}

func TestCloseIsSafeWithoutExtraction(t *testing.T) {
	if err := NewEmbeddedRunner().Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestEmbeddedRunnerSatisfiesRunner(t *testing.T) {
	var _ Runner = NewEmbeddedRunner()
}
