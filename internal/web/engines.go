package web

import (
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/build"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/engine"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// The Run area: an engine you already have, set up and started.
//
// # The document and the binding are two different things, and stay so
//
// An engine profile is portable: it describes an engine, its actions and its
// content layouts, and it contains no path on anybody's machine (internal/
// profile enforces that). A local binding is the opposite — it is nothing but
// this machine's paths, this machine's version probe result, and what this
// user approved. The API keeps them in separate fields of every response for
// the same reason the files are separate: a user has to be able to see which
// half came from the author and which half is theirs, and exporting a profile
// must never carry a home directory with it.
//
// # Nothing here launches anything
//
// A launch is [job.Service.Submit], the same as a compile. This area resolves
// what to submit and reports what would stop it — see [engine.Checker], whose
// answer is the same one the executor will act on because both read the trust
// and the digest out of the catalog entry rather than recomputing them.

func (s *Server) engineAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/engines":        s.handleEngineList,
		"GET /api/v1/engines/detect": s.handleEngineDetect,
		"GET /api/v1/engines/{id}":   s.handleEngineGet,
		// Play this build: its level staged where this engine looks, before Start.
		"POST /api/v1/engines/{id}/stage-build": s.handleEngineStageBuild,
	}
}

func currentPlatform() profile.Platform {
	return profile.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// engineEntries is every engine profile the executor can see, sorted.
//
// The same catalog the executor reads — there is no separate list of engines,
// because a second list is a list that can disagree about which document an id
// names.
func (s *Server) engineEntries() ([]job.CatalogEntry, error) {
	catalog, err := s.catalog()
	if err != nil {
		return nil, err
	}
	entries, err := catalog.List()
	if err != nil {
		return nil, err
	}
	engines := make([]job.CatalogEntry, 0, len(entries))
	for _, entry := range entries {
		if _, isEngine := entry.Profile.(*profile.EngineProfile); isEngine {
			engines = append(engines, entry)
		}
	}
	sort.Slice(engines, func(i, j int) bool {
		return engines[i].Profile.Metadata().ID < engines[j].Profile.Metadata().ID
	})
	return engines, nil
}

func (s *Server) engineEntry(id string) (job.CatalogEntry, error) {
	entries, err := s.engineEntries()
	if err != nil {
		return job.CatalogEntry{}, err
	}
	available := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Profile.Metadata().ID == id {
			return entry, nil
		}
		available = append(available, entry.Profile.Metadata().ID)
	}
	return job.CatalogEntry{}, fmt.Errorf("%w: no engine profile has the id %q (this machine has: %v)",
		job.ErrNoProfile, id, available)
}

// describeEngine is one engine as the Run area shows it: the portable document,
// this machine's binding, and what is still missing before it can start.
func (s *Server) describeEngine(entry job.CatalogEntry, local binding.LocalBinding, actionID string) map[string]any {
	document := entry.Profile.(*profile.EngineProfile)
	support, note := document.SupportFor(currentPlatform())

	body := describeProfile(entry)
	body["runtime"] = document.Runtime
	body["engine_version"] = document.EngineVersion
	body["last_qualified"] = document.LastQualified
	body["game_profile"] = document.GameProfile
	body["content_layouts"] = document.ContentLayouts
	body["executables"] = document.Executables
	body["platform_support"] = map[string]any{
		"platform": currentPlatform(), "status": support, "note": note,
	}
	body["binding"] = describeBinding(local)

	checker := engine.Checker{Platform: currentPlatform()}
	// Reported per action, because an engine that cannot host a dedicated
	// server can still play a map, and a single "ready" flag would hide which
	// of the two the user is actually being stopped from doing.
	perAction := make(map[string]any, len(document.Actions))
	ready := false
	for _, action := range document.Actions {
		problems := checker.Check(document, entry.Trust, entry.Digest, local, action.ID)
		perAction[action.ID] = describeProblems(problems)
		if len(problems) == 0 {
			ready = true
		}
	}
	body["action_problems"] = perAction
	body["ready"] = ready
	if actionID != "" {
		body["problems"] = describeProblems(
			checker.Check(document, entry.Trust, entry.Digest, local, actionID))
	}
	return body
}

// describeBinding reports the local half, or nil when there is none.
//
// nil rather than an empty object: "this machine has nothing recorded for this
// engine" is a state the page draws differently from "it is recorded and
// incomplete", and an empty object would make the two look alike.
func describeBinding(local binding.LocalBinding) map[string]any {
	if local.ProfileID == "" {
		return nil
	}
	granted := local.Grant != nil
	return map[string]any{
		"profile_id":          local.ProfileID,
		"profile_version":     local.ProfileVersion,
		"profile_digest":      local.ProfileDigest,
		"trust":               local.Trust,
		"acquisition":         local.Acquisition,
		"executables":         local.Executables,
		"roots":               local.Roots,
		"resolved_version":    local.ResolvedVersion,
		"version_checked_at":  local.VersionCheckedAt,
		"installs":            local.Installs,
		"overrides":           local.Overrides,
		"granted":             granted,
		"granted_at":          grantedAt(local),
		"granted_permissions": grantedPermissions(local),
		"updated_at":          local.UpdatedAt,
	}
}

