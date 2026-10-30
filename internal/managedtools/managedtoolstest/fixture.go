// Package managedtoolstest is a deterministic stand-in for GitHub's release
// API and release downloads, serving releases in the official schemas with
// real zip archives, for tests of internal/managedtools and of what uses it.
//
// Only test files import it, so it is never linked into the Companion. The
// archives it serves carry real programs — one small Go program compiled per
// test process and copied under each tool's program names — so a version
// check, a build and a provenance record all read bytes that ran.
package managedtoolstest

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
)

// Server is the stand-in. Its handlers read Releases under mu, so a test can
// publish, withdraw or alter a release between two checks.
type Server struct {
	*httptest.Server
	mu sync.Mutex
	// Releases per repository ("auto-pigeon/ericw-tools"), newest first.
	Releases map[string][]*Release
	// Requests counts the release-list requests per repository, and
	// Downloads the file downloads by name.
	Requests  map[string]int
	Downloads map[string]int
	// RateLimit makes the next list request answer 429 with Retry-After.
	RateLimit time.Duration
	// RedirectTo, when set, makes a download answer 302 to this URL.
	RedirectTo string
	// Fail makes every request answer 503.
	Fail atomic.Bool
	// Hold, when set, blocks each archive download until it is closed.
	Hold chan struct{}
}

// Release is one served release.
type Release struct {
	ID         int64
	Tag        string
	Draft      bool
	Prerelease bool
	Published  time.Time
	// Files by name: the archive(s), release-manifest.json, anything else.
	Files map[string][]byte
	// Digests overrides GitHub's listed digest per file ("" = none listed).
	Digests map[string]*string
	// Sizes overrides the listed size per file.
	Sizes map[string]int64
	// ids per file, stable across requests.
	ids map[string]int64
}

var nextID atomic.Int64

// NewServer starts a stand-in on loopback.
func NewServer(t testing.TB) *Server {
	s := &Server{Releases: map[string][]*Release{}, Requests: map[string]int{}, Downloads: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Source is a production release source pointed at this server.
func (s *Server) Source(t testing.TB) managedtools.Source {
	source, err := managedtools.NewLoopbackSource(s.URL, s.Client())
	if err != nil {
		t.Fatal(err)
	}
	return source
}

// Publish adds a release at the top of a repository's list.
func (s *Server) Publish(repo string, release *Release) *Release {
	s.mu.Lock()
	defer s.mu.Unlock()
	if release.ID == 0 {
		release.ID = nextID.Add(1)
	}
	if release.Published.IsZero() {
		release.Published = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC).Add(time.Duration(release.ID) * time.Minute)
	}
	release.ids = map[string]int64{}
	for name := range release.Files {
		release.ids[name] = nextID.Add(1)
	}
	s.Releases[repo] = append([]*Release{release}, s.Releases[repo]...)
	return release
}

// Mutate changes a published release under the server's lock.
func (s *Server) Mutate(change func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change()
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if s.Fail.Load() {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/api/repos/") && strings.HasSuffix(path, "/releases"):
		s.list(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/api/repos/"), "/releases"))
	case strings.HasPrefix(path, "/dl/"):
		s.download(w, r, strings.TrimPrefix(path, "/dl/"))
	case strings.HasPrefix(path, "/asset/"):
		s.download(w, r, strings.TrimPrefix(path, "/asset/"))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) list(w http.ResponseWriter, r *http.Request, repo string) {
	s.mu.Lock()
	s.Requests[repo]++
	if s.RateLimit > 0 {
		wait := s.RateLimit
		s.RateLimit = 0
		s.mu.Unlock()
		w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)))
		http.Error(w, "slow down", http.StatusTooManyRequests)
		return
	}
	var body []map[string]any
	for _, release := range s.Releases[repo] {
		var assets []map[string]any
		names := make([]string, 0, len(release.Files))
		for name := range release.Files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			bytes := release.Files[name]
			sum := sha256.Sum256(bytes)
			digest := "sha256:" + hex.EncodeToString(sum[:])
			if override, ok := release.Digests[name]; ok {
				if override == nil {
					digest = ""
				} else {
					digest = *override
				}
			}
			size := int64(len(bytes))
			if override, ok := release.Sizes[name]; ok {
				size = override
			}
			asset := map[string]any{"id": release.ids[name], "name": name, "size": size, "state": "uploaded"}
			if digest != "" {
				asset["digest"] = digest
			}
			assets = append(assets, asset)
		}
		body = append(body, map[string]any{
			"id": release.ID, "tag_name": release.Tag, "name": release.Tag, "draft": release.Draft,
			"prerelease": release.Prerelease, "published_at": release.Published.Format(time.RFC3339),
			"html_url": "https://github.com/" + repo + "/releases/tag/" + release.Tag, "assets": assets,
		})
	}
	s.mu.Unlock()
	encoded, _ := json.Marshal(body)
	sum := sha256.Sum256(encoded)
	etag := `W/"` + hex.EncodeToString(sum[:8]) + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(encoded)
}

