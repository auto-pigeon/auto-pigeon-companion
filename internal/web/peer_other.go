//go:build !linux && !windows

package web

import "net"

// platformPeer cannot ask on this platform yet (macOS has no /proc and no
// stdlib route to a TCP socket's owner), so the connection is served as it
// always was — the residual threat row T54 names. See peer.go and README.md.
func platformPeer(_, _ *net.TCPAddr) peerVerdict { return peerUnsupported }
