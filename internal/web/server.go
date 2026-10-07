// Package web is the Companion's local GUI: an HTTP server on loopback serving
// an embedded static frontend plus a small JSON API.
//
// # Why this instead of a GUI toolkit
//
// There is no GUI dependency at all — no Fyne, no Wails, no Gio, no embedded
// browser engine. The "window" is the user's own browser, pointed at a
// loopback address. That is what keeps the binary CGO-free and cross-compilable
// to all six targets with plain `go build`, and it is why this package is
// net/http and //go:embed rather than a widget tree.
//
// # Loopback only
//
// Listen binds 127.0.0.1 explicitly, never :port. This server exposes a user's
// AUB session and can start processes on their machine; it must not be
// reachable from the network, and binding the loopback address is the control
// that guarantees it rather than hoping a firewall does.
//
// # The guard
//
// Loopback is not by itself a boundary: a page the user has open on some other
// origin can script requests at a known local port, and a name that resolves to
// 127.0.0.1 makes those requests look local. So every API route goes through
// [Server.guard]: a per-run token in a request *header*, a Host that must name
// a loopback address, an Origin that must match it, and Sec-Fetch-Site when the
// browser sends it. See auth.go, which explains what each check is for and what
// the token does not cover.
//
// # Nothing runs here
//
// This package starts no processes. Everything that does goes to
// [job.Service], which the CLI drives too — so a rule the executor enforces is
// enforced for the browser as well, rather than for whichever caller went
// through the right function.
package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/aue"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/autobuild"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/incident"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/pathpick"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
)

// DrainTimeout is how long a shutdown waits for in-flight requests.
const DrainTimeout = 10 * time.Second

// Paths is where this run keeps its state on disk.
//
// Passed in rather than resolved here, because the caller is the one that knows
// what `--config` meant: a session pointed at a deliberate config file must not
// reach into the real machine's profiles, bindings and grants. The CLI resolves
// all of them in one place (`statePaths`) and hands them over.
//
// An empty field falls back to the configured default, which is what a test
// that only cares about jobs wants.
type Paths struct {
	// Profiles is the directory of profile documents the catalog reads.
	Profiles string
	// Bindings is the file recording what is installed on this machine.
	Bindings string
	// Builds is where build manifests are kept.
	Builds string
	// AssetCache is the local cache of AUB revisions.
	AssetCache string
	// ConfigDir is where this run's token, address and pending join link live.
	// Empty means the user's configuration directory.
	ConfigDir string
}

// Server is the local GUI server.
type Server struct {
	version string
	debug   bool
	// siteLinks caches AUB's answer to where the gallery is (the footer's
	// News link). See sitelinks.go.
	siteLinks siteLinksCache
	// hosting lists games hosted from Build & Run in Live Games. See hosting.go.
	hosting hostingState
	jobs    *job.Service
	// playLive is the one registry of Build & Run sequences this process is
	// executing. playService builds a coordinator per request, and every one of
	// them must share this, or a cancel cannot reach the run it names.
	playLive *playrun.Live
	// autobuild is this process's one auto-build service (autobuild.go),
	// made on first use; its poller runs only when `serve` starts it.
	autobuild     *autobuild.Service
	autobuildOnce sync.Once
	autobuildErr  error
	runner        aue.Runner
	paths         Paths
	picker        *pathpick.Picker
	scanner       engine.Scanner
	builds        *buildRuns
	// q3runs is the Quake III package runs this process is waiting on or has
	// waited on. See q3package.go.
	q3runs *q3Runs
	// leaks tells the editor, through AUB, what is happening to its leak
	// requests. One worker, stopped by Close. See leaksender.go.
	leaks *leakSender
	logf  func(format string, args ...any)
	// games is the Games area's process-wide state: download and launch
	// coordination, and the reviews waiting for an approval.
	games  *gameState
	newAUB func(baseURL string) (*aub.Client, error)
	token  *Token
	// lifecycle decides when this process stops, and counts the pages that
	// hold a lease. See lifecycle.go.
	lifecycle *Lifecycle
	// incidents is Options.Incidents; never nil.
	incidents func() []incident.RecentIncident

	// mu guards the two values a request can change under another request:
	// the stored configuration, and the client built from the address in it.
	// Signing in rewrites the session; Settings rewrites the backend address
	// and rebuilds the client around it. Both are ordinary things for a person
	// with two tabs open to do at once, and neither is safe to read while the
	// other is halfway through.
	mu       sync.RWMutex
	settings config.Config
	client   *aub.Client
	// index is the frontend page with the API token substituted in. Built once
	// at construction: the token does not change during a run, and rebuilding
	// it per request would be a string replacement on every page load.
	index []byte

	// updateConfig persists a change to the config file. Held as a field so
	// tests can point it at a temporary file instead of the user's real config
	// directory.
	//
	// A *mutation*, not a value: this server is one of several processes that
	// write config.json, and a handler that wrote back the whole struct it read
	// at startup would silently undo whatever another instance changed in
	// between. See [config.Update].
	updateConfig func(func(*config.Config) error) (config.Config, error)
	readConfig   func() (config.Config, error)

	handler http.Handler
}

