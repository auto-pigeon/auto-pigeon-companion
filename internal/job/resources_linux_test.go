//go:build linux

package job

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// NEW_365: the sampler against real processes the executor started. Bounds,
// not exact figures — a CI machine's scheduler decides how busy "busy" is —
// and comparisons with independent readings of the same pids.

const mib = int64(1) << 20

// cpuWindow is each process's CPU time at the start and end of the busy window, read from
// /proc/<pid>/stat by the test and not by the sampler.
type cpuWindow struct {
	firstAt, lastAt time.Time
	first, last     map[int]time.Duration
}

func (w *cpuWindow) read(t *testing.T, pids []int) {
	t.Helper()
	now := map[int]time.Duration{}
	for _, pid := range pids {
		raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return
		}
		usage, err := parseUsageStat(string(raw), int64(os.Getpagesize()))
		if err != nil {
			t.Fatalf("reading /proc/%d/stat: %v", pid, err)
		}
		now[pid] = usage.cpu
	}
	if w.first == nil {
		w.first, w.firstAt = now, time.Now()
		return
	}
	w.last, w.lastAt = now, time.Now()
}

// busiestProcess is the percentage of one CPU the busier of the two used over the window.
func (w *cpuWindow) busiestProcess(t *testing.T) float64 {
	t.Helper()
	elapsed := w.lastAt.Sub(w.firstAt)
	if w.last == nil || elapsed < 500*time.Millisecond {
		t.Fatalf("the independent CPU window is %s long, too short to mean anything", elapsed)
	}
	var busiest float64
	for pid, end := range w.last {
		used := float64(end-w.first[pid]) / float64(elapsed) * 100
		t.Logf("process %d used %.0f%% over %s", pid, used, elapsed.Round(time.Millisecond))
		busiest = max(busiest, used)
	}
	return busiest
}

func liveOf(t *testing.T, h *harness, id string) *LiveView {
	t.Helper()
	j, err := h.service.Get(id)
	if err != nil {
		t.Fatalf("reading %s: %v", id, err)
	}
	return h.service.Live(j)
}

// vmRSS is /proc/<pid>/status's VmRSS: another file, another field, the same
// kernel — the independent reading the prompt asks for once.
func vmRSS(t *testing.T, pid int) int64 {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		t.Fatalf("reading /proc/%d/status: %v", pid, err)
	}
	match := regexp.MustCompile(`VmRSS:\s+(\d+) kB`).FindStringSubmatch(string(raw))
	if match == nil {
		t.Fatalf("/proc/%d/status has no VmRSS", pid)
	}
	kb, _ := strconv.ParseInt(match[1], 10, 64)
	return kb * 1024
}

