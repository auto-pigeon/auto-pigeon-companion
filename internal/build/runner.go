package build

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// Options configures a Runner.
type Options struct {
	// Service is the executor. Every process a build starts goes through it.
	Service *job.Service
	// Dir is where build directories are created.
	Dir string
	// Bindings resolves a tool profile's local installation, for the manifest's
	// record of which downloads a build depended on. Nil means none are
	// recorded, which is true on a machine with no bindings.
	Bindings func(profileID string) (binding.LocalBinding, bool)
	// Platform is the machine. Zero means the running one.
	Platform profile.Platform
	// Companion is the build version, recorded in the manifest.
	Companion string
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// Logf receives one line per step. Nil discards them.
	Logf func(format string, args ...any)
	// Announce reports the manifest as it changes, before Run returns.
	//
	// It exists for the caller that is NOT blocking on Run. The GUI starts a
	// build and immediately has to answer three questions the return value
	// cannot: what is this build called, which stage is it on, and which job
	// would a Cancel button have to stop. A build whose id only exists once it
	// has finished is a build nothing can report the progress of.
	//
	// It is called from the goroutine running the build, so an implementation
	// must not block and must not keep the pointer: the manifest goes on
	// changing underneath. Copy what is needed. Nil discards them.
	Announce func(*Manifest)
}

// Runner runs pipelines.
type Runner struct {
	options  Options
	resolver *Resolver
}

