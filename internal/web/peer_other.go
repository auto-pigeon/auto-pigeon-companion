//go:build !linux && !windows && !darwin

package web

import "net"

// platformPeer has no way to ask on this platform, which no release targets
// (every released one — Linux, Windows, macOS — can). Not being able to ask is
// not an answer, so the connection is refused (peer.go) rather than served.
func platformPeer(_, _ *net.TCPAddr) peerVerdict { return peerUnsupported }
