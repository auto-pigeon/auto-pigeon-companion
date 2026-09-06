package job

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// A hermetic tool, and why it is this test binary.
//
// Every fixture in this package needs a real program: a real fork, a real exit
// status, real pipes, a real process group. A fake that returns canned bytes
// would leave the only interesting parts — the ones that go wrong on somebody
// else's machine — untested.
//
// Shipping a compiled program in the repository is out of the question, and
// building one at test time needs a toolchain and six platforms' worth of
// spelling. So the test binary re-executes itself: `go test` already produced
// an executable for this platform, and os.Args[0] is its path.
//
// It is selected by an *argument*, not an environment variable, because the
// executor deliberately does not pass its own environment to a job. That is the
// property under test, so the test cannot rely on breaking it.

const helperFlag = "-aucom-test-helper"

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == helperFlag {
		os.Exit(helperMain(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// helperMain is the fake tool. Each mode exists for one fixture.
func helperMain(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "helper: no mode")
		return 2
	}
	mode, rest := args[0], args[1:]
	switch mode {
	case "echo":
		fmt.Println(strings.Join(rest, " "))
	case "argv":
		// One argument per line, so a test can assert on exactly what argv the
		// process received rather than on a re-joined string.
		for _, arg := range rest {
			fmt.Println(arg)
		}
	case "exit":
		code, _ := strconv.Atoi(rest[0])
		fmt.Println("about to exit", code)
		return code
	case "stderr":
		fmt.Fprintln(os.Stderr, strings.Join(rest, " "))
	case "flood":
		count, _ := strconv.Atoi(rest[0])
		line := strings.Repeat("x", 99) + "\n"
		for written := 0; written < count; written += len(line) {
			os.Stdout.WriteString(line)
		}
	case "invalid-utf8":
		// A truncated multi-byte sequence, a lone continuation byte, and an
		// ANSI escape that would repaint the reader's terminal.
		os.Stdout.Write([]byte{'s', 't', 'a', 'r', 't', ' ', 0xff, 0xfe, 0x80, ' ', 0x1b, '[', '2', 'J', ' ', 'e', 'n', 'd', '\n'})
	case "sleep":
		seconds, _ := strconv.Atoi(rest[0])
		fmt.Println("sleeping")
		os.Stdout.Sync()
		time.Sleep(time.Duration(seconds) * time.Second)
		fmt.Println("woke")
	case "write":
		if err := os.MkdirAll(filepath.Dir(rest[0]), 0o700); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := os.WriteFile(rest[0], []byte(strings.Join(rest[1:], " ")), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("wrote", rest[0])
	case "symlink":
		// rest[0] is the link to create, rest[1] what it points at.
		_ = os.MkdirAll(filepath.Dir(rest[0]), 0o700)
		if err := os.Symlink(rest[1], rest[0]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("linked")
	case "spawn":
		// A grandchild that outlives this process and keeps the output pipe
		// open. The classic way a supervisor hangs forever on a finished job.
		seconds := rest[0]
		child := exec.Command(os.Args[0], helperFlag, "sleep", seconds)
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("spawned", child.Process.Pid)
		os.Stdout.Sync()
	case "dump-env":
		for _, entry := range os.Environ() {
			fmt.Println(entry)
		}
	case "cwd":
		dir, _ := os.Getwd()
		fmt.Println(dir)
	default:
		fmt.Fprintln(os.Stderr, "helper: unknown mode", mode)
		return 2
	}
	return 0
}

// helperPath is the program a fixture profile is pointed at.
func helperPath(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary: %v", err)
	}
	return path
}

// fixtureAction is one action of the fixture tool profile, as a JSON fragment.
type fixtureAction struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	Executable  string           `json:"executable"`
	Args        []any            `json:"args,omitempty"`
	WorkingDir  map[string]any   `json:"working_dir,omitempty"`
	Inputs      []map[string]any `json:"inputs,omitempty"`
	Outputs     []map[string]any `json:"outputs,omitempty"`
	Options     []map[string]any `json:"options,omitempty"`
	Diagnostics []map[string]any `json:"diagnostics,omitempty"`
	Roots       []map[string]any `json:"roots,omitempty"`
	Environment map[string]any   `json:"environment,omitempty"`
	SessionRole string           `json:"session_role,omitempty"`
	Timeout     int              `json:"timeout_seconds,omitempty"`
	SuccessCode []int            `json:"success_exit_codes,omitempty"`
}

// fixtureProfile builds a valid, user-authored tool profile around the helper.
//
// Built as JSON and decoded through profile.Decode, not constructed as Go
// values: a fixture that skipped the decoder would be testing a path no
// document takes.
func fixtureProfile(t *testing.T, id string, actions ...fixtureAction) []byte {
	t.Helper()
	document := map[string]any{
		"schema_version": "aucom.profile/1.0",
		"kind":           "tool",
		"id":             id,
		"version":        "1.0.0",
		"name":           "Executor test tool",
		"summary":        "A fixture profile that runs the test binary as an external program.",
		"publisher":      map[string]any{"name": "Auto-Pigeon tests"},
		"license":        map[string]any{"spdx": "MIT", "name": "MIT"},
		"tool_version":   "0.0.0-fixture",
		"platforms": []map[string]any{
			{"platform": map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH}, "status": "supported"},
		},
		"acquisition": []map[string]any{
			{"mode": "user_path", "title": "Point at a copy you already have", "hint": "choose the test binary"},
		},
		"executables": []map[string]any{
			{"name": "helper", "title": "The fixture program", "file": "helper{platform.exe_suffix}"},
		},
		"actions": actions,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the fixture profile: %v", err)
	}
	return encoded
}

