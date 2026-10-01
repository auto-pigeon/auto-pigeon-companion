package playrun

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// execute is one run's whole life.
//
// Every stage follows the same shape: enter the state and SAVE, do the work,
// record what it produced and save again. The save before the work is the point
// — a record that were written only on success would be a record that never
// says what was happening when the power went out.
func (s *Service) execute(ctx context.Context, record *Record) {
	record.StartedAt = s.deps.Now()

	stages := []struct {
		state State
		run   func(context.Context, *Record) error
	}{
		{DownloadingMap, s.downloadMap},
		{DownloadingTextures, s.downloadTextures},
		{Converting, s.convert},
		{Compiling, s.compile},
		{Installing, s.install},
		{Launching, s.launch},
	}

	if record.Request.BuildOnly {
		// Download, textures, convert, compile — and stop: see Request.BuildOnly.
		stages = stages[:4]
	}

	for _, stage := range stages {
		if err := ctx.Err(); err != nil {
			s.cancelled(record)

			return
		}
		s.enter(record, stage.state)
		err := stage.run(ctx, record)
		switch {
		case err == nil:
			s.closeStage(record, "")
			s.save(record)
		case ctx.Err() != nil:
			s.cancelled(record)

			return
		default:
			s.failed(record, stage.state, err)

			return
		}
	}

	if record.Request.BuildOnly {
		record.State = Succeeded
		record.FinishedAt = s.deps.Now()
		s.save(record)
		s.deps.Logf("run %s: built in %s (build only)", record.ID, record.Elapsed(s.deps.Now()))

		return
	}

	// The engine started. That is the end of the automated sequence: the run
	// succeeded, and nothing waits for a game somebody is playing to exit.
	s.enter(record, Running)
	s.closeStage(record, "")
	record.State = Succeeded
	record.FinishedAt = s.deps.Now()
	s.save(record)
	s.deps.Logf("run %s: succeeded in %s", record.ID, record.Elapsed(s.deps.Now()))
}

// --- the stages -------------------------------------------------------------

func (s *Service) downloadMap(ctx context.Context, record *Record) error {
	result, err := s.deps.FetchMap(ctx, record.Request)
	if err != nil {
		return err
	}
	if strings.TrimSpace(result.Path) == "" {
		return errors.New("playrun: the map revision produced no file")
	}
	record.MapFile, record.MapSource = result.Path, result.Source
	s.detail(record, fmt.Sprintf("%s revision %s", record.Request.AssetID, record.Request.RevisionID))

	return nil
}

func (s *Service) downloadTextures(ctx context.Context, record *Record) error {
	result, err := s.deps.FetchBundle(ctx, record.Request)
	if err != nil {
		return err
	}
	if result.Ref == nil || strings.TrimSpace(result.ContentRoot) == "" {
		return errors.New("playrun: the texture bundle produced no verified content root")
	}
	record.Bundle, record.BundleRoot = result.Ref, result.ContentRoot

	// The pairing, checked here as well as at the two places that fetched the
	// halves. A current export must never be paired with a historical map
	// revision, and this is the one place that holds both numbers at once.
	if result.Ref.MapID != record.Request.AssetID {
		return fmt.Errorf("playrun: the texture bundle is for map %s and this run is for %s",
			result.Ref.MapID, record.Request.AssetID)
	}
	if result.Ref.Revision != record.Request.RevisionNumber {
		return fmt.Errorf("playrun: the texture bundle is revision %d and this run is revision %d",
			result.Ref.Revision, record.Request.RevisionNumber)
	}

	// The gate. Nothing starts the extractor or a compiler on a bundle AUB has
	// said it could not complete.
	if !result.Ref.CompilerReady {
		// The one exception, and only when the person asked for it in the
		// review: WADs AUB may not redistribute, taken from the folder they
		// named. See ownwads.go.
		names, onlyNotCarried := OwnWADsNeeded(result.Ref.CompilerRefusals)
		if onlyNotCarried && record.Request.OwnWADsDir != "" {
			root, own, err := s.completeWithOwnWADs(record, result.ContentRoot, names)
			if err != nil {
				record.Remedy = "Put your own copy of " + strings.Join(names, ", ") +
					" in the folder you named, or name the folder that has it."

				return fmt.Errorf("%w: %v", ErrNotCompilerReady, err)
			}
			record.BundleRoot, record.OwnWADs = root, own
			s.detail(record, fmt.Sprintf("%d file(s), bundle %s, completed with your own copy of %s",
				len(result.Ref.Files), result.Ref.Digest, strings.Join(names, ", ")))

			return nil
		}
		record.Remedy = compilerRefusalRemedy(result.Ref.CompilerRefusals)

		return fmt.Errorf("%w: %s", ErrNotCompilerReady,
			strings.Join(result.Ref.CompilerRefusals, "; "))
	}
	s.detail(record, fmt.Sprintf("%d file(s), bundle %s", len(result.Ref.Files), result.Ref.Digest))

	return nil
}

