package hostgame

import "time"

// NewForTest is New with a beat loop that does not wait.
//
// It lives in an `_test.go` file so the seam exists for the external test package
// and nowhere else: production beats at the cadence AUB published, and a
// constructor that could take another would be an invitation to a client that
// beats slower than the server's tolerance — which is indistinguishable from a
// departed host.
func NewForTest(backend Backend, jobs Jobs, interval time.Duration) *Advertiser {
	advertiser := New(backend, jobs)
	advertiser.after = func(time.Duration) <-chan time.Time { return time.After(interval) }

	return advertiser
}