// download serves <owner>/<repo>/releases/download/<tag>/<name>.
func (s *Server) download(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	if len(parts) != 6 || parts[2] != "releases" || parts[3] != "download" {
		http.NotFound(w, r)
		return
	}
	repo, tag, name := parts[0]+"/"+parts[1], parts[4], parts[5]
	s.mu.Lock()
	redirect := s.RedirectTo
	if redirect != "" && strings.HasPrefix(r.URL.Path, "/dl/") {
		s.mu.Unlock()
		http.Redirect(w, r, strings.ReplaceAll(redirect, "{path}", rest), http.StatusFound)
		return
	}
	var found []byte
	for _, release := range s.Releases[repo] {
		if release.Tag == tag {
			found = release.Files[name]
		}
	}
	s.Downloads[name]++
	hold := s.Hold
	s.mu.Unlock()
	if found == nil {
		http.NotFound(w, r)
		return
	}
	if hold != nil && strings.HasSuffix(name, ".zip") {
		select {
		case <-hold:
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(found)))
	_, _ = w.Write(found)
}

// --- releases in the official shapes -----------------------------------------

// Spec describes one release to build.
type Spec struct {
	Tool    managedtools.ToolID
	Version string
	// Targets to publish; default this machine's.
	Targets []string
	// Banner is what the programs print; default one the built-in profile
	// accepts.
	Banner     string
	Prerelease bool
	Draft      bool
	// Schema overrides the release-manifest schema.
	Schema string
	// Unqualified makes the acceptance verdict "fail".
	Unqualified bool
	// Archive changes an archive's entries after its file list was written.
	Archive func(entries map[string][]byte) map[string][]byte
	// ArchiveManifest changes the inner file list before it is written.
	ArchiveManifest func(manifest map[string]any)
	// Program is the bytes for every program; default the compiled fixture.
	Program []byte
	// PrintsVersion, when set, is the version the programs say they are
	// instead of Version: a release whose programs fail their version check.
	PrintsVersion string
}

// ThisTarget is the release name of the machine the test runs on.
func ThisTarget() string { return managedtools.Target(runtime.GOOS, runtime.GOARCH) }

// Banner is a version banner the built-in profiles accept for a version.
func Banner(tool managedtools.ToolID, version string) string {
	if tool == managedtools.EricW {
		return "2.0.0-alpha11-49-gd645c6f4+auto-pigeon." + version
	}
	return version + "+gca2e281d.upstream.82161339"
}

// Build makes the release's files: one archive per target and its
// release-manifest.json.
func Build(t testing.TB, spec Spec) *Release {
	tool, ok := managedtools.Find(spec.Tool)
	if !ok {
		t.Fatalf("no tool %s", spec.Tool)
	}
	if len(spec.Targets) == 0 {
		spec.Targets = []string{ThisTarget()}
	}
	if spec.Banner == "" {
		spec.Banner = Banner(spec.Tool, spec.Version)
	}
	program := spec.Program
	if program == nil {
		program = FixtureProgram(t)
	}
	tag := tool.TagPrefix + spec.Version
	files := map[string][]byte{}
	targets := map[string]any{}
	var assets []map[string]any
	for _, target := range spec.Targets {
		archiveName, archive := buildArchive(t, tool, spec, target, program)
		files[archiveName] = archive
		sum := sha256.Sum256(archive)
		verdict := "pass"
		if spec.Unqualified {
			verdict = "fail"
		}
		targets[target] = map[string]any{"asset": archiveName, "status": "published", "required": true,
			"sha256": hex.EncodeToString(sum[:]), "size": len(archive)}
		assets = append(assets, map[string]any{"name": archiveName, "kind": "toolchain", "platform": target,
			"sha256": hex.EncodeToString(sum[:]), "size": len(archive), "acceptance": map[string]any{"verdict": verdict}})
	}
	schema := spec.Schema
	if schema == "" {
		schema = tool.ManifestSchemas[0]
	}
	manifest := map[string]any{
		"schema": schema, "tag": tag, "version": spec.Version, "version_banner": spec.Banner,
		"commit": strings.Repeat("c", 40), "prerelease": spec.Prerelease, "signed": false,
		"fork": "https://github.com/" + tool.Owner + "/" + tool.Repo, "targets": targets, "assets": assets,
	}
	encoded, _ := json.MarshalIndent(manifest, "", "  ")
	files[managedtools.ManifestAssetName] = encoded
	files["SHA256SUMS"] = []byte("not read by the Companion\n")
	return &Release{Tag: tag, Prerelease: spec.Prerelease, Draft: spec.Draft, Files: files}
}