// compilerRefusalRemedy turns AUB's refusal codes into the sentence a person
// acts on. An unrecognised code is passed through rather than flattened: a
// deployment that adds one must not make the panel say nothing.
func compilerRefusalRemedy(refusals []string) string {
	for _, refusal := range refusals {
		code, subject, _ := strings.Cut(refusal, ":")
		subject = strings.TrimSpace(subject)
		switch strings.TrimSpace(code) {
		case "wad_bytes_not_carried":
			return "Auto-Pigeon cannot redistribute " + subject + " — it is the deployment's copy of " +
				"somebody else's game. Upload your own copy of that WAD, or declare one the map can use."
		case "texture_source_private":
			return "The texture source " + subject + " belongs to somebody else. Ask its owner to share it, " +
				"or declare a source you can read."
		case "texture_missing", "texture_source_missing":
			return "Nothing the map declares supplies " + subject + ". Add the WAD that holds it, or change the " +
				"faces that use it."
		case "wad_inventory_incomplete":
			return "Auto-Pigeon could not read the contents of " + subject + ", so it cannot say whether the " +
				"build would find its textures."
		}
	}
	if len(refusals) > 0 {
		return "Auto-Pigeon Backend refused this bundle: " + strings.Join(refusals, "; ")
	}

	return "This map's texture bundle is not complete enough to compile."
}

func (s *Service) convert(ctx context.Context, record *Record) error {
	result, err := s.deps.Convert(ctx, record.Request, record.MapFile)
	if err != nil {
		return err
	}
	record.Extractor = result.Extractor
	if result.Path != "" && result.Path != record.MapFile {
		record.ConvertedMap = result.Path
		record.Conversion = result.Conversion
	}
	switch {
	case result.Extractor == nil:
		s.detail(record, "the revision is already a Quake .map; the extractor was not needed")
	case result.Extractor.Verified:
		s.detail(record, "extractor "+result.Extractor.Version+", verified")
	default:
		s.detail(record, "extractor "+result.Extractor.Version+
			", a local developer override and NOT verified")
	}

	return nil
}

