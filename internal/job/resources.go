package job

import (
	"errors"
	"runtime"
	"sync"
	"time"
)

// Live CPU and memory readings of a running tool (NEW_365).
//
// One sampler per supervised tool job, started by the executor once the
// program has started and stopped when it has been reaped, timed out, been
// cancelled or the Companion shut down. A viewer never starts one: the Jobs
// page, the Activity drawer and the CLI all read the last value this sampler
// stored, so three open tabs are three readers of one sampler, not three
// samplers.
//
// What the numbers are, once, because two views that disagreed about it would
// be worse than no numbers:
//
//   - CPU percent is CPU time over wall time across the measured processes:
//     one logical CPU fully busy is 100%, so four busy workers read about 400%.
//     It is never divided by the host's core count. It needs two samples, so
//     the first reading says "measuring" and carries no CPU figure at all.
//   - RAM is the measured processes' current resident set (working set on
//     Windows), summed. Pages two of them share are counted twice — the kernel
//     reports a resident set per process, not per tree — so the sum is an upper
//     bound. Peak is the highest sum seen, labelled as a peak.
//   - A process that exited, or a reading that failed, is a timestamped state,
//     never a zero: 0% CPU is a real reading of a process that is waiting.
//
// None of this says whether a tool is making progress or is stuck. A compiler
// at 0% may be waiting on a disk; one at 400% may be looping. The executor's
// own bounds and the user's Stop decide what happens to it.

// ResourceInterval is how often a running tool is sampled.
const ResourceInterval = time.Second

// The states of a [ResourceReading].
const (
	// ResourcesMeasuring: one sample so far; memory is known, CPU is not yet.
	ResourcesMeasuring = "measuring"
	// ResourcesLive: a current reading of a running process tree.
	ResourcesLive = "live"
	// ResourcesFinished: the process tree exited; only the summary remains.
	ResourcesFinished = "finished"
	// ResourcesUnavailable: nothing could be read. Reason says why.
	ResourcesUnavailable = "unavailable"
)

// The scopes of a [ResourceReading]: what the figures cover.
const (
	// ScopeProcessGroup: the tool and every process in the group the executor
	// started it in — its descendants that stayed in the group it owns.
	ScopeProcessGroup = "process_group"
	// ScopeRootProcess: the tool's own process only; child processes are not
	// counted, because nothing here proves they are the job's.
	ScopeRootProcess = "root_process"
)

// ResourceReading is the last thing a sampler knew about a job's processes.
type ResourceReading struct {
	State string `json:"state"`
	Scope string `json:"scope,omitempty"`
	// CPUPercent is nil until two samples exist, and once nothing is running.
	CPUPercent *float64 `json:"cpu_percent,omitempty"`
	// RSSBytes is the current resident set, summed; nil when not running.
	RSSBytes *int64 `json:"rss_bytes,omitempty"`
	// Processes is how many processes this reading counted.
	Processes int `json:"processes,omitempty"`
	// Peaks are over the whole run, for the summary.
	PeakCPUPercent float64 `json:"peak_cpu_percent,omitempty"`
	PeakRSSBytes   int64   `json:"peak_rss_bytes,omitempty"`
	// LogicalCPUs is this machine's logical CPU count, so a view can say what
	// "400%" is out of without dividing by it.
	LogicalCPUs int `json:"logical_cpus"`
	// SampledAt is when this reading was taken.
	SampledAt time.Time `json:"sampled_at,omitempty"`
	// Samples is how many readings the sampler took.
	Samples int64 `json:"samples"`
	// Reason is why a reading is unavailable, in a few words.
	Reason string `json:"reason,omitempty"`
}

// ResourceSummary is what is kept on the job record once its program has
// stopped: the peaks and how they were measured, never a time series.
type ResourceSummary struct {
	Scope          string    `json:"scope,omitempty"`
	PeakCPUPercent float64   `json:"peak_cpu_percent,omitempty"`
	PeakRSSBytes   int64     `json:"peak_rss_bytes,omitempty"`
	LogicalCPUs    int       `json:"logical_cpus"`
	Samples        int64     `json:"samples"`
	Unavailable    string    `json:"unavailable,omitempty"`
	At             time.Time `json:"at"`
}

// procKey names one process precisely: a pid is reused, a pid and the time
// the kernel started it are not.
type procKey struct {
	pid   int
	start uint64
}

// procUsage is one process's cumulative CPU time and current resident set.
type procUsage struct {
	cpu time.Duration
	rss int64
}

// treeReader reads the processes a job owns right now. errTreeExited means the
// tool's own process is gone (or was replaced by another with its pid).
type treeReader interface {
	read() (map[procKey]procUsage, error)
}

var errTreeExited = errors.New("job: the process has exited")

// sampler holds the readings of one job.
type sampler struct {
	reader   treeReader
	scope    string
	now      func() time.Time
	interval time.Duration

	mu       sync.Mutex
	reading  ResourceReading
	previous map[procKey]procUsage
	prevAt   time.Time

	stop chan struct{}
	done chan struct{}
	once sync.Once
}