// Options configures a Server. Every field except Config has a working default,
// so a caller that only has a version string still gets a functioning GUI.
type Options struct {
	Version string
	// Debug unlocks the developer controls — in Settings, typing a server
	// address that is not one of the official deployments. Started with
	// `companion --debug` / `serve --debug`; off for everybody else.
	Debug  bool
	Client *aub.Client
	// Jobs is the process runtime. A server without one still serves the page
	// and the account routes; the job routes report that this build has none.
	Jobs *job.Service
	// AUE runs extractor subcommands. Injected so tests, and a future
	// HTTP-backed runner, need no change here.
	AUE    aue.Runner
	Config config.Config
	// UpdateConfig applies one change to the config file, under the lock that
	// makes this program a single writer of it. Nil means the user's own
	// config.json through [config.Update].
	UpdateConfig func(func(*config.Config) error) (config.Config, error)
	// ReadConfig reads the config file as it is now. With it, a sign-in or a
	// sign-out made by `companion auth` in a terminal reaches a page that is
	// already running; without it (tests), the session is the one given at
	// construction.
	ReadConfig func() (config.Config, error)
	// Token authenticates every API request. Nil mints one, which is what a
	// test wants; `companion serve` passes the token it published so another
	// process can use it.
	Token *Token
	// Paths is where this run keeps its state. See [Paths].
	Paths Paths
	// Logf receives the lines this server has to say outside a response — a
	// run a stopped Companion left unfinished, a swept temporary directory.
	// Nil discards them, which is what a test wants.
	Logf func(format string, args ...any)
	// Picker opens native file dialogs on the user's desktop. Nil means a
	// default one, which is what `serve` wants; a test supplies its own so no
	// window ever opens.
	Picker *pathpick.Picker
	// Scanner looks for installed games. The zero value scans this machine.
	Scanner engine.Scanner
	// NewAUB builds a client for a base URL, so a settings change can point the
	// server at a different backend without a restart. Nil means aub.New.
	NewAUB func(baseURL string) (*aub.Client, error)
	// Lifecycle decides when the process stops. Nil is server mode, which never
	// stops on its own — what a test and `companion serve` want.
	Lifecycle *Lifecycle
	// Incidents lists what this process raised, newest first, for the page's
	// Report a bug (bugreport.go). Nil means none: the page files cold
	// reports only.
	Incidents func() []incident.RecentIncident
}

