package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Packaging metadata is not compiled, so nothing else would catch it drifting:
// a .desktop file still saying Exec=auto-pigeon-launcher, an nfpm config
// installing a binary name the build no longer produces, or an installer
// pointing at a LICENSE that says something different from the repository's.
// These tests read the files as text and assert the handful of facts that must
// agree with each other and with the binary this package builds.
//
// They live beside cmd/companion because that is where the executable's name is
// decided, which is the fact most of them are about.

// repoFile reads a file relative to the repository root.
func repoFile(t *testing.T, elem ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, elem...)...)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}

// executableName is the binary name every packaging file must agree on. It is
// the last path element of this package's directory.
const executableName = "companion"

// productName is the human-readable name shown to users.
const productName = "Auto-Pigeon Companion"

func mustContain(t *testing.T, name, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("%s does not contain %q", name, want)
		}
	}
}

func TestLinuxPackageMetadata(t *testing.T) {
	nfpm := repoFile(t, "build", "linux", "nfpm.yaml")
	mustContain(t, "nfpm.yaml", nfpm,
		"name: auto-pigeon-companion",
		"license: MIT",
		"dst: /usr/bin/"+executableName,
		"dst: /usr/share/doc/auto-pigeon-companion/LICENSE",
		"dst: /usr/share/doc/auto-pigeon-companion/THIRD_PARTY_NOTICES.md",
		"auto-pigeon-companion.desktop",
	)

	desktop := repoFile(t, "build", "linux", "auto-pigeon-companion.desktop")
	mustContain(t, "the .desktop file", desktop,
		"Name="+productName,
		"Exec="+executableName,
		"Terminal=false",
	)
}

func TestWindowsInstallerMetadata(t *testing.T) {
	iss := repoFile(t, "build", "windows", "installer.iss")
	mustContain(t, "installer.iss", iss,
		`#define AppName "`+productName+`"`,
		`#define AppExeName "`+executableName+`.exe"`,
		`LicenseFile=..\..\LICENSE`,
		`Source: "..\..\THIRD_PARTY_NOTICES.md"`,
	)
	// A stable AppId is what makes an upgrade replace the previous install
	// rather than sit beside it, so it must be a literal, never derived.
	if !strings.Contains(iss, "AppId={{9F1C0F5B-6C4A-4C1E-9E77-4B4A2C6D51A2}") {
		t.Error("installer.iss lost its fixed AppId")
	}
}

func TestMacOSBundleMetadata(t *testing.T) {
	plist := repoFile(t, "build", "macos", "Info.plist.tmpl")
	mustContain(t, "Info.plist.tmpl", plist,
		"<string>"+productName+"</string>",
		"<key>CFBundleIdentifier</key>",
		"@BUNDLE_ID@", "@VERSION@", "@EXECUTABLE@", "@COPYRIGHT@",
		"<key>LSUIElement</key>",
	)

	script := repoFile(t, "build", "macos", "make-app-bundle.sh")
	mustContain(t, "make-app-bundle.sh", script,
		`BUNDLE_ID="io.github.andrea-dintino.auto-pigeon-companion"`,
		`EXECUTABLE="`+executableName+`"`,
		`APP_NAME="`+productName+`"`,
	)
	// Every placeholder the template declares must be substituted, or the
	// shipped Info.plist carries a literal @TOKEN@.
	for _, token := range []string{"@BUNDLE_ID@", "@VERSION@", "@ARCH@", "@EXECUTABLE@", "@COPYRIGHT@"} {
		if !strings.Contains(script, "s|"+token+"|") {
			t.Errorf("make-app-bundle.sh does not substitute %s", token)
		}
	}
	// The licence files ship inside the bundle.
	mustContain(t, "make-app-bundle.sh", script,
		"Contents/Resources/LICENSE",
		"Contents/Resources/THIRD_PARTY_NOTICES.md",
	)
}

// TestCIBuildsEverySupportedTarget pins the six OS/architecture targets that
// have to keep working, and that CI builds the entry point this package is.
func TestCIBuildsEverySupportedTarget(t *testing.T) {
	workflow := repoFile(t, ".github", "workflows", "build.yml")
	for _, target := range []string{
		"{ goos: windows, goarch: amd64",
		"{ goos: windows, goarch: arm64",
		"{ goos: linux, goarch: amd64",
		"{ goos: linux, goarch: arm64",
		"{ goos: darwin, goarch: amd64",
		"{ goos: darwin, goarch: arm64",
	} {
		if !strings.Contains(workflow, target) {
			t.Errorf("the build workflow is missing %s }", target)
		}
	}
	mustContain(t, "the build workflow", workflow,
		"./cmd/"+executableName,
		`CGO_ENABLED: "0"`,
		"go test ./...",
	)
}

// TestPackagingMentionsNoRetiredLauncher is the rename's backstop: the Launcher
// is retired, and no file a user installs may still name it.
func TestPackagingMentionsNoRetiredLauncher(t *testing.T) {
	for _, file := range [][]string{
		{"build", "linux", "nfpm.yaml"},
		{"build", "linux", "auto-pigeon-companion.desktop"},
		{"build", "windows", "installer.iss"},
		{"build", "macos", "Info.plist.tmpl"},
		{"build", "macos", "make-app-bundle.sh"},
		{".github", "workflows", "build.yml"},
	} {
		body := repoFile(t, file...)
		if strings.Contains(body, "auto-pigeon-launcher") {
			t.Errorf("%s still names auto-pigeon-launcher", filepath.Join(file...))
		}
	}
}

