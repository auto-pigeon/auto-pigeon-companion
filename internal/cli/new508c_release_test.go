package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	mt "github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools/managedtoolstest"
)

// NEW_508C, on the real `main` of a real unpacked release (the harness of
// firstrun_release_test.go): where a portable Companion keeps its build tools,
// and one Update that installs both toolchains and restarts into a newer
// Companion, followed in the real page.

// C4: a release in a folder whose path has spaces, started from another
// working directory, keeps its tools beside itself — and puts there what an
// earlier copy had installed in the per-user folder, again after the whole
// folder is moved; a folder it cannot write is said, with its path.
func TestAReleaseKeepsItsToolsBesideItselfWhereverItWasStartedFromAndWhereverItMoves(t *testing.T) {
	f := newFirstRun(t)
	spaced := filepath.Join(filepath.Dir(f.bundle), "My Games", "Auto Pigeon Companion "+firstRunVersion)
	if err := os.MkdirAll(filepath.Dir(spaced), 0o755); err != nil {
		t.Fatal(err)
	}
	renameFolder(t, f.bundle, spaced)
	f.bundle = spaced
	f.dir = t.TempDir() // started from somewhere else entirely

	// An earlier copy installed both toolchains in the per-user folder.
	perUser := filepath.Join(f.home, "data", "auto-pigeon-companion", "tools")
	f.serve("AUCOM_TOOLS_DIR=" + perUser)
	f.checked()
	if code, raw := f.api(http.MethodPost, "/api/v1/tools/install", `{"tools":["ericw-tools","q3map2"]}`); code != http.StatusOK {
		t.Fatalf("install = %d %s", code, raw)
	}
	status := f.until("both tools in the per-user folder", bothReadyUnder(perUser))
	old := map[string]string{}
	for _, tool := range status.Tools {
		if tool.Active != nil && tool.ID != "companion" {
			old[tool.ID] = tool.Active.Dir
		}
	}
	f.stop()

	// This copy, with nothing chosen: the folder beside it, and the
	// installations re-made there from the verified releases.
	beside := filepath.Join(f.bundle, managedtools.BuildToolsDirName)
	f.serve()
	if got := f.status().Root; got != beside {
		t.Fatalf("the tools folder is %q; want %q beside the bundle", got, beside)
	}
	f.until("both tools beside the bundle", bothReadyUnder(beside))
	for tool, dir := range old {
		if _, err := managedtools.ReadInstall(dir); err != nil {
			t.Errorf("the per-user %s installation was touched: %v", tool, err)
		}
	}
	f.stop()

	// The whole folder moved: the tools follow it.
	moved := filepath.Join(filepath.Dir(f.bundle), "moved here", filepath.Base(f.bundle))
	if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
		t.Fatal(err)
	}
	renameFolder(t, f.bundle, moved)
	f.bundle = moved
	movedBeside := filepath.Join(moved, managedtools.BuildToolsDirName)
	f.serve()
	if got := f.status().Root; got != movedBeside {
		t.Fatalf("after the move the tools folder is %q; want %q", got, movedBeside)
	}
	f.until("both tools beside the moved bundle", bothReadyUnder(movedBeside))
	f.stop()

	// A folder it cannot write: still its folder, and said.
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Log("read-only folder case skipped: needs POSIX permissions and a user they apply to")
		return
	}
	if err := os.RemoveAll(movedBeside); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(moved, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(moved, 0o755) })
	f.serve()
	var body struct {
		Root      string `json:"root"`
		RootError string `json:"root_error"`
	}
	_, raw := f.api(http.MethodGet, "/api/v1/tools", "")
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.Root != movedBeside || !strings.Contains(body.RootError, movedBeside) {
		t.Fatalf("a read-only bundle folder: root %q, error %q; want the folder beside it and the reason", body.Root, body.RootError)
	}
}

// renameFolder moves a whole release folder the way a person does after quitting the Companion.
//
// On Windows a folder whose files were just in use cannot be renamed for a moment after the
// process that used them has exited: the handles of an exiting process, and Defender scanning the
// tool binaries the Companion just installed, refuse the move with "Access is denied" (seen on the
// windows-latest runner, 2026-11-12, while the same run passed on Linux and macOS and the previous
// Windows run passed). The move is retried for a bounded time; a lock that never goes away still
// fails here, naming the error.
func renameFolder(t *testing.T, from, to string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for wait := 100 * time.Millisecond; ; wait = min(2*wait, 2*time.Second) {
		err := os.Rename(from, to)
		if err == nil {
			return
		}
		if runtime.GOOS != "windows" || time.Now().After(deadline) {
			t.Fatalf("moving the release folder: %v", err)
		}
		time.Sleep(wait)
	}
}

