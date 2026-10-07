package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakadapter"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// The pipeline a leak test runs is the user's choice, per game (NEW_310, HITL
// 2026-10-06).
//
// The Companion ships profiles, and the user installs every program by hand:
// which compiler answers "does this map leak" on this computer is theirs to
// say. So nothing is pinned out of the box. When the editor's request for a
// game arrives and that game has no pinned pipeline, the Companion asks —
// a dialog listing the installed pipelines that can answer that game's leak
// test, the unready ones greyed with their reasons and the way to Profiles —
// and the pin is kept in config.json as `leak_test_pipelines`.
//
// "Can answer" is leakadapter's: the pipeline is for that game and publishes,
// by ROLE, the point file a leak leaves and the compiler's own text. Readiness
// is the same pipelineReadiness Build & Run uses. The game is still the saved
// APMap's own word; a pin never decides it.

// leakPipelineDoc is a pipeline document in the form leakadapter reads.
func leakPipelineDoc(id string, pipeline *profile.PipelineProfile) leakadapter.Pipeline {
	doc := leakadapter.Pipeline{ID: id}
	if pipeline.GameProfile != nil {
		doc.Games = append(doc.Games, pipeline.GameProfile.Slug, pipeline.GameProfile.EngineFamily)
	}
	for _, output := range pipeline.Outputs {
		doc.Outputs = append(doc.Outputs, leakadapter.PipelineOutput{Name: output.Name, Role: output.Role, From: output.From})
	}
	return doc
}

// pinnedLeakPipeline is the pipeline pinned for a game, or "".
func (s *Server) pinnedLeakPipeline(game string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings.LeakTestPipelines[game]
}

// leakBinding binds one installed pipeline to a game's leak test.
func (s *Server) leakBinding(game, pipelineID string) (leakadapter.Binding, error) {
	adapter, err := leakadapter.ForProfile(game)
	if err != nil {
		return leakadapter.Binding{}, err
	}
	catalog, err := s.catalog()
	if err != nil {
		return leakadapter.Binding{}, err
	}
	entry, err := catalog.Lookup(pipelineID)
	if err != nil {
		return leakadapter.Binding{}, fmt.Errorf("the pipeline %q is not installed", pipelineID)
	}
	pipeline, ok := entry.Profile.(*profile.PipelineProfile)
	if !ok {
		return leakadapter.Binding{}, fmt.Errorf("%q is not a pipeline", pipelineID)
	}
	binding, err := adapter.Bind(leakPipelineDoc(pipelineID, pipeline))
	if err != nil {
		return leakadapter.Binding{}, fmt.Errorf("%s cannot be the %s leak test: %w", pipeline.Meta.Name, game, err)
	}
	return binding, nil
}

// leakChoices lists every installed pipeline that can answer a game's leak
// test, the game's built-in one first, with the readiness Build & Run shows.
func (s *Server) leakChoices(game string) ([]map[string]any, error) {
	adapter, err := leakadapter.ForProfile(game)
	if err != nil {
		return nil, err
	}
	runner, err := s.buildRunner(nil)
	if err != nil {
		return nil, err
	}
	catalog, err := s.catalog()
	if err != nil {
		return nil, err
	}
	entries, err := catalog.List()
	if err != nil {
		return nil, err
	}
	set, _, err := s.bindings()
	if err != nil {
		return nil, err
	}
	choices := make([]map[string]any, 0)
	for _, entry := range entries {
		pipeline, ok := entry.Profile.(*profile.PipelineProfile)
		if !ok {
			continue
		}
		meta := entry.Profile.Metadata()
		if _, err := adapter.Bind(leakPipelineDoc(meta.ID, pipeline)); err != nil {
			continue
		}
		readiness := pipelineReadiness(pipeline, runner.Resolver(), set)
		choices = append(choices, map[string]any{
			"id": meta.ID, "name": meta.Name, "summary": meta.Summary, "trust": entry.Trust,
			"builtin": meta.ID == adapter.PipelineID, "readiness": readiness.view(),
		})
	}
	sort.SliceStable(choices, func(i, j int) bool {
		return choices[i]["builtin"].(bool) && !choices[j]["builtin"].(bool)
	})
	return choices, nil
}

func (s *Server) leakPipelineAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/leak-test/pipelines":  s.handleLeakPipelines,
		"POST /api/v1/leak-test/pipelines": s.handleLeakPipelinePin,
	}
}

// handleLeakPipelines answers `?game=quake1`: what is pinned, and every
// pipeline that could be.
func (s *Server) handleLeakPipelines(w http.ResponseWriter, r *http.Request) {
	game := strings.TrimSpace(r.URL.Query().Get("game"))
	choices, err := s.leakChoices(game)
	if errors.Is(err, leakadapter.ErrUnsupported) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"game": game, "pinned": s.pinnedLeakPipeline(game), "choices": choices})
}

// handleLeakPipelinePin pins (or, with an empty pipeline, unpins) a game's
// leak-test pipeline. A pipeline that cannot answer that game's leak test is
// refused by name; one that needs setup may be pinned — the dialog greys it,
// and a build of it is refused by the same readiness check every build has.
func (s *Server) handleLeakPipelinePin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Game     string `json:"game"`
		Pipeline string `json:"pipeline"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	body.Game, body.Pipeline = strings.TrimSpace(body.Game), strings.TrimSpace(body.Pipeline)
	if _, err := leakadapter.ForProfile(body.Game); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if body.Pipeline != "" {
		if _, err := s.leakBinding(body.Game, body.Pipeline); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
	}
	updated, err := s.updateConfig(func(current *config.Config) error {
		if body.Pipeline == "" {
			delete(current.LeakTestPipelines, body.Game)
			return nil
		}
		if current.LeakTestPipelines == nil {
			current.LeakTestPipelines = map[string]string{}
		}
		current.LeakTestPipelines[body.Game] = body.Pipeline
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.mu.Lock()
	s.settings.LeakTestPipelines = updated.LeakTestPipelines
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"game": body.Game, "pinned": body.Pipeline})
}

// leakBindingFor is how a finished build is read: its own record when it has
// one (1.4), and otherwise the built-in pipeline it must then have been.
func leakBindingFor(manifest *build.Manifest) (leakadapter.Adapter, leakadapter.Binding, bool) {
	if manifest.LeakTest != nil {
		adapter, err := leakadapter.ForProfile(manifest.LeakTest.Game)
		if err != nil {
			return leakadapter.Adapter{}, leakadapter.Binding{}, false
		}
		return adapter, *manifest.LeakTest, true
	}
	adapter, ok := leakadapter.ForPipeline(manifest.Pipeline.ID)
	if !ok {
		return leakadapter.Adapter{}, leakadapter.Binding{}, false
	}
	return adapter, adapter.Builtin(), true
}

// isLeakPipeline says whether a pipeline is a game's built-in leak test or one
// the user pinned for one: the Build page's `leak_test` flag.
func (s *Server) isLeakPipeline(id string) bool {
	if _, builtin := leakadapter.ForPipeline(id); builtin {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, pinned := range s.settings.LeakTestPipelines {
		if pinned == id {
			return true
		}
	}
	return false
}
