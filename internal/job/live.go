package job

import (
	"os"
	"time"
)

// LiveView is what a viewer shows of a job between its output's lines: when
// the program last wrote anything, how far it said it had got, and what its
// processes are using (NEW_365). Read from the executor's own state; asking for
// it starts nothing.
type LiveView struct {
	// Now is the Companion's clock when this was read, so a view says how old
	// a reading is by the same clock that took it.
	Now time.Time `json:"now"`
	// Supervised is true while THIS Companion holds the job's live state. A
	// job recorded as running that it does not hold has no live reading here,
	// and the view says only that it is recorded as running.
	Supervised bool `json:"supervised"`
	// LastOutputAt is when the program last wrote to stdout, stderr or one of
	// its declared logs. Output inactivity, NOT a verdict on the process.
	LastOutputAt *time.Time       `json:"last_output_at,omitempty"`
	Progress     *ProgressReading `json:"progress,omitempty"`
	// Resources is the live reading while it runs, and the frozen summary as
	// a reading once it has stopped.
	Resources *ResourceReading `json:"resources,omitempty"`
}

// Live reads a job's live view.
func (s *Service) Live(j *Job) *LiveView {
	if j == nil {
		return nil
	}
	s.mu.Lock()
	live := s.live[j.ID]
	sampling := s.samplers[j.ID]
	s.mu.Unlock()
	view := &LiveView{Now: time.Now(), Supervised: live != nil}
	var last time.Time
	if live != nil {
		for _, c := range []*capture{live.stdout, live.stderr} {
			if at := c.lastWrite(); at.After(last) {
				last = at
			}
		}
		view.Progress = live.progress.Reading()
		// A declared transcript is written by the tool itself, often while its
		// stdout says nothing; its modification time is when it last wrote.
		for _, sidecar := range j.SidecarLogs {
			if path, err := s.sidecarPath(j, sidecar); err == nil {
				if info, err := os.Stat(path); err == nil && info.ModTime().After(last) {
					last = info.ModTime()
				}
			}
		}
	}
	if !last.IsZero() {
		view.LastOutputAt = &last
	}
	switch {
	case sampling != nil:
		reading := sampling.Reading()
		view.Resources = &reading
	case j.Resources != nil:
		summary := j.Resources
		reading := ResourceReading{
			State: ResourcesFinished, Scope: summary.Scope, PeakCPUPercent: summary.PeakCPUPercent,
			PeakRSSBytes: summary.PeakRSSBytes, LogicalCPUs: summary.LogicalCPUs, Samples: summary.Samples,
			SampledAt: summary.At, Reason: summary.Unavailable,
		}
		if summary.Unavailable != "" {
			reading.State = ResourcesUnavailable
		}
		view.Resources = &reading
	}
	return view
}
