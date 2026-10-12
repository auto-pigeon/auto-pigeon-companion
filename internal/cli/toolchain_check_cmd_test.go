//go:build !windows

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
)

// NEW_323D from a terminal: `toolchain check`, `job run` and `build run` give
// the answer the page gives about a modern profile bound to programs it was
// not written for, and none of them starts a compiler. The fixture programs
// record their own arguments.

const ericwQ1 = "auto-pigeon.ericw-tools.q1"

func compatPrograms(t *testing.T, dir, log string, banners map[string]string) map[string]string {
	t.Helper()
	paths := map[string]string{}
	for name, banner := range banners {
		path := filepath.Join(dir, name)
		script := fmt.Sprintf("#!/bin/sh\necho \"%s|$*\" >> '%s'\nif [ $# -eq 0 ]; then\ncat <<'BANNER'\n---- %s / ericw-tools %s ----\n  %s\nBANNER\nexit 1\nfi\necho compiled\n",
			name, log, name, banner, map[bool]string{true: `-logfile "path"`, false: "usage: " + name + " bspfile"}[strings.HasPrefix(banner, "2.")])
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		paths[name] = path
	}
	return paths
}

func bindCompat(t *testing.T, env *Env, paths map[string]string) {
	t.Helper()
	dir := filepath.Dir(env.ConfigPath)
	entry, err := job.NewCatalog(filepath.Join(dir, "profiles")).Lookup(ericwQ1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := binding.Update(filepath.Join(dir, "bindings.json"), func(set *binding.Set) error {
		return set.Put(binding.LocalBinding{
			ProfileID: ericwQ1, ProfileVersion: entry.Profile.Metadata().Version, ProfileDigest: entry.Digest,
			Trust: entry.Trust, Acquisition: "user_path", Executables: paths,
		})
	}); err != nil {
		t.Fatal(err)
	}
}

func compiled(t *testing.T, log string) []string {
	t.Helper()
	data, _ := os.ReadFile(log)
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" && !strings.HasSuffix(line, "|") {
			out = append(out, line)
		}
	}
	return out
}

