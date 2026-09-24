package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetref"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetsync"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle"
)

// Build & Run: the one-click journey, and the place every existing service is
// wired to the coordinator that orders them.
//
// Nothing here does any of the work. [playrun.Service] owns the order, the
// record and the cancellation; this file is the adapter that hands it the
// asset syncer, the texture-bundle cache, the extractor runner, the build
// runner, the level stager and the job service — each of which already exists,
// and none of which is reimplemented.
//
// # Why the token never leaves this process
//
// The page receives sanitized manifest facts: the ordered WAD names, whether
// the bundle is compiler-ready, the refusals. It never receives the AUB
// session token and never receives a private download URL, because a page that
// held either would be a page that moves the whole authentication boundary
// into JavaScript. `AUCOM/AUE/AUT 246I1`.

func (s *Server) playAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		// The review model: what would be fetched, what would run, where files
		// would be written, and the final argv.
		"POST /api/v1/play/plan": s.handlePlayPlan,
		// One confirmation starts the complete server-side sequence.
		"POST /api/v1/play/runs":             s.handlePlayStart,
		"GET /api/v1/play/runs":              s.handlePlayList,
		"GET /api/v1/play/runs/{id}":         s.handlePlayGet,
		"POST /api/v1/play/runs/{id}/cancel": s.handlePlayCancel,
		"POST /api/v1/play/runs/{id}/retry":  s.handlePlayRetry,
		// The bundle's sanitized status for one revision, for the Review step.
		"GET /api/v1/play/textures": s.handlePlayTextures,
	}
}

// playRunsDir is where run records live: beside the job store and the builds,
// because a run is a set of those plus an order.
func (s *Server) playRunsDir() (string, error) {
	jobs, err := s.jobsDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(filepath.Dir(jobs), "play-runs"), nil
}

// textureCacheDir is where verified texture bundles live.
func (s *Server) textureCacheDir() (string, error) {
	dir, err := s.assetCacheDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "texture-bundles"), nil
}

func (s *Server) textureCache() (*texturebundle.Cache, error) {
	dir, err := s.textureCacheDir()
	if err != nil {
		return nil, err
	}

	return texturebundle.Open(dir)
}

// playService builds the coordinator, wired to this run's real services.
//
// Built per request for the reason every other state helper in this package is:
// the profile directory, the binding file and the session all change under a
// running server, and a coordinator captured at construction would be one that
// stops matching the disk.
func (s *Server) playService() (*playrun.Service, error) {
	dir, err := s.playRunsDir()
	if err != nil {
		return nil, err
	}
	store, err := playrun.OpenStore(dir)
	if err != nil {
		return nil, err
	}

	return playrun.NewService(store, playrun.Deps{
		FetchMap:        s.playFetchMap,
		FetchBundle:     s.playFetchBundle,
		Convert:         s.playConvert,
		MapInputName:    s.playMapInputName,
		Build:           s.playBuild,
		PlanInstall:     s.playPlanInstall,
		Install:         s.playInstall,
		VerifyInstalled: s.playVerifyInstalled,
		Launch:          s.playLaunch,
		Launched:        s.playLaunched,
		Unstage:         s.playUnstage,
		Logf:            s.logf,
		Live:            s.playLive,
	})
}

// --- the stages -------------------------------------------------------------

// playFetchMap materializes the exact map revision through the SAME asset
// syncer and content-addressed cache every other AUB input goes through.
func (s *Server) playFetchMap(ctx context.Context, request playrun.Request) (playrun.MapResult, error) {
	store, err := s.assets()
	if err != nil {
		return playrun.MapResult{}, err
	}
	var syncer *assetsync.Syncer
	if client := s.aubClient(); client != nil && client.Authenticated() {
		if made, makeErr := assetsync.NewSyncer(ctx, client, store); makeErr == nil {
			syncer = made
		}
	}
	buildsDir, err := s.buildsDir()
	if err != nil {
		return playrun.MapResult{}, err
	}
	stage, err := os.MkdirTemp(ensureDir(buildsDir), "play-map-")
	if err != nil {
		return playrun.MapResult{}, fmt.Errorf("creating a staging directory: %w", err)
	}

	// The exact revision, never `current`: [playrun.Request.Normalize] has
	// already refused a request without one.
	reference := assetref.Prefix + request.AssetType + "/" + request.AssetID + "@" + request.RevisionID
	if request.SourceFile != "" {
		reference += "#" + request.SourceFile
	}
	resolved, sources, err := assetref.ResolveAll(ctx, store, syncer,
		map[string]string{playMapInput: reference}, stage)
	if err != nil {
		return playrun.MapResult{}, err
	}
	source := sources[playMapInput]

	return playrun.MapResult{Path: resolved[playMapInput], Source: &source}, nil
}

