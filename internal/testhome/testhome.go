// Package testhome gives a test binary a home of its own.
//
// NEW_508A, issue #5. Two packages' tests — internal/cli and internal/web —
// reached the per-user directories the operating system hands out: the
// request-intent lock guards in the configuration directory, `play-runs` and
// `assets` in the cache directory, a browser's downloads in ~/Downloads and
// the link handler's mimeapps.list in the data directory. Run by a developer,
// that is the developer's real Companion profile. A fixture that passes its
// own directories to the code under test is not enough, because the next test
// that forgets to is silent; so the whole process is moved instead, once, in
// TestMain, before any test runs.
//
// It is a package and not a _test file because two packages need the same
// rule, and a rule with two copies is one that drifts.
package testhome

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Home is an isolated set of per-user directories for one test process.
type Home struct {
	// Root holds everything: the home, and the config, cache and data
	// directories under it.
	Root string
	// watched are the real per-user Companion directories, with the newest
	// modification time anywhere under each when the process was moved.
	watched map[string]time.Time
}

// program is the directory name the Companion uses under each per-user base.
const program = "auto-pigeon-companion"

// Isolate moves this process to a fresh home and returns it. Every variable
// os.UserHomeDir, os.UserConfigDir and os.UserCacheDir consult is set, on
// every platform this builds for: setting only HOME would contain a POSIX run
// and leave a Windows one in the real profile.
//
// The Go toolchain's own directories are pinned to where they already are
// first, so a test that builds a helper keeps the build and module caches
// instead of filling new ones under the temporary home.
func Isolate() (*Home, error) {
	home := &Home{watched: map[string]time.Time{}}
	for _, base := range realBases() {
		directory := filepath.Join(base, program)
		home.watched[directory] = newest(directory)
	}
	pinToolchain()

	root, err := os.MkdirTemp("", "aucom-test-home-")
	if err != nil {
		return nil, err
	}
	home.Root = root
	for name, value := range map[string]string{
		"HOME":            root,
		"USERPROFILE":     root,
		"XDG_CONFIG_HOME": filepath.Join(root, "config"),
		"XDG_CACHE_HOME":  filepath.Join(root, "cache"),
		"XDG_DATA_HOME":   filepath.Join(root, "data"),
		"XDG_STATE_HOME":  filepath.Join(root, "state"),
		"AppData":         filepath.Join(root, "config"),
		"LocalAppData":    filepath.Join(root, "cache"),
	} {
		if err := os.Setenv(name, value); err != nil {
			return nil, err
		}
	}
	for _, directory := range []string{"config", "cache", "data", "state", "Downloads"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			return nil, err
		}
	}
	return home, nil
}

// Contains reports whether path is inside the isolated home.
func (h *Home) Contains(path string) bool {
	rel, err := filepath.Rel(h.Root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Touched names every real per-user Companion directory that changed since
// Isolate: created, or with something in it written. Empty is the pass.
func (h *Home) Touched() []string {
	var touched []string
	for directory, before := range h.watched {
		if after := newest(directory); after.After(before) {
			touched = append(touched, fmt.Sprintf("%s (newest change %s, was %s)", directory,
				after.Format(time.RFC3339Nano), before.Format(time.RFC3339Nano)))
		}
	}
	return touched
}

// Close removes the isolated home.
func (h *Home) Close() { _ = os.RemoveAll(h.Root) }

// Run is the whole of a TestMain that needs nothing else: isolate, run, check
// that the real profile was left alone, clean up. A run that touched it fails,
// whatever its tests said.
func Run(run func() int) int {
	home, err := Isolate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "testhome:", err)
		return 1
	}
	defer home.Close()
	code := run()
	if touched := home.Touched(); len(touched) > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: this test run wrote to the real per-user Companion directories:\n  %s\n",
			strings.Join(touched, "\n  "))
		if code == 0 {
			code = 1
		}
	}
	return code
}

// realBases are the per-user bases as the process started with them.
func realBases() []string {
	var bases []string
	if base, err := os.UserConfigDir(); err == nil {
		bases = append(bases, base)
	}
	if base, err := os.UserCacheDir(); err == nil {
		bases = append(bases, base)
	}
	if home, err := os.UserHomeDir(); err == nil {
		bases = append(bases, filepath.Join(home, ".local", "share"))
	}
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		bases = append(bases, base)
	}
	return bases
}

// newest is the latest modification time of directory or anything under it,
// and the zero time when it does not exist.
func newest(directory string) time.Time {
	var latest time.Time
	_ = filepath.WalkDir(directory, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := entry.Info(); err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})
	return latest
}

// pinToolchain fixes the Go toolchain's directories at their present places,
// which are all derived from the home this process is about to leave.
func pinToolchain() {
	set := func(name, value string) {
		if os.Getenv(name) == "" && value != "" {
			_ = os.Setenv(name, value)
		}
	}
	if cache, err := os.UserCacheDir(); err == nil {
		set("GOCACHE", filepath.Join(cache, "go-build"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		set("GOPATH", filepath.Join(home, "go"))
	}
	if path := os.Getenv("GOPATH"); path != "" {
		set("GOMODCACHE", filepath.Join(filepath.SplitList(path)[0], "pkg", "mod"))
	}
	if config, err := os.UserConfigDir(); err == nil {
		set("GOENV", filepath.Join(config, "go", "env"))
	}
}
