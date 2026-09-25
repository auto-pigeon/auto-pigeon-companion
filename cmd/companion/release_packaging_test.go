package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Packaging metadata is not compiled, so nothing else catches it drifting.
// These read the files as text, the way packaging_test.go does, and assert the
// facts that have to agree between the packages, the program and the threat
// model.

// The autopigeon:// handler has to be declared the same way by every package,
// and the shape of it is the security property: `game open`, no approval flag,
// the URL as its own quoted argument, no shell.
func TestEveryPackageRegistersTheHandlerTheSameWay(t *testing.T) {
	desktop := repoFile(t, "build", "linux", "auto-pigeon-companion-join.desktop")
	mustContain(t, "the join .desktop entry", desktop,
		"MimeType=x-scheme-handler/autopigeon;",
		"Exec="+executableName+" game open %u",
		// The launcher entry is the visible one. A second Auto-Pigeon
		// Companion in the applications menu would be a bug.
		"NoDisplay=true",
		// `game open` hands the link to the Companion's page, where the review
		// is read; opening a terminal would show nothing.
		"Terminal=false",
	)
	// In the Exec line, not in the comment above it explaining why it is absent.
	if strings.Contains(directiveLines(desktop, "#"), "--approve") {
		t.Error("the .desktop handler approves what a link would run")
	}

	// The launcher entry must NOT claim the scheme: it runs the program with no
	// arguments, which is GUI mode, and a desktop passing it a URL would have
	// the URL silently dropped.
	launcher := repoFile(t, "build", "linux", "auto-pigeon-companion.desktop")
	if strings.Contains(launcher, "x-scheme-handler") {
		t.Error("the launcher entry claims the scheme; a URL passed to GUI mode is a URL dropped")
	}

	nfpm := repoFile(t, "build", "linux", "nfpm.yaml")
	mustContain(t, "nfpm.yaml", nfpm,
		"auto-pigeon-companion-join.desktop",
		"dst: /usr/share/applications/auto-pigeon-companion-join.desktop",
	)

	iss := repoFile(t, "build", "windows", "installer.iss")
	mustContain(t, "installer.iss", iss,
		// HKCU, so no administrator rights and nothing machine-wide.
		`Root: HKCU; Subkey: "Software\Classes\autopigeon"`,
		`ValueName: "URL Protocol"`,
		// The program path is quoted ALWAYS: an unquoted path under
		// "C:\Program Files" invites the loader to try "C:\Program.exe".
		`""{app}\{#AppExeName}"" game open ""%1"""`,
	)
	directives := directiveLines(iss, ";")
	if strings.Contains(directives, "HKLM") || strings.Contains(directives, "HKEY_LOCAL_MACHINE") {
		t.Error("installer.iss writes a machine-wide key for a per-user install")
	}

	plist := repoFile(t, "build", "macos", "Info.plist.tmpl")
	mustContain(t, "Info.plist.tmpl", plist,
		"<key>CFBundleURLTypes</key>",
		"<key>CFBundleURLSchemes</key>",
		"<string>autopigeon</string>",
		// Viewer, not Editor: this application does not own what the scheme names.
		"<string>Viewer</string>",
	)
}

// A handler pointing at a program that is no longer installed is a link that
// does nothing, silently. Each package has to take its own declaration away.
func TestUninstallingRemovesTheHandlerOnEveryPackagedPlatform(t *testing.T) {
	iss := repoFile(t, "build", "windows", "installer.iss")
	// uninsdeletekey on the root key takes the subkeys with it.
	if !strings.Contains(iss, "Flags: uninsdeletekey") {
		t.Error("installer.iss does not remove its registry key on uninstall")
	}

	// On Linux the declaration is a file the package owns, so removing the
	// package removes it. What must NOT be there is a package script writing
	// the association into the user's own mimeapps.list, which no uninstall
	// could then undo for every user on the machine.
	nfpm := repoFile(t, "build", "linux", "nfpm.yaml")
	if strings.Contains(nfpm, "xdg-mime default") {
		t.Error("a package script sets a per-user default it cannot remove")
	}
	postremove := repoFile(t, "build", "linux", "postremove.sh")
	mustContain(t, "postremove.sh", postremove, "update-desktop-database")

	// On macOS the handler is the bundle's, so removing the bundle removes it.
	// This is the claim the code makes; it has to be the one the packaging does.
	script := repoFile(t, "build", "macos", "make-app-bundle.sh")
	mustContain(t, "make-app-bundle.sh", script, "Contents/Info.plist")
}