// NewServer builds the server and its routes.
func NewServer(options Options) (*Server, error) {
	settings := options.Config

	client := options.Client
	if client == nil {
		var err error
		// An unconfigured AUB address is an error the user must act on, not
		// something to paper over with a guessed default — see
		// config.ErrAUBNotConfigured. The server still starts, so the page can
		// say so; only the AUB-backed routes fail.
		// The EFFECTIVE address: the environment (and so the development
		// `.env` and the config.json beside the executable, which set it) wins
		// over the file, exactly as it does for every CLI command. Reading
		// only the file here made the page say "no server chosen" while
		// `companion auth status` named one (NEW_244D).
		if effective, aubErr := settings.AUB(); aubErr == nil {
			client, err = aub.New(effective, nil)
			if err != nil {
				return nil, err
			}
			client.SetToken(settings.Session.Token)
		}
	}

	update := options.UpdateConfig
	if update == nil {
		update = func(mutate func(*config.Config) error) (config.Config, error) {
			path, err := config.Path()
			if err != nil {
				return config.Config{}, err
			}
			return config.Update(path, mutate)
		}
	}

	token := options.Token
	if token == nil {
		var err error
		if token, err = NewToken(); err != nil {
			return nil, err
		}
	}

	newAUB := options.NewAUB
	if newAUB == nil {
		newAUB = func(baseURL string) (*aub.Client, error) { return aub.New(baseURL, nil) }
	}

	picker := options.Picker
	if picker == nil {
		picker = &pathpick.Picker{}
	}
	logf := options.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	lifecycle := options.Lifecycle
	if lifecycle == nil {
		lifecycle = NewLifecycle(LifecycleOptions{})
	}

	incidents := options.Incidents
	if incidents == nil {
		incidents = func() []incident.RecentIncident { return nil }
	}

	server := &Server{
		incidents:    incidents,
		lifecycle:    lifecycle,
		playLive:     playrun.NewLive(),
		version:      options.Version,
		debug:        options.Debug,
		jobs:         options.Jobs,
		runner:       options.AUE,
		paths:        options.Paths,
		picker:       picker,
		scanner:      options.Scanner,
		builds:       newBuildRuns(),
		q3runs:       newQ3Runs(),
		logf:         logf,
		games:        newGameState(),
		newAUB:       newAUB,
		token:        token,
		settings:     settings,
		client:       client,
		updateConfig: update,
		readConfig:   options.ReadConfig,
	}
	index, err := indexPage(token)
	if err != nil {
		return nil, err
	}
	server.index = index
	server.leaks = newLeakSender(nil, server.leakSession, logf)
	server.handler = server.routes()

	// Records a previous Companion left mid-run, and the temporary directories
	// a killed extraction left behind. Both are states a durable record can be
	// wrong in, and both are cheap to put right exactly once, here.
	server.recoverPlayRuns()
	server.sweepTextureBundles()

	return server, nil
}

// Token is the credential this server requires on every API request.
func (s *Server) Token() *Token { return s.token }

// config is the current settings, copied under the lock.
//
// config.Config is a value, so a handler that took one holds a consistent
// snapshot for as long as it needs one — which is what a handler wants, rather
// than a pointer whose fields could change between two reads of it.
func (s *Server) config() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// adoptSessionFromDisk takes the session another process wrote to the config
// file. The page used to keep the session it read at start, so `companion
// auth login` in a terminal was invisible until the Companion restarted
// (NEW_244D, with the operator's own install). Only the session moves: the
// backend address is rebuilt by Settings, and changing it under a running page
// is a restart's job.
func (s *Server) adoptSessionFromDisk() {
	if s.readConfig == nil {
		return
	}
	current, err := s.readConfig()
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if current.Session.Token == s.settings.Session.Token {
		return
	}
	s.settings.Session = current.Session
	if s.client == nil {
		return
	}
	if current.Session.Token == "" {
		s.client.Logout()
	} else {
		s.client.SetToken(current.Session.Token)
	}
}

// aubClient is the current AUB client, or nil when no address is configured.
func (s *Server) aubClient() *aub.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

// ServeHTTP makes the Server an http.Handler, which is what lets tests drive it
// with httptest.NewServer and no port of its own.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// http.ServeMux, not a router dependency: fixed paths, no path parameters,
	// no middleware stack. Same reasoning as the no-CLI-framework decision.
	// The page itself carries the API token, so it is served from memory
	// rather than straight off the embedded filesystem. Everything else —
	// the stylesheet, the script — is static.
	mux.Handle("GET /{$}", http.HandlerFunc(s.handleIndex))
	mux.Handle("GET /index.html", http.HandlerFunc(s.handleIndex))
	mux.Handle("GET /", s.hostGuard(moduleTypes(http.FileServerFS(assetsFS()))))

	// One table, one registration loop. Every guarded route in this package
	// comes from [Server.api], so a route cannot be added without the guard,
	// and the test that sweeps the surface sweeps all of it.
	for pattern, handler := range s.api() {
		mux.Handle(pattern, s.guard(handler))
	}
	return mux
}