// New builds a runner. It indexes the profile catalog once: a build that took a
// different answer to "what provides this capability" halfway through would be
// a build nobody could describe afterwards.
func New(options Options) (*Runner, error) {
	if options.Service == nil {
		return nil, errors.New("build: a runner needs a job service")
	}
	if strings.TrimSpace(options.Dir) == "" {
		return nil, errors.New("build: a runner needs a directory to build in")
	}
	resolver, err := NewResolver(options.Service.Catalog())
	if err != nil {
		return nil, err
	}
	if options.Platform.Zero() {
		options.Platform = profile.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Logf == nil {
		options.Logf = func(string, ...any) {}
	}
	if options.Announce == nil {
		options.Announce = func(*Manifest) {}
	}
	return &Runner{options: options, resolver: resolver}, nil
}

// Resolver exposes the capability index, for `build show` and for tests.
func (r *Runner) Resolver() *Resolver { return r.resolver }

// Request is one build.
type Request struct {
	// PipelineID names the pipeline profile.
	PipelineID string
	// Sources names where an input came from, for the inputs that came from
	// somewhere with an identity. Keyed by the same declared input name Inputs
	// is, and entirely optional: an input the user pointed at on their own disk
	// has no source, and its absence is recorded as absence rather than as an
	// empty object.
	//
	// It is supplied by the caller — internal/cli, which is what resolved the
	// asset — rather than discovered here, because this package must not learn
	// how to talk to a backend to record where a file came from.
	Sources map[string]SourceRef

	// Inputs maps a declared pipeline input to a file on this machine.
	//
	// An input is one FILE. A directory is a root — see Roots, and see
	// roots.go for why the two are different questions rather than one
	// question with a lenient validator.
	Inputs map[string]string

	// Roots maps a root role to a directory on this machine, for this build
	// only.
	//
	// It is how a verified AUB texture bundle becomes the `content_root` that
	// EricW's `qbsp` is given with `-wadpath`, without the bundle's directory
	// being written into the persistent tool binding. Every rule about which
	// roles may be supplied, and what a supplied role must be, is in roots.go
	// and is checked before anything runs.
	Roots map[string]string
	// RootSources names where a supplied root's contents came from, keyed by
	// the same role Roots is. Optional, in the way Sources is: a directory the
	// user pointed at has no identity, and its absence is recorded as absence.
	RootSources map[string]RootSource
	// Options overrides a step's options: step id -> option name -> value.
	// Checked against the resolved action's own [profile.OptionSpec], so an
	// override cannot be a value the tool's author did not allow.
	Options map[string]map[string]string
	// Label is a short human name for the build list.
	Label string
	// Strict fails the build when any step recorded an error-severity
	// diagnostic, even where the tool itself exited zero.
	Strict bool
	// Mirror receives each step's output as it is produced. Nil discards it.
	Mirror io.Writer
}

// Run executes a pipeline and returns its manifest.
//
// The manifest is returned for both outcomes. A failed build is one of the
// things a manifest is most useful for, and returning only an error would throw
// away the record of the two stages that did work.
func (r *Runner) Run(ctx context.Context, request Request) (*Manifest, error) {
	pipeline, entry, err := r.pipeline(request.PipelineID)
	if err != nil {
		return nil, err
	}
	steps, err := r.resolveSteps(pipeline)
	if err != nil {
		return nil, err
	}

	// Before a directory is created or a byte is downloaded: a build that
	// supplies a root no step declares is refused now rather than after two
	// stages have compiled.
	roots, err := checkRoots(request, steps)
	if err != nil {
		return nil, err
	}

	id, err := NewID(r.options.Now())
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(r.options.Dir, id)
	layout, err := createLayout(dir)
	if err != nil {
		return nil, err
	}

	manifest := &Manifest{
		SchemaVersion: SchemaVersion,
		BuildID:       id,
		Companion:     r.options.Companion,
		Platform:      r.options.Platform.String(),
		Label:         request.Label,
		Pipeline:      documentRef(entry),
		EngineFamily:  pipelineFamily(entry),
		State:         job.Running,
		Strict:        request.Strict,
		StartedAt:     r.options.Now(),
		Directory:     dir,
		Roots:         roots,
	}

	// Everything the pipeline declared, in order, before anything runs. A step
	// that is never reached is `skipped` with a reason rather than absent.
	for _, resolved := range steps {
		manifest.Steps = append(manifest.Steps, Step{
			ID:         resolved.Step.ID,
			Title:      resolved.Step.Title,
			Capability: resolved.Step.Capability,
			ActionID:   resolved.Action.ID,
			Skipped:    true,
			Error:      "the build stopped before this step",
		})
	}
	if err := manifest.Save(dir); err != nil {
		return nil, err
	}
	// The earliest point at which this build has an identity. Announced before
	// a single byte is staged, so a caller watching it never has a window in
	// which a build is running and has no name.
	r.options.Announce(manifest)

	wires, err := r.stageInputs(pipeline, request, layout, manifest)
	if err != nil {
		return r.fail(manifest, job.Failed, err)
	}
	manifest.Tools = r.tools(steps)
	if err := manifest.Save(dir); err != nil {
		return nil, err
	}

	for i, resolved := range steps {
		index := i
		// Recorded the moment the step has a job, rather than when it finishes.
		// Between those two points is where a build spends nearly all of its
		// time, and it is exactly the interval in which somebody wants to know
		// what is running and be able to stop it.
		started := func(running Step) {
			manifest.Steps[index] = running
			_ = manifest.Save(dir)
			r.options.Announce(manifest)
		}
		step, err := r.runStep(ctx, request, layout, resolved, wires, manifest, started)
		manifest.Steps[i] = step
		if saveErr := manifest.Save(dir); saveErr != nil {
			return nil, saveErr
		}
		r.options.Announce(manifest)
		if err != nil && ctx.Err() != nil {
			// Cancelled by whoever started it: recorded as exactly that, and
			// finished now, so nothing later mistakes it for a build the
			// Companion abandoned by crashing.
			return r.fail(manifest, job.Cancelled,
				fmt.Errorf("cancelled while the %s step was running", resolved.Step.ID))
		}
		if err != nil {
			// Published anyway, best effort. A failed compile still wrote the
			// point file that says where the leak is, and leaving it inside the
			// build's own scratch layout would mean the one artifact the user
			// needs is the one a failure hides.
			_ = r.publish(pipeline, layout, wires, manifest)
			return r.fail(manifest, step.State, fmt.Errorf("the %s step: %w", resolved.Step.ID, err))
		}
	}

	if err := r.publish(pipeline, layout, wires, manifest); err != nil {
		return r.fail(manifest, job.Failed, err)
	}
	if request.Strict {
		if offender := strictFindings(manifest); offender != "" {
			return r.fail(manifest, job.Failed, errors.New(offender))
		}
	}
	manifest.State = job.Succeeded
	return r.finish(manifest)
}

// Preview resolves every stage of a build and starts nothing.
//
// It is a *prediction*, and says so. Only the first stage's inputs exist yet, so
// the later stages are resolved against the paths this build would put their
// inputs at — which is a claim about what the executor will do rather than a
// fact about what it did. The claim is checked where it matters: [Runner.Run]
// previews each step through the executor immediately before submitting the
// identical request, and fails the build if the two argvs differ.
//
// Every check that can be made without a filesystem is made here: the
// capabilities resolve, the wiring type-checks, and every option goes through
// its own [profile.OptionSpec]. A pipeline that would fail on its third stage
// fails here instead, before the user has waited through two.
const (
	previewWorkspace = "<workspace>"
	previewBuild     = "<build>"
)

func (r *Runner) Preview(request Request) (*Manifest, error) {
	pipeline, entry, err := r.pipeline(request.PipelineID)
	if err != nil {
		return nil, err
	}
	steps, err := r.resolveSteps(pipeline)
	if err != nil {
		return nil, err
	}
	manifest := &Manifest{
		SchemaVersion: SchemaVersion,
		BuildID:       "preview",
		Companion:     r.options.Companion,
		Platform:      r.options.Platform.String(),
		Label:         request.Label,
		Pipeline:      documentRef(entry),
		EngineFamily:  pipelineFamily(entry),
		State:         job.Queued,
		StartedAt:     r.options.Now(),
	}
	manifest.Tools = r.tools(steps)
	// Checked in a preview as well as in a run. A preview whose job is to say
	// "this is what would happen" must refuse the same requests the run does,
	// or the user finds out at stage three.
	if manifest.Roots, err = checkRoots(request, steps); err != nil {
		return nil, err
	}

	// Where the user's files would be after the build copied them in, and where
	// the executor would stage them. Placeholders rather than a real directory:
	// a preview that created one would have started doing the build.
	wires := map[string]wire{}
	for _, input := range pipeline.Inputs {
		source, supplied := request.Inputs[input.Name]
		if !supplied || strings.TrimSpace(source) == "" {
			continue
		}
		wires["pipeline."+input.Name] = wire{Path: filepath.Base(source), Role: input.Role}
		manifest.Inputs = append(manifest.Inputs, FileRecord{Name: input.Name, Role: input.Role, Path: source})
	}

	for _, resolved := range steps {
		step := Step{
			ID:                resolved.Step.ID,
			Title:             resolved.Step.Title,
			Capability:        resolved.Step.Capability,
			ActionID:          resolved.Action.ID,
			State:             job.Queued,
			PreviewDifference: "predicted: resolved against where this build would stage each file, not against files that exist",
		}
		if catalogEntry, ok := r.resolver.Entry(resolved.Step.Capability); ok {
			step.Profile = documentRef(catalogEntry)
		}
		invocation, options, inputs, err := r.previewStep(request, resolved, wires)
		step.Options, step.Inputs = options, inputs
		if err != nil {
			step.Error = err.Error()
			manifest.Steps = append(manifest.Steps, step)
			return manifest, nil
		}
		// Shown rather than withheld, for the reason [job.Service.Preview]
		// shows a command a profile has not been granted: reading what a
		// pipeline would run is what somebody does *before* installing the
		// tool. The command is a shape, with `<tool-root>` where the
		// installation would be, and the step says so.
		if _, bound := r.binding(resolved.Profile.Meta.ID); !bound {
			step.Error = fmt.Sprintf("%s is not installed on this machine, so the %q root is not configured: "+
				"`companion acquire resolve --bind` a copy of it first",
				resolved.Profile.Meta.ID, profile.RootToolInstall)
		}
		step.Command = &job.CommandPreview{
			Executable: invocation.Command.Executable,
			Args:       invocation.Command.Args,
			WorkingDir: invocation.Command.WorkingDir,
			Digest:     invocation.Command.Digest(),
			Shell:      invocation.Command.String(),
		}
		// What this step hands on: the base name of each declared output, which
		// is what the next step will be staged with.
		for _, output := range resolved.Action.Outputs {
			path := invocation.Outputs[output.Name]
			wires[resolved.Step.ID+"."+output.Name] = wire{
				Path: filepath.Base(path), Role: output.Role, Optional: output.Optional,
			}
			step.Outputs = append(step.Outputs, FileRecord{
				Name: output.Name, Role: output.Role, Path: path, Optional: output.Optional,
			})
		}
		manifest.Steps = append(manifest.Steps, step)
	}
	for _, declared := range pipeline.Outputs {
		source := wires[declared.From]
		manifest.Outputs = append(manifest.Outputs, FileRecord{
			Name: declared.Name, Role: declared.Role, From: declared.From,
			Path: source.Path, Optional: declared.Optional,
		})
	}
	return manifest, nil
}

// previewStep resolves one step against predicted paths, through the same
// [profile.Resolve] the executor uses.
func (r *Runner) previewStep(request Request, resolved profile.ResolvedStep, wires map[string]wire) (profile.Invocation, map[string]string, []FileRecord, error) {
	options := map[string]string{}
	for name, value := range resolved.Step.Options {
		options[name] = value
	}
	for name, value := range request.Options[resolved.Step.ID] {
		options[name] = value
	}

	// The executor stages an input at `<workspace>/input/<group>/<base name>`,
	// where the group is the input's own name or, for a sidecar, the name of
	// the input it accompanies.
	groups := map[string]string{}
	for _, declared := range resolved.Action.Inputs {
		groups[declared.Name] = declared.StageGroup()
	}

	inputs := map[string]string{}
	var records []FileRecord
	for _, w := range resolved.Step.Inputs {
		source, produced := wires[w.From]
		required := false
		for _, declared := range resolved.Action.Inputs {
			if declared.Name == w.Name {
				required = declared.Required
			}
		}
		if !produced || source.Missing || source.Path == "" {
			if required {
				return profile.Invocation{}, options, records, fmt.Errorf(
					"needs %q, and nothing supplies %s", w.Name, w.From)
			}
			records = append(records, FileRecord{Name: w.Name, From: w.From, Missing: true, Optional: true})
			continue
		}
		staged := filepath.Join(previewWorkspace, "input", groups[w.Name], source.Path)
		inputs[w.Name] = staged
		records = append(records, FileRecord{Name: w.Name, Role: source.Role, Path: staged, From: w.From})
	}

	roots := map[string]string{
		profile.RootWorkspace: previewWorkspace,
		profile.RootBuild:     previewBuild,
	}
	executables := map[string]string{}
	if local, bound := r.binding(resolved.Profile.Meta.ID); bound {
		for role, path := range local.Roots {
			if role != profile.RootWorkspace {
				roots[role] = path
			}
		}
		for name, path := range local.Executables {
			executables[name] = path
		}
	}
	// The request's roots, over the binding's, exactly as [Runner.stepRequest]
	// merges them. Preview and execution therefore resolve to the same argv
	// after path substitution, which is the property comparePreview checks and
	// the reason a `-wadpath` shown in a review is the one that runs.
	for role, path := range request.Roots {
		if containsString(protectedRoots, role) {
			continue
		}
		roots[role] = path
	}
	if roots[profile.RootToolInstall] == "" {
		roots[profile.RootToolInstall] = "<tool-root>"
	}

	invocation, err := profile.Resolve(resolved.Profile, resolved.Action.ID, profile.Request{
		Platform:    r.options.Platform,
		Roots:       roots,
		Executables: executables,
		Inputs:      inputs,
		Options:     options,
	})
	return invocation, options, records, err
}

// wire is one file a step may read: where it is, and what kind of file it is.
type wire struct {
	Path     string
	Role     string
	Optional bool
	SHA256   string
	Size     int64
	// Missing marks a declared artifact the producing step did not produce.
	Missing bool
}

func (r *Runner) pipeline(id string) (*profile.PipelineProfile, job.CatalogEntry, error) {
	entry, err := r.options.Service.Catalog().Lookup(id)
	if err != nil {
		return nil, job.CatalogEntry{}, err
	}
	pipeline, isPipeline := entry.Profile.(*profile.PipelineProfile)
	if !isPipeline {
		return nil, job.CatalogEntry{}, fmt.Errorf("build: %s is a %s profile; a build runs a pipeline",
			id, entry.Profile.Metadata().Kind)
	}
	return pipeline, entry, nil
}

func (r *Runner) resolveSteps(pipeline *profile.PipelineProfile) ([]profile.ResolvedStep, error) {
	needed := make([]string, 0, len(pipeline.Steps))
	for _, step := range pipeline.Steps {
		needed = append(needed, step.Capability)
	}
	if err := r.resolver.CheckConflicts(needed); err != nil {
		return nil, err
	}
	steps, err := pipeline.Resolve(r.resolver)
	if err != nil {
		return nil, fmt.Errorf("build: %s does not resolve on this machine:\n%w\ninstalled capabilities: %s",
			pipeline.Meta.ID, err, strings.Join(r.resolver.Capabilities(), ", "))
	}
	return steps, nil
}

// stageInputs copies the user's files into the build directory.
//
// Copied for the reason the executor copies them: `vis` and `light` rewrite the
// file they are handed, and a build that let a tool reach the user's own BSP
// would be a build that could damage it. It also puts every file a step reads
// inside one root, which is what the `build_root` role is.
func (r *Runner) stageInputs(pipeline *profile.PipelineProfile, request Request, layout layout, manifest *Manifest) (map[string]wire, error) {
	wires := map[string]wire{}
	for _, name := range sortedKeys(request.Inputs) {
		declared := false
		for _, input := range pipeline.Inputs {
			if input.Name == name {
				declared = true
			}
		}
		if !declared {
			names := make([]string, 0, len(pipeline.Inputs))
			for _, input := range pipeline.Inputs {
				names = append(names, input.Name)
			}
			return nil, fmt.Errorf("the input %q was supplied, and %s declares only: %s",
				name, pipeline.Meta.ID, strings.Join(names, ", "))
		}
	}
	for _, input := range pipeline.Inputs {
		source, supplied := request.Inputs[input.Name]
		if !supplied || strings.TrimSpace(source) == "" {
			if input.Required {
				return nil, fmt.Errorf("the required input %q was not supplied", input.Name)
			}
			continue
		}
		destination := filepath.Join(layout.Input, input.Name, filepath.Base(source))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return nil, fmt.Errorf("creating %s: %w", filepath.Dir(destination), err)
		}
		if err := copyFile(source, destination); err != nil {
			return nil, fmt.Errorf("the input %q: %w", input.Name, err)
		}
		digest, size, err := digestFile(destination)
		if err != nil {
			return nil, err
		}
		wires["pipeline."+input.Name] = wire{Path: destination, Role: input.Role, SHA256: digest, Size: size}
		record := FileRecord{
			Name: input.Name, Role: input.Role, Path: destination, Size: size, SHA256: digest,
		}
		if source, named := request.Sources[input.Name]; named {
			provenance := source
			record.Source = &provenance
		}
		manifest.Inputs = append(manifest.Inputs, record)
	}
	return wires, nil
}

