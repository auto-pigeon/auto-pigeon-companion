package aub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeAUB stands in for PocketBase: it answers the two auth endpoints the Companion uses
// and records what it was sent, so the request shape is asserted rather than
// assumed.
func fakeAUB(t *testing.T) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/collections/users/auth-with-password", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		var body struct {
			Identity string `json:"identity"`
			Password string `json:"password"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Password != "correct" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"code": 400, "message": "Failed to authenticate."})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"token":  "token-1",
			"record": map[string]any{"id": "user-1", "email": body.Identity},
		})
	})
	mux.HandleFunc("POST /api/collections/users/auth-refresh", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"code": 401, "message": "Missing auth."})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"token":  "token-2",
			"record": map[string]any{"id": "user-1", "email": "a@example"},
		})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, &seen
}

func TestLogin(t *testing.T) {
	server, seen := fakeAUB(t)
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	session, err := client.Login(context.Background(), "a@example", "correct")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if session.Token != "token-1" || session.UserID != "user-1" || session.Email != "a@example" {
		t.Errorf("session = %+v", session)
	}
	// Expires is ZERO until a deployment has said how long its tokens last.
	// That is the honest answer — config.Session.Valid reads it as "unknown,
	// treat as valid" — and it replaced a two-week estimate copied out of
	// PocketBase's documentation, which was wrong on every deployment whose
	// operator had configured anything else.
	if !session.Expires.IsZero() {
		t.Errorf("Expires = %v, want zero until the deployment has been asked",
			session.Expires)
	}
	// A successful login must leave the client authenticated; otherwise every
	// caller has to remember to install the token itself.
	if !client.Authenticated() {
		t.Error("client is not authenticated after a successful login")
	}
	if got := (*seen)[0].Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestLoginRejectsBadCredentialsAsUnauthorized(t *testing.T) {
	server, _ := fakeAUB(t)
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Login(context.Background(), "a@example", "wrong")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want an *APIError", err)
	}
	// PocketBase answers bad credentials with 400, so Unauthorized() must not
	// be the only signal a caller has — but the message has to survive, since
	// it is what the user sees.
	if apiErr.Message != "Failed to authenticate." {
		t.Errorf("Message = %q", apiErr.Message)
	}
	if client.Authenticated() {
		t.Error("a failed login left a token installed")
	}
}

func TestRefreshSendsTheTokenAndReplacesIt(t *testing.T) {
	server, seen := fakeAUB(t)
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.SetToken("token-1")

	session, err := client.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if session.Token != "token-2" || client.Token() != "token-2" {
		t.Errorf("token = %q / %q, want token-2", session.Token, client.Token())
	}
	if got := (*seen)[0].Header.Get("Authorization"); got != "token-1" {
		t.Errorf("Authorization = %q, want token-1", got)
	}
}

func TestRefreshWithoutATokenFails(t *testing.T) {
	server, _ := fakeAUB(t)
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Refresh(context.Background()); err == nil {
		t.Fatal("expected an error refreshing without a token")
	}
}

// A failed refresh must not log the user out: the network being down is not the
// same as the session being invalid.
func TestFailedRefreshKeepsTheExistingToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.SetToken("token-1")
	if _, err := client.Refresh(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if client.Token() != "token-1" {
		t.Errorf("token = %q, want the original to survive", client.Token())
	}
}

func TestNewRejectsUnusableBaseURLs(t *testing.T) {
	for _, bad := range []string{"", "   ", "aub.example", "ftp://aub.example"} {
		if _, err := New(bad, nil); err == nil {
			t.Errorf("New(%q) did not fail", bad)
		}
	}
	client, err := New("https://aub.example/", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The trailing slash must be normalised away, or every path becomes a
	// double slash.
	if client.BaseURL() != "https://aub.example" {
		t.Errorf("BaseURL() = %q", client.BaseURL())
	}
}

func TestAPIErrorTruncatesANonJSONBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		for i := 0; i < 1000; i++ {
			w.Write([]byte("<html>not pocketbase</html>"))
		}
	}))
	defer server.Close()

	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = client.ListRecords(context.Background(), "anything", nil, &struct{}{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want an *APIError", err)
	}
	if len(apiErr.Message) > 210 {
		t.Errorf("message is %d bytes; a wrong base URL should not paste a page into an error", len(apiErr.Message))
	}
}

// TestLoginRequiresBothCredentials keeps the client from issuing a request it
// already knows AUB will reject. Carried over from the Companion bootstrap's
// own auth tests when the two clients were merged.
func TestLoginRequiresBothCredentials(t *testing.T) {
	client, err := New("http://aub.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Login(context.Background(), "  ", "password"); err == nil {
		t.Error("an empty email was accepted")
	}
	if _, err := client.Login(context.Background(), "a@example", ""); err == nil {
		t.Error("an empty password was accepted")
	}
}

// TestLogoutClearsTheToken pins the local half of sign-out: the client stops
// presenting the token. It is not revocation — see Client.Logout.
func TestLogoutClearsTheToken(t *testing.T) {
	client, err := New("http://aub.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	client.SetToken("a-token")
	if !client.Authenticated() {
		t.Fatal("SetToken did not authenticate the client")
	}
	client.Logout()
	if client.Authenticated() || client.Token() != "" {
		t.Errorf("Logout left token %q", client.Token())
	}
}

// The capability document is where the session contract comes from, and adopting
// one is what makes a stored session carry a real expiry.
func TestAdoptingCapabilitiesReplacesTheGuessWithTheDeploymentsOwnAnswer(t *testing.T) {
	server, _ := fakeAUB(t)
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	before, err := client.Login(context.Background(), "a@example", "correct")
	if err != nil {
		t.Fatal(err)
	}
	if !before.Expires.IsZero() {
		t.Fatalf("Expires = %v before any capability read", before.Expires)
	}

	client.AdoptCapabilities(Capabilities{
		APIVersion: CompanionAPIVersion,
		Session: SessionContract{
			AuthCollection:       "users",
			TokenLifetimeSeconds: 3600,
		},
	})

	after, err := client.Login(context.Background(), "a@example", "correct")
	if err != nil {
		t.Fatal(err)
	}
	if after.Expires.IsZero() {
		t.Fatal("Expires is still zero after the deployment declared a lifetime")
	}
	remaining := time.Until(after.Expires)
	if remaining < 55*time.Minute || remaining > time.Hour {
		t.Errorf("Expires is %v away; the deployment said one hour", remaining)
	}
}

// A deployment that could not resolve a lifetime says zero, and zero must stay
// "unknown" rather than becoming "already expired".
func TestALifetimeOfZeroLeavesTheExpiryUnknown(t *testing.T) {
	contract := SessionContract{TokenLifetimeSeconds: 0}
	if got := contract.Lifetime(); got != 0 {
		t.Errorf("Lifetime() = %v, want 0", got)
	}
	negative := SessionContract{TokenLifetimeSeconds: -5}
	if got := negative.Lifetime(); got != 0 {
		t.Errorf("a negative lifetime became %v", got)
	}
}
