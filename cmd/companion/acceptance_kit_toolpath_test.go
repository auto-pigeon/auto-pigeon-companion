package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var ericwPrograms = []string{"qbsp", "vis", "light", "bspinfo", "bsputil"}

func writePrograms(t *testing.T, dir string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// kit runs the shipped entry point of this platform, as an operator does: the
// PowerShell half on Windows, the POSIX half elsewhere.
func kit(t *testing.T, companion, toolPath, out string, environment []string) (*exec.Cmd, []byte) {
	t.Helper()
	script := filepath.Join(repoRoot(t), "acceptance", "run-acceptance")
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		command = exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script+".ps1",
			"-Companion", companion, "-ToolPath", toolPath, "-Only", "toolchain", "-NoChecksums", "-Out", out)
	} else {
		command = exec.Command("sh", script+".sh", "--companion", companion, "--tool-path", toolPath,
			"--only", "toolchain", "--no-checksums", "--out", out)
	}
	command.Env = environment
	output, _ := command.CombinedOutput()
	return command, output
}

// NEW_374 L7c: -ToolPath takes what its help says, through the kit's own
// argument path and the one resolver a profile binds through. The folder a
// release unpacks to and the bin folder inside it are the same build; nothing
// else is, and a compiler elsewhere on this machine is never picked up.
func TestTheKitTakesAnEricWBuildByItsFolderOrItsBinFolder(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the Companion")
	}
	companion := filepath.Join(t.TempDir(), "companion")
	if runtime.GOOS == "windows" {
		companion += ".exe"
	}
	build := exec.Command("go", "build", "-trimpath", "-o", companion, "./cmd/companion")
	build.Dir = repoRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	decoy := t.TempDir()
	writePrograms(t, decoy, ericwPrograms...)
	environment := append(os.Environ(), "PATH="+decoy+string(os.PathListSeparator)+os.Getenv("PATH"))

	for name, test := range map[string]struct {
		layout func(t *testing.T, build string) // the unpacked build
		named  string                           // what is passed, under it
		state  string
		detail string
	}{
		"the official archive's folder": {
			layout: func(t *testing.T, build string) { writePrograms(t, filepath.Join(build, "bin"), ericwPrograms...) },
			named:  ".", state: "pass", detail: "(<tool_path>)",
		},
		"its bin folder": {
			layout: func(t *testing.T, build string) { writePrograms(t, filepath.Join(build, "bin"), ericwPrograms...) },
			named:  "bin", state: "pass", detail: filepath.Join("<tool_path>", ".."),
		},
		// Upstream's layout. The profile bound here is the Auto-Pigeon build's, whose
		// programs are in bin/, so this is not that build (NEW_380).
		"a build with its programs at the top": {
			layout: func(t *testing.T, build string) { writePrograms(t, build, ericwPrograms...) },
			named:  ".", state: "fail", detail: "no complete Auto-Pigeon EricW build",
		},
		"a program missing": {
			layout: func(t *testing.T, build string) {
				writePrograms(t, filepath.Join(build, "bin"), "qbsp", "light", "bspinfo", "bsputil")
			},
			named: ".", state: "fail", detail: "vis",
		},
		"the wrong folder": {
			layout: func(t *testing.T, build string) { writePrograms(t, filepath.Join(build, "other"), ericwPrograms...) },
			named:  ".", state: "fail", detail: "no complete Auto-Pigeon EricW build",
		},
	} {
		t.Run(name, func(t *testing.T) {
			build := t.TempDir()
			test.layout(t, build)
			out := filepath.Join(t.TempDir(), "bundle")
			_, output := kit(t, companion, filepath.Join(build, test.named), out, environment)
			raw, err := os.ReadFile(filepath.Join(out, "bundle.json"))
			if err != nil {
				t.Fatalf("no bundle: %v\n%s", err, output)
			}
			var bundle struct {
				Lanes []struct {
					ID           string `json:"id"`
					State        string `json:"state"`
					Observations []struct {
						Claim  string `json:"claim"`
						State  string `json:"state"`
						Detail string `json:"detail"`
					} `json:"observations"`
				} `json:"lanes"`
			}
			if err := json.Unmarshal(raw, &bundle); err != nil {
				t.Fatal(err)
			}
			for _, lane := range bundle.Lanes {
				if lane.ID != "toolchain" {
					continue
				}
				t.Logf("%+v", lane)
				if lane.State != test.state {
					t.Errorf("the lane is %s, want %s", lane.State, test.state)
				}
				last := lane.Observations[len(lane.Observations)-1]
				if !strings.Contains(last.Detail, test.detail) {
					t.Errorf("the lane says %q, want %q in it", last.Detail, test.detail)
				}
				if test.state == "fail" && strings.Contains(string(raw), decoy) {
					t.Errorf("a compiler on PATH reached the bundle: %s", raw)
				}
				return
			}
			t.Fatalf("no toolchain lane:\n%s", raw)
		})
	}
}

// The help of both entry points and the option table say the same thing.
func TestTheKitSaysWhatToolPathTakes(t *testing.T) {
	for _, name := range []string{"run-acceptance.sh", "run-acceptance.ps1", "kit-options.json"} {
		raw, err := os.ReadFile(filepath.Join(repoRoot(t), "acceptance", name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if strings.Contains(text, "ROOT of an EricW") || !strings.Contains(text, "bin folder") {
			t.Errorf("%s does not say the folder or its bin folder", name)
		}
	}
}
