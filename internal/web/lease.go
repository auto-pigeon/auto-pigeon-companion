package web

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The page's lease: one WebSocket per open page, which is how this process
// knows a page is open. See lifecycle.go for why a lease and not an unload
// event.
//
// # Why a WebSocket, and why written here
//
// A long-lived fetch would work in one tab and fail in seven: browsers allow
// six HTTP/1.1 connections per host, shared by every tab, and a page whose
// lease held one of them would starve the seventh tab's API calls. WebSocket
// connections are not in that pool.
//
// The server side needed here is small — accept an upgrade, send short text
// frames, read the client's frames to know it is alive — and this repository
// has no dependency at all (go.mod is the standard library), so it is written
// out rather than imported: RFC 6455's handshake and frame format, nothing
// from its extensions, and no fragmentation beyond tolerating it.
//
// # Authentication
//
// The route is in [Server.api], so the guard's four checks apply: loopback
// Host, matching Origin (a browser ALWAYS sends Origin on a WebSocket), the
// Sec-Fetch-Site check, and this run's token. A page cannot set a header on a
// WebSocket, so the token travels as a subprotocol, `aucom.token.<token>`, which
// is still a request header and never a URL: it is not in history, not in a
// Referer, and not in any log this program writes. It is the same per-run token,
// not a second credential.

// leaseProtocol is the subprotocol the server selects, naming this message
// format. The token subprotocol is offered beside it and never echoed.
const leaseProtocol = "aucom.lease.v1"

// leaseTokenPrefix marks the subprotocol that carries the token.
const leaseTokenPrefix = "aucom.token."

// leaseBeat is how often the server speaks, so a page can tell a Companion that
// went away from one that is quiet. The page beats too, which keeps the socket
// honest through anything between the two.
//
// There is deliberately NO silence deadline on the server's side. This is a
// loopback socket: when a tab closes, a renderer crashes or the browser is
// killed, the operating system closes it and the read below returns at once.
// What a deadline would add is a way to lose a page that is still open —
// browsers throttle a hidden tab's timers to once a minute and freeze some
// tabs outright, and a frozen tab is still a window the person has open. A
// tab the browser DISCARDS is closed, and is counted as closed.
const (
	leaseBeat = 10 * time.Second
	// leaseMaxFrame bounds a client frame. The page sends a dozen bytes.
	leaseMaxFrame = 4 << 10
)

// websocketGUID is RFC 6455's fixed accept-key suffix.
const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Opcodes this server reads or writes.
const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

// leaseToken is the token a WebSocket handshake offered as a subprotocol.
// Empty on anything that is not a WebSocket upgrade, so no other route learns
// to read a credential from here.
func leaseToken(r *http.Request) string {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return ""
	}
	for _, header := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, offered := range strings.Split(header, ",") {
			offered = strings.TrimSpace(offered)
			if value, ok := strings.CutPrefix(offered, leaseTokenPrefix); ok {
				return value
			}
		}
	}
	return ""
}

// leaseMessage is what the server sends. One shape, so the page has one parser.
type leaseMessage struct {
	Type string `json:"type"`
	// hello
	Mode         string `json:"mode,omitempty"`
	GraceSeconds int    `json:"grace_seconds,omitempty"`
	Version      string `json:"version,omitempty"`
	// stopping
	Cause ExitCause `json:"cause,omitempty"`
}