// tools records what each distinct tool profile in this build is, and which
// executables it will start.
func (r *Runner) tools(steps []profile.ResolvedStep) []ToolRecord {
	seen := map[string]bool{}
	var records []ToolRecord
	for _, resolved := range steps {
		id := resolved.Profile.Meta.ID
		if seen[id] {
			continue
		}
		seen[id] = true
		entry, _ := r.resolver.Entry(resolved.Step.Capability)
		record := ToolRecord{Profile: documentRef(entry), ToolVersion: resolved.Profile.ToolVersion}
		local, bound := r.binding(id)
		if bound {
			record.ResolvedVersion = local.ResolvedVersion
			record.Acquisition = local.Acquisition
			record.Installs = local.Installs
			for _, declared := range resolved.Profile.Executables {
				path, resolvedPath := local.Executables[declared.Name]
				if !resolvedPath {
					if root := local.Roots[profile.RootToolInstall]; root != "" {
						file := strings.ReplaceAll(declared.File, "{platform.exe_suffix}", r.options.Platform.ExeSuffix())
						path = filepath.Join(root, filepath.FromSlash(file))
					}
				}
				if path == "" {
					continue
				}
				executable := ExecutableRecord{Name: declared.Name, Path: path}
				if digest, size, err := digestFile(path); err == nil {
					executable.SHA256, executable.Size = digest, size
				} else {
					executable.Unreadable = err.Error()
				}
				record.Executables = append(record.Executables, executable)
			}
		}
		records = append(records, record)
	}
	return records
}