// canonicalMIT is the SPDX `MIT` licence text, unmodified, with this
// repository's copyright line (NEW_247G). The comparison is whole-file: a
// reworded clause is a different licence, whatever the heading says.
const canonicalMIT = `MIT License

Copyright (c) 2026 Andrea D'Intino

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
`

// TestLicenseIsMIT keeps the licence, the package metadata and the notices
// agreeing with each other. The contradiction this replaces — a verbatim
// AGPL-3.0 LICENSE beside `license: Proprietary` in the package config — is
// exactly what an unread file drifts into.
func TestLicenseIsMIT(t *testing.T) {
	license := repoFile(t, "LICENSE")
	if license != canonicalMIT {
		t.Errorf("LICENSE is not the canonical MIT text with the line %q", "Copyright (c) 2026 Andrea D'Intino")
	}
	notices := repoFile(t, "THIRD_PARTY_NOTICES.md")
	mustContain(t, "THIRD_PARTY_NOTICES.md", notices, "MIT", "GPL-2.0", "Apache-2.0")
}

// An archive that carries the extractor is not an MIT archive (NEW_247G). No
// file a user reads about a release or an installed package may say it is, and
// the one line the macOS bundle shows as its copyright must not call the whole
// .app MIT, because a release puts the proprietary extractor inside it.
func TestNothingCallsAnArchiveWithTheExtractorMIT(t *testing.T) {
	for _, file := range [][]string{
		{"README.md"},
		{"THIRD_PARTY_NOTICES.md"},
		{"build", "release-plan.py"},
		{"build", "bundle-manifest.py"},
		{"build", "bundle-sidecar.sh"},
		{"build", "release.sh"},
		{"build", "linux", "nfpm.yaml"},
		{"build", "windows", "installer.iss"},
		{"build", "macos", "make-app-bundle.sh"},
	} {
		body := strings.ToLower(strings.Join(strings.Fields(repoFile(t, file...)), " "))
		for _, phrase := range []string{
			"mit-licensed archive", "mit-licensed bundle", "mit-licensed release",
			"mit licensed archive", "mit licensed bundle", "mit licensed release",
			"one mit-licensed desktop application", "entirely mit",
		} {
			if strings.Contains(body, phrase) && !strings.Contains(body, "never \""+phrase) &&
				!strings.Contains(body, "read as "+phrase) {
				t.Errorf("%s says %q", filepath.Join(file...), phrase)
			}
		}
	}
	script := repoFile(t, "build", "macos", "make-app-bundle.sh")
	if strings.Contains(script, "Andrea D'Intino. MIT licensed;") {
		t.Error("the macOS bundle's copyright line calls the whole .app MIT, and a release puts the extractor in it")
	}
	mustContain(t, "make-app-bundle.sh", script, "Auto-Pigeon Extractor is proprietary")
}

// TestNoCompiledInAUBPort is the workspace port contract in test form: 8090 is
// PocketBase's framework default and never Auto-Pigeon's, and no component may
// compile in where another one lives. Test fixtures may name a port; production
// code may not.
func TestNoCompiledInAUBPort(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		name := info.Name()
		if info.IsDir() {
			if name == ".git" || name == "graft" || name == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue // Comments explain the rule; they do not break it.
			}
			if strings.Contains(line, ":8090") {
				t.Errorf("%s compiles in port 8090: %s", path, trimmed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestNoticesCoverEveryRedistributedComponent guards the claims in
// THIRD_PARTY_NOTICES.md that a code change could quietly falsify.
//
// The dangerous one is AUE: a release bundle ships it beside this executable,
// and it is proprietary (NEW_247G) — not MIT, whatever the archive's other
// licence files say, and not AGPL any more. The notices are what a person who
// unpacks an archive reads, so a notices file that listed it under the wrong
// licence, or stopped saying that using it needs the owner's authorization,
// would be the whole failure.
func TestNoticesCoverEveryRedistributedComponent(t *testing.T) {
	notices := repoFile(t, "THIRD_PARTY_NOTICES.md")
	mustContain(t, "THIRD_PARTY_NOTICES.md", notices,
		"auto-pigeon-extractor",
		"LicenseRef-Auto-Pigeon-Proprietary",
		"Auto-Pigeon Proprietary Software License",
		"LICENSE-auto-pigeon-extractor.txt",
		"written authorization",
		"auto-pigeon-libraries",
		"Apache-2.0",
		"GPL-2.0",
		"MIT",
	)
	// The summary row for the extractor names the proprietary licence and
	// neither of the ones it must never be listed under.
	row := ""
	for _, line := range strings.Split(notices, "\n") {
		if strings.HasPrefix(line, "| auto-pigeon-extractor") {
			row = line
		}
	}
	if row == "" {
		t.Fatal("THIRD_PARTY_NOTICES.md has no summary row for auto-pigeon-extractor")
	}
	licence := strings.Split(row, "|")[2]
	if !strings.Contains(licence, "LicenseRef-Auto-Pigeon-Proprietary") ||
		strings.Contains(licence, "MIT") || strings.Contains(licence, "GPL") {
		t.Errorf("the extractor's summary row lists its licence as %q", strings.TrimSpace(licence))
	}

	// The claim "no release embeds AUE yet" rests on this: the staging
	// directory holds nothing but .gitkeep in a clean checkout, and .gitignore
	// is what keeps a staged binary from ever being committed.
	ignore := repoFile(t, ".gitignore")
	mustContain(t, ".gitignore", ignore,
		"/internal/aue/embedded/*",
		"!/internal/aue/embedded/.gitkeep",
	)
}
