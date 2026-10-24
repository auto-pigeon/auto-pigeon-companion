//go:build linux

package web

import (
	"encoding/binary"
	"net"
	"strings"
	"testing"
)

// The /proc/net/tcp reading: the PEER's row is the one whose local address is
// the connection's far end, never this process's mirror row.
func TestSocketOwnerReadsThePeersRowNotOurs(t *testing.T) {
	listener := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8789}
	peer := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 54321}
	table := strings.Join([]string{
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode",
		"   0: " + procAddress(listener) + " 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1 1",
		"   1: " + procAddress(listener) + " " + procAddress(peer) + " 01 00000000:00000000 00:00000000 00000000  1000        0 2 1",
		"   2: " + procAddress(peer) + " " + procAddress(listener) + " 01 00000000:00000000 00:00000000 00000000  4242        0 3 1",
	}, "\n")
	uid, found := socketOwner(strings.NewReader(table), peer, listener)
	if !found || uid != 4242 {
		t.Fatalf("socketOwner = %d, %v; want the peer's 4242", uid, found)
	}
	if _, found := socketOwner(strings.NewReader(table), &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}, listener); found {
		t.Fatal("a peer with no row was found")
	}
	if binary.NativeEndian.Uint16([]byte{1, 0}) == 1 && procAddress(listener) != "0100007F:2255" {
		t.Fatalf("procAddress = %q, want the kernel's little-endian spelling 0100007F:2255", procAddress(listener))
	}
}