// ArchiveName is the official name of a tool's archive.
func ArchiveName(tool managedtools.Tool, version, target string) string {
	if tool.ID == managedtools.EricW {
		return "auto-pigeon-ericw-tools-" + version + "-" + target + ".zip"
	}
	return "auto-pigeon-q3map2-" + version + "-" + target + ".zip"
}

func buildArchive(t testing.TB, tool managedtools.Tool, spec Spec, target string, program []byte) (string, []byte) {
	name := ArchiveName(tool, spec.Version, target)
	root := strings.TrimSuffix(name, ".zip")
	suffix := ""
	if strings.HasPrefix(target, "windows-") {
		suffix = ".exe"
	}
	prints := spec.Version
	if spec.PrintsVersion != "" {
		prints = spec.PrintsVersion
	}
	entries := map[string][]byte{
		"README.md":           []byte("fixture release " + prints + "\n"),
		"licenses/COPYING":    []byte("GPL text stands in here\n"),
		"bin/libfixture.so.1": []byte("a library the programs need\n"),
	}
	for _, p := range tool.Programs {
		entries[p+suffix] = program
	}
	if tool.ID == managedtools.EricW {
		entries["bin/maputil"+suffix] = program
	}
	var files []map[string]any
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sum := sha256.Sum256(entries[n])
		mode := "0644"
		if strings.HasPrefix(n, "bin/") && !strings.Contains(n, ".so") {
			mode = "0755"
		}
		files = append(files, map[string]any{"path": n, "sha256": hex.EncodeToString(sum[:]), "size": len(entries[n]), "mode": mode})
	}
	inner := map[string]any{"schema": tool.ArchiveSchemas[0], "root": root, "platform": target,
		"version": spec.Version, "commit": strings.Repeat("c", 40), "files": files}
	if spec.ArchiveManifest != nil {
		spec.ArchiveManifest(inner)
	}
	if spec.Archive != nil {
		entries = spec.Archive(entries)
	}
	innerJSON, _ := json.MarshalIndent(inner, "", "  ")
	return name, Zip(t, root, entries, innerJSON)
}