func (r *Runner) binding(profileID string) (binding.LocalBinding, bool) {
	if r.options.Bindings == nil {
		return binding.LocalBinding{}, false
	}
	return r.options.Bindings(profileID)
}

// stepRequest turns one resolved pipeline step into a job request.
func (r *Runner) stepRequest(request Request, layout layout, resolved profile.ResolvedStep, wires map[string]wire) (job.Request, map[string]string, []FileRecord, error) {
	options := map[string]string{}
	for name, value := range resolved.Step.Options {
		options[name] = value
	}
	for name, value := range request.Options[resolved.Step.ID] {
		options[name] = value
	}

	inputs := map[string]string{}
	var records []FileRecord
	for _, w := range resolved.Step.Inputs {
		source, produced := wires[w.From]
		required := false
		for _, declared := range resolved.Action.Inputs {
			if declared.Name == w.Name {
				required = declared.Required
			}
		}
		switch {
		case !produced || source.Missing:
			if required {
				return job.Request{}, nil, nil, fmt.Errorf(
					"needs %q, which %s did not produce; it is a required input of the %q action",
					w.Name, w.From, resolved.Action.ID)
			}
			records = append(records, FileRecord{Name: w.Name, From: w.From, Missing: true, Optional: true})
			continue
		default:
			inputs[w.Name] = source.Path
			records = append(records, FileRecord{
				Name: w.Name, Role: source.Role, Path: source.Path, From: w.From,
				Size: source.Size, SHA256: source.SHA256,
			})
		}
	}

	// The build directory, so that every file a step reads is inside a root the
	// executor was told about, plus whatever roots this build was given. The
	// build root is written LAST and is therefore not overridable — checkRoots
	// has already refused a request that tried, and this is the second half of
	// that rule, in the one place the map is assembled.
	roots := map[string]string{}
	for role, path := range request.Roots {
		if containsString(protectedRoots, role) {
			continue
		}
		roots[role] = path
	}
	roots[profile.RootBuild] = layout.Dir

	return job.Request{
		ProfileID: resolved.Profile.Meta.ID,
		ActionID:  resolved.Action.ID,
		Inputs:    inputs,
		Options:   options,
		Roots:     roots,
		Label:     request.Label,
	}, options, records, nil
}

