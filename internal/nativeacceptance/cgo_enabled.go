//go:build cgo

package nativeacceptance

// cgoEnabled records whether this artifact was built with cgo.
//
// `build/release.sh` sets CGO_ENABLED=0 for every target, because a
// dynamically-linked artifact is one that depends on the libc of the machine it
// was built on rather than the one it runs on. A bundle that reported `cgo:
// true` would be evidence that something other than the release script built
// the binary the operator ran, which is exactly the sort of thing an acceptance
// run exists to notice.
const cgoEnabled = true
