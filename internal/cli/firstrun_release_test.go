package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	mt "github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools/managedtoolstest"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// The first start of a release, end to end, with nothing set by hand.
//
// v1.352 shipped a wiring omission that hand-set unit dependencies hid, and
// `AskFirst: true` in newManagedTools and `useBundleTools` in cli.Run were then
// proved only by native fixture rows. So these tests set neither. They build
// the real program, lay it out as an unpacked release, and start its `main`
// with a task-owned configuration: what makes it a portable bundle, where its
// tools go and whether it asks is decided by the code a release runs. The only
// difference from a release is the `aucom_fixture_releases` tag, which points
// the release source at a loopback stand-in.

var firstRunProgram struct {
	sync.Mutex
	dir  string
	path string
	err  error
}

const firstRunVersion = "1.900"

// fixtureRelease builds the program once per test binary: every test copies it
// into a bundle of its own.
func fixtureRelease(t *testing.T) []byte {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the Companion")
	}
	if runtime.GOOS == "darwin" {
		t.Skip("a macOS application is replaced by hand: it has no portable bundle and no folder beside it")
	}
	program := &firstRunProgram
	program.Lock()
	defer program.Unlock()
	if program.path == "" && program.err == nil {
		dir, err := os.MkdirTemp("", "aucom-first-run-")
		if err != nil {
			t.Fatal(err)
		}
		program.dir, program.path = dir, filepath.Join(dir, "companion")
		build := exec.Command("go", "build", "-tags", "aucom_fixture_releases",
			"-ldflags", "-X main.version="+firstRunVersion, "-o", program.path, "./cmd/companion")
		build.Dir = filepath.Join("..", "..")
		if output, err := build.CombinedOutput(); err != nil {
			program.err = fmt.Errorf("%v\n%s", err, output)
		}
	}
	if program.err != nil {
		t.Fatal(program.err)
	}
	raw, err := os.ReadFile(program.path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// firstRun is one machine that has never run the Companion: an unpacked
// release, its own home, and a stand-in for GitHub.
type firstRun struct {
	t        *testing.T
	bundle   string
	home     string
	releases *mt.Server
	archives map[string]string // tool to the archive this machine would download
	serving  *exec.Cmd
	address  string
	token    string
}

func newFirstRun(t *testing.T) *firstRun {
	t.Helper()
	program := fixtureRelease(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &firstRun{t: t, bundle: filepath.Join(base, "auto-pigeon-companion-"+firstRunVersion), home: filepath.Join(base, "home"),
		releases: mt.NewServer(t), archives: map[string]string{}}
	for path, body := range mt.CompanionEntries(t, mt.CompanionSpec{Version: firstRunVersion, Companion: program}) {
		target := filepath.Join(f.bundle, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, body, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for tool, version := range map[managedtools.ToolID]string{managedtools.EricW: "1.4566", managedtools.Q3Map2: "2.5.17n-ap.3031"} {
		found, _ := managedtools.Find(tool)
		f.releases.Publish(found.Owner+"/"+found.Repo, mt.Build(t, mt.Spec{Tool: tool, Version: version, Prerelease: true}))
		f.archives[string(tool)] = mt.ArchiveName(found, version, mt.ThisTarget())
	}
	return f
}

func (f *firstRun) configDir() string {
	return filepath.Join(f.home, "config", "auto-pigeon-companion")
}

// environment is this process's, minus everything that would decide the
// question: the test binary turns managed tools off and owns XDG_DATA_HOME.
func (f *firstRun) environment(extra ...string) []string {
	var kept []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(strings.ToUpper(entry), "=")
		if !strings.HasPrefix(name, "AUCOM_") && !strings.HasPrefix(name, "XDG_") {
			kept = append(kept, entry)
		}
	}
	return append(append(kept,
		"HOME="+f.home, "USERPROFILE="+f.home,
		"XDG_CONFIG_HOME="+filepath.Join(f.home, "config"), "XDG_DATA_HOME="+filepath.Join(f.home, "data"),
		"XDG_CACHE_HOME="+filepath.Join(f.home, "cache"),
		"AppData="+filepath.Join(f.home, "config"), "LocalAppData="+filepath.Join(f.home, "data"),
		"AUCOM_FIXTURE_RELEASES_URL="+f.releases.URL), extra...)
}

// serve starts the program in the bundle and waits until it answers.
func (f *firstRun) serve(extra ...string) {
	t := f.t
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	f.address = "127.0.0.1:" + strconv.Itoa(port)
	program := filepath.Join(f.bundle, "companion")
	if runtime.GOOS == "windows" {
		program += ".exe"
	}
	log, err := os.Create(filepath.Join(t.TempDir(), "serve.log"))
	if err != nil {
		t.Fatal(err)
	}
	f.serving = exec.Command(program, "serve", "--managed-tools", "on", "--port", strconv.Itoa(port))
	f.serving.Env = f.environment(extra...)
	f.serving.Stdout, f.serving.Stderr = log, log
	if err := f.serving.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = f.serving.Process.Kill()
		_ = f.serving.Wait()
		log.Close()
		if t.Failed() {
			raw, _ := os.ReadFile(log.Name())
			t.Logf("serve said:\n%s", raw)
		}
	})
	deadline := time.Now().Add(30 * time.Second)
	for {
		raw, err := os.ReadFile(filepath.Join(f.configDir(), "api-token"))
		if err == nil && len(raw) > 0 {
			f.token = strings.TrimSpace(string(raw))
			if status, _ := f.api(http.MethodGet, "/api/status", ""); status == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the program did not come up on %s", f.address)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (f *firstRun) api(method, path, body string) (int, []byte) {
	request, err := http.NewRequest(method, "http://"+f.address+path, strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	request.Header.Set("X-AUCOM-Token", f.token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, nil
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.StatusCode, raw
}

// firstRunStatus is the part of /api/v1/tools the page acts on.
type firstRunStatus struct {
	Root      string `json:"root"`
	SettingUp bool   `json:"setting_up"`
	Tools     []struct {
		ID              string `json:"id"`
		Kind            string `json:"kind"`
		Phase           string `json:"phase"`
		AwaitingInstall bool   `json:"awaiting_install"`
		Active          *struct {
			Dir    string `json:"dir"`
			SHA256 string `json:"actual_sha256"`
		} `json:"active"`
		Check struct {
			LastSuccess time.Time `json:"last_success"`
		} `json:"check"`
	} `json:"tools"`
}

func (f *firstRun) status() firstRunStatus {
	f.t.Helper()
	code, raw := f.api(http.MethodGet, "/api/v1/tools", "")
	if code != http.StatusOK {
		f.t.Fatalf("GET /api/v1/tools = %d %s", code, raw)
	}
	var status firstRunStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		f.t.Fatal(err)
	}
	return status
}

// buildTools are the two toolchains in a status, the Companion's own row aside.
func (f *firstRun) buildTools(status firstRunStatus) map[string]bool {
	awaiting := map[string]bool{}
	for _, tool := range status.Tools {
		if tool.ID != "companion" {
			awaiting[tool.ID] = tool.AwaitingInstall
		}
	}
	return awaiting
}

func (f *firstRun) until(what string, done func(firstRunStatus) bool) firstRunStatus {
	f.t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for {
		status := f.status()
		if done(status) {
			return status
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("%s did not happen: %+v", what, status)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// checked waits for a real check of both tools, so "nothing was installed" is
// said after the coordinator knew what it could install, and not before.
func (f *firstRun) checked() firstRunStatus {
	f.t.Helper()
	if code, raw := f.api(http.MethodPost, "/api/v1/tools/check", "{}"); code != http.StatusOK {
		f.t.Fatalf("a check = %d %s", code, raw)
	}
	return f.until("a check of both tools", func(status firstRunStatus) bool {
		for _, tool := range status.Tools {
			if tool.ID != "companion" && tool.Check.LastSuccess.IsZero() {
				return false
			}
		}
		return true
	})
}

func (f *firstRun) archivesFetched() map[string]int {
	fetched := map[string]int{}
	f.releases.Mutate(func() {
		for name, count := range f.releases.Downloads {
			if strings.HasSuffix(name, ".zip") {
				fetched[name] = count
			}
		}
	})
	return fetched
}

// consentReport is what testdata/firstrunconsent.js saw.
type consentReport struct {
	Title  string   `json:"title"`
	Links  []string `json:"links"`
	Notice string   `json:"notice"`
	Error  string   `json:"error"`
}

// answerInBrowser answers the Install window of the real page in a real
// browser, through a proxy that only appends the driver. ok is false with no
// Chrome-family browser on this machine.
func (f *firstRun) answerInBrowser(answer string) (consentReport, bool) {
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
		return consentReport{}, false
	}
	script, err := os.ReadFile(filepath.Join("testdata", "firstrunconsent.js"))
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
	reports := make(chan consentReport, 1)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/journey/drive.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = w.Write(script)
		case "/journey/config":
			_ = json.NewEncoder(w).Encode(map[string]string{"answer": answer})
		case "/journey/report":
			var report consentReport
			_ = json.NewDecoder(r.Body).Decode(&report)
			reports <- report
		default:
			proxy.ServeHTTP(w, r)
		}
	}))
	defer front.Close()
	profileDir, err := os.MkdirTemp("", "aucom-first-run-browser-")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, browser, "--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--no-first-run", "--no-default-browser-check", "--disable-extensions", "--disable-background-networking",
		"--disable-component-update", "--password-store=basic", "--window-size=1280,900", "--user-data-dir="+profileDir, front.URL+"/")
	command.WaitDelay = 15 * time.Second
	// A pipe, so Wait lasts until the helpers sharing it have gone and the
	// profile can be removed (browser_test.go stopBrowser).
	var browserOutput strings.Builder
	command.Stdout, command.Stderr = &browserOutput, &browserOutput
	if err := command.Start(); err != nil {
		t.Skipf("could not start %s: %v", browser, err)
	}
	defer func() {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = os.RemoveAll(profileDir)
	}()
	select {
	case report := <-reports:
		if report.Error != "" {
			t.Fatalf("the page: %s", report.Error)
		}
		t.Logf("the page answered %s: %+v", answer, report)
		return report, true
	case <-time.After(75 * time.Second):
		t.Fatal("the page never reported")
	}
	return consentReport{}, false
}

// NEW_374 L8: an untouched first run asks, installs nothing on its own or on a
// Cancel, and on a person's Install puts the verified tools beside the bundle.
func TestAReleasesFirstRunAsksThenInstallsBesideItself(t *testing.T) {
	f := newFirstRun(t)
	beside := filepath.Join(f.bundle, managedtools.BuildToolsDirName)
	f.serve()

	status := f.checked()
	if status.Root != beside {
		t.Fatalf("the tools folder is %q, not %q beside the bundle", status.Root, beside)
	}
	for tool, awaiting := range f.buildTools(status) {
		if !awaiting {
			t.Fatalf("a first run did not ask about %s: %+v", tool, status)
		}
	}
	if status.SettingUp {
		t.Fatalf("it says it is setting up before anybody said so: %+v", status)
	}
	if fetched := f.archivesFetched(); len(fetched) != 0 {
		t.Fatalf("archives were downloaded before anybody said so: %v", fetched)
	}

	// Cancel, in the page, and the coordinator woken by another check.
	report, browser := f.answerInBrowser("Cancel")
	if browser {
		if report.Title != "Install the Auto-Pigeon build tools" || len(report.Links) != 2 {
			t.Fatalf("the Install window: %+v", report)
		}
	} else {
		t.Log("no browser here: a Cancel is the absence of the request below")
	}
	status = f.checked()
	for tool, awaiting := range f.buildTools(status) {
		if !awaiting {
			t.Fatalf("after a Cancel %s no longer waits: %+v", tool, status)
		}
	}
	if fetched := f.archivesFetched(); len(fetched) != 0 {
		t.Fatalf("archives were downloaded after a Cancel: %v", fetched)
	}
	if _, err := os.Stat(beside); err == nil {
		entries, _ := os.ReadDir(beside)
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), ".") {
				t.Fatalf("something was put in %s after a Cancel: %s", beside, entry.Name())
			}
		}
	}

	// Install: the verified path, into the folder beside the bundle.
	if report, browser = f.answerInBrowser("Install"); browser {
		if report.Notice != beside {
			t.Fatalf("the notice says %q, not %q", report.Notice, beside)
		}
	} else if code, raw := f.api(http.MethodPost, "/api/v1/tools/install", `{"tools":["ericw-tools","q3map2"]}`); code != http.StatusOK {
		t.Fatalf("install = %d %s", code, raw)
	}
	status = f.until("both tools to be ready", func(status firstRunStatus) bool {
		for _, tool := range status.Tools {
			if tool.ID != "companion" && tool.Phase != "ready" {
				return false
			}
		}
		return true
	})
	for _, tool := range status.Tools {
		if tool.ID == "companion" {
			continue
		}
		if tool.Active == nil || !strings.HasPrefix(tool.Active.Dir, beside+string(filepath.Separator)) {
			t.Fatalf("%s is not beside the bundle: %+v", tool.ID, tool.Active)
		}
		if tool.Active.SHA256 == "" {
			t.Fatalf("%s has no verified digest: %+v", tool.ID, tool.Active)
		}
		if count := f.archivesFetched()[f.archives[tool.ID]]; count != 1 {
			t.Fatalf("%s was downloaded %d times", tool.ID, count)
		}
	}
}

