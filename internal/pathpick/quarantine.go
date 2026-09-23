package pathpick

// QuarantineNote is what the page says about a program macOS has quarantined,
// or "" when there is nothing to say (always "" off macOS).
//
// A program downloaded with a browser carries com.apple.quarantine, and an
// unsigned one — every Quake engine and compiler build — is then refused by
// Gatekeeper with a dialog the Companion never sees: the job just fails. Said
// here, where the program is chosen, with the two ways a person clears it.
func QuarantineNote(path string) string {
	if !quarantined(path) {
		return ""
	}
	return "macOS has marked this program as downloaded from the internet, and will refuse to start it " +
		"if it is not signed. Open it once from Finder with right-click › Open, or run: " +
		"xattr -d com.apple.quarantine \"" + path + "\""
}