// playMapInput is the internal name this adapter resolves the map under. It is
// not the pipeline's input name — [Server.playMapInputName] asks the pipeline
// for that — it is just a key in a one-entry map.
const playMapInput = "map"

// playFetchBundle downloads and verifies the texture export for the same
// revision, reusing a complete verified cache entry when there is one.
func (s *Server) playFetchBundle(ctx context.Context, request playrun.Request) (playrun.BundleResult, error) {
	cache, err := s.textureCache()
	if err != nil {
		return playrun.BundleResult{}, err
	}
	want := texturebundle.Expect{MapID: request.AssetID, Revision: request.RevisionNumber}

	// Offline first. A verified entry is re-checked by Lookup itself, so a
	// reuse is a reuse of files that still hash to what was verified.
	if entry, found := cache.Lookup(want); found {
		return bundleResult(entry, s.backendAddress()), nil
	}

	client := s.aubClient()
	if client == nil || !client.Authenticated() {
		return playrun.BundleResult{}, errors.New(
			"this map's textures are not in the local cache yet, and downloading them needs you to be signed in")
	}
	bundle, err := client.TextureExport(ctx, request.AssetID, request.RevisionNumber)
	if err != nil {
		return playrun.BundleResult{}, err
	}
	entry, err := cache.Publish(ctx, bundle, want)
	if err != nil {
		return playrun.BundleResult{}, err
	}

	return bundleResult(entry, s.backendAddress()), nil
}

// bundleResult turns a verified cache entry into the portable identity the run
// records and the local root the build reads.
func bundleResult(entry texturebundle.Entry, backend string) playrun.BundleResult {
	ref := &build.BundleRef{
		Schema:           entry.Receipt.ManifestSchema,
		Backend:          backend,
		MapID:            entry.Receipt.MapID,
		Revision:         entry.Receipt.Revision,
		Digest:           entry.Receipt.BundleDigest,
		WADsDeclared:     entry.Receipt.WADsDeclared,
		CompilerReady:    entry.Receipt.CompilerReady,
		CompilerRefusals: entry.Receipt.CompilerRefusals,
	}
	for _, file := range entry.Receipt.Files {
		ref.Files = append(ref.Files, build.BundleFile{
			Path: file.Path, Source: file.Source, SHA256: file.SHA256, Bytes: file.Bytes,
		})
	}

	return playrun.BundleResult{Ref: ref, ContentRoot: entry.ContentRoot}
}

func (s *Server) backendAddress() string {
	if client := s.aubClient(); client != nil {
		return client.BaseURL()
	}

	return ""
}

// playConvert runs the extractor out of process, through the one converter
// internal/assetref owns. A revision that is already a `.map` is returned
// unchanged and nothing is started.
func (s *Server) playConvert(ctx context.Context, _ playrun.Request, mapFile string) (playrun.ConvertResult, error) {
	if !strings.EqualFold(filepath.Ext(mapFile), ".apmap") {
		return playrun.ConvertResult{Path: mapFile}, nil
	}
	converted, _, err := assetref.ConvertAPMapInputs(ctx, s.runner,
		map[string]string{playMapInput: mapFile}, nil)
	if err != nil {
		return playrun.ConvertResult{}, err
	}
	provenance := s.runner.Provenance()

	return playrun.ConvertResult{
		Path: converted[playMapInput],
		Extractor: &playrun.ExtractorRef{
			Version:  provenance.Version,
			Protocol: provenance.Protocol,
			Path:     provenance.Path,
			SHA256:   provenance.Digest,
			Verified: provenance.Verified,
		},
	}, nil
}

