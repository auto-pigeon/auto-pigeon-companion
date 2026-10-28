package managedtools

import (
	"time"
)

// Phase is one tool's state as the page shows it.
type Phase string

const (
	PhaseDisabled    Phase = "disabled"
	PhaseNotChecked  Phase = "not_checked"
	PhaseChecking    Phase = "checking"
	PhaseDownloading Phase = "downloading"
	PhaseVerifying   Phase = "verifying"
	PhaseReady       Phase = "ready"
	PhaseFailed      Phase = "failed"
	PhaseUnsupported Phase = "unsupported"
)

// Status is everything the page and `companion tools status` show.
type Status struct {
	Enabled bool    `json:"enabled"`
	Channel Channel `json:"channel"`
	Target  string  `json:"target"`
	Root    string  `json:"root"`
	// SettingUp is true while any tool has never been installed and is being
	// (or failed to be): the main flow's "Setting up build tools" card.
	SettingUp bool         `json:"setting_up"`
	Tools     []ToolStatus `json:"tools"`
	Notices   []Notice     `json:"notices,omitempty"`
}

// ToolStatus is one tool.
type ToolStatus struct {
	ID          ToolID `json:"id"`
	Kind        Kind   `json:"kind,omitempty"`
	Title       string `json:"title"`
	ReleasesURL string `json:"releases_url"`
	Phase       Phase  `json:"phase"`
	// Version is what Phase is about: the version downloading, or active.
	Version string `json:"version,omitempty"`
	Done    int64  `json:"done,omitempty"`
	Total   int64  `json:"total,omitempty"`
	// Detail is one sentence for an unsupported platform or a failure.
	Detail   string         `json:"detail,omitempty"`
	Active   *InstallRecord `json:"active,omitempty"`
	Previous *InstallRef    `json:"previous,omitempty"`
	// Offer is a newer eligible release nobody has answered for.
	Offer *Candidate `json:"offer,omitempty"`
	// Pending is a chosen update not yet active.
	Pending *Pending        `json:"pending,omitempty"`
	Check   CheckView       `json:"check"`
	Newer   []Verdict       `json:"newer,omitempty"`
	Skipped []BytesIdentity `json:"skipped,omitempty"`
	// Integrity is a version seen with other bytes than this program knew.
	Integrity *IntegrityEvent `json:"integrity,omitempty"`
	Failure   *SetupFailure   `json:"failure,omitempty"`
	// Self is the Companion's own row: how it is installed and can be
	// updated, and its last replacement.
	Self *SelfView `json:"self,omitempty"`
}

// Pending is a chosen update.
type Pending struct {
	Intent  Intent `json:"intent"`
	Version string `json:"version"`
	// Staged is true once it is downloaded and verified.
	Staged bool `json:"staged"`
	// Waiting names the work it waits for (Update now only).
	Waiting  []string `json:"waiting,omitempty"`
	NotesURL string   `json:"notes_url,omitempty"`
	// Restarting is an "Update now" of the Companion that has closed
	// admission and is stopping to be replaced.
	Restarting bool `json:"restarting,omitempty"`
	// Manual is what a person has to do to finish an update the Companion
	// downloaded and verified but does not put in place itself.
	Manual string `json:"manual,omitempty"`
}

