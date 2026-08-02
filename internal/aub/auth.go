package aub

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// AuthCollection is the Pocketbase auth collection AUC authenticates against.
//
// TODO(confirm-aub-auth-shape): "users" is the Pocketbase default and a
// placeholder. Andrea must confirm the real collection name, whether login is
// by email or username, and which record fields AUC may rely on. Everything in
// this file is written against the generic Pocketbase auth-with-password shape
// and should be treated as a stub until that confirmation lands.
const AuthCollection = "users"

// Session is one authenticated AUB session.
type Session struct {
	Token      string
	UserID     string
	Email      string
	ObtainedAt time.Time
}

// authResponse is Pocketbase's auth-with-password response envelope.
type authResponse struct {
	Token  string `json:"token"`
	Record struct {
		ID       string `json:"id"`
		Email    string `json:"email"`
		Username string `json:"username"`
		Verified bool   `json:"verified"`
	} `json:"record"`
}

// Login exchanges credentials for a session token and stores the token on the
// client, so subsequent calls on the same Client are authenticated.
//
// The identity argument is whatever the collection treats as the login
// identity — an email address under the default Pocketbase configuration.
func (client *Client) Login(ctx context.Context, identity, password string) (Session, error) {
	if identity == "" || password == "" {
		return Session{}, fmt.Errorf("both an identity and a password are required")
	}
	body := map[string]string{"identity": identity, "password": password}
	path := "/api/collections/" + AuthCollection + "/auth-with-password"

	var decoded authResponse
	if err := client.do(ctx, http.MethodPost, path, body, &decoded); err != nil {
		return Session{}, err
	}
	if decoded.Token == "" {
		return Session{}, fmt.Errorf("AUB accepted the login but returned no token")
	}

	client.Token = decoded.Token
	email := decoded.Record.Email
	if email == "" {
		email = decoded.Record.Username
	}
	return Session{
		Token:      decoded.Token,
		UserID:     decoded.Record.ID,
		Email:      email,
		ObtainedAt: time.Now().UTC(),
	}, nil
}

// Refresh exchanges the current token for a fresh one. Pocketbase's
// auth-refresh endpoint requires the existing token in the Authorization
// header and issues a new one with a new expiry — that is the whole token
// lifecycle AUC needs, since there are no separate refresh tokens.
//
// TODO(confirm-aub-auth-shape): when the session should be refreshed (on a
// timer, on 401, at startup) is a product decision that depends on AUB's
// configured token lifetime, which is not yet known. Nothing calls this yet.
func (client *Client) Refresh(ctx context.Context) (Session, error) {
	if client.Token == "" {
		return Session{}, fmt.Errorf("no session to refresh")
	}
	path := "/api/collections/" + AuthCollection + "/auth-refresh"

	var decoded authResponse
	if err := client.do(ctx, http.MethodPost, path, nil, &decoded); err != nil {
		return Session{}, err
	}
	if decoded.Token == "" {
		return Session{}, fmt.Errorf("AUB refreshed the session but returned no token")
	}

	client.Token = decoded.Token
	email := decoded.Record.Email
	if email == "" {
		email = decoded.Record.Username
	}
	return Session{
		Token:      decoded.Token,
		UserID:     decoded.Record.ID,
		Email:      email,
		ObtainedAt: time.Now().UTC(),
	}, nil
}

// Logout forgets the token locally. Pocketbase has no server-side session
// invalidation endpoint for record auth — tokens are stateless and expire on
// their own — so this is deliberately client-only, and the name should not be
// read as a promise that the token stops working elsewhere.
func (client *Client) Logout() {
	client.Token = ""
}
