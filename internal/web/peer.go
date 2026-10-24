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
// The answer is one of three. Only a POSITIVE "another account" refuses: where
// the platform cannot say (macOS today, a lookup that failed), the connection is
// served as it always was, and the refusal never depends on a guess.
type peerVerdict int

const (
	peerUnknown peerVerdict = iota
	peerSameUser
	peerOtherUser
)

type peerKey struct{}

// peerContext is the http.Server's ConnContext: it records the verdict for the
// connection every request on it will carry.
func peerContext(ctx context.Context, conn net.Conn) context.Context {
	return context.WithValue(ctx, peerKey{}, identifyPeer(conn))
}

// identifyPeer asks the platform about a TCP connection's far end.
func identifyPeer(conn net.Conn) peerVerdict {
	local, okLocal := conn.LocalAddr().(*net.TCPAddr)
	remote, okRemote := conn.RemoteAddr().(*net.TCPAddr)
	if !okLocal || !okRemote || !remote.IP.IsLoopback() {
		return peerUnknown
	}
	return platformPeer(local, remote)
}

// errAnotherAccount is the refusal. It names the situation and what to do,
// and nothing about the account that owns the Companion.
var errAnotherAccount = errors.New(
	"this Companion belongs to another account on this computer; start your own Companion from your own account")

// peerGuard refuses every request on a connection another OS account opened,
// before any route — the page, its assets and the API alike.
func (s *Server) peerGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if verdict, _ := r.Context().Value(peerKey{}).(peerVerdict); verdict == peerOtherUser {
			s.logf("web: refused %s %s from a loopback connection owned by another account", r.Method, r.URL.Path)
			writeError(w, http.StatusForbidden, errAnotherAccount)
			return
		}
		next.ServeHTTP(w, r)
	})
}
