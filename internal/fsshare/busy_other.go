//go:build !windows

package fsshare

func isBusy(error) bool { return false }
