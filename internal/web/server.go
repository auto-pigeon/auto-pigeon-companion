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

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/aue"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/engine"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/launch"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/pathpick"
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
}

// Server is the local GUI server.
type Server struct {
	version  string
	provider launch.Provider
	jobs     *job.Service
	runner   aue.Runner
	paths    Paths
	picker   *pathpick.Picker
	scanner  engine.Scanner
	builds   *buildRuns
	newAUB   func(baseURL string) (*aub.Client, error)
	token    *Token

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

	// saveConfig persists a changed config. Held as a field so tests can point
	// it at a temporary file instead of the user's real config directory.
	saveConfig func(config.Config) error

	handler http.Handler
}

// Options configures a Server. Every field except Config has a working default,
// so a caller that only has a version string still gets a functioning GUI.
type Options struct {
	Version  string
	Client   *aub.Client
	Provider launch.Provider
	// Jobs is the process runtime. A server without one still serves the page
	// and the account routes; the job routes report that this build has none.
	Jobs *job.Service
	// AUE runs extractor subcommands. Injected so tests, and a future
	// HTTP-backed runner, need no change here.
	AUE    aue.Runner
	Config config.Config
	// SaveConfig persists configuration changes; nil means config.Save.
	SaveConfig func(config.Config) error
	// Token authenticates every API request. Nil mints one, which is what a
	// test wants; `companion serve` passes the token it published so another
	// process can use it.
	Token *Token
	// Paths is where this run keeps its state. See [Paths].
	Paths Paths
	// Picker opens native file dialogs on the user's desktop. Nil means a
	// default one, which is what `serve` wants; a test supplies its own so no
	// window ever opens.
	Picker *pathpick.Picker
	// Scanner looks for installed games. The zero value scans this machine.
	Scanner engine.Scanner
	// NewAUB builds a client for a base URL, so a settings change can point the
	// server at a different backend without a restart. Nil means aub.New.
	NewAUB func(baseURL string) (*aub.Client, error)
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
		if settings.AUBBaseURL != "" {
			client, err = aub.New(settings.AUBBaseURL, nil)
			if err != nil {
				return nil, err
			}
			client.SetToken(settings.Session.Token)
		}
	}

	provider := options.Provider
	if provider == nil {
		provider = launch.ExampleProvider()
	}

	save := options.SaveConfig
	if save == nil {
		save = config.Save
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

	server := &Server{
		version:    options.Version,
		provider:   provider,
		jobs:       options.Jobs,
		runner:     options.AUE,
		paths:      options.Paths,
		picker:     picker,
		scanner:    options.Scanner,
		builds:     newBuildRuns(),
		newAUB:     newAUB,
		token:      token,
		settings:   settings,
		client:     client,
		saveConfig: save,
	}
	index, err := indexPage(token)
	if err != nil {
		return nil, err
	}
	server.index = index
	server.handler = server.routes()
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
	mux.Handle("GET /", s.hostGuard(http.FileServerFS(assetsFS())))

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
		"GET /api/status":         s.handleStatus,
		"POST /api/auth/login":    s.handleLogin,
		"POST /api/auth/logout":   s.handleLogout,
		"GET /api/launch-configs": s.handleLaunchConfigs,
		"POST /api/launch":        s.handleLaunch,
		// Deliberately not a general "run any AUE subcommand" escape hatch:
		// each extractor operation gets its own route with its own validated
		// inputs as features land. This one proves the subprocess path end to
		// end.
		"GET /api/aue/version": s.handleAUEVersion,
	}
	for _, table := range []map[string]http.HandlerFunc{
		s.jobAPI(), s.profileAPI(), s.libraryAPI(),
		s.engineAPI(), s.buildAPI(), s.settingsAPI(), s.pathAPI(),
		s.feedbackAPI(),
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
			writeError(w, status, err)
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
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errorBody{Error: err.Error()})
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
	Email         string `json:"email,omitempty"`
	Platform      string `json:"platform"`
	ToolCacheDir  string `json:"tool_cache_dir"`
	JobsDir       string `json:"jobs_dir"`
	AUEAvailable  bool   `json:"aue_available"`
	// AUEVerified says whether the extractor this build would run was verified
	// against the signed catalogue, or is an unverified developer override. It
	// is a separate field from AUEAvailable because "there is one" and "it is
	// the one we vouch for" are different facts, and a page that showed only
	// the first would show a development override exactly as it shows a
	// production install.
	AUEVerified   bool   `json:"aue_verified"`
	AUEProvenance string `json:"aue_provenance,omitempty"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	settings := s.config()
	cache, err := settings.ToolCache()
	if err != nil {
		cache = ""
	}
	jobs, err := settings.Jobs()
	if err != nil {
		jobs = ""
	}
	baseURL := ""
	authenticated := false
	if client := s.aubClient(); client != nil {
		baseURL = client.BaseURL()
		authenticated = client.Authenticated()
	}
	verified, provenance := false, ""
	if aue.Available(s.runner) {
		record := s.runner.Provenance()
		verified, provenance = record.Verified, record.Mode
	}
	writeJSON(w, http.StatusOK, statusBody{
		Version:       s.version,
		AUBBaseURL:    baseURL,
		Authenticated: authenticated,
		Email:         settings.Session.Email,
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
		ToolCacheDir:  cache,
		JobsDir:       jobs,
		AUEAvailable:  aue.Available(s.runner),
		AUEVerified:   verified,
		AUEProvenance: provenance,
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

	s.mu.Lock()
	s.settings.Session = config.Session{
		Token:   session.Token,
		UserID:  session.UserID,
		Email:   session.Email,
		Expires: session.Expires,
	}
	updated := s.settings
	s.mu.Unlock()
	if err := s.saveConfig(updated); err != nil {
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
	s.mu.Lock()
	s.settings.Session = config.Session{}
	updated := s.settings
	s.mu.Unlock()
	if err := s.saveConfig(updated); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
}

func (s *Server) handleLaunchConfigs(w http.ResponseWriter, r *http.Request) {
	configs, err := s.provider.Configs(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": configs})
}

// handleLaunch resolves a launch config into a command, and starts it as a job.
//
// There is no separate "run a game" path any more. The config becomes a
// generated engine profile (see internal/launch), and starting it is a
// submission to the same executor a compile goes through — so a launched game
// is supervised, cancellable, and recorded, exactly like everything else.
func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Game     string `json:"game"`
		Map      string `json:"map"`
		GameRoot string `json:"game_root"`
		// DryRun resolves the command and returns it without starting
		// anything. The page defaults to it until a user has a game installed.
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	service, ok := s.requireJobs(w)
	if !ok {
		return
	}

	configs, err := s.provider.Configs(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	selected, err := launch.Find(configs, request.Game)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	gameRoot := request.GameRoot
	if gameRoot == "" {
		gameRoot = s.config().GameRoots[selected.Game]
	}
	jobRequest, err := launch.JobRequest(selected, gameRoot, request.Map, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if request.DryRun {
		previewed, err := service.Preview(jobRequest)
		if err != nil {
			writeError(w, jobStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"job": previewed, "command": previewed.Command.Shell, "started": false,
		})
		return
	}

	submitted, err := service.Submit(jobRequest)
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	w.Header().Set("Location", "/api/v1/jobs/"+submitted.ID)
	writeJSON(w, http.StatusAccepted, map[string]any{"job": submitted, "started": true})
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
