package pathpick

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A macOS application bundle, chosen where a program is asked for.
//
// Quake engines on macOS ship as bundles — vkQuake.app, QuakeSpasm.app,
// DarkPlaces.app — and a bundle is a folder, so "choose the engine program"
// refused the very thing a Mac user would choose. The program is the file the
// bundle's Info.plist names as CFBundleExecutable, under Contents/MacOS.
//
// Checked by name and layout rather than by the platform the Companion runs
// on, so it is the same rule everywhere and testable anywhere.

var bundleExecutableKey = regexp.MustCompile(`<key>\s*CFBundleExecutable\s*</key>\s*<string>\s*([^<]+?)\s*</string>`)

// BundleExecutable resolves a .app folder to its program, or reports false.
// An XML Info.plist is read for CFBundleExecutable; a binary one, which this
// program does not parse, falls back to the only regular file in
// Contents/MacOS, and to nothing when there are several to choose from.
func BundleExecutable(bundle string) (string, bool) {
	if !strings.HasSuffix(strings.ToLower(filepath.Base(bundle)), ".app") {
		return "", false
	}
	macos := filepath.Join(bundle, "Contents", "MacOS")
	if raw, err := os.ReadFile(filepath.Join(bundle, "Contents", "Info.plist")); err == nil {
		if match := bundleExecutableKey.FindSubmatch(raw); match != nil {
			name := string(match[1])
			if name == filepath.Base(name) && name != "." && name != ".." {
				candidate := filepath.Join(macos, name)
				if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
					return candidate, true
				}
			}
		}
	}
	entries, err := os.ReadDir(macos)
	if err != nil {
		return "", false
	}
	var only string
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		if only != "" {
			return "", false
		}
		only = filepath.Join(macos, entry.Name())
	}
	return only, only != ""
}
