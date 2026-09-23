//go:build !darwin

package pathpick

// quarantined is a macOS question; everywhere else the answer is no.
func quarantined(string) bool { return false }
