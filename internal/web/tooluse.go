package web

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/selfupdate"
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

// BundleRegistry is the coordination of the bundle self runs from, in the
// bundle's own transaction folder: processes with different tools folders can
// run from one bundle, and that folder is what they share (02_NEW_368). It is
// nil for a bundle no Companion replaces.
func BundleRegistry(self managedtools.SelfInstall) (*tooluse.Registry, error) {
	if self.Mode != managedtools.SelfPortable {
		return nil, nil
	}
	return tooluse.Open(filepath.Join(self.Root, selfupdate.DirName))
}

// ToolLeaser is [build.Options.Lease] over registry and, when the bundle is
// one that gets replaced, over bundle too.
func ToolLeaser(registry, bundle *tooluse.Registry, program string, wait time.Duration) func([]string) (build.ToolLease, error) {
	return func(profileIDs []string) (build.ToolLease, error) {
		lease, err := registry.Use(ToolsFor(profileIDs), program, wait)
		if err != nil {
			return nil, err
		}
		if bundle == nil {
			return lease, nil
		}
		held, err := bundle.Use([]string{BundleTool}, program, wait)
		if err != nil {
			lease.Release()
			return nil, err
		}
		return bothLeases{Lease: lease, bundle: held}, nil
	}
}

// bothLeases is a pipeline's hold on its tools folder and on its bundle.
type bothLeases struct {
	*tooluse.Lease
	bundle *tooluse.Lease
}

// AddStore records the job store in both: either may be read after this
// process died.
func (l bothLeases) AddStore(store string) error {
	if err := l.Lease.AddStore(store); err != nil {
		return err
	}
	return l.bundle.AddStore(store)
}

func (l bothLeases) Release() {
	l.bundle.Release()
	l.Lease.Release()
}

// SelfExclusion is the switch gates a replacement of the Companion's bundle
// holds, from its last look at what is running until it can no longer put
// the old bundle back: no pipeline starts in that time, in any process that
// shares the tools folder or the bundle.
type SelfExclusion struct {
	gates []*tooluse.Gate
}

// Held reports whether every gate is still this process's own.
func (e *SelfExclusion) Held() bool {
	if e == nil {
		return false
	}
	for _, gate := range e.gates {
		if !gate.Held() {
			return false
		}
	}
	return true
}

// Release gives the gates up. It may be called on nil.
func (e *SelfExclusion) Release() {
	if e == nil {
		return
	}
	for _, gate := range e.gates {
		gate.Release()
	}
}

// TakeSelfExclusion takes the bundle's switch gates and reads what they wait
// for. With an error nothing is held and nothing is known, and the update
// waits. With work listed the gates are held: the caller releases them.
func (s *Server) TakeSelfExclusion(version string) (*SelfExclusion, []tooluse.Use, error) {
	registry, err := s.toolRegistry()
	if err != nil {
		return nil, nil, fmt.Errorf("the tools folder cannot be used to see what is running: %w", err)
	}
	if s.bundleUseErr != nil {
		return nil, nil, fmt.Errorf("the Companion's folder cannot be used to see what is running: %w", s.bundleUseErr)
	}
	registries := []*tooluse.Registry{registry}
	if s.bundleUse != nil {
		registries = append(registries, s.bundleUse)
	}
	exclusion := &SelfExclusion{}
	var users []tooluse.Use
	seen := map[string]bool{}
	for _, each := range registries {
		gate, err := each.Switch(BundleTool, "the Companion replacing itself with "+version)
		if err != nil {
			exclusion.Release()
			return nil, nil, err
		}
		exclusion.gates = append(exclusion.gates, gate)
		holders, err := gate.Holders()
		if err != nil {
			exclusion.Release()
			return nil, nil, err
		}
		for _, use := range holders {
			if !seen[use.Label] {
				seen[use.Label] = true
				users = append(users, use)
			}
		}
	}
	return exclusion, users, nil
}

// SetToolRegistry fixes the tools folder's coordination, before the server
// serves. Tests give each server its own.
func (s *Server) SetToolRegistry(registry *tooluse.Registry) {
	s.toolUseOnce.Do(func() { s.toolUse = registry })
}

// SetBundle fixes the bundle this process runs from, before the server
// serves. A folder that cannot be used refuses builds and updates alike.
func (s *Server) SetBundle(self managedtools.SelfInstall) {
	s.bundleUse, s.bundleUseErr = BundleRegistry(self)
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
	if err := registry.Switching(s.pipelineTools(pipelineID)); err != nil {
		return err
	}
	if s.bundleUse != nil {
		return s.bundleUse.Switching([]string{BundleTool})
	}
	return nil
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