func (s *Service) compile(ctx context.Context, record *Record) error {
	source := record.ConvertedMap
	if source == "" {
		source = record.MapFile
	}
	// Each WAD at the path the map declares it at (declaredwads.go).
	placed, err := s.placeDeclaredWADs(record, source)
	if err != nil {
		return fmt.Errorf("playrun: placing the map's WADs where it declares them: %w", err)
	}
	if len(placed) > 0 {
		s.detail(record, "texture WADs also placed where the map declares them: "+strings.Join(placed, ", "))
	}
	request := build.Request{
		PipelineID: record.Request.PipelineID,
		Inputs:     map[string]string{},
		Roots:      map[string]string{profile.RootContent: record.BundleRoot},
		RootSources: map[string]build.RootSource{profile.RootContent: {
			Kind: build.RootFromTextureBundle, Bundle: record.Bundle, OwnFiles: ownBundleFiles(record.OwnWADs),
		}},
		Options: record.Request.Options,
		Strict:  record.Request.Strict,
		Label:   record.Request.Label,
	}
	if record.MapSource != nil {
		request.Sources = map[string]build.SourceRef{}
	}

	// Which declared input the map source is, asked of the pipeline rather than
	// assumed: a pipeline names its own inputs, and a coordinator that hard-coded
	// one would be a coordinator that works for exactly one pipeline document.
	name, err := s.mapInputName(record.Request.PipelineID)
	if err != nil {
		return err
	}
	request.Inputs[name] = source
	if record.MapSource != nil {
		request.Sources[name] = *record.MapSource
	}
	if record.Conversion != nil {
		request.Conversions = map[string]build.Conversion{name: *record.Conversion}
	}
	// The packages the saved map is bound to, read out of the APMap that was
	// fetched — not out of the `.map`, which has nowhere to carry them — and
	// resolved now rather than kept in the record: what is handed to the build
	// is a verified file on this machine, and a path does not survive a restart
	// as a fact.
	if s.deps.BoundPackages != nil {
		packages, err := s.deps.BoundPackages(ctx, record.Request, record.MapFile)
		if err != nil {
			return err
		}
		request.Packages = packages
	}

	announce := func(manifest *build.Manifest) {
		if manifest == nil {
			return
		}
		record.BuildID = manifest.BuildID
		record.CurrentStep, record.CurrentJob = runningStep(manifest)
		s.detail(record, describeBuild(manifest))
		s.save(record)
	}

	manifest, err := s.deps.Build(ctx, request, announce)
	if manifest != nil {
		record.BuildID = manifest.BuildID
		record.CurrentStep, record.CurrentJob = runningStep(manifest)
	}
	if err != nil {
		return err
	}
	record.CurrentStep, record.CurrentJob = "", ""
	s.detail(record, "build "+record.BuildID)

	// The compiler exits 0 with no textures at all (textures.go), so its own
	// words are read here rather than trusting the status.
	warning, err := checkTextures(manifest).verdict()
	if err != nil {
		record.Remedy = "The map names its WADs in worldspawn's `wad` key. Open the build in Jobs to see " +
			"which archive the compiler could not find, and make sure the map's texture bundle, or " +
			"your own copy of the WAD, carries it at that path."

		return err
	}
	if warning != "" {
		record.Warnings = append(record.Warnings, warning)
		s.detail(record, "build "+record.BuildID+" — "+warning)
	}

	return nil
}

// mapInputName is which declared input of the pipeline the map source is.
func (s *Service) mapInputName(pipelineID string) (string, error) {
	if s.deps.MapInputName == nil {
		return "", errors.New("playrun: nothing can say which input of this pipeline the map is")
	}

	return s.deps.MapInputName(pipelineID)
}

func runningStep(manifest *build.Manifest) (string, string) {
	for _, step := range manifest.Steps {
		if step.JobID != "" && !step.State.Terminal() {
			return step.ID, step.JobID
		}
	}

	return "", ""
}

func describeBuild(manifest *build.Manifest) string {
	step, jobID := runningStep(manifest)
	if step == "" {
		return "build " + manifest.BuildID
	}

	return fmt.Sprintf("build %s, %s step, job %s", manifest.BuildID, step, jobID)
}

func (s *Service) install(ctx context.Context, record *Record) error {
	plan, err := s.deps.PlanInstall(ctx, record)
	if err != nil {
		return err
	}
	result, err := s.deps.Install(ctx, record.Request, plan)
	if err != nil {
		return err
	}
	record.InstalledDir, record.Installed = result.Dir, result.Files
	s.detail(record, fmt.Sprintf("%d file(s) under %s", len(result.Files), result.Dir))

	return nil
}

func (s *Service) launch(ctx context.Context, record *Record) error {
	// The staged digests, again, immediately before the engine is started.
	// Between installing and launching is where a second Companion, a sync
	// client or a person with a file manager could have changed what is about
	// to be loaded.
	if err := s.deps.VerifyInstalled(ctx, record.Request, record.Installed); err != nil {
		return err
	}
	launched, err := s.deps.Launch(ctx, record.Request)
	if err != nil {
		return err
	}
	record.Launch = &launched
	s.detail(record, describeLaunch(launched))
	if s.deps.Launched != nil {
		s.deps.Launched(*record)
	}

	return nil
}

