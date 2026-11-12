package managedtools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// One person's "Update" (NEW_508C): every official target this Companion
// manages — the two toolchains and the Companion itself — checked now and
// brought to the newest verified release, through the same coordinator, the
// same verification and the same switch rules an offer goes through. It is
// the explicit counterpart of an offer: an offer waits for an answer, this IS
// the answer, for everything at once.
//
// HITL, 2026-11-12: official installs and updates take the newest available
// verified official release for this platform and channel. Nothing here
// selects an older one, and nothing here weakens a check: a release that is
// not eligible is said to be unavailable, with its reason.

// UpdateAction is what one Update did, or found, for one target.
type UpdateAction string

const (
	// UpdateCurrent: the newest verified release is the one in use.
	UpdateCurrent UpdateAction = "current"
	// UpdateInstalling: not installed; the newest verified release is being
	// installed now.
	UpdateInstalling UpdateAction = "installing"
	// UpdateUpdating: a newer verified release is being installed, and is
	// switched to once no work uses the version it replaces.
	UpdateUpdating UpdateAction = "updating"
	// UpdateRepairing: the installation in use cannot be built with — gone,
	// damaged, refused by its profile, or outside this Companion's tools
	// folder — and the newest verified release replaces it.
	UpdateRepairing UpdateAction = "repairing"
	// UpdateManual: downloaded and verified, and a person puts it in place
	// (a Companion that cannot replace its own files).
	UpdateManual UpdateAction = "manual"
	// UpdateNotAutomatic: this copy is never replaced by itself (a
	// development build); the release is named, not installed.
	UpdateNotAutomatic UpdateAction = "not_automatic"
	// UpdateUnavailable: no release can be installed here, with the reason.
	UpdateUnavailable UpdateAction = "unavailable"
	// UpdateFailed: the check failed, or the release is refused for its bytes.
	UpdateFailed UpdateAction = "failed"
)

// UpdateStep is one target's part of an Update.
type UpdateStep struct {
	Tool   ToolID       `json:"tool"`
	Kind   Kind         `json:"kind,omitempty"`
	Title  string       `json:"title"`
	Action UpdateAction `json:"action"`
	From   string       `json:"from,omitempty"`
	To     string       `json:"to,omitempty"`
	Detail string       `json:"detail,omitempty"`
	// Checked is whether this Update read the release list (false: GitHub
	// asked to wait, or the list could not be read; the last answer is used
	// when there is one).
	Checked bool `json:"checked"`
}

