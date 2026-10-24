package web

import (
	"encoding/binary"
	"net"
)

// ownerPIDFromTable finds the owning process of the socket bound at `self` and
// connected to `peer`, in the buffer Windows' GetExtendedTcpTable fills for
// TCP_TABLE_OWNER_PID_CONNECTIONS (a MIB_TCPTABLE_OWNER_PID).
//
// Platform-neutral on purpose, so the layout reading is tested on every
// platform rather than only where the syscall exists. The table is a DWORD
// count and then rows of six DWORDs — state, local address, local port, remote
// address, remote port, owning PID — little-endian words whose address bytes
// are in network order and whose ports are the first two bytes, big-endian.
func ownerPIDFromTable(table []byte, self, peer *net.TCPAddr) (uint32, bool) {
	selfIP, peerIP := self.IP.To4(), peer.IP.To4()
	if len(table) < 4 || selfIP == nil || peerIP == nil {
		return 0, false
	}
	const rowSize = 24
	count := int(binary.LittleEndian.Uint32(table[:4]))
	for i := 0; i < count; i++ {
		start := 4 + i*rowSize
		if start+rowSize > len(table) {
			return 0, false
		}
		row := table[start : start+rowSize]
		if net.IP(row[4:8]).Equal(selfIP) && int(binary.BigEndian.Uint16(row[8:10])) == self.Port &&
			net.IP(row[12:16]).Equal(peerIP) && int(binary.BigEndian.Uint16(row[16:18])) == peer.Port {
			return binary.LittleEndian.Uint32(row[20:24]), true
		}
	}
	return 0, false
}
