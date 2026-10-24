//go:build !linux && !windows

package web

import "net"

// platformPeer cannot say on this platform yet (macOS has no /proc and no
// stdlib route to a TCP socket's owner), so the connection is served as it
// always was. See peer.go and README.md.
func platformPeer(_, _ *net.TCPAddr) peerVerdict { return peerUnknown }
