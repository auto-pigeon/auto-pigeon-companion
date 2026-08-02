// Package web is AUC's local GUI: a net/http server bound to loopback,
// serving an embedded plain HTML/CSS/JS frontend plus a small JSON API that
// the page calls.
//
// # Why a local server instead of a GUI toolkit
//
// There is no native toolkit and no webview here. The frontend is static files
// compiled into the binary with //go:embed, and it runs in whatever browser
// the user already has. That keeps the build CGO-free — all six targets are
// plain `GOOS`/`GOARCH` + `go build` — and keeps the dependency list empty.
//
// # Security posture
//
// The server binds 127.0.0.1 only. Because any local process can reach a
// loopback port, and because a page in the user's browser on some other origin
// could otherwise script requests at it, two guards apply to every API route:
//
//   - a same-origin/no-origin check on the Origin header, rejecting
//     cross-origin calls;
//   - mutating routes must be POST, so a plain cross-site form or <img> cannot
//     trigger them.
//
// These are cheap and stdlib-only. They are not a substitute for treating
// anything reachable on loopback as semi-trusted.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/aue"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
)

// Options configures a Server.
type Options struct {
	// Addr is the listen address. It must be a loopback address; Listen
	// rejects anything else.
	Addr string
	// Config is the loaded user config, used for the AUB base URL and any
	// stored session.
	Config config.Config
	// AUE runs extractor subcommands. Injected so tests and a future
	// HTTP-backed runner need no changes here.
	AUE aue.Runner
	// Version is the build-time version string, surfaced by /api/status.
	Version string
	// Logf receives one line per server lifecycle event. nil discards them.
	Logf func(format string, args ...any)
}

// Server owns the listener, the router, and the mutable session state.
type Server struct {
	options  Options
	listener net.Listener
	http     *http.Server

	mu      sync.RWMutex
	client  *aub.Client
	session config.Session
}

// ErrNotLoopback is returned when Listen is asked to bind a non-loopback
// address. Binding 0.0.0.0 would put an authenticated user's session on the
// local network, so it is refused rather than warned about.
var ErrNotLoopback = errors.New("the GUI server may only bind a loopback address")

// Listen binds the address and returns a Server ready to Serve. Binding
// separately from serving is what lets GUI mode learn the actual port when
// Addr uses port 0.
func Listen(options Options) (*Server, error) {
	if options.Addr == "" {
		options.Addr = config.DefaultServerAddr
	}
	if err := requireLoopback(options.Addr); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", options.Addr)
	if err != nil {
		return nil, fmt.Errorf("cannot listen on %s: %w", options.Addr, err)
	}

	client := aub.New(options.Config.AUBBaseURL)
	client.Token = options.Config.Session.Token

	server := &Server{
		options:  options,
		listener: listener,
		client:   client,
		session:  options.Config.Session,
	}
	server.http = &http.Server{
		Handler: server.routes(),
		// The frontend is local and tiny; these bounds exist to keep a stuck
		// or hostile local client from pinning a connection forever.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return server, nil
}

func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("cannot parse listen address %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("%w: %s", ErrNotLoopback, addr)
	}
	return nil
}

// Addr is the address actually bound, with the real port when port 0 was
// requested.
func (server *Server) Addr() string { return server.listener.Addr().String() }

// URL is the address to open in a browser.
func (server *Server) URL() string { return "http://" + server.Addr() + "/" }

// Serve runs until ctx is cancelled, then shuts down gracefully.
func (server *Server) Serve(ctx context.Context) error {
	errs := make(chan error, 1)
	go func() {
		err := server.http.Serve(server.listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errs <- err
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		server.logf("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.http.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown failed: %w", err)
		}
		return nil
	}
}

func (server *Server) logf(format string, args ...any) {
	if server.options.Logf != nil {
		server.options.Logf(format, args...)
	}
}

func (server *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Static frontend. Assets are embedded, so this never touches the disk.
	mux.Handle("GET /", http.FileServerFS(assetsFS()))

	mux.Handle("GET /api/status", server.guard(server.handleStatus))
	mux.Handle("POST /api/auth/login", server.guard(server.handleLogin))
	mux.Handle("POST /api/auth/logout", server.guard(server.handleLogout))

	// TODO: real map operations. This route exists to prove the AUE path end
	// to end (extract embedded binary, exec, return stdout) and is
	// deliberately not a general "run any subcommand" escape hatch — each
	// operation gets its own route with its own validated inputs as features
	// land.
	mux.Handle("GET /api/aue/version", server.guard(server.handleAUEVersion))

	return mux
}

