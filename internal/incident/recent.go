package incident

// RecentDepth bounds how many raised incidents the page may be offered to
// report. A Companion that fails a job every second must not grow a list
// without bound; past this the oldest is forgotten.
const RecentDepth = 16

// RecentIncident is what the page may attach to a bug report about an
// incident this process raised (NEW_247H): exactly the typed fields the shared
// bug-report contract's `incident` object accepts, plus the job it came from.
//
// It is the redacted envelope's identifiers and machine names only. The
// message, the user action, the release and the environment are not here:
// the report states its own release and environment, and a sentence is not a
// classification. Nothing here says which area a report is about — the page
// asks the contract (`suggestBugReportArea`), which reads `code` and
// `subsystem` from its one table, and the person can change the answer.
type RecentIncident struct {
	IncidentID    string `json:"incident_id"`
	Code          string `json:"code"`
	Severity      string `json:"severity"`
	Subsystem     string `json:"subsystem,omitempty"`
	Operation     string `json:"operation,omitempty"`
	OccurredAt    string `json:"occurred_at"`
	Recoverable   bool   `json:"recoverable"`
	CorrelationID string `json:"correlation_id,omitempty"`
	// JobID is the failed job an `aucom.job_failed` came from, so the Jobs
	// area can offer to report that job's failure. Empty for anything else.
	JobID string `json:"job_id,omitempty"`
}

// remember keeps a raised, valid incident for Recent. Memory only: a restart
// forgets them, as it forgets the correlation ids they carry.
func (r *Reporter) remember(inc Incident, jobID string) {
	entry := RecentIncident{
		IncidentID:    inc.IncidentID,
		Code:          inc.Code,
		Severity:      inc.Severity,
		Subsystem:     inc.Subsystem,
		Operation:     inc.Operation,
		OccurredAt:    inc.OccurredAt,
		Recoverable:   inc.Recoverable,
		CorrelationID: inc.CorrelationID,
		JobID:         jobID,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recent = append(r.recent, entry)
	if len(r.recent) > RecentDepth {
		r.recent = append([]RecentIncident(nil), r.recent[len(r.recent)-RecentDepth:]...)
	}
}

// Recent is the incidents this process raised, newest first, at most
// RecentDepth of them. Safe on a nil receiver.
func (r *Reporter) Recent() []RecentIncident {
	if r == nil {
		return []RecentIncident{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RecentIncident, 0, len(r.recent))
	for i := len(r.recent) - 1; i >= 0; i-- {
		out = append(out, r.recent[i])
	}
	return out
}
