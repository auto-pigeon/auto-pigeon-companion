package web

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// `NEW_342` lead `aucom/web/index-token-served-to-any-loopback-peer`, repaired in
// `NEW_348`: a connection another OS account opened gets nothing — not the
// token-carrying page, not an asset, not the API.
func TestAnotherAccountsConnectionIsRefusedEverywhere(t *testing.T) {
	server, _ := newTestServer(t, nil)

	for _, path := range []string{"/", "/index.html", "/app.js", "/api/status"} {
		r := request(t, server, http.MethodGet, path, server.token.Value())
		r = r.WithContext(context.WithValue(r.Context(), peerKey{}, peerOtherUser))
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, r)
		if recorder.Code != http.StatusForbidden {
			t.Errorf("GET %s from another account answered %d, want 403", path, recorder.Code)
		}
		if strings.Contains(recorder.Body.String(), server.token.Value()) {
			t.Fatalf("GET %s from another account carried the token", path)
		}
	}

	// This account, and a handler called with no accepted connection behind it,
	// are served as before.
	for _, verdict := range []peerVerdict{peerSameUser, peerNotChecked} {
		r := request(t, server, http.MethodGet, "/", "")
		r = r.WithContext(context.WithValue(r.Context(), peerKey{}, verdict))
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, r)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), server.token.Value()) {
			t.Fatalf("verdict %d: GET / answered %d without the page", verdict, recorder.Code)
		}
	}
}

// `NEW_349`: a peer the platform COULD identify and did not is refused like
// another account. The peer chooses its socket, so "no answer" is something
// another account can ask for (an AF_INET6 dual-stack socket to
// ::ffff:127.0.0.1 was not found in /proc/net/tcp and got the token).
func TestAnUnidentifiedPeerIsRefusedEverywhere(t *testing.T) {
	server, _ := newTestServer(t, nil)
	for _, path := range []string{"/", "/index.html", "/app.js", "/api/status"} {
		r := request(t, server, http.MethodGet, path, server.token.Value())
		r = r.WithContext(context.WithValue(r.Context(), peerKey{}, peerUnidentified))
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, r)
		if recorder.Code != http.StatusForbidden {
			t.Errorf("GET %s from an unidentified peer answered %d, want 403", path, recorder.Code)
		}
		if strings.Contains(recorder.Body.String(), server.token.Value()) {
			t.Fatalf("GET %s from an unidentified peer carried the token", path)
		}
	}
}

// `NEW_353A`: a platform that cannot ask at all is not served unasked any more.
// Every released platform can ask (macOS since NEW_353A), so this verdict is
// only an unreleased build's, and it refuses like an unidentified peer.
func TestAPlatformThatCannotAskIsRefusedEverywhere(t *testing.T) {
	server, _ := newTestServer(t, nil)
	for _, path := range []string{"/", "/index.html", "/app.js", "/api/status"} {
		r := request(t, server, http.MethodGet, path, server.token.Value())
		r = r.WithContext(context.WithValue(r.Context(), peerKey{}, peerUnsupported))
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, r)
		if recorder.Code != http.StatusForbidden {
			t.Errorf("GET %s on a platform that cannot ask answered %d, want 403", path, recorder.Code)
		}
		if strings.Contains(recorder.Body.String(), server.token.Value()) {
			t.Fatalf("GET %s on a platform that cannot ask carried the token", path)
		}
	}
}

