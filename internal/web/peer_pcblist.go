package web

import (
	"encoding/binary"
	"net"
)

// The layout macOS's `net.inet.tcp.pcblist_n` sysctl returns, which is how
// `netstat` lists TCP sockets there and the native answer to "which account
// owns the other end of this loopback connection" (`NEW_353A`). No cgo: Go's
// own syscall.Sysctl reads it.
//
// The buffer is a `struct xinpgen` header, then one item per socket — a run of
// records, each beginning with its own length and kind and each starting on an
// 8-byte boundary (xnu `bsd/netinet/in_pcblist.c`, get_pcblist_n): an
// XSO_INPCB `struct xinpcb_n`, then the XSO_SOCKET `struct xsocket_n` of the
// same socket, then its buffers, statistics and TCP state — and a trailing
// xinpgen. Both structures are declared under `#pragma pack(4)`
// (`bsd/netinet/in_pcb.h`, `bsd/sys/socketvar.h`); the offsets below are what a
// compiler gives them there, 104 bytes each, and a record of another length is
// not read as one of them.
const (
	pcbKindInpcb  = 0x010 // XSO_INPCB
	pcbKindSocket = 0x001 // XSO_SOCKET

	xinpcbNSize   = 104
	xinpcbFport   = 16 // u_short, network order
	xinpcbLport   = 18 // u_short, network order
	xinpcbVflag   = 44 // INP_IPV4 0x1, INP_IPV6 0x2
	xinpcbFaddr   = 48 // in6_addr, or in_addr_4in6 (the IPv4 address in its last four bytes)
	xinpcbLaddr   = 64
	xsocketNSize  = 104
	xsocketNUID   = 64 // uid_t: kauth_cred_getuid(so->so_cred)
	inpFlagIPv4   = 0x1
	pcbRecordHead = 8
)

// uidFromPCBList finds the owner of the socket bound at `self` and connected
// to `peer` in a pcblist_n buffer. platformPeer asks with self = the
// connection's remote end and peer = this listener: the peer's own socket,
// never this process's accepted socket, which is its mirror. A client that
// connected from an AF_INET6 dual-stack socket to ::ffff:127.0.0.1 is converted
// to IPv4 by the kernel when it connects (INP_IPV4, the address in the last
// four bytes), and an IPv6 socket's mapped spelling reads as the same IPv4
// address, so one comparison covers both. Two matching sockets that disagree about their owner are no
// answer. Platform-neutral on purpose, like ownerPIDFromTable: the reading is
// tested on every platform, the sysctl only on macOS.
func uidFromPCBList(buf []byte, self, peer *net.TCPAddr) (uint32, bool) {
	selfIP, peerIP := self.IP.To4(), peer.IP.To4()
	if len(buf) < pcbRecordHead || selfIP == nil || peerIP == nil {
		return 0, false
	}
	order := binary.NativeEndian
	offset := roundUp8(int(order.Uint32(buf[0:4]))) // the xinpgen header's own length
	var (
		pending bool // the last XSO_INPCB record is the connection's peer
		uid     uint32
		found   bool
	)
	for offset >= pcbRecordHead && offset+pcbRecordHead <= len(buf) {
		length := int(order.Uint32(buf[offset : offset+4]))
		kind := order.Uint32(buf[offset+4 : offset+8])
		if length < pcbRecordHead || offset+length > len(buf) {
			break
		}
		record := buf[offset : offset+length]
		switch {
		case kind == pcbKindInpcb && length == xinpcbNSize:
			pending = int(binary.BigEndian.Uint16(record[xinpcbLport:])) == self.Port &&
				int(binary.BigEndian.Uint16(record[xinpcbFport:])) == peer.Port &&
				pcbAddress(record, xinpcbLaddr).Equal(selfIP) &&
				pcbAddress(record, xinpcbFaddr).Equal(peerIP)
		case kind == pcbKindSocket && length == xsocketNSize:
			if pending {
				owner := order.Uint32(record[xsocketNUID:])
				if found && owner != uid {
					return 0, false
				}
				uid, found = owner, true
			}
			pending = false
		}
		offset += roundUp8(length)
	}
	return uid, found
}

// pcbAddress reads one end of an xinpcb_n as IPv4: an INP_IPV4 socket keeps its
// address in the last four bytes of the 16; an IPv6 one is IPv4 only when it is
// the mapped spelling, and otherwise is no IPv4 address at all.
func pcbAddress(record []byte, at int) net.IP {
	raw := record[at : at+16]
	if record[xinpcbVflag]&inpFlagIPv4 != 0 {
		return net.IP(append([]byte(nil), raw[12:16]...))
	}
	return net.IP(append([]byte(nil), raw...)).To4()
}

func roundUp8(n int) int { return (n + 7) &^ 7 }
