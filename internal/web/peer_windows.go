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
	tcpTableOwnerPIDConnections = 4
	errorInsufficientBuffer     = 122
	processQueryLimitedInfo     = 0x1000
)

// platformPeer finds the process that owns the peer's socket and compares the
// account its token runs as with this process's own.
//
// Every failure — the table, opening the process, reading its token — answers
// "unknown", and an unknown peer is served as before: a refusal here must be a
// fact the system stated, never the absence of an answer.
func platformPeer(local, remote *net.TCPAddr) peerVerdict {
	table, ok := tcpOwnerTable()
	if !ok {
		return peerUnknown
	}
	pid, found := ownerPIDFromTable(table, remote, local)
	if !found {
		return peerUnknown
	}
	theirs, ok := processSID(pid)
	if !ok {
		return peerUnknown
	}
	ours, ok := processSID(uint32(syscall.Getpid()))
	if !ok {
		return peerUnknown
	}
	if theirs == ours {
		return peerSameUser
	}
	return peerOtherUser
}

// tcpOwnerTable reads the IPv4 connection table with owning PIDs.
func tcpOwnerTable() ([]byte, bool) {
	size := uint32(64 << 10)
	for attempt := 0; attempt < 4; attempt++ {
		buffer := make([]byte, size)
		result, _, _ := procGetExtendedTcpTable.Call(
			uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)),
			0, afINET, tcpTableOwnerPIDConnections, 0)
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
