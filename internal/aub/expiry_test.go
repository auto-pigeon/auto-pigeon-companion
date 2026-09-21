package aub

import (
	"encoding/base64"
	"testing"
	"time"
)

func jwtWith(payload string) string {
	enc := base64.RawURLEncoding.EncodeToString

	return enc([]byte(`{"alg":"HS256"}`)) + "." + enc([]byte(payload)) + ".sig"
}

// 246I1.1: the page said "signed in" on a token that had expired the day
// before. The claim is read; the signature is AUB's to check.
func TestAnExpiredTokenIsReportedExpired(t *testing.T) {
	now := time.Unix(1790000000, 0)
	if !TokenExpired(jwtWith(`{"exp":1789920106}`), now) {
		t.Error("a token past its exp is not reported expired")
	}
	if TokenExpired(jwtWith(`{"exp":1790500000}`), now) {
		t.Error("a token before its exp is reported expired")
	}
}

func TestATokenWithoutAnExpiryIsLeftToTheServer(t *testing.T) {
	now := time.Now()
	for _, token := range []string{"", "fixture-token", jwtWith(`{"id":"u1"}`), "a.!!!.c"} {
		if TokenExpired(token, now) {
			t.Errorf("%q is reported expired", token)
		}
	}
}
