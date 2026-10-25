//go:build linux

package web

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

// procTables are where Linux lists this network namespace's TCP sockets. The
// listener binds 127.0.0.1, but a client may connect to it from either family:
// an AF_INET socket is a row of /proc/net/tcp; an AF_INET6 dual-stack socket
// connected to ::ffff:127.0.0.1 is delivered to the same listener and is a row
// of /proc/net/tcp6 only, its addresses in the IPv4-mapped spelling (`NEW_349`).
var procTables = []struct {
	path   string
	mapped bool
}{
	{"/proc/net/tcp", false},
	{"/proc/net/tcp6", true},
}

// platformPeer reads the owner of the peer's own socket out of /proc/net/tcp
// and /proc/net/tcp6.
//
// The peer's socket is the row whose LOCAL address is the connection's remote
// end and whose REMOTE address is this listener; its `uid` column is the
// account that opened it. This process's own accepted socket is the mirror row
// and is never matched. A live loopback peer in this network namespace is
// always in one of the two tables, so finding it in neither — or reading
// neither — is unidentified, and refused (peer.go).
func platformPeer(local, remote *net.TCPAddr) peerVerdict {
	for _, table := range procTables {
		file, err := os.Open(table.path)
		if err != nil {
			continue
		}
		uid, found := socketOwner(file, remote, local, table.mapped)
		file.Close()
		if !found {
			continue
		}
		if uid == os.Getuid() {
			return peerSameUser
		}
		return peerOtherUser
	}
	return peerUnidentified
}

// socketOwner finds the uid of the socket bound at `self` and connected to
// `peer` in a /proc/net/tcp table, or — mapped — in a /proc/net/tcp6 table,
// where an IPv4 address is spelled as ::ffff:a.b.c.d.
func socketOwner(table io.Reader, self, peer *net.TCPAddr, mapped bool) (int, bool) {
	wantLocal, wantRemote := procAddress(self, mapped), procAddress(peer, mapped)
	if wantLocal == "" || wantRemote == "" {
		return 0, false
	}
	scanner := bufio.NewScanner(table)
	scanner.Scan() // the header line
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		// sl local_address rem_address st tx:rx tr:when retrnsmt uid ...
		if len(fields) < 8 || !strings.EqualFold(fields[1], wantLocal) || !strings.EqualFold(fields[2], wantRemote) {
			continue
		}
		uid, err := strconv.Atoi(fields[7])
		if err != nil {
			return 0, false
		}
		return uid, true
	}
	return 0, false
}

// procAddress spells an IPv4 address the way the kernel prints it: each 32-bit
// word of the address as the kernel's native-order word in hex, then the port.
// /proc/net/tcp has the one word; /proc/net/tcp6 (mapped) has four, of which
// the IPv4-mapped form is 0, 0, the ::ffff: marker and the IPv4 word.
func procAddress(address *net.TCPAddr, mapped bool) string {
	ip := address.IP.To4()
	if ip == nil {
		return ""
	}
	raw := []byte(ip)
	if mapped {
		raw = []byte(ip.To16())
	}
	var spelled strings.Builder
	for word := 0; word < len(raw); word += 4 {
		fmt.Fprintf(&spelled, "%08X", binary.NativeEndian.Uint32(raw[word:word+4]))
	}
	fmt.Fprintf(&spelled, ":%04X", address.Port)
	return spelled.String()
}
