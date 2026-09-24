//go:build windows

package job

import (
	"syscall"
	"testing"
)

// On Windows the display agrees with the standard library's own escaping.
func TestWindowsQuoteAgreesWithEscapeArg(t *testing.T) {
	for _, in := range []string{"", "plain", `C:\Users\me\quake.exe`, `C:\Program Files\Quake\q.exe`,
		`C:\Temp\dir with space\`, `say "hi"`, `a\"b`, "tab\there"} {
		if got, want := windowsQuote(in), syscall.EscapeArg(in); got != want {
			t.Errorf("windowsQuote(%q) = %s, syscall.EscapeArg = %s", in, got, want)
		}
	}
}