// playMapInputName asks the pipeline which of its declared inputs the map
// source is, using the same role classification the Build page renders from.
func (s *Server) playMapInputName(pipelineID string) (string, error) {
	catalog, err := s.catalog()
	if err != nil {
		return "", err
	}
	entry, err := catalog.Lookup(pipelineID)
	if err != nil {
		return "", err
	}
	pipeline, isPipeline := entry.Profile.(*profile.PipelineProfile)
	if !isPipeline {
		return "", fmt.Errorf("%s is not a build profile", pipelineID)
	}
	family := documentFamily(pipeline)
	for _, input := range pipeline.Inputs {
		if profile.InputSourceKind(input.Role, family) == profile.SourceKindMap {
			return input.Name, nil
		}
	}

	return "", fmt.Errorf("%s declares no map source to build from", pipelineID)
}

func (s *Server) playBuild(ctx context.Context, request build.Request,
	announce func(*build.Manifest)) (*build.Manifest, error) {
	runner, err := s.buildRunner(announce)
	if err != nil {
		return nil, err
	}

	return runner.Run(ctx, request)
}

// playPlanInstall reads the finished build and the verified bundle and says
// what goes into the mod directory.
func (s *Server) playPlanInstall(_ context.Context, record *playrun.Record) (playrun.InstallPlan, error) {
	dir, err := s.buildsDir()
	if err != nil {
		return playrun.InstallPlan{}, err
	}
	manifest, err := build.Find(dir, record.BuildID)
	if err != nil {
		return playrun.InstallPlan{}, err
	}
	level, err := build.PlayableLevel(manifest)
	if err != nil {
		return playrun.InstallPlan{}, err
	}
	plan := playrun.InstallPlan{
		BSP: level.BSP, Lit: level.Lit,
		BuildManifest: filepath.Join(manifest.Directory, build.ManifestFileName),
	}
	// The WADs, in the map's declaration order, from the verified content root.
	// Order preserved rather than sorted: it is the map's own content, and the
	// staged tree is what somebody inspects afterwards.
	if record.Bundle != nil {
		for _, file := range record.Bundle.Files {
			plan.WADs = append(plan.WADs, playrun.InstallFile{
				Path: file.Path, Source: filepath.Join(record.BundleRoot, filepath.FromSlash(file.Path)),
			})
		}
	}

	return plan, nil
}

// playInstall stages through engine.LevelStaging, which owns the stamp, the
// refusal to write into a directory the Companion did not create, the rollback
// and `engine unstage`.
func (s *Server) playInstall(_ context.Context, request playrun.Request,
	plan playrun.InstallPlan) (playrun.InstallResult, error) {
	wads := make([]engine.StagedSource, 0, len(plan.WADs))
	for _, file := range plan.WADs {
		wads = append(wads, engine.StagedSource{Path: file.Path, Source: file.Source})
	}
	staged, err := engine.LevelStaging{
		GameRoot: request.GameRoot, ModName: request.ModName, MapName: request.MapName,
		BSP: plan.BSP, Lit: plan.Lit, ProfileID: request.EngineProfileID,
		WADs: wads, BuildManifest: plan.BuildManifest,
	}.Stage()
	if err != nil {
		return playrun.InstallResult{}, err
	}
	files := make([]playrun.StagedFile, 0, len(staged.Stamp.Files))
	for _, file := range staged.Stamp.Files {
		files = append(files, playrun.StagedFile{Path: file.Path, SHA256: file.SHA256, Bytes: file.Size})
	}

	return playrun.InstallResult{
		Dir:   filepath.Join(request.GameRoot, request.ModName),
		Files: files,
	}, nil
}

// playVerifyInstalled re-hashes every staged file immediately before launch.
func (s *Server) playVerifyInstalled(_ context.Context, request playrun.Request,
	files []playrun.StagedFile) error {
	dir := filepath.Join(request.GameRoot, request.ModName)
	for _, file := range files {
		path := filepath.Join(dir, filepath.FromSlash(file.Path))
		digest, size, err := engine.DigestFile(path)
		switch {
		case err != nil:
			return fmt.Errorf("%s is not readable where it was staged: %w", file.Path, err)
		case size != file.Bytes:
			return fmt.Errorf("%s is %d bytes and was staged at %d", file.Path, size, file.Bytes)
		case !strings.EqualFold(digest, file.SHA256):
			return fmt.Errorf("%s no longer matches what was staged", file.Path)
		}
	}

	return nil
}

