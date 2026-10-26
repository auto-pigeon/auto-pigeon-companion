//go:build darwin || linux

package web

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// `NEW_353A`: a real connection from a second account, natively. A client run
// as `nobody` connects to this process's listener and the accepted connection
// is judged another account's — the exact verdict on Linux, not just a 403,
// which an unidentified peer would also get; on macOS, a refusing verdict. It
// needs a non-interactive `sudo` (the CI runners have one); without it the
// control is skipped and says so.
func TestASecondAccountsRealConnectionIsAnotherAccount(t *testing.T) {
	if err := exec.Command("sudo", "-n", "-u", "nobody", "true").Run(); err != nil {
		t.Skipf("no non-interactive sudo to the nobody account here: %v", err)
	}
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("no curl to connect from the second account")
	}
	listener, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url := fmt.Sprintf("http://127.0.0.1:%d/", listener.Addr().(*net.TCPAddr).Port)
	client := exec.CommandContext(ctx, "sudo", "-n", "-u", "nobody", curl, "-s", "-m", "10", "-o", "/dev/null", url)
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer client.Wait() //nolint:errcheck // the client is cut off below; its status is not the evidence
	_ = listener.(*net.TCPListener).SetDeadline(time.Now().Add(20 * time.Second))
	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("the second account's client never connected: %v", err)
	}
	defer conn.Close()
	verdict := identifyPeer(conn)
	t.Logf("%s: identifyPeer on nobody's connection = %d (other account %d, unidentified %d)",
		runtime.GOOS, verdict, peerOtherUser, peerUnidentified)
	switch {
	case runtime.GOOS == "linux" && verdict != peerOtherUser:
		// /proc/net/tcp lists every account's sockets: the answer is exact.
		t.Fatalf("identifyPeer on nobody's connection = %d, want peerOtherUser (%d)", verdict, peerOtherUser)
	case verdict != peerOtherUser && verdict != peerUnidentified:
		// macOS may not list another account's socket to this one; either way
		// the connection must be refused, never served.
		t.Fatalf("identifyPeer on nobody's connection = %d, which peerGuard serves", verdict)
	}
}