// runStep submits one step and waits for it.
func (r *Runner) runStep(ctx context.Context, request Request, layout layout, resolved profile.ResolvedStep, wires map[string]wire, manifest *Manifest, started func(Step)) (Step, error) {
	step := Step{
		ID:         resolved.Step.ID,
		Title:      resolved.Step.Title,
		Capability: resolved.Step.Capability,
		ActionID:   resolved.Action.ID,
		State:      job.Failed,
	}
	if entry, ok := r.resolver.Entry(resolved.Step.Capability); ok {
		step.Profile = documentRef(entry)
	}

	jobRequest, options, inputs, err := r.stepRequest(request, layout, resolved, wires)
	step.Options, step.Inputs = options, inputs
	if err != nil {
		step.Error = err.Error()
		return step, err
	}

	// Previewed with the identical request, immediately before it is submitted.
	// A preview and a run get different job directories, so the two argvs are
	// compared after substituting one workspace path for the other — which is
	// what "the preview matches what ran, after path substitution" means, and
	// is a claim worth making only if something checks it.
	previewed, err := r.options.Service.Preview(jobRequest)
	if err != nil {
		step.Error = err.Error()
		return step, err
	}

	submitted, err := r.options.Service.SubmitWatched(jobRequest, request.Mirror)
	if err != nil {
		step.Error = err.Error()
		return step, err
	}
	step.JobID = submitted.ID
	step.State = job.Running
	step.Skipped = false
	step.Error = ""
	started(step)
	r.options.Logf("build %s: step %s is job %s", manifest.BuildID, resolved.Step.ID, submitted.ID)

	finished, err := r.options.Service.Wait(ctx, submitted.ID)
	if err != nil && ctx.Err() != nil {
		// The BUILD was cancelled. Wait only stops waiting; the compiler it
		// started would otherwise run to completion in the background — which
		// is what a live cancel showed (246I1.1): `vis` finished 1.5 s after
		// the person pressed Stop, under a dialog that said the compiler is
		// stopped. So the job is cancelled too, and waited for, briefly.
		stopped := r.stopJob(submitted.ID)
		step.State, step.Error = job.Cancelled, "cancelled while this step was running"
		if stopped != nil {
			step.Command, step.ExitCode = stopped.Command, stopped.ExitCode
			step.StartedAt, step.FinishedAt = stopped.StartedAt, stopped.FinishedAt
			step.DurationMS = stopped.Duration().Milliseconds()
			if stopped.State == job.Succeeded {
				// It finished before the signal arrived; say so rather than
				// claiming it was stopped.
				step.State, step.Error = job.Succeeded, ""
			}
		}

		return step, ctx.Err()
	}
	if err != nil {
		step.Error = err.Error()
		return step, err
	}
	step.State = finished.State
	step.Command = finished.Command
	step.ExitCode = finished.ExitCode
	step.TimedOut = finished.TimedOut
	step.Diagnostics = finished.Diagnostics
	step.Stdout, step.Stderr = finished.Stdout, finished.Stderr
	step.StartedAt, step.FinishedAt = finished.StartedAt, finished.FinishedAt
	step.DurationMS = finished.Duration().Milliseconds()
	step.Error = finished.Error

	step.PreviewMatched, step.PreviewDifference = comparePreview(previewed, finished)

	// Collected whatever the outcome: a failed compile still wrote the point
	// file that says where the leak is, and the manifest is where somebody
	// looks for it.
	outputs, collectErr := r.collect(layout, resolved, finished, wires)
	step.Outputs = outputs

	switch {
	case finished.State != job.Succeeded:
		message := finished.Error
		if message == "" {
			message = string(finished.State)
		}
		return step, errors.New(message)
	case collectErr != nil:
		step.Error = collectErr.Error()
		step.State = job.Failed
		return step, collectErr
	case !step.PreviewMatched:
		step.State = job.Failed
		step.Error = "the command that ran is not the command that was previewed: " + step.PreviewDifference
		return step, errors.New(step.Error)
	}
	return step, nil
}

