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
// # Same-origin guard
//
// Loopback is not by itself a boundary: a page the user has open on some other
// origin can script requests at a known local port. So every API route goes
// through guard, which rejects a request carrying a cross-origin Origin header,
// and every mutating route is POST-only so a plain cross-site form or <img>
// cannot reach it. Both are cheap and stdlib-only.
//
// TODO(andrea): neither guard authenticates the *caller*. Any process running
// as the user can still drive this server while it is up. The usual remedy is a
// random token minted at startup, put in the URL that gets opened, and required
// on every API call. Worth doing before any release; noted rather than
// half-built.
package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/aue"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/launch"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/tools"
)

// DrainTimeout is how long a shutdown waits for in-flight requests.
const DrainTimeout = 10 * time.Second

// Server is the local GUI server.
type Server struct {
	version  string
	client   *aub.Client
	provider launch.Provider
	manager  tools.Manager
	runner   aue.Runner
	settings config.Config

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
	Manager  tools.Manager
	// AUE runs extractor subcommands. Injected so tests, and a future
	// HTTP-backed runner, need no change here.
	AUE    aue.Runner
	Config config.Config
	// SaveConfig persists configuration changes; nil means config.Save.
	SaveConfig func(config.Config) error
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

	manager := options.Manager
	if manager == nil {
		cache, err := settings.ToolCache()
		if err != nil {
			return nil, err
		}
		// The fake tool until real ones are chosen — see internal/tools.
		manager = tools.NewNoop(cache)
	}

	provider := options.Provider
	if provider == nil {
		provider = launch.ExampleProvider()
	}

	save := options.SaveConfig
	if save == nil {
		save = config.Save
	}

	server := &Server{
		version:    options.Version,
		client:     client,
		provider:   provider,
		manager:    manager,
		runner:     options.AUE,
		settings:   settings,
		saveConfig: save,
	}
	server.handler = server.routes()
	return server, nil
}

// ServeHTTP makes the Server an http.Handler, which is what lets tests drive it
// with httptest.NewServer and no port of its own.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// http.ServeMux, not a router dependency: fixed paths, no path parameters,
	// no middleware stack. Same reasoning as the no-CLI-framework decision.
	mux.Handle("GET /", http.FileServerFS(assetsFS()))
	mux.Handle("GET /api/status", guard(s.handleStatus))
	mux.Handle("POST /api/auth/login", guard(s.handleLogin))
	mux.Handle("POST /api/auth/logout", guard(s.handleLogout))
	mux.Handle("GET /api/launch-configs", guard(s.handleLaunchConfigs))
	mux.Handle("POST /api/build", guard(s.handleBuild))
	mux.Handle("POST /api/launch", guard(s.handleLaunch))
	// Deliberately not a general "run any AUE subcommand" escape hatch: each
	// extractor operation gets its own route with its own validated inputs as
	// features land. This one proves the subprocess path end to end.
	mux.Handle("GET /api/aue/version", guard(s.handleAUEVersion))

	return mux
}

// guard rejects cross-origin API calls. See the package comment.
func guard(handler http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, errors.New("cross-origin requests are not allowed"))
			return
		}
		handler(w, r)
	})
}

// sameOrigin accepts a request with no Origin header — a plain navigation, or a
// curl from the CLI examples in README.md — and one whose Origin host matches
// the Host it was sent to. Comparing against r.Host rather than a remembered
// listener address is what keeps this correct whichever loopback spelling the
// user's browser resolved.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
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
	if s.client == nil {
		writeError(w, http.StatusServiceUnavailable, config.ErrAUBNotConfigured)
		return nil, false
	}
	return s.client, true
}

type statusBody struct {
	Version       string `json:"version"`
	AUBBaseURL    string `json:"aub_base_url"`
	Authenticated bool   `json:"authenticated"`
	Email         string `json:"email,omitempty"`
	Platform      string `json:"platform"`
	ToolCacheDir  string `json:"tool_cache_dir"`
	AUEAvailable  bool   `json:"aue_available"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cache, err := s.settings.ToolCache()
	if err != nil {
		cache = ""
	}
	baseURL := ""
	authenticated := false
	if s.client != nil {
		baseURL = s.client.BaseURL()
		authenticated = s.client.Authenticated()
	}
	writeJSON(w, http.StatusOK, statusBody{
		Version:       s.version,
		AUBBaseURL:    baseURL,
		Authenticated: authenticated,
		Email:         s.settings.Session.Email,
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
		ToolCacheDir:  cache,
		AUEAvailable:  aue.Available(s.runner),
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

	s.settings.Session = config.Session{
		Token:   session.Token,
		UserID:  session.UserID,
		Email:   session.Email,
		Expires: session.Expires,
	}
	if err := s.saveConfig(s.settings); err != nil {
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
	if s.client != nil {
		s.client.Logout()
	}
	s.settings.Session = config.Session{}
	if err := s.saveConfig(s.settings); err != nil {
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

func (s *Server) handleBuild(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Tool    string   `json:"tool"`
		Version string   `json:"version"`
		Args    []string `json:"args"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Tool == "" {
		request.Tool = tools.NoopToolName
	}

	// Buffered rather than streamed: the fake tool produces five lines. When a
	// real tool is wired in, this endpoint becomes a streaming one
	// (text/event-stream or chunked), which is a change to this handler alone —
	// tools.Build already takes writers and already streams into them.
	var output bytes.Buffer
	err := tools.Build(r.Context(), s.manager, tools.BuildRequest{
		Tool:    request.Tool,
		Version: request.Version,
		Args:    request.Args,
	}, &output, &output)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "output": output.String()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"output": output.String()})
}

func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Game     string `json:"game"`
		Map      string `json:"map"`
		GameRoot string `json:"game_root"`
		// DryRun resolves the plan and returns it without starting anything.
		// The GUI defaults to this until a user has a real game installed.
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &request) {
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
		gameRoot = s.settings.GameRoots[selected.Game]
	}
	plan, err := launch.Resolve(launch.Request{Config: selected, GameRoot: gameRoot, Map: request.Map})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.DryRun {
		writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "command": plan.String(), "started": false})
		return
	}
	if err := launch.Run(r.Context(), plan, io.Discard, io.Discard); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "command": plan.String()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "command": plan.String(), "started": true})
}

func (s *Server) handleAUEVersion(w http.ResponseWriter, r *http.Request) {
	// "this build has no extractor" is a property of the installation, not a
	// failure of the request, so it is 503 with the message that names the
	// override — the same thing the page says next to its disabled button.
	// Only an extractor that exists and then fails is 502.
	if !aue.Available(s.runner) {
		writeError(w, http.StatusServiceUnavailable, aue.ErrNoEmbeddedBinary)
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
