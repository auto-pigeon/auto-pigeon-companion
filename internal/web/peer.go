package web

import (
	"context"
	"errors"
	"net"
	"net/http"
)

// Which OS account is on the other end of a loopback connection.
//
// # Why the token alone did not keep other accounts out
//
// The token file is 0600 in a 0700 directory: it is meant for this account's
// processes only. But `GET /` has to load without a token, and it carries the
// token so the page can use it — and loopback TCP does not care which account
// connects. Another account on the same computer could ask for the page and
// read the token out of it, and then drive everything the guarded API drives as
// this account (`NEW_342` lead `aucom/web/index-token-served-to-any-loopback-peer`,
// repaired in `NEW_348`). A web page on another origin never could: the Host,
// Origin and Sec-Fetch-Site checks and the absence of CORS keep it from reading
// any response, and that boundary is unchanged.
//
// So the server asks the operating system who owns the other end of each
// connection, once, when it is accepted, and refuses every request on a
// connection another account opened. Nothing about launching changes: the
// browser this account started is this account.
//
// The answer is one of five, and three of them refuse:
//
//   - the same account: served;
//   - another account: refused;
//   - unidentified — the platform can answer this question and did not (no row
//     for the peer's socket, a table or process that could not be read, a peer
//     that is not a loopback TCP socket): REFUSED. The peer chooses its own
//     socket, and `NEW_349` showed it can choose one the lookup does not find —
//     an AF_INET6 dual-stack socket connected to ::ffff:127.0.0.1 is delivered
//     to the 127.0.0.1 listener but listed only in /proc/net/tcp6 — so "no
//     answer" is something another account can ask for, and it must not open
//     the page. On Linux a live loopback peer in this network namespace always
//     has a row in /proc/net/tcp or /proc/net/tcp6, so this is not a state the
//     user's own browser reaches;
//   - unsupported — this platform has no way to ask at all: REFUSED. Every
//     released platform can ask — Linux reads /proc/net/tcp and tcp6, Windows
//     its TCP owner tables, macOS the kernel's socket list (`NEW_353A`; until
//     then macOS was served unasked, the residual T54 named) — so this is a
//     build for a platform nobody releases, and not knowing is not an answer;
//   - not checked — the zero value, a handler called in-process with no
//     accepted connection behind it (tests): served. Serve's ConnContext sets a
//     verdict on every connection it accepts, so no network request has it.
type peerVerdict int

const (
	peerNotChecked peerVerdict = iota
	peerSameUser
	peerOtherUser
	peerUnidentified
	peerUnsupported
)

type peerKey struct{}

// peerContext is the http.Server's ConnContext: it records the verdict for the
// connection every request on it will carry.
func peerContext(ctx context.Context, conn net.Conn) context.Context {
	return context.WithValue(ctx, peerKey{}, identifyPeer(conn))
}

// identifyPeer asks the platform about a TCP connection's far end. A
// connection that is not loopback TCP is not one the listener should have
// accepted, and is unidentified.
func identifyPeer(conn net.Conn) peerVerdict {
	local, okLocal := conn.LocalAddr().(*net.TCPAddr)
	remote, okRemote := conn.RemoteAddr().(*net.TCPAddr)
	if !okLocal || !okRemote || !remote.IP.IsLoopback() {
		return peerUnidentified
	}
	return platformPeer(local, remote)
}

// errAnotherAccount is the refusal. It names the situation and what to do,
// and nothing about the account that owns the Companion.
var errAnotherAccount = errors.New(
	"this Companion belongs to another account on this computer; start your own Companion from your own account")

// errUnidentifiedPeer is the refusal for a connection whose owner the system
// could not name. Like errAnotherAccount it says what to do.
var errUnidentifiedPeer = errors.New(
	"this Companion could not tell which account on this computer opened the connection, so it refused it; " +
		"open the address the Companion printed when it started, from your own account")

// errUnsupportedPeer is the refusal on a platform that cannot ask at all.
var errUnsupportedPeer = errors.New(
	"this Companion cannot tell which account on this computer opened the connection on this operating system, " +
		"so it does not serve its page here; use a release for Linux, Windows or macOS")

// peerGuard refuses every request on a connection another OS account opened,
// or one whose owner the platform could not name or cannot ask about, before
// any route — the page, its assets and the API alike.
func (s *Server) peerGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verdict, _ := r.Context().Value(peerKey{}).(peerVerdict)
		switch verdict {
		case peerOtherUser:
			s.logf("web: refused %s %s from a loopback connection owned by another account", r.Method, r.URL.Path)
			writeError(w, http.StatusForbidden, errAnotherAccount)
			return
		case peerUnidentified:
			s.logf("web: refused %s %s from a loopback connection whose owner the system could not name", r.Method, r.URL.Path)
			writeError(w, http.StatusForbidden, errUnidentifiedPeer)
			return
		case peerUnsupported:
			s.logf("web: refused %s %s: this platform cannot name a loopback connection's owner", r.Method, r.URL.Path)
			writeError(w, http.StatusForbidden, errUnsupportedPeer)
			return
		}
		next.ServeHTTP(w, r)
	})
}