// What a person chose stays chosen: the configured folder and the environment
// variable both win over the folder beside the bundle, and a profile they
// pointed at their own program is left alone by an install.
func TestAReleaseKeepsWhatAPersonChose(t *testing.T) {
	for name, choose := range map[string]func(f *firstRun, own string) []string{
		"a configured folder": func(f *firstRun, own string) []string {
			if err := os.MkdirAll(f.configDir(), 0o700); err != nil {
				f.t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]string{"managed_tools_dir": own})
			if err := os.WriteFile(filepath.Join(f.configDir(), "config.json"), raw, 0o600); err != nil {
				f.t.Fatal(err)
			}
			return nil
		},
		"the environment": func(f *firstRun, own string) []string { return []string{"AUCOM_TOOLS_DIR=" + own} },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFirstRun(t)
			own := filepath.Join(f.home, "my-tools")
			extra := choose(f, own)

			// A profile bound to a program of the person's own.
			program := filepath.Join(f.home, "my-own-ericw", "qbsp")
			if err := os.MkdirAll(filepath.Dir(program), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(program, mt.FixtureProgram(t), 0o755); err != nil {
				t.Fatal(err)
			}
			entry, err := builtin.Find("auto-pigeon.ericw-tools.auto-pigeon-build.q1")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(f.configDir(), 0o700); err != nil {
				t.Fatal(err)
			}
			bindings := filepath.Join(f.configDir(), "bindings.json")
			set, _ := binding.LoadFile(bindings) // a first run has none
			if err := set.Put(binding.LocalBinding{ProfileID: entry.Profile.Metadata().ID, ProfileVersion: entry.Profile.Metadata().Version,
				ProfileDigest: entry.Digest, Trust: entry.Trust(), Acquisition: profile.AcquireUserPath,
				Executables: map[string]string{"qbsp": program}, UpdatedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			if err := binding.SaveFile(bindings, set); err != nil {
				t.Fatal(err)
			}

			f.serve(extra...)
			status := f.checked()
			if status.Root != own {
				t.Fatalf("the tools folder is %q, not the chosen %q", status.Root, own)
			}
			if code, raw := f.api(http.MethodPost, "/api/v1/tools/install", `{"tools":["ericw-tools"]}`); code != http.StatusOK {
				t.Fatalf("install = %d %s", code, raw)
			}
			f.until("EricW to be ready", func(status firstRunStatus) bool {
				for _, tool := range status.Tools {
					if tool.ID == "ericw-tools" {
						return tool.Phase == "ready"
					}
				}
				return false
			})
			if _, err := os.Stat(filepath.Join(f.bundle, managedtools.BuildToolsDirName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the chosen folder was ignored: build-tools beside the bundle: %v", err)
			}
			// The bindings are written after the activation answers; read
			// until the profile nobody chose is bound.
			deadline := time.Now().Add(20 * time.Second)
			for {
				after, err := binding.LoadFile(bindings)
				if err != nil {
					t.Fatal(err)
				}
				other, _ := after.Find("auto-pigeon.ericw-tools.auto-pigeon-build.q2")
				if other.Managed != nil {
					mine, _ := after.Find("auto-pigeon.ericw-tools.auto-pigeon-build.q1")
					if mine.Acquisition != profile.AcquireUserPath || mine.Executables["qbsp"] != program || mine.Managed != nil {
						t.Fatalf("the person's own program was replaced: %+v", mine)
					}
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("the install bound nothing: %+v", other)
				}
				time.Sleep(50 * time.Millisecond)
			}
		})
	}
}
