package aub

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DefaultAuthCollection is PocketBase's own default, and is used only until a
// deployment has been asked.
//
// The authority is `GET /api/companion/v1/capabilities`, which names
// `session.auth_collection` and `session.login_path` for the deployment being
// talked to. This constant is what a client that has not read that yet — the
// very first login, before there is a token to read capabilities with — uses to
// get one, and Client.AdoptSession replaces it the moment the answer arrives.
const DefaultAuthCollection = "users"

// Session is a successful authentication against AUB.
type Session struct {
	Token   string
	UserID  string
	Email   string
	Expires time.Time
}

// authResponse is PocketBase's auth-with-password / auth-refresh response.
type authResponse struct {
	Token  string `json:"token"`
	Record struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"record"`
}

// session builds the stored session, dating it by the lifetime the DEPLOYMENT
// declared rather than by an estimate.
//
// A zero lifetime — nobody has read capabilities yet, or the server could not
// resolve one — leaves Expires ZERO, which config.Session.Valid reads as
// "unknown, treat as valid". That is the honest answer and the right behaviour:
// the server is the authority on whether a token still works, and refusing to
// send one AUB might still accept would log somebody out for no reason. An
// invented expiry would do exactly that, on a deployment whose operator had
// configured a shorter or longer one.
func (c *Client) session(r authResponse) Session {
	session := Session{Token: r.Token, UserID: r.Record.ID, Email: r.Record.Email}
	if c.tokenLifetime > 0 {
		session.Expires = time.Now().Add(c.tokenLifetime)
	}

	return session
}

// Login exchanges an email and password for a session, and installs the
// resulting token on the client so subsequent calls are authenticated.
func (c *Client) Login(ctx context.Context, email, password string) (Session, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return Session{}, fmt.Errorf("aub: email is empty")
	}
	if password == "" {
		return Session{}, fmt.Errorf("aub: password is empty")
	}

	body := struct {
		Identity string `json:"identity"`
		Password string `json:"password"`
	}{Identity: email, Password: password}

	var response authResponse
	path := "/api/collections/" + c.authCollection() + "/auth-with-password"
	if err := c.do(ctx, http.MethodPost, path, nil, body, &response); err != nil {
		return Session{}, err
	}
	if response.Token == "" {
		return Session{}, fmt.Errorf("aub: login succeeded but returned no token")
	}

	session := c.session(response)
	c.token = session.Token

	return session, nil
}

// Refresh exchanges the current token for a fresh one. It requires an already
// authenticated client; a caller with no token should call Login instead.
//
// On failure the existing token is left in place: a refresh that fails because
// the network is down must not log the user out of a session that is still
// good. Clearing the session is reserved for an APIError that reports
// Unauthorized, and is the caller's decision — see internal/cli.
func (c *Client) Refresh(ctx context.Context) (Session, error) {
	if !c.Authenticated() {
		return Session{}, fmt.Errorf("aub: cannot refresh without a token")
	}

	var response authResponse
	path := "/api/collections/" + c.authCollection() + "/auth-refresh"
	if err := c.do(ctx, http.MethodPost, path, nil, nil, &response); err != nil {
		return Session{}, err
	}
	if response.Token == "" {
		return Session{}, fmt.Errorf("aub: refresh succeeded but returned no token")
	}

	session := c.session(response)
	c.token = session.Token

	return session, nil
}

// Logout forgets the token locally.
//
// PocketBase has no server-side token revocation endpoint — its tokens are
// stateless JWTs valid until they expire — so this is exactly as strong as it
// sounds: the client stops presenting the token, and anyone who already copied
// it out of config.json still holds a working one until it expires. Named
// Logout because that is what it does from the user's side, documented here so
// nobody mistakes it for revocation.
func (c *Client) Logout() { c.token = "" }