// api is the whole guarded HTTP surface, as one table.
//
// The unversioned `/api/...` routes are the page's own and are not a contract.
// Everything under `/api/v1/` is: `companion job` talks to it, and a script may.
func (s *Server) api() map[string]http.HandlerFunc {
	routes := map[string]http.HandlerFunc{
		"GET /api/status":       s.handleStatus,
		"POST /api/auth/login":  s.handleLogin,
		"POST /api/auth/logout": s.handleLogout,
		// Deliberately not a general "run any AUE subcommand" escape hatch:
		// each extractor operation gets its own route with its own validated
		// inputs as features land. This one proves the subprocess path end to
		// end.
		"GET /api/aue/version": s.handleAUEVersion,
	}
	for _, table := range []map[string]http.HandlerFunc{
		s.jobAPI(), s.profileAPI(), s.libraryAPI(),
		s.engineAPI(), s.buildAPI(), s.playAPI(), s.settingsAPI(), s.siteLinksRoutes(), s.hostingRoutes(), s.bugReportRoutes(), s.pathAPI(),
		s.feedbackAPI(), s.aboutAPI(), s.accountAPI(), s.gamesAPI(), s.noticesAPI(),
		s.lifecycleAPI(), s.leakTestAPI(), s.leakPipelineAPI(), s.uriAPI(), s.autobuildAPI(), s.q3API(),
	} {
		for pattern, handler := range table {
			if _, clash := routes[pattern]; clash {
				// Unreachable unless two tables in this package claim one
				// pattern, which http.ServeMux would panic on at registration
				// anyway — said here so the message names the cause.
				panic("web: two API tables both register " + pattern)
			}
			routes[pattern] = handler
		}
	}
	return routes
}

// guard applies every check in auth.go to an API route.
func (s *Server) guard(handler http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status, err := checkRequest(r, s.token); err != nil {
			if r.URL.Path == leasePath {
				// A page whose lease is refused is a page the lifecycle
				// cannot see, so it is said in the log. Origin, Host and
				// Sec-Fetch-Site are what the checks read; the token is not
				// written anywhere.
				s.logf("lifecycle: a page's lease was refused (%s): %d %v; Host %q, Origin %q, Sec-Fetch-Site %q",
					leasePeer(r), status, err, r.Host, r.Header.Get("Origin"), r.Header.Get("Sec-Fetch-Site"))
			}
			body := errorBody{Error: err.Error()}
			if status == http.StatusUnauthorized {
				body.Code = codeTokenRefused
			}
			writeJSON(w, status, body)
			return
		}
		handler(w, r)
	})
}

// hostGuard is the guard without the token, for the static assets a browser
// fetches before it has one.
//
// The Host check still applies: a page served to a rebound name would be a page
// on an attacker's origin, holding this server's token.
func (s *Server) hostGuard(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			writeError(w, http.StatusForbidden,
				fmt.Errorf("this server answers only to a loopback address; %q is not one", r.Host))
			return
		}
		handler.ServeHTTP(w, r)
	})
}

// moduleTypes states the media type of the two asset kinds a module graph
// depends on, rather than leaving it to the platform's MIME table.
//
// The notice banner is an ES module that imports the vendored contract, which
// imports its rules with `import … with { type: "json" }`. A browser refuses a
// module script whose type is not JavaScript and a JSON module whose type is
// not JSON, and `mime.TypeByExtension` consults the operating system's own
// table first — so a machine whose table says something odd about `.mjs` or
// `.json` would get a page with no banner and nothing in the log. nosniff
// makes the browser hold us to what we said.
func moduleTypes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".mjs"), strings.HasSuffix(r.URL.Path, ".js"):
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		case strings.HasSuffix(r.URL.Path, ".json"):
			w.Header().Set("Content-Type", "application/json")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Revalidated on every load: the embedded files carry no modification
		// time, so a browser that cached them heuristically kept the previous
		// build's script beside this build's page after an update.
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

// handleIndex serves the page with this run's API token in it.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if !loopbackHost(r.Host) {
		writeError(w, http.StatusForbidden,
			fmt.Errorf("this server answers only to a loopback address; %q is not one", r.Host))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Never cached: it carries a credential that is only valid for this run.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The page loads nothing from anywhere else and never has: stated as a
	// policy the browser enforces rather than as a property of the source.
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Write(s.index)
}

// tokenPlaceholder is what indexPage substitutes. It is in the embedded page so
// the page is a complete, readable file rather than a template with a hole.
const tokenPlaceholder = "__AUCOM_API_TOKEN__"

func indexPage(token *Token) ([]byte, error) {
	raw, err := fs.ReadFile(assetsFS(), "index.html")
	if err != nil {
		return nil, fmt.Errorf("web: reading the embedded page: %w", err)
	}
	if !bytes.Contains(raw, []byte(tokenPlaceholder)) {
		// A page with no placeholder is a page that would load and then fail
		// every request, which is a much worse thing to find out at runtime.
		return nil, fmt.Errorf("web: the embedded page has no %s to put the API token in", tokenPlaceholder)
	}
	return bytes.ReplaceAll(raw, []byte(tokenPlaceholder), []byte(token.Value())), nil
}