func bothReadyUnder(dir string) func(firstRunStatus) bool {
	return func(status firstRunStatus) bool {
		ready := 0
		for _, tool := range status.Tools {
			if tool.ID != "companion" && tool.Phase == "ready" && tool.Active != nil &&
				strings.HasPrefix(tool.Active.Dir, dir+string(filepath.Separator)) {
				ready++
			}
		}
		return ready == 2
	}
}

// stop ends the serving process and waits for it.
func (f *firstRun) stop() {
	f.t.Helper()
	_ = f.serving.Process.Kill()
	_ = f.serving.Wait()
	// The next start writes a token of its own; serve must not read this one.
	_ = os.Remove(filepath.Join(f.configDir(), "api-token"))
}

// C3 + C2 + C4: the sidebar's Update in the real page, against a catalogue that
// offers all three. Both toolchains become active at the newest offered
// versions first; then the Companion puts its own update in place and comes
// back as the new version, the tools still beside it. The dialog stays open
// with an indeterminate bar and the stage it is in for the 30-odd seconds the
// downloads are held, across the restart, and ends on the verified result.
func TestTheSidebarUpdateInstallsBothToolchainsThenRestartsIntoTheNewCompanion(t *testing.T) {
	f := newFirstRun(t)
	newer := buildCompanionVersion(t, "1.901")
	// The new bundle's converter is the real one: the staged Companion's
	// proving start asks it which protocol it speaks.
	converter := buildProgram(t, "mapio-1.245", "./cmd/mapio", "-X main.version=1.245")
	f.releases.Publish(mt.CompanionRepo, mt.BuildCompanion(t, mt.CompanionSpec{Version: "1.901", Companion: newer, Converter: converter, Prerelease: true}))
	beside := filepath.Join(f.bundle, managedtools.BuildToolsDirName)
	f.serve()
	t.Cleanup(func() { f.quit() })
	f.checked()

	hold := make(chan struct{})
	f.releases.Mutate(func() { f.releases.Hold = hold })
	released := false
	release := func() {
		if !released {
			released = true
			close(hold)
		}
	}
	t.Cleanup(release)

	type sample struct {
		At            int64    `json:"at"`
		Load          int      `json:"load"`
		Open          bool     `json:"open"`
		Stage         string   `json:"stage"`
		Indeterminate bool     `json:"indeterminate"`
		Rows          []string `json:"rows"`
		Texts         []string `json:"texts"`
		Done          bool     `json:"done"`
		Result        string   `json:"result"`
		Stopped       bool     `json:"stopped"`
	}
	var report struct {
		Sidebar *struct {
			Text        string `json:"text"`
			DescribedBy string `json:"describedBy"`
			LeftInNav   bool   `json:"leftInNav"`
			BelowAreas  bool   `json:"belowAreas"`
		} `json:"sidebar"`
		Samples []sample `json:"samples"`
		Loads   int      `json:"loads"`
		Error   string   `json:"error"`
	}
	reports := make(chan []byte, 1)
	browser := f.driveInBrowser("new508cupdate.js", reports)
	heldFrom := time.Now()
	if !browser {
		t.Log("no Chrome-family browser here: the Update is pressed through the API the button calls")
		if code, raw := f.api(http.MethodPost, "/api/v1/tools/update", "{}"); code != http.StatusOK {
			t.Fatalf("POST /api/v1/tools/update = %d %s", code, raw)
		}
	}
	// The downloads stay held for longer than 30 seconds in all — each one for
	// less than the client's 30-second wait for response headers, which a real
	// GitHub answers in well under a second: EricW for 20 s, then q3map2.
	f.until("a held download", func(firstRunStatus) bool { return len(f.archivesFetched()) > 0 })
	time.Sleep(20*time.Second - time.Since(heldFrom))
	second := make(chan struct{})
	release()
	f.releases.Mutate(func() { f.releases.Hold = second })
	f.until("the second held download", func(firstRunStatus) bool { return len(f.archivesFetched()) > 1 })
	time.Sleep(20 * time.Second)
	f.releases.Mutate(func() { f.releases.Hold = nil })
	close(second)

	// The old process stops to be replaced; the new one answers on the same
	// address with a token of its own.
	deadline := time.Now().Add(3 * time.Minute)
	var version string
	for time.Now().Before(deadline) {
		f.rereadToken()
		if code, raw := f.api(http.MethodGet, "/api/status", ""); code == http.StatusOK {
			var answer struct {
				Version string `json:"version"`
			}
			_ = json.Unmarshal(raw, &answer)
			if version = answer.Version; version == "1.901" {
				break
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if version != "1.901" {
		updateLog, _ := os.ReadFile(filepath.Join(f.configDir(), SelfUpdateLogName))
		t.Fatalf("the Companion did not come back as 1.901 (last answer %q); self-update log:\n%s", version, updateLog)
	}
	if updateLog, err := os.ReadFile(filepath.Join(f.configDir(), SelfUpdateLogName)); err == nil {
		t.Logf("self-update log:\n%s", updateLog)
	}
	var after struct {
		Tools []struct {
			ID      string `json:"id"`
			Version string `json:"version"`
			Active  *struct {
				Dir     string `json:"dir"`
				Version string `json:"version"`
			} `json:"active"`
			Self *struct {
				Last *struct {
					Outcome string `json:"outcome"`
					From    string `json:"from"`
					To      string `json:"to"`
				} `json:"last"`
			} `json:"self"`
		} `json:"tools"`
	}
	_, raw := f.api(http.MethodGet, "/api/v1/tools", "")
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"ericw-tools": "1.4566", "q3map2": "2.5.17n-ap.3031"}
	for _, tool := range after.Tools {
		switch tool.ID {
		case "companion":
			if tool.Version != "1.901" || tool.Self == nil || tool.Self.Last == nil || tool.Self.Last.Outcome != "updated" {
				t.Errorf("the Companion after its update: %+v", tool)
			}
		default:
			if tool.Active == nil || tool.Active.Version != want[tool.ID] || !strings.HasPrefix(tool.Active.Dir, beside+string(filepath.Separator)) {
				t.Errorf("%s after the Update: %+v; want %s beside the bundle", tool.ID, tool.Active, want[tool.ID])
			}
		}
	}
	for name, count := range f.archivesFetched() {
		if count != 1 {
			t.Errorf("%s was downloaded %d times; one operation downloads each once", name, count)
		}
	}
	journal, _ := os.ReadFile(filepath.Join(f.bundle, ".companion-update", "journal.json"))
	t.Logf("self-update journal: %s", strings.TrimSpace(string(journal)))

	if !browser {
		return
	}
	select {
	case raw := <-reports:
		if err := json.Unmarshal(raw, &report); err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Minute):
		t.Fatal("the page never reported")
	}
	if report.Error != "" {
		t.Fatalf("the page: %s (after %d samples)", report.Error, len(report.Samples))
	}
	if s := report.Sidebar; s == nil || s.Text != "Update" || !strings.Contains(s.DescribedBy, "installs the current official versions") || !s.LeftInNav || !s.BelowAreas {
		t.Errorf("the sidebar's Update: %+v", report.Sidebar)
	}
	if report.Loads < 2 {
		t.Errorf("the page loaded %d times; the restart should have reloaded it once", report.Loads)
	}
	var heldOpen time.Duration
	var first, last *sample
	for i := range report.Samples {
		s := &report.Samples[i]
		if s.Stopped {
			t.Errorf("the stopped screen replaced the Update dialog at %d: %+v", i, s)
		}
		if s.Load == 1 && s.Open && s.Indeterminate && !s.Done && s.Stage != "" {
			if first == nil {
				first = s
			}
			heldOpen = time.Duration(s.At-first.At) * time.Millisecond
		}
		if s.Load == 1 && !s.Open && first != nil && !s.Done {
			t.Errorf("the dialog closed before the restart at %d: %+v", i, s)
		}
		last = s
	}
	if heldOpen < 30*time.Second {
		t.Errorf("the dialog showed the stage with an indeterminate bar for %s; want at least 30 s", heldOpen)
	}
	if last == nil || !last.Done || last.Result != "The newest verified versions are installed." {
		t.Fatalf("the last sample: %+v", last)
	}
	for _, row := range []string{"companion:installed", "ericw-tools:installed", "q3map2:installed"} {
		if !hasString(last.Rows, row) {
			t.Errorf("the final rows %v lack %s", last.Rows, row)
		}
	}
	t.Logf("first held sample: %+v", first)
	t.Logf("last sample: %+v (%d samples, %d loads)", last, len(report.Samples), report.Loads)
	stages := map[string]bool{}
	for _, s := range report.Samples {
		stages[s.Stage] = true
	}
	t.Logf("stages shown: %v", sortedKeys(stages))
}

func hasString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func sortedKeys(set map[string]bool) []string {
	var out []string
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// rereadToken picks up the token a restarted Companion wrote.
func (f *firstRun) rereadToken() {
	if raw, err := os.ReadFile(filepath.Join(f.configDir(), "api-token")); err == nil && len(raw) > 0 {
		f.token = strings.TrimSpace(string(raw))
	}
}

// quit asks whatever Companion answers on the address to quit: the relaunched
// one is not this test's child.
func (f *firstRun) quit() {
	f.rereadToken()
	_, _ = f.api(http.MethodPost, "/api/lifecycle/quit", `{}`)
}

var companionVersions = struct {
	sync.Mutex
	built map[string][]byte
}{built: map[string][]byte{}}

// buildCompanionVersion builds the Companion once per test binary at version.
func buildCompanionVersion(t *testing.T, version string) []byte {
	t.Helper()
	fixtureRelease(t) // the same skips
	return buildProgram(t, "companion-"+version, "./cmd/companion", "-X main.version="+version, "aucom_fixture_releases")
}

// buildProgram builds one program of this module once per test binary.
func buildProgram(t *testing.T, key, pkg, ldflags string, tags ...string) []byte {
	t.Helper()
	companionVersions.Lock()
	defer companionVersions.Unlock()
	if raw, ok := companionVersions.built[key]; ok {
		return raw
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "program")
	args := []string{"build"}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	build := exec.Command("go", append(args, "-ldflags", ldflags, "-o", path, pkg)...)
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building %s: %v\n%s", key, err, output)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	companionVersions.built[key] = raw
	return raw
}

// driveInBrowser opens the real page through a proxy that appends the named
// driver script, and forwards the report it posts. It returns at once; false
// with no Chrome-family browser on this machine.
func (f *firstRun) driveInBrowser(script string, reports chan<- []byte) bool {
	t := f.t
	t.Helper()
	browser := os.Getenv("AUCOM_TEST_BROWSER")
	for _, candidate := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "chrome"} {
		if browser == "" {
			if path, err := exec.LookPath(candidate); err == nil {
				browser = path
			}
		}
	}
	if browser == "" {
		return false
	}
	driver, err := os.ReadFile(filepath.Join("testdata", script))
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: f.address})
	proxy.ModifyResponse = func(response *http.Response) error {
		if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") {
			return nil
		}
		page, err := io.ReadAll(response.Body)
		if err != nil {
			return err
		}
		page = []byte(strings.Replace(string(page), "</body>", `<script src="/journey/drive.js"></script></body>`, 1))
		response.Body = io.NopCloser(strings.NewReader(string(page)))
		response.ContentLength = int64(len(page))
		response.Header.Set("Content-Length", strconv.Itoa(len(page)))
		return nil
	}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/journey/drive.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(driver)
		case "/journey/report":
			raw, _ := io.ReadAll(r.Body)
			select {
			case reports <- raw:
			default:
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			proxy.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(front.Close)
	profileDir, err := os.MkdirTemp("", "aucom-new508c-browser-")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	command := exec.CommandContext(ctx, browser, "--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--no-first-run", "--no-default-browser-check", "--disable-extensions", "--disable-background-networking",
		"--disable-component-update", "--password-store=basic", "--window-size=1280,900", "--user-data-dir="+profileDir, front.URL+"/")
	command.WaitDelay = 15 * time.Second
	var output strings.Builder
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		cancel()
		t.Skipf("could not start %s: %v", browser, err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
		cancel()
		_ = os.RemoveAll(profileDir)
	})
	return true
}

