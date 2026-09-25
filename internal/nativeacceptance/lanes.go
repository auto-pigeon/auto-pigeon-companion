package nativeacceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ToolProfileID is the id the profile lane's authored document declares.
//
// It is not the id of anything published: this document is written by an
// acceptance run, into a directory that run made, and is withdrawn and purged
// before the run ends.
const ToolProfileID = "auto-pigeon.acceptance.native-kit"

// Q1ToolProfileID and Q1PipelineID are the built-in documents the compile lane
// drives. Named here rather than typed at three call sites.
const (
	Q1ToolProfileID = "auto-pigeon.ericw-tools.q1"
	Q1PipelineID    = "auto-pigeon.q1.normal"
)

func (r *run) lane(ctx context.Context, id string) Lane {
	switch id {
	case "artifact":
		return r.laneArtifact(ctx)
	case "first-start":
		return r.laneFirstStart(ctx)
	case "uri":
		return r.laneURI(ctx)
	case "profile":
		return r.laneProfile(ctx)
	case "toolchain":
		return r.laneToolchain(ctx)
	case "compile":
		return r.laneCompile(ctx)
	case "engine":
		return r.laneEngine(ctx)
	case "jobs":
		return r.laneJobs(ctx)
	case "purge":
		return r.lanePurge(ctx)
	}
	return Lane{State: NotAvailable, Reason: "no such lane"}
}

// --- artifact ---------------------------------------------------------------

func (r *run) laneArtifact(ctx context.Context) Lane {
	lane := Lane{}
	add := func(observation Observation) { lane.Observations = append(lane.Observations, observation) }

	// What the SHELL did before this program started. It is reported and not
	// re-done: a program cannot vouch for its own bytes, and one that claimed
	// to would be checking a file it had already loaded.
	switch r.options.ChecksumsVerified {
	case Pass:
		add(pass("the entry point verified the artifact's checksums before starting it",
			r.redact.Line(r.options.ChecksumDetail)))
	case Fail:
		add(failed("the entry point verified the artifact's checksums before starting it",
			r.redact.Line(r.options.ChecksumDetail)))
	default:
		add(unavailable("the entry point verified the artifact's checksums before starting it",
			"no checksum result was reported; a direct invocation cannot vouch for its own bytes"))
	}

	sbomPath := filepath.Join(r.work, "sbom.cdx.json")
	res := r.exec(ctx, 120*time.Second, "release", "sbom", "--out", sbomPath)
	if res.code != 0 {
		add(failed("this build writes its own SBOM", r.redact.Line(res.output())))
	} else {
		add(r.checkSBOM(sbomPath))
	}

	version := r.exec(ctx, 30*time.Second, "version")
	add(verdict(version.code == 0 && strings.TrimSpace(version.stdout) != "",
		"the program reports a version",
		strings.TrimSpace(version.stdout),
		"it printed nothing"))

	facts := Facts()
	add(pass("the running artifact names its own target",
		fmt.Sprintf("%s, %s, %d cpu, %s-endian, cgo=%t",
			facts.Target, facts.Runtime, facts.CPUs, facts.Endian, facts.Cgo)))

	// The one check that separates native evidence from an emulator's.
	matched, normalised := hostArchMatches(r.options.HostArch)
	switch {
	case r.options.HostArch == "":
		add(unavailable("the artifact's architecture is this machine's own",
			"no host architecture was reported; run through the kit's entry point"))
	case normalised == "":
		add(unavailable("the artifact's architecture is this machine's own",
			fmt.Sprintf("the operating system calls this machine %q, which this build has no mapping for",
				r.options.HostArch)))
	case matched:
		add(pass("the artifact's architecture is this machine's own",
			fmt.Sprintf("%s artifact on a %s machine", runtime.GOARCH, normalised)))
	default:
		add(failed("the artifact's architecture is this machine's own",
			fmt.Sprintf("a %s artifact is running on a %s machine, so this run is evidence about an emulator",
				runtime.GOARCH, normalised)))
	}
	return lane
}