// stopJob cancels a job the build no longer wants and waits, bounded, for it
// to reach a terminal state. It returns the job as it ended, or nil when that
// could not be read in time.
func (r *Runner) stopJob(id string) *job.Job {
	if _, err := r.options.Service.Cancel(id); err != nil {
		r.options.Logf("build: stopping job %s: %v", id, err)
	}
	ctx, done := context.WithTimeout(context.Background(), stopJobTimeout)
	defer done()
	stopped, err := r.options.Service.Wait(ctx, id)
	if err != nil {
		r.options.Logf("build: job %s did not stop within %s: %v", id, stopJobTimeout, err)

		return nil
	}

	return stopped
}

// stopJobTimeout bounds how long a cancelled build waits for its running job to
// end. The job service's own signal-then-kill escalation is well inside it.
const stopJobTimeout = 30 * time.Second

// collect copies a step's artifacts into the build directory and records them.
func (r *Runner) collect(layout layout, resolved profile.ResolvedStep, finished *job.Job, wires map[string]wire) ([]FileRecord, error) {
	produced := map[string]job.Artifact{}
	for _, artifact := range finished.Artifacts {
		produced[artifact.Name] = artifact
	}
	var records []FileRecord
	for _, declared := range resolved.Action.Outputs {
		reference := resolved.Step.ID + "." + declared.Name
		artifact, found := produced[declared.Name]
		if !found || artifact.Missing || artifact.Path == "" {
			wires[reference] = wire{Role: declared.Role, Optional: declared.Optional, Missing: true}
			records = append(records, FileRecord{
				Name: declared.Name, Role: declared.Role, Optional: declared.Optional, Missing: true,
			})
			continue
		}
		destination := filepath.Join(layout.Stage, resolved.Step.ID, declared.Name, filepath.Base(artifact.Path))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return records, fmt.Errorf("creating %s: %w", filepath.Dir(destination), err)
		}
		if err := copyFile(artifact.Path, destination); err != nil {
			return records, fmt.Errorf("collecting the output %q: %w", declared.Name, err)
		}
		wires[reference] = wire{
			Path: destination, Role: declared.Role, Optional: declared.Optional,
			SHA256: artifact.SHA256, Size: artifact.Size,
		}
		records = append(records, FileRecord{
			Name: declared.Name, Role: declared.Role, Path: destination,
			Size: artifact.Size, SHA256: artifact.SHA256, Optional: declared.Optional,
		})
	}
	return records, nil
}

