package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/maturity"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2install"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2run"
)

// Build & Run for a Quake II map (Q2_020): the Installing and Launching stages
// of a packaged run, handed to the package panel's own owners.
//
// internal/q2pack plans and writes the PAK from the finished build, with the
// grants the person gave in the review; internal/q2install puts it in a mod
// folder of its own beside an untouched `baseq2`; internal/q2run starts the
// engine and accepts only its own word that it opened the map from that
// package. Each is the same call the panel's routes make, so a package made
// here is listed under the build's “Package and run this map” and an
// installation under Run — one store, one installer, one engine watcher.

// playPlanPackaged is the review of a packaged run: what will be fetched, what
// runs, the package the compile will lead to and where it will be installed.
// The package itself is planned after the compile, from the BSP; the review
// says so rather than inventing its members.
func (s *Server) playPlanPackaged(w http.ResponseWriter, request playrun.Request) {
	root, err := filepath.Abs(request.GameRoot)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	modDir := filepath.Join(root, request.ModName)
	inputName, err := s.playMapInputName(request.PipelineID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	grants := request.Grants
	if grants == nil {
		grants = []q2pack.Grant{}
	}
	plan := map[string]any{
		"family": request.Family, "packaged": true, "maturity_message": maturity.Quake2Message,
		"map_input": inputName,
		"map": map[string]any{
			"asset_id": request.AssetID, "revision_id": request.RevisionID,
			"revision": request.RevisionNumber, "source_file": request.SourceFile,
			"map_name": request.MapName,
		},
		"build": map[string]any{"pipeline": request.PipelineID, "strict": request.Strict},
		"run": map[string]any{
			"engine": request.EngineProfileID, "action": request.EngineActionID,
			"game_root": root, "mod": request.ModName,
		},
		"writes": map[string]any{
			"directory": modDir,
			"files": []string{
				filepath.Join(request.ModName, request.MapName+".pak"),
				filepath.Join(request.ModName, q2install.StampName),
			},
			"never_writes": q2pack.BaseGameDir + ", and every other folder a Quake II installation owns",
		},
		"textures": map[string]any{
			"known": false, "packaged": true, "downloads": true,
			"message": "The map's texture export for this revision is downloaded and verified. The .wal files " +
				"of the account texture sources the map binds, at the revisions it binds, are laid over your " +
				"content folder where the compiler reads them, and travel in the package. Textures installed " +
				"with the game are not sent: the compiler reads them from the base game folder bound to it in " +
				"Profiles, and the package plan names any it does not have.",
		},
		"package": map[string]any{
			"grants": grants, "accept_reason": request.AcceptReason,
			"message": "After the compile, a PAK is made of the compiled map, of the map's account textures, and " +
				"of the files of your content folder that the compiled map names — textures, sky, sounds, " +
				"models — when you say they may " +
				"travel with it. What the base game supplies is never copied. If the map names a file nothing " +
				"holds, or one nobody said may travel, the run stops before anything is installed and names it.",
		},
		"launch": s.previewPackagedLaunch(request, root, modDir),
	}
	writeJSON(w, http.StatusOK, plan)
}

// previewPackagedLaunch resolves the engine command a packaged run would start,
// with the roots internal/q2run gives it.
func (s *Server) previewPackagedLaunch(request playrun.Request, root, modDir string) map[string]any {
	service, err := s.requireJobsService()
	if err != nil {
		return map[string]any{"known": false, "message": err.Error()}
	}
	previewed, err := service.Preview(job.Request{
		ProfileID: request.EngineProfileID, ActionID: request.EngineActionID,
		Runtime: map[string]string{profile.RuntimeMapName: request.MapName, profile.RuntimeModName: request.ModName},
		Roots:   map[string]string{profile.RootGame: root, profile.RootContent: modDir},
	})
	if err != nil || previewed == nil || previewed.Command == nil {
		message := "This engine's command is resolved once the package is installed."
		if err != nil {
			message += " " + err.Error()
		}
		return map[string]any{"known": false, "message": message}
	}
	return map[string]any{
		"known": true, "executable": previewed.Command.Executable, "args": previewed.Command.Args,
		"working_dir": previewed.Command.WorkingDir,
	}
}

// playPlanPackage plans the package of the finished build, and holds the run
// — before anything is written — when the plan says something may not travel.
func (s *Server) playPlanPackage(record *playrun.Record) (playrun.InstallPlan, error) {
	dir, err := s.buildsDir()
	if err != nil {
		return playrun.InstallPlan{}, err
	}
	manifest, err := build.Find(dir, record.BuildID)
	if err != nil {
		return playrun.InstallPlan{}, err
	}
	plan, err := q2pack.Prepare(q2pack.Request{Manifest: manifest, MapName: record.Request.MapName,
		Grants: record.Request.Grants})
	if err != nil {
		return playrun.InstallPlan{}, err
	}
	accepting := record.Request.AcceptReason != ""
	if held := plan.Blocked(accepting); held != nil {
		return playrun.InstallPlan{}, &playrun.RemedyError{
			Err:    fmt.Errorf("%w: %v", playrun.ErrPackageHeld, held),
			Remedy: packageHeldRemedy(plan, accepting, record.BuildID),
		}
	}
	return playrun.InstallPlan{Package: plan, Manifest: manifest,
		BuildManifest: filepath.Join(manifest.Directory, build.ManifestFileName)}, nil
}

// packageHeldRemedy is what a person does about a held package, in the order
// it can be done: a missing file is put back and the run tried again; a file
// nobody said may travel is a choice made in the review, or file by file in
// the build's package panel.
func packageHeldRemedy(plan *q2pack.Plan, accepting bool, buildID string) string {
	var missing, ungranted, other []string
	for _, problem := range plan.Problems {
		if accepting && problem.Acceptable {
			continue
		}
		switch problem.Code {
		case "missing":
			missing = append(missing, problem.Path)
		case "no_grant", "not_redistributable":
			ungranted = append(ungranted, problem.Path)
		default:
			other = append(other, problem.Message)
		}
	}
	var sentences []string
	if len(missing) > 0 {
		sentences = append(sentences, "Put "+strings.Join(missing, ", ")+" in the content folder bound to the "+
			"compiler in Profiles (or remove it from the map), then press Try again.")
	}
	if len(ungranted) > 0 {
		sentences = append(sentences, "Say whether "+strings.Join(ungranted, ", ")+" may travel with the map: "+
			"start Build & Run again and answer “Your own files in the package” in the review, or open build "+
			buildID+" under Build and use “Package and run this map” to decide file by file.")
	}
	if len(other) > 0 {
		sentences = append(sentences, "Change what the package plan names: "+strings.Join(other, "; ")+".")
	}
	if len(missing) > 0 || len(ungranted) > 0 {
		sentences = append(sentences, "A package that leaves a file out on purpose is possible too: accept it "+
			"in writing in the review.")
	}
	return strings.Join(sentences, " ")
}

// playInstallPackage writes the package and installs it, through the store and
// the installer the package panel uses.
func (s *Server) playInstallPackage(ctx context.Context, request playrun.Request,
	plan playrun.InstallPlan) (playrun.InstallResult, error) {
	if plan.Package == nil {
		return playrun.InstallResult{}, errors.New("there is no package plan to install")
	}
	store, err := s.q2Packages()
	if err != nil {
		return playrun.InstallResult{}, err
	}
	record, existed, err := store.Create(plan.Package, q2pack.Options{Companion: s.version,
		AcceptReason: request.AcceptReason, Manifest: plan.Manifest})
	if err != nil {
		return playrun.InstallResult{}, err
	}
	s.logf("q2 package %s: written from build %s by Build & Run, %d member(s), complete=%v", record.ID,
		record.Plan.BuildID, record.Archive.Entries, record.Complete)
	dir, err := s.q2InstallsDir()
	if err != nil {
		return playrun.InstallResult{}, err
	}
	installRequest := q2install.Request{Package: record, EngineProfileID: request.EngineProfileID,
		GameRoot: request.GameRoot, Mod: request.ModName, Dir: dir}
	if id := q2install.InstallationID(absolute(request.GameRoot), request.ModName); id != "" {
		if jobID := s.q2EngineRunning(id); jobID != "" {
			return playrun.InstallResult{}, failure.As(failure.InstallConflict, fmt.Errorf(
				"an engine is still running %s (job %s): stop it under Jobs or Run, then press Try again",
				request.ModName, jobID))
		}
	}
	installation, err := q2install.Install(ctx, installRequest)
	if err != nil {
		return playrun.InstallResult{}, err
	}
	s.logf("q2 install %s: package %s into %s by Build & Run", installation.ID, installation.PackageID, installation.ModDir)
	return playrun.InstallResult{
		Dir: installation.ModDir,
		Package: &playrun.PackageRef{ID: record.ID, Archive: record.Archive.File, SHA256: record.Archive.SHA256,
			Members: len(record.Entries), Complete: record.Complete, NotCarried: record.Plan.NotCarried, Reused: existed},
		Installation: &playrun.InstallationRef{ID: installation.ID, ModDir: installation.ModDir,
			FSGame: installation.FSGame, MapName: installation.MapName, Replaced: installation.Replaced},
	}, nil
}

// q2EngineRunning is the job of an engine this process started on an
// installation and that has not stopped, or empty.
func (s *Server) q2EngineRunning(installationID string) string {
	run := s.q2runs.forInstallation(installationID)
	if run == nil || run.jobID() == "" || s.jobs == nil {
		return ""
	}
	if current, err := s.jobs.Get(run.jobID()); err == nil && !current.State.Terminal() {
		return run.jobID()
	}
	return ""
}

// playLaunchPackage starts the engine on the installed package through
// internal/q2run, and returns once the engine has said whether it opened the
// map from it. The run is registered as the package panel's runs are, so the
// panel's own guards — no reinstall or removal under a running engine — and
// its Stop button see it.
func (s *Server) playLaunchPackage(ctx context.Context, request playrun.Request) (playrun.LaunchRecord, error) {
	service, err := s.requireJobsService()
	if err != nil {
		return playrun.LaunchRecord{}, err
	}
	dir, err := s.q2InstallsDir()
	if err != nil {
		return playrun.LaunchRecord{}, err
	}
	installation, err := q2install.Load(dir, q2install.InstallationID(absolute(request.GameRoot), request.ModName))
	if err != nil {
		return playrun.LaunchRecord{}, err
	}
	id, err := newQ3RunID()
	if err != nil {
		return playrun.LaunchRecord{}, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	run := &q3Run{ID: id, Installation: installation.ID, Engine: request.EngineProfileID,
		Action: request.EngineActionID, StartedAt: time.Now().UTC(), cancel: cancel}
	s.q2runs.add(run)
	result, err := q2run.Launch(runCtx, service, q2run.Request{
		Installation: installation, EngineProfileID: request.EngineProfileID, ActionID: request.EngineActionID,
		Label: firstNonBlank(request.Label, "Build & Run "+installation.MapName),
		Started: func(jobID string) {
			run.mu.Lock()
			run.job = jobID
			run.mu.Unlock()
		},
	})
	run.mu.Lock()
	run.done = true
	if result != nil {
		run.result = result
	}
	if err != nil {
		run.err, run.class = err.Error(), failure.Of(err)
	}
	run.mu.Unlock()
	s.saveRun(run, "q2-runs")
	if err != nil {
		return playrun.LaunchRecord{}, err
	}
	launched := playrun.LaunchRecord{ProfileID: request.EngineProfileID, ActionID: request.EngineActionID,
		JobID: result.JobID, Executable: result.Executable, Args: result.Args, WorkingDir: result.WorkingDir,
		MapLoad: result.MapLoad, LoadedFrom: result.LoadedFrom, SourceVerified: result.SourceVerified,
		Message: result.Message}
	s.logf("q2 run %s (Build & Run): map load %s from %q, engine job %s", run.ID, result.MapLoad,
		result.LoadedFrom, result.JobID)
	if result.OK() {
		return launched, nil
	}
	remedy := result.Hint
	if remedy == "" {
		remedy = "Open the engine's job under Jobs to read what it printed. The installed package is kept, " +
			"under Run, where it can be started again or removed."
	}
	return launched, &playrun.RemedyError{Err: errors.New(result.Message), Remedy: remedy}
}

func absolute(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