// writeJSON is the single response encoder, so no handler invents its own
// content type or status handling.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// The frontend is same-origin and served from this binary; none of these
	// responses — several of which carry account state — should be cached by
	// the browser across runs.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already sent; there is nothing to report to the
		// client, and the connection will be closed under it.
		return
	}
}

type errorBody struct {
	Error string `json:"error"`
	// Code names the few refusals a page acts on rather than only shows.
	Code string `json:"code,omitempty"`
	// Class is what kind of failure this is, when the error says
	// (internal/failure). Additive: a page that does not read it loses nothing.
	Class string `json:"class,omitempty"`
}

// codeTokenRefused marks a request whose token is not this run's. From the
// page that means one thing — it was served by an earlier start of the
// Companion — and the page says so instead of printing the header advice
// meant for somebody writing a script (NEW_244D).
const codeTokenRefused = "token_refused"

func writeError(w http.ResponseWriter, status int, err error) {
	// The class travels beside the sentence when the error carries one
	// (internal/failure): a build that could not START — the extractor refused
	// the map, a bound package is not held — has no manifest to carry it.
	writeJSON(w, status, errorBody{Error: err.Error(), Class: failure.Of(err)})
}

// maxRequestBody caps a request body. Every request this API takes is a small
// JSON object; the cap stops a malformed or hostile local client from making
// the server allocate without bound.
const maxRequestBody = 1 << 20

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return false
	}
	return true
}

// requireClient reports the AUB client, or writes the "not configured" error
// and returns false. Every AUB-backed route goes through it so an unconfigured
// address produces one accurate message instead of a nil dereference.
func (s *Server) requireClient(w http.ResponseWriter) (*aub.Client, bool) {
	s.adoptSessionFromDisk()
	client := s.aubClient()
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, config.ErrAUBNotConfigured)
		return nil, false
	}
	return client, true
}