// handleLease holds one page's lease for as long as its WebSocket is open.
func (s *Server) handleLease(w http.ResponseWriter, r *http.Request) {
	conn, rw, err := upgradeWebSocket(w, r)
	if err != nil {
		// upgradeWebSocket has already answered.
		s.logf("lifecycle: a page's lease handshake was refused (%s): %v", leasePeer(r), err)
		return
	}
	lease := &leaseConn{conn: conn}
	defer conn.Close()

	release, stopping := s.lifecycle.acquire(leasePeer(r), lease.stop)
	// Why this lease ended, as the log says it: the browser closing the
	// socket (a tab closed, a reload, a crash), the page's own close frame,
	// or the Companion stopping. Set by whichever of those came first.
	reason := "the connection ended"
	defer func() { release(lease.endReason(reason)) }()
	if stopping {
		lease.stop(s.lifecycle.Cause())
		return
	}

	mode := "server"
	if s.lifecycle.Interactive() {
		mode = "interactive"
	}
	if err := lease.send(leaseMessage{Type: "hello", Mode: mode,
		GraceSeconds: int(s.lifecycle.CloseGrace() / time.Second), Version: s.version}); err != nil {
		reason = "the hello could not be sent: " + err.Error()
		return
	}

	// The writer: a beat every leaseBeat, so the page knows the process is
	// there even when nothing else is happening.
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(leaseBeat)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := lease.send(leaseMessage{Type: "beat"}); err != nil {
					lease.setEnd("a beat could not be written: " + err.Error())
					conn.Close()
					return
				}
			}
		}
	}()

	// The reader: a close frame or a closed socket ends the lease.
	for {
		opcode, payload, err := readFrame(rw.Reader, leaseMaxFrame)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				reason = "the browser closed the socket without a close frame"
			} else {
				reason = "reading from the page failed: " + err.Error()
			}
			return
		}
		switch opcode {
		case opClose:
			reason = "the page closed it" + describeClose(payload)
			lease.write(opClose, closePayload(1000, ""))
			return
		case opPing:
			if err := lease.write(opPong, payload); err != nil {
				reason = "a pong could not be written: " + err.Error()
				return
			}
		}
	}
}

// leasePeer describes the page holding a lease, for the log: the connection's
// local port on the browser's side and the browser's own name and version.
// Never a header that could carry a credential, and never the subprotocols.
func leasePeer(r *http.Request) string {
	peer := r.RemoteAddr
	if _, port, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		peer = "port " + port
	}
	agent := strings.Map(func(c rune) rune {
		if c < 0x20 || c == 0x7f {
			return -1
		}
		return c
	}, r.UserAgent())
	if len(agent) > 160 {
		agent = agent[:160] + "…"
	}
	if agent == "" {
		agent = "no user agent"
	}
	return peer + ", " + agent
}

// describeClose renders a close frame's status code and reason for the log.
// 1001 is what a browser sends when the tab or window is closed or the page is
// navigated away.
func describeClose(payload []byte) string {
	if len(payload) < 2 {
		return " with no status code"
	}
	code := binary.BigEndian.Uint16(payload)
	text := fmt.Sprintf(" (code %d", code)
	if code == 1001 {
		text += ", going away: a tab closed or navigated"
	}
	if reason := strings.TrimSpace(string(payload[2:])); reason != "" {
		text += ", " + fmt.Sprintf("%q", reason)
	}
	return text + ")"
}

// leaseConn serialises writes: the beat, the hello and the stop come from
// different goroutines.
type leaseConn struct {
	mu     sync.Mutex
	conn   net.Conn
	closed bool
	// end is why the lease ended when it was not the page's side that ended
	// it: the Companion stopping, or a beat that could not be written.
	end string
}

// setEnd records why the lease ended, first reason wins.
func (c *leaseConn) setEnd(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.end == "" {
		c.end = reason
	}
}

// endReason is the recorded reason if there is one, else the reader's.
func (c *leaseConn) endReason(reader string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.end != "" {
		return c.end
	}
	return reader
}

func (c *leaseConn) write(opcode byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := c.conn.Write(frame(opcode, payload))
	return err
}

func (c *leaseConn) send(message leaseMessage) error {
	raw, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return c.write(opText, raw)
}

// stop tells the page why the process is stopping, then closes: 1001 is
// "going away", which is what a server shutting down says.
func (c *leaseConn) stop(cause ExitCause) {
	c.setEnd("the Companion is stopping (" + string(cause) + ")")
	c.send(leaseMessage{Type: "stopping", Cause: cause})
	c.write(opClose, closePayload(1001, string(cause)))
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.conn.Close()
}