// The macOS socket list (net.inet.tcp.pcblist_n), read on every platform: the
// PEER's socket is found by its own local end, never this listener's mirror
// socket, an IPv4-mapped client is found by its IPv4 address, a truncated or
// re-laid-out list answers nothing, and two sockets that disagree are no answer.
func TestUIDFromPCBListReadsTheMacLayout(t *testing.T) {
	listener := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8789}
	peer := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 54321}
	order := binary.NativeEndian
	record := func(kind uint32, size int) []byte {
		out := make([]byte, size)
		order.PutUint32(out[0:4], uint32(size))
		order.PutUint32(out[4:8], kind)
		return out
	}
	item := func(local, foreign *net.TCPAddr, uid uint32, vflag byte, mapped bool) []byte {
		pcb := record(pcbKindInpcb, xinpcbNSize)
		binary.BigEndian.PutUint16(pcb[xinpcbFport:], uint16(foreign.Port))
		binary.BigEndian.PutUint16(pcb[xinpcbLport:], uint16(local.Port))
		pcb[xinpcbVflag] = vflag
		if mapped { // what an IPv6 socket's own spelling of a mapped address is
			copy(pcb[xinpcbFaddr:], foreign.IP.To16())
			copy(pcb[xinpcbLaddr:], local.IP.To16())
		} else { // in_addr_4in6: the IPv4 address in the last four bytes
			copy(pcb[xinpcbFaddr+12:], foreign.IP.To4())
			copy(pcb[xinpcbLaddr+12:], local.IP.To4())
		}
		socket := record(pcbKindSocket, xsocketNSize)
		order.PutUint32(socket[xsocketNUID:], uid)
		out := append(pcb, socket...)
		for _, kind := range []uint32{0x002, 0x004, 0x008} { // buffers and statistics
			out = append(out, record(kind, 36)...)
			out = append(out, make([]byte, 4)...) // to the next 8-byte boundary
		}
		return append(out, record(0x020, 208)...) // TCP state
	}
	header := func(count uint32) []byte {
		out := make([]byte, 24)
		order.PutUint32(out[0:4], 24)
		order.PutUint32(out[4:8], count)
		return out
	}
	list := header(3)
	list = append(list, item(listener, &net.TCPAddr{IP: net.IPv4zero, Port: 0}, 501, inpFlagIPv4, false)...)
	list = append(list, item(listener, peer, 501, inpFlagIPv4, false)...) // this process's accepted socket
	list = append(list, item(peer, listener, 4242, inpFlagIPv4, false)...)
	list = append(list, header(pcbKindInpcb)...) // a trailer whose count looks like a kind

	if uid, found := uidFromPCBList(list, peer, listener); !found || uid != 4242 {
		t.Fatalf("uidFromPCBList = %d, %v; want the peer's 4242", uid, found)
	}
	if _, found := uidFromPCBList(list, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}, listener); found {
		t.Fatal("a peer with no socket was found")
	}
	if _, found := uidFromPCBList(list[:24+xinpcbNSize+50], peer, listener); found {
		t.Fatal("a truncated list produced an answer")
	}

	// An AF_INET6 socket whose addresses are still the mapped spelling.
	mapped := append(header(1), item(peer, listener, 4243, 0x2, true)...)
	if uid, found := uidFromPCBList(mapped, peer, listener); !found || uid != 4243 {
		t.Fatalf("uidFromPCBList(mapped) = %d, %v; want 4243", uid, found)
	}

	// Two sockets on one tuple with different owners: no answer.
	twice := append(append(header(2), item(peer, listener, 4242, inpFlagIPv4, false)...), item(peer, listener, 501, inpFlagIPv4, false)...)
	if _, found := uidFromPCBList(twice, peer, listener); found {
		t.Fatal("two disagreeing owners produced an answer")
	}

	// A kernel whose structures changed size is not read with these offsets.
	other := append(header(1), item(peer, listener, 4242, inpFlagIPv4, false)...)
	order.PutUint32(other[24:28], xinpcbNSize+8)
	if _, found := uidFromPCBList(other, peer, listener); found {
		t.Fatal("a record of another length was read as an xinpcb_n")
	}
}

// The real listener and the real ConnContext: a connection this test process
// opens is this account's, and gets the page.
func TestThisAccountsOwnConnectionGetsThePage(t *testing.T) {
	server, _ := newTestServer(t, nil)
	listener, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, listener, server) }()
	t.Cleanup(func() { cancel(); <-done })

	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(URL(listener))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), server.token.Value()) {
		t.Fatalf("this account's own GET / answered %d", response.StatusCode)
	}
}

// The Windows table layout, read on every platform.
func TestOwnerPIDFromTableReadsTheWindowsLayout(t *testing.T) {
	listener := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8789}
	peer := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 54321}
	row := func(local, remote *net.TCPAddr, pid uint32) []byte {
		out := make([]byte, 24)
		binary.LittleEndian.PutUint32(out[0:4], 5) // ESTABLISHED
		copy(out[4:8], local.IP.To4())
		binary.BigEndian.PutUint16(out[8:10], uint16(local.Port))
		copy(out[12:16], remote.IP.To4())
		binary.BigEndian.PutUint16(out[16:18], uint16(remote.Port))
		binary.LittleEndian.PutUint32(out[20:24], pid)
		return out
	}
	table := make([]byte, 4)
	binary.LittleEndian.PutUint32(table, 2)
	table = append(table, row(listener, peer, 100)...)
	table = append(table, row(peer, listener, 4242)...)
	if pid, found := ownerPIDFromTable(table, peer, listener); !found || pid != 4242 {
		t.Fatalf("ownerPIDFromTable = %d, %v; want the peer's 4242", pid, found)
	}
	if _, found := ownerPIDFromTable(table[:30], peer, listener); found {
		t.Fatal("a truncated table produced an answer")
	}
}

// The Windows IPv6 table layout, read on every platform: a dual-stack client
// connected to ::ffff:127.0.0.1 is found there by its mapped addresses.
func TestOwnerPIDFromTable6ReadsAMappedPeer(t *testing.T) {
	listener := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8789}
	peer := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 54321}
	row := func(local, remote *net.TCPAddr, pid uint32) []byte {
		out := make([]byte, 56)
		copy(out[0:16], local.IP.To16())
		binary.BigEndian.PutUint16(out[20:22], uint16(local.Port))
		copy(out[24:40], remote.IP.To16())
		binary.BigEndian.PutUint16(out[44:46], uint16(remote.Port))
		binary.LittleEndian.PutUint32(out[48:52], 5) // ESTABLISHED
		binary.LittleEndian.PutUint32(out[52:56], pid)
		return out
	}
	table := make([]byte, 4)
	binary.LittleEndian.PutUint32(table, 2)
	table = append(table, row(listener, peer, 100)...)
	table = append(table, row(peer, listener, 4242)...)
	if pid, found := ownerPIDFromTable6(table, peer, listener); !found || pid != 4242 {
		t.Fatalf("ownerPIDFromTable6 = %d, %v; want the mapped peer's 4242", pid, found)
	}
	if _, found := ownerPIDFromTable6(table[:70], peer, listener); found {
		t.Fatal("a truncated table produced an answer")
	}
}