func TestTheCommandLineRefusesWhatThePageRefuses(t *testing.T) {
	env := acquireEnv(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "invocations.log")
	source := filepath.Join(dir, "room.map")
	if err := os.WriteFile(source, []byte("{ \"classname\" \"worldspawn\" }\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A modern qbsp beside a 0.18.1 vis and light: the mixed installation.
	bindCompat(t, env, compatPrograms(t, dir, log, map[string]string{
		"qbsp": "2.0.0-alpha11", "vis": "v0.18.1", "light": "v0.18.1", "bspinfo": "v0.18.1", "bsputil": "v0.18.1"}))

	code, out, errOut := runCLI(t, env, "toolchain", "check", ericwQ1, "--json")
	if code != 1 {
		t.Fatalf("toolchain check on a mixed installation exited %d: %s%s", code, out, errOut)
	}
	var report struct {
		Compatible bool `json:"compatible"`
		Programs   []struct {
			Executable, State, Identity, Problem string
		} `json:"programs"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v in %s", err, out)
	}
	got := map[string]string{}
	for _, program := range report.Programs {
		got[program.Executable] = program.State + " " + program.Identity
		if program.State == "incompatible" && !strings.Contains(program.Problem, "v0.18.1") {
			t.Errorf("%s: the problem does not say what it is: %s", program.Executable, program.Problem)
		}
	}
	if report.Compatible || got["qbsp"] != "compatible 2.0.0-alpha11" || got["vis"] != "incompatible v0.18.1" || got["light"] != "incompatible v0.18.1" || len(got) != 3 {
		t.Fatalf("toolchain check: %+v", report)
	}

	// The same sentence for a person.
	code, out, _ = runCLI(t, env, "toolchain", "check", ericwQ1)
	if code != 1 || !strings.Contains(out, "vis") || !strings.Contains(out, "v0.18.1") || !strings.Contains(out, "Nothing else was chosen for you") {
		t.Errorf("toolchain check, as text (%d): %s", code, out)
	}

	// The LISTING says it too (NEW_323D, found live): `build pipelines` printed "ready" for all four
	// built-in Quake 1 pipelines while Profiles and Build & Run said "needs setup", because it only asked
	// whether some profile provides each capability. The qbsp-only leak test is still ready: its program is right.
	code, out, _ = runCLI(t, env, "build", "pipelines", "--json")
	var listed []struct {
		ID       string `json:"id"`
		Runnable bool   `json:"runnable"`
		Ready    bool   `json:"ready"`
		Problems []struct{ Step, Fault, Summary string }
	}
	if err := json.Unmarshal([]byte(out), &listed); code != 0 || err != nil {
		t.Fatalf("build pipelines --json (%d): %v in %s", code, err, out)
	}
	ready := map[string]bool{}
	for _, row := range listed {
		ready[row.ID] = row.Ready
		if row.ID == "auto-pigeon.q1.fast-preview" {
			if !row.Runnable || len(row.Problems) != 2 || row.Problems[0].Step != "vis" || row.Problems[0].Fault != "tool_incompatible" ||
				!strings.Contains(row.Problems[0].Summary, "v0.18.1") || row.Problems[1].Step != "light" {
				t.Errorf("fast-preview on a mixed installation: %+v", row)
			}
		}
	}
	if ready["auto-pigeon.q1.fast-preview"] || ready["auto-pigeon.q1.normal"] || ready["auto-pigeon.q1.final"] || !ready["auto-pigeon.q1.leak-test"] {
		t.Errorf("build pipelines readiness on a mixed installation: %v", ready)
	}
	code, out, _ = runCLI(t, env, "build", "pipelines")
	if code != 0 || !strings.Contains(out, "needs setup") || !strings.Contains(out, "vis (tool_incompatible)") {
		t.Errorf("build pipelines, as text (%d): %s", code, out)
	}

	// `job run` on the incompatible stage: refused, not run.
	code, out, errOut = runCLI(t, env, "job", "run", "--profile", ericwQ1, "--action", "vis",
		"--input", "bsp="+source, "--input", "prt="+source)
	if code == 0 || !strings.Contains(errOut, "v0.18.1") {
		t.Errorf("job run vis exited %d: %s%s", code, out, errOut)
	}
	// `build run`: the whole pipeline, before the compatible qbsp is started.
	code, out, errOut = runCLI(t, env, "build", "run", "--pipeline", "auto-pigeon.q1.fast-preview", "--input", "source_map="+source, "--quiet")
	if code == 0 || !strings.Contains(errOut, "the vis step cannot start") || !strings.Contains(errOut, "v0.18.1") {
		t.Errorf("build run exited %d: %s%s", code, out, errOut)
	}
	if ran := compiled(t, log); len(ran) != 0 {
		t.Fatalf("a program was started with arguments: %v", ran)
	}
	code, out, _ = runCLI(t, env, "build", "list", "--json")
	if code != 0 || strings.Contains(out, "fast-preview") {
		t.Errorf("the refused build left a record (%d): %s", code, out)
	}

	// The binding the page reads now holds the answers, and no approval.
	set, err := binding.LoadFile(filepath.Join(filepath.Dir(env.ConfigPath), "bindings.json"))
	if err != nil {
		t.Fatal(err)
	}
	local, _ := set.Find(ericwQ1)
	if local.Grant != nil || local.Compatibility["vis"].State != binding.FactIncompatible || local.Compatibility["qbsp"].State != binding.FactCompatible {
		t.Errorf("the binding after checking: grant %v, facts %+v", local.Grant, local.Compatibility)
	}

	// Supported programs throughout: the check passes and says so.
	bindCompat(t, env, compatPrograms(t, dir, log, map[string]string{
		"qbsp": "2.0.0-alpha11", "vis": "2.0.0-alpha11", "light": "2.0.0-alpha11", "bspinfo": "2.0.0-alpha11", "bsputil": "2.0.0-alpha11"}))
	if code, out, errOut = runCLI(t, env, "toolchain", "check", ericwQ1); code != 0 || strings.Contains(out, "incompatible") {
		t.Errorf("toolchain check on supported programs exited %d: %s%s", code, out, errOut)
	}
	code, out, _ = runCLI(t, env, "build", "pipelines")
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "auto-pigeon.q1.") && (code != 0 || !strings.Contains(line, " ready") || strings.Contains(line, "needs setup")) {
			t.Errorf("build pipelines on supported programs (%d): %s", code, line)
		}
	}
	if strings.Contains(out, "tool_incompatible") {
		t.Errorf("build pipelines on supported programs still names an incompatible program: %s", out)
	}
	// A toolchain that requires nothing has nothing to check, and says that.
	if code, out, _ = runCLI(t, env, "toolchain", "check", "auto-pigeon.q3map2"); code != 0 || !strings.Contains(out, "requires nothing") {
		t.Errorf("toolchain check q3map2 (%d): %s", code, out)
	}
}