// upgradeWebSocket completes the RFC 6455 handshake and hands back the raw
// connection. On a refusal it has written the HTTP answer itself.
func upgradeWebSocket(w http.ResponseWriter, r *http.Request) (net.Conn, *bufio.ReadWriter, error) {
	refuse := func(status int, message string) (net.Conn, *bufio.ReadWriter, error) {
		writeError(w, status, errors.New(message))
		return nil, nil, errors.New(message)
	}
	if !headerHasToken(r.Header, "Connection", "upgrade") || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return refuse(http.StatusUpgradeRequired, "this route is a WebSocket: the page opens it to say it is still open")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		w.Header().Set("Sec-WebSocket-Version", "13")
		return refuse(http.StatusUpgradeRequired, "this server speaks WebSocket version 13")
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if decoded, err := base64.StdEncoding.DecodeString(key); err != nil || len(decoded) != 16 {
		return refuse(http.StatusBadRequest, "the WebSocket handshake has no valid Sec-WebSocket-Key")
	}
	if !headerHasToken(r.Header, "Sec-WebSocket-Protocol", leaseProtocol) {
		return refuse(http.StatusBadRequest, "the lease needs the "+leaseProtocol+" subprotocol")
	}
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return refuse(http.StatusInternalServerError, "this connection cannot be upgraded: "+err.Error())
	}
	// Whatever deadline net/http left on the connection is for reading one
	// request's headers; this connection lives as long as the page.
	conn.SetDeadline(time.Time{})
	sum := sha1.Sum([]byte(key + websocketGUID))
	response := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n" +
		"Sec-WebSocket-Protocol: " + leaseProtocol + "\r\n\r\n"
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := rw.WriteString(response); err != nil {
		conn.Close()
		return nil, nil, err
	}
	if err := rw.Flush(); err != nil {
		conn.Close()
		return nil, nil, err
	}
	return conn, rw, nil
}

// headerHasToken reports whether a comma-separated header lists a token,
// ignoring case.
func headerHasToken(header http.Header, name, token string) bool {
	for _, value := range header.Values(name) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// frame encodes one unmasked server frame with FIN set.
func frame(opcode byte, payload []byte) []byte {
	header := []byte{0x80 | opcode}
	switch n := len(payload); {
	case n < 126:
		header = append(header, byte(n))
	case n <= 0xFFFF:
		header = append(header, 126, byte(n>>8), byte(n))
	default:
		header = append(header, 127)
		header = binary.BigEndian.AppendUint64(header, uint64(n))
	}
	return append(header, payload...)
}

// closePayload is a close frame's status code and reason, the reason cut to
// fit the 125-byte control-frame limit.
func closePayload(code uint16, reason string) []byte {
	if len(reason) > 123 {
		reason = reason[:123]
	}
	return append(binary.BigEndian.AppendUint16(nil, code), reason...)
}

// readFrame reads one client frame and unmasks it. A client frame that is not
// masked, or is larger than max, is a protocol error.
func readFrame(reader *bufio.Reader, max int) (byte, []byte, error) {
	var head [2]byte
	if _, err := io.ReadFull(reader, head[:]); err != nil {
		return 0, nil, err
	}
	opcode := head[0] & 0x0F
	if head[1]&0x80 == 0 {
		return 0, nil, errors.New("websocket: a client frame was not masked")
	}
	length := uint64(head[1] & 0x7F)
	switch length {
	case 126:
		var extended [2]byte
		if _, err := io.ReadFull(reader, extended[:]); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(extended[:]))
	case 127:
		var extended [8]byte
		if _, err := io.ReadFull(reader, extended[:]); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(extended[:])
	}
	if length > uint64(max) {
		return 0, nil, fmt.Errorf("websocket: a %d-byte frame is larger than this lease accepts", length)
	}
	var mask [4]byte
	if _, err := io.ReadFull(reader, mask[:]); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	switch opcode {
	case opContinuation, opText, opBinary, opClose, opPing, opPong:
		return opcode, payload, nil
	}
	return 0, nil, fmt.Errorf("websocket: opcode %#x is not one this lease knows", opcode)
}
