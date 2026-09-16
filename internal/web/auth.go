package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Authenticating the local API, and what each of the four checks is for.
//
// This server can start processes on somebody's machine. Loopback alone does
// not protect it: any page the user has open, on any origin, can script
// requests at a guessable local port, and a name whose DNS record points at
// 127.0.0.1 makes those requests look same-origin to the browser. So four
// checks, each closing something the others do not.
//
//  1. A token, in a header. This is the one that matters, and it is a *header*
//     rather than a cookie on purpose: a cross-origin page cannot set a custom
//     header without a CORS preflight, this server answers no preflight, and
//     nothing is ever authenticated by something the browser attaches on its
//     own. There is no CSRF surface to defend because there is no ambient
//     credential.
//  2. The Host header must name a loopback address. That is the DNS-rebinding
//     defence: `http://rebound.example/` resolving to 127.0.0.1 arrives here
//     with `Host: rebound.example`, and is refused before anything reads it.
//  3. Origin, when present, must match Host. Cheap, and it catches a
//     misconfigured client before the token check has to.
//  4. Sec-Fetch-Site, when the browser sends it, must say the request came from
//     this origin or from no page at all.
//
// # What the token does not protect against, said plainly
//
// The page has to be able to load, so `GET /` is not authenticated, and it
// carries the token so the page can use it. Any process running as the same
// user can therefore read it — but that process can already read config.json,
// which holds the AUB session token, so this is the boundary that was there
// anyway. What the token closes is the case the boundary never covered: a web
// page, on some other origin, driving the executor. See README.md.

// tokenHeader is the Companion's own header. Authorization is accepted too, for
// clients that already have a bearer mechanism.
const tokenHeader = "X-AUCOM-Token"

// TokenFileName is the file the running server writes its token to, so a
// `companion job` invocation in another terminal can talk to it.
const TokenFileName = "api-token"

// ErrNoToken reports that no server is running, or that its token file is gone.
var ErrNoToken = errors.New("web: no local API token: start the Companion with `companion serve`")

// Token is the local API credential for one run of the server.
//
// Minted per run and never persisted beyond it: a token that outlived the
// process would be a standing credential for a server that is not listening.
type Token struct{ value string }

// NewToken mints a token from the system's random source.
func NewToken() (*Token, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("web: minting the local API token: %w", err)
	}
	return &Token{value: base64.RawURLEncoding.EncodeToString(raw[:])}, nil
}

// Value is the token as a string.
func (t *Token) Value() string {
	if t == nil {
		return ""
	}
	return t.value
}

// Matches reports whether a candidate is this token, in constant time.
func (t *Token) Matches(candidate string) bool {
	if t == nil || t.value == "" || candidate == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(t.value), []byte(candidate)) == 1
}

// TokenPath is where the running server publishes its token.
func TokenPath(configDir string) string { return filepath.Join(configDir, TokenFileName) }

// WriteToken publishes the token for other processes running as this user.
//
// 0600 before any content is written, and the directory 0700: this file is a
// credential for something that starts processes.
func WriteToken(path string, token *Token) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("web: creating %s: %w", dir, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("web: writing %s: %w", path, err)
	}
	if _, err := file.WriteString(token.Value() + "\n"); err != nil {
		file.Close()
		return fmt.Errorf("web: writing %s: %w", path, err)
	}
	return file.Close()
}

// ReadToken reads a running server's token.
func ReadToken(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", ErrNoToken
		}
		return "", fmt.Errorf("web: reading %s: %w", path, err)
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", ErrNoToken
	}
	return value, nil
}

// RemoveToken deletes the token file on shutdown.
func RemoveToken(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("web: removing %s: %w", path, err)
	}
	return nil
}

// presentedToken pulls the credential out of a request. Header only: never a
// query parameter, which would put it in browser history, in a Referer header
// and in every proxy log between here and nowhere.
func presentedToken(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get(tokenHeader)); value != "" {
		return value
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if scheme, value, found := strings.Cut(authorization, " "); found && strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(value)
	}
	return ""
}

// loopbackHost reports whether a Host header names this machine.
//
// The rebinding defence. A browser tricked into treating `evil.example` as
// 127.0.0.1 still sends `Host: evil.example`, and this is where that stops.
// `localhost` is accepted because it is the name a user types; every other name
// is refused, whatever it resolves to.
func loopbackHost(host string) bool {
	if host == "" {
		return false
	}
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	name = strings.Trim(name, "[]")
	if strings.EqualFold(name, "localhost") {
		return true
	}
	address := net.ParseIP(name)
	return address != nil && address.IsLoopback()
}

// sameOrigin accepts a request with no Origin header — a plain navigation, or a
// curl from the examples in README.md — and one whose Origin host matches the
// Host it was sent to. Comparing against r.Host rather than a remembered
// listener address is what keeps this correct whichever loopback spelling the
// browser resolved.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return origin == ""
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

// fetchSiteAllowed checks the browser's own account of where the request came
// from. Absent on every non-browser client, which is why it can only refuse and
// never permit.
func fetchSiteAllowed(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	}
	return false
}

// checkRequest applies the four checks, returning the status and the sentence
// to refuse with. A nil error means the request may proceed.
//
// The messages differ on purpose: "cross-origin" and "not signed in" are
// different problems with different fixes, and a single opaque 403 for both is
// how a working curl example becomes an afternoon.
func checkRequest(r *http.Request, token *Token) (int, error) {
	if !loopbackHost(r.Host) {
		return http.StatusForbidden, fmt.Errorf(
			"this server answers only to a loopback address; %q is not one, so this request was not made to the machine it reached", r.Host)
	}
	if !sameOrigin(r) {
		return http.StatusForbidden, errors.New("cross-origin requests are not allowed")
	}
	if !fetchSiteAllowed(r) {
		return http.StatusForbidden, errors.New("this request was made by a page on another site, which is not allowed to drive the Companion")
	}
	if !token.Matches(presentedToken(r)) {
		return http.StatusUnauthorized, fmt.Errorf(
			"this request needs the local API token: send it as `%s: <token>`, from the file the running server writes beside config.json", tokenHeader)
	}
	return http.StatusOK, nil
}

// URLFileName is where a running server records the address it actually bound,
// beside its token, so `companion game open` can raise the page that is already
// there instead of starting a second server. A configured port that was taken
// falls back to a free one, which is why the port cannot be derived.
const URLFileName = "api-url"

// URLPath is the URL file for a configuration directory.
func URLPath(configDir string) string { return filepath.Join(configDir, URLFileName) }

// WriteURL records the running server's loopback address. 0600, like the token.
func WriteURL(path, address string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(address)+"\n"), 0o600)
}

// ReadURL reads a running server's address. It accepts only a loopback http URL,
// so a file somebody else wrote cannot send `game open` anywhere else.
func ReadURL(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", ErrNoToken
	}
	value := strings.TrimSpace(string(raw))
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || !loopbackHost(parsed.Host) {
		return "", fmt.Errorf("web: %s does not hold a loopback address", path)
	}
	return value, nil
}

// RemoveURL deletes the URL file. A missing file is not an error.
func RemoveURL(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