// playLaunch starts the engine through the job service, with the structured
// argv the engine profile resolves — the executable and each argument as its
// own element, never a command string.
func (s *Server) playLaunch(_ context.Context, request playrun.Request) (playrun.LaunchRecord, error) {
	service, err := s.requireJobsService()
	if err != nil {
		return playrun.LaunchRecord{}, err
	}
	jobRequest := job.Request{
		ProfileID: request.EngineProfileID,
		ActionID:  request.EngineActionID,
		Runtime: map[string]string{
			profile.RuntimeMapName: request.MapName,
			profile.RuntimeModName: request.ModName,
		},
		Label: request.Label,
	}
	// A hosted game's engine output is watched from its first line, so the
	// listing can say which version is running (see observedEngineVersion).
	var mirror io.Writer
	var opening *engineOpening
	if request.Listing != nil && hostingActions[request.EngineActionID] != "" {
		opening = &engineOpening{}
		mirror = opening
	}
	submitted, err := service.SubmitWatched(jobRequest, mirror)
	if err != nil {
		return playrun.LaunchRecord{}, err
	}
	if opening != nil {
		s.hosting.keepOpening(submitted.ID, opening)
	}
	record := playrun.LaunchRecord{
		ProfileID: request.EngineProfileID, ActionID: request.EngineActionID, JobID: submitted.ID,
	}
	// The argv as the operating system will receive it. Previewed through the
	// same resolver the executor uses, so what is recorded is what starts.
	if previewed, previewErr := service.Preview(jobRequest); previewErr == nil &&
		previewed != nil && previewed.Command != nil {
		record.Executable = previewed.Command.Executable
		record.Args = previewed.Command.Args
		record.WorkingDir = previewed.Command.WorkingDir
	}

	return record, nil
}

// playUnstage removes what a cancelled or failed run installed, through the
// same ownership-respecting removal `companion engine unstage` performs: a file
// the user changed since it was staged is left alone.
func (s *Server) playUnstage(request playrun.Request) error {
	if _, err := engine.ReadStamp(filepath.Join(request.GameRoot, request.ModName)); err != nil {
		// Nothing there the Companion staged. A run that failed before it
		// installed has nothing to clean up, and removing a directory this
		// program did not create is exactly what internal/engine refuses.
		return nil
	}
	_, err := engine.Unstage(request.GameRoot, request.ModName)

	return err
}

// --- the HTTP surface -----------------------------------------------------------

// playRequestBody is what the Build & Run page sends. Exact identities, never
// display labels.
type playRequestBody struct {
	AssetType      string `json:"asset_type,omitempty"`
	AssetID        string `json:"asset_id"`
	RevisionID     string `json:"revision_id"`
	RevisionNumber int    `json:"revision_number"`
	SourceFile     string `json:"source_file,omitempty"`

	Pipeline string                       `json:"pipeline"`
	Options  map[string]map[string]string `json:"options,omitempty"`
	Strict   bool                         `json:"strict,omitempty"`

	Engine string `json:"engine"`
	Action string `json:"action"`
	Mod    string `json:"mod,omitempty"`
	Map    string `json:"map"`
	Label  string `json:"label,omitempty"`
	// OwnWADsDir is the folder the person named in the review for WADs
	// Auto-Pigeon may not redistribute. See playrun/ownwads.go.
	OwnWADsDir string `json:"own_wads_dir,omitempty"`
	// Listing lists a hosted game in Live Games. See hosting.go.
	Listing *playListingBody `json:"listing,omitempty"`
}

// playListingBody is the listing the page confirmed in step 3.
type playListingBody struct {
	Title        string `json:"title"`
	Visibility   string `json:"visibility"`
	EndpointHost string `json:"endpoint_host"`
	EndpointPort int    `json:"endpoint_port"`
}