// guard wraps an API handler with the same-origin check described in the
// package comment.
func (server *Server) guard(handler func(http.ResponseWriter, *http.Request) (any, error)) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !server.sameOrigin(request) {
			writeError(writer, http.StatusForbidden, "cross-origin requests are not allowed")
			return
		}
		payload, err := handler(writer, request)
		if err != nil {
			status := http.StatusInternalServerError
			var apiErr *aub.APIError
			if errors.As(err, &apiErr) {
				// A rejected login is the user's problem to fix, not a server
				// fault, so AUB's status is passed through.
				status = apiErr.Status
			}
			var badRequest *badRequestError
			if errors.As(err, &badRequest) {
				status = http.StatusBadRequest
			}
			writeError(writer, status, err.Error())
			return
		}
		if payload == nil {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(writer, http.StatusOK, payload)
	})
}

// sameOrigin accepts requests with no Origin header (a plain navigation or a
// CLI curl) and requests whose Origin matches the address we are bound to.
func (server *Server) sameOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, server.Addr()) || strings.EqualFold(parsed.Host, request.Host)
}

type badRequestError struct{ message string }

func (err *badRequestError) Error() string { return err.message }

// StatusPayload is what the frontend renders on load.
type StatusPayload struct {
	Version       string `json:"version"`
	AUBBaseURL    string `json:"aub_base_url"`
	Authenticated bool   `json:"authenticated"`
	Email         string `json:"email,omitempty"`
	AUEAvailable  bool   `json:"aue_available"`
}

func (server *Server) handleStatus(_ http.ResponseWriter, _ *http.Request) (any, error) {
	server.mu.RLock()
	session := server.session
	server.mu.RUnlock()

	available := false
	if checker, ok := server.options.AUE.(interface{ Available() bool }); ok {
		available = checker.Available()
	}
	return StatusPayload{
		Version:       server.options.Version,
		AUBBaseURL:    server.options.Config.AUBBaseURL,
		Authenticated: session.Valid(),
		Email:         session.Email,
		AUEAvailable:  available,
	}, nil
}

func (server *Server) handleLogin(_ http.ResponseWriter, request *http.Request) (any, error) {
	var body struct {
		Identity string `json:"identity"`
		Password string `json:"password"`
	}
	// 64 KiB is generous for two short strings and keeps a runaway local
	// client from streaming an unbounded body into memory.
	if err := json.NewDecoder(io.LimitReader(request.Body, 64<<10)).Decode(&body); err != nil {
		return nil, &badRequestError{message: "the request body is not valid JSON"}
	}
	if body.Identity == "" || body.Password == "" {
		return nil, &badRequestError{message: "an identity and a password are required"}
	}

	server.mu.Lock()
	client := server.client
	server.mu.Unlock()

	session, err := client.Login(request.Context(), body.Identity, body.Password)
	if err != nil {
		return nil, err
	}

	stored := config.Session{
		Token:      session.Token,
		UserID:     session.UserID,
		Email:      session.Email,
		ObtainedAt: session.ObtainedAt,
	}
	server.mu.Lock()
	server.session = stored
	server.mu.Unlock()

	// Persisting the token means the next launch starts signed in. A failure
	// to persist is reported but does not undo the successful login — the
	// in-memory session is still usable for this run.
	updated := server.options.Config
	updated.Session = stored
	if err := config.Save(updated); err != nil {
		server.logf("warning: could not save session: %v", err)
	}

	return StatusPayload{
		Version:       server.options.Version,
		AUBBaseURL:    server.options.Config.AUBBaseURL,
		Authenticated: true,
		Email:         stored.Email,
	}, nil
}

func (server *Server) handleLogout(_ http.ResponseWriter, _ *http.Request) (any, error) {
	server.mu.Lock()
	server.client.Logout()
	server.session = config.Session{}
	server.mu.Unlock()

	updated := server.options.Config
	updated.Session = config.Session{}
	if err := config.Save(updated); err != nil {
		server.logf("warning: could not clear saved session: %v", err)
	}
	return map[string]bool{"authenticated": false}, nil
}

func (server *Server) handleAUEVersion(_ http.ResponseWriter, request *http.Request) (any, error) {
	if server.options.AUE == nil {
		return nil, fmt.Errorf("no AUE runner is configured")
	}
	stdout, err := server.options.AUE.Run(request.Context(), "version")
	if err != nil {
		return nil, err
	}
	return map[string]string{"version": strings.TrimSpace(string(stdout))}, nil
}

func writeJSON(writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	// The frontend is same-origin and served from this binary; nothing here
	// should ever be cached by the browser across runs.
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(payload)
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"error": message})
}