// publish copies what the pipeline declared it produces into `output/`.
func (r *Runner) publish(pipeline *profile.PipelineProfile, layout layout, wires map[string]wire, manifest *Manifest) error {
	manifest.Outputs = nil
	var missing []string
	for _, declared := range pipeline.Outputs {
		source, produced := wires[declared.From]
		if !produced || source.Missing {
			manifest.Outputs = append(manifest.Outputs, FileRecord{
				Name: declared.Name, Role: declared.Role, From: declared.From,
				Optional: declared.Optional, Missing: true,
			})
			if !declared.Optional {
				missing = append(missing, declared.Name)
			}
			continue
		}
		destination := filepath.Join(layout.Output, declared.Name, filepath.Base(source.Path))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(destination), err)
		}
		if err := copyFile(source.Path, destination); err != nil {
			return fmt.Errorf("publishing %q: %w", declared.Name, err)
		}
		manifest.Outputs = append(manifest.Outputs, FileRecord{
			Name: declared.Name, Role: declared.Role, Path: destination, From: declared.From,
			Size: source.Size, SHA256: source.SHA256, Optional: declared.Optional,
		})
	}
	if len(missing) > 0 {
		return fmt.Errorf("the pipeline declares outputs that were not produced: %s", strings.Join(missing, ", "))
	}
	return nil
}

// strictFindings reports the first error-severity diagnostic, for `--strict`.
func strictFindings(manifest *Manifest) string {
	for _, step := range manifest.Steps {
		for _, diagnostic := range step.Diagnostics {
			if diagnostic.Severity != profile.SeverityError {
				continue
			}
			message := diagnostic.Message
			if message == "" {
				message = diagnostic.Raw
			}
			return fmt.Sprintf("--strict: the %s step reported %s: %s", step.ID, diagnostic.RuleID, message)
		}
	}
	return ""
}

func (r *Runner) fail(manifest *Manifest, state job.State, cause error) (*Manifest, error) {
	if state == job.Succeeded || state == "" {
		state = job.Failed
	}
	manifest.State = state
	manifest.Error = cause.Error()
	finished, saveErr := r.finish(manifest)
	if saveErr != nil {
		return finished, saveErr
	}
	return finished, cause
}

func (r *Runner) finish(manifest *Manifest) (*Manifest, error) {
	manifest.FinishedAt = r.options.Now()
	manifest.DurationMS = manifest.FinishedAt.Sub(manifest.StartedAt).Milliseconds()
	manifest.computeKey(r.generalizeRoots(manifest))
	if manifest.Directory != "" {
		if err := manifest.Save(manifest.Directory); err != nil {
			return manifest, err
		}
	}
	return manifest, nil
}

// generalizeRoots is every directory whose name is a fact about this machine
// rather than about the build: where the build put its files, where the
// executor put its jobs, and where each tool is installed.
//
// The tool roots matter more than they look. A managed download lands in a
// content-addressed directory whose name contains the artifact's digest, so
// leaving it in the argv would put the same digest in the key twice and, worse,
// would make the key depend on where the cache is. The executable's own digest
// is recorded separately, which is the part that means something.
func (r *Runner) generalizeRoots(manifest *Manifest) []string {
	roots := []string{manifest.Directory, r.options.Service.Store().Root()}
	// A supplied root is an absolute path on one machine. Its IDENTITY — the
	// bundle digest, the declaration order, each file's digest — is in the key;
	// where it happens to be cached is not.
	for _, root := range manifest.Roots {
		roots = append(roots, root.Path)
	}
	for _, tool := range manifest.Tools {
		if local, bound := r.binding(tool.Profile.ID); bound {
			for _, path := range local.Roots {
				roots = append(roots, path)
			}
		}
		for _, executable := range tool.Executables {
			roots = append(roots, filepath.Dir(executable.Path))
		}
	}
	return roots
}

