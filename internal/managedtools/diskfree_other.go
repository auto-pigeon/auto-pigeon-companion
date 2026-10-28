//go:build !(linux || darwin || freebsd || netbsd || openbsd || windows)

package managedtools

// freeBytes is unknown here; the download's own size bound still applies.
func freeBytes(string) int64 { return -1 }
