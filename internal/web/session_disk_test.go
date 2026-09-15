package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
)

// NEW_244D: `companion auth login` in a terminal was invisible to a running
// page until the Companion restarted. The page now takes the session from the
// config file on its next status request — and a sign-out the same way.
func TestASignInMadeInATerminalReachesARunningPage(t *testing.T) {
	var mu sync.Mutex
	onDisk := config.Config{AUBBaseURL: "http://127.0.0.1:9"}
	client, err := aub.New(onDisk.AUBBaseURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(Options{
		Version: "test",
		Config:  onDisk,
		Client:  client,
		UpdateConfig: func(mutate func(*config.Config) error) (config.Config, error) {
			mu.Lock()
			defer mu.Unlock()
			return onDisk, mutate(&onDisk)
		},
		ReadConfig: func() (config.Config, error) {
			mu.Lock()
			defer mu.Unlock()
			return onDisk, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)

	status := func() (bool, string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		request.Host = testHost
		request.Header.Set(tokenHeader, server.Token().Value())
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		var body statusBody
		if recorder.Code != http.StatusOK {
			t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
		}
		return body.Authenticated, body.Email
	}

	if signedIn, _ := status(); signedIn {
		t.Fatal("signed in before anybody signed in")
	}
	mu.Lock()
	onDisk.Session = config.Session{Token: "terminal-token", Email: "user1@aup.com"}
	mu.Unlock()
	if signedIn, email := status(); !signedIn || email != "user1@aup.com" {
		t.Errorf("after a terminal sign-in the page says signed in = %v as %q", signedIn, email)
	}
	mu.Lock()
	onDisk.Session = config.Session{}
	mu.Unlock()
	if signedIn, _ := status(); signedIn {
		t.Error("after a terminal sign-out the page still says signed in")
	}
}