// Zip writes an archive with one top-level folder, the file list, and
// entries. Names beginning with "!" are written verbatim (no root folder):
// how a test writes an absolute or escaping name.
func Zip(t testing.TB, root string, entries map[string][]byte, inner []byte) []byte {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	add := func(name string, body []byte, mode os.FileMode) {
		header := &zip.FileHeader{Name: name, Method: zip.Store} // stored: the fixture program is copied six times per archive
		header.SetMode(mode)
		w, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(body)
	}
	dir := &zip.FileHeader{Name: root + "/"}
	dir.SetMode(os.ModeDir | 0o755)
	if _, err := writer.CreateHeader(dir); err != nil {
		t.Fatal(err)
	}
	if inner != nil {
		add(root+"/"+managedtools.ArchiveManifestName, inner, 0o644)
	}
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		mode := os.FileMode(0o644)
		if strings.HasPrefix(n, "bin/") && !strings.Contains(n, ".so") {
			mode = 0o755
		}
		// A Companion bundle's two programs, as the release archives carry them.
		if base := path.Base(n); base == "companion" || base == "companion.exe" || strings.HasPrefix(base, "auto-pigeon-mapio") {
			mode = 0o755
		}
		if strings.HasPrefix(n, "@link:") {
			header := &zip.FileHeader{Name: root + "/" + strings.TrimPrefix(n, "@link:")}
			header.SetMode(os.ModeSymlink | 0o777)
			w, _ := writer.CreateHeader(header)
			_, _ = w.Write(entries[n])
			continue
		}
		if strings.HasPrefix(n, "!") {
			add(strings.TrimPrefix(n, "!"), entries[n], mode)
			continue
		}
		add(root+"/"+n, entries[n], mode)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// --- the fixture program ------------------------------------------------------

var (
	programOnce  sync.Once
	programBytes []byte
	programErr   error
)

// fixtureSource is the stand-in compiler: named qbsp, vis, light, bspinfo,
// bsputil or q3map2 by its file name, it prints the banner the real one
// prints (read from FIXTURE_BANNER beside it, so one binary serves every
// version), the options the compatibility check looks for, and, given a
// .map and a .bsp, writes a BSP.
const fixtureSource = `package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	self, _ := os.Executable()
	name := strings.TrimSuffix(filepath.Base(self), ".exe")
	banner, _ := os.ReadFile(filepath.Join(filepath.Dir(self), "..", "README.md"))
	version := strings.TrimSpace(strings.TrimPrefix(string(banner), "fixture release "))
	stamp := ""
	if name == "q3map2" {
		stamp = version + "+gca2e281d.upstream.82161339"
		fmt.Println("Q3Map         - v1.0r (c) 1999 Id Software Inc.")
		fmt.Println("Q3Map (ydnar) - v" + stamp)
	} else {
		stamp = "2.0.0-alpha11-49-gd645c6f4+auto-pigeon." + version
		fmt.Println("---- " + name + " / ericw-tools " + stamp + " ----")
		fmt.Println("ericw-tools " + stamp)
		fmt.Println("usage: -logfile -threads -basedir -gamedir -fast -level -q2bsp -leaktest -wadpath")
	}
	var mapFile, bspFile string
	for _, arg := range os.Args[1:] {
		switch {
		case strings.HasSuffix(arg, ".map"):
			mapFile = arg
		case strings.HasSuffix(arg, ".bsp"):
			bspFile = arg
		}
	}
	if mapFile != "" && bspFile == "" {
		bspFile = strings.TrimSuffix(mapFile, ".map") + ".bsp"
	}
	if name == "qbsp" && mapFile != "" {
		source, err := os.ReadFile(mapFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		// "// AUCOM_HOLD_SECONDS n" in the map: a silent compile that takes
		// n seconds (at most 300), for a live test of work that is running.
		if _, rest, found := strings.Cut(string(source), "AUCOM_HOLD_SECONDS "); found {
			var seconds int
			fmt.Sscanf(rest, "%d", &seconds)
			if seconds > 300 {
				seconds = 300
			}
			time.Sleep(time.Duration(seconds) * time.Second)
		}
		// "// AUCOM_HOLD_UNTIL <file>": compile once that file exists (at
		// most 300 s), so a test holds a pipeline between two real stages.
		if _, rest, found := strings.Cut(string(source), "AUCOM_HOLD_UNTIL "); found {
			gate := strings.TrimSpace(strings.SplitN(rest, "\n", 2)[0])
			for deadline := time.Now().Add(300 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
				if _, err := os.Stat(gate); err == nil {
					break
				}
			}
		}
		_ = os.WriteFile(bspFile, append([]byte("BSP29 built by "+self+"\n"), source...), 0o644)
		_ = os.WriteFile(strings.TrimSuffix(bspFile, ".bsp")+".prt", []byte("PRT1\n"), 0o644)
	}
	if (name == "vis" || name == "light") && bspFile != "" {
		f, err := os.OpenFile(bspFile, os.O_APPEND|os.O_WRONLY, 0)
		if err == nil {
			fmt.Fprintln(f, name+" by "+self)
			f.Close()
		}
	}
}
`

// FixtureProgram is the compiled stand-in, for this machine.
func FixtureProgram(t testing.TB) []byte {
	programOnce.Do(func() {
		dir, err := os.MkdirTemp("", "aucom-managedtools-fixture-")
		if err != nil {
			programErr = err
			return
		}
		defer os.RemoveAll(dir)
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(fixtureSource), 0o644); err != nil {
			programErr = err
			return
		}
		_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixture\n\ngo 1.21\n"), 0o644)
		out := filepath.Join(dir, "fixture")
		command := exec.Command("go", "build", "-o", out, ".")
		command.Dir = dir
		command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=-mod=mod")
		if output, err := command.CombinedOutput(); err != nil {
			programErr = fmt.Errorf("building the fixture program: %v\n%s", err, output)
			return
		}
		programBytes, programErr = os.ReadFile(out)
	})
	if programErr != nil {
		t.Fatal(programErr)
	}
	return programBytes
}

// Digest is "sha256:<hex>" of b, for a Digests override.
func Digest(b []byte) *string {
	sum := sha256.Sum256(b)
	d := "sha256:" + hex.EncodeToString(sum[:])
	return &d
}
