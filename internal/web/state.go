package web

import (
	"errors"
	"path/filepath"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/assetsync"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
)

// Where this run's state lives, resolved once per request rather than held.
//
// Held would be wrong. The profile directory gains a document when somebody
// imports one; the binding file gains an entry when somebody binds an engine;
// the builds directory gains a manifest when a build finishes. Every one of
// those can happen through this API, and a catalog captured at construction
// would be a catalog that stops matching the disk the moment the user does
// anything. Re-reading a directory to list twelve JSON files is not a cost
// worth caching against that.
//
// [Paths] wins over the configuration when it is set, because the CLI resolved
// it knowing what `--config` meant. See its comment.

func (s *Server) profilesDir() (string, error) {
	if s.paths.Profiles != "" {
		return s.paths.Profiles, nil
	}
	return s.config().Profiles()
}

func (s *Server) jobsDir() (string, error) {
	return s.config().Jobs()
}

func (s *Server) bindingsPath() (string, error) {
	if s.paths.Bindings != "" {
		return s.paths.Bindings, nil
	}
	return config.BindingsPath()
}

func (s *Server) buildsDir() (string, error) {
	if s.paths.Builds != "" {
		return s.paths.Builds, nil
	}
	jobs, err := s.jobsDir()
	if err != nil {
		return "", err
	}
	// Beside the job store, which is what the CLI does — a build is a set of
	// jobs plus a manifest, and the two belong in one place.
	return filepath.Join(filepath.Dir(jobs), "builds"), nil
}

func (s *Server) assetCacheDir() (string, error) {
	if s.paths.AssetCache != "" {
		return s.paths.AssetCache, nil
	}
	return s.config().AssetCache()
}

// catalog is the profile catalog, read fresh.
//
// The EXECUTOR's catalog whenever there is one, not a second one built here.
// The service's is a chain — the documents on disk, then the engine profiles
// generated from this machine's launch configs — and a page that listed only
// the first would be a page offering to run a profile the executor has, and
// hiding one it also has. A build with no job service falls back to the
// documents, which is all there is to list.
func (s *Server) catalog() (job.Catalog, error) {
	if s.jobs != nil {
		return s.jobs.Catalog(), nil
	}
	dir, err := s.profilesDir()
	if err != nil {
		return nil, err
	}
	return job.NewCatalog(dir), nil
}

// bindings is the local binding set, read fresh. A machine with no binding file
// yet gets an empty set rather than an error: that is a first run.
func (s *Server) bindings() (*binding.Set, string, error) {
	path, err := s.bindingsPath()
	if err != nil {
		return nil, "", err
	}
	set, err := binding.LoadFile(path)
	if err != nil && !errors.Is(err, binding.ErrNoFile) {
		return nil, path, err
	}
	return set, path, nil
}

// assets opens the local asset cache.
func (s *Server) assets() (*assetsync.Store, error) {
	dir, err := s.assetCacheDir()
	if err != nil {
		return nil, err
	}
	return assetsync.Open(dir)
}