func (r *run) checkSBOM(path string) Observation {
	data, err := os.ReadFile(path)
	if err != nil {
		return failed("this build writes its own SBOM", "the file it reported writing is not there")
	}
	var document struct {
		BOMFormat  string `json:"bomFormat"`
		Version    string `json:"specVersion"`
		Components []struct {
			Name string `json:"name"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return failed("this build writes its own SBOM", "it is not JSON")
	}
	if document.BOMFormat != "CycloneDX" || len(document.Components) == 0 {
		return failed("this build writes its own SBOM",
			fmt.Sprintf("format %q with %d components", document.BOMFormat, len(document.Components)))
	}
	return pass("this build writes its own SBOM",
		fmt.Sprintf("CycloneDX %s, %d components", document.Version, len(document.Components)))
}

// --- first start ------------------------------------------------------------

func (r *run) laneFirstStart(ctx context.Context) Lane {
	lane := Lane{}
	add := func(observation Observation) { lane.Observations = append(lane.Observations, observation) }

	migrate := r.exec(ctx, 60*time.Second, "migrate")
	add(verdict(migrate.code == 0,
		"a first start on a machine with nothing to migrate says so and succeeds",
		r.redact.Line(migrate.stdout),
		r.redact.Line(migrate.output())))

	before, err := r.uninstallTargets(ctx)
	if err != nil {
		add(failed("the program lists what it has put on this machine", r.redact.Line(err.Error())))
		return lane
	}
	add(verdict(len(before) > 0,
		"the program lists what it has put on this machine",
		fmt.Sprintf("%d locations, %d already present", len(before), countPresent(before)),
		"it listed nothing"))

	// Preserve: `uninstall` without --purge is a listing and nothing else.
	preserve := r.exec(ctx, 60*time.Second, "uninstall")
	after, afterErr := r.uninstallTargets(ctx)
	switch {
	case preserve.code != 0:
		add(failed("uninstall without --purge changes nothing", r.redact.Line(preserve.output())))
	case afterErr != nil:
		add(failed("uninstall without --purge changes nothing", r.redact.Line(afterErr.Error())))
	case countPresent(after) == countPresent(before):
		add(pass("uninstall without --purge changes nothing",
			fmt.Sprintf("%d locations still present", countPresent(after))))
	default:
		add(failed("uninstall without --purge changes nothing",
			fmt.Sprintf("%d present before, %d after", countPresent(before), countPresent(after))))
	}

	configDir, err := r.resolveConfigDir(ctx)
	if err != nil {
		add(failed("the program says where its own configuration lives", r.redact.Line(err.Error())))
		return lane
	}
	r.redact.Root(configDir, "config")
	add(pass("the program says where its own configuration lives",
		"inside the directory this run owns, so nothing on the operator's machine is touched"))
	return lane
}

type uninstallTarget struct {
	Label   string `json:"Label"`
	Path    string `json:"Path"`
	Present bool   `json:"Present"`
	Bytes   int64  `json:"Bytes"`
	Files   int    `json:"Files"`
}

func (r *run) uninstallTargets(ctx context.Context) ([]uninstallTarget, error) {
	res := r.exec(ctx, 60*time.Second, "uninstall", "--json")
	if res.code != 0 {
		return nil, fmt.Errorf("uninstall --json exited %d", res.code)
	}
	var targets []uninstallTarget
	if err := json.Unmarshal([]byte(res.stdout), &targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func countPresent(targets []uninstallTarget) int {
	count := 0
	for _, target := range targets {
		if target.Present {
			count++
		}
	}
	return count
}

// --- uri --------------------------------------------------------------------

func (r *run) laneURI(ctx context.Context) Lane {
	lane := Lane{}
	add := func(observation Observation) { lane.Observations = append(lane.Observations, observation) }

	status := r.exec(ctx, 60*time.Second, "uri", "status")
	add(verdict(status.code == 0 && strings.Contains(status.stdout, "autopigeon://"),
		"the program says what handles autopigeon:// on this machine",
		r.redact.Line(firstLine(status.stdout)),
		r.redact.Line(status.output())))

	// Registration is performed only where THIS RUN CONTAINS THE WRITE.
	//
	// On Linux the handler is a desktop entry and a mimeapps.list under
	// $XDG_DATA_HOME, which this run points at a directory it made, so
	// registering and unregistering is fully exercised and costs the operator
	// nothing. On Windows it is HKCU, which no environment variable moves, and
	// the installer owns those keys; on macOS the command reports and does not
	// perform it at all, because a loose binary has no bundle to declare a
	// scheme in. Performing it anyway would change which program opens a link
	// on somebody's machine as a side effect of an acceptance run.
	if runtime.GOOS != "linux" {
		add(inapplicable("registering and unregistering the handler is exercised",
			"on "+runtime.GOOS+" the handler is not written anywhere this run owns: the installer "+
				"or the application bundle owns it, and an acceptance run may not change "+
				"which program opens a scheme on this machine"))
		return lane
	}

	register := r.exec(ctx, 60*time.Second, "uri", "register")
	if register.code != 0 {
		add(failed("registering the handler succeeds", r.redact.Line(register.output())))
		return lane
	}
	registered := r.exec(ctx, 60*time.Second, "uri", "status")
	add(verdict(strings.Contains(registered.stdout, "registered: yes"),
		"registering the handler succeeds and the status says so",
		"registered: yes",
		r.redact.Line(registered.output())))

	entry := filepath.Join(r.home, "data", "applications", "auto-pigeon-companion-join.desktop")
	_, err := os.Stat(entry)
	add(verdict(err == nil,
		"the handler was written inside this run's own directory and nowhere else",
		"the desktop entry is under this run's XDG_DATA_HOME",
		"nothing was written where this run pointed XDG_DATA_HOME"))

	unregister := r.exec(ctx, 60*time.Second, "uri", "unregister")
	final := r.exec(ctx, 60*time.Second, "uri", "status")
	add(verdict(unregister.code == 0 && strings.Contains(final.stdout, "registered: no"),
		"unregistering removes it again",
		"registered: no",
		r.redact.Line(unregister.output()+final.output())))
	return lane
}

func firstLine(text string) string {
	trimmed := strings.TrimSpace(text)
	if index := strings.IndexAny(trimmed, "\r\n"); index >= 0 {
		return trimmed[:index]
	}
	return trimmed
}
