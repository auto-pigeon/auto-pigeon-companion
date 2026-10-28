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
	"github.com/auto-pigeon/auto-pigeon-companion/internal/autobuild"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/maturity"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3pack"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3packages"
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
		BoundPackages:   s.playBoundPackages,
		MapInputName:    s.playMapInputName,
		DeclaresRoot:    s.pipelineDeclaresRoot,
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
		return playrun.MapResult{}, downloadFailure(err)
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
	entry, err := s.verifiedTextureBundle(ctx, request.AssetID, request.RevisionNumber)
	if err != nil {
		return playrun.BundleResult{}, downloadFailure(err)
	}

	return bundleResult(entry, s.backendAddress()), nil
}

// verifiedTextureBundle is the one place a map revision's texture export is
// found or fetched: Build & Run and the editor's leak test both come here
// (NEW_357), so neither has a cache, a download or a check of its own.
func (s *Server) verifiedTextureBundle(ctx context.Context, mapID string, revision int) (texturebundle.Entry, error) {
	cache, err := s.textureCache()
	if err != nil {
		return texturebundle.Entry{}, err
	}
	want := texturebundle.Expect{MapID: mapID, Revision: revision}

	// Offline first. A verified entry is re-checked by Lookup itself, so a
	// reuse is a reuse of files that still hash to what was verified.
	if entry, found := cache.Lookup(want); found {
		return entry, nil
	}

	client := s.aubClient()
	if client == nil || !client.Authenticated() {
		return texturebundle.Entry{}, errors.New(
			"this map's textures are not in the local cache yet, and downloading them needs you to be signed in")
	}
	bundle, err := client.TextureExport(ctx, mapID, revision)
	if err != nil {
		return texturebundle.Entry{}, err
	}

	return cache.Publish(ctx, bundle, want)
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
	for _, requirement := range entry.Manifest.OrderedWADs() {
		if bound := boundSource(requirement); bound != nil {
			ref.Bound = append(ref.Bound, *bound)
		}
	}

	result := playrun.BundleResult{Ref: ref, ContentRoot: entry.ContentRoot, Sources: wadSources(entry)}
	if entry.Receipt.Licenses {
		result.Licenses = filepath.Join(entry.Dir, texturebundle.LicensesName)
	}
	for _, notice := range entry.Receipt.Notices {
		result.Notices = append(result.Notices, playrun.StagedFile{
			Path: notice.Path, SHA256: notice.SHA256, Bytes: notice.Bytes,
		})
	}
	if len(result.Notices) > 0 {
		result.NoticesDir = entry.Dir
	}

	return result
}

// wadSources says which source supplied each WAD the map declares, in the
// map's own order.
//
// `Staged` is decided by the receipt — the files this machine hashed against
// the manifest's digests — and never by the manifest's `included` flag: a
// requirement is staged when every member it names is one of the verified
// files. The credit is the deployment's declaration, relayed for an installed
// source that was carried.
func wadSources(entry texturebundle.Entry) []playrun.WADSource {
	verified := map[string]texturebundle.Member{}
	for _, file := range entry.Receipt.Files {
		verified[file.Path] = file
	}
	wads := entry.Manifest.OrderedWADs()
	out := make([]playrun.WADSource, 0, len(wads))
	for _, wad := range wads {
		source := playrun.WADSource{Name: wad.Name, Origin: wad.Origin, Revision: wad.Revision, Binding: boundSource(wad)}
		staged := len(wad.Files) > 0
		for _, file := range wad.Files {
			member, ok := verified[file.Path]
			if !ok || !strings.EqualFold(member.SHA256, file.SHA256) {
				staged = false

				break
			}
			source.Bytes += member.Bytes
		}
		source.Staged = staged
		switch {
		case !staged:
			source.Bytes = 0
		case len(wad.Files) == 1:
			source.SHA256 = strings.ToLower(wad.Files[0].SHA256)
		default:
			source.SHA256 = strings.ToLower(wad.SourceSHA256)
		}
		if verdict := wad.Redistribution; verdict != nil {
			if staged && verdict.Decision == texturebundle.DecisionIncluded {
				source.Source, source.Credit = verdict.Source, verdict.Credit
			}
			if !staged {
				source.NotSentReason = verdict.Reason
			}
		}
		out = append(out, source)
	}

	return out
}

