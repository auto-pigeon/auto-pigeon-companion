package job

import "testing"

// The Windows display quoting reads back as the argument, by the rules a
// Windows program parses its command line with (CommandLineToArgvW).
func TestWindowsQuoteReadsBackAsTheArgument(t *testing.T) {
	cases := map[string]string{
		"":                             `""`,
		"plain":                        `plain`,
		`C:\Users\me\quake.exe`:        `C:\Users\me\quake.exe`,
		`C:\Program Files\Quake\q.exe`: `"C:\Program Files\Quake\q.exe"`,
		`C:\Temp\dir with space\`:      `"C:\Temp\dir with space\\"`,
		`say "hi"`:                     `"say \"hi\""`,
		`a\"b`:                         `a\\\"b`,
		"tab\there":                    "\"tab\there\"",
	}
	for in, want := range cases {
		if got := windowsQuote(in); got != want {
			t.Errorf("windowsQuote(%q) = %s, want %s", in, got, want)
		}
	}
}