// A package script runs as root and cannot know which of the machine's users
// wanted their build history deleted. So it deletes nobody's, and the per-user
// command is what removes user data.
func TestNoPackageScriptTouchesUserData(t *testing.T) {
	for _, file := range [][]string{
		{"build", "linux", "postinstall.sh"},
		{"build", "linux", "postremove.sh"},
		{"build", "linux", "nfpm.yaml"},
		{"build", "windows", "installer.iss"},
	} {
		name := filepath.Join(file...)
		body := repoFile(t, file...)
		for _, forbidden := range []string{
			"rm -rf /home", "rm -rf ~", "rm -rf $HOME", "rm -rf \"$HOME",
			".config/auto-pigeon-companion", ".cache/auto-pigeon-companion",
			"{userappdata}", "{localappdata}",
		} {
			// A COMMENT may name these directories — the nfpm config explains
			// at length why it leaves them alone — so only non-comment lines
			// count.
			for _, line := range strings.Split(body, "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
					continue
				}
				if strings.Contains(line, forbidden) {
					t.Errorf("%s touches user data: %s", name, trimmed)
				}
			}
		}
	}

	// The Windows uninstaller must not delete the cache directory either: it
	// holds third-party binaries under their own licences.
	iss := repoFile(t, "build", "windows", "installer.iss")
	if strings.Contains(iss, "[UninstallDelete]") {
		section := iss[strings.Index(iss, "[UninstallDelete]"):]
		for _, line := range strings.Split(section, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Type:") {
				t.Errorf("the uninstaller deletes something it did not install: %s", trimmed)
			}
		}
	}

	// And the way a person removes their own data has to exist and be named
	// where the packaging tells them about it.
	mustContain(t, "nfpm.yaml", repoFile(t, "build", "linux", "nfpm.yaml"),
		"companion uninstall --purge")
}

// Being unsigned is the current truth. What must never happen is packaging that
// implies otherwise, or a signing key reaching the workflow every pull request
// runs.
func TestThePackagingIsTruthfulAboutNotBeingSigned(t *testing.T) {
	for _, file := range [][]string{
		{"build", "macos", "make-app-bundle.sh"},
		{"build", "windows", "installer.iss"},
		{"build", "release.sh"},
	} {
		name := filepath.Join(file...)
		body := strings.ToLower(repoFile(t, file...))
		if !strings.Contains(body, "unsigned") && !strings.Contains(body, "not signed") &&
			!strings.Contains(body, "signs nothing") {
			t.Errorf("%s does not say the artifact it produces is unsigned", name)
		}
	}

	// The procedures are written down, so the day a certificate exists is a day
	// of running them rather than of inventing them.
	bundle := repoFile(t, "build", "macos", "make-app-bundle.sh")
	mustContain(t, "make-app-bundle.sh", bundle, "codesign", "notarytool", "stapler")

	// And the verification workflow still reads no secret.
	workflow := repoFile(t, ".github", "workflows", "build.yml")
	mustContain(t, "the build workflow", workflow, "no-credentials")
}

// The release script is the whole procedure, so what it produces is what a
// downloader gets. Its contract is asserted here; AUT's security lane runs it.
func TestTheReleaseScriptProducesChecksumsAndAnSBOM(t *testing.T) {
	script := repoFile(t, "build", "release.sh")
	mustContain(t, "release.sh", script,
		"release sbom",
		"release checksums",
		// Reproducible: same commit, same bytes.
		"-trimpath",
		"CGO_ENABLED=0",
	)
	// An archive carries what runs the Companion and nothing else (operator
	// decision, 2026-09-25): no licence or notice files, no acceptance kit.
	for _, shipped := range []string{"cp LICENSE", "THIRD_PARTY_NOTICES.md \"", "cp acceptance/",
		"kit-options.json \"", "run-acceptance.sh \""} {
		if strings.Contains(script, shipped) {
			t.Errorf("release.sh copies %q into an artifact again", shipped)
		}
	}
	for _, target := range []string{
		"windows/amd64", "windows/arm64", "linux/amd64", "linux/arm64",
		"darwin/amd64", "darwin/arm64",
	} {
		if !strings.Contains(script, target) {
			t.Errorf("release.sh does not build %s", target)
		}
	}
	// The staging tree is removed before the checksums are taken, or SHA256SUMS
	// would name files nobody downloads.
	if !strings.Contains(script, `rm -rf "$OUT/stage"`) {
		t.Error("release.sh digests its staging tree")
	}
	// Executable as committed — the mode every checkout gets — and, where the
	// filesystem has an executable bit at all, in this checkout too.
	staged, err := exec.Command("git", "-C", filepath.Join("..", ".."), "ls-files", "-s", "build/release.sh").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	if !strings.HasPrefix(string(staged), "100755 ") {
		t.Errorf("build/release.sh is not committed executable: %q", staged)
	}
	if info, err := os.Stat(filepath.Join("..", "..", "build", "release.sh")); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm()&0o111 == 0 && runtime.GOOS != "windows" {
		t.Error("build/release.sh is not executable")
	}
}

// directiveLines drops comment lines, so a check reads what a file DOES rather
// than what it explains. Every one of these files says at length why it is not
// doing the dangerous thing, and a substring search would find that.
func directiveLines(body, commentPrefix string) string {
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, commentPrefix) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
