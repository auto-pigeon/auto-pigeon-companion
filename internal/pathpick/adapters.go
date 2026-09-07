package pathpick

import (
	"fmt"
	"strings"
)

// adapter is one native file chooser, as data.
//
// Three fields, because there are exactly three things that differ between a
// GNOME dialog and a PowerShell one: what the program is called, what argv asks
// it the question, and how it says the user pressed Cancel. Everything else —
// validating the answer, bounding the wait, refusing a second dialog — is the
// same for all of them and lives in Pick.
type adapter struct {
	binary string
	build  func(Request) ([]string, error)
	// decline reports whether this exit code and output mean the user declined.
	// It is per-adapter because the platforms genuinely disagree: the Unix
	// helpers exit non-zero, and the Windows one exits 0 having printed
	// nothing.
	decline func(exitCode int, output string) bool
}

// adaptersFor is the table, in preference order.
//
// Order is a real decision on Linux: zenity first because GTK's chooser is the
// one present on the most desktops and the one whose exit codes are documented,
// kdialog second so a Plasma machine that has no zenity still gets its own
// dialog rather than a text field.
func adaptersFor(goos string) []adapter {
	switch goos {
	case "darwin":
		return []adapter{osascriptAdapter()}
	case "windows":
		return []adapter{powershellAdapter()}
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly", "solaris", "illumos":
		return []adapter{zenityAdapter(), kdialogAdapter()}
	}
	return nil
}

// --- zenity -----------------------------------------------------------------

func zenityAdapter() adapter {
	return adapter{
		binary: "zenity",
		build: func(request Request) ([]string, error) {
			args := []string{"--file-selection", "--title=" + request.Title}
			switch request.Kind {
			case Directory:
				args = append(args, "--directory")
			case SaveFile:
				args = append(args, "--save", "--confirm-overwrite")
			}
			if request.StartDir != "" {
				// The trailing separator is what tells GTK this is the
				// directory to open in rather than a file to preselect.
				args = append(args, "--filename="+strings.TrimSuffix(request.StartDir, "/")+"/")
			}
			for _, filter := range request.Filters {
				args = append(args, "--file-filter="+filter.Name+" | "+globs(filter, " "))
			}
			return args, nil
		},
		// 1 is Cancel and 5 is zenity's own timeout, which from here is the
		// same outcome: no path was chosen and nobody needs telling why.
		decline: func(code int, _ string) bool { return code == 1 || code == 5 },
	}
}

// --- kdialog ----------------------------------------------------------------

func kdialogAdapter() adapter {
	return adapter{
		binary: "kdialog",
		build: func(request Request) ([]string, error) {
			// kdialog takes the starting location as a positional argument
			// and has no way to say "wherever you like". Pick has normally
			// filled this in with the user's home directory already; ":" is
			// kdialog's own spelling of "remember where you were last", which
			// is the best remaining answer on a machine whose home directory
			// could not be resolved.
			start := request.StartDir
			if start == "" {
				start = ":aucom"
			}
			args := []string{"--title", request.Title}
			switch request.Kind {
			case Directory:
				args = append(args, "--getexistingdirectory", start)
			case OpenFile:
				args = append(args, "--getopenfilename", start)
			case SaveFile:
				args = append(args, "--getsavefilename", start)
			}
			if len(request.Filters) > 0 && request.Kind != Directory {
				args = append(args, kdialogFilters(request.Filters))
			}
			return args, nil
		},
		decline: func(code int, _ string) bool { return code == 1 },
	}
}

// kdialogFilters is Qt's filter syntax: globs, then the label, per filter,
// joined by newlines.
func kdialogFilters(filters []Filter) string {
	parts := make([]string, 0, len(filters))
	for _, filter := range filters {
		parts = append(parts, globs(filter, " ")+"|"+filter.Name)
	}
	return strings.Join(parts, "\n")
}

// --- osascript --------------------------------------------------------------

