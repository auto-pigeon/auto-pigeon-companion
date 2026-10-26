//go:build darwin

package web

import (
	"net"
	"os"
	"syscall"
	"testing"
)

// `NEW_353A`: on macOS the owner of a loopback connection is asked of the
// kernel's socket list, natively. A connection this test process opens — from
// an AF_INET socket, and from an AF_INET6 dual-stack socket connected to
// ::ffff:127.0.0.1, which reaches the same 127.0.0.1 listener — is this
// account's. Before NEW_353A macOS could not ask, and served every connection.
func TestAMacPeerIsIdentifiedByItsSocketOwner(t *testing.T) {
	if _, err := tcpPCBList(); err != nil {
		t.Fatalf("net.inet.tcp.pcblist_n cannot be read: %v", err)
	}
	for _, c := range []struct {
		name string
		dial func(port int) (net.Conn, error)
	}{
		{"ipv4", func(port int) (net.Conn, error) {
			return net.DialTCP("tcp4", nil, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
		}},
		{"mapped-ipv6", dialMappedLoopbackDarwin},
	} {
		t.Run(c.name, func(t *testing.T) {
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
			client, err := c.dial(listener.Addr().(*net.TCPAddr).Port)
			if err != nil {
				t.Skipf("this host cannot open that client socket: %v", err)
			}
			defer client.Close()
			conn, ok := <-accepted
			if !ok {
				t.Fatal("the listener accepted nothing")
			}
			defer conn.Close()

			list, err := tcpPCBList()
			if err != nil {
				t.Fatal(err)
			}
			local, remote := conn.LocalAddr().(*net.TCPAddr), conn.RemoteAddr().(*net.TCPAddr)
			uid, found := uidFromPCBList(list, remote, local)
			if !found || int(uid) != os.Getuid() {
				t.Fatalf("the peer's socket in pcblist_n (%d bytes): uid %d found %v; want %d", len(list), uid, found, os.Getuid())
			}
			if _, mirror := uidFromPCBList(list, local, &net.TCPAddr{IP: remote.IP, Port: 1}); mirror {
				t.Fatal("a tuple nobody connected was found")
			}
			if verdict := identifyPeer(conn); verdict != peerSameUser {
				t.Fatalf("identifyPeer = %d, want peerSameUser (%d)", verdict, peerSameUser)
			}
		})
	}
}

// dialMappedLoopbackDarwin opens an AF_INET6 socket with IPV6_V6ONLY off,
// connected to ::ffff:127.0.0.1 — Go's "tcp6" dial sets IPV6_V6ONLY.
func dialMappedLoopbackDarwin(port int) (net.Conn, error) {
	fd, err := syscall.Socket(syscall.AF_INET6, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(fd)
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