func (b playRequestBody) request(gameRoot string) playrun.Request {
	var listing *playrun.Listing
	if b.Listing != nil {
		listing = &playrun.Listing{Title: b.Listing.Title, Visibility: b.Listing.Visibility,
			EndpointHost: b.Listing.EndpointHost, EndpointPort: b.Listing.EndpointPort}
	}
	return playrun.Request{
		Listing:   listing,
		AssetType: b.AssetType, AssetID: b.AssetID,
		RevisionID: b.RevisionID, RevisionNumber: b.RevisionNumber, SourceFile: b.SourceFile,
		PipelineID: b.Pipeline, Options: b.Options, Strict: b.Strict,
		EngineProfileID: b.Engine, EngineActionID: b.Action,
		GameRoot: gameRoot, ModName: b.Mod, MapName: b.Map, Label: b.Label,
		OwnWADsDir: b.OwnWADsDir,
	}
}

// gameRootFor resolves an engine's game folder from the binding the user
// granted. The page never sends it: where a game is installed is configuration,
// and a request that could name it could name anything.
func (s *Server) gameRootFor(profileID string) (string, error) {
	set, _, err := s.bindings()
	if err != nil {
		return "", err
	}
	local, _ := set.Find(profileID)
	root := local.Roots[profile.RootGame]
	if root == "" {
		return "", errors.New(
			"this engine has no game folder set on this machine yet: choose it under " +
				"“Set up this engine on this machine” and press Save setup")
	}

	return root, nil
}

func (s *Server) handlePlayStart(w http.ResponseWriter, r *http.Request) {
	var body playRequestBody
	if !decodeJSON(w, r, &body) {
		return
	}
	gameRoot, err := s.gameRootFor(body.Engine)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	service, err := s.playService()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	record, err := service.Start(body.request(gameRoot))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, playView(record, time.Now().UTC()))
}

func (s *Server) handlePlayGet(w http.ResponseWriter, r *http.Request) {
	service, err := s.playService()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	record, err := service.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, playStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, playView(record, time.Now().UTC()))
}

