package release_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/release"
)

// The declaration has to be readable, and every refusal in it has to be one
// somebody could hit by editing the file.
func TestSupportDeclarationLoads(t *testing.T) {
	support, err := release.LoadSupport()
	if err != nil {
		t.Fatalf("native-support.json: %v", err)
	}
	if len(support.Targets) == 0 {
		t.Fatal("no targets are declared, so this build claims nothing")
	}
}

// `build/release.sh` produces the artifacts and this file describes them. Two
// lists that agree today and are maintained by different hands is the shape
// this workspace keeps finding drift in, so the test reads the shell script.
func TestBuiltTargetsMatchTheReleaseScript(t *testing.T) {
	script, err := os.ReadFile("../../build/release.sh")
	if err != nil {
		t.Fatalf("reading the release script: %v", err)
	}
	pattern := regexp.MustCompile(`(?m)^TARGETS="([^"]+)"`)
	match := pattern.FindSubmatch(script)
	if match == nil {
		t.Fatal("build/release.sh has no default TARGETS line any more; this test and it have to move together")
	}
	fromScript := strings.Split(string(match[1]), ",")
	sort.Strings(fromScript)

	declared, err := release.BuiltTargets()
	if err != nil {
		t.Fatalf("BuiltTargets: %v", err)
	}
	if strings.Join(fromScript, ",") != strings.Join(declared, ",") {
		t.Fatalf("build/release.sh builds %v and native-support.json declares %v", fromScript, declared)
	}
}

// The one derivation, and the property the whole file exists for: nothing that
// has not been RUN becomes a pass.
func TestStateOf(t *testing.T) {
	cases := []struct {
		name          string
		target        release.Target
		verifications []release.Verification
		want          release.SupportState
	}{
		{
			name:   "no artifact at all",
			target: release.Target{Target: "plan9/386", Built: false, NativeHost: release.HostNone},
			want:   release.Unsupported,
		},
		{
			name:   "built, nobody has a machine",
			target: release.Target{Target: "windows/arm64", Built: true, NativeHost: release.HostNone},
			want:   release.BuildOnly,
		},
		{
			name:   "built, the owner has a machine and has not run it",
			target: release.Target{Target: "linux/arm64", Built: true, NativeHost: release.HostOwnerManual},
			want:   release.ManualPending,
		},
		{
			name:   "a bundle passed",
			target: release.Target{Target: "linux/amd64", Built: true, NativeHost: release.HostAgent},
			verifications: []release.Verification{
				{Target: "linux/amd64", Verdict: "pass", BundleID: "b", Digest: "d"},
			},
			want: release.NativePass,
		},
		{
			name:   "a bundle failed, and a later pass does not erase it",
			target: release.Target{Target: "linux/amd64", Built: true, NativeHost: release.HostAgent},
			verifications: []release.Verification{
				{Target: "linux/amd64", Verdict: "pass", BundleID: "b1", Digest: "d"},
				{Target: "linux/amd64", Verdict: "fail", BundleID: "b2", Digest: "d"},
			},
			want: release.NativeFail,
		},
		{
			name:   "a verification for another platform decides nothing here",
			target: release.Target{Target: "darwin/arm64", Built: true, NativeHost: release.HostNone},
			verifications: []release.Verification{
				{Target: "linux/amd64", Verdict: "pass", BundleID: "b", Digest: "d"},
			},
			want: release.BuildOnly,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := release.StateOf(testCase.target, testCase.verifications); got != testCase.want {
				t.Fatalf("StateOf = %q, want %q", got, testCase.want)
			}
		})
	}
}

// A row an operator reads must never say pass for a platform nobody ran.
func TestNoRowIsPassWithoutAVerification(t *testing.T) {
	rows, err := release.Rows()
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	for _, row := range rows {
		if row.State == release.NativePass && len(row.Verifications) == 0 {
			t.Fatalf("%s is native_pass with nothing behind it", row.Target)
		}
		if row.State != release.NativePass && row.State != release.NativeFail && len(row.Verifications) > 0 {
			t.Fatalf("%s has %d verifications and state %q", row.Target, len(row.Verifications), row.State)
		}
	}
}