// CheckView is the schedule, as the page says it.
type CheckView struct {
	LastAttempt      time.Time `json:"last_attempt,omitempty"`
	LastSuccess      time.Time `json:"last_success,omitempty"`
	NextAttempt      time.Time `json:"next_attempt,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
	RateLimitedUntil time.Time `json:"rate_limited_until,omitempty"`
}

// Status reads the durable state and this process's progress.
func (m *Manager) Status() Status {
	out := Status{Enabled: !m.opts.Disabled, Channel: m.opts.Channel, Target: m.Target(), Root: m.opts.ToolsRoot}
	state, err := LoadState(m.opts.StatePath)
	if err != nil {
		state = newState()
	}
	now := m.now()
	for _, tool := range m.targets() {
		ts := state.Tool(tool.ID)
		live := m.liveOf(tool.ID)
		if tool.Kind == KindApplication {
			out.Tools = append(out.Tools, m.selfStatus(ts, live, now))
			continue
		}
		view := ToolStatus{
			ID: tool.ID, Title: tool.Title, ReleasesURL: tool.ReleasesURL(),
			Previous: ts.Previous, Newer: ts.Check.Newer, Skipped: ts.Skipped,
			Integrity: ts.Integrity, Failure: ts.Failure,
			Check: CheckView{
				LastAttempt: ts.Check.LastAttempt, LastSuccess: ts.Check.LastSuccess,
				NextAttempt: ts.Check.NextAttempt, LastError: ts.Check.LastError,
				RateLimitedUntil: ts.Check.RateLimitedUntil,
			},
		}
		if ts.Active != nil {
			if record, err := ReadInstall(ts.Active.Dir); err == nil {
				view.Active = &record
			}
		}
		best := ts.Check.Best
		switch {
		case m.opts.Disabled && view.Active == nil:
			view.Phase = PhaseDisabled
		case live.Phase == "downloading" || live.Phase == "installed":
			view.Phase, view.Version, view.Done, view.Total = PhaseDownloading, live.Version, live.Done, live.Total
		case live.Phase == "verifying":
			view.Phase, view.Version, view.Done, view.Total = PhaseVerifying, live.Version, live.Done, live.Total
		case view.Active != nil:
			view.Phase, view.Version = PhaseReady, view.Active.Version
		case live.Phase == "checking":
			view.Phase = PhaseChecking
		case ts.Failure != nil:
			view.Phase, view.Version, view.Detail = PhaseFailed, ts.Failure.Version, ts.Failure.Error
		case ts.Check.LastSuccess.IsZero():
			view.Phase = PhaseNotChecked
			if ts.Check.LastError != "" {
				view.Phase, view.Detail = PhaseFailed, ts.Check.LastError
			}
		case best == nil:
			view.Phase = PhaseUnsupported
			view.Detail = "no release of " + tool.Title + " can be installed on this computer (" + m.Target() + ")"
			if len(ts.Check.Newer) > 0 {
				view.Detail += ": the newest " + ts.Check.Newer[0].Tag + " — " + ts.Check.Newer[0].Reason.Describe()
			}
		case ts.Integrity != nil:
			view.Phase, view.Detail = PhaseFailed, integritySentence(ts.Integrity)
		default:
			view.Phase = PhaseNotChecked
		}
		if view.Active == nil && view.Phase != PhaseDisabled {
			out.SettingUp = true
		}
		if view.Active != nil && best != nil && m.newer(tool, best.Version, view.Active.Version) {
			identity := best.Identity()
			switch {
			case ts.Intent != IntentNone && ts.IntentFor != nil && *ts.IntentFor == identity:
				_, staged := ts.installed(identity)
				view.Pending = &Pending{Intent: ts.Intent, Version: best.Version, Staged: staged, NotesURL: best.NotesURL}
				if ts.Intent == IntentNow {
					view.Pending.Waiting = live.Waiting
				}
			case ts.Integrity != nil && ts.Integrity.Seen == identity:
			case ts.skipped(identity):
			case ts.Dismissed != nil && ts.Dismissed.BytesIdentity == identity &&
				ts.Dismissed.Session == m.session && now.Sub(ts.Dismissed.At) < dismissalWindow:
			default:
				copied := *best
				view.Offer = &copied
			}
		}
		out.Tools = append(out.Tools, view)
	}
	m.mu.Lock()
	out.Notices = append([]Notice(nil), m.notice...)
	m.mu.Unlock()
	return out
}

func integritySentence(event *IntegrityEvent) string {
	return "release " + event.Seen.Version + " was published again with different bytes than this Companion " +
		"already knows for it; it is not installed. Check the release page, and install it by hand if you trust it"
}
