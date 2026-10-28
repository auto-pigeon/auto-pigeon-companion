package job

import (
	"errors"
	"testing"
	"time"
)

// NEW_365: the arithmetic of a reading, with a clock and counters the test
// controls. Nothing here starts a process.

type scriptedTree struct {
	readings []map[procKey]procUsage
	errs     []error
	n        int
}

func (s *scriptedTree) read() (map[procKey]procUsage, error) {
	i := s.n
	s.n++
	if i < len(s.errs) && s.errs[i] != nil {
		return nil, s.errs[i]
	}
	return s.readings[i], nil
}

func usage(cpu time.Duration, rss int64) procUsage { return procUsage{cpu: cpu, rss: rss} }

func TestAReadingNeedsTwoSamplesBeforeItHasACPUFigure(t *testing.T) {
	t0 := time.Unix(1000, 0)
	s := newSampler(nil, ScopeProcessGroup, func() time.Time { return t0 })
	s.record(t0, map[procKey]procUsage{{10, 1}: usage(time.Second, 64<<20)}, nil)
	first := s.Reading()
	if first.State != ResourcesMeasuring || first.CPUPercent != nil {
		t.Fatalf("first sample = %s with cpu %v, want measuring with no CPU figure", first.State, first.CPUPercent)
	}
	if first.RSSBytes == nil || *first.RSSBytes != 64<<20 {
		t.Fatalf("first sample memory = %v, want 64 MiB: memory is known from one sample", first.RSSBytes)
	}
	s.record(t0.Add(time.Second), map[procKey]procUsage{{10, 1}: usage(1500*time.Millisecond, 64<<20)}, nil)
	second := s.Reading()
	if second.State != ResourcesLive || second.CPUPercent == nil || *second.CPUPercent != 50 {
		t.Fatalf("second sample = %s %v, want live 50%%", second.State, second.CPUPercent)
	}
}

func TestFourBusyProcessesReadAboutFourHundredPercent(t *testing.T) {
	before := map[procKey]procUsage{}
	after := map[procKey]procUsage{}
	for pid := 1; pid <= 4; pid++ {
		before[procKey{pid, 7}] = usage(10*time.Second, 1)
		after[procKey{pid, 7}] = usage(12*time.Second, 1) // 2 s of CPU in 2 s of wall time, each
	}
	got, ok := cpuPercent(before, after, 2*time.Second)
	if !ok || got != 400 {
		t.Fatalf("cpu = %v (%v), want 400: one busy logical CPU is 100%%, never divided by the core count", got, ok)
	}
}

func TestAReusedPidIsANewProcessAndABackwardCounterIsNoWork(t *testing.T) {
	before := map[procKey]procUsage{{50, 100}: usage(30*time.Second, 1), {51, 100}: usage(5*time.Second, 1)}
	after := map[procKey]procUsage{
		// pid 50 exited and its pid now names another process that started
		// since: compared with nothing, counted from zero, not as -29.5 s.
		{50, 900}: usage(500*time.Millisecond, 1),
		// a counter that went backwards contributes nothing.
		{51, 100}: usage(4*time.Second, 1),
	}
	got, ok := cpuPercent(before, after, time.Second)
	if !ok || got != 50 {
		t.Fatalf("cpu = %v, want 50 (the new process's 0.5 s only)", got)
	}
	if _, ok := cpuPercent(before, after, 0); ok {
		t.Fatalf("zero elapsed time gave a figure; it must give none")
	}
}

func TestAChildThatStartsAndOneThatExitsAreCountedOnlyWhileTheyRun(t *testing.T) {
	t0 := time.Unix(2000, 0)
	s := newSampler(nil, ScopeProcessGroup, func() time.Time { return t0 })
	root, child := procKey{1, 1}, procKey{2, 5}
	s.record(t0, map[procKey]procUsage{root: usage(0, 10)}, nil)
	s.record(t0.Add(time.Second), map[procKey]procUsage{root: usage(100*time.Millisecond, 10), child: usage(900*time.Millisecond, 90)}, nil)
	r := s.Reading()
	if *r.CPUPercent != 100 || *r.RSSBytes != 100 || r.Processes != 2 {
		t.Fatalf("with a child = %v%% %v bytes %d processes, want 100%% 100 2", *r.CPUPercent, *r.RSSBytes, r.Processes)
	}
	s.record(t0.Add(2*time.Second), map[procKey]procUsage{root: usage(200*time.Millisecond, 10)}, nil)
	r = s.Reading()
	if *r.CPUPercent != 10 || *r.RSSBytes != 10 || r.Processes != 1 {
		t.Fatalf("after the child exited = %v%% %v bytes, want 10%% 10: an exited child is not current usage", *r.CPUPercent, *r.RSSBytes)
	}
	if r.PeakRSSBytes != 100 || r.PeakCPUPercent != 100 {
		t.Fatalf("peaks = %v %v, want 100 100", r.PeakRSSBytes, r.PeakCPUPercent)
	}
}

