package cli

import (
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// The profile a run used was written for 1.4556; the Companion's managed
// install had moved on to 1.4560, and that is the program that ran (AUCOM #49,
// NEW_633). `build show` names the build that ran on its `tool` line and the
// profile as the profile — never the profile's number as the run's compiler.
const (
	q2ProfileName  = "EricW tools, Auto-Pigeon build 1.4556 (Quake II, experimental)"
	q2WrittenFor   = "2.0.0-alpha11-70-ge923cca3+auto-pigeon.1.4556"
	q2RanIdentity  = "2.0.0-alpha11-74-gc8a1cfaf+auto-pigeon.1.4560"
	q2RanQbspSHA   = "8d4c5f4800000000000000000000000000000000000000000000000000000000"
	q2OlderQbspSHA = "1111111100000000000000000000000000000000000000000000000000000000"
)

func q2Tool(managed *binding.ManagedInstall, executables ...build.ExecutableRecord) build.ToolRecord {
	return build.ToolRecord{
		Profile:     build.DocumentRef{ID: "auto-pigeon.ericw-tools.auto-pigeon-build.q2", Version: "1.0.0", Name: q2ProfileName},
		ToolVersion: q2WrittenFor, Acquisition: profile.AcquireManagedOfficial, Managed: managed, Executables: executables,
	}
}

func toolLines(out string) (tool, profileLine string) {
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "tool "):
			tool = trimmed
		case strings.HasPrefix(trimmed, "profile "):
			profileLine = trimmed
		}
	}
	return tool, profileLine
}

func TestBuildShowNamesTheCompilerThatRanNotTheProfilesVersion(t *testing.T) {
	env, stdout, _ := testEnv(t)
	printBuildOutcome(env, &build.Manifest{BuildID: "b_ran", State: job.Failed, Tools: []build.ToolRecord{q2Tool(
		&binding.ManagedInstall{Tool: "ericw-tools", Version: "1.4560", Tag: "auto-pigeon-ericw-v1.4560"},
		build.ExecutableRecord{Name: "qbsp", Path: "/managed/1.4560/bin/qbsp", SHA256: q2RanQbspSHA,
			Identity: q2RanIdentity, IdentityState: "compatible"},
	)}})
	tool, profileLine := toolLines(stdout.String())
	if !strings.Contains(tool, "ericw-tools 1.4560") || !strings.Contains(tool, q2RanIdentity) {
		t.Errorf("the tool line does not name the build that ran (1.4560):\n%s", stdout)
	}
	if strings.Contains(tool, "1.4556") {
		t.Errorf("the tool line names the profile's 1.4556 as the run's compiler:\n%s", stdout)
	}
	if !strings.Contains(profileLine, q2ProfileName) || !strings.Contains(profileLine, "written for "+q2WrittenFor) {
		t.Errorf("the profile is not named as the profile, written for its version:\n%s", stdout)
	}
}

// Two managed versions on one machine: the build's record is what the binding
// said when it started, and the executable's identity is about those bytes.
// A run on the older install reads as the older install.
func TestBuildShowTellsTwoInstalledVersionsApart(t *testing.T) {
	for _, tc := range []struct {
		version, identity, sha string
	}{
		{"1.4560", q2RanIdentity, q2RanQbspSHA},
		{"1.4559", "2.0.0-alpha11-73-g0a1b2c3d+auto-pigeon.1.4559", q2OlderQbspSHA},
	} {
		env, stdout, _ := testEnv(t)
		printBuildOutcome(env, &build.Manifest{BuildID: "b_" + tc.version, State: job.Succeeded, Tools: []build.ToolRecord{q2Tool(
			&binding.ManagedInstall{Tool: "ericw-tools", Version: tc.version},
			build.ExecutableRecord{Name: "qbsp", Path: "/managed/" + tc.version + "/bin/qbsp", SHA256: tc.sha, Identity: tc.identity},
		)}})
		tool, _ := toolLines(stdout.String())
		if !strings.Contains(tool, "ericw-tools "+tc.version) || !strings.Contains(tool, tc.identity) {
			t.Errorf("a run on %s does not say so:\n%s", tc.version, stdout)
		}
		if !strings.Contains(stdout.String(), tc.sha) {
			t.Errorf("the run on %s does not carry the digest of the qbsp it started:\n%s", tc.version, stdout)
		}
	}
}

// A run recorded before any identity was kept: nothing says which build ran,
// so nothing is invented — the line says it was not recorded and the profile
// stays the profile.
func TestBuildShowSaysAnOldRunsCompilerWasNotRecorded(t *testing.T) {
	env, stdout, _ := testEnv(t)
	old := q2Tool(nil, build.ExecutableRecord{Name: "qbsp", Path: "/somewhere/qbsp", SHA256: q2OlderQbspSHA})
	old.Acquisition = profile.AcquireUserPath
	printBuildOutcome(env, &build.Manifest{BuildID: "b_old", State: job.Succeeded, Tools: []build.ToolRecord{old}})
	tool, profileLine := toolLines(stdout.String())
	if !strings.Contains(tool, "version not recorded") {
		t.Errorf("an old run without an identity does not say its version was not recorded:\n%s", stdout)
	}
	if strings.Contains(tool, "1.4556") || strings.Contains(tool, q2WrittenFor) {
		t.Errorf("an old run is labelled with the profile's version as if it ran:\n%s", stdout)
	}
	if !strings.Contains(profileLine, "written for "+q2WrittenFor) {
		t.Errorf("the profile line is missing:\n%s", stdout)
	}
}