func grantedAt(local binding.LocalBinding) any {
	if local.Grant == nil {
		return nil
	}
	return local.Grant.GrantedAt
}

// grantedPermissions is what was approved, as opposed to what the document now
// asks for. The page shows both: a grant that no longer covers the document is
// the thing the user has to be shown, not a boolean that quietly went false.
func grantedPermissions(local binding.LocalBinding) []string {
	if local.Grant == nil {
		return nil
	}
	return local.Grant.Granted
}

func describeProblems(problems engine.Problems) []map[string]any {
	items := make([]map[string]any, 0, len(problems))
	for _, problem := range problems {
		items = append(items, map[string]any{
			"fault": problem.Fault, "summary": problem.Summary, "fix": problem.Fix,
		})
	}
	return items
}

func (s *Server) handleEngineList(w http.ResponseWriter, r *http.Request) {
	entries, err := s.engineEntries()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	set, _, err := s.bindings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	action := r.URL.Query().Get("action")
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		local, _ := set.Find(entry.Profile.Metadata().ID)
		items = append(items, s.describeEngine(entry, local, action))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "platform": currentPlatform()})
}

func (s *Server) handleEngineGet(w http.ResponseWriter, r *http.Request) {
	entry, err := s.engineEntry(r.PathValue("id"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	set, _, err := s.bindings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	local, _ := set.Find(entry.Profile.Metadata().ID)
	writeJSON(w, http.StatusOK, s.describeEngine(entry, local, r.URL.Query().Get("action")))
}

// handleEngineDetect proposes game directories. It writes nothing.
//
// Discovery proposes and never decides — see internal/engine. What comes back
// is a list a person confirms, and the evidence that produced each one, so they
// can check the suggestion instead of trusting it.
func (s *Server) handleEngineDetect(w http.ResponseWriter, r *http.Request) {
	scanner := s.scanner
	if near := r.URL.Query().Get("near"); near != "" {
		// A directory the user named, normally the one they just picked an
		// engine executable out of, which is where a hand-installed Quake
		// usually is. Checked first: a value from a query string becomes a
		// path here and nowhere else.
		resolved, err := checkDirectory(near)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		scanner.Extra = append(append([]string(nil), scanner.Extra...), resolved)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": scanner.Detect()})
}

// handleEngineStageBuild stages a finished build's level into this engine's
// game directory as `<mod>/maps/<map>.bsp`, so the Run area can start it with
// `-game <mod> +map <map>`. It writes only through engine.LevelStaging, whose
// record `engine unstage` reads, and it refuses a directory the Companion did
// not stage (NEW_244D: playing a build needed a hand-made maps/ folder and the
// CLI).
func (s *Server) handleEngineStageBuild(w http.ResponseWriter, r *http.Request) {
	var request struct {
		BuildID string `json:"build_id"`
		Mod     string `json:"mod"`
		Map     string `json:"map"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	entry, err := s.engineEntry(r.PathValue("id"))
	if err != nil {
		writeError(w, jobStatus(err), err)
		return
	}
	set, _, err := s.bindings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	id := entry.Profile.Metadata().ID
	local, _ := set.Find(id)
	gameRoot := local.Roots["game_root"]
	if gameRoot == "" {
		writeError(w, http.StatusBadRequest, errors.New(
			"this engine has no game directory set yet: choose it under “Set up this engine on this machine” and press Save setup"))
		return
	}
	dir, err := s.buildsDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	manifest, err := build.Find(dir, request.BuildID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	s.reconcileBuild(manifest)
	level, err := build.PlayableLevel(manifest)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	mod := strings.TrimSpace(request.Mod)
	if mod == "" {
		mod = DefaultPlayMod
	}
	mapName := strings.TrimSpace(request.Map)
	if mapName == "" {
		mapName = level.MapName
	}
	staged, err := engine.LevelStaging{
		GameRoot: gameRoot, ModName: mod, MapName: mapName,
		BSP: level.BSP, Lit: level.Lit, ProfileID: id,
	}.Stage()
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, engine.ErrOccupied) {
			status = http.StatusConflict
		}
		writeError(w, status, err)
		return
	}
	files := make([]string, 0, len(staged.Stamp.Files))
	for _, file := range staged.Stamp.Files {
		files = append(files, file.Path)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mod": mod, "map": mapName, "files": files, "overwrote": staged.Overwrote,
		"label": manifest.Label, "pipeline": manifest.Pipeline.Name,
	})
}

// DefaultPlayMod is the game directory a build is staged into when nobody named
// one. One directory for "what I am trying right now": staging the next build
// replaces the previous one through the staging record.
const DefaultPlayMod = "auto-pigeon"
