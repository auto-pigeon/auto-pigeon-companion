//go:build windows

package web

import (
	"net"
	"syscall"
	"unsafe"
)

var procGetExtendedTcpTable = syscall.NewLazyDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

const (
	afINET                      = 2
	afINET6                     = 23
	tcpTableOwnerPIDConnections = 4
	errorInsufficientBuffer     = 122
	processQueryLimitedInfo     = 0x1000
)

// platformPeer finds the process that owns the peer's socket and compares the
// account its token runs as with this process's own.
//
// The peer is looked for in the IPv4 table and then in the IPv6 one, where a
// dual-stack client connected to ::ffff:127.0.0.1 is listed (`NEW_349`). Every
// failure — no table, no row, a process or token that could not be opened —
// is unidentified, and an unidentified peer is refused (peer.go): the peer
// chooses its socket, so "no answer" must not be a way in. A process this
// account started can always be opened for PROCESS_QUERY_LIMITED_INFORMATION
// by this account, so a denied open is itself the sign of another account.
func platformPeer(local, remote *net.TCPAddr) peerVerdict {
	pid, found := uint32(0), false
	if table, ok := tcpOwnerTable(afINET); ok {
		pid, found = ownerPIDFromTable(table, remote, local)
	}
	if !found {
		if table, ok := tcpOwnerTable(afINET6); ok {
			pid, found = ownerPIDFromTable6(table, remote, local)
		}
	}
	if !found {
		return peerUnidentified
	}
	theirs, ok := processSID(pid)
	if !ok {
		return peerUnidentified
	}
	ours, ok := processSID(uint32(syscall.Getpid()))
	if !ok {
		return peerUnidentified
	}
	if theirs == ours {
		return peerSameUser
	}
	return peerOtherUser
}

// tcpOwnerTable reads one address family's connection table with owning PIDs.
func tcpOwnerTable(family uintptr) ([]byte, bool) {
	size := uint32(64 << 10)
	for attempt := 0; attempt < 4; attempt++ {
		buffer := make([]byte, size)
		result, _, _ := procGetExtendedTcpTable.Call(
			uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)),
			0, family, tcpTableOwnerPIDConnections, 0)
		switch result {
		case 0:
			return buffer[:size], true
		case errorInsufficientBuffer:
			size += 16 << 10
			continue
		default:
			return nil, false
		}
	}
	return nil, false
}

// processSID is the account SID a process's token runs as.
func processSID(pid uint32) (string, bool) {
	handle, err := syscall.OpenProcess(processQueryLimitedInfo, false, pid)
	if err != nil {
		return "", false
	}
	defer syscall.CloseHandle(handle)
	var token syscall.Token
	if err := syscall.OpenProcessToken(handle, syscall.TOKEN_QUERY, &token); err != nil {
		return "", false
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", false
	}
	sid, err := user.User.Sid.String()
	if err != nil {
		return "", false
	}
	return sid, true
}
