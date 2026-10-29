//go:build !unix && !windows

package lockfile

func processGone(int) bool { return false }
