package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/urischeme"
)

// Settings can say whether this computer hands `autopigeon://` links to this
// Companion, and can make it so (NEW_307W): until now only a terminal could,
// and a Companion with no handler made the editor wait for a result that could
// never arrive. Run against a desktop of the test's own, on every host.
func TestSettingsReportsAndRegistersTheLinkHandler(t *testing.T) {
	m := newMachine(t)
	executable := filepath.Join(t.TempDir(), "my programs", "companion")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("stand-in"), 0o700); err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	previous := newURIRegistrar
	newURIRegistrar = func() *urischeme.Registrar {
		return &urischeme.Registrar{GOOS: "linux", Executable: executable, DataHome: data,
			Run: func(string, ...string) ([]byte, error) { return nil, nil }}
	}
	t.Cleanup(func() { newURIRegistrar = previous })

	status, body := m.call(http.MethodGet, "/api/v1/uri", nil)
	if status != http.StatusOK || body["registered"] != false || body["scheme"] != urischeme.Scheme {
		t.Fatalf("before: %d %v", status, body)
	}
	status, body = m.call(http.MethodPost, "/api/v1/uri/register", nil)
	if status != http.StatusOK || body["registered"] != true {
		t.Fatalf("register: %d %v", status, body)
	}
	// The whole path is one argument, spaces and all, and the link is the last.
	command, _ := body["command"].([]any)
	if len(command) < 3 || command[0] != executable || !strings.HasPrefix(command[len(command)-1].(string), "%") {
		t.Errorf("the handler runs %v", command)
	}
	status, body = m.call(http.MethodGet, "/api/v1/uri", nil)
	if status != http.StatusOK || body["registered"] != true {
		t.Fatalf("after: %d %v", status, body)
	}
}