func TestTheSamplerMeasuresTheToolsOwnTreeBusyThenQuiet(t *testing.T) {
	workers := 2
	if runtime.NumCPU() < 4 {
		workers = 1
	}
	h := newHarness(t, fixtureProfile(t, "test.resources",
		modeAction("tree", "resources", "64", "2500", strconv.Itoa(workers), "3500", "child"),
		modeAction("single", "resources", "16", "2500", "1", "3500"),
	))
	tree, err := h.service.Submit(h.helperRequest("test.resources", "tree"))
	if err != nil {
		t.Fatal(err)
	}
	single, err := h.service.Submit(h.helperRequest("test.resources", "single"))
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, h, tree.ID, Running)
	waitForState(t, h, single.ID, Running)

	var busiest, quietest float64 = 0, 1e9
	var busyRSS int64
	window := &cpuWindow{}
	var lastOutputDuringQuiet []time.Time
	var sampledDuringQuiet []time.Time
	compared := false
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		view := liveOf(t, h, tree.ID)
		if view.Resources == nil {
			t.Fatalf("a running tool has no resource reading")
		}
		r := view.Resources
		if r.State == ResourcesFinished {
			break
		}
		if r.Scope != ScopeProcessGroup || r.LogicalCPUs != runtime.NumCPU() {
			t.Fatalf("reading scope %q, logical cpus %d", r.Scope, r.LogicalCPUs)
		}
		stdout := h.mustLogLive(tree.ID)
		quiet := strings.Contains(stdout, "burned")
		if pids := pidsOf(stdout); !quiet && len(pids) == 2 {
			window.read(t, pids)
		}
		if r.State == ResourcesLive && r.CPUPercent != nil {
			if !quiet && *r.CPUPercent > busiest {
				busiest = *r.CPUPercent
				busyRSS = *r.RSSBytes
			}
			if quiet && strings.Count(stdout, "burned") == 2 && !strings.Contains(stdout, "done") && time.Since(view.Now) < time.Second {
				if *r.CPUPercent < quietest {
					quietest = *r.CPUPercent
				}
				sampledDuringQuiet = append(sampledDuringQuiet, r.SampledAt)
				if view.LastOutputAt != nil {
					lastOutputDuringQuiet = append(lastOutputDuringQuiet, *view.LastOutputAt)
				}
				if !compared && r.Processes == 2 {
					compared = true
					pids := pidsOf(stdout)
					var independent int64
					for _, pid := range pids {
						independent += vmRSS(t, pid)
					}
					// Read a moment apart, of a process that is not
					// allocating: within 15%.
					if diff := float64(independent-*r.RSSBytes) / float64(independent); diff > 0.15 || diff < -0.15 {
						t.Errorf("sampler RSS %d vs /proc status VmRSS %d for pids %v", *r.RSSBytes, independent, pids)
					}
					t.Logf("independent reading: sampler %d MiB, VmRSS %d MiB, pids %v", *r.RSSBytes/mib, independent/mib, pids)
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	finished := h.waitFor(tree.ID)
	h.waitFor(single.ID)

	t.Logf("busiest %.0f%%, quietest %.0f%%, busy RSS %d MiB", busiest, quietest, busyRSS/mib)
	// More than EITHER process used by itself proves the reading sums the tree, however busy the machine
	// is. A fixed number of cores could not: on a 4-vCPU hosted runner with every package under test the
	// two processes shared two cores (201% and 202%, NEW_371A release run 41132624992), so what they
	// could use said nothing about what the sampler counted.
	alone := window.busiestProcess(t)
	if busiest < alone*1.25 {
		t.Errorf("busiest reading %.0f%%, and one of the two processes used %.0f%% alone: the reading did not sum them", busiest, alone)
	}
	if quietest > 25 {
		t.Errorf("quietest reading %.0f%% while both processes slept", quietest)
	}
	if busyRSS < 2*60*mib || busyRSS > 2*200*mib {
		t.Errorf("busy RSS %d MiB, want two 64 MiB blocks and a runtime each", busyRSS/mib)
	}
	if !compared {
		t.Errorf("never compared the reading with an independent one")
	}
	// Quiet output, fresh readings: the sampled-at times advance while the last
	// output time stands still.
	if len(sampledDuringQuiet) >= 2 {
		if !sampledDuringQuiet[len(sampledDuringQuiet)-1].After(sampledDuringQuiet[0]) {
			t.Errorf("resource timestamps did not advance during the silence")
		}
		if n := len(lastOutputDuringQuiet); n >= 2 && !lastOutputDuringQuiet[n-1].Equal(lastOutputDuringQuiet[0]) {
			t.Errorf("last output moved during a silence: %v → %v", lastOutputDuringQuiet[0], lastOutputDuringQuiet[n-1])
		}
	} else {
		t.Errorf("too few readings during the silence: %d", len(sampledDuringQuiet))
	}
	if finished.Resources == nil || finished.Resources.PeakRSSBytes < 2*60*mib || finished.Resources.Samples < 3 {
		t.Errorf("the record's summary = %+v", finished.Resources)
	}
	h.service.mu.Lock()
	left := len(h.service.samplers)
	h.service.mu.Unlock()
	if left != 0 {
		t.Errorf("%d samplers left after both jobs ended", left)
	}
	// The other job, concurrently: its own tree, one process.
	other, _ := h.service.Get(single.ID)
	if other.Resources == nil || other.Resources.PeakRSSBytes > 120*mib {
		t.Errorf("the single-process job's summary = %+v; it must not include the other job's tree", other.Resources)
	}
}

// mustLogLive reads stdout while the job may still be running.
func (h *harness) mustLogLive(id string) string {
	chunk, err := h.service.Output(id, OutputRequest{Source: OutputStdout, Offset: 0, Max: MaxOutputChunk})
	if err != nil {
		return ""
	}
	return chunk.Text
}

func pidsOf(stdout string) []int {
	var out []int
	for _, match := range regexp.MustCompile(`allocated \d+ (\d+)`).FindAllStringSubmatch(stdout, -1) {
		pid, _ := strconv.Atoi(match[1])
		out = append(out, pid)
	}
	return out
}

const (
	// viewerLabel marks the goroutines this test's viewers run on. A goroutine inherits its
	// creator's labels, so whatever a viewer's read starts carries it too.
	viewerLabel = "many-viewers"
	// settleBound is how long a goroutine the test expects has to appear or go. It stays
	// under the 2.2 seconds left of the viewers' job when they finish, so anything that lasts
	// as long as the job is still alive when this runs out.
	settleBound = 2 * time.Second
)

func goroutineProfile(t *testing.T) string {
	t.Helper()
	var text strings.Builder
	if err := pprof.Lookup("goroutine").WriteTo(&text, 1); err != nil {
		t.Fatalf("reading the goroutine profile: %v", err)
	}
	return text.String()
}

// goroutinesIn counts the goroutines whose stack or labels in a profile mention marker.
func goroutinesIn(t *testing.T, profile, marker string) int {
	t.Helper()
	_, blocks, _ := strings.Cut(profile, "\n") // the first line is the total
	found := 0
	for _, block := range strings.Split(blocks, "\n\n") {
		if !strings.Contains(block, marker) {
			continue
		}
		count, _, _ := strings.Cut(block, " @ ")
		n, err := strconv.Atoi(count)
		if err != nil {
			t.Fatalf("reading a goroutine profile block %q: %v", block, err)
		}
		found += n
	}
	return found
}

// settleGoroutines polls until unsettled has nothing left to say about the profile, and fails
// with its last word and the profile once settleBound has passed. Goroutines cannot be waited
// for from outside, so this watches the actual state rather than sleeping and hoping.
func settleGoroutines(t *testing.T, unsettled func(profile string) string) {
	t.Helper()
	start := time.Now()
	for {
		profile := goroutineProfile(t)
		reason := unsettled(profile)
		if reason == "" {
			t.Logf("settled in %s", time.Since(start).Round(time.Microsecond))
			return
		}
		if time.Since(start) > settleBound {
			t.Fatalf("%s, still after %s:\n%s", reason, settleBound, profile)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestManyViewersShareOneSamplerAndAnEngineHasNone(t *testing.T) {
	engine := withSessionRole(fixtureAction{
		ID: "play_map", Title: "Play a map", Executable: "helper",
		Args:    []any{helperFlag, "sleep", "2"},
		Roots:   []map[string]any{{"role": "workspace", "access": "read_write", "purpose": "scratch space"}},
		Timeout: 60,
	}, "client")
	he := newHarness(t, fixtureEngineProfile(t, "test.resources.engine", engine))
	played, err := he.service.Submit(he.helperRequest("test.resources.engine", "play_map"))
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, he, played.ID, Running)
	if view := liveOf(t, he, played.ID); view.Resources != nil {
		t.Errorf("an engine has a resource reading: %+v", view.Resources)
	}
	he.waitFor(played.ID)

	h := newHarness(t, fixtureProfile(t, "test.resources.viewers", modeAction("quiet", "resources", "8", "200", "1", "2500")))
	job, err := h.service.Submit(h.helperRequest("test.resources.viewers", "quiet"))
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, h, job.ID, Running)
	// Running is recorded from inside onStarted, and only then does the executor start the
	// sampler, the two output readers and the waiter, in that order. A count taken here could
	// be those four short: 8 grew to 12 with every viewer gone (NEW_371A, build job
	// 124336092517), which settling the second count would never have cured. The waiter is the
	// last of them, so seeing it means the baseline holds all four.
	settleGoroutines(t, func(profile string) string {
		if goroutinesIn(t, profile, "os/exec.(*Cmd).Wait+") != 1 {
			return "the job's waiter has not started"
		}
		return ""
	})
	before := runtime.NumGoroutine()
	var group sync.WaitGroup
	for viewer := 0; viewer < 3; viewer++ {
		group.Add(1)
		go pprof.Do(context.Background(), pprof.Labels("test", viewerLabel), func(context.Context) {
			defer group.Done()
			for i := 0; i < 10; i++ {
				liveOf(t, h, job.ID)
				time.Sleep(50 * time.Millisecond)
			}
		})
	}
	group.Wait()
	// Wait proves each viewer called Done, not that it has returned, so these are given
	// settleBound. The first two are by cause: the process-wide count alone would let a
	// harness goroutine exiting pay for a goroutine a viewer left behind.
	settleGoroutines(t, func(profile string) string {
		left, sampling, after := goroutinesIn(t, profile, viewerLabel), goroutinesIn(t, profile, "(*sampler).run+"), runtime.NumGoroutine()
		switch {
		case left != 0:
			return fmt.Sprintf("%d goroutines a viewer ran on or started are alive", left)
		case sampling != 1:
			return fmt.Sprintf("%d sampler goroutines for one job read by three viewers, want 1", sampling)
		case after > before:
			return fmt.Sprintf("goroutines grew from %d to %d while three viewers read", before, after)
		}
		return ""
	})
	h.service.mu.Lock()
	samplers := len(h.service.samplers)
	h.service.mu.Unlock()
	if samplers != 1 {
		t.Errorf("%d samplers for one job read by three viewers, want 1", samplers)
	}
	h.waitFor(job.ID)
}

func TestATimedOutOrStoppedToolLeavesNoSamplerBehind(t *testing.T) {
	timed := modeAction("timed", "sleep", "30")
	timed.Timeout = 1
	h := newHarness(t, fixtureProfile(t, "test.resources.stop", timed, modeAction("stopped", "sleep", "30")))
	out := h.runToEnd(h.helperRequest("test.resources.stop", "timed"))
	if !out.TimedOut || out.Resources == nil {
		t.Fatalf("timed out = %v, summary %+v", out.TimedOut, out.Resources)
	}
	stopped, err := h.service.Submit(h.helperRequest("test.resources.stop", "stopped"))
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, h, stopped.ID, Running)
	time.Sleep(1500 * time.Millisecond)
	if _, err := h.service.Cancel(stopped.ID); err != nil {
		t.Fatal(err)
	}
	cancelled := h.waitFor(stopped.ID)
	if cancelled.State != Cancelled || cancelled.Resources == nil {
		t.Fatalf("cancelled = %s, summary %+v", cancelled.State, cancelled.Resources)
	}
	if view := h.service.Live(cancelled); view.Resources == nil || view.Resources.State != ResourcesFinished || view.Resources.CPUPercent != nil {
		t.Fatalf("a stopped job's reading = %+v, want finished with no live figure", view.Resources)
	}
	h.service.mu.Lock()
	defer h.service.mu.Unlock()
	if len(h.service.samplers) != 0 {
		t.Fatalf("%d samplers left", len(h.service.samplers))
	}
}

func TestOneSampleCostsWellUnderOnePercentOfACore(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.resources.cost", modeAction("quiet", "resources", "8", "100", "1", "3000", "child")))
	job, err := h.service.Submit(h.helperRequest("test.resources.cost", "quiet"))
	if err != nil {
		t.Fatal(err)
	}
	running := waitForState(t, h, job.ID, Running)
	time.Sleep(500 * time.Millisecond)
	reader, _, err := newTreeReader(running.Process.PID, running.Process.StartTicks)
	if err != nil {
		t.Fatal(err)
	}
	const reads = 200
	var cpuBefore, cpuAfter time.Duration
	cpuBefore = selfCPU(t)
	start := time.Now()
	for i := 0; i < reads; i++ {
		if _, err := reader.read(); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
	wall := time.Since(start) / reads
	cpuAfter = selfCPU(t)
	perSample := (cpuAfter - cpuBefore) / reads
	// One sample a second: its cost per second as a share of one core.
	t.Logf("one sample: %v wall, %v CPU — %.3f%% of one core at one sample a second", wall, perSample, float64(perSample)/float64(time.Second)*100)
	if perSample > 10*time.Millisecond {
		t.Errorf("one sample costs %v of CPU; at one a second that is over 1%% of a core", perSample)
	}
	h.waitFor(job.ID)
}

func selfCPU(t *testing.T) time.Duration {
	raw, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Fatal(err)
	}
	st, err := parseUsageStat(string(raw), 4096)
	if err != nil {
		t.Fatal(err)
	}
	return st.cpu
}

func TestAPercentageArrivesBeforeTheSilenceAndFullVisIsNotTheEnd(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.progress", modeAction("vis", "progress", "2500")))
	job, err := h.service.Submit(h.helperRequest("test.progress", "vis"))
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, h, job.ID, Running)
	deadline := time.Now().Add(2 * time.Second)
	var got *ProgressReading
	for time.Now().Before(deadline) {
		if view := liveOf(t, h, job.ID); view.Progress != nil && view.Progress.Phase == "Full VIS" && view.Progress.Percent != nil {
			got = view.Progress
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got == nil || *got.Percent != 12 {
		t.Fatalf("during Full VIS's silence the reading was %+v, want Full VIS 12%% (Base VIS's 100%% does not carry over)", got)
	}
	h.waitFor(job.ID)
}
