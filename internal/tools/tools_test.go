package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

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

// TestNoopAcquisitionIsTheWholeDownloadPath: resolve, "download", verify,
// install, and take the cache-hit path on the second call.
//
// It stops at an installed file, which is where this package's responsibility
// now stops: running one is internal/job's, and its fixtures cover a real
// process with a real exit status rather than a fake that prints.
func TestNoopAcquisitionIsTheWholeDownloadPath(t *testing.T) {
	cache := t.TempDir()
	manager := NewNoop(cache)

	ref, err := manager.Resolve(NoopToolName, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	installed, err := manager.EnsureDownloaded(context.Background(), ref)
	if err != nil {
		t.Fatalf("EnsureDownloaded: %v", err)
	}

	want := filepath.Join(cache, ref.Name, ref.Version, ref.GOOS+"-"+ref.GOARCH, ref.ExecutableName())
	if installed != want {
		t.Errorf("installed at %s, want %s", installed, want)
	}
	verified, err := verifyFile(installed, ref.SHA256)
	if err != nil || !verified {
		t.Fatalf("the installed file does not match its digest: %v %v", verified, err)
	}

	// The second call must take the cache-hit path rather than writing again.
	info, err := os.Stat(installed)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	again, err := manager.EnsureDownloaded(context.Background(), ref)
	if err != nil {
		t.Fatalf("second EnsureDownloaded: %v", err)
	}
	if again != installed {
		t.Errorf("second call installed at %s, want %s", again, installed)
	}
	after, err := os.Stat(installed)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !after.ModTime().Equal(info.ModTime()) {
		t.Error("the cached file was rewritten on a cache hit")
	}
}

// TestNothingHereRunsAnything: the Manager interface is acquisition only.
//
// Asserted rather than assumed, because "this package does not execute" is a
// licensing statement as well as a design one — see the package comment — and
// a Run method growing back here is exactly how a second execution path starts.
func TestNothingHereRunsAnything(t *testing.T) {
	managerType := reflect.TypeOf((*Manager)(nil)).Elem()
	for i := 0; i < managerType.NumMethod(); i++ {
		switch name := managerType.Method(i).Name; name {
		case "Resolve", "EnsureDownloaded":
		default:
			t.Errorf("Manager has the method %q; acquisition is this package's only job, and running belongs to internal/job", name)
		}
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