func (s *Server) handlePlayList(w http.ResponseWriter, r *http.Request) {
	service, err := s.playService()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	records, err := service.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	now := time.Now().UTC()
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		view := playView(record, now)
		if listing := s.hosting.view(record.ID); listing != nil {
			view["listing"] = listing
		}
		items = append(items, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handlePlayCancel(w http.ResponseWriter, r *http.Request) {
	service, err := s.playService()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	if err = service.Cancel(r.PathValue("id")); err != nil {
		writeError(w, playStatus(err), err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"cancelling": true})
}

func (s *Server) handlePlayRetry(w http.ResponseWriter, r *http.Request) {
	service, err := s.playService()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	record, err := service.Retry(r.PathValue("id"))
	if err != nil {
		writeError(w, playStatus(err), err)
		return
	}
	writeJSON(w, http.StatusAccepted, playView(record, time.Now().UTC()))
}

func playStatus(err error) int {
	switch {
	case errors.Is(err, playrun.ErrNotFound):
		return http.StatusNotFound
	case err != nil:
		return http.StatusConflict
	default:
		return http.StatusOK
	}
}

// playView is the sanitized record the page receives.
//
// Constructed rather than serialized wholesale, for internal/feedback's reason:
// a filter is only as good as the last person who thought about it, and a map
// built field by field cannot leak one a future field adds.
func playView(record *playrun.Record, now time.Time) map[string]any {
	out := map[string]any{
		"id":         record.ID,
		"state":      record.State,
		"title":      record.State.Title(),
		"active":     record.State.Active(),
		"created_at": record.CreatedAt,
		"elapsed_ms": record.Elapsed(now).Milliseconds(),
		"map":        record.Request.MapName,
		"mod":        record.Request.ModName,
		"asset_id":   record.Request.AssetID,
		"revision":   record.Request.RevisionNumber,
		"pipeline":   record.Request.PipelineID,
		"engine":     record.Request.EngineProfileID,
		"can_cancel": record.Cancellable(),
		"can_retry":  record.Retryable(),
	}
	if record.Error != "" {
		out["error"] = record.Error
		// One sentence for the Activity card; the whole text stays in
		// "error" for Technical details and Jobs.
		out["error_summary"] = playrun.Summary(record.Error)
		out["failed_at"] = record.FailedAt
		out["failed_at_title"] = record.FailedAt.Title()
	}
	// A cancelled run names the stage it was stopped in, so the page can say
	// "you cancelled this during Compiling" instead of painting it as a failure.
	if record.State == playrun.Cancelled && len(record.Stages) > 0 {
		last := record.Stages[len(record.Stages)-1]
		out["cancelled_at_title"] = last.State.Title()
		out["installed_nothing"] = len(record.Installed) == 0
	}
	if remedy := playrun.CurrentRemedy(record); remedy != "" {
		out["remedy"] = remedy
	}
	if len(record.Warnings) > 0 {
		out["warnings"] = record.Warnings
	}
	if record.RetryOf != "" {
		out["retry_of"] = record.RetryOf
	}
	if !record.FinishedAt.IsZero() {
		out["finished_at"] = record.FinishedAt
	}
	stages := make([]map[string]any, 0, len(record.Stages))
	for _, stage := range record.Stages {
		row := map[string]any{
			"state": stage.State, "title": stage.State.Title(),
			"started_at": stage.StartedAt, "duration_ms": stage.DurationMS,
		}
		if !stage.FinishedAt.IsZero() {
			row["finished_at"] = stage.FinishedAt
		}
		if stage.Detail != "" {
			row["detail"] = stage.Detail
		}
		if stage.Error != "" {
			row["error"] = stage.Error
			if record.State == playrun.Cancelled {
				row["cancelled"] = true
			}
		}
		stages = append(stages, row)
	}
	out["stages"] = stages

	if record.BuildID != "" {
		out["build_id"] = record.BuildID
	}
	if record.CurrentJob != "" {
		out["current_step"], out["current_job"] = record.CurrentStep, record.CurrentJob
	}
	if record.Bundle != nil {
		out["bundle"] = map[string]any{
			"digest": record.Bundle.Digest, "revision": record.Bundle.Revision,
			"wads_declared":     record.Bundle.WADsDeclared,
			"compiler_ready":    record.Bundle.CompilerReady,
			"compiler_refusals": record.Bundle.CompilerRefusals,
			"files":             bundleFileView(record.Bundle.Files),
		}
	}
	if record.Extractor != nil {
		out["extractor"] = map[string]any{
			"version": record.Extractor.Version, "protocol": record.Extractor.Protocol,
			"verified": record.Extractor.Verified,
		}
	}
	if len(record.Installed) > 0 {
		installed := make([]map[string]any, 0, len(record.Installed))
		for _, file := range record.Installed {
			installed = append(installed, map[string]any{
				"path": file.Path, "sha256": file.SHA256, "bytes": file.Bytes,
			})
		}
		out["installed"] = installed
		out["installed_dir"] = record.InstalledDir
	}
	if record.Launch != nil {
		out["launch"] = map[string]any{
			"profile_id": record.Launch.ProfileID, "action_id": record.Launch.ActionID,
			"job_id": record.Launch.JobID,
			// The argv element by element, which is how a person checks it.
			"executable": record.Launch.Executable, "args": record.Launch.Args,
		}
	}

	return out
}

func bundleFileView(files []build.BundleFile) []map[string]any {
	out := make([]map[string]any, 0, len(files))
	for _, file := range files {
		out = append(out, map[string]any{
			"path": file.Path, "source": file.Source, "sha256": file.SHA256, "bytes": file.Bytes,
		})
	}

	return out
}

// handlePlayTextures reports the bundle status for one map revision, for the
// Review step.
//
// Sanitized facts only: the ordered declaration, the verdict, the refusals, the
// file names and digests. No token, no download URL, and no local path.
func (s *Server) handlePlayTextures(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	assetID := strings.TrimSpace(query.Get("asset_id"))
	revision := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(query.Get("revision")), "%d", &revision); err != nil {
		revision = 0
	}
	if assetID == "" || revision <= 0 {
		writeError(w, http.StatusBadRequest,
			errors.New("a texture bundle is asked for by map id and exact revision number"))
		return
	}
	cache, err := s.textureCache()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	want := texturebundle.Expect{MapID: assetID, Revision: revision}
	entry, cached := cache.Lookup(want)
	if !cached {
		client := s.aubClient()
		if client == nil || !client.Authenticated() {
			writeJSON(w, http.StatusOK, map[string]any{
				"cached": false, "known": false,
				"message": "Sign in to check which textures this map revision needs.",
			})
			return
		}
		bundle, downloadErr := client.TextureExport(r.Context(), assetID, revision)
		if downloadErr != nil {
			writeError(w, aubStatus(downloadErr), downloadErr)
			return
		}
		entry, err = cache.Publish(r.Context(), bundle, want)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, withOwnWADs(textureView(entry, cached), entry, query.Get("own_wads_dir")))
}

// withOwnWADs adds to a texture view what "use my own copy" could do for it:
// which WADs AUB refused only because it may not redistribute them, and —
// when the person named a folder — which of those that folder holds. The
// review draws its offer from this; nothing here decides a run.
func withOwnWADs(view map[string]any, entry texturebundle.Entry, dir string) map[string]any {
	if entry.Receipt.CompilerReady {
		return view
	}
	names, only := playrun.OwnWADsNeeded(entry.Receipt.CompilerRefusals)
	view["own_wads_possible"] = only
	if !only {
		return view
	}
	view["own_wads_needed"] = names
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return view
	}
	view["own_wads_dir"] = dir
	found, err := playrun.FindOwnWADs(dir, names)
	if err != nil {
		view["own_wads_error"] = err.Error()
		return view
	}
	ready := true
	for _, wad := range found {
		ready = ready && wad.Found
	}
	view["own_wads"] = found
	view["ready_with_own_wads"] = ready

	return view
}