func osascriptAdapter() adapter {
	return adapter{
		binary: "osascript",
		build: func(request Request) ([]string, error) {
			var script strings.Builder
			switch request.Kind {
			case Directory:
				script.WriteString("POSIX path of (choose folder with prompt " +
					appleString(request.Title))
			case OpenFile:
				script.WriteString("POSIX path of (choose file with prompt " +
					appleString(request.Title))
			case SaveFile:
				script.WriteString("POSIX path of (choose file name with prompt " +
					appleString(request.Title))
			}
			if request.StartDir != "" {
				script.WriteString(" default location POSIX file " + appleString(request.StartDir))
			}
			if request.Kind == OpenFile && len(request.Filters) > 0 {
				// AppleScript filters by extension, with no display name for
				// each set, so every extension the caller offered becomes one
				// list.
				extensions := make([]string, 0)
				for _, filter := range request.Filters {
					for _, extension := range filter.Extensions {
						extensions = append(extensions, appleString(extension))
					}
				}
				if len(extensions) > 0 {
					script.WriteString(" of type {" + strings.Join(extensions, ", ") + "}")
				}
			}
			script.WriteString(")")
			return []string{"-e", script.String()}, nil
		},
		// Cancelling any of the `choose` commands raises AppleScript error
		// -128, which osascript reports as exit status 1.
		decline: func(code int, _ string) bool { return code == 1 },
	}
}

// appleString quotes a value for AppleScript source.
//
// The script is one argv element, so no shell ever sees it and shell quoting is
// not the question; AppleScript's own string syntax is. Backslash first, then
// the quote, because escaping in the other order would escape the escapes.
func appleString(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

// --- powershell -------------------------------------------------------------

func powershellAdapter() adapter {
	return adapter{
		binary: "powershell",
		build: func(request Request) ([]string, error) {
			script, err := windowsScript(request)
			if err != nil {
				return nil, err
			}
			// -STA because the WinForms dialogs require a single-threaded
			// apartment and silently do nothing without it. -NoProfile so a
			// user's profile script cannot change what this prints, which is
			// the only output this package parses.
			return []string{"-NoProfile", "-STA", "-Command", script}, nil
		},
		// The WinForms dialogs return DialogResult.Cancel, which the script
		// turns into "print nothing and exit 0". That is why this adapter is
		// the one that has to look at the output as well as at the code.
		decline: func(code int, output string) bool { return code == 0 && output == "" },
	}
}

func windowsScript(request Request) (string, error) {
	var body strings.Builder
	body.WriteString("Add-Type -AssemblyName System.Windows.Forms; ")
	switch request.Kind {
	case Directory:
		body.WriteString("$dialog = New-Object System.Windows.Forms.FolderBrowserDialog; ")
		body.WriteString("$dialog.Description = " + powerShellString(request.Title) + "; ")
		if request.StartDir != "" {
			body.WriteString("$dialog.SelectedPath = " + powerShellString(request.StartDir) + "; ")
		}
		body.WriteString("if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) " +
			"{ [Console]::Out.Write($dialog.SelectedPath) }")
	case OpenFile, SaveFile:
		control := "OpenFileDialog"
		if request.Kind == SaveFile {
			control = "SaveFileDialog"
		}
		body.WriteString("$dialog = New-Object System.Windows.Forms." + control + "; ")
		body.WriteString("$dialog.Title = " + powerShellString(request.Title) + "; ")
		if request.StartDir != "" {
			body.WriteString("$dialog.InitialDirectory = " + powerShellString(request.StartDir) + "; ")
		}
		if filter := windowsFilter(request.Filters); filter != "" {
			body.WriteString("$dialog.Filter = " + powerShellString(filter) + "; ")
		}
		body.WriteString("if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) " +
			"{ [Console]::Out.Write($dialog.FileName) }")
	default:
		return "", fmt.Errorf("pathpick: no Windows dialog for %q", request.Kind)
	}
	return body.String(), nil
}

// windowsFilter is the Win32 filter string: label, NUL-free pipe separators,
// pairs of label and pattern.
func windowsFilter(filters []Filter) string {
	parts := make([]string, 0, len(filters))
	for _, filter := range filters {
		parts = append(parts, filter.Name+"|"+globs(filter, ";"))
	}
	return strings.Join(parts, "|")
}

// powerShellString quotes a value as a PowerShell single-quoted string, in
// which the only metacharacter is the quote itself and it is escaped by
// doubling. Single quotes, not double: inside double quotes PowerShell expands
// `$name` and subexpressions, and a directory called `$RECYCLE.BIN` is a real
// path a user can pick.
func powerShellString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// globs renders one filter's extensions as `*.ext` patterns.
func globs(filter Filter, separator string) string {
	patterns := make([]string, 0, len(filter.Extensions))
	for _, extension := range filter.Extensions {
		patterns = append(patterns, "*."+strings.TrimPrefix(extension, "."))
	}
	if len(patterns) == 0 {
		patterns = append(patterns, "*")
	}
	return strings.Join(patterns, separator)
}