// C2: an Update whose new Companion does not come up ends on a distinct, true
// outcome in the same dialog — the version that runs is the old one, the
// toolchains it installed stay installed — and never on "up to date".
func TestASidebarUpdateWhoseNewCompanionDoesNotServeSaysSoAndKeepsTheOldOne(t *testing.T) {
	f := newFirstRun(t)
	newer := buildCompanionVersion(t, "1.901")
	converter := buildProgram(t, "mapio-1.245", "./cmd/mapio", "-X main.version=1.245")
	f.releases.Publish(mt.CompanionRepo, mt.BuildCompanion(t, mt.CompanionSpec{Version: "1.901", Companion: newer, Converter: converter, Prerelease: true}))
	f.serve("AUCOM_FIXTURE_FAIL_SERVE=1.901")
	t.Cleanup(func() { f.quit() })
	f.checked()
	reports := make(chan []byte, 1)
	browser := f.driveInBrowser("new508cupdate.js", reports)
	if !browser {
		if code, raw := f.api(http.MethodPost, "/api/v1/tools/update", "{}"); code != http.StatusOK {
			t.Fatalf("POST /api/v1/tools/update = %d %s", code, raw)
		}
	}
	var last struct {
		Outcome, From, To, Detail string
	}
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		f.rereadToken()
		if code, raw := f.api(http.MethodGet, "/api/v1/tools", ""); code == http.StatusOK {
			var body struct {
				Tools []struct {
					ID      string `json:"id"`
					Version string `json:"version"`
					Failure *struct {
						Version string `json:"version"`
						Error   string `json:"error"`
					} `json:"failure"`
					Self *struct {
						Last *struct {
							Outcome string `json:"outcome"`
							From    string `json:"from"`
							To      string `json:"to"`
							Detail  string `json:"detail"`
						} `json:"last"`
					} `json:"self"`
				} `json:"tools"`
			}
			_ = json.Unmarshal(raw, &body)
			for _, tool := range body.Tools {
				if tool.ID != "companion" || tool.Version != "1.900" {
					continue
				}
				if tool.Self != nil && tool.Self.Last != nil && tool.Self.Last.To == "1.901" {
					last.Outcome, last.From, last.To, last.Detail = tool.Self.Last.Outcome, tool.Self.Last.From, tool.Self.Last.To, tool.Self.Last.Detail
				} else if tool.Failure != nil && tool.Failure.Version == "1.901" {
					last.Outcome, last.To, last.Detail = "failed_before_restart", "1.901", tool.Failure.Error
				}
			}
			if last.Outcome != "" && last.Outcome != "activated" {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if last.Outcome == "" || last.Outcome == "updated" || last.Outcome == "activated" {
		updateLog, _ := os.ReadFile(filepath.Join(f.configDir(), SelfUpdateLogName))
		t.Fatalf("no failure recorded for 1.901 with 1.900 running: %+v\n%s", last, updateLog)
	}
	t.Logf("the failed update: %+v", last)
	if !browser {
		return
	}
	var report struct {
		Samples []struct {
			Load    int      `json:"load"`
			Rows    []string `json:"rows"`
			Texts   []string `json:"texts"`
			Done    bool     `json:"done"`
			Result  string   `json:"result"`
			Stage   string   `json:"stage"`
			Stopped bool     `json:"stopped"`
		} `json:"samples"`
		Error string `json:"error"`
	}
	select {
	case raw := <-reports:
		if err := json.Unmarshal(raw, &report); err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Minute):
		t.Fatal("the page never reported")
	}
	if report.Error != "" || len(report.Samples) == 0 {
		t.Fatalf("the page: %q (%d samples)", report.Error, len(report.Samples))
	}
	end := report.Samples[len(report.Samples)-1]
	if !end.Done || !hasString(end.Rows, "companion:failed") || end.Stage != "Update finished with problems" ||
		end.Result != "What failed is listed above; the version in use was kept." {
		t.Fatalf("the dialog's end: %+v", end)
	}
	for _, row := range []string{"ericw-tools:installed", "q3map2:installed"} {
		if !hasString(end.Rows, row) {
			t.Errorf("the toolchains it installed: rows %v lack %s", end.Rows, row)
		}
	}
	t.Logf("the dialog's end: %+v", end)
}