func TestADeniedReadIsUnavailableAndAnExitIsFinishedNeverZero(t *testing.T) {
	t0 := time.Unix(3000, 0)
	s := newSampler(nil, ScopeProcessGroup, func() time.Time { return t0 })
	s.record(t0, map[procKey]procUsage{{1, 1}: usage(0, 10)}, nil)
	s.record(t0.Add(time.Second), nil, errors.New("reading process 1 is not permitted here"))
	r := s.Reading()
	if r.State != ResourcesUnavailable || r.CPUPercent != nil || r.RSSBytes != nil || r.Reason == "" || !r.SampledAt.Equal(t0.Add(time.Second)) {
		t.Fatalf("denied = %+v, want unavailable, timestamped, no figures", r)
	}
	// The next good reading starts measuring again rather than averaging over
	// the gap.
	s.record(t0.Add(2*time.Second), map[procKey]procUsage{{1, 1}: usage(5*time.Second, 10)}, nil)
	if r = s.Reading(); r.State != ResourcesMeasuring || r.CPUPercent != nil {
		t.Fatalf("after a gap = %s %v, want measuring", r.State, r.CPUPercent)
	}
	if more := s.record(t0.Add(3*time.Second), nil, errTreeExited); more {
		t.Fatalf("an exited tree asked for another sample")
	}
	if r = s.Reading(); r.State != ResourcesFinished || r.CPUPercent != nil || r.RSSBytes != nil {
		t.Fatalf("exited = %+v, want finished with no live figures", r)
	}
}

func TestStoppingASamplerEndsItsGoroutineAndIsSafeTwice(t *testing.T) {
	tree := &scriptedTree{}
	for i := 0; i < 1000; i++ {
		tree.readings = append(tree.readings, map[procKey]procUsage{{1, 1}: usage(time.Duration(i)*time.Millisecond, 1)})
	}
	s := newSampler(tree, ScopeProcessGroup, time.Now)
	s.interval = time.Millisecond
	go s.run()
	time.Sleep(20 * time.Millisecond)
	final := s.Stop()
	select {
	case <-s.done:
	default:
		t.Fatalf("the sampling goroutine is still running after Stop")
	}
	if final.State != ResourcesFinished {
		t.Fatalf("stopped = %s, want finished", final.State)
	}
	if again := s.Stop(); again.State != ResourcesFinished {
		t.Fatalf("a second Stop = %s", again.State)
	}
}

func TestAPlatformThatCannotMeasureSaysUnavailable(t *testing.T) {
	s := unavailableSampler("CPU and memory are not measured on this platform", time.Now)
	r := s.Stop()
	if r.State != ResourcesUnavailable || r.Reason == "" || r.CPUPercent != nil || r.RSSBytes != nil {
		t.Fatalf("unavailable platform = %+v", r)
	}
	if summary := r.summary(); summary.Unavailable == "" {
		t.Fatalf("its summary lost the reason")
	}
}

func TestProgressBelongsToItsPhaseAndComesOnlyFromTheTool(t *testing.T) {
	clock := time.Unix(4000, 0)
	p := newProgressTracker(func() time.Time { return clock })
	if p.Reading() != nil {
		t.Fatalf("a tool that printed nothing has a progress reading")
	}
	p.feed("stdout", []byte("Calculating Base Vis:\n[  0%]  ...\r[ 50%]  ...\r[100%] time elapsed: 00:00:00\n"))
	r := p.Reading()
	if r.Phase != "Base VIS" || r.Percent == nil || *r.Percent != 100 {
		t.Fatalf("after Base VIS = %+v, want Base VIS 100", r)
	}
	// Base VIS at 100% is not the build at 100%: Full VIS clears the number.
	p.feed("stdout", []byte("Calculating Full Vis:\r\n"))
	if r = p.Reading(); r.Phase != "Full VIS" || r.Percent != nil {
		t.Fatalf("Full VIS start = %+v, want Full VIS with no percentage yet", r)
	}
	// A bracket split across two reads, unterminated, ahead of a silence.
	p.feed("stdout", []byte("[ 1"))
	p.feed("stdout", []byte("2%]  ..."))
	if r = p.Reading(); r.Percent == nil || *r.Percent != 12 {
		t.Fatalf("an unterminated fragment = %+v, want 12", r)
	}
	// Dots, timers and other numbers are not percentages.
	p.feed("stdout", []byte("\r0...1...2...3...\n   1234 portals\ntime elapsed: 00:10:00\n[ 999%]\n"))
	if r = p.Reading(); *r.Percent != 12 {
		t.Fatalf("non-progress lines changed the percentage to %d", *r.Percent)
	}
	p.feed("stderr", []byte("---- CalcAmbientSounds ----\n"))
	if r = p.Reading(); r.Phase != "CalcAmbientSounds" || r.Percent != nil {
		t.Fatalf("a section line = %+v", r)
	}
	p.feed("stdout", []byte("---- vis / ericw-tools 2.0.0 ----\n--- BasePortalVis (8) ---\n"))
	if r = p.Reading(); r.Phase != "BasePortalVis" {
		t.Fatalf("q3map2's section = %+v; the EricW banner is not a phase", r)
	}
}

func TestALiveReadDeliversCarriageReturnFragmentsAndHoldsOnlyTheLast(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.output.cr", modeAction("cr", "cr")))
	job, err := h.service.Submit(h.helperRequest("test.output.cr", "cr"))
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, h, job.ID, Running)
	var text string
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		chunk, err := h.service.Output(job.ID, OutputRequest{Source: OutputStdout, Offset: 0})
		if err == nil && chunk.Text != "start\n" && chunk.Text != "" {
			text = chunk.Text
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if text != "start\n[ 10%] a\r[ 20%] b\r" {
		t.Fatalf("live read = %q, want the finished fragments up to the last carriage return", text)
	}
	finished := h.waitFor(job.ID)
	chunk, err := h.service.Output(finished.ID, OutputRequest{Source: OutputStdout, Offset: 0})
	if err != nil || chunk.Text != "start\n[ 10%] a\r[ 20%] b\r[ 30%] c\r\nend\r\n" {
		t.Fatalf("final read = %q (%v)", chunk.Text, err)
	}
}
