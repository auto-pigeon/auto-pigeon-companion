//go:build !cgo

package nativeacceptance

// cgoEnabled records whether this artifact was built with cgo. See the other
// half of this pair for why the value is worth carrying.
const cgoEnabled = false
