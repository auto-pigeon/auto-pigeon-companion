package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRunProcess covers the real os/exec path — the one a GPL-2.0 tool will
// take. Re-executing the test binary is the standard way to get a real child
// process without shipping a fixture executable or assuming a shell exists,
// which matters because these tests run on all six targets.
func TestRunProcess(t *testing.T) {
	if os.Getenv("AUL_TEST_SUBPROCESS") != "" {
		fmt.Fprintln(os.Stdout, "child stdout: "+strings.Join(os.Args[1:], " "))
		fmt.Fprintln(os.Stderr, "child stderr")
		if os.Getenv("AUL_TEST_SUBPROCESS") == "fail" {
			os.Exit(3)
		}
		os.Exit(0)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUL_TEST_SUBPROCESS", "ok")

	var stdout, stderr bytes.Buffer
	// -test.run limits the re-executed binary to this one test, which the
	// environment variable above then short-circuits into the child branch.
	args := []string{"-test.run=TestRunProcess", "hello"}
	if err := RunProcess(context.Background(), self, args, "", &stdout, &stderr); err != nil {
		t.Fatalf("RunProcess: %v (stderr: %s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "child stdout: -test.run=TestRunProcess hello") {
		t.Errorf("stdout did not carry the child's output: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "child stderr") {
		t.Errorf("stderr did not carry the child's output: %q", stderr.String())
	}

	t.Run("non-zero exit is reported with its status", func(t *testing.T) {
		t.Setenv("AUL_TEST_SUBPROCESS", "fail")
		var out bytes.Buffer
		err := RunProcess(context.Background(), self, args, "", &out, &out)
		if err == nil {
			t.Fatal("expected an error for a non-zero exit")
		}
		if !strings.Contains(err.Error(), "status 3") {
			t.Errorf("error did not report the exit status: %v", err)
		}
	})
}

func TestEnsureDownloadedVerifiesAndCaches(t *testing.T) {
	payload := []byte("#!/bin/sh\necho fake\n")
	sum := sha256.Sum256(payload)

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write(payload)
	}))
	defer server.Close()

	cache := t.TempDir()
	manager := New(cache).(*cacheManager)
	ref := ToolRef{
		Name: "fake", Version: "1.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		URL: server.URL, SHA256: hex.EncodeToString(sum[:]),
	}

	path, err := manager.EnsureDownloaded(context.Background(), ref)
	if err != nil {
		t.Fatalf("EnsureDownloaded: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("cached content = %q, want %q", got, payload)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("downloaded tool is not executable: %v", info.Mode())
		}
	}

	// Second call must hit the cache, not the network: re-downloading a
	// verified tool on every build would be the difference between a fast
	// build and a slow one.
	if _, err := manager.EnsureDownloaded(context.Background(), ref); err != nil {
		t.Fatalf("second EnsureDownloaded: %v", err)
	}
	if hits != 1 {
		t.Errorf("server was hit %d times, want 1", hits)
	}
}

// A checksum mismatch must leave nothing executable behind. This is the
// security property of the download path, not a nicety.
func TestEnsureDownloadedRejectsChecksumMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("something else entirely"))
	}))
	defer server.Close()

	cache := t.TempDir()
	manager := New(cache).(*cacheManager)
	ref := ToolRef{
		Name: "fake", Version: "1.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		URL: server.URL, SHA256: strings.Repeat("00", sha256.Size),
	}

	if _, err := manager.EnsureDownloaded(context.Background(), ref); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("error = %v, want ErrChecksumMismatch", err)
	}
	if _, err := os.Stat(manager.cachePath(ref)); !os.IsNotExist(err) {
		t.Errorf("a mismatched download was installed at %s", manager.cachePath(ref))
	}
	// The temporary file must be gone too, not just unnamed.
	entries, err := os.ReadDir(filepath.Dir(manager.cachePath(ref)))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("cache directory is not empty after a rejected download: %v", entries)
	}
}

func TestEnsureDownloadedRefusesUnverifiableTool(t *testing.T) {
	manager := New(t.TempDir())
	ref := ToolRef{Name: "fake", Version: "1.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, URL: "http://example.invalid"}
	if _, err := manager.EnsureDownloaded(context.Background(), ref); err == nil {
		t.Fatal("expected a refusal to download a tool with no checksum")
	}
}

func TestResolveUnknownTool(t *testing.T) {
	manager := New(t.TempDir())
	if _, err := manager.Resolve("nothing-registered", "1.0"); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("error = %v, want ErrUnknownTool", err)
	}
}

// TestBuildWithNoopTool is the end-to-end pipeline the prompt asks for:
// resolve, download, verify, run, stream output — with no real binary.
func TestBuildWithNoopTool(t *testing.T) {
	cache := t.TempDir()
	manager := NewNoop(cache)

	var stdout, stderr bytes.Buffer
	request := BuildRequest{Args: []string{"--example", "argument"}}
	if err := Build(context.Background(), manager, request, &stdout, &stderr); err != nil {
		t.Fatalf("Build: %v", err)
	}

	output := stdout.String()
	for _, want := range []string{
		"resolved " + NoopToolName,
		"verified ",
		"fake tool " + NoopToolVersion,
		"args: --example argument",
		"done",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output is missing %q:\n%s", want, output)
		}
	}

	// The "download" must have produced a verified file in the cache, so the
	// second run takes the cache-hit path rather than writing again.
	ref := NoopRef()
	path := filepath.Join(cache, ref.Name, ref.Version, ref.GOOS+"-"+ref.GOARCH, ref.ExecutableName())
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the fake tool was not cached: %v", err)
	}
	if _, err := manager.EnsureDownloaded(context.Background(), ref); err != nil {
		t.Fatalf("second EnsureDownloaded: %v", err)
	}
}

// A cached file that no longer matches its digest must be replaced, not served.
func TestNoopEnsureDownloadedReplacesCorruptCache(t *testing.T) {
	cache := t.TempDir()
	manager := NewNoop(cache)
	ref := NoopRef()

	path, err := manager.EnsureDownloaded(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupted"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.EnsureDownloaded(context.Background(), ref); err != nil {
		t.Fatalf("EnsureDownloaded over a corrupt cache: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != noopPayload {
		t.Errorf("corrupt cache was not replaced: %q", got)
	}
}

// The registry is empty on purpose until the tools are chosen. This test exists
// so populating it is a deliberate act that updates a test, not a silent
// change — and so the licensing note travels with the first entry.
func TestRegistryIsEmptyUntilToolsAreChosen(t *testing.T) {
	if len(Registry) != 0 {
		t.Fatalf("Registry has %d entries; if real GPL-2.0 tools were added, "+
			"update THIRD_PARTY_NOTICES.md with each tool's license text and "+
			"copyright, confirm every entry carries a SHA256 and a License, and "+
			"then update this test", len(Registry))
	}
}