// textureView is the sanitized bundle status.
func textureView(entry texturebundle.Entry, wasCached bool) map[string]any {
	wads := make([]map[string]any, 0, len(entry.Manifest.OrderedWADs()))
	for _, wad := range entry.Manifest.OrderedWADs() {
		files := make([]map[string]any, 0, len(wad.Files))
		for _, file := range wad.Files {
			files = append(files, map[string]any{
				"path": file.Path, "sha256": file.SHA256, "bytes": file.Bytes,
			})
		}
		wads = append(wads, map[string]any{
			"order": wad.Order, "name": wad.Name, "status": wad.Status,
			"origin": wad.Origin, "included": wad.Included, "note": wad.Note,
			"files": files,
		})
	}

	return map[string]any{
		"cached": true, "known": true, "was_cached": wasCached,
		"map_id": entry.Receipt.MapID, "revision": entry.Receipt.Revision,
		"game": entry.Receipt.Game, "digest": entry.Receipt.BundleDigest,
		"schema": entry.Receipt.ManifestSchema,
		// The map's own declaration order. Later declarations win a name
		// collision, so this is precedence and not decoration.
		"wads_declared":     entry.Receipt.WADsDeclared,
		"wads":              wads,
		"compiler_ready":    entry.Receipt.CompilerReady,
		"compiler_refusals": entry.Receipt.CompilerRefusals,
		"unresolved":        entry.Manifest.Unresolved,
		"total_bytes":       entry.Receipt.TotalBytes,
		"licenses":          entry.Receipt.Licenses,
		"installed_notice":  entry.Manifest.InstalledNotice,
	}
}

// --- the review model ------------------------------------------------------------