// boundSource is a requirement's binding to one revision of an account
// texture source, in the build manifest's terms; nil when it has none.
func boundSource(requirement texturebundle.OrderedWAD) *build.BoundTextureSource {
	binding := requirement.Binding
	if binding == nil {
		return nil
	}
	return &build.BoundTextureSource{
		Name: requirement.Name, Status: binding.Status, SourceID: binding.SourceID,
		RevisionID: binding.RevisionID, Revision: binding.Revision,
		SourceSHA256: binding.SourceSHA256, PaletteSHA256: binding.PaletteSHA256,
	}
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
	converted, _, conversions, err := assetref.ConvertAPMapInputsRecorded(ctx, s.runner,
		map[string]string{playMapInput: mapFile}, nil)
	if err != nil {
		return playrun.ConvertResult{}, err
	}
	provenance := s.runner.Provenance()
	var conversion *build.Conversion
	if recorded, ok := conversions[playMapInput]; ok {
		conversion = &recorded
	}

	return playrun.ConvertResult{
		Path:       converted[playMapInput],
		Conversion: conversion,
		Extractor: &playrun.ExtractorRef{
			Version:  provenance.Version,
			Protocol: provenance.Protocol,
			Path:     provenance.Path,
			SHA256:   provenance.Digest,
			Verified: provenance.Verified,
		},
	}, nil
}