func documentRef(entry job.CatalogEntry) DocumentRef {
	if entry.Profile == nil {
		return DocumentRef{}
	}
	meta := entry.Profile.Metadata()
	return DocumentRef{ID: meta.ID, Version: meta.Version, Name: meta.Name, Digest: entry.Digest, Trust: entry.Trust}
}

// comparePreview checks that what ran is what was shown, after substituting the
// job directory a preview was resolved against for the one the run got.
func comparePreview(previewed, finished *job.Job) (bool, string) {
	switch {
	case previewed == nil || previewed.Command == nil:
		return false, "nothing was previewed"
	case finished.Command == nil:
		return false, "the job recorded no command"
	}
	substitute := func(value string) string {
		if previewed.Workspace == "" || finished.Workspace == "" {
			return value
		}
		return strings.ReplaceAll(value, previewed.Workspace, finished.Workspace)
	}
	if got, want := substitute(previewed.Command.Executable), finished.Command.Executable; got != want {
		return false, fmt.Sprintf("previewed %s, ran %s", got, want)
	}
	if len(previewed.Command.Args) != len(finished.Command.Args) {
		return false, fmt.Sprintf("previewed %d arguments, ran %d", len(previewed.Command.Args), len(finished.Command.Args))
	}
	for i, arg := range previewed.Command.Args {
		if got, want := substitute(arg), finished.Command.Args[i]; got != want {
			return false, fmt.Sprintf("argument %d was previewed as %q and ran as %q", i, got, want)
		}
	}
	if got, want := substitute(previewed.Command.WorkingDir), finished.Command.WorkingDir; got != want {
		return false, fmt.Sprintf("previewed the working directory %s, ran in %s", got, want)
	}
	return true, ""
}

// NewID is a build id: a UTC timestamp and four random bytes, sortable by when
// it started and unique without a counter anybody has to keep.
func NewID(now time.Time) (string, error) {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("build: generating an id: %w", err)
	}
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(suffix[:]), nil
}

// idPattern is what NewID produces, for listing a directory of builds.
func validID(id string) bool {
	if len(id) != len("20060102T150405Z")+1+8 {
		return false
	}
	if id[len("20060102T150405Z")] != '-' {
		return false
	}
	if _, err := time.Parse("20060102T150405Z", id[:len("20060102T150405Z")]); err != nil {
		return false
	}
	_, err := hex.DecodeString(id[len("20060102T150405Z")+1:])
	return err == nil
}

// layout is one build's directory tree.
type layout struct {
	Dir    string
	Input  string
	Stage  string
	Output string
}

func layoutFor(dir string) layout {
	return layout{
		Dir:    dir,
		Input:  filepath.Join(dir, "input"),
		Stage:  filepath.Join(dir, "stage"),
		Output: filepath.Join(dir, "output"),
	}
}

func createLayout(dir string) (layout, error) {
	l := layoutFor(dir)
	for _, path := range []string{l.Dir, l.Input, l.Stage, l.Output} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return l, fmt.Errorf("build: creating %s: %w", path, err)
		}
	}
	return l, nil
}

// List returns every build in a directory, newest first.
func List(dir string) ([]*Manifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("build: reading %s: %w", dir, err)
	}
	var out []*Manifest
	for _, entry := range entries {
		if !entry.IsDir() || !validID(entry.Name()) {
			continue
		}
		manifest, err := LoadManifest(filepath.Join(dir, entry.Name(), ManifestFileName))
		if err != nil {
			continue
		}
		out = append(out, manifest)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BuildID > out[j].BuildID })
	return out, nil
}

// Find returns one build by id.
func Find(dir, id string) (*Manifest, error) {
	if !validID(id) {
		return nil, fmt.Errorf("build: %q is not a build id", id)
	}
	return LoadManifest(filepath.Join(dir, id, ManifestFileName))
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("reading %s: %w", source, err)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", source)
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("writing %s: %w", destination, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copying %s: %w", source, err)
	}
	return out.Close()
}

func digestFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("build: reading %s: %w", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, fmt.Errorf("build: reading %s: %w", path, err)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), size, nil
}

// pipelineFamily reads the engine family off a pipeline catalog entry.
//
// A separate function rather than an inline type assertion because it is done
// at both manifest-construction sites, and two spellings of the same assertion
// is one place for them to diverge.
func pipelineFamily(entry job.CatalogEntry) string {
	// A pipeline may legitimately declare no game profile — one that operates on
	// a file format rather than on a project has none — so the nil is a normal
	// answer and not a missing case.
	if pipeline, ok := entry.Profile.(*profile.PipelineProfile); ok && pipeline.GameProfile != nil {
		return pipeline.GameProfile.EngineFamily
	}
	return ""
}