// handlePlayPlan answers what a Build & Run would do, and starts nothing.
//
// It explains what will be fetched, which programs will run, where files will
// be written and the final argv. A fact that can only be known after a download
// is MARKED as such rather than invented.
func (s *Server) handlePlayPlan(w http.ResponseWriter, r *http.Request) {
	var body playRequestBody
	if !decodeJSON(w, r, &body) {
		return
	}
	gameRoot, err := s.gameRootFor(body.Engine)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	request := body.request(gameRoot)
	if err = request.Normalize(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err = engine.CheckMapName(request.MapName); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err = engine.CheckModName(request.ModName); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	modDir := filepath.Join(request.GameRoot, request.ModName)
	plan := map[string]any{
		"map": map[string]any{
			"asset_id": request.AssetID, "revision_id": request.RevisionID,
			"revision": request.RevisionNumber, "source_file": request.SourceFile,
			"map_name": request.MapName,
		},
		"build": map[string]any{"pipeline": request.PipelineID, "strict": request.Strict},
		"run": map[string]any{
			"engine": request.EngineProfileID, "action": request.EngineActionID,
			"game_root": request.GameRoot, "mod": request.ModName,
		},
		"writes": map[string]any{
			"directory": modDir,
			"files": []string{
				filepath.Join(request.ModName, "maps", request.MapName+".bsp"),
				filepath.Join(request.ModName, "maps", request.MapName+".lit") + " (only when the compiler produces one)",
				filepath.Join(request.ModName, engine.WADDir) + string(filepath.Separator) +
					"<the map's declared WADs, in order>",
				filepath.Join(request.ModName, engine.BuildManifestName),
			},
			// Said plainly, because it is the difference between a rule and a
			// habit: the Companion never writes into `id1` or any other
			// directory a Quake installation already owns.
			"never_writes": "id1, and every other directory a Quake installation owns",
		},
	}

	// The programs. Which tool provides each step, and at which version, comes
	// from the same preview the Build page shows — and the whole plan is
	// refused now if the pipeline does not resolve on this machine.
	runner, err := s.buildRunner(nil)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	inputName, err := s.playMapInputName(request.PipelineID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	plan["map_input"] = inputName

	// The engine's argv, resolved through the same resolver the executor uses.
	// Where a fact depends on a file that does not exist yet, it is marked.
	plan["launch"] = s.previewLaunch(request)
	plan["build_preview_note"] = "The compiler's exact arguments are resolved once the map and its " +
		"textures are downloaded, because `-wadpath` is the verified bundle's own directory."

	// The bundle, when this machine already holds a verified one. Otherwise
	// the review says it will be fetched rather than claiming what is in it.
	cache, err := s.textureCache()
	if err == nil {
		if entry, found := cache.Lookup(texturebundle.Expect{
			MapID: request.AssetID, Revision: request.RevisionNumber,
		}); found {
			plan["textures"] = withOwnWADs(textureView(entry, true), entry, request.OwnWADsDir)
		} else {
			plan["textures"] = map[string]any{
				"cached": false, "known": false,
				"message": "This revision's textures will be downloaded and verified when you press Build & Run.",
			}
		}
	}
	_ = runner

	writeJSON(w, http.StatusOK, plan)
}

// previewLaunch resolves the engine command this plan would run.
func (s *Server) previewLaunch(request playrun.Request) map[string]any {
	service, err := s.requireJobsService()
	if err != nil {
		return map[string]any{"known": false, "message": err.Error()}
	}
	previewed, err := service.Preview(job.Request{
		ProfileID: request.EngineProfileID,
		ActionID:  request.EngineActionID,
		Runtime: map[string]string{
			profile.RuntimeMapName: request.MapName,
			profile.RuntimeModName: request.ModName,
		},
	})
	if err != nil || previewed == nil || previewed.Command == nil {
		message := "This engine's command cannot be resolved yet."
		if err != nil {
			message = err.Error()
		}

		return map[string]any{"known": false, "message": message}
	}

	return map[string]any{
		"known": true, "executable": previewed.Command.Executable, "args": previewed.Command.Args,
		"working_dir": previewed.Command.WorkingDir,
	}
}

// --- recovery ---------------------------------------------------------------------

// recoverPlayRuns marks runs a stopped Companion left active. Called once at
// start-up: a record saying `compiling` with nothing compiling would otherwise
// claim forever that a build is running.
func (s *Server) recoverPlayRuns() {
	service, err := s.playService()
	if err != nil {
		return
	}
	if count, recoverErr := service.Recover(); recoverErr == nil && count > 0 {
		s.logf("play: %d run(s) that a previous Companion left unfinished were marked failed", count)
	}
}

// sweepTextureBundles removes interrupted extractions a killed process left.
func (s *Server) sweepTextureBundles() {
	cache, err := s.textureCache()
	if err != nil {
		return
	}
	if removed, sweepErr := cache.Sweep(time.Hour); sweepErr == nil && len(removed) > 0 {
		s.logf("play: swept %d unfinished texture extraction(s)", len(removed))
	}
}
