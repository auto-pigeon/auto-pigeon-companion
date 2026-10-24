//go:build linux

package web

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

// platformPeer reads the owner of the peer's own socket out of /proc/net/tcp.
//
// The peer's socket is the row whose LOCAL address is the connection's remote
// end and whose REMOTE address is this listener; its `uid` column is the
// account that opened it. This process's own accepted socket is the mirror row
// and is never matched. The listener binds 127.0.0.1 only, so IPv4 is the table.
func platformPeer(local, remote *net.TCPAddr) peerVerdict {
	file, err := os.Open("/proc/net/tcp")
	if err != nil {
		return peerUnknown
	}
	defer file.Close()
	uid, found := socketOwner(file, remote, local)
	if !found {
		return peerUnknown
	}
	if uid == os.Getuid() {
		return peerSameUser
	}
	return peerOtherUser
}

// socketOwner finds the uid of the socket bound at `self` and connected to
// `peer` in a /proc/net/tcp table.
func socketOwner(table io.Reader, self, peer *net.TCPAddr) (int, bool) {
	wantLocal, wantRemote := procAddress(self), procAddress(peer)
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

// procAddress spells an IPv4 address the way /proc/net/tcp does: the 32-bit
// address as the kernel's native-order word, then the port, both in hex.
func procAddress(address *net.TCPAddr) string {
	ip := address.IP.To4()
	if ip == nil {
		return ""
	}
	word := binary.NativeEndian.Uint32(ip)
	var raw [4]byte
	binary.BigEndian.PutUint32(raw[:], word)
	return strings.ToUpper(hex.EncodeToString(raw[:])) + ":" + strings.ToUpper(strconv.FormatUint(uint64(address.Port)|0x10000, 16)[1:])
}
