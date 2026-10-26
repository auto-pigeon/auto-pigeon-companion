//go:build darwin

package web

import (
	"errors"
	"net"
	"os"
	"syscall"
)

// platformPeer asks macOS who owns the peer's socket: the kernel's TCP socket
// list (`net.inet.tcp.pcblist_n`, what `netstat` reads), whose socket records
// carry the uid of the credential that opened each one (`NEW_353A`). Read
// without cgo, through Go's own syscall.Sysctl.
//
// As on Linux, a live loopback peer is always in that list, so a list that
// cannot be read, or that does not hold the peer, is unidentified — refused
// (peer.go) — rather than served.
func platformPeer(local, remote *net.TCPAddr) peerVerdict {
	list, err := tcpPCBList()
	if err != nil {
		return peerUnidentified
	}
	uid, found := uidFromPCBList(list, remote, local)
	if !found {
		return peerUnidentified
	}
	if int(uid) == os.Getuid() {
		return peerSameUser
	}
	return peerOtherUser
}

// tcpPCBList reads the list. Its size is asked first and the list then copied;
// sockets opened in between can make the copy too small (ENOMEM), so it is
// asked again a bounded number of times.
func tcpPCBList() ([]byte, error) {
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		var value string
		value, err = syscall.Sysctl("net.inet.tcp.pcblist_n")
		if err == nil {
			return []byte(value), nil
		}
		if !errors.Is(err, syscall.ENOMEM) {
			return nil, err
		}
	}
	return nil, err
}
