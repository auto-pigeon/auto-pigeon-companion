package aub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// These tests stand up a fake Pocketbase rather than asserting against a real
// AUB instance: the endpoint shape is still unconfirmed (see the TODO in
// auth.go), so what is pinned down here is AUC's half of the exchange — the
// path it calls, the body it sends, the header it sets afterwards.

func TestLoginStoresTheTokenOnTheClient(t *testing.T) {
	var gotPath string
	var gotBody map[string]string

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotPath = request.URL.Path
		_ = json.NewDecoder(request.Body).Decode(&gotBody)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"token":"tok","record":{"id":"u1","email":"a@b.c"}}`))
	}))
	defer server.Close()

	client := New(server.URL)
	session, err := client.Login(context.Background(), "a@b.c", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if want := "/api/collections/" + AuthCollection + "/auth-with-password"; gotPath != want {
		t.Fatalf("path = %q, want %q", gotPath, want)
	}
	if gotBody["identity"] != "a@b.c" || gotBody["password"] != "secret" {
		t.Fatalf("body = %v, want identity/password fields", gotBody)
	}
	if session.Token != "tok" || session.UserID != "u1" || session.Email != "a@b.c" {
		t.Fatalf("session = %+v", session)
	}
	if client.Token != "tok" {
		t.Fatalf("client.Token = %q, want the new token", client.Token)
	}
}

func TestLoginSurfacesTheServerStatusAndMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"code":400,"message":"Failed to authenticate."}`))
	}))
	defer server.Close()

	client := New(server.URL)
	_, err := client.Login(context.Background(), "a@b.c", "wrong")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusBadRequest || apiErr.Message != "Failed to authenticate." {
		t.Fatalf("apiErr = %+v", apiErr)
	}
	if client.Token != "" {
		t.Fatalf("client.Token = %q, want empty after a failed login", client.Token)
	}
}

func TestLoginRequiresBothCredentials(t *testing.T) {
	// Checked locally so an empty form does not become a network round trip.
	client := New("http://127.0.0.1:1")
	if _, err := client.Login(context.Background(), "", "secret"); err == nil {
		t.Fatal("Login with no identity returned no error")
	}
	if _, err := client.Login(context.Background(), "a@b.c", ""); err == nil {
		t.Fatal("Login with no password returned no error")
	}
}

func TestAuthenticatedRequestsCarryTheToken(t *testing.T) {
	var gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotAuthorization = request.Header.Get("Authorization")
		_, _ = writer.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := New(server.URL)
	client.Token = "tok"
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if gotAuthorization != "tok" {
		t.Fatalf("Authorization = %q, want %q", gotAuthorization, "tok")
	}
}

func TestLogoutClearsTheToken(t *testing.T) {
	client := New("http://127.0.0.1:1")
	client.Token = "tok"
	client.Logout()
	if client.Token != "" {
		t.Fatalf("client.Token = %q, want empty", client.Token)
	}
}

func TestRefreshWithoutASessionIsAnError(t *testing.T) {
	client := New("http://127.0.0.1:1")
	if _, err := client.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh with no token returned no error")
	}
}
