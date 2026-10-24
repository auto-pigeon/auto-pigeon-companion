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

	// This account, and a peer the platform could not identify, are served as
	// before: only a positive answer refuses.
	for _, verdict := range []peerVerdict{peerSameUser, peerUnknown} {
		r := request(t, server, http.MethodGet, "/", "")
		r = r.WithContext(context.WithValue(r.Context(), peerKey{}, verdict))
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, r)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), server.token.Value()) {
			t.Fatalf("verdict %d: GET / answered %d without the page", verdict, recorder.Code)
		}
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