// writeProfile puts a fixture profile in a catalog directory.
func writeProfile(t *testing.T, dir string, document []byte) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	name := filepath.Join(dir, "fixture.tool.json")
	if err := os.WriteFile(name, document, 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

// harness is a service wired to temporary directories, with the fixture profile
// installed and granted.
type harness struct {
	t        *testing.T
	service  *Service
	store    *Store
	dir      string
	profiles string
	grant    *grantSource
	// cancel stops the context the service was started with, which is what a
	// test uses to stand in for the Companion shutting down.
	cancel context.CancelFunc
}

// grantSource stands in for the binding store: it records that the fixture
// profile was reviewed, so profile.Authorize lets a `local` document run.
//
// A real grant, against the real digest, through the real Authorize. A harness
// that bypassed authorization would leave every fixture below testing a path
// that no profile a user installs can take.
type grantSource struct {
	bindings map[string]binding.LocalBinding
}

func (g *grantSource) lookup(profileID string) (binding.LocalBinding, bool) {
	local, found := g.bindings[profileID]
	return local, found
}

// grantEverything records the user having approved every permission a profile
// asks for.
func (g *grantSource) grantEverything(t *testing.T, entry CatalogEntry) {
	t.Helper()
	g.bindings[entry.Profile.Metadata().ID] = binding.LocalBinding{
		SchemaVersion: binding.SchemaVersion,
		ProfileID:     entry.Profile.Metadata().ID,
		ProfileDigest: entry.Digest,
		Trust:         entry.Trust,
		Grant: &profile.Grant{
			ProfileID: entry.Profile.Metadata().ID,
			Version:   entry.Profile.Metadata().Version,
			Digest:    entry.Digest,
			Trust:     entry.Trust,
			Granted:   profile.PermissionIDs(entry.Profile),
			GrantedAt: time.Now().UTC(),
		},
	}
}

func newHarness(t *testing.T, document []byte, options ...func(*Options)) *harness {
	t.Helper()
	dir := t.TempDir()
	profiles := filepath.Join(dir, "profiles")
	writeProfile(t, profiles, document)

	store, err := OpenStore(filepath.Join(dir, "jobs"))
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	catalog := NewCatalog(profiles)
	grants := &grantSource{bindings: map[string]binding.LocalBinding{}}
	entries, err := catalog.readDir()
	if err != nil {
		t.Fatalf("reading the fixture catalog: %v", err)
	}
	for _, entry := range entries {
		grants.grantEverything(t, entry)
	}

	opts := Options{
		Store:       store,
		Catalog:     catalog,
		Bindings:    grants.lookup,
		Concurrency: 2,
		Logf:        func(format string, args ...any) { t.Logf("service: "+format, args...) },
	}
	for _, apply := range options {
		apply(&opts)
	}
	service, err := NewService(opts)
	if err != nil {
		t.Fatalf("building the service: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := service.Start(ctx); err != nil {
		cancel()
		t.Fatalf("starting the service: %v", err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("closing the service: %v", err)
		}
		cancel()
	})
	return &harness{t: t, service: service, store: store, dir: dir, profiles: profiles, grant: grants, cancel: cancel}
}

// runToEnd submits a job and waits for it to finish.
func (h *harness) runToEnd(request Request) *Job {
	h.t.Helper()
	submitted, err := h.service.Submit(request)
	if err != nil {
		h.t.Fatalf("submitting: %v", err)
	}
	return h.waitFor(submitted.ID)
}

func (h *harness) waitFor(id string) *Job {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	finished, err := h.service.Wait(ctx, id)
	if err != nil {
		h.t.Fatalf("waiting for %s: %v", id, err)
	}
	return finished
}

func (h *harness) mustLog(id, stream string) string {
	h.t.Helper()
	data, err := h.service.Logs(id, stream, false)
	if err != nil {
		h.t.Fatalf("reading the %s log: %v", stream, err)
	}
	return string(data)
}

// helperRequest is the common shape: run the fixture action with the test
// binary as the tool.
func (h *harness) helperRequest(profileID, actionID string) Request {
	return Request{
		ProfileID:   profileID,
		ActionID:    actionID,
		Executables: map[string]string{"helper": helperPath(h.t)},
	}
}

// waitForState blocks until a job reaches a state, or the test's patience runs
// out. Polling the store rather than an in-memory signal is deliberate: it is
// the same thing a second process sees.
func waitForState(t *testing.T, h *harness, id string, want State) *Job {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		j, err := h.service.Get(id)
		if err == nil {
			if j.State == want {
				return j
			}
			if j.State.Terminal() {
				t.Fatalf("job %s reached %s before %s (error: %s)", id, j.State, want, j.Error)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	j, _ := h.service.Get(id)
	if j != nil {
		t.Fatalf("job %s was still %s after 30s, waiting for %s", id, j.State, want)
	}
	t.Fatalf("job %s never appeared, waiting for %s", id, want)
	return nil
}

// fixtureEngineProfile builds an engine profile around the helper.
//
// Engine rather than tool because `{runtime.*}` — the values a user types into
// a box, and the only place an arbitrary string reaches an argument array — is
// an engine-only namespace. The injection fixtures need that path, and a tool
// profile that tried to use it would be refused by the validator.
func fixtureEngineProfile(t *testing.T, id string, actions ...fixtureAction) []byte {
	t.Helper()
	document := map[string]any{
		"schema_version": "aucom.profile/1.0",
		"kind":           "engine",
		"id":             id,
		"version":        "1.0.0",
		"name":           "Executor test engine",
		"summary":        "A fixture engine profile that runs the test binary as an external program.",
		"publisher":      map[string]any{"name": "Auto-Pigeon tests"},
		"license":        map[string]any{"spdx": "MIT", "name": "MIT"},
		"game_profile":   map[string]any{"slug": "quake1", "engine_family": "quake1"},
		"runtime":        "test-engine",
		"engine_version": "0.0.0-fixture",
		"platforms": []map[string]any{
			{"platform": map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH}, "status": "supported"},
		},
		"acquisition": []map[string]any{
			{"mode": "user_path", "title": "Point at an engine you already have", "hint": "choose the test binary"},
		},
		"executables": []map[string]any{
			{"name": "helper", "title": "The fixture program", "file": "helper{platform.exe_suffix}"},
		},
		"actions": actions,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the fixture engine profile: %v", err)
	}
	return encoded
}