// UpdateAll checks every target now and starts whatever brings each one to
// its newest verified release. It returns at once with what it started; the
// installs and the Companion's own restart happen in the coordinator, in
// order: toolchains first, the Companion's restart only once no toolchain is
// installing or switching (see [Manager.toolWork]).
//
// A version a person asked never to be told about again is still installed
// by an explicit Update: the suppression is about notices, and this is not
// one. An integrity event (a version republished with other bytes) is never
// installed by anything.
func (m *Manager) UpdateAll(ctx context.Context) ([]UpdateStep, error) {
	if m.opts.Disabled {
		return nil, errors.New("managedtools: automatic build-tool setup is turned off in this Companion")
	}
	checked := map[ToolID]bool{}
	checkErr := map[ToolID]string{}
	for _, tool := range m.targets() {
		outcome, err := m.check(ctx, tool, true)
		switch {
		case err == nil:
			checked[tool.ID] = true
		case errors.Is(err, errNotDue):
			// Checked less than a minute ago, or GitHub asked to wait: the
			// last answer stands, and the step says so when there is none.
			checkErr[tool.ID] = outcome.Message
		default:
			checkErr[tool.ID] = err.Error()
		}
	}
	// Reconcile first, so a deleted or damaged installation is seen as one
	// before anything is decided about it.
	for _, tool := range Tools {
		state, err := LoadState(m.opts.StatePath)
		if err != nil {
			return nil, err
		}
		m.workMu.Lock()
		err = m.reconcile(tool, state.Tool(tool.ID))
		m.workMu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	var steps []UpdateStep
	now := m.now()
	_, err := UpdateState(m.opts.StatePath, func(state *State) error {
		steps = steps[:0]
		for _, tool := range m.targets() {
			ts := state.Tool(tool.ID)
			step := UpdateStep{Tool: tool.ID, Kind: tool.Kind, Title: tool.Title, Checked: checked[tool.ID]}
			if tool.Kind == KindApplication {
				step = m.planSelf(step, ts)
			} else {
				step = m.planTool(tool, step, ts, now)
			}
			if !step.Checked && step.Detail == "" && checkErr[tool.ID] != "" &&
				(step.Action == UpdateUnavailable || step.Action == UpdateFailed) {
				step.Detail = checkErr[tool.ID]
			}
			steps = append(steps, step)
		}
		return nil
	})
	m.Wake()
	return steps, err
}

// planTool decides one toolchain's step and records the choice in ts.
func (m *Manager) planTool(tool Tool, step UpdateStep, ts *ToolState, now time.Time) UpdateStep {
	best := ts.Check.Best
	if ts.Active != nil {
		step.From = ts.Active.Version
	}
	switch {
	case best == nil:
		step.Action = UpdateUnavailable
		if ts.Check.LastError != "" {
			step.Action, step.Detail = UpdateFailed, ts.Check.LastError
		} else if len(ts.Check.Newer) > 0 {
			step.Detail = ts.Check.Newer[0].Tag + ": " + ts.Check.Newer[0].Reason.Describe()
			if ts.Check.Newer[0].Detail != "" {
				step.Detail += " (" + ts.Check.Newer[0].Detail + ")"
			}
		} else if !ts.Check.LastSuccess.IsZero() {
			step.Detail = "no release of " + tool.Title + " can be installed on this computer (" + m.Target() + ")"
		}
		return step
	case ts.Integrity != nil && ts.Integrity.Seen == best.Identity():
		step.Action, step.To, step.Detail = UpdateFailed, best.Version, integritySentence(ts.Integrity)
		return step
	}
	step.To = best.Version
	retry := func() {
		if ts.Failure != nil {
			ts.Failure.NextAttempt = now
		}
	}
	switch {
	case ts.Active == nil:
		ts.InstallApproved = true
		retry()
		step.Action = UpdateInstalling
	case m.repairReason(tool, ts) != "":
		step.Detail = m.repairReason(tool, ts)
		ts.InstallApproved = true
		retry()
		step.Action = UpdateRepairing
	case m.newer(tool, best.Version, ts.Active.Version):
		identity := best.Identity()
		ts.Intent, ts.IntentFor, ts.Dismissed = IntentNow, &identity, nil
		retry()
		step.Action = UpdateUpdating
	default:
		step.Action, step.To = UpdateCurrent, ts.Active.Version
	}
	return step
}

// planSelf decides the Companion's own step.
func (m *Manager) planSelf(step UpdateStep, ts *ToolState) UpdateStep {
	self := m.opts.Self
	tool := m.selfTool()
	step.From = self.Version
	best := ts.Check.Best
	switch {
	case best == nil:
		step.Action = UpdateUnavailable
		if ts.Check.LastError != "" {
			step.Action, step.Detail = UpdateFailed, ts.Check.LastError
		} else if len(ts.Check.Newer) > 0 {
			step.Detail = ts.Check.Newer[0].Tag + ": " + ts.Check.Newer[0].Reason.Describe()
		}
		if best == nil && step.Detail == "" && !ts.Check.LastSuccess.IsZero() {
			step.Action, step.To = UpdateCurrent, self.Version
		}
		return step
	case !m.newer(tool, best.Version, self.Version):
		step.Action, step.To = UpdateCurrent, self.Version
		return step
	case ts.Integrity != nil && ts.Integrity.Seen == best.Identity():
		step.Action, step.To, step.Detail = UpdateFailed, best.Version, integritySentence(ts.Integrity)
		return step
	case self.Mode == SelfDevelopment:
		step.Action, step.To, step.Detail = UpdateNotAutomatic, best.Version, self.Reason
		return step
	}
	identity := best.Identity()
	ts.Intent, ts.IntentFor, ts.Dismissed = IntentNow, &identity, nil
	if ts.Failure != nil {
		ts.Failure.NextAttempt = m.now()
	}
	step.To = best.Version
	step.Action = UpdateUpdating
	if self.Mode == SelfManual {
		step.Action, step.Detail = UpdateManual, self.Reason
	}
	return step
}

// repairReason says why the active installation of tool cannot stay the one
// in use, or "" when it can. The newest verified release replaces it, through
// the ordinary install and the ordinary switch (which waits for work):
//
//   - its built-in profile refuses the version it prints, or does not accept
//     it — a release a newer Companion names as not to be built with (NEW_507,
//     #34) is not left bound to every Quake 1 and Quake II build;
//   - it is older than the oldest release this Companion was qualified with;
//   - it lives outside this Companion's own tools folder, when that folder is
//     the implicit one beside a portable bundle: a per-user folder an earlier
//     copy used is not silently the place this copy builds from (NEW_508C).
//
// A deliberate folder (AUCOM_TOOLS_DIR, managed_tools_dir) is never "outside".
func (m *Manager) repairReason(tool Tool, ts *ToolState) string {
	if ts.Active == nil || tool.Kind == KindApplication {
		return ""
	}
	if m.opts.ImplicitRoot && !within(m.installer.toolDir(tool), ts.Active.Dir) {
		return fmt.Sprintf("the installation in use is in %s, outside this Companion's tools folder %s",
			filepath.Dir(ts.Active.Dir), m.opts.ToolsRoot)
	}
	if baseline, err := tool.Version(tool.Baseline); err == nil {
		if active, err := tool.Version(ts.Active.Version); err == nil && active.Compare(baseline) < 0 {
			return fmt.Sprintf("%s %s is older than %s, the oldest release this Companion was qualified with",
				tool.Title, ts.Active.Version, tool.Baseline)
		}
	}
	if probe := m.opts.Probes[tool.ID]; probe != nil {
		record, err := ReadInstall(ts.Active.Dir)
		if err != nil || record.Banner == "" {
			return ""
		}
		if refused, isRefused := probe.Refused(record.Banner); isRefused {
			return fmt.Sprintf("its built-in profile refuses %s: %s", record.Banner, refused.Reason)
		}
		if !probe.Accepts(record.Banner) {
			return fmt.Sprintf("its built-in profile does not accept %s (%s)", record.Banner, probe.AcceptText())
		}
	}
	return ""
}

// installedIn is the complete installation of these exact bytes inside dir.
func (t *ToolState) installedIn(identity BytesIdentity, dir string) (InstallRef, bool) {
	for _, installed := range t.Installed {
		if installed.Identity() == identity && within(dir, installed.Dir) {
			return installed, true
		}
	}
	return InstallRef{}, false
}

// RootProblem is why dir cannot be written, as [Status.RootError] says it.
func RootProblem(dir string) string { return rootProblem(dir) }

// rootProblem is why dir cannot be written, as a sentence with its remedy, or
// "". It creates nothing: the nearest folder that exists is the one asked.
func rootProblem(dir string) string {
	probe := dir
	for {
		info, err := os.Stat(probe)
		if err == nil {
			if !info.IsDir() {
				return fmt.Sprintf("the Companion's tools folder %s cannot be made: %s is not a folder. "+
					"Move the Companion's folder somewhere you can write, or choose a tools folder in Settings", dir, probe)
			}
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return ""
		}
		probe = parent
	}
	file, err := os.CreateTemp(probe, ".aucom-write-check-*")
	if err != nil {
		return fmt.Sprintf("the Companion cannot write its tools folder %s (%v). Move the Companion's folder somewhere "+
			"you can write — your Documents or Desktop — or choose a tools folder in Settings", dir, err)
	}
	name := file.Name()
	file.Close()
	_ = os.Remove(name)
	return ""
}