func newSampler(reader treeReader, scope string, now func() time.Time) *sampler {
	return &sampler{
		reader:   reader,
		scope:    scope,
		now:      now,
		interval: ResourceInterval,
		reading:  ResourceReading{State: ResourcesMeasuring, Scope: scope, LogicalCPUs: runtime.NumCPU()},
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// unavailableSampler is a sampler that never samples: a platform, or a
// process, that cannot be measured says so rather than reading zero.
func unavailableSampler(reason string, now func() time.Time) *sampler {
	s := newSampler(nil, "", now)
	s.reading.State = ResourcesUnavailable
	s.reading.Reason = reason
	s.reading.SampledAt = now()
	close(s.done)
	s.once.Do(func() { close(s.stop) })
	return s
}

// run samples until stopped. A failed sample never stops the job, its output
// or its cancellation: it is recorded on the reading and the next tick tries
// again.
func (s *sampler) run() {
	defer close(s.done)
	s.sample()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			if !s.sample() {
				return
			}
		}
	}
}

// sample takes one reading, and reports whether there is anything left to
// sample.
func (s *sampler) sample() bool {
	usage, err := s.reader.read()
	return s.record(s.now(), usage, err)
}

// record folds one reading in. Separate from sample so the arithmetic is
// tested with a clock and counters a test controls.
func (s *sampler) record(at time.Time, usage map[procKey]procUsage, err error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reading.Samples++
	s.reading.SampledAt = at
	switch {
	case errors.Is(err, errTreeExited):
		s.finishLocked()
		return false
	case err != nil:
		// A read that failed: the process may still be running, so the state
		// says the figures are not known now, and the previous counters are
		// dropped so the next good reading starts measuring again rather
		// than averaging over the gap.
		s.reading.State = ResourcesUnavailable
		s.reading.Reason = err.Error()
		s.reading.CPUPercent, s.reading.RSSBytes = nil, nil
		s.previous = nil
		return true
	}
	var rss int64
	for _, u := range usage {
		rss += u.rss
	}
	s.reading.Reason = ""
	s.reading.Processes = len(usage)
	s.reading.RSSBytes = &rss
	if rss > s.reading.PeakRSSBytes {
		s.reading.PeakRSSBytes = rss
	}
	if s.previous == nil {
		s.reading.State = ResourcesMeasuring
		s.reading.CPUPercent = nil
	} else if percent, ok := cpuPercent(s.previous, usage, at.Sub(s.prevAt)); ok {
		s.reading.State = ResourcesLive
		s.reading.CPUPercent = &percent
		if percent > s.reading.PeakCPUPercent {
			s.reading.PeakCPUPercent = percent
		}
	}
	s.previous, s.prevAt = usage, at
	return true
}

func (s *sampler) finishLocked() {
	s.reading.Reason = ""
	s.reading.State = ResourcesFinished
	s.reading.CPUPercent, s.reading.RSSBytes = nil, nil
	s.reading.Processes = 0
	s.previous = nil
}

// cpuPercent is CPU time used between two readings over the wall time between
// them, 100 per fully busy logical CPU.
//
// Each process is compared with itself, by pid AND start time, so a pid that
// was reused is a new process, not a counter that jumped. A process present
// only in the newer reading started in between and all of its CPU time is
// counted; one present only in the older exited, and the CPU it used after the
// older reading is lost — an undercount, never an invention. A counter that
// went backwards is not a negative amount of work and contributes nothing.
func cpuPercent(previous, current map[procKey]procUsage, elapsed time.Duration) (float64, bool) {
	if elapsed <= 0 {
		return 0, false
	}
	var used time.Duration
	for key, now := range current {
		before, seen := previous[key]
		switch {
		case !seen:
			used += now.cpu
		case now.cpu > before.cpu:
			used += now.cpu - before.cpu
		}
	}
	return float64(used) / float64(elapsed) * 100, true
}

// Stop ends sampling and freezes the reading as finished. Safe to call more
// than once and from any goroutine; it returns once the sampling goroutine has.
func (s *sampler) Stop() ResourceReading {
	s.once.Do(func() { close(s.stop) })
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	// A sampler that never had a reader (a platform that cannot measure)
	// stays unavailable, with its reason; every other one is now finished.
	if s.reader != nil {
		s.reading.SampledAt = s.now()
		s.finishLocked()
	}
	return s.reading
}

// Reading is the latest reading.
func (s *sampler) Reading() ResourceReading {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.reading
	if s.reading.CPUPercent != nil {
		v := *s.reading.CPUPercent
		out.CPUPercent = &v
	}
	if s.reading.RSSBytes != nil {
		v := *s.reading.RSSBytes
		out.RSSBytes = &v
	}
	return out
}

// summary is the reading as kept on the job record.
func (r ResourceReading) summary() *ResourceSummary {
	out := &ResourceSummary{
		Scope: r.Scope, PeakCPUPercent: r.PeakCPUPercent, PeakRSSBytes: r.PeakRSSBytes,
		LogicalCPUs: r.LogicalCPUs, Samples: r.Samples, At: r.SampledAt,
	}
	if r.State == ResourcesUnavailable {
		out.Unavailable = r.Reason
	}
	return out
}

// startSampler starts the sampler for a program the executor just started.
func startSampler(pid int, startTicks uint64, now func() time.Time) *sampler {
	reader, scope, err := newTreeReader(pid, startTicks)
	if err != nil {
		return unavailableSampler(err.Error(), now)
	}
	s := newSampler(reader, scope, now)
	go s.run()
	return s
}