type statusBody struct {
	Version       string `json:"version"`
	AUBBaseURL    string `json:"aub_base_url"`
	Authenticated bool   `json:"authenticated"`
	// SessionExpired says a session is stored and its token has expired, so
	// the page asks for a sign-in instead of showing "signed in" on a session
	// every AUB call would refuse.
	SessionExpired bool   `json:"session_expired,omitempty"`
	Email          string `json:"email,omitempty"`
	Platform       string `json:"platform"`
	JobsDir        string `json:"jobs_dir"`
	AUEAvailable   bool   `json:"aue_available"`
	// AUEVerified says whether the extractor this build would run was checked
	// against the release's bundle manifest, or is an unverified one — a
	// developer override, or a copy no manifest lists. It
	// is a separate field from AUEAvailable because "there is one" and "it is
	// the one we vouch for" are different facts, and a page that showed only
	// the first would show a development override exactly as it shows a
	// production install.
	AUEVerified   bool   `json:"aue_verified"`
	AUEProvenance string `json:"aue_provenance,omitempty"`
	// Debug says the developer controls are unlocked (`--debug`).
	Debug bool `json:"debug"`
	// Backends are the official deployments a person may choose, and
	// BackendLabel names the one in use when it is one of them.
	Backends     []config.Backend `json:"backends"`
	BackendLabel string           `json:"backend_label,omitempty"`
	// AUBIgnored are server addresses a released Companion set aside, each
	// with where it came from (config.ApplyReleasePolicy), and AUBIgnoredWhy
	// the sentence that explains them. Empty in the ordinary case.
	AUBIgnored    []string `json:"aub_ignored,omitempty"`
	AUBIgnoredWhy string   `json:"aub_ignored_why,omitempty"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.adoptSessionFromDisk()
	settings := s.config()
	jobs, err := settings.Jobs()
	if err != nil {
		jobs = ""
	}
	baseURL := ""
	authenticated, expired := false, false
	if client := s.aubClient(); client != nil {
		baseURL = client.BaseURL()
		authenticated = client.Authenticated()
		if authenticated && client.SessionExpired(time.Now()) {
			authenticated, expired = false, true
		}
	}
	verified, provenance := false, ""
	if aue.Available(s.runner) {
		record := s.runner.Provenance()
		verified, provenance = record.Verified, record.Mode
	}
	writeJSON(w, http.StatusOK, statusBody{
		Version:        s.version,
		AUBBaseURL:     baseURL,
		Authenticated:  authenticated,
		SessionExpired: expired,
		Email:          settings.Session.Email,
		Platform:       runtime.GOOS + "/" + runtime.GOARCH,
		JobsDir:        jobs,
		AUEAvailable:   aue.Available(s.runner),
		AUEVerified:    verified,
		AUEProvenance:  provenance,
		Debug:          s.debug,
		Backends:       config.OfficialBackends,
		BackendLabel:   backendLabel(baseURL),
		AUBIgnored:     ignoredAddresses(),
		AUBIgnoredWhy:  ignoredWhy(),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	client, ok := s.requireClient(w)
	if !ok {
		return
	}

	session, err := client.Login(r.Context(), request.Email, request.Password)
	if err != nil {
		var apiErr *aub.APIError
		if errors.As(err, &apiErr) && apiErr.Unauthorized() {
			writeError(w, http.StatusUnauthorized, err)
			return
		}
		writeError(w, http.StatusBadGateway, err)
		return
	}

	updated, err := s.updateConfig(func(current *config.Config) error {
		current.Session = config.Session{
			Token:   session.Token,
			UserID:  session.UserID,
			Email:   session.Email,
			Expires: session.Expires,
		}
		return nil
	})
	if err == nil {
		s.mu.Lock()
		s.settings = updated
		s.mu.Unlock()
		// A leak request that arrived while nobody was signed in is
		// acknowledged to the editor now.
		s.leaks.sessionChanged()
	} else {
		// The login itself succeeded and the in-memory client is usable; only
		// persistence failed, so this is reported without failing the request.
		writeJSON(w, http.StatusOK, map[string]any{
			"email":   session.Email,
			"warning": "signed in, but the session could not be saved: " + err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"email": session.Email})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if client := s.aubClient(); client != nil {
		client.Logout()
	}
	updated, err := s.updateConfig(func(current *config.Config) error {
		current.Session = config.Session{}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.mu.Lock()
	s.settings = updated
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
}

func (s *Server) handleAUEVersion(w http.ResponseWriter, r *http.Request) {
	// "this build has no extractor" is a property of the installation, not a
	// failure of the request, so it is 503 with the message that names the
	// override — the same thing the page says next to its disabled button.
	// Only an extractor that exists and then fails is 502.
	if !aue.Available(s.runner) {
		writeError(w, http.StatusServiceUnavailable, aue.ErrNoExtractor)
		return
	}
	stdout, err := s.runner.Run(r.Context(), "version")
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"version": strings.TrimSpace(string(stdout))})
}

// Listen binds the loopback listener the server runs on. Port 0 asks the OS for
// an ephemeral port; a port already in use falls back to one, because a stale
// instance or an unrelated service holding the configured port should not stop
// the Companion from starting.
//
// The address is 127.0.0.1 by construction rather than by validation: there is
// no argument that can make this bind a routable interface.
func Listen(port int) (net.Listener, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err == nil {
		return listener, nil
	}
	if port == 0 {
		return nil, fmt.Errorf("web: binding a loopback port: %w", err)
	}
	fallback, fallbackErr := net.Listen("tcp", "127.0.0.1:0")
	if fallbackErr != nil {
		return nil, fmt.Errorf("web: binding 127.0.0.1:%d (%v) and any free port: %w", port, err, fallbackErr)
	}
	return fallback, nil
}

// URL is the address a listener is reachable at.
func URL(listener net.Listener) string {
	return "http://" + listener.Addr().String() + "/"
}

// Serve runs the server on listener until ctx is cancelled, then drains
// in-flight requests and returns.
func Serve(ctx context.Context, listener net.Listener, handler http.Handler) error {
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
			return
		}
		errs <- nil
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	// A fresh context: ctx is already cancelled, and Shutdown would abandon
	// exactly the requests it is meant to drain.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), DrainTimeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if err := <-errs; err != nil {
		return err
	}
	return shutdownErr
}

// assetsFS is the embedded frontend, rooted so "/" serves index.html.
func assetsFS() fs.FS {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		// Unreachable: the directory is embedded at compile time, so a failure
		// here would mean the binary was built without its own assets.
		panic("web: embedded assets are missing: " + err.Error())
	}
	return sub
}
