package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/compat"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Asking a profile's programs what they are (NEW_323D).
//
// The page never decides compatibility and never starts anything: it asks the
// executor to ask ([job.Service.CheckCompatibility]) at the three moments a
// program can have become a different one — when a path is recorded, when the
// profile is approved, and when a person presses "Check these programs" — and
// once when the Companion starts, for bindings recorded before this existed or
// changed while it was not running. A page poll reads what was recorded.

// checkPrograms asks, and records the answers in the binding file this server
// reads. A profile that requires nothing of its programs is a no-op. again is
// "Check these programs": ask every one now. Otherwise only a program with no
// answer for its exact bytes is started.
func (s *Server) checkPrograms(profileID string, again bool) error {
	jobs, err := s.requireJobsService()
	if err != nil {
		return err
	}
	facts, err := jobs.CheckCompatibility(profileID, again)
	if len(facts) == 0 {
		return err
	}
	path, pathErr := s.bindingsPath()
	if pathErr != nil {
		return pathErr
	}
	keep := make(map[string]binding.ExecutableFact, len(facts))
	for name, fact := range facts {
		if fact.SHA256 != "" {
			keep[name] = fact
		}
	}
	if recordErr := binding.RecordFacts(path, profileID, keep); recordErr != nil {
		return recordErr
	}
	return err
}

// handleProfileCheck is "Check these programs": ask again, now, and answer
// with the profile as it then stands. It changes no path and no approval.
func (s *Server) handleProfileCheck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	entry, _, err := s.profileEntry(id)
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	if len(compat.ProbedExecutables(entry.Profile)) == 0 {
		writeError(w, http.StatusBadRequest, errors.New(
			entry.Profile.Metadata().Name+" requires nothing of its programs, so there is nothing to check"))
		return
	}
	key := checkingPrograms + "/" + id
	if !s.board.begin(key, checkingPrograms, entry.Profile.Metadata().Name) {
		writeError(w, http.StatusConflict, errors.New("these programs are being checked already; the page shows the answer when it comes"))
		return
	}
	err = s.checkPrograms(id, true)
	s.board.end(key, err, false)
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	_, local, err := s.profileEntry(id)
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, s.describeCatalogEntry(entry, local))
}

// describeCompatibility is one row per program the profile holds to its
// probe: where it is, what it said it is, and whether that answer is still
// about the file that is there. Nil for a profile that requires nothing.
func describeCompatibility(entry job.CatalogEntry, local binding.LocalBinding) []map[string]any {
	tool, isTool := entry.Profile.(*profile.ToolProfile)
	if !isTool || tool.CompatibilityProbe == nil {
		return nil
	}
	asked := compat.ProbedExecutables(entry.Profile)
	rows := make([]map[string]any, 0, len(asked))
	for _, name := range asked {
		probed, _ := tool.CompatibilityProbe.For(name)
		row := map[string]any{
			"executable": probed.Name,
			"accepts":    tool.CompatibilityProbe.AcceptText(),
			"mentions":   probed.Mentions,
			"state":      "not_bound",
		}
		path := local.Executables[probed.Name]
		if path != "" {
			row["path"] = path
			row["state"] = "not_checked"
			if identity, err := compat.Stat(path); err == nil {
				fact, recorded := local.Compatibility[probed.Name]
				if recorded && fact.Describes(path, identity.Size, identity.ModifiedAt, tool.CompatibilityProbe.Requirement(probed.Name)) {
					row["state"] = fact.State
					row["identity"] = fact.Identity
					row["detail"] = fact.Detail
					row["missing"] = fact.Missing
					row["sha256"] = fact.SHA256
					row["checked_at"] = fact.CheckedAt
					row["job_id"] = fact.JobID
				}
			} else {
				row["state"] = binding.FactMissing
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// RefreshCompatibility asks every approved, bound profile whose recorded
// answers no longer describe its programs. Called once when the Companion
// starts, in the background: it is what makes a binding recorded by an older
// build, or a compiler replaced while the Companion was closed, read as what
// it is rather than as "not checked" until somebody opens Profiles. The page
// shows it while it runs, and its failure with Retry (NEW_368); a second
// call while one runs does nothing.
func (s *Server) RefreshCompatibility() {
	if s.board.begin(checkingPrograms, checkingPrograms, "") {
		s.refreshPrograms()
	}
}

// startRefresh is RefreshCompatibility in the background, recorded as begun
// before it returns.
func (s *Server) startRefresh() {
	if s.board.begin(checkingPrograms, checkingPrograms, "") {
		go s.refreshPrograms()
	}
}

func (s *Server) refreshPrograms() {
	var failures []string
	defer func() {
		var err error
		if len(failures) > 0 {
			err = errors.New(strings.Join(failures, "; "))
		}
		s.board.end(checkingPrograms, err, true)
	}()
	catalog, err := s.catalog()
	if err != nil {
		failures = append(failures, err.Error())
		return
	}
	entries, err := catalog.List()
	if err != nil {
		failures = append(failures, err.Error())
		return
	}
	set, _, err := s.bindings()
	if err != nil || set == nil {
		if err != nil {
			failures = append(failures, err.Error())
		}
		return
	}
	for _, entry := range entries {
		id := entry.Profile.Metadata().ID
		local, bound := set.Find(id)
		if !bound || profile.Authorize(entry.Profile, entry.Trust, entry.Digest, local.Grant) != nil {
			continue
		}
		stale := false
		for _, row := range describeCompatibility(entry, local) {
			stale = stale || row["state"] == "not_checked"
		}
		if !stale {
			continue
		}
		s.board.detail(checkingPrograms, entry.Profile.Metadata().Name)
		if err := s.checkPrograms(id, false); err != nil {
			s.logf("compatibility: checking %s: %v", id, err)
			failures = append(failures, entry.Profile.Metadata().Name+": "+err.Error())
		}
	}
}
