package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/tooluse"
)

// The cross-process half of the managed toolchains' protection (01_NEW_368
// §2): every pipeline of every Companion process takes a use lease in the
// tools folder, and a version switch — a toolchain's, or the Companion's own
// bundle replacement — takes that folder's switch gate. internal/tooluse is
// the owner; this is how the server and the CLI take part.

// codeToolsUpdating is a build refused because a toolchain it needs is
// switching version: retryable, nothing was started or consumed.
const codeToolsUpdating = "tools_updating"

// BundleTool is the lease key every pipeline holds besides its toolchains':
// the Companion's own bundle, which a self-update switches.
const BundleTool = string(managedtools.Companion)

// leaseWait bounds how long a pipeline that got past the pre-check waits for
// a switch that started in between.
const leaseWait = 30 * time.Second

// ToolsFor is the lease keys of a set of tool profiles: the bundle, and the
// managed toolchain of each profile that has one.
func ToolsFor(profileIDs []string) []string {
	tools := []string{BundleTool}
	for _, id := range profileIDs {
		if tool, ok := managedtools.ForProfile(id); ok {
			tools = append(tools, string(tool.ID))
		}
	}
	return tools
}

// ToolLeaser is [build.Options.Lease] over registry.
func ToolLeaser(registry *tooluse.Registry, program string, wait time.Duration) func([]string) (build.ToolLease, error) {
	return func(profileIDs []string) (build.ToolLease, error) {
		lease, err := registry.Use(ToolsFor(profileIDs), program, wait)
		if err != nil {
			return nil, err
		}
		return lease, nil
	}
}

// SetToolRegistry fixes the tools folder's coordination, before the server
// serves. Tests give each server its own.
func (s *Server) SetToolRegistry(registry *tooluse.Registry) {
	s.toolUseOnce.Do(func() { s.toolUse = registry })
}

// toolRegistry is the coordination of this configuration's tools folder.
func (s *Server) toolRegistry() (*tooluse.Registry, error) {
	s.toolUseOnce.Do(func() {
		s.mu.RLock()
		root, err := s.settings.Tools()
		s.mu.RUnlock()
		if err != nil {
			s.toolUseErr = err
			return
		}
		s.toolUse, s.toolUseErr = tooluse.Open(root)
	})
	return s.toolUse, s.toolUseErr
}

// pipelineTools is the lease keys of a pipeline's stages; every managed
// toolchain when the pipeline is not known yet.
func (s *Server) pipelineTools(pipelineID string) []string {
	all := []string{BundleTool}
	for _, tool := range managedtools.Tools {
		all = append(all, string(tool.ID))
	}
	if pipelineID == "" {
		return all
	}
	runner, err := s.buildRunner(nil)
	if err != nil {
		return all
	}
	catalog, err := s.catalog()
	if err != nil {
		return all
	}
	entry, err := catalog.Lookup(pipelineID)
	if err != nil {
		return all
	}
	pipeline, ok := entry.Profile.(*profile.PipelineProfile)
	if !ok {
		return all
	}
	steps, err := pipeline.Resolve(runner.Resolver())
	if err != nil {
		return all
	}
	var ids []string
	for _, step := range steps {
		ids = append(ids, step.Profile.Meta.ID)
	}
	return ToolsFor(ids)
}

// toolsSwitching is the pre-check a build start makes before it takes
// anything over: a switch under way refuses the request now, retryably,
// rather than after the request was handed to a run.
func (s *Server) toolsSwitching(pipelineID string) error {
	registry, err := s.toolRegistry()
	if err != nil {
		return nil // the run's own lease reports it
	}
	return registry.Switching(s.pipelineTools(pipelineID))
}

// refuseIfSwitching writes the retryable refusal and reports whether it did.
func (s *Server) refuseIfSwitching(w http.ResponseWriter, pipelineID string) bool {
	err := s.toolsSwitching(pipelineID)
	if err == nil {
		return false
	}
	w.Header().Set("Retry-After", "15")
	writeJSON(w, http.StatusServiceUnavailable, errorBody{Error: err.Error(), Code: codeToolsUpdating})
	return true
}

// isRetryLater is a refusal that started and consumed nothing.
func isRetryLater(err error) bool {
	return errors.Is(err, errUpdating) || errors.Is(err, tooluse.ErrSwitching)
}