func describeLaunch(launch LaunchRecord) string {
	parts := append([]string{filepath.Base(launch.Executable)}, launch.Args...)

	return strings.Join(parts, " ")
}

// --- transitions ------------------------------------------------------------

// enter records the start of a stage and saves before the work begins.
func (s *Service) enter(record *Record, state State) {
	record.State = state
	record.Stages = append(record.Stages, Stage{State: state, StartedAt: s.deps.Now()})
	s.save(record)
	s.deps.Logf("run %s: %s", record.ID, state)
}

// detail attaches the one technical sentence behind the current stage.
func (s *Service) detail(record *Record, detail string) {
	if len(record.Stages) == 0 {
		return
	}
	record.Stages[len(record.Stages)-1].Detail = detail
}

// closeStage finishes the current stage, with its duration.
func (s *Service) closeStage(record *Record, cause string) {
	if len(record.Stages) == 0 {
		return
	}
	stage := &record.Stages[len(record.Stages)-1]
	if !stage.FinishedAt.IsZero() {
		return
	}
	stage.FinishedAt = s.deps.Now()
	stage.DurationMS = stage.FinishedAt.Sub(stage.StartedAt).Milliseconds()
	stage.Error = cause
}

func (s *Service) failed(record *Record, state State, cause error) {
	record.State = Failed
	record.FailedAt = state
	record.Error = cause.Error()
	if record.Remedy == "" {
		record.Remedy = remedyFor(state, cause)
	}
	record.FinishedAt = s.deps.Now()
	s.closeStage(record, cause.Error())
	// Nothing half-installed. A failure after the install is the one case where
	// files are already in somebody's game folder, and leaving them there would
	// mean a mod directory holding a level the engine was never started on.
	if state == Launching || state == Installing {
		if err := s.deps.Unstage(record.Request); err != nil {
			s.deps.Logf("run %s: cleaning up the install: %v", record.ID, err)
		} else {
			record.Installed, record.InstalledDir = nil, ""
		}
	}
	s.save(record)
	s.deps.Logf("run %s: failed at %s: %v", record.ID, state, cause)
}

func (s *Service) cancelled(record *Record) {
	record.State = Cancelled
	record.FinishedAt = s.deps.Now()
	s.closeStage(record, "cancelled")
	if len(record.Installed) > 0 {
		if err := s.deps.Unstage(record.Request); err != nil {
			s.deps.Logf("run %s: cleaning up the install: %v", record.ID, err)
		} else {
			record.Installed, record.InstalledDir = nil, ""
		}
	}
	s.save(record)
	s.deps.Logf("run %s: cancelled", record.ID)
}

// remedyFor is what the user can do about a failure at this stage.
func remedyFor(state State, cause error) string {
	if errors.Is(cause, ErrNotCompilerReady) {
		return "" // downloadTextures has already written the specific one.
	}
	switch state {
	case DownloadingMap:
		return "Check you are still signed in, and that this revision still exists on the server."
	case DownloadingTextures:
		return "Check you are still signed in. If the map has been saved since you chose its revision, " +
			"reload the map and choose the current one."
	case Converting:
		return convertRemedy(cause)
	case Compiling:
		return "Open the build in Jobs to read the compiler's output; the failing step names what it could not do."
	case Installing:
		return "Check the game folder is writable, and that nothing else owns the target mod directory."
	case Launching:
		return "Check the engine is still installed where this profile points, and that the game folder is intact."
	default:
		return ""
	}
}

func (s *Service) save(record *Record) {
	if err := s.store.Save(record); err != nil {
		s.deps.Logf("run %s: writing the record: %v", record.ID, err)
	}
}

// ownBundleFiles records the person's own WADs in the manifest's terms.
func ownBundleFiles(files []StagedFile) []build.BundleFile {
	if len(files) == 0 {
		return nil
	}
	out := make([]build.BundleFile, 0, len(files))
	for _, file := range files {
		out = append(out, build.BundleFile{Path: file.Path, Source: "own_copy", SHA256: file.SHA256, Bytes: file.Bytes})
	}

	return out
}
