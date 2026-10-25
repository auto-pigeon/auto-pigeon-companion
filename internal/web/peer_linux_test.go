//go:build linux

package web

import (
	"encoding/binary"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
)

// The /proc/net/tcp reading: the PEER's row is the one whose local address is
// the connection's far end, never this process's mirror row.
func TestSocketOwnerReadsThePeersRowNotOurs(t *testing.T) {
	listener := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8789}
	peer := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 54321}
	table := strings.Join([]string{
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode",
		"   0: " + procAddress(listener, false) + " 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1 1",
		"   1: " + procAddress(listener, false) + " " + procAddress(peer, false) + " 01 00000000:00000000 00:00000000 00000000  1000        0 2 1",
		"   2: " + procAddress(peer, false) + " " + procAddress(listener, false) + " 01 00000000:00000000 00:00000000 00000000  4242        0 3 1",
	}, "\n")
	uid, found := socketOwner(strings.NewReader(table), peer, listener, false)
	if !found || uid != 4242 {
		t.Fatalf("socketOwner = %d, %v; want the peer's 4242", uid, found)
	}
	if _, found := socketOwner(strings.NewReader(table), &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}, listener, false); found {
		t.Fatal("a peer with no row was found")
	}
	if binary.NativeEndian.Uint16([]byte{1, 0}) == 1 && procAddress(listener, false) != "0100007F:2255" {
		t.Fatalf("procAddress = %q, want the kernel's little-endian spelling 0100007F:2255", procAddress(listener, false))
	}
}

// The /proc/net/tcp6 reading: a dual-stack client's row spells both ends as
// ::ffff:127.0.0.1, and its uid is read from there — another account's too.
func TestSocketOwnerReadsAMappedPeersRowInTCP6(t *testing.T) {
	listener := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8789}
	peer := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 54321}
	if binary.NativeEndian.Uint16([]byte{1, 0}) == 1 && procAddress(peer, true) != "0000000000000000FFFF00000100007F:D431" {
		t.Fatalf("procAddress(mapped) = %q, want the kernel's 0000000000000000FFFF00000100007F:D431", procAddress(peer, true))
	}
	table := strings.Join([]string{
		"  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode",
		"   0: " + procAddress(peer, true) + " " + procAddress(listener, true) + " 01 00000000:00000000 00:00000000 00000000  4242        0 9 1",
	}, "\n")
	uid, found := socketOwner(strings.NewReader(table), peer, listener, true)
	if !found || uid != 4242 {
		t.Fatalf("socketOwner(tcp6) = %d, %v; want the mapped peer's 4242", uid, found)
	}
	// The IPv4 spelling is not the tcp6 row: each table is read in its own.
	if _, found := socketOwner(strings.NewReader(table), peer, listener, false); found {
		t.Fatal("the tcp6 row was matched with the IPv4 spelling")
	}
}

// `NEW_349` (aucom/web/index-token-served-to-any-loopback-peer, NEW_342 N2): a
// client that connects from a dual-stack AF_INET6 socket to ::ffff:127.0.0.1
// reaches the 127.0.0.1 listener, but its own socket is listed only in
// /proc/net/tcp6, in the mapped spelling. It must still be identified — before
// this repair the lookup read /proc/net/tcp alone, found no row, and served it.
func TestAMappedIPv6PeerIsIdentified(t *testing.T) {
	listener, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- conn
	}()

	client, err := dialMappedLoopback(listener.Addr().(*net.TCPAddr).Port)
	if err != nil {
		t.Skipf("this host cannot open a dual-stack IPv6 socket: %v", err)
	}
	defer client.Close()
	conn, ok := <-accepted
	if !ok {
		t.Fatal("the listener accepted nothing")
	}
	defer conn.Close()

	if verdict := identifyPeer(conn); verdict != peerSameUser {
		t.Fatalf("identifyPeer on a ::ffff:127.0.0.1 client = %d, want peerSameUser (%d)", verdict, peerSameUser)
	}
}

// dialMappedLoopback opens what `curl http://[::ffff:127.0.0.1]:port/` opens: an
// AF_INET6 socket with IPV6_V6ONLY off, connected to the IPv4-mapped loopback.
// Go's own "tcp6" dial sets IPV6_V6ONLY, so the socket is made by hand.
func dialMappedLoopback(port int) (net.Conn, error) {
	fd, err := syscall.Socket(syscall.AF_INET6, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_IPV6, syscall.IPV6_V6ONLY, 0); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	address := &syscall.SockaddrInet6{Port: port}
	copy(address.Addr[:], net.ParseIP("::ffff:127.0.0.1").To16())
	if err := syscall.Connect(fd, address); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "mapped-loopback")
	defer file.Close()
	return net.FileConn(file)
}