// playBoundPackages resolves the packages the fetched map is bound to, through
// the Companion's own session and its content-addressed cache.
func (s *Server) playBoundPackages(ctx context.Context, _ playrun.Request, mapFile string) (*build.BoundPackages, error) {
	store, err := s.assets()
	if err != nil {
		return nil, err
	}
	var account q3packages.Account
	if client := s.aubClient(); client != nil && client.Authenticated() {
		account = client
	}

	return assetref.BoundPackages(ctx, account, store, map[string]string{playMapInput: mapFile})
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

// pipelineDeclaresRoot says whether a step of the pipeline, resolved the way
// a build of it would be, declares the root role.
func (s *Server) pipelineDeclaresRoot(pipelineID, role string) (bool, error) {
	catalog, err := s.catalog()
	if err != nil {
		return false, err
	}
	entry, err := catalog.Lookup(pipelineID)
	if err != nil {
		return false, err
	}
	pipeline, isPipeline := entry.Profile.(*profile.PipelineProfile)
	if !isPipeline {
		return false, fmt.Errorf("%s is not a build profile", pipelineID)
	}
	runner, err := s.buildRunner(nil)
	if err != nil {
		return false, err
	}
	steps, err := pipeline.Resolve(runner.Resolver())
	if err != nil {
		return false, err
	}
	for _, step := range steps {
		for _, root := range step.Action.Roots {
			if root.Role == role {
				return true, nil
			}
		}
	}
	return false, nil
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
	if record.Request.Packaged() {
		return s.playPlanPackage(record)
	}
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
func (s *Server) playInstall(ctx context.Context, request playrun.Request,
	plan playrun.InstallPlan) (playrun.InstallResult, error) {
	if request.Packaged() {
		return s.playInstallPackage(ctx, request, plan)
	}
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
	if request.Packaged() {
		// internal/q2run re-hashes the installed archive and its map
		// immediately before it starts the engine (q2install.Verify): one
		// check, in the owner that starts the process.
		return nil
	}
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
func (s *Server) playLaunch(ctx context.Context, request playrun.Request) (playrun.LaunchRecord, error) {
	if request.Packaged() {
		return s.playLaunchPackage(ctx, request)
	}
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

	return record, awaitEngineStart(ctx, service, submitted.ID)
}

// engineStartWindow is how long a launch watches a just-submitted engine before
// calling it started. An engine that cannot start at all — a shared library the
// loader cannot find, an argument it refuses, a file that is not a program —
// ends inside it; one still running after it is a launched game, and nothing
// waits for a game somebody is playing to exit.
var engineStartWindow = 4 * time.Second

// awaitEngineStart is NEW_330's answer to a false success NEW_331 saw with a
// released Companion: "Starting the game ✓ · Running ✓ · Finished" while the
// engine job had failed with exit 127 in a millisecond. A submitted job is not
// a started game. A job that ends unsuccessfully inside the window fails the
// launch, naming the job a person opens to read why.
func awaitEngineStart(ctx context.Context, service *job.Service, id string) error {
	watch, stop := context.WithTimeout(ctx, engineStartWindow)
	defer stop()
	ended, err := service.Wait(watch, id)
	if err != nil || ended == nil || ended.State == job.Succeeded {
		// Still running when the window closed, or a game that started and was
		// closed again: either way the engine started.
		return nil
	}
	exit := "no exit status"
	if ended.ExitCode != nil {
		exit = fmt.Sprintf("exit status %d", *ended.ExitCode)
	}
	why := strings.TrimSpace(ended.Error)
	if why == "" {
		why = "it stopped before the game started"
	}

	return fmt.Errorf("the engine did not start: job %s %s with %s — %s", id, ended.State, exit, why)
}

// playUnstage removes what a cancelled or failed run installed, through the
// same ownership-respecting removal `companion engine unstage` performs: a file
// the user changed since it was staged is left alone.
func (s *Server) playUnstage(request playrun.Request) error {
	if request.Packaged() {
		// A package installation is put in place whole or not at all, and
		// stays listed under Run; nothing half-installed is left to remove.
		return nil
	}
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
	// OwnWADsDir is the folder the person named in the review for WADs the
	// deployment did not send. See playrun/ownwads.go.
	OwnWADsDir string `json:"own_wads_dir,omitempty"`
	// Grants and AcceptReason are a Quake II review's answers about the
	// files of the content folder the package may carry (playq2.go).
	Grants       []q2pack.Grant `json:"grants,omitempty"`
	AcceptReason string         `json:"accept_reason,omitempty"`
	// Listing lists a hosted game in Live Games. See hosting.go.
	Listing *playListingBody `json:"listing,omitempty"`
	// BuildOnly is Build & Run with "Run after build" switched off (NEW_363):
	// the same coordinator, downloads and build, and then nothing is
	// installed and no engine starts — so Engine, Action and Mod are not asked
	// for. See playrun.Request.BuildOnly.
	BuildOnly bool `json:"build_only,omitempty"`
	// BuildRequestID names the editor's build request this start answers
	// (buildrequest.go). Optional: a start without it is unchanged. Not part
	// of the run's request, so it never makes two builds different ones.
	BuildRequestID string `json:"build_request_id,omitempty"`
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
		listing = &playrun.Listing{Title: b.Listing.Title, Visibility: listingVisibility(b.Listing.Visibility),
			EndpointHost: b.Listing.EndpointHost, EndpointPort: b.Listing.EndpointPort}
	}
	return playrun.Request{
		Listing:   listing,
		AssetType: b.AssetType, AssetID: b.AssetID,
		RevisionID: b.RevisionID, RevisionNumber: b.RevisionNumber, SourceFile: b.SourceFile,
		PipelineID: b.Pipeline, Options: b.Options, Strict: b.Strict,
		EngineProfileID: b.Engine, EngineActionID: b.Action,
		GameRoot: gameRoot, ModName: b.Mod, MapName: b.Map, Label: b.Label,
		OwnWADsDir: b.OwnWADsDir, Grants: b.Grants, AcceptReason: b.AcceptReason,
		BuildOnly: b.BuildOnly,
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

// errPlayIsNotForQuake3 is why Build & Run refuses a Quake III pipeline.
//
// Build & Run stages a LOOSE level — `<mod>/maps/<map>.bsp` and the map's WADs —
// which is the whole of what a Quake map needs to be played. A Quake III map
// names shaders, images and models that have to travel with it in a PK3, and
// which of those may be redistributed is a question only its author can answer.
// Running one through this sequence produced a mod folder with a BSP in it and
// an engine that drew the default texture everywhere (`Q3_010`, row 24): a run
// that reported success and showed nothing. So the sequence says where the
// Quake III path is, rather than doing half of it.
var errPlayIsNotForQuake3 = errors.New(
	"Build & Run plays a Quake map by putting its level in a mod folder. A Quake III map needs its " +
		"textures, shaders and models packaged with it, and you have to say which of them may be " +
		"redistributed: build it under Build, then use “Package and run this map” under the build's result")

// A Quake II map is played from a PAK in a mod folder of its own, which the
// engine has to say it opened. Q2_009 refused it here and pointed at the
// package panel; since Q2_020 the sequence runs that panel's own owners —
// internal/q2pack, q2install and q2run — so Build & Run for a Quake II build
// profile packages, installs and waits for the engine's word in one record
// (see [playrun.Request.Packaged]). Nothing here packages anything itself.

// playRefuses reports a pipeline Build & Run does not run.
func (s *Server) playRefuses(pipelineID string) error {
	catalog, err := s.catalog()
	if err != nil {
		return nil // the sequence's own first stage reports a catalog it cannot read
	}
	entry, err := catalog.Lookup(pipelineID)
	if err != nil {
		return nil
	}
	switch documentFamily(entry.Profile) {
	case q3pack.FamilyQuake3:
		return errPlayIsNotForQuake3
	}
	return nil
}

// playFamily is the engine family of a build profile, as its document says.
// Empty when the catalog cannot say: the sequence's own stages report that.
func (s *Server) playFamily(pipelineID string) string {
	catalog, err := s.catalog()
	if err != nil {
		return ""
	}
	entry, err := catalog.Lookup(pipelineID)
	if err != nil {
		return ""
	}
	return documentFamily(entry.Profile)
}

// engineFits is the one answer to "can this engine play what this build
// profile makes" (`NEW_330`). It is the two documents' own word — the
// `game_profile.engine_family` each declares — and never a name: an engine
// called "Quake" that declares Quake III plays Quake III maps. A build profile
// that declares no game is not judged. The pipeline listing carries this answer
// for every engine and a run is refused on it, so the page compares nothing.
//
// Until NEW_330 only a Quake II build was held to it, and only in the page: a
// Quake 1 build offered Yamagi as "Ready to start" and a Quake III build
// offered vkQuake and Yamagi (`Q2_020` finding 2).
func engineFits(family string, document profile.Profile) error {
	if family == "" {
		return nil
	}
	name := document.Metadata().Name
	engineDocument, isEngine := document.(*profile.EngineProfile)
	if !isEngine {
		return fmt.Errorf("%s is not an engine profile", name)
	}
	if plays := documentFamily(engineDocument); plays != family {
		game := maturity.GameName(family)
		return fmt.Errorf("%s plays %s maps, and this build profile makes %s maps: choose an engine for %s, "+
			"or set one up under Profiles", name, maturity.GameName(plays), game, game)
	}
	return nil
}

// playFits refuses, before anything runs, an engine that cannot play what the
// build profile makes ([engineFits]), and for Quake II an action that does not
// load a map on this computer — the same test the package panel's engine list
// applies.
func (s *Server) playFits(family, engineID, actionID string) error {
	catalog, err := s.catalog()
	if err != nil {
		return err
	}
	entry, err := catalog.Lookup(engineID)
	if err != nil {
		return err
	}
	if err := engineFits(family, entry.Profile); err != nil {
		return err
	}
	if family != q2pack.FamilyQuake2 {
		return nil
	}
	document := entry.Profile.(*profile.EngineProfile)
	action, found := document.ActionByID(actionID)
	if !found || !loadsAMap(action) || action.SessionRole != profile.SessionClient {
		return fmt.Errorf("Build & Run plays a Quake II map on this computer: choose %s's action "+
			"that starts the game on a map", document.Meta.Name)
	}
	return nil
}

// playRequest is the request a body describes, with what this machine says
// about it: the engine's game folder, and the build profile's family.
func (s *Server) playRequest(body playRequestBody) (playrun.Request, int, error) {
	if err := s.playRefuses(body.Pipeline); err != nil {
		return playrun.Request{}, http.StatusConflict, err
	}
	family := s.playFamily(body.Pipeline)
	if body.BuildOnly {
		// Nothing is installed and no engine starts: an engine named anyway
		// would be a choice the run silently ignores, so it is refused.
		if body.Engine != "" || body.Action != "" || body.Listing != nil {
			return playrun.Request{}, http.StatusBadRequest,
				errors.New("a build without running names no engine, action or listing")
		}
		request := body.request("")
		request.Family = family
		return request, http.StatusOK, nil
	}
	if err := s.playFits(family, body.Engine, body.Action); err != nil {
		return playrun.Request{}, http.StatusBadRequest, err
	}
	gameRoot, err := s.gameRootFor(body.Engine)
	if err != nil {
		return playrun.Request{}, http.StatusBadRequest, err
	}
	request := body.request(gameRoot)
	request.Family = family
	return request, http.StatusOK, nil
}

func (s *Server) handlePlayStart(w http.ResponseWriter, r *http.Request) {
	var body playRequestBody
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.BuildRequestID != "" {
		s.startForBuildRequest(w, r, body)
		return
	}
	request, status, err := s.playRequest(body)
	if err != nil {
		writeError(w, status, err)
		return
	}
	service, err := s.playService()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	// A build by hand — Build, or Build & Run — of the revision Auto-build is
	// building now with the same build profile is the same build twice:
	// refused, pointing at the one running (NEW_363). The question and the
	// reservation that keeps the poller from starting it are one step under
	// Auto-build's own lock (NEW_365, autobuild.AdmitManual); a start that
	// fails gives the revision back.
	record, err := s.admitPlay(request, func() (*playrun.Record, error) { return service.Start(request) })
	if s.refusedDuplicate(w, err) {
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, playView(record, time.Now().UTC()))
}

// refusedDuplicate answers a start refused because the same build is already
// going: an automatic one (auto_build_running), or this exact run, started by
// another press or another tab (already_running, with its id so the page can
// follow it instead).
func (s *Server) refusedDuplicate(w http.ResponseWriter, err error) bool {
	var running *playrun.AlreadyRunningError
	switch {
	case errors.Is(err, autobuild.ErrSameRevisionRunning):
		writeJSON(w, http.StatusConflict, errorBody{Error: err.Error(), Code: "auto_build_running"})
	case errors.As(err, &running):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "already_running", "run_id": running.RunID})
	default:
		return false
	}
	return true
}

// admitPlay starts a hand-made run through Auto-build's admission, when this
// Companion has an auto-build service, and directly otherwise.
func (s *Server) admitPlay(request playrun.Request, start func() (*playrun.Record, error)) (*playrun.Record, error) {
	autobuilds, _ := s.autobuildService()
	if autobuilds == nil {
		return start()
	}
	var record *playrun.Record
	err := autobuilds.AdmitManual(autobuild.ManualBuild{
		AssetID: request.AssetID, RevisionID: request.RevisionID,
		PipelineID: request.PipelineID, Backend: s.backendAddress(),
	}, func() error {
		var startErr error
		record, startErr = start()
		return startErr
	})
	return record, err
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
	for index, record := range records {
		view := playView(record, now)
		if listing := s.hosting.view(record.ID); listing != nil {
			view["listing"] = listing
		}
		// The build's steps and their jobs, for the runs the Activity drawer
		// shows output for: every active one and the most recent few. Read
		// from the build's manifest, which the runner saves the moment a step
		// has a job; the rest of the history does not pay for a file read a
		// second.
		if record.BuildID != "" && (record.State.Active() || index < 3) {
			if steps := s.buildStepsView(record.BuildID); steps != nil {
				view["build_steps"] = steps
			}
		}
		// The running step's live line — last output, the tool's own progress,
		// CPU and memory — from the same sampler the Jobs page reads, so the
		// drawer and the full log cannot disagree (NEW_365).
		if record.State.Active() && record.CurrentJob != "" && s.jobs != nil {
			if running, err := s.jobs.Get(record.CurrentJob); err == nil {
				view["current_job_live"] = s.jobs.Live(running)
			}
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
	// A retry is a new hand-made run of the same request: admitted the same
	// way as a first one, so it cannot duplicate an automatic build either.
	previous, err := service.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, playStatus(err), err)
		return
	}
	record, err := s.admitPlay(previous.Request, func() (*playrun.Record, error) { return service.Retry(previous.ID) })
	if s.refusedDuplicate(w, err) {
		return
	}
	if err != nil {
		writeError(w, playStatus(err), err)
		return
	}
	writeJSON(w, http.StatusAccepted, playView(record, time.Now().UTC()))
}

// buildStepsView is a build's steps as the Activity drawer labels its output:
// the step, the program it ran and the job that holds what it printed.
func (s *Server) buildStepsView(buildID string) []map[string]any {
	dir, err := s.buildsDir()
	if err != nil {
		return nil
	}
	manifest, err := build.Find(dir, buildID)
	if err != nil {
		return nil
	}
	steps := make([]map[string]any, 0, len(manifest.Steps))
	for _, step := range manifest.Steps {
		row := map[string]any{
			"id": step.ID, "title": step.Title, "state": step.State,
			"job_id": step.JobID, "skipped": step.Skipped,
			"tool": step.Profile.Name,
		}
		// The manifest records a step's command when the step finishes; while
		// it runs, the job it started already knows what it is running.
		executable := ""
		if step.Command != nil {
			executable = step.Command.Executable
		} else if step.JobID != "" && s.jobs != nil {
			if running, err := s.jobs.Get(step.JobID); err == nil && running.Command != nil {
				executable = running.Command.Executable
			}
		}
		if executable != "" {
			row["program"] = programName(executable)
		}
		steps = append(steps, row)
	}
	return steps
}

// programName is an executable's name as a person calls it: `qbsp`, not
// `C:\Tools\ericw\bin\qbsp.exe`.
func programName(executable string) string {
	base := filepath.Base(strings.ReplaceAll(executable, "\\", "/"))
	return strings.TrimSuffix(base, filepath.Ext(base))
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
	if record.Request.BuildOnly {
		out["build_only"] = true
	}
	if record.Request.Family != "" {
		out["family"] = record.Request.Family
	}
	if record.Request.Packaged() {
		out["packaged"] = true
	}
	if record.Request.Trigger != "" {
		out["trigger"] = record.Request.Trigger
	}
	if record.Error != "" {
		out["error"] = record.Error
		// One sentence for the Activity card; the whole text stays in
		// "error" for Technical details and Jobs.
		out["error_summary"] = playrun.Summary(record.Error)
		if record.Summary != "" {
			out["error_summary"] = record.Summary
		}
		// The category a page says in its reader's language (failurewords.go).
		if record.FailureCode != "" {
			out["failure_code"] = record.FailureCode
		}
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
	// Which source supplied each WAD the build read: Technical details.
	if len(record.WADSources) > 0 {
		sources := make([]map[string]any, 0, len(record.WADSources))
		for _, source := range record.WADSources {
			row := map[string]any{"name": source.Name, "origin": source.Origin, "staged": source.Staged}
			if source.Revision > 0 {
				row["revision"] = source.Revision
			}
			if source.SHA256 != "" {
				row["sha256"], row["bytes"] = source.SHA256, source.Bytes
			}
			if source.Credit != "" {
				row["credit"], row["source"] = source.Credit, source.Source
			}
			if source.NotSentReason != "" {
				row["not_sent_reason"] = source.NotSentReason
			}
			sources = append(sources, row)
		}
		out["wad_sources"] = sources
	}
	if len(record.Notices) > 0 {
		notices := make([]map[string]any, 0, len(record.Notices))
		for _, notice := range record.Notices {
			notices = append(notices, map[string]any{
				"path": notice.Path, "sha256": notice.SHA256, "bytes": notice.Bytes,
			})
		}
		out["notices"], out["notices_dir"] = notices, record.NoticesDir
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
	if record.Package != nil {
		out["package"] = map[string]any{
			"id": record.Package.ID, "archive": record.Package.Archive, "sha256": record.Package.SHA256,
			"members": record.Package.Members, "complete": record.Package.Complete,
			"not_carried": record.Package.NotCarried, "reused": record.Package.Reused,
		}
	}
	if record.Installation != nil {
		out["installation"] = map[string]any{
			"id": record.Installation.ID, "mod_dir": record.Installation.ModDir,
			"fs_game": record.Installation.FSGame, "map_name": record.Installation.MapName,
			"replaced": record.Installation.Replaced,
		}
	}
	if record.Launch != nil {
		launch := map[string]any{
			"profile_id": record.Launch.ProfileID, "action_id": record.Launch.ActionID,
			"job_id": record.Launch.JobID,
			// The argv element by element, which is how a person checks it.
			"executable": record.Launch.Executable, "args": record.Launch.Args,
		}
		// A packaged run's engine said whether it opened the map, and from where.
		if record.Launch.MapLoad != "" {
			launch["map_load"], launch["loaded_from"] = record.Launch.MapLoad, record.Launch.LoadedFrom
			launch["source_verified"], launch["message"] = record.Launch.SourceVerified, record.Launch.Message
		}
		out["launch"] = launch
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
			writeAUBError(w, downloadErr)
			return
		}
		entry, err = cache.Publish(r.Context(), bundle, want)
		if err != nil {
			writeJSON(w, http.StatusConflict, errorBody{Error: err.Error(), Code: aubFailureCode(err)})
			return
		}
	}
	writeJSON(w, http.StatusOK, withOwnWADs(textureView(entry, cached), entry, query.Get("own_wads_dir")))
}

// withOwnWADs adds to a texture view what "use my own copy" could do for it:
// which WADs are missing only because AUB did not send their bytes, and —
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
		row := map[string]any{
			"order": wad.Order, "name": wad.Name, "status": wad.Status,
			"origin": wad.Origin, "included": wad.Included, "note": wad.Note,
			"files": files,
		}
		if wad.Revision > 0 {
			row["revision"] = wad.Revision
		}
		// The deployment's decision about an installed source, as it gave it:
		// the reason a withheld one was not sent, or the source, credit and
		// terms a carried one travels under.
		if verdict := wad.Redistribution; verdict != nil {
			row["redistribution"] = map[string]any{
				"decision": verdict.Decision, "reason": verdict.Reason,
				"source": verdict.Source, "credit": verdict.Credit, "terms": verdict.Terms,
				"primary_notice": verdict.PrimaryNotice, "notice_version": verdict.NoticeVersion,
				"notice_paths": verdict.NoticePaths,
			}
		}
		wads = append(wads, row)
	}
	notices := make([]map[string]any, 0, len(entry.Receipt.Notices))
	for _, notice := range entry.Receipt.Notices {
		notices = append(notices, map[string]any{
			"path": notice.Path, "sha256": notice.SHA256, "bytes": notice.Bytes, "sources": notice.Sources,
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
		// The third-party notice files kept with this bundle, by name and
		// digest. Never a path on this machine.
		"notices": notices,
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
	request, status, err := s.playRequest(body)
	if err != nil {
		writeError(w, status, err)
		return
	}
	if err = request.Normalize(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err = engine.CheckMapName(request.MapName); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err = engine.CheckModName(request.ModName); err != nil && !request.BuildOnly {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if request.Packaged() && !request.BuildOnly {
		s.playPlanPackaged(w, request)
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
	if request.BuildOnly {
		// Build only (NEW_363): nothing is written into a game folder and no
		// engine is started, so the plan names neither.
		delete(plan, "run")
		delete(plan, "writes")
		plan["build_only"] = true
	}
	if request.BuildOnly && request.Packaged() {
		// A Quake II compiler reads the game folders bound to it; the map's
		// account textures come with the run (Q2_015A). Nothing to look up.
		plan["textures"] = map[string]any{
			"cached": false, "known": false,
			"message": "This revision's account textures are downloaded when the build starts.",
		}
		writeJSON(w, http.StatusOK, plan)
		return
	}
	// Whether the compiler will be given the map's textures at all (NEW_310A,
	// the dark dm2 on Windows): a build profile whose stages read no texture
	// folder compiles a map whose WADs it cannot open, and the BSP carries no
	// textures — the level loads untextured. Said here, before anything runs.
	if reads, err := s.pipelineDeclaresRoot(request.PipelineID, profile.RootContent); err == nil {
		plan["reads_textures"] = reads
		if !reads {
			plan["textures_warning"] = "No stage of this build profile reads a texture folder, so the compiler " +
				"cannot open the map's WADs: the BSP will carry no textures and the level loads untextured (dark). " +
				"Give its compile stage's tool a texture folder (-wadpath) in Profiles, or choose a build profile that has one."
		}
	}

	// The engine's argv, resolved through the same resolver the executor uses.
	// Where a fact depends on a file that does not exist yet, it is marked.
	if !request.BuildOnly {
		plan["launch"] = s.previewLaunch(request)
	}
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
				"message": "This revision's textures will be downloaded and verified when the build starts.",
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
